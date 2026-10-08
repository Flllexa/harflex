package openai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/persioflexa/harflex/internal/agentcore"
)

func collect(t *testing.T, ctx context.Context, c *Client, req agentcore.ChatRequest) ([]agentcore.StreamEvent, error) {
	t.Helper()
	events, errs := c.Stream(ctx, req)
	var out []agentcore.StreamEvent
	var terminal error
	timer := time.NewTimer(3 * time.Second)
	defer timer.Stop()
	for events != nil || errs != nil {
		select {
		case e, ok := <-events:
			if !ok {
				events = nil
			} else {
				out = append(out, e)
			}
		case err, ok := <-errs:
			if !ok {
				errs = nil
			} else {
				if terminal != nil {
					t.Fatal("multiple terminal errors")
				}
				terminal = err
			}
		case <-timer.C:
			t.Fatal("stream channels did not close")
		}
	}
	return out, terminal
}

func serverClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	s := httptest.NewServer(handler)
	t.Cleanup(s.Close)
	c, err := New(Config{ID: "test", BaseURL: s.URL, APIKey: func(context.Context) (string, error) { return "test-secret", nil }})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestEmptyKeyOmitsAuthorizationHeader(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, present := r.Header["Authorization"]; present {
			t.Error("empty local key produced an Authorization header")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(server.Close)
	client, err := New(Config{ID: "local", BaseURL: server.URL + "/v1", APIKey: func(context.Context) (string, error) { return "", nil }})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := collect(t, t.Context(), client, agentcore.ChatRequest{Model: "local"}); err != nil {
		t.Fatal(err)
	}
}

func TestOutputLimitUsesProviderSpecificWireField(t *testing.T) {
	for _, tc := range []struct{ provider, field string }{{"openai", "max_completion_tokens"}, {"openrouter", "max_completion_tokens"}, {"ollama", "max_tokens"}, {"lm_studio", "max_tokens"}} {
		t.Run(tc.provider, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if body[tc.field] != float64(321) {
					t.Errorf("output cap body = %v", body)
				}
				other := "max_tokens"
				if tc.field == other {
					other = "max_completion_tokens"
				}
				if _, ok := body[other]; ok {
					t.Errorf("unexpected %s: %v", other, body)
				}
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, "data: [DONE]\n\n")
			}))
			t.Cleanup(server.Close)
			client, err := New(Config{ID: "limited", ProviderType: tc.provider, BaseURL: server.URL, APIKey: func(context.Context) (string, error) { return "", nil }})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := collect(t, t.Context(), client, agentcore.ChatRequest{Model: "selected", MaxOutputTokens: 321}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestGenericOutputLimitFailsClosedBeforeHTTP(t *testing.T) {
	var called atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called.Store(true) }))
	t.Cleanup(server.Close)
	client, err := New(Config{ID: "generic", ProviderType: "generic", BaseURL: server.URL, APIKey: func(context.Context) (string, error) { return "", nil }})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := collect(t, t.Context(), client, agentcore.ChatRequest{Model: "manual", MaxOutputTokens: 321}); err == nil {
		t.Fatal("generic cap accepted")
	}
	if called.Load() {
		t.Fatal("generic request reached network")
	}
}

