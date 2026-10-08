package modelcatalog

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
)

func TestHTTPProviderRoutesAndExactIDs(t *testing.T) {
	cases := []struct {
		name, provider, basePath, path, body, id, source, bearer string
		tls                                                      bool
	}{
		{"openai", "openai", "/v1", "/v1/models", `{"data":[{"id":"gpt-exact","owned_by":"openai"}]}`, "gpt-exact", "openai_models", "inference-canary", true},
		{"generic custom prefix", "generic", "/custom/v1", "/custom/v1/models", `{"data":[{"id":"vendor/exact"}]}`, "vendor/exact", "generic_models", "inference-canary", true},
		{"lm studio native", "lm_studio", "/v1", "/api/v1/models", `{"models":[{"type":"embedding","key":"skip"},{"type":"llm","key":"local/exact","display_name":"Local","loaded_instances":[],"capabilities":{"reasoning":{"allowed_options":["off","high"],"default":"off"}}}]}`, "local/exact", "lm_studio_native", "", false},
		{"ollama native", "ollama", "/v1", "/api/tags", `{"models":[{"name":"llama:exact","model":"llama:exact"}]}`, "llama:exact", "ollama_tags", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != tc.path {
					t.Errorf("request = %s %s, want GET %s", r.Method, r.URL.Path, tc.path)
				}
				if got := r.Header.Get("Authorization"); got != bearerHeader(tc.bearer) {
					t.Errorf("Authorization = %q, want provider-specific bearer", got)
				}
				if tc.bearer == "" {
					if _, exists := r.Header["Authorization"]; exists {
						t.Error("keyless local request sent Authorization")
					}
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, tc.body)
			})
			var server *httptest.Server
			if tc.tls {
				server = httptest.NewTLSServer(handler)
			} else {
				server = httptest.NewServer(handler)
			}
			t.Cleanup(server.Close)

			page, err := FetchHTTPPage(t.Context(), server.Client(), HTTPQuery{
				BackendID: "profile-1", ProviderType: tc.provider, BaseURL: server.URL + tc.basePath, InferenceKey: tc.bearer,
			}, "", MaxBytes)
			if err != nil || len(page.Models) != 1 {
				t.Fatalf("page = %+v, err = %v", page, err)
			}
			model := page.Models[0]
			if model.ID != tc.id || model.BackendID != "profile-1" || model.Source != tc.source ||
				page.Source != tc.source || model.Availability != "listed" || page.AccountFiltered || page.NextCursor != "" || page.Bytes != int64(len(tc.body)) {
				t.Fatalf("page = %+v", page)
			}
			if tc.provider == "lm_studio" {
				if model.DisplayName != "Local" || model.Loaded == nil || *model.Loaded ||
					len(model.SupportedReasoningEfforts) != 2 || model.SupportedReasoningEfforts[1] != "high" || model.DefaultReasoningEffort != "off" {
					t.Fatal(model)
				}
			}
			if tc.provider == "openai" && model.OwnedBy != "openai" {
				t.Fatal(model)
			}
		})
	}
}

func TestHTTPPreservesEscapedGenericPathAndOrigin(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.EscapedPath() != "/custom%2Ftenant/v1/models" || r.Host != strings.TrimPrefix(server.URL, "https://") {
			t.Errorf("request = %s host %s", r.URL.EscapedPath(), r.Host)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":[]}`)
	}))
	t.Cleanup(server.Close)
	page, err := FetchHTTPPage(t.Context(), server.Client(), HTTPQuery{ProviderType: "generic", BaseURL: server.URL + "/custom%2Ftenant/v1"}, "", MaxBytes)
	if err != nil || page.Source != "generic_models" || len(page.Models) != 0 {
		t.Fatal(page, err)
	}
}

