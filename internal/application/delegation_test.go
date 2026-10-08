package application

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/persioflexa/harflex/internal/agentcore"
	"github.com/persioflexa/harflex/internal/catalog"
	"github.com/persioflexa/harflex/internal/externalagent"
	"github.com/persioflexa/harflex/internal/providers/openai"
)

func TestDelegationDTOBoundsTaskPreview(t *testing.T) {
	task := strings.Repeat("ação ", 250)
	link := catalog.Delegation{TaskPrompt: task}
	preview := delegationDTO(link).TaskPrompt
	if utf8.RuneCountInString(preview) != 281 || !strings.HasSuffix(preview, "…") || link.TaskPrompt != task {
		t.Fatalf("delegated task preview was not bounded safely: runes=%d", utf8.RuneCountInString(preview))
	}
}

func TestDelegatedPromptBudgetSurvivesRestartAndDoesNotConstrainParent(t *testing.T) {
	_, db, vault := setup(t)
	provider := &fakeProvider{}
	factory := func(openai.Config) (agentcore.Provider, error) { return provider, nil }
	s := NewService(t.Context(), Dependencies{Store: db, Secrets: vault, External: map[string]ExternalBackend{}, ProviderFactory: factory})
	workspace, err := s.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveProviderProfile(profileInput()); err != nil {
		t.Fatal(err)
	}
	agent, err := s.SaveAgent(SaveAgentInput{Name: "Revisor", Instructions: "Revise", BackendID: "local", AllowedTools: []string{"read"}})
	if err != nil {
		t.Fatal(err)
	}
	parent, err := s.CreateDirectSession(CreateDirectSessionInput{WorkspaceID: workspace.ID, BackendID: "local", Reason: "Consulta pontual"})
	if err != nil {
		t.Fatal(err)
	}
	child, err := s.DelegateToAgent(DelegateToAgentInput{ParentSessionID: parent.ID, AgentID: agent.ID, RequestID: "budget-child-0001", Prompt: "Revise"})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if result, err := s.Prompt(PromptInput{SessionID: child.Session.ID, Text: "Rodada"}); err != nil || result.Status != RunCompleted {
			t.Fatalf("prompt %d: %+v %v", i, result, err)
		}
	}
	Shutdown(s)
	next := NewService(t.Context(), Dependencies{Store: db, Secrets: vault, External: map[string]ExternalBackend{}, ProviderFactory: factory})
	if err := next.Recover(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := next.OpenSession(OpenSessionInput{SessionID: child.Session.ID, WorkspaceID: workspace.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := next.OpenSession(OpenSessionInput{SessionID: parent.ID, WorkspaceID: workspace.ID}); err != nil {
		t.Fatal(err)
	}
	links, err := next.ListDelegations(parent.ID)
	if err != nil || len(links) != 1 || links[0].PromptCount != 2 || links[0].PromptLimit != 3 || links[0].TimeoutSeconds != 300 {
		t.Fatalf("budget readback: %+v %v", links, err)
	}
	if result, err := next.Prompt(PromptInput{SessionID: child.Session.ID, Text: "Última rodada"}); err != nil || result.Status != RunCompleted {
		t.Fatalf("third prompt: %+v %v", result, err)
	}
	if _, err := next.Prompt(PromptInput{SessionID: child.Session.ID, Text: "Proibida"}); !errors.Is(err, ErrDelegationBudgetExceeded) {
		t.Fatalf("fourth prompt: %v", err)
	}
	links, err = next.ListDelegations(parent.ID)
	if err != nil || len(links) != 1 || links[0].PromptCount != 3 {
		t.Fatalf("final budget readback: %+v %v", links, err)
	}
	if result, err := next.Prompt(PromptInput{SessionID: parent.ID, Text: "Quarta livre"}); err != nil || result.Status != RunCompleted {
		t.Fatalf("parent must be independent: %+v %v", result, err)
	}
}

type waitingExternal struct {
	fakeExternal
	cancelled chan struct{}
}

func (a waitingExternal) Run(ctx context.Context, _ externalagent.Request) (<-chan externalagent.Event, <-chan error) {
	events := make(chan externalagent.Event)
	errors := make(chan error, 1)
	go func() { <-ctx.Done(); errors <- ctx.Err(); close(events); close(errors); close(a.cancelled) }()
	return events, errors
}

func TestDelegatedRunTimeoutCancelsGoAndCLI(t *testing.T) {
	for _, kind := range []string{"go", "cli"} {
		t.Run(kind, func(t *testing.T) {
			_, db, vault := setup(t)
			provider := &fakeProvider{block: true, started: make(chan struct{})}
			adapter := waitingExternal{fakeExternal: fakeExternal{available: true}, cancelled: make(chan struct{})}
			s := NewService(t.Context(), Dependencies{Store: db, Secrets: vault, External: map[string]ExternalBackend{"codex": adapter}, ProviderFactory: func(openai.Config) (agentcore.Provider, error) { return provider, nil }})
			s.delegationTimeout = 25 * time.Millisecond
			workspace, err := s.OpenWorkspace(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			backendID := "codex"
			if kind == "go" {
				backendID = "local"
				if _, err := s.SaveProviderProfile(profileInput()); err != nil {
					t.Fatal(err)
				}
			}
			agent, err := s.SaveAgent(SaveAgentInput{Name: "Revisor", Instructions: "Revise", BackendID: backendID, AllowedTools: []string{"read"}})
			if err != nil {
				t.Fatal(err)
			}
			parent, err := s.CreateDirectSession(CreateDirectSessionInput{WorkspaceID: workspace.ID, BackendID: backendID, Reason: "Consulta pontual"})
			if err != nil {
				t.Fatal(err)
			}
			child, err := s.DelegateToAgent(DelegateToAgentInput{ParentSessionID: parent.ID, AgentID: agent.ID, RequestID: "timeout-child-0001", Prompt: "Revise"})
			if err != nil {
				t.Fatal(err)
			}
			started := time.Now()
			result, err := s.Prompt(PromptInput{SessionID: child.Session.ID, Text: "Revise"})
			if err != nil || result.Status != RunCancelled || time.Since(started) > time.Second {
				t.Fatalf("timeout: %+v %v", result, err)
			}
			if kind == "cli" {
				select {
				case <-adapter.cancelled:
				case <-time.After(time.Second):
					t.Fatal("CLI context was not cancelled")
				}
			}
			links, err := s.ListDelegations(parent.ID)
			if err != nil || len(links) != 1 || links[0].PromptCount != 1 || links[0].Status != "cancelled" {
				t.Fatalf("timeout readback: %+v %v", links, err)
			}
		})
	}
}

func TestDelegationPersistsRedactedTaskAndRetriesWithoutDuplicateChild(t *testing.T) {
	s, db, vault := setup(t)
	workspace, err := s.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveProviderProfile(profileInput()); err != nil {
		t.Fatal(err)
	}
	agent, err := s.SaveAgent(SaveAgentInput{Name: "Analista", Instructions: "Leia", BackendID: "local", AllowedTools: []string{"read"}})
	if err != nil {
		t.Fatal(err)
	}
	parent, err := s.CreateDirectSession(CreateDirectSessionInput{WorkspaceID: workspace.ID, BackendID: "local", Reason: "Trabalho exploratório"})
	if err != nil {
		t.Fatal(err)
	}
	prompt := "Revise o arquivo com " + profileInput().APIKey + " " + strings.Repeat("contexto ", 60) + "marcador-final"
	first, err := s.DelegateToAgent(DelegateToAgentInput{ParentSessionID: parent.ID, AgentID: agent.ID, RequestID: "recover-child-0001", Prompt: prompt})
	if err != nil {
		t.Fatal(err)
	}
	if !first.Created {
		t.Fatal("new task was not marked as created")
	}
	links, err := s.ListDelegations(parent.ID)
	if err != nil || len(links) != 1 {
		t.Fatalf("links: %+v %v", links, err)
	}
	var task string
	if err := db.DB().QueryRowContext(t.Context(), "SELECT task_prompt FROM session_delegations WHERE child_session_id=?", first.Session.ID).Scan(&task); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(task, "[REDACTED]") || strings.Contains(task, profileInput().APIKey) || utf8.RuneCountInString(task) > 281 {
		t.Fatalf("task was not redacted: %q", task)
	}
	if links[0].TaskPrompt != task {
		t.Fatalf("task readback: %+v", links[0])
	}
	events, err := db.ListAfterLimit(t.Context(), first.Session.ID, 0, 20)
	if err != nil || len(events) != 1 || events[0].Type != "subagent.prompt.queued" || !bytes.Contains(events[0].Data, []byte("[REDACTED]")) || !bytes.Contains(events[0].Data, []byte("marcador-final")) || bytes.Contains(events[0].Data, []byte(profileInput().APIKey)) {
		t.Fatalf("child events: %+v %v", events, err)
	}
	Shutdown(s)
	second := NewService(t.Context(), Dependencies{Store: db, Secrets: vault, External: map[string]ExternalBackend{}, ProviderFactory: func(openai.Config) (agentcore.Provider, error) { return &fakeProvider{}, nil }})
	if err := second.Recover(t.Context()); err != nil {
		t.Fatal(err)
	}
	if opened, err := second.OpenSession(OpenSessionInput{SessionID: first.Session.ID, WorkspaceID: workspace.ID}); err != nil || opened.ID != first.Session.ID {
		t.Fatalf("open queued child after restart: %+v %v", opened, err)
	}
	again, err := second.DelegateToAgent(DelegateToAgentInput{ParentSessionID: parent.ID, AgentID: agent.ID, RequestID: "recover-child-0001", Prompt: prompt})
	if err != nil || again.Session.ID != first.Session.ID {
		t.Fatalf("retry duplicated delegation: %+v %v", again, err)
	}
	if again.Created {
		t.Fatal("retry must require explicit user action before running")
	}
	if _, err := second.DelegateToAgent(DelegateToAgentInput{ParentSessionID: parent.ID, AgentID: agent.ID, RequestID: "recover-child-0001", Prompt: "Outra tarefa"}); !errors.Is(err, ErrDelegationRequestConflict) {
		t.Fatalf("same request ID accepted different task: %v", err)
	}
	newTask, err := second.DelegateToAgent(DelegateToAgentInput{ParentSessionID: parent.ID, AgentID: agent.ID, RequestID: "recover-child-0002", Prompt: prompt})
	if err != nil || !newTask.Created || newTask.Session.ID == first.Session.ID {
		t.Fatalf("new request must create an independent child: %+v %v", newTask, err)
	}
	links, err = second.ListDelegations(parent.ID)
	if err != nil || len(links) != 2 {
		t.Fatalf("retry links: %+v %v", links, err)
	}
	events, err = db.ListAfterLimit(t.Context(), first.Session.ID, 0, 20)
	if err != nil || len(events) != 1 {
		t.Fatalf("retry silently ran task: %+v %v", events, err)
	}
	if result, err := second.Prompt(PromptInput{SessionID: first.Session.ID, Text: prompt}); err != nil || result.Status != RunCompleted {
		t.Fatalf("explicit resumed prompt: %+v %v", result, err)
	}
}

func TestDelegationFailureDoesNotLeaveOrphanSessionOrTask(t *testing.T) {
	s, db, _ := setup(t)
	workspace, err := s.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveProviderProfile(profileInput()); err != nil {
		t.Fatal(err)
	}
	agent, err := s.SaveAgent(SaveAgentInput{Name: "Analista", Instructions: "Leia", BackendID: "local", AllowedTools: []string{"read"}})
	if err != nil {
		t.Fatal(err)
	}
	parent, err := s.CreateDirectSession(CreateDirectSessionInput{WorkspaceID: workspace.ID, BackendID: "local", Reason: "Análise breve"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.DB().ExecContext(t.Context(), `CREATE TRIGGER reject_delegation BEFORE INSERT ON session_delegations BEGIN SELECT RAISE(ABORT, 'test rejection'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DelegateToAgent(DelegateToAgentInput{ParentSessionID: parent.ID, AgentID: agent.ID, RequestID: "reject-child-0001", Prompt: "Revisar"}); err == nil {
		t.Fatal("delegation unexpectedly succeeded")
	}
	sessions, err := s.ListSessions(ListSessionsInput{WorkspaceID: workspace.ID})
	if err != nil || len(sessions) != 1 || sessions[0].ID != parent.ID {
		t.Fatalf("orphan session: %+v %v", sessions, err)
	}
	events, err := db.ListAfterLimit(t.Context(), parent.ID, 0, 20)
	if err != nil || len(events) != 1 || events[0].Type != "sdd.bypassed" {
		t.Fatalf("parent events changed: %+v %v", events, err)
	}
}

func TestDelegationRejectsInvalidUTF8BeforeCreatingChild(t *testing.T) {
	s, _, _ := setup(t)
	workspace, err := s.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveProviderProfile(profileInput()); err != nil {
		t.Fatal(err)
	}
	agent, err := s.SaveAgent(SaveAgentInput{Name: "Revisor", Instructions: "Revise", BackendID: "local", AllowedTools: []string{"read"}})
	if err != nil {
		t.Fatal(err)
	}
	parent, err := s.CreateDirectSession(CreateDirectSessionInput{WorkspaceID: workspace.ID, BackendID: "local", Reason: "Consulta pontual"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.DelegateToAgent(DelegateToAgentInput{ParentSessionID: parent.ID, AgentID: agent.ID, RequestID: "invalid-child-0001", Prompt: string([]byte{0xff})}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("invalid UTF-8 was accepted: %v", err)
	}
	if _, err := s.DelegateToAgent(DelegateToAgentInput{ParentSessionID: parent.ID, AgentID: agent.ID, RequestID: "invalid-child-0002", Prompt: "NUL\x00after"}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("NUL-containing task was accepted: %v", err)
	}
	links, err := s.ListDelegations(parent.ID)
	if err != nil || len(links) != 0 {
		t.Fatalf("invalid child persisted: %+v %v", links, err)
	}
}

func TestConcurrentDuplicateDelegationHasOneChild(t *testing.T) {
	s, _, _ := setup(t)
	workspace, err := s.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveProviderProfile(profileInput()); err != nil {
		t.Fatal(err)
	}
	agent, err := s.SaveAgent(SaveAgentInput{Name: "Revisor", Instructions: "Revise", BackendID: "local", AllowedTools: []string{"read"}})
	if err != nil {
		t.Fatal(err)
	}
	parent, err := s.CreateDirectSession(CreateDirectSessionInput{WorkspaceID: workspace.ID, BackendID: "local", Reason: "Consulta pontual"})
	if err != nil {
		t.Fatal(err)
	}
	const attempts = 8
	results := make([]DelegatedSessionDTO, attempts)
	errors := make([]error, attempts)
	var wg sync.WaitGroup
	for index := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[index], errors[index] = s.DelegateToAgent(DelegateToAgentInput{ParentSessionID: parent.ID, AgentID: agent.ID, RequestID: "same-child-00001", Prompt: "Revise os critérios"})
		}()
	}
	wg.Wait()
	created := 0
	for index, item := range results {
		if errors[index] != nil || item.Session.ID == "" || item.Session.ID != results[0].Session.ID {
			t.Fatalf("attempt %d: %+v %v", index, item, errors[index])
		}
		if item.Created {
			created++
		}
	}
	if created != 1 {
		t.Fatalf("created count: %d", created)
	}
	sessions, err := s.ListSessions(ListSessionsInput{WorkspaceID: workspace.ID})
	if err != nil || len(sessions) != 2 {
		t.Fatalf("concurrent orphan: %+v %v", sessions, err)
	}
}

func TestConcurrentDistinctDelegationsRespectChildLimit(t *testing.T) {
	s, _, _ := setup(t)
	workspace, err := s.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveProviderProfile(profileInput()); err != nil {
		t.Fatal(err)
	}
	agent, err := s.SaveAgent(SaveAgentInput{Name: "Revisor", Instructions: "Revise", BackendID: "local", AllowedTools: []string{"read"}})
	if err != nil {
		t.Fatal(err)
	}
	parent, err := s.CreateDirectSession(CreateDirectSessionInput{WorkspaceID: workspace.ID, BackendID: "local", Reason: "Consulta pontual"})
	if err != nil {
		t.Fatal(err)
	}
	const attempts = 8
	errResults := make([]error, attempts)
	var wg sync.WaitGroup
	for index := range errResults {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errResults[index] = s.DelegateToAgent(DelegateToAgentInput{ParentSessionID: parent.ID, AgentID: agent.ID, RequestID: fmt.Sprintf("distinct-child-%04d", index), Prompt: fmt.Sprintf("Tarefa %d", index)})
		}()
	}
	wg.Wait()
	created, limited := 0, 0
	for _, err := range errResults {
		switch {
		case err == nil:
			created++
		case errors.Is(err, ErrDelegationLimit):
			limited++
		default:
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if created != 5 || limited != attempts-5 {
		t.Fatalf("created=%d limited=%d", created, limited)
	}
	links, err := s.ListDelegations(parent.ID)
	if err != nil || len(links) != 5 {
		t.Fatalf("children: %+v %v", links, err)
	}
}

func TestBusyDelegationDoesNotConsumeAnotherPromptAttempt(t *testing.T) {
	_, db, vault := setup(t)
	provider := &fakeProvider{block: true, started: make(chan struct{})}
	s := NewService(t.Context(), Dependencies{Store: db, Secrets: vault, External: map[string]ExternalBackend{}, ProviderFactory: func(openai.Config) (agentcore.Provider, error) { return provider, nil }})
	workspace, err := s.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveProviderProfile(profileInput()); err != nil {
		t.Fatal(err)
	}
	agent, err := s.SaveAgent(SaveAgentInput{Name: "Revisor", Instructions: "Revise", BackendID: "local", AllowedTools: []string{"read"}})
	if err != nil {
		t.Fatal(err)
	}
	parent, err := s.CreateDirectSession(CreateDirectSessionInput{WorkspaceID: workspace.ID, BackendID: "local", Reason: "Consulta pontual"})
	if err != nil {
		t.Fatal(err)
	}
	child, err := s.DelegateToAgent(DelegateToAgentInput{ParentSessionID: parent.ID, AgentID: agent.ID, RequestID: "busy-child-00001", Prompt: "Revise"})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		_, _ = s.Prompt(PromptInput{SessionID: child.Session.ID, Text: "Primeira chamada"})
		close(done)
	}()
	select {
	case <-provider.started:
	case <-time.After(time.Second):
		t.Fatal("first call did not start")
	}
	if _, err := s.Prompt(PromptInput{SessionID: child.Session.ID, Text: "Chamada concorrente"}); !errors.Is(err, agentcore.ErrSessionBusy) {
		t.Fatalf("busy call: %v", err)
	}
	links, err := s.ListDelegations(parent.ID)
	if err != nil || len(links) != 1 || links[0].PromptCount != 1 {
		t.Fatalf("busy call consumed budget: %+v %v", links, err)
	}
	if err := s.Cancel(child.Session.ID); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("first call did not cancel")
	}
}

func TestDelegationCreatesDurableChildWithBoundedDepth(t *testing.T) {
	s, _, _ := setup(t)
	workspace, err := s.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveProviderProfile(profileInput()); err != nil {
		t.Fatal(err)
	}
	agent, err := s.SaveAgent(SaveAgentInput{Name: "Analista", Instructions: "Leia antes de responder", BackendID: "local", AllowedTools: []string{"read"}})
	if err != nil {
		t.Fatal(err)
	}
	parent, err := s.CreateSession(CreateSessionInput{WorkspaceID: workspace.ID, BackendID: "local"})
	if err != nil {
		t.Fatal(err)
	}
	child, err := s.DelegateToAgent(DelegateToAgentInput{ParentSessionID: parent.ID, AgentID: agent.ID, RequestID: "nested-child-0001", Prompt: "Revise os critérios"})
	if err != nil || child.Session.ID == "" || child.Session.BackendID != "local" || child.Depth != 1 {
		t.Fatalf("delegate: %+v %v", child, err)
	}
	links, err := s.ListDelegations(parent.ID)
	if err != nil || len(links) != 1 || links[0].ChildSessionID != child.Session.ID || links[0].AgentID != agent.ID {
		t.Fatalf("links: %+v %v", links, err)
	}
	child2, err := s.DelegateToAgent(DelegateToAgentInput{ParentSessionID: child.Session.ID, AgentID: agent.ID, RequestID: "nested-child-0002", Prompt: "Cheque outro arquivo"})
	if err != nil || child2.Depth != 2 {
		t.Fatalf("second depth: %+v %v", child2, err)
	}
}

func TestDelegationReadbackIncludesParentAndJournalOutcome(t *testing.T) {
	s, db, _ := setup(t)
	workspace, err := s.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveProviderProfile(profileInput()); err != nil {
		t.Fatal(err)
	}
	agent, err := s.SaveAgent(SaveAgentInput{Name: "Analista", Instructions: "Leia", BackendID: "local", AllowedTools: []string{"read"}})
	if err != nil {
		t.Fatal(err)
	}
	parent, err := s.CreateSession(CreateSessionInput{WorkspaceID: workspace.ID, BackendID: "local"})
	if err != nil {
		t.Fatal(err)
	}
	child, err := s.DelegateToAgent(DelegateToAgentInput{ParentSessionID: parent.ID, AgentID: agent.ID, RequestID: "outcome-child-0001", Prompt: "Investigue"})
	if err != nil {
		t.Fatal(err)
	}
	if link, err := s.GetParentDelegation(parent.ID); err != nil || link != nil {
		t.Fatalf("root parent: %+v %v", link, err)
	}
	if link, err := s.GetParentDelegation(child.Session.ID); err != nil || link == nil || link.ParentSessionID != parent.ID || link.Status != "ready" {
		t.Fatalf("child parent: %+v %v", link, err)
	}

	appendEvent := func(typ string, data any) {
		t.Helper()
		if _, err := db.Append(t.Context(), child.Session.ID, "agent_session", typ, data); err != nil {
			t.Fatal(err)
		}
	}
	appendEvent("run.started", map[string]any{})
	links, err := s.ListDelegations(parent.ID)
	if err != nil || len(links) != 1 || links[0].Status != "running" || links[0].Result != "" {
		t.Fatalf("running readback: %+v %v", links, err)
	}
	appendEvent("message.assistant", map[string]any{"role": "assistant", "content": "  Revisão concluída.  "})
	appendEvent("run.completed", map[string]any{"reason": ""})
	links, err = s.ListDelegations(parent.ID)
	if err != nil || len(links) != 1 || links[0].Status != "completed" || links[0].Result != "Revisão concluída." || links[0].ErrorCode != "" {
		t.Fatalf("completed readback: %+v %v", links, err)
	}
	appendEvent("run.started", map[string]any{})
	appendEvent("run.failed", map[string]any{"reason": "execution_failed"})
	links, err = s.ListDelegations(parent.ID)
	if err != nil || len(links) != 1 || links[0].Status != "failed" || links[0].Result != "" || links[0].ErrorCode != "execution_failed" {
		t.Fatalf("failed readback: %+v %v", links, err)
	}
	appendEvent("external.run.started", map[string]any{"adapter": "codex"})
	appendEvent("external.event", map[string]any{"type": "assistant.message", "text": "Resposta do CLI", "raw": map[string]any{}})
	appendEvent("external.run.completed", map[string]any{"adapter": "codex"})
	links, err = s.ListDelegations(parent.ID)
	if err != nil || len(links) != 1 || links[0].Status != "completed" || links[0].Result != "Resposta do CLI" {
		t.Fatalf("external readback: %+v %v", links, err)
	}
}

func TestCancellingParentCancelsRunningSubagent(t *testing.T) {
	_, db, vault := setup(t)
	provider := &fakeProvider{block: true, started: make(chan struct{})}
	s := NewService(t.Context(), Dependencies{Store: db, Secrets: vault, External: map[string]ExternalBackend{}, ProviderFactory: func(openai.Config) (agentcore.Provider, error) { return provider, nil }})
	workspace, err := s.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveProviderProfile(profileInput()); err != nil {
		t.Fatal(err)
	}
	agent, err := s.SaveAgent(SaveAgentInput{Name: "Leitor", Instructions: "Leia", BackendID: "local", AllowedTools: []string{"read"}})
	if err != nil {
		t.Fatal(err)
	}
	parent, err := s.CreateSession(CreateSessionInput{WorkspaceID: workspace.ID, BackendID: "local"})
	if err != nil {
		t.Fatal(err)
	}
	child, err := s.DelegateToAgent(DelegateToAgentInput{ParentSessionID: parent.ID, AgentID: agent.ID, RequestID: "cancel-child-0001", Prompt: "Revisar"})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan RunResultDTO, 1)
	go func() {
		result, _ := s.Prompt(PromptInput{SessionID: child.Session.ID, Text: "Revisar"})
		done <- result
	}()
	select {
	case <-provider.started:
	case <-time.After(2 * time.Second):
		t.Fatal("subagent did not start")
	}
	if err := s.Cancel(parent.ID); err != nil {
		t.Fatalf("cancel parent: %v", err)
	}
	select {
	case result := <-done:
		if result.Status != RunCancelled {
			t.Fatalf("child outcome: %+v", result)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("child did not stop")
	}
	links, err := s.ListDelegations(parent.ID)
	if err != nil || len(links) != 1 || links[0].Status != "cancelled" || links[0].PromptCount != 1 {
		t.Fatalf("cancel readback: %+v %v", links, err)
	}
	Shutdown(s)
	next := NewService(t.Context(), Dependencies{Store: db, Secrets: vault, External: map[string]ExternalBackend{}, ProviderFactory: func(openai.Config) (agentcore.Provider, error) { return &fakeProvider{}, nil }})
	if err := next.Recover(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := next.OpenSession(OpenSessionInput{SessionID: child.Session.ID, WorkspaceID: workspace.ID}); err != nil {
		t.Fatal(err)
	}
	if result, err := next.Prompt(PromptInput{SessionID: child.Session.ID, Text: "Continue após cancelamento"}); err != nil || result.Status != RunCompleted {
		t.Fatalf("continue child: %+v %v", result, err)
	}
	links, err = next.ListDelegations(parent.ID)
	if err != nil || len(links) != 1 || links[0].PromptCount != 2 || links[0].Status != "completed" {
		t.Fatalf("continued readback: %+v %v", links, err)
	}
}
