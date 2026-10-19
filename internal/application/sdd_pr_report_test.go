package application

import "testing"

// The PRs conversation often runs on a CLI (Claude Code, Codex), whose runs and replies are journaled as external
// events; its final reply is the stage's evidence all the same.
func TestThePRsReportReadsAConversationRunOnACLI(t *testing.T) {
	s, db, _ := setup(t)
	workspace, err := s.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveProviderProfile(profileInput()); err != nil {
		t.Fatal(err)
	}
	chat, err := s.CreateDirectSession(CreateDirectSessionInput{WorkspaceID: workspace.ID, BackendID: "local", Reason: "PRs"})
	if err != nil {
		t.Fatal(err)
	}
	const report = "O PR #18 está aprovado e não havia nada novo para corrigir."
	for _, event := range []struct {
		kind string
		data any
	}{
		{"external.run.started", map[string]string{"adapter": "claude"}},
		{"external.event", map[string]string{"type": "assistant.message", "messageId": "m1", "mode": "delta", "text": "O PR #18 está aprovado"}},
		{"external.event", map[string]string{"type": "assistant.message", "messageId": "m1", "mode": "delta", "text": " e não havia nada novo para corrigir."}},
		{"external.run.completed", map[string]string{"adapter": "claude"}},
	} {
		if _, err := db.Append(t.Context(), chat.ID, "agent_session", event.kind, event.data); err != nil {
			t.Fatal(err)
		}
	}
	completed, got, err := s.pipelinePRReport(chat.ID)
	if err != nil || !completed || got != report {
		t.Fatalf("PRs report from a CLI conversation: completed=%v report=%q err=%v", completed, got, err)
	}
}