func TestOpenRouterSeparateKeysAndPagination(t *testing.T) {
	var userCalls, generalCalls atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("unexpected method %s", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/models/user":
			userCalls.Add(1)
			if r.Header.Get("Authorization") != "Bearer management-canary" || r.URL.Query().Get("q") != "" ||
				r.URL.Query().Get("limit") != "100" {
				t.Errorf("account request = %s, auth = %q", r.URL.String(), r.Header.Get("Authorization"))
			}
			switch r.URL.Query().Get("offset") {
			case "0":
				_, _ = io.WriteString(w, `{"data":[{"id":"provider/a","name":"A","context_length":4096}],"total_count":2}`)
			case "1":
				_, _ = io.WriteString(w, `{"data":[{"id":"provider/b","name":"B"}],"total_count":2}`)
			default:
				t.Errorf("unexpected offset: %s", r.URL.RawQuery)
			}
		case "/api/v1/models":
			generalCalls.Add(1)
			if r.Header.Get("Authorization") != "Bearer inference-canary" || r.URL.Query().Get("q") != "a" ||
				r.URL.Query().Get("offset") != "0" || r.URL.Query().Get("limit") != "100" {
				t.Errorf("general request = %s, auth = %q", r.URL.String(), r.Header.Get("Authorization"))
			}
			_, _ = io.WriteString(w, `{"data":[{"id":"provider/b","name":"B"}],"total_count":1}`)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	t.Cleanup(server.Close)

	query := HTTPQuery{
		BackendID: "router", ProviderType: "openrouter", BaseURL: server.URL + "/api/v1",
		InferenceKey: "inference-canary", ManagementKey: "management-canary", SearchTerm: "a",
	}
	first, err := FetchHTTPPage(t.Context(), server.Client(), query, "", MaxBytes)
	if err != nil || first.Source != "openrouter_account" || !first.AccountFiltered || first.NextCursor != "1" ||
		len(first.Models) != 1 || first.Models[0].ID != "provider/a" || first.Models[0].DisplayName != "A" || first.Models[0].ContextLength != 4096 {
		t.Fatal(first, err)
	}
	second, err := FetchHTTPPage(t.Context(), server.Client(), query, first.NextCursor, MaxBytes-first.Bytes)
	if err != nil || len(second.Models) != 1 || second.Models[0].ID != "provider/b" || second.NextCursor != "" || !second.AccountFiltered {
		t.Fatal(second, err)
	}
	if userCalls.Load() != 2 || generalCalls.Load() != 0 {
		t.Fatalf("account = %d, general = %d", userCalls.Load(), generalCalls.Load())
	}

	query.ManagementKey = ""
	general, err := FetchHTTPPage(t.Context(), server.Client(), query, "", MaxBytes)
	if err != nil || general.Source != "openrouter_general_unfiltered" || general.AccountFiltered || len(general.Models) != 1 || general.Models[0].Availability != "listed_unfiltered" ||
		userCalls.Load() != 2 || generalCalls.Load() != 1 {
		t.Fatal(general, err, userCalls.Load(), generalCalls.Load())
	}
}

func TestHTTPNoRedirectNoRawFailureAndNoImplicitFallback(t *testing.T) {
	var redirected, general atomic.Int32
	target := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { redirected.Add(1) }))
	t.Cleanup(target.Close)
	source := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models/user" {
			w.WriteHeader(http.StatusForbidden)
			_, _ = io.WriteString(w, "management-canary private diagnostic")
			return
		}
		if r.URL.Path == "/v1/models" {
			general.Add(1)
		}
		http.Redirect(w, r, target.URL+"/stolen", http.StatusFound)
	}))
	t.Cleanup(source.Close)
	query := HTTPQuery{ProviderType: "openrouter", BaseURL: source.URL + "/v1", InferenceKey: "inference-canary", ManagementKey: "management-canary"}
	_, err := FetchHTTPPage(t.Context(), source.Client(), query, "", MaxBytes)
	if !errors.Is(err, ErrUnauthorized) || strings.Contains(fmt.Sprint(err), "canary") || redirected.Load() != 0 || general.Load() != 0 {
		t.Fatal(err, redirected.Load(), general.Load())
	}
	query.ProviderType, query.ManagementKey = "generic", ""
	_, err = FetchHTTPPage(t.Context(), source.Client(), query, "", MaxBytes)
	if !errors.Is(err, ErrUnavailable) || redirected.Load() != 0 || general.Load() != 1 {
		t.Fatal(err, redirected.Load(), general.Load())
	}
}

