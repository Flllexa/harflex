package application

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/persioflexa/harflex/internal/catalog"
)

type sharedCapabilityStore struct {
	Store
	selection catalog.ModelSelection
}

func (s sharedCapabilityStore) GetSessionModelSelection(context.Context, string) (catalog.ModelSelection, error) {
	return s.selection, nil
}

func reasoningReadbackFixture(t *testing.T, effort string) (*Service, *catalog.ModelSelection) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"models":[{"type":"llm","key":"chosen","loaded_instances":[],"capabilities":{"reasoning":{"allowed_options":["low","high"],"default":"low"}}}]}`)
	}))
	t.Cleanup(server.Close)
	s, db, _ := setup(t)
	s.modelHTTPClient = server.Client()
	profile := profileInput()
	profile.ID, profile.ProviderType, profile.BaseURL, profile.APIKey = "lm", "lm_studio", server.URL+"/v1", ""
	if _, err := s.SaveProviderProfile(profile); err != nil {
		t.Fatal(err)
	}
	result, err := s.QueryHTTPModelCatalog(t.Context(), HTTPModelCatalogQuery{ProfileID: profile.ID})
	if err != nil || !result.Complete {
		t.Fatal("catalog unavailable", err)
	}
	input := apiSelectionInput(result, profile.ID, "chosen", 321)
	input.ReasoningEffort, input.ConfirmJITLoad = effort, true
	selection, err := s.prepareAPIModelSelection(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := s.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	record := catalog.SessionRecord{ID: "readback-capability", WorkspaceID: workspace.ID, BackendID: profile.ID, Mode: "sdd_readonly", Status: "ready", CreatedAt: now, UpdatedAt: now}
	selection.SessionID, selection.WorkspacePath = record.ID, workspace.Path
	if err := db.CreateSessionWithSnapshots(t.Context(), record, nil, "", nil, "agent_session", selection); err != nil {
		t.Fatal(err)
	}
	return s, selection
}

func TestAutomaticSelectionPersistsAdvertisedReasoningCapabilities(t *testing.T) {
	s, selected := reasoningReadbackFixture(t, "")
	want := []string{"low", "high"}
	if !slices.Equal(selected.SupportedReasoningEfforts, want) {
		t.Fatalf("Automatic dropped capability: %v", selected.SupportedReasoningEfforts)
	}
	stored, err := s.store.GetSessionModelSelection(t.Context(), selected.SessionID)
	if err != nil || !slices.Equal(stored.SupportedReasoningEfforts, want) {
		t.Fatal("capability not persisted", stored.SupportedReasoningEfforts, err)
	}
}

func TestSessionModelSelectionCapabilityReadbackIsDefensiveAndPublic(t *testing.T) {
	for _, effort := range []string{"", "high"} {
		t.Run("effort="+effort, func(t *testing.T) {
			s, selected := reasoningReadbackFixture(t, effort)
			persistedStore := s.store
			s.store = sharedCapabilityStore{Store: persistedStore, selection: *selected}
			dto, err := s.GetSessionModelSelection(selected.SessionID)
			if err != nil {
				t.Fatal(err)
			}
			if dto.ReasoningEffort != effort || !slices.Equal(dto.SupportedReasoningEfforts, []string{"low", "high"}) {
				t.Fatal("public capability missing", dto)
			}
			raw, err := json.Marshal(dto)
			if err != nil {
				t.Fatal(err)
			}
			for _, denied := range []string{selected.CredentialIdentity, "credentialIdentity", "credentialToken", "apiKey"} {
				if denied != "" && strings.Contains(string(raw), denied) {
					t.Fatal("private credential material exposed")
				}
			}
			dto.SupportedReasoningEfforts[0] = "corrupted"
			again, err := s.GetSessionModelSelection(selected.SessionID)
			if err != nil || !slices.Equal(again.SupportedReasoningEfforts, []string{"low", "high"}) {
				t.Fatal("public readback mutated", again, err)
			}
			stored, err := persistedStore.GetSessionModelSelection(t.Context(), selected.SessionID)
			if err != nil || !slices.Equal(stored.SupportedReasoningEfforts, []string{"low", "high"}) {
				t.Fatal("Store snapshot mutated", stored.SupportedReasoningEfforts, err)
			}
		})
	}
}
