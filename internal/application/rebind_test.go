package application

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/persioflexa/harflex/internal/catalog"

	"github.com/persioflexa/harflex/internal/agentcore"
	"github.com/persioflexa/harflex/internal/providers/openai"
	"github.com/persioflexa/harflex/internal/storage/sqlite"
)

// Editing the provider in Settings used to leave every chat of it read-only; it now goes on with the same model.
func TestEditedProfileKeepsTheConversation(t *testing.T) {
	provider := &fakeProvider{}
	s, db, vault, session := sessionSetup(t, provider)
	if _, err := s.Prompt(PromptInput{SessionID: session.ID, Text: "primeiro"}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond)
	edited := profileInput()
	edited.Name = "Local (editado)"
	if _, err := s.SaveProviderProfile(edited); err != nil {
		t.Fatal(err)
	}
	next := &fakeProvider{}
	reopened := NewService(t.Context(), Dependencies{Store: db, Secrets: vault, External: map[string]ExternalBackend{}, ProviderFactory: func(c openai.Config) (agentcore.Provider, error) { next.key = c.APIKey; return next, nil }})
	opened, err := reopened.OpenSession(OpenSessionInput{SessionID: session.ID, WorkspaceID: session.WorkspaceID})
	if err != nil || !opened.Resumable {
		t.Fatalf("the chat closed after its provider was edited: %+v %v", opened, err)
	}
	if result, err := reopened.Prompt(PromptInput{SessionID: session.ID, Text: "segundo"}); err != nil || result.Status != RunCompleted {
		t.Fatalf("the chat did not go on: %+v %v", result, err)
	}
	if got := len(next.request.Messages); got < 3 {
		t.Fatalf("the history did not come along: %d messages", got)
	}
}

func TestAProviderEditedWhileTheChatIsOpenDoesNotStopIt(t *testing.T) {
	s, _, _, session := sessionSetup(t, &fakeProvider{})
	if _, err := s.Prompt(PromptInput{SessionID: session.ID, Text: "primeiro"}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond)
	edited := profileInput()
	edited.Name = "Local (editado)"
	if _, err := s.SaveProviderProfile(edited); err != nil {
		t.Fatal(err)
	}
	if result, err := s.Prompt(PromptInput{SessionID: session.ID, Text: "segundo"}); err != nil || result.Status != RunCompleted {
		t.Fatalf("the open chat stopped when its provider was edited: %+v %v", result, err)
	}
}

func TestDeletedProviderLeavesTheChatReadOnly(t *testing.T) {
	s, db, _, session := sessionSetup(t, &fakeProvider{})
	if _, err := db.DB().Exec(`DELETE FROM provider_profiles WHERE id='local'`); err != nil {
		t.Skipf("cannot remove the profile in this schema: %v", err)
	}
	opened, err := s.OpenSession(OpenSessionInput{SessionID: session.ID, WorkspaceID: session.WorkspaceID})
	if err == nil && opened.Resumable {
		t.Fatal("a chat whose provider is gone claims it can go on")
	}
}

// selectedChat is a chat whose model choice ("chosen") was checked against a catalog server; models is what that
// server lists from then on.
func selectedChat(t *testing.T, models *atomic.Value) (*Service, *sqlite.Store, SaveProviderProfileInput, catalog.SessionRecord) {
	t.Helper()
	models.Store(`{"data":[{"id":"chosen"},{"id":"fallback"}]}`)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, models.Load().(string))
	}))
	t.Cleanup(server.Close)
	s, db, _ := setup(t)
	s.modelHTTPClient = server.Client()
	in := profileInput()
	in.ID, in.ProviderType, in.BaseURL, in.Model = "api", "openai", server.URL+"/v1", "fallback"
	if _, err := s.SaveProviderProfile(in); err != nil {
		t.Fatal(err)
	}
	result, err := s.QueryHTTPModelCatalog(t.Context(), HTTPModelCatalogQuery{ProfileID: in.ID})
	if err != nil {
		t.Fatal(err)
	}
	selection, err := s.prepareAPIModelSelection(t.Context(), apiSelectionInput(result, in.ID, "chosen", 321))
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := s.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	record := catalog.SessionRecord{ID: "selected-chat", WorkspaceID: workspace.ID, BackendID: in.ID, Status: "completed", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	selection.SessionID, selection.WorkspacePath = record.ID, workspace.Path
	if err := db.CreateSessionWithSnapshots(t.Context(), record, nil, "", nil, "agent_session", selection); err != nil {
		t.Fatal(err)
	}
	return s, db, in, record
}

// An edit that leaves the model listed keeps the chat on that same model.
func TestAnEditedProfileKeepsTheChatOnItsModel(t *testing.T) {
	var models atomic.Value
	s, db, in, record := selectedChat(t, &models)
	in.Name = "API (editado)"
	if _, err := s.SaveProviderProfile(in); err != nil {
		t.Fatal(err)
	}
	if err := s.rebindSession(&record); err != nil {
		t.Fatal(err)
	}
	if stored, err := db.GetSessionModelSelection(t.Context(), record.ID); err != nil || stored.ModelID != "chosen" {
		t.Fatalf("the chat changed model after an edit: %+v %v", stored, err)
	}
}

// A model the provider no longer lists gives way to the profile's own model.
func TestAChatWhoseModelLeftTheProviderTakesTheProfileModel(t *testing.T) {
	var models atomic.Value
	s, db, in, record := selectedChat(t, &models)
	models.Store(`{"data":[{"id":"fallback"}]}`)
	in.Name = "API (editado)"
	if _, err := s.SaveProviderProfile(in); err != nil {
		t.Fatal(err)
	}
	if err := s.rebindSession(&record); err != nil {
		t.Fatal(err)
	}
	if stored, err := db.GetSessionModelSelection(t.Context(), record.ID); err != nil || stored.ModelID != "fallback" {
		t.Fatalf("the chat did not take the profile's model: %+v %v", stored, err)
	}
}

// A chat whose model choice went to one address never follows its profile to another, even when nothing recorded
// the move (a profile moved before the app kept that date).
func TestARecordedDestinationKeepsTheChatFromFollowingAMove(t *testing.T) {
	var models atomic.Value
	s, db, in, record := selectedChat(t, &models)
	other := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":[{"id":"chosen"}]}`)
	}))
	t.Cleanup(other.Close)
	s.modelHTTPClient = other.Client()
	in.BaseURL = other.URL + "/v1"
	if _, err := s.SaveProviderProfile(in); err != nil {
		t.Fatal(err)
	}
	if _, err := db.DB().Exec(`UPDATE provider_profiles SET base_url_changed_at = '' WHERE id = 'api'`); err != nil {
		t.Fatal(err)
	}
	if err := s.rebindSession(&record); !errors.Is(err, ErrBackendChanged) {
		t.Fatalf("the chat followed its profile to another address: %v", err)
	}
}