func TestHTTPUnsupportedMalformedAndBudget(t *testing.T) {
	for _, tc := range []struct {
		name, contentType, body string
		status                  int
		want                    error
	}{
		{"404 unsupported", "application/json", "private-canary", http.StatusNotFound, ErrUnsupported},
		{"405 unsupported", "application/json", "private-canary", http.StatusMethodNotAllowed, ErrUnsupported},
		{"401 unauthorized", "application/json", "private-canary", http.StatusUnauthorized, ErrUnauthorized},
		{"202 not a complete list", "application/json", `{"data":[]}`, http.StatusAccepted, ErrUnavailable},
		{"206 partial response", "application/json", `{"data":[]}`, http.StatusPartialContent, ErrUnavailable},
		{"malformed", "application/json", `{"data":`, http.StatusOK, ErrUnavailable},
		{"missing list", "application/json", `{}`, http.StatusOK, ErrUnavailable},
		{"unexpected content type", "text/html", `{"data":[]}`, http.StatusOK, ErrUnavailable},
		{"over budget", "application/json", strings.Repeat("x", 1024), http.StatusOK, ErrLimit},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", tc.contentType)
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}))
			t.Cleanup(server.Close)
			_, err := FetchHTTPPage(t.Context(), server.Client(), HTTPQuery{ProviderType: "generic", BaseURL: server.URL + "/v1"}, "", 32)
			if !errors.Is(err, tc.want) || strings.Contains(fmt.Sprint(err), "canary") {
				t.Fatal(err, tc.want)
			}
		})
	}
}

func TestHTTPRejectsMillionItemsWithinByteBudget(t *testing.T) {
	// A 3 MiB response can contain over a million tiny objects. The item limit
	// must stop decoding before allocating a million-element model slice.
	items := strings.Repeat("{},", 1_000_000) + "{}"
	body := `{"data":[` + items + `]}`
	if int64(len(body)) >= MaxBytes {
		t.Fatalf("fixture exceeds byte budget: %d", len(body))
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(server.Close)
	page, err := FetchHTTPPage(t.Context(), server.Client(), HTTPQuery{ProviderType: "generic", BaseURL: server.URL + "/v1"}, "", MaxBytes)
	if !errors.Is(err, ErrLimit) || len(page.Models) != 0 {
		t.Fatalf("page models = %d, err = %v", len(page.Models), err)
	}
}

func TestHTTPBoundsLMStudioReasoningOptions(t *testing.T) {
	options := strings.Repeat(`"off",`, 100_000) + `"high"`
	body := `{"models":[{"type":"llm","key":"local/exact","loaded_instances":[],"capabilities":{"reasoning":{"allowed_options":[` + options + `],"default":"high"}}}]}`
	if int64(len(body)) >= MaxBytes {
		t.Fatalf("fixture exceeds byte budget: %d", len(body))
	}
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	page, err := FetchHTTPPage(t.Context(), client, HTTPQuery{ProviderType: "lm_studio", BaseURL: "http://127.0.0.1:1234/v1"}, "", MaxBytes)
	if !errors.Is(err, ErrLimit) || len(page.Models) != 0 {
		t.Fatalf("page models = %d, err = %v", len(page.Models), err)
	}
}

func TestHTTPStreamsLargeValidLMStudioLoadedInstanceList(t *testing.T) {
	instance := `{"id":"loaded/exact","config":{"context_length":4096}}`
	instances := strings.Repeat(instance+",", 100_000) + instance
	body := `{"models":[{"type":"llm","key":"local/exact","loaded_instances":[` + instances + `]}]}`
	if int64(len(body)) >= MaxBytes {
		t.Fatalf("fixture exceeds byte budget: %d", len(body))
	}
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	page, err := FetchHTTPPage(t.Context(), client, HTTPQuery{ProviderType: "lm_studio", BaseURL: "http://127.0.0.1:1234/v1"}, "", MaxBytes)
	if err != nil || len(page.Models) != 1 || page.Models[0].Loaded == nil || !*page.Models[0].Loaded {
		t.Fatalf("page = %+v, err = %v", page, err)
	}
}

func TestHTTPRejectsInvalidLMStudioLoadedInstances(t *testing.T) {
	for _, tc := range []struct{ name, instances string }{
		{"null entry", `[null]`},
		{"number entry", `[1]`},
		{"missing instance ID", `[{}]`},
		{"missing config", `[{"id":"loaded/exact"}]`},
		{"invalid ID", `[{"id":"bad\nitem","config":{"context_length":4096}}]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := `{"models":[{"type":"llm","key":"local/exact","loaded_instances":` + tc.instances + `}]}`
			client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
			})}
			page, err := FetchHTTPPage(t.Context(), client, HTTPQuery{ProviderType: "lm_studio", BaseURL: "http://127.0.0.1:1234/v1"}, "", MaxBytes)
			if !errors.Is(err, ErrUnavailable) || len(page.Models) != 0 {
				t.Fatalf("page models = %d, err = %v", len(page.Models), err)
			}
		})
	}
}

func TestHTTPReadCancellationIsInterrupted(t *testing.T) {
	query := HTTPQuery{ProviderType: "generic", BaseURL: "https://example.com/v1"}
	newClient := func(cancel context.CancelFunc) *http.Client {
		return &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: &cancelDuringRead{cancel: cancel}}, nil
		})}
	}
	ctx, cancel := context.WithCancel(t.Context())
	_, err := FetchHTTPPage(ctx, newClient(cancel), query, "", MaxBytes)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("direct read cancellation = %v", err)
	}
	parent, stop := context.WithCancel(t.Context())
	got := Collect(parent, func(ctx context.Context, cursor string, remaining int64) (Page, error) {
		return FetchHTTPPage(ctx, newClient(stop), query, cursor, remaining)
	})
	if got.Status != StatusInterrupted || got.ErrorCode != "catalog_cancelled" || got.Complete {
		t.Fatalf("collector result = %+v", got)
	}
}

func TestHTTPRejectsInvalidInputBeforeNetwork(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":[]}`)
	}))
	t.Cleanup(server.Close)
	for _, tc := range []struct {
		name, provider, baseURL, cursor string
		remaining                       int64
		want                            error
	}{
		{"zero budget", "generic", server.URL + "/v1", "", 0, ErrLimit},
		{"URL userinfo", "generic", "http://secret@127.0.0.1/v1", "", MaxBytes, ErrUnavailable},
		{"URL query", "generic", server.URL + "/v1?token=secret", "", MaxBytes, ErrUnavailable},
		{"URL fragment", "generic", server.URL + "/v1#private", "", MaxBytes, ErrUnavailable},
		{"invalid cursor", "openrouter", server.URL + "/api/v1", "-1", MaxBytes, ErrUnavailable},
		{"overflow cursor", "openrouter", server.URL + "/api/v1", strconv.Itoa(int(^uint(0) >> 1)), MaxBytes, ErrUnavailable},
		{"unknown provider", "unknown", server.URL + "/v1", "", MaxBytes, ErrUnsupported},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := FetchHTTPPage(t.Context(), server.Client(), HTTPQuery{ProviderType: tc.provider, BaseURL: tc.baseURL}, tc.cursor, tc.remaining)
			if !errors.Is(err, tc.want) {
				t.Fatal(err, tc.want)
			}
		})
	}
	if calls.Load() != 0 {
		t.Fatalf("unexpected network requests: %d", calls.Load())
	}
}

