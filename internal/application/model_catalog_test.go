package application

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/persioflexa/harflex/internal/agentcore"
	"github.com/persioflexa/harflex/internal/catalog"
	"github.com/persioflexa/harflex/internal/modelcatalog"
	"github.com/persioflexa/harflex/internal/providers/openai"
)

func TestManagementKeyOnlyListsAndInferenceKeyOnlyCompletes(t *testing.T) {
	var mu sync.Mutex
	headers := map[string][]string{}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		headers[r.URL.Path] = append(headers[r.URL.Path], r.Header.Get("Authorization"))
		mu.Unlock()
		switch r.URL.Path {
		case "/api/v1/models/user":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"data":[{"id":"openai/m","name":"M"}],"total_count":1}`)
		case "/api/v1/chat/completions":
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\ndata: [DONE]\n\n")
		default:
			t.Errorf("unexpected route %s", r.URL.Path)
		}
	}))
	t.Cleanup(server.Close)
	s, _, _ := setup(t)
	s.modelHTTPClient = server.Client()
	s.providerFactory = func(c openai.Config) (agentcore.Provider, error) {
		c.HTTPClient = server.Client()
		return openai.New(c)
	}
	in := profileInput()
	in.ID, in.Name, in.ProviderType, in.BaseURL, in.Model, in.APIKey = "router", "Router", "openrouter", server.URL+"/api/v1", "openai/m", "inference-canary"
	if _, err := s.SaveProviderProfile(in); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveOpenRouterManagementKey("router", "management-canary"); err != nil {
		t.Fatal(err)
	}
	found, err := s.QueryHTTPModelCatalog(t.Context(), HTTPModelCatalogQuery{ProfileID: "router"})
	if err != nil || found.Status != modelcatalog.StatusComplete || !found.AccountFiltered {
		t.Fatal(found, err)
	}
	workspace, err := s.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	session, err := s.CreateSession(CreateSessionInput{WorkspaceID: workspace.ID, BackendID: "router"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.Prompt(PromptInput{SessionID: session.ID, Text: "hello"})
	if err != nil || result.Status != RunCompleted {
		t.Fatal(result, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if got := headers["/api/v1/models/user"]; len(got) != 1 || got[0] != "Bearer management-canary" {
		t.Fatal(got)
	}
	if got := headers["/api/v1/chat/completions"]; len(got) != 1 || got[0] != "Bearer inference-canary" {
		t.Fatal(got)
	}
	encoded, _ := json.Marshal(found)
	if strings.Contains(string(encoded), "canary") {
		t.Fatal("secret in catalog DTO")
	}
}

func TestHTTPModelCatalogAccountSearchFiltersLocallyWithoutFallback(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/api/v1/models/user" || r.URL.Query().Get("q") != "" ||
			r.Header.Get("Authorization") != "Bearer management-canary" {
			t.Errorf("unexpected account catalog request: %s?%s", r.URL.Path, r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":[{"id":"provider/a","name":"Alpha"},{"id":"provider/b","name":"Beta"}],"total_count":2}`)
	}))
	t.Cleanup(server.Close)
	s, _, _ := setup(t)
	s.modelHTTPClient = server.Client()
	in := profileInput()
	in.ID, in.ProviderType, in.BaseURL, in.APIKey = "router", "openrouter", server.URL+"/api/v1", "inference-canary"
	if _, err := s.SaveProviderProfile(in); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveOpenRouterManagementKey("router", "management-canary"); err != nil {
		t.Fatal(err)
	}
	found, err := s.QueryHTTPModelCatalog(t.Context(), HTTPModelCatalogQuery{ProfileID: "router", SearchTerm: "beta"})
	if err != nil || !found.Complete || !found.AccountFiltered || len(found.Models) != 1 || found.Models[0].ID != "provider/b" || calls.Load() != 1 {
		t.Fatal(found, err, calls.Load())
	}
}

