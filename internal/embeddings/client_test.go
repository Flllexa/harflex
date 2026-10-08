package embeddings

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestProfileAcceptsOnlyLiteralLoopbackHTTP(t *testing.T) {
	valid := []string{"http://127.0.0.1:11434", "http://127.8.1.2:1234/", "http://[::1]:1234"}
	for _, endpoint := range valid {
		t.Run(endpoint, func(t *testing.T) {
			profile, err := ValidateProfile(Profile{Kind: Ollama, BaseURL: endpoint, Model: "embeddinggemma"})
			if err != nil || profile.Fingerprint == "" {
				t.Fatalf("valid local profile rejected: %+v %v", profile, err)
			}
		})
	}
	invalid := []string{"http://localhost:11434", "http://example.com:11434", "http://192.168.1.2:11434", "https://127.0.0.1:11434", "http://user@127.0.0.1:11434", "http://127.0.0.1:11434/?x=1", "http://127.0.0.1:11434/#x", "http://127.0.0.1:11434/v1", "http://127.0.0.1:0", "http://0177.0.0.1:1234"}
	for _, endpoint := range invalid {
		t.Run(endpoint, func(t *testing.T) {
			if _, err := ValidateProfile(Profile{Kind: Ollama, BaseURL: endpoint, Model: "embeddinggemma"}); err == nil {
				t.Fatal("unsafe endpoint accepted")
			}
		})
	}
	for _, model := range []string{"", " model", "model\nheader", strings.Repeat("x", 129)} {
		if _, err := ValidateProfile(Profile{Kind: Ollama, BaseURL: "http://127.0.0.1:11434", Model: model}); err == nil {
			t.Fatalf("invalid model %q accepted", model)
		}
	}
	if _, err := ValidateProfile(Profile{Kind: "cloud", BaseURL: "http://127.0.0.1:11434", Model: "x"}); err == nil {
		t.Fatal("unknown provider accepted")
	}
}

func TestEmbedUsesProviderBatchContract(t *testing.T) {
	for _, tc := range []struct {
		kind           Kind
		path, response string
	}{
		{Ollama, "/api/embed", `{"embeddings":[[1,0],[0,1]]}`},
		{LMStudio, "/v1/embeddings", `{"data":[{"index":1,"embedding":[0,1]},{"index":0,"embedding":[1,0]}]}`},
	} {
		t.Run(string(tc.kind), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != tc.path || r.Method != http.MethodPost {
					t.Errorf("wrong route: %s %s", r.Method, r.URL.Path)
				}
				if r.Header.Get("Authorization") != "" {
					t.Error("unexpected credential")
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(tc.response))
			}))
			t.Cleanup(server.Close)
			profile, err := ValidateProfile(Profile{Kind: tc.kind, BaseURL: server.URL, Model: "local-model"})
			if err != nil {
				t.Fatal(err)
			}
			client := NewClient(profile)
			vectors, err := client.Embed(t.Context(), []string{"one", "two"})
			if err != nil || len(vectors) != 2 || len(vectors[0]) != 2 || vectors[0][0] != 1 || vectors[1][1] != 1 {
				t.Fatalf("invalid ordered vectors: %v %v", vectors, err)
			}
		})
	}
}

func TestEmbedRejectsRedirectMalformedAndOversizedResponses(t *testing.T) {
	for _, tc := range []struct {
		name     string
		status   int
		body     string
		location string
	}{
		{"redirect", http.StatusTemporaryRedirect, "", "https://example.com/collect"},
		{"missing vectors", http.StatusOK, `{}`, ""},
		{"wrong count", http.StatusOK, `{"embeddings":[[1,0]]}`, ""},
		{"ragged dimensions", http.StatusOK, `{"embeddings":[[1,0],[1]]}`, ""},
		{"zero vector", http.StatusOK, `{"embeddings":[[0,0],[1,0]]}`, ""},
		{"oversized", http.StatusOK, fmt.Sprintf(`{"embeddings":[[1,0],[0,1]],"padding":"%s"}`, strings.Repeat("x", MaxResponseBytes)), ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tc.location != "" {
					w.Header().Set("Location", tc.location)
				}
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			t.Cleanup(server.Close)
			profile, _ := ValidateProfile(Profile{Kind: Ollama, BaseURL: server.URL, Model: "model"})
			if _, err := NewClient(profile).Embed(t.Context(), []string{"one", "two"}); err == nil {
				t.Fatal("invalid response accepted")
			}
		})
	}
}

func TestLMStudioResponseRequiresExplicitIndexes(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"embedding":[1,0]}]}`))
	}))
	t.Cleanup(server.Close)
	profile, _ := ValidateProfile(Profile{Kind: LMStudio, BaseURL: server.URL, Model: "model"})
	if _, err := NewClient(profile).Embed(t.Context(), []string{"one"}); err == nil {
		t.Fatal("LM Studio item without index accepted")
	}
}

func TestEmbedRespectsCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	t.Cleanup(server.Close)
	profile, _ := ValidateProfile(Profile{Kind: Ollama, BaseURL: server.URL, Model: "model"})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := NewClient(profile).Embed(ctx, []string{"one"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
}