func TestHTTPDoesNotReadErrorBody(t *testing.T) {
	probe := &readProbe{}
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusForbidden, Body: probe, Header: make(http.Header)}, nil
	})}
	_, err := FetchHTTPPage(context.Background(), client, HTTPQuery{ProviderType: "generic", BaseURL: "https://example.com/v1"}, "", MaxBytes)
	if !errors.Is(err, ErrUnauthorized) || probe.read.Load() != 0 || probe.closed.Load() != 1 {
		t.Fatal(err, probe.read.Load(), probe.closed.Load())
	}
}

func TestHTTPDoesNotSendOrStoreClientCookies(t *testing.T) {
	var receivedCookie atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") != "" {
			receivedCookie.Store(true)
		}
		w.Header().Set("Set-Cookie", "new=private; Path=/")
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":[]}`)
	}))
	t.Cleanup(server.Close)
	base, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	jar.SetCookies(base, []*http.Cookie{{Name: "existing", Value: "private", Path: "/"}})
	client := server.Client()
	client.Jar = jar
	_, err = FetchHTTPPage(t.Context(), client, HTTPQuery{ProviderType: "generic", BaseURL: server.URL + "/v1"}, "", MaxBytes)
	if err != nil {
		t.Fatal(err)
	}
	if receivedCookie.Load() {
		t.Error("catalog request sent a client cookie")
	}
	cookies := jar.Cookies(base)
	if len(cookies) != 1 || cookies[0].Name != "existing" || cookies[0].Value != "private" {
		t.Errorf("catalog response modified the caller's cookie jar: %d cookies", len(cookies))
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type readProbe struct{ read, closed atomic.Int32 }

func (p *readProbe) Read([]byte) (int, error) { p.read.Add(1); return 0, io.EOF }
func (p *readProbe) Close() error             { p.closed.Add(1); return nil }

type cancelDuringRead struct{ cancel context.CancelFunc }

func (p *cancelDuringRead) Read([]byte) (int, error) {
	p.cancel()
	return 0, io.ErrUnexpectedEOF
}
func (p *cancelDuringRead) Close() error { return nil }

func bearerHeader(key string) string {
	if key == "" {
		return ""
	}
	return "Bearer " + key
}