func TestManagementKeyStatusAndClearPreserveHistory(t *testing.T) {
	s, db, _ := setup(t)
	in := profileInput()
	in.ID, in.ProviderType, in.BaseURL, in.APIKey = "router", "openrouter", "https://router.example/api/v1", "inference-canary"
	if _, err := s.SaveProviderProfile(in); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"management-canary-1", "management-canary-2"} {
		if _, err := s.SaveOpenRouterManagementKey("router", key); err != nil {
			t.Fatal(err)
		}
	}
	status, err := s.GetOpenRouterManagementKeyStatus("router")
	if err != nil || !status.Configured || !status.Usable || status.Origin != "https://router.example:443" {
		t.Fatal(status, err)
	}
	if err := s.ClearOpenRouterManagementKey("router"); err != nil {
		t.Fatal(err)
	}
	status, err = s.GetOpenRouterManagementKeyStatus("router")
	if err != nil || status.Configured || status.Usable {
		t.Fatal(status, err)
	}
	refs, err := db.ListCredentialReferences(t.Context())
	if err != nil || len(refs) != 3 {
		t.Fatal(refs, err)
	}
}

func TestManagementKeyDeniedBeforeVaultOnChangedOrigin(t *testing.T) {
	s, db, vault := setup(t)
	in := profileInput()
	in.ID, in.ProviderType, in.BaseURL, in.APIKey = "router", "openrouter", "https://old.example/api/v1", "inference-canary"
	if _, err := s.SaveProviderProfile(in); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveOpenRouterManagementKey("router", "management-canary"); err != nil {
		t.Fatal(err)
	}
	profile, err := db.GetProviderProfile(t.Context(), "router")
	if err != nil {
		t.Fatal(err)
	}
	profile.BaseURL = "https://new.example/api/v1"
	if err := db.UpsertProviderProfile(t.Context(), profile); err != nil {
		t.Fatal(err)
	}
	status, err := s.GetOpenRouterManagementKeyStatus("router")
	if err != nil || !status.Configured || status.Usable {
		t.Fatal(status, err)
	}
	before := vault.gets
	got, err := s.QueryHTTPModelCatalog(t.Context(), HTTPModelCatalogQuery{ProfileID: "router"})
	if err != nil || got.ErrorCode != "catalog_management_origin_changed" || vault.gets != before {
		t.Fatal(got, err)
	}
}

func TestHTTPModelCatalogCacheSeparatesRevisionAndRefresh(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":[{"id":"m"}]}`)
	}))
	t.Cleanup(server.Close)
	s, db, _ := setup(t)
	s.modelHTTPClient = server.Client()
	in := profileInput()
	in.ID, in.ProviderType, in.BaseURL, in.APIKey = "api", "openai", server.URL+"/v1", "inference-canary"
	if _, err := s.SaveProviderProfile(in); err != nil {
		t.Fatal(err)
	}
	for _, refresh := range []bool{false, false, true} {
		got, err := s.QueryHTTPModelCatalog(t.Context(), HTTPModelCatalogQuery{ProfileID: "api", Refresh: refresh})
		if err != nil || !got.Complete || got.Status != modelcatalog.StatusComplete {
			t.Fatal(got, err)
		}
	}
	if calls.Load() != 2 {
		t.Fatalf("cache/refresh calls = %d", calls.Load())
	}
	profile, err := db.GetProviderProfile(t.Context(), "api")
	if err != nil {
		t.Fatal(err)
	}
	profile.UpdatedAt = profile.UpdatedAt.Add(1)
	if err := db.UpsertProviderProfile(t.Context(), profile); err != nil {
		t.Fatal(err)
	}
	if _, err := s.QueryHTTPModelCatalog(t.Context(), HTTPModelCatalogQuery{ProfileID: "api"}); err != nil || calls.Load() != 3 {
		t.Fatal(err, calls.Load())
	}
}

func TestNoHTTPDiscoveryUntilExplicitQuery(t *testing.T) {
	s, _, _ := setup(t)
	var calls int
	s.modelHTTPClient = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls++
		return nil, fmt.Errorf("unexpected catalog request")
	})}
	if _, err := s.GetSettings(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ListProviderProfiles(); err != nil {
		t.Fatal(err)
	}
	_ = s.ListBackends()
	if calls != 0 {
		t.Fatalf("discovery without explicit query: %d", calls)
	}
}

