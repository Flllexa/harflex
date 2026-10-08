package application

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/persioflexa/harflex/internal/agentcore"
)

func TestFullAccessIsSavedOnlyWithTheExplicitConfirmation(t *testing.T) {
	s, db, _, session := sessionSetup(t, &fakeProvider{})
	if _, err := s.SetWorkspaceProfile(SetWorkspaceProfileInput{WorkspaceID: session.WorkspaceID, Profile: "full_access"}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("full access without confirmation was accepted: %v", err)
	}
	workspace, err := db.GetWorkspace(t.Context(), session.WorkspaceID)
	if err != nil || workspace.Profile == "full_access" {
		t.Fatalf("an unconfirmed request changed the profile: %+v %v", workspace, err)
	}
	updated, err := s.SetWorkspaceProfile(SetWorkspaceProfileInput{WorkspaceID: session.WorkspaceID, Profile: "full_access", ConfirmFullAccess: true})
	if err != nil || updated.Profile != "full_access" {
		t.Fatalf("confirmed full access: %+v %v", updated, err)
	}
	if workspace, err = db.GetWorkspace(t.Context(), session.WorkspaceID); err != nil || workspace.Profile != "full_access" {
		t.Fatalf("stored workspace = %+v %v", workspace, err)
	}
	// Leaving full access needs no confirmation, and nothing else is selectable by accident.
	if updated, err = s.SetWorkspaceProfile(SetWorkspaceProfileInput{WorkspaceID: session.WorkspaceID, Profile: "ask"}); err != nil || updated.Profile != "ask" {
		t.Fatalf("back to ask: %+v %v", updated, err)
	}
	for _, profile := range []string{"sandbox", "unknown", ""} {
		if _, err := s.SetWorkspaceProfile(SetWorkspaceProfileInput{WorkspaceID: session.WorkspaceID, Profile: profile, ConfirmFullAccess: true}); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("profile %q was accepted: %v", profile, err)
		}
	}
}

// A session opened while the project asked for everything runs shell commands unprompted the moment the
// person chooses full access, and asks again as soon as they leave it.
func TestFullAccessChangesAnOpenSessionWithoutRestartingIt(t *testing.T) {
	provider := &fakeProvider{toolCall: &agentcore.ToolCall{ID: "shell-full", Name: "bash", Arguments: json.RawMessage(`{"command":"echo done > full.txt"}`)}}
	s, db, _, session := sessionSetup(t, provider)
	workspace, err := db.GetWorkspace(t.Context(), session.WorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetWorkspaceProfile(SetWorkspaceProfileInput{WorkspaceID: workspace.ID, Profile: "full_access", ConfirmFullAccess: true}); err != nil {
		t.Fatal(err)
	}
	result, err := s.Prompt(PromptInput{SessionID: session.ID, Text: "Write the marker file"})
	if err != nil || result.Status != RunCompleted || result.Approval != nil {
		t.Fatalf("full access still asked for approval: %+v %v", result, err)
	}
	if _, err := os.Stat(filepath.Join(workspace.Path, "full.txt")); err != nil {
		t.Fatalf("the command did not run: %v", err)
	}

	if _, err := s.SetWorkspaceProfile(SetWorkspaceProfileInput{WorkspaceID: workspace.ID, Profile: "trusted_workspace"}); err != nil {
		t.Fatal(err)
	}
	provider.toolCall = &agentcore.ToolCall{ID: "shell-again", Name: "bash", Arguments: json.RawMessage(`{"command":"echo again > again.txt"}`)}
	result, err = s.Prompt(PromptInput{SessionID: session.ID, Text: "Write another marker"})
	if err != nil || result.Status != RunAwaitingApproval || result.Approval == nil {
		t.Fatalf("a shell command ran unprompted after leaving full access: %+v %v", result, err)
	}
	if _, err := os.Stat(filepath.Join(workspace.Path, "again.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the command ran before approval: %v", err)
	}
}