func TestRequestAndText(t *testing.T) {
	type contextKey struct{}
	ctx := context.WithValue(context.Background(), contextKey{}, "present")
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/v1/chat/completions" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-secret" || r.Header.Get("Content-Type") != "application/json" || r.Header.Get("Accept") != "text/event-stream" {
			t.Error("wrong headers")
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		if body["model"] != "model" || body["stream"] != true {
			t.Errorf("body = %v", body)
		}
		options, ok := body["stream_options"].(map[string]any)
		if !ok || options["include_usage"] != true {
			t.Errorf("stream options must request usage: %v", body["stream_options"])
		}
		msgs := body["messages"].([]any)
		tool := msgs[2].(map[string]any)
		if tool["tool_call_id"] != "call" || tool["role"] != "tool" || tool["content"] != "result" {
			t.Errorf("tool message = %v", tool)
		}
		assistant := msgs[1].(map[string]any)
		call := assistant["tool_calls"].([]any)[0].(map[string]any)
		if call["id"] != "call" || call["type"] != "function" || call["function"].(map[string]any)["arguments"] != "{\"q\":1}" {
			t.Errorf("call = %v", call)
		}
		spec := body["tools"].([]any)[0].(map[string]any)
		fn := spec["function"].(map[string]any)
		if spec["type"] != "function" || fn["name"] != "lookup" || fn["description"] != "Search" || fn["parameters"].(map[string]any)["type"] != "object" {
			t.Errorf("spec = %v", spec)
		}
		w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hello\"}}]}\n\ndata: {\"choices\":[{\"delta\":{\"content\":\" world\"}}]}\n\ndata: [DONE]\n\n")
	}))
	defer s.Close()
	c, err := New(Config{ID: "provider", BaseURL: s.URL + "/v1/", APIKey: func(got context.Context) (string, error) {
		if got.Value(contextKey{}) != "present" {
			t.Error("context missing")
		}
		return "test-secret", nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	if c.ID() != "provider" || c.Capabilities() != (agentcore.Capabilities{Streaming: true, ToolCalls: true}) {
		t.Fatal("wrong provider identity/capabilities")
	}
	events, err := collect(t, ctx, c, agentcore.ChatRequest{Model: "model", Messages: []agentcore.Message{{Role: agentcore.RoleUser, Content: "question"}, {Role: agentcore.RoleAssistant, ToolCalls: []agentcore.ToolCall{{ID: "call", Name: "lookup", Arguments: json.RawMessage(`{"q":1}`)}}}, {Role: agentcore.RoleTool, ToolCallID: "call", Content: "result"}}, Tools: []agentcore.ToolSpec{{Name: "lookup", Description: "Search", Schema: json.RawMessage(`{"type":"object"}`)}}})
	if err != nil || len(events) != 2 || events[0].Type != "text_delta" || events[0].Delta != "hello" || events[1].Delta != " world" {
		t.Fatalf("events=%+v err=%v", events, err)
	}
}

func TestToolFragmentsAndUsage(t *testing.T) {
	for _, finish := range []string{"tool_calls", ""} {
		t.Run("finish_"+finish, func(t *testing.T) {
			c := serverClient(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				chunks := []string{
					`{"choices":[{"delta":{"tool_calls":[{"index":1,"id":"b","function":{"name":"sec","arguments":"{\"b\":"}},{"index":0,"id":"a","function":{"name":"fir","arguments":"{\"a\":"}}]}}]}`,
					`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"name":"st","arguments":"1}"}},{"index":1,"function":{"name":"ond","arguments":"2}"}}]},"finish_reason":"` + finish + `"}]}`,
					`{"choices":[],"usage":{"prompt_tokens":11,"completion_tokens":7}}`,
				}
				for _, chunk := range chunks {
					fmt.Fprintf(w, "data: %s\n\n", chunk)
				}
				fmt.Fprint(w, "data: [DONE]\n\n")
			})
			events, err := collect(t, context.Background(), c, agentcore.ChatRequest{})
			if err != nil {
				t.Fatal(err)
			}
			var calls []agentcore.ToolCall
			var usage *agentcore.Usage
			for _, e := range events {
				if e.ToolCall != nil {
					calls = append(calls, *e.ToolCall)
				}
				if e.Usage != nil {
					usage = e.Usage
				}
			}
			if len(calls) != 2 || calls[0].ID != "a" || calls[0].Name != "first" || string(calls[0].Arguments) != `{"a":1}` || calls[1].ID != "b" || calls[1].Name != "second" || string(calls[1].Arguments) != `{"b":2}` {
				t.Fatalf("calls=%+v", calls)
			}
			if usage == nil || usage.InputTokens != 11 || usage.OutputTokens != 7 {
				t.Fatalf("usage=%+v", usage)
			}
		})
	}
}

func TestSSE(t *testing.T) {
	cases := []struct{ name, body, want string }{
		{"crlf multiline", ": heartbeat\r\n\r\ndata: {\"choices\":\r\ndata: [{\"delta\":{\"content\":\"ok\"}}]}\r\n\r\ndata: [DONE]\r\n\r\n", ""},
		{"bad json", "data: {not-json}\n\n", "invalid"},
		{"null chunk", "data: null\n\ndata: [DONE]\n\n", "invalid"},
		{"missing chunk fields", "data: {}\n\ndata: [DONE]\n\n", "invalid"},
		{"invalid utf8", "data: \xff\n\n", "UTF-8"},
		{"oversized line", "data: " + strings.Repeat("x", (1<<20)+1) + "\n\n", "limit"},
		{"oversized event", strings.Repeat("data: "+strings.Repeat("x", 1024)+"\n", 1025) + "\n", "limit"},
		{"incomplete event", "data: {\"choices\":[]}", "unexpected EOF"},
		{"missing done", "data: {\"choices\":[]}\n\n", "unexpected EOF"},
		{"invalid arguments", "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"x\",\"function\":{\"name\":\"f\",\"arguments\":\"[]\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\n", "arguments"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := serverClient(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, tc.body)
			})
			events, err := collect(t, context.Background(), c, agentcore.ChatRequest{})
			if tc.want == "" {
				if err != nil || len(events) != 1 || events[0].Delta != "ok" {
					t.Fatalf("events=%v err=%v", events, err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err=%v want %q", err, tc.want)
			}
		})
	}
}