func TestKeylessLocalAndUnsupportedGenericDoNotInventModels(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, present := r.Header["Authorization"]; present {
			t.Error("keyless local request sent Authorization")
		}
		if r.URL.Path == "/api/v1/models" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"models":[{"type":"llm","key":"installed","loaded_instances":[]}]}`)
			return
		}
		if r.URL.Path == "/v1/models" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		t.Errorf("unexpected path %s", r.URL.Path)
	}))
	t.Cleanup(server.Close)
	s, _, _ := setup(t)
	in := profileInput()
	in.ID, in.ProviderType, in.BaseURL, in.Model, in.APIKey = "lm", "lm_studio", server.URL+"/v1", "installed", ""
	if _, err := s.SaveProviderProfile(in); err != nil {
		t.Fatal(err)
	}
	local, err := s.QueryHTTPModelCatalog(t.Context(), HTTPModelCatalogQuery{ProfileID: "lm"})
	if err != nil || local.Status != modelcatalog.StatusComplete || len(local.Models) != 1 || local.Models[0].Loaded == nil || *local.Models[0].Loaded {
		t.Fatal(local, err)
	}
	in.ID, in.ProviderType, in.Model = "generic", "generic", "manual-preference"
	if _, err := s.SaveProviderProfile(in); err != nil {
		t.Fatal(err)
	}
	missing, err := s.QueryHTTPModelCatalog(t.Context(), HTTPModelCatalogQuery{ProfileID: "generic"})
	if err != nil || missing.Status != modelcatalog.StatusUnsupported || len(missing.Models) != 0 {
		t.Fatal(missing, err)
	}
}

func TestUnsavedDraftKeyIsOneShotAndNotCached(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Authorization") != "Bearer draft-canary" {
			t.Error("wrong draft key")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":[{"id":"m"}]}`)
	}))
	t.Cleanup(server.Close)
	s, db, _ := setup(t)
	s.modelHTTPClient = server.Client()
	for range 2 {
		draft := &HTTPModelCatalogDraft{ProviderType: "openai", BaseURL: server.URL + "/v1", APIKey: "draft-canary"}
		found, err := s.QueryHTTPModelCatalog(t.Context(), HTTPModelCatalogQuery{ProfileID: "draft", Draft: draft})
		if err != nil || !found.Complete || draft.APIKey != "" {
			t.Fatal(found, err)
		}
		encoded, _ := json.Marshal(found)
		if strings.Contains(string(encoded), "canary") {
			t.Fatal("draft secret in result")
		}
	}
	if calls.Load() != 2 {
		t.Fatal("draft query cached")
	}
	if _, err := db.GetProviderProfile(t.Context(), "draft"); err == nil {
		t.Fatal("draft was saved")
	}
}

func TestUnsavedDraftKeyIsClearedEvenWhenQueryRejected(t *testing.T) {
	s, _, _ := setup(t)
	draft := &HTTPModelCatalogDraft{ProviderType: "openai", BaseURL: "https://api.example/v1", APIKey: "draft-canary"}
	_, err := s.QueryHTTPModelCatalog(t.Context(), HTTPModelCatalogQuery{Draft: draft})
	if !errors.Is(err, ErrInvalidInput) || draft.APIKey != "" {
		t.Fatalf("rejected query retained draft key: err=%v cleared=%t", err, draft.APIKey == "")
	}
}

func TestHTTPModelCatalogRejectsEchoedCredential(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":[{"id":"inference-canary","owned_by":"private"}]}`)
	}))
	t.Cleanup(server.Close)
	s, _, _ := setup(t)
	s.modelHTTPClient = server.Client()
	in := profileInput()
	in.ID, in.ProviderType, in.BaseURL, in.APIKey = "api", "openai", server.URL+"/v1", "inference-canary"
	if _, err := s.SaveProviderProfile(in); err != nil {
		t.Fatal(err)
	}
	found, err := s.QueryHTTPModelCatalog(t.Context(), HTTPModelCatalogQuery{ProfileID: "api"})
	raw, _ := json.Marshal(found)
	if err != nil || found.ErrorCode != "catalog_secret_echo" || found.Complete || strings.Contains(string(raw), "inference-canary") {
		t.Fatal(found, err)
	}
}

