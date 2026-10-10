package mcpclient

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var ErrInvalidConfig = errors.New("invalid MCP server configuration")
var ErrToolNotFound = errors.New("MCP tool not found")

// How an HTTP server wants the stored credential presented. Bearer sends the credential as the token itself;
// Basic expects "user:secret" (for example an Atlassian e-mail and API token) and sends it base64-encoded.
const (
	AuthBearer = "bearer"
	AuthBasic  = "basic"
)

type Config struct {
	Transport     string
	Command       string
	Args          []string
	URL           string
	WorkspacePath string
	TokenEnvVar   string
	// AuthScheme is AuthBearer (also the meaning of "") or AuthBasic; only HTTP servers use it.
	AuthScheme string
	Token      string
}

type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Schema      json.RawMessage `json:"schema"`
}

type Result struct {
	Text    string `json:"text"`
	IsError bool   `json:"isError"`
}

func validate(cfg Config) error {
	if cfg.WorkspacePath == "" || !filepath.IsAbs(cfg.WorkspacePath) {
		return ErrInvalidConfig
	}
	info, err := os.Stat(cfg.WorkspacePath)
	if err != nil || !info.IsDir() {
		return ErrInvalidConfig
	}
	switch cfg.Transport {
	case "stdio":
		if !filepath.IsAbs(cfg.Command) || cfg.URL != "" || len(cfg.Args) > 32 || (cfg.AuthScheme != "" && cfg.AuthScheme != AuthBearer) {
			return ErrInvalidConfig
		}
		if cfg.Token != "" && cfg.TokenEnvVar == "" {
			return ErrInvalidConfig
		}
		if cfg.TokenEnvVar != "" && !validTokenEnvVar(cfg.TokenEnvVar) {
			return ErrInvalidConfig
		}
		info, err := os.Stat(cfg.Command)
		if err != nil || !info.Mode().IsRegular() {
			return ErrInvalidConfig
		}
		for _, arg := range cfg.Args {
			if len(arg) > 4096 || strings.ContainsRune(arg, '\x00') {
				return ErrInvalidConfig
			}
			if cfg.Token != "" && strings.Contains(arg, cfg.Token) {
				return ErrInvalidConfig
			}
		}
	case "http":
		if cfg.Command != "" || len(cfg.Args) != 0 || cfg.TokenEnvVar != "" {
			return ErrInvalidConfig
		}
		switch cfg.AuthScheme {
		case "", AuthBearer:
		case AuthBasic:
			if !validBasicCredential(cfg.Token) {
				return ErrInvalidConfig
			}
		default:
			return ErrInvalidConfig
		}
		u, err := url.Parse(cfg.URL)
		if err != nil || u.Hostname() == "" || u.User != nil || u.Fragment != "" || u.RawQuery != "" || u.ForceQuery {
			return ErrInvalidConfig
		}
		if u.Scheme != "https" {
			if u.Scheme != "http" || !isLoopback(u.Hostname()) {
				return ErrInvalidConfig
			}
		}
	default:
		return ErrInvalidConfig
	}
	return nil
}

func Validate(cfg Config) error { return validate(cfg) }

