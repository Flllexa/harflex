// Package mcpserver serves Harflex's own tools to the CLI agents (Codex, Claude Code) over MCP's streamable HTTP
// transport. It listens only on the loopback interface; each conversation gets its own bearer token, bound to the
// tools of its project, and the token travels to the CLI through its environment.
package mcpserver

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/persioflexa/harflex/internal/agentcore"
	"github.com/persioflexa/harflex/internal/tools"
)

const (
	protocolVersion = "2025-06-18"
	maxRequestBytes = 4 << 20
	callTimeout     = 2 * time.Minute
)

type Server struct {
	mu       sync.RWMutex
	bindings map[string][]tools.Tool
	listener net.Listener
	http     *http.Server
	url      string
}

// Start listens on a free loopback port.
func Start() (*Server, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	s := &Server{bindings: map[string][]tools.Tool{}, listener: listener, url: "http://" + listener.Addr().String() + "/mcp"}
	mux := http.NewServeMux()
	mux.HandleFunc("/mcp", s.handle)
	s.http = &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = s.http.Serve(listener) }()
	return s, nil
}

// URL is the endpoint the CLIs are given.
func (s *Server) URL() string { return s.url }

// Register binds a new token to items; release forgets it.
func (s *Server) Register(items []tools.Tool) (token string, release func()) {
	var raw [32]byte
	_, _ = rand.Read(raw[:])
	token = hex.EncodeToString(raw[:])
	s.mu.Lock()
	s.bindings[token] = items
	s.mu.Unlock()
	return token, func() {
		s.mu.Lock()
		delete(s.bindings, token)
		s.mu.Unlock()
	}
}

func (s *Server) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return s.http.Shutdown(ctx)
}

func (s *Server) lookup(r *http.Request) ([]tools.Tool, bool) {
	header := r.Header.Get("Authorization")
	token, ok := strings.CutPrefix(header, "Bearer ")
	if !ok || token == "" {
		return nil, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for known, items := range s.bindings {
		if subtle.ConstantTimeCompare([]byte(known), []byte(token)) == 1 {
			return items, true
		}
	}
	return nil, false
}

// A page in a browser must not reach the server, even through a rebinding DNS name.
func loopbackRequest(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.Host)
	if err != nil {
		host = r.Host
	}
	if host != "127.0.0.1" && host != "localhost" {
		return false
	}
	return r.Header.Get("Origin") == ""
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	if !loopbackRequest(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	items, ok := s.lookup(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	switch r.Method {
	case http.MethodPost:
	case http.MethodDelete:
		w.WriteHeader(http.StatusOK)
		return
	default:
		// No server-initiated stream: every response comes back on the POST.
		w.Header().Set("Allow", "POST, DELETE")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxRequestBytes+1))
	if err != nil || len(body) > maxRequestBytes {
		http.Error(w, "request too large", http.StatusRequestEntityTooLarge)
		return
	}
	var request rpcRequest
	if err := json.Unmarshal(body, &request); err != nil || request.JSONRPC != "2.0" || request.Method == "" {
		writeJSON(w, map[string]any{"jsonrpc": "2.0", "id": nil, "error": rpcError{Code: -32700, Message: "invalid JSON-RPC request"}})
		return
	}
	// Notifications and responses expect no answer.
	if len(request.ID) == 0 || string(request.ID) == "null" {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	result, failure := s.dispatch(r.Context(), items, request)
	response := map[string]any{"jsonrpc": "2.0", "id": request.ID}
	if failure != nil {
		response["error"] = failure
	} else {
		response["result"] = result
	}
	writeJSON(w, response)
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}

func (s *Server) dispatch(ctx context.Context, items []tools.Tool, request rpcRequest) (any, *rpcError) {
	switch request.Method {
	case "initialize":
		var params struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(request.Params, &params)
		version := protocolVersion
		if params.ProtocolVersion != "" {
			version = params.ProtocolVersion
		}
		return map[string]any{
			"protocolVersion": version,
			"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
			"serverInfo":      map[string]string{"name": "harflex", "version": "1"},
			"instructions":    "Harflex platform tools for this conversation's project.",
		}, nil
	case "ping":
		return map[string]any{}, nil
	case "tools/list":
		list := make([]map[string]any, 0, len(items))
		for _, item := range items {
			spec := item.Spec()
			list = append(list, map[string]any{"name": spec.Name, "description": spec.Description, "inputSchema": spec.Schema})
		}
		return map[string]any{"tools": list}, nil
	case "tools/call":
		var params struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(request.Params, &params); err != nil || params.Name == "" {
			return nil, &rpcError{Code: -32602, Message: "invalid tool call"}
		}
		for _, item := range items {
			if item.Spec().Name == params.Name {
				return call(ctx, item, params.Arguments), nil
			}
		}
		return nil, &rpcError{Code: -32602, Message: "unknown tool"}
	default:
		return nil, &rpcError{Code: -32601, Message: "method not found"}
	}
}

func call(parent context.Context, item tools.Tool, args json.RawMessage) map[string]any {
	if len(args) == 0 || string(args) == "null" {
		args = json.RawMessage(`{}`)
	}
	ctx, cancel := context.WithTimeout(parent, callTimeout)
	defer cancel()
	result, err := item.Execute(ctx, args, nil)
	if err != nil {
		message := "the tool failed"
		var failure *agentcore.ToolFailure
		if errors.As(err, &failure) {
			message = failure.Code + ": " + failure.Message
		}
		return map[string]any{"content": []map[string]string{{"type": "text", "text": message}}, "isError": true}
	}
	return map[string]any{"content": []map[string]string{{"type": "text", "text": string(result.Content)}}, "isError": false}
}