func TestLegacyHTTPProfileIsBlockedBeforeVaultAndNetwork(t *testing.T) {
	s, db, vault := setup(t)
	profile := catalog.ProviderProfile{
		ID: "legacy", Name: "Legacy", Kind: "openai_compatible", ProviderType: "generic",
		BaseURL: "http://remote.example/v1", Model: "m", CredentialProvider: "vault",
		CredentialAccount: "old", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	if err := db.UpsertProviderProfile(t.Context(), profile); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	s.modelHTTPClient = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return nil, fmt.Errorf("should not connect")
	})}
	before := vault.gets
	found, err := s.QueryHTTPModelCatalog(t.Context(), HTTPModelCatalogQuery{ProfileID: "legacy"})
	if err != nil || found.ErrorCode != "catalog_profile_blocked" || vault.gets != before || calls.Load() != 0 {
		t.Fatal(found, err)
	}
}

func TestManagementKeyChangeDuringCatalogQueryCannotComplete(t *testing.T) {
	for _, change := range []string{"rotate", "clear"} {
		t.Run(change, func(t *testing.T) {
			started := make(chan struct{}, 1)
			release := make(chan struct{})
			var releaseOnce sync.Once
			releaseNow := func() { releaseOnce.Do(func() { close(release) }) }
			defer releaseNow()
			var calls atomic.Int32
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				n := calls.Add(1)
				if n == 1 {
					if r.URL.Path != "/api/v1/models/user" || r.Header.Get("Authorization") != "Bearer management-A" {
						t.Errorf("first request crossed credential purpose: %s", r.URL.Path)
					}
					started <- struct{}{}
					select {
					case <-release:
					case <-r.Context().Done():
						return
					}
				} else if change == "rotate" {
					if r.URL.Path != "/api/v1/models/user" || r.Header.Get("Authorization") != "Bearer management-B" {
						t.Errorf("rotated request crossed credential purpose: %s", r.URL.Path)
					}
				} else if r.URL.Path != "/api/v1/models" || r.Header.Get("Authorization") != "Bearer inference-key" {
					t.Errorf("cleared request crossed credential purpose: %s", r.URL.Path)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"data":[{"id":"openai/m","name":"M"}],"total_count":1}`)
			}))
			t.Cleanup(server.Close)
			s, _, _ := setup(t)
			s.modelHTTPClient = server.Client()
			in := profileInput()
			in.ID, in.ProviderType, in.BaseURL, in.APIKey = "router", "openrouter", server.URL+"/api/v1", "inference-key"
			if _, err := s.SaveProviderProfile(in); err != nil {
				t.Fatal(err)
			}
			if _, err := s.SaveOpenRouterManagementKey("router", "management-A"); err != nil {
				t.Fatal(err)
			}
			type answer struct {
				result modelcatalog.Result
				err    error
			}
			done := make(chan answer, 1)
			go func() {
				result, err := s.QueryHTTPModelCatalog(t.Context(), HTTPModelCatalogQuery{ProfileID: "router"})
				done <- answer{result, err}
			}()
			select {
			case <-started:
			case <-time.After(5 * time.Second):
				t.Fatal("catalog request did not start")
			}
			if change == "rotate" {
				if _, err := s.SaveOpenRouterManagementKey("router", "management-B"); err != nil {
					t.Fatal(err)
				}
			} else if err := s.ClearOpenRouterManagementKey("router"); err != nil {
				t.Fatal(err)
			}
			releaseNow()
			first := <-done
			if first.err != nil || first.result.Status != modelcatalog.StatusPartial || first.result.Complete || first.result.ErrorCode != "catalog_management_changed" {
				t.Fatal(first)
			}
			second, err := s.QueryHTTPModelCatalog(t.Context(), HTTPModelCatalogQuery{ProfileID: "router"})
			if err != nil || !second.Complete || calls.Load() != 2 || second.AccountFiltered != (change == "rotate") {
				t.Fatal(second, err, calls.Load())
			}
		})
	}
}
