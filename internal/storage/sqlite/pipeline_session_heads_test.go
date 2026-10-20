package sqlite

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/persioflexa/harflex/internal/catalog"
)

// Screens that only ask which conversation is a pipeline's latest Coder read the links without their snapshots,
// which hold the whole project's file list.
func TestPipelineSessionHeadsSkipTheSnapshots(t *testing.T) {
	store, _, run := authoringFixture(t)
	run.Kind = "legacy"
	if err := store.CreatePipeline(t.Context(), run); err != nil {
		t.Fatal(err)
	}
	for i, id := range []string{"old-coder", "new-coder"} {
		if err := store.CreateSessionWithSnapshots(t.Context(), catalog.SessionRecord{ID: id, WorkspaceID: run.WorkspaceID, BackendID: "local", Status: "completed", CreatedAt: run.CreatedAt, UpdatedAt: run.UpdatedAt}, nil, "", nil, "agent_session", nil); err != nil {
			t.Fatal(err)
		}
		link := catalog.PipelineSession{ID: id + "-link", PipelineID: run.ID, SessionID: id, Role: "coder", BaselineGitFiles: []string{"a.go"}, BaselineGitFileHashes: map[string]string{"a.go": "h"}, CreatedAt: run.CreatedAt.Add(time.Duration(i) * time.Minute)}
		if i == 1 {
			link.ExecutionSnapshot = json.RawMessage(`{"root":"/copy","sourceRoot":"/project","manifest":[]}`)
		}
		if err := store.LinkPipelineSession(t.Context(), link); err != nil {
			t.Fatal(err)
		}
	}
	heads, err := store.ListPipelineSessionHeads(t.Context(), run.ID, "coder")
	if err != nil || len(heads) != 2 {
		t.Fatalf("heads: %+v %v", heads, err)
	}
	if heads[0].SessionID != "new-coder" || !heads[0].HasExecutionSnapshot || heads[1].SessionID != "old-coder" || heads[1].HasExecutionSnapshot || !heads[0].CreatedAt.After(heads[1].CreatedAt) {
		t.Fatalf("heads newest first with their copy: %+v", heads)
	}
}
