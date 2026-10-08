package application

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/persioflexa/harflex/internal/agentcore"
	"github.com/persioflexa/harflex/internal/providers/openai"
)

func TestLocalNoKeyCreatesSessionWithoutVaultRead(t *testing.T) {
	for _, providerType := range []string{"ollama", "lm_studio"} {
		t.Run(providerType, func(t *testing.T) {
			s, _, vault := setup(t)
			in := profileInput()
			in.ProviderType, in.BaseURL, in.APIKey = providerType, "http://127.0.0.1:11434/v1", ""
			if _, err := s.SaveProviderProfile(in); err != nil {
				t.Fatal(err)
			}
			backends := s.ListBackends()
			if len(backends) != 1 || !backends[0].Available {
				t.Fatalf("local backend unavailable: %+v", backends)
			}
			provider := &fakeProvider{}
			s.providerFactory = func(cfg openai.Config) (agentcore.Provider, error) {
				key, err := cfg.APIKey(t.Context())
				if err != nil || key != "" {
					t.Fatalf("keyless profile supplied key: %v", err)
				}
				return provider, nil
			}
			workspace, err := s.OpenWorkspace(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			session, err := s.CreateSession(CreateSessionInput{WorkspaceID: workspace.ID, BackendID: in.ID})
			if err != nil {
				t.Fatal(err)
			}
			if result, err := s.Prompt(PromptInput{SessionID: session.ID, Text: "hello"}); err != nil || result.Status != RunCompleted {
				t.Fatal(result, err)
			}
			if vault.gets != 0 {
				t.Fatal("keyless profile read the vault")
			}
		})
	}
}

func TestLegacyRemoteHTTPIsListedButCannotStartOrResumeRun(t *testing.T) {
	s, db, vault := setup(t)
	provider := &fakeProvider{}
	factoryCalls := 0
	s.providerFactory = func(cfg openai.Config) (agentcore.Provider, error) {
		factoryCalls++
		provider.key = cfg.APIKey
		return provider, nil
	}
	in := profileInput()
	in.BaseURL = "https://legacy.example/v1"
	if _, err := s.SaveProviderProfile(in); err != nil {
		t.Fatal(err)
	}
	workspace, err := s.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	old, err := s.CreateSession(CreateSessionInput{WorkspaceID: workspace.ID, BackendID: in.ID})
	if err != nil {
		t.Fatal(err)
	}
	if result, err := s.Prompt(PromptInput{SessionID: old.ID, Text: "historical"}); err != nil || result.Status != RunCompleted {
		t.Fatal(result, err)
	}
	if _, err := s.SaveSettings(SaveSettingsInput{DefaultBackendID: in.ID}); err != nil {
		t.Fatal(err)
	}
	profile, err := db.GetProviderProfile(t.Context(), in.ID)
	if err != nil {
		t.Fatal(err)
	}
	profile.BaseURL = "http://legacy.example/v1" // Emulates a persisted pre-migration endpoint.
	if err := db.UpsertProviderProfile(t.Context(), profile); err != nil {
		t.Fatal(err)
	}
	listed, err := s.ListProviderProfiles()
	if err != nil || len(listed) != 1 || !listed[0].EndpointBlocked || listed[0].BaseURL != profile.BaseURL {
		t.Fatalf("legacy profile hidden: %+v %v", listed, err)
	}
	backends := s.ListBackends()
	if len(backends) != 1 || backends[0].Available {
		t.Fatalf("unsafe backend available: %+v", backends)
	}
	gets := vault.gets
	if _, err := s.SaveSettings(SaveSettingsInput{DefaultBackendID: in.ID}); !errors.Is(err, ErrProviderEndpointBlocked) || ErrorCode(err) != "provider_endpoint_blocked" {
		t.Fatalf("unsafe default accepted: %v", err)
	}
	if settings, err := s.GetSettings(); err != nil || settings.DefaultBackendID != in.ID {
		t.Fatalf("saved default changed: %+v %v", settings, err)
	}
	if _, err := s.CreateSession(CreateSessionInput{WorkspaceID: workspace.ID, BackendID: in.ID}); !errors.Is(err, ErrProviderEndpointBlocked) {
		t.Fatalf("unsafe new run admitted: %v", err)
	}
	if vault.gets != gets || factoryCalls != 1 {
		t.Fatal("blocked run reached vault or provider factory")
	}
	reopened := NewService(t.Context(), Dependencies{Store: db, Secrets: vault, External: map[string]ExternalBackend{}})
	view, err := reopened.OpenSession(OpenSessionInput{SessionID: old.ID, WorkspaceID: workspace.ID})
	if err != nil || view.Resumable {
		t.Fatalf("history not read-only: %+v %v", view, err)
	}
	events, err := reopened.ListEvents(ListEventsInput{SessionID: old.ID, Limit: 100})
	if err != nil || len(events) == 0 {
		t.Fatalf("history unavailable: %d events, %v", len(events), err)
	}
	if _, err := reopened.Prompt(PromptInput{SessionID: old.ID, Text: "do not send"}); !errors.Is(err, ErrProviderEndpointBlocked) {
		t.Fatalf("read-only session resumed: %v", err)
	}
	if vault.gets != gets {
		t.Fatal("read-only history accessed the vault")
	}
}

func TestLegacyRemoteHTTPReopenedDelegatedPromptPreservesBudget(t *testing.T) {
	s, db, vault := setup(t)
	s.providerFactory = func(openai.Config) (agentcore.Provider, error) { return &fakeProvider{}, nil }
	in := profileInput()
	in.BaseURL = "https://legacy.example/v1"
	if _, err := s.SaveProviderProfile(in); err != nil {
		t.Fatal(err)
	}
	workspace, err := s.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	agent, err := s.SaveAgent(SaveAgentInput{Name: "Reviewer", Instructions: "Review", BackendID: in.ID, AllowedTools: []string{"read"}})
	if err != nil {
		t.Fatal(err)
	}
	parent, err := s.CreateSession(CreateSessionInput{WorkspaceID: workspace.ID, BackendID: in.ID})
	if err != nil {
		t.Fatal(err)
	}
	child, err := s.DelegateToAgent(DelegateToAgentInput{ParentSessionID: parent.ID, AgentID: agent.ID, RequestID: "blocked-child-0001", Prompt: "Review"})
	if err != nil {
		t.Fatal(err)
	}
	if result, err := s.Prompt(PromptInput{SessionID: child.Session.ID, Text: "historical"}); err != nil || result.Status != RunCompleted {
		t.Fatal(result, err)
	}
	links, err := s.ListDelegations(parent.ID)
	if err != nil || len(links) != 1 || links[0].PromptCount != 1 {
		t.Fatalf("historical prompt count: %+v %v", links, err)
	}
	profile, err := db.GetProviderProfile(t.Context(), in.ID)
	if err != nil {
		t.Fatal(err)
	}
	profile.BaseURL = "http://legacy.example/v1"
	if err := db.UpsertProviderProfile(t.Context(), profile); err != nil {
		t.Fatal(err)
	}
	Shutdown(s)
	reopened := NewService(t.Context(), Dependencies{Store: db, Secrets: vault, External: map[string]ExternalBackend{}})
	view, err := reopened.OpenSession(OpenSessionInput{SessionID: child.Session.ID, WorkspaceID: workspace.ID})
	if err != nil || view.Resumable {
		t.Fatalf("delegated history not read-only: %+v %v", view, err)
	}
	gets := vault.gets
	if _, err := reopened.Prompt(PromptInput{SessionID: child.Session.ID, Text: "blocked"}); !errors.Is(err, ErrProviderEndpointBlocked) {
		t.Fatalf("blocked delegated prompt: %v", err)
	}
	links, err = reopened.ListDelegations(parent.ID)
	if err != nil || len(links) != 1 || links[0].PromptCount != 1 {
		t.Fatalf("refused prompt consumed budget: %+v %v", links, err)
	}
	if vault.gets != gets {
		t.Fatal("refused delegated prompt read the vault")
	}
}

func TestReclassifiedLocalInferenceOmitsAuthorization(t *testing.T) {
	var authPresent atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, present := r.Header["Authorization"]; present {
			authPresent.Store(true)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\ndata: [DONE]\n\n")
	}))
	t.Cleanup(server.Close)
	s, db, vault := setup(t)
	in := profileInput()
	in.BaseURL = "https://remote.example/v1"
	if _, err := s.SaveProviderProfile(in); err != nil {
		t.Fatal(err)
	}
	before, err := db.GetProviderProfile(t.Context(), in.ID)
	if err != nil {
		t.Fatal(err)
	}
	in.ProviderType, in.BaseURL, in.APIKey, in.ClearCredential = "lm_studio", server.URL+"/v1", "", true
	if _, err := s.SaveProviderProfile(in); err != nil {
		t.Fatal(err)
	}
	after, err := db.GetProviderProfile(t.Context(), in.ID)
	if err != nil || after.CredentialProvider != "" || after.CredentialAccount != "" {
		t.Fatal("active key not cleared", err)
	}
	refs, err := db.ListCredentialReferences(t.Context())
	if err != nil || len(refs) != 1 || refs[0].Account != before.CredentialAccount || vault.deletes != 0 {
		t.Fatal("historical key lost", err)
	}
	workspace, err := s.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	session, err := s.CreateSession(CreateSessionInput{WorkspaceID: workspace.ID, BackendID: in.ID})
	if err != nil {
		t.Fatal(err)
	}
	if result, err := s.Prompt(PromptInput{SessionID: session.ID, Text: "hello"}); err != nil || result.Status != RunCompleted {
		t.Fatal(result, err)
	}
	if authPresent.Load() {
		t.Fatal("reclassified local inference sent Authorization")
	}
	if vault.gets != 0 {
		t.Fatal("cleared profile read the vault")
	}
}