func TestStatusAndContentType(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusOK} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			c := serverClient(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(status)
				fmt.Fprint(w, "test-secret Authorization: Bearer test-secret "+strings.Repeat("x", 9000))
			})
			_, err := collect(t, context.Background(), c, agentcore.ChatRequest{})
			if err == nil || strings.Contains(err.Error(), "test-secret") || len(err.Error()) > 8500 {
				t.Fatalf("unsafe or missing error: %v", err)
			}
			if status != 200 && (!strings.Contains(err.Error(), "401") || !strings.Contains(err.Error(), "truncated") || !strings.Contains(err.Error(), "[REDACTED]")) {
				t.Fatal(err)
			}
			if status == 200 && !strings.Contains(err.Error(), "content type") {
				t.Fatal(err)
			}
		})
	}
}

func TestValidationAndKeyFailure(t *testing.T) {
	for _, base := range []string{"", ":bad", "ftp://example.com", "https://user:pass@example.com", "https://example.com?q=x", "https://example.com#x", "https:///path"} {
		if _, err := New(Config{ID: "id", BaseURL: base, APIKey: func(context.Context) (string, error) { return "key", nil }}); err == nil {
			t.Errorf("accepted %q", base)
		}
	}
	if _, err := New(Config{ID: " ", BaseURL: "https://example.com", APIKey: func(context.Context) (string, error) { return "key", nil }}); err == nil {
		t.Error("accepted empty ID")
	}
	var requests atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1) }))
	defer s.Close()
	sentinel := errors.New("key unavailable")
	c, err := New(Config{ID: "id", BaseURL: s.URL, APIKey: func(context.Context) (string, error) { return "", sentinel }})
	if err != nil {
		t.Fatal(err)
	}
	_, err = collect(t, context.Background(), c, agentcore.ChatRequest{})
	if !errors.Is(err, sentinel) || requests.Load() != 0 {
		t.Fatalf("err=%v requests=%d", err, requests.Load())
	}
	c, err = New(Config{ID: "id", BaseURL: s.URL, APIKey: func(context.Context) (string, error) { return "test-secret", nil }})
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{`[]`, `null`, `"secret"`, `{broken`} {
		for _, req := range []agentcore.ChatRequest{{Tools: []agentcore.ToolSpec{{Schema: json.RawMessage(raw)}}}, {Messages: []agentcore.Message{{ToolCalls: []agentcore.ToolCall{{Arguments: json.RawMessage(raw)}}}}}} {
			_, err = collect(t, context.Background(), c, req)
			if err == nil || strings.Contains(err.Error(), raw) {
				t.Fatalf("invalid safe error %v", err)
			}
		}
	}
	if requests.Load() != 0 {
		t.Fatal("invalid requests reached server")
	}
}