// validBasicCredential accepts "user:secret" with both parts present and nothing a header cannot carry.
func validBasicCredential(credential string) bool {
	user, secret, found := strings.Cut(credential, ":")
	if !found || user == "" || secret == "" {
		return false
	}
	for _, r := range credential {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

// authorization is the Authorization header for the stored credential; empty when there is none.
func authorization(cfg Config) string {
	if cfg.Token == "" {
		return ""
	}
	if cfg.AuthScheme == AuthBasic {
		return "Basic " + base64.StdEncoding.EncodeToString([]byte(cfg.Token))
	}
	return "Bearer " + cfg.Token
}

func isLoopback(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func validTokenEnvVar(name string) bool {
	if len(name) == 0 || len(name) > 128 {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		if c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c == '_' || i > 0 && c >= '0' && c <= '9' {
			continue
		}
		return false
	}
	if strings.EqualFold(name, "NO_COLOR") {
		return false
	}
	for inherited := range stdioAllowedEnv {
		if strings.EqualFold(name, inherited) {
			return false
		}
	}
	return true
}

var stdioAllowedEnv = map[string]bool{"PATH": true, "HOME": true, "USERPROFILE": true, "TMPDIR": true, "TEMP": true, "TMP": true, "LANG": true, "LC_ALL": true, "LC_CTYPE": true, "SystemRoot": true, "WINDIR": true, "ComSpec": true, "PATHEXT": true, "XDG_CONFIG_HOME": true, "XDG_DATA_HOME": true, "XDG_CACHE_HOME": true, "XDG_STATE_HOME": true}

func stdioEnvFrom(entries []string) []string {
	env := make([]string, 0, len(stdioAllowedEnv)+1)
	for _, entry := range entries {
		key, _, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		ascii := true
		for i := 0; i < len(key); i++ {
			c := key[i]
			if c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_' {
				continue
			}
			ascii = false
			break
		}
		if !ascii {
			continue
		}
		for allowed := range stdioAllowedEnv {
			if !strings.EqualFold(key, allowed) {
				continue
			}
			env = append(env, entry)
			break
		}
	}
	return append(env, "NO_COLOR=1")
}

func stdioEnv() []string { return stdioEnvFrom(os.Environ()) }

type authTransport struct {
	base          http.RoundTripper
	authorization string
}

func (t authTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.Header = clone.Header.Clone()
	clone.Header.Set("Authorization", t.authorization)
	return t.base.RoundTrip(clone)
}

func connect(ctx context.Context, cfg Config) (*mcp.ClientSession, error) {
	if err := validate(cfg); err != nil {
		return nil, err
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "Harflex", Version: "0.2.2"}, nil)
	var transport mcp.Transport
	if cfg.Transport == "stdio" {
		cmd := exec.CommandContext(ctx, cfg.Command, cfg.Args...)
		cmd.Dir = cfg.WorkspacePath
		cmd.Env = stdioEnv()
		if cfg.TokenEnvVar != "" && cfg.Token != "" {
			cmd.Env = append(cmd.Env, cfg.TokenEnvVar+"="+cfg.Token)
		}
		transport = &mcp.CommandTransport{Command: cmd, TerminateDuration: 2 * time.Second}
	} else {
		httpClient := &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		if header := authorization(cfg); header != "" {
			httpClient.Transport = authTransport{base: http.DefaultTransport, authorization: header}
		}
		transport = &mcp.StreamableClientTransport{Endpoint: cfg.URL, HTTPClient: httpClient, MaxRetries: -1, DisableStandaloneSSE: true}
	}
	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		return nil, fmt.Errorf("connect MCP server: %w", err)
	}
	return session, nil
}

func Discover(ctx context.Context, cfg Config) ([]Tool, error) {
	callCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	session, err := connect(callCtx, cfg)
	if err != nil {
		return nil, err
	}
	defer session.Close()
	result := make([]Tool, 0)
	cursor := ""
	for page := 0; page < 10; page++ {
		response, err := session.ListTools(callCtx, &mcp.ListToolsParams{Cursor: cursor})
		if err != nil {
			return nil, fmt.Errorf("list MCP tools: %w", err)
		}
		for _, tool := range response.Tools {
			if tool == nil || tool.Name == "" || len(tool.Name) > 128 {
				continue
			}
			if len(result) >= 256 {
				return nil, errors.New("MCP tool list exceeds limit")
			}
			schema, err := json.Marshal(tool.InputSchema)
			if err != nil || len(schema) > 64*1024 {
				continue
			}
			result = append(result, Tool{Name: tool.Name, Description: tool.Description, Schema: schema})
		}
		if response.NextCursor == "" {
			return result, nil
		}
		cursor = response.NextCursor
	}
	return nil, errors.New("MCP tool list exceeded page limit")
}

func Call(ctx context.Context, cfg Config, name string, args json.RawMessage) (Result, error) {
	if name == "" || len(name) > 128 || len(args) > 1024*1024 {
		return Result{}, ErrInvalidConfig
	}
	var arguments map[string]any
	if err := json.Unmarshal(args, &arguments); err != nil || arguments == nil {
		return Result{}, ErrInvalidConfig
	}
	callCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	session, err := connect(callCtx, cfg)
	if err != nil {
		return Result{}, err
	}
	defer session.Close()
	response, err := session.CallTool(callCtx, &mcp.CallToolParams{Name: name, Arguments: arguments})
	if err != nil {
		return Result{}, fmt.Errorf("call MCP tool: %w", err)
	}
	var output strings.Builder
	for _, content := range response.Content {
		if text, ok := content.(*mcp.TextContent); ok {
			if output.Len()+len(text.Text) > 1024*1024 {
				return Result{}, errors.New("MCP result exceeds limit")
			}
			if output.Len() > 0 {
				output.WriteByte('\n')
			}
			output.WriteString(text.Text)
		}
	}
	if output.Len() == 0 && response.StructuredContent != nil {
		encoded, err := json.Marshal(response.StructuredContent)
		if err != nil || len(encoded) > 1024*1024 {
			return Result{}, errors.New("MCP structured result exceeds limit")
		}
		output.Write(encoded)
	}
	return Result{Text: output.String(), IsError: response.IsError}, nil
}
