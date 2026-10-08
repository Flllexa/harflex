package application

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMalformedAdvertisedEffortBlocksAutomaticBrainstormPersistence(t *testing.T) {
	for _, effort := range []string{"", " ", " high"} {
		t.Run(fmt.Sprintf("effort=%q", effort), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprintf(w, `{"models":[{"type":"llm","key":"chosen","loaded_instances":[],"capabilities":{"reasoning":{"allowed_options":[%q]}}}]}`, effort)
			}))
			t.Cleanup(server.Close)
			s, db, _ := setup(t)
			s.modelHTTPClient = server.Client()
			profile := profileInput()
			profile.ID, profile.ProviderType, profile.BaseURL, profile.APIKey = "lm", "lm_studio", server.URL+"/v1", ""
			if _, err := s.SaveProviderProfile(profile); err != nil {
				t.Fatal(err)
			}
			listed, err := s.QueryHTTPModelCatalog(t.Context(), HTTPModelCatalogQuery{ProfileID: profile.ID})
			if err != nil || listed.Complete {
				t.Fatal("malformed catalog was not rejected", err)
			}
			workspace, err := s.OpenWorkspace(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			pipeline, err := s.CreateAuthoringPipeline(CreateAuthoringPipelineInput{WorkspaceID: workspace.ID, RequestID: "invalid-effort-pipeline", Discovery: "Preserve user Discovery"})
			if err != nil {
				t.Fatal(err)
			}
			selection := apiSelectionInput(listed, profile.ID, "chosen", 256)
			selection.ConfirmJITLoad = true
			_, err = s.StartBrainstorming(StartBrainstormingInput{PipelineID: pipeline.ID, PipelineRevision: pipeline.Revision, DiscoveryVersion: 1, RequestID: "invalid-effort-start", Selection: selection})
			if err == nil {
				t.Fatal("malformed Automatic selection admitted")
			}
			if _, err := db.GetBrainstormingByPipeline(t.Context(), pipeline.ID, 1); !errors.Is(err, sql.ErrNoRows) {
				t.Fatal("invalid catalog persisted a run", err)
			}
		})
	}
}