func TestCancellation(t *testing.T) {
	for _, mode := range []string{"before", "during", "unread"} {
		t.Run(mode, func(t *testing.T) {
			started := make(chan struct{})
			c := serverClient(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				if mode == "unread" {
					for i := 0; i < 100; i++ {
						fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"x\"}}]}\n\n")
					}
				} else {
					fmt.Fprint(w, ": waiting\n\n")
				}
				w.(http.Flusher).Flush()
				close(started)
				<-r.Context().Done()
			})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "before" {
				cancel()
				_, err := collect(t, ctx, c, agentcore.ChatRequest{})
				if !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
				return
			}
			events, errs := c.Stream(ctx, agentcore.ChatRequest{})
			select {
			case <-started:
			case <-time.After(3 * time.Second):
				t.Fatal("request not started")
			}
			cancel()
			select {
			case err := <-errs:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("err=%v", err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("error channel blocked")
			}
			for range events {
			}
			if _, ok := <-errs; ok {
				t.Fatal("error channel not closed")
			}
		})
	}
}

func TestServerClosesMidEvent(t *testing.T) {
	c := serverClient(t, func(w http.ResponseWriter, r *http.Request) {
		conn, buf, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		fmt.Fprint(buf, "HTTP/1.1 200 OK\r\nContent-Type: text/event-stream\r\nContent-Length: 999\r\n\r\ndata: {\"choices\":")
		buf.Flush()
	})
	_, err := collect(t, context.Background(), c, agentcore.ChatRequest{})
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("err=%v", err)
	}
}

func TestBoundedToolAccumulator(t *testing.T) {
	c := serverClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fragment := strings.Repeat("x", 600000)
		for i := 0; i < 2; i++ {
			fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"a\",\"function\":{\"name\":\"f\",\"arguments\":%q}}]}}]}\n\n", fragment)
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	})
	_, err := collect(t, context.Background(), c, agentcore.ChatRequest{})
	if err == nil || !strings.Contains(err.Error(), "limit") {
		t.Fatalf("err=%v", err)
	}
}

func TestStatusRedactionAtBoundaries(t *testing.T) {
	for _, body := range []string{strings.Repeat("x", 8188) + "test-secret", strings.Repeat("test-secret", 1000)} {
		t.Run(fmt.Sprint(len(body)), func(t *testing.T) {
			c := serverClient(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(401); fmt.Fprint(w, body) })
			_, err := collect(t, context.Background(), c, agentcore.ChatRequest{})
			if err == nil || strings.Contains(err.Error(), "test") || len(err.Error()) > 8300 {
				t.Fatalf("unsafe status error (%d bytes)", len(err.Error()))
			}
		})
	}
}

func TestDefaultClientAndSafeKeyError(t *testing.T) {
	secretErr := errors.New("Authorization: Bearer secret-material")
	c, err := New(Config{ID: "id", BaseURL: "https://example.com", APIKey: func(context.Context) (string, error) { return "", secretErr }})
	if err != nil {
		t.Fatal(err)
	}
	if c.http.Timeout != 0 || c.http.Transport == nil {
		t.Fatal("default client must let context govern stream lifetime")
	}
	_, err = collect(t, context.Background(), c, agentcore.ChatRequest{})
	if !errors.Is(err, secretErr) || strings.Contains(err.Error(), "secret-material") || strings.Contains(err.Error(), "Authorization") {
		t.Fatalf("unsafe key error: %v", err)
	}
}

