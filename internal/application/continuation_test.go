package application

import (
	"testing"
)

// The Coder through a CLI that Harflex mediates keeps its history in Harflex's journal: it continues after its run.
func TestHostCoderConversationContinuesAfterItsRun(t *testing.T) {
	s, _, run, _, _ := codexHostCodeFixture(t, "trusted_workspace", `{"reply":"Pronto.","toolCalls":[]}`, `{"reply":"Ajustado.","toolCalls":[]}`)
	coder, err := s.CreatePipelineSession(CreatePipelineSessionInput{PipelineID: run.ID, BackendID: "codex", Role: "coder", ConfirmWorkspaceCopy: true})
	if err != nil {
		t.Fatal(err)
	}
	if result, err := s.Prompt(PromptInput{SessionID: coder.Session.ID, Text: coder.Prompt}); err != nil || result.Status != "completed" {
		t.Fatalf("first run = %+v %v", result, err)
	}
	record, err := s.store.GetSession(t.Context(), coder.Session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if resumable, _, err := s.sessionContinuation(record); !resumable || err != nil {
		t.Fatalf("the Coder conversation closed after its run: %v %v", resumable, err)
	}
	if result, err := s.Prompt(PromptInput{SessionID: coder.Session.ID, Text: "Ajuste o título"}); err != nil || result.Status != "completed" {
		t.Fatalf("second message in the same chat = %+v %v", result, err)
	}
}

// QA after its verdict still talks: the recorded verdict stays as it was.
func TestQAConversationContinuesAfterItsVerdict(t *testing.T) {
	s, db, _, session := sessionSetup(t, &fakeProvider{})
	record, err := db.GetSession(t.Context(), session.ID)
	if err != nil {
		t.Fatal(err)
	}
	record.Mode, record.Status = "evaluation", "completed"
	if err := db.UpsertSession(t.Context(), record); err != nil {
		t.Fatal(err)
	}
	if resumable, _, err := s.sessionContinuation(record); !resumable || err != nil {
		t.Fatalf("the QA conversation closed after its verdict: %v %v", resumable, err)
	}
}