func TestEscapedBasePath(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.EscapedPath() != "/tenant%2Fname/v1/chat/completions" {
			t.Errorf("path=%s", r.URL.EscapedPath())
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer s.Close()
	c, err := New(Config{ID: "id", BaseURL: s.URL + "/tenant%2Fname/v1", APIKey: func(context.Context) (string, error) { return "key", nil }})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = collect(t, context.Background(), c, agentcore.ChatRequest{}); err != nil {
		t.Fatal(err)
	}
}

func TestRedirectDoesNotForwardCredentials(t *testing.T) {
	var received atomic.Int32
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer destination.Close()
	c := serverClient(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, destination.URL, http.StatusTemporaryRedirect)
	})
	_, err := collect(t, context.Background(), c, agentcore.ChatRequest{})
	if err == nil || !strings.Contains(err.Error(), "307") || received.Load() != 0 {
		t.Fatalf("redirect followed: err=%v received=%d", err, received.Load())
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type failingReader struct{ err error }

func (r failingReader) Read([]byte) (int, error) { return 0, r.err }

func TestBodyReadErrorsAreSafe(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusBadGateway} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			sentinel := errors.New("Authorization: Bearer test-secret")
			client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(failingReader{err: sentinel})}, nil
			})}
			c, err := New(Config{ID: "test", BaseURL: "https://example.com/v1", HTTPClient: client, APIKey: func(context.Context) (string, error) { return "test-secret", nil }})
			if err != nil {
				t.Fatal(err)
			}
			_, err = collect(t, context.Background(), c, agentcore.ChatRequest{})
			if !errors.Is(err, sentinel) {
				t.Fatalf("read cause lost: %v", err)
			}
			if strings.Contains(err.Error(), "test-secret") || strings.Contains(err.Error(), "Authorization") {
				t.Fatalf("unsafe read error: %v", err)
			}
		})
	}
}

func TestMalformedTrailerErrorIsSafe(t *testing.T) {
	c := serverClient(t, func(w http.ResponseWriter, r *http.Request) {
		conn, buf, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		fmt.Fprint(buf, "HTTP/1.1 200 OK\r\nContent-Type: text/event-stream\r\nTransfer-Encoding: chunked\r\n\r\n0\r\nAuthorization Bearer test-secret\r\n\r\n")
		buf.Flush()
	})
	_, err := collect(t, context.Background(), c, agentcore.ChatRequest{})
	if err == nil {
		t.Fatal("missing malformed trailer error")
	}
	if strings.Contains(err.Error(), "test-secret") || strings.Contains(err.Error(), "Authorization") {
		t.Fatalf("unsafe trailer error: %v", err)
	}
}

func TestMessageContentPresence(t *testing.T) {
	messages := []agentcore.Message{
		{Role: agentcore.RoleSystem},
		{Role: agentcore.RoleUser},
		{Role: agentcore.RoleTool, ToolCallID: "call"},
		{Role: agentcore.RoleAssistant, ToolCalls: []agentcore.ToolCall{{ID: "call", Name: "lookup", Arguments: json.RawMessage(`{}`)}}},
		{Role: agentcore.RoleAssistant, Content: "answer"},
	}
	c := serverClient(t, func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []map[string]any `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		if len(body.Messages) != len(messages) {
			t.Errorf("message count: %d", len(body.Messages))
			return
		}
		for i := 0; i < 3; i++ {
			content, present := body.Messages[i]["content"]
			if !present || content != "" {
				t.Errorf("role %s needs explicit empty content: %v", messages[i].Role, body.Messages[i])
			}
		}
		if body.Messages[2]["tool_call_id"] != "call" {
			t.Error("tool call ID lost")
		}
		if content, present := body.Messages[3]["content"]; present && content != nil {
			t.Errorf("empty assistant tool call content should be omitted or null: %v", content)
		}
		if body.Messages[4]["content"] != "answer" {
			t.Error("assistant text lost")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: [DONE]\n\n")
	})
	if _, err := collect(t, context.Background(), c, agentcore.ChatRequest{Messages: messages}); err != nil {
		t.Fatal(err)
	}
}
