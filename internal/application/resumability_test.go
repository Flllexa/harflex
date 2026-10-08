package application

import (
	"context"
	"errors"
	"github.com/persioflexa/harflex/internal/agentcore"
	"github.com/persioflexa/harflex/internal/catalog"
	"github.com/persioflexa/harflex/internal/externalagent"
	"github.com/persioflexa/harflex/internal/providers/openai"
	"testing"
	"time"
)

func TestSessionResumableReflectsBackendAndDurableContinuation(t *testing.T) {
	for _, mode := range []string{"api", "removed_api", "changed_api", "unused_opencode", "bound_opencode", "unbound_opencode", "invalid_opencode", "unavailable_opencode", "bound_codex", "used_codex", "unused_codex", "unknown"} {
		t.Run(mode, func(t *testing.T) {
			first, db, vault, created := sessionSetup(t, &fakeProvider{})
			Shutdown(first)
			record, err := db.GetSession(t.Context(), created.ID)
			if err != nil {
				t.Fatal(err)
			}
			adapters := map[string]ExternalBackend{"opencode": &reopenExternal{}, "codex": resumableFakeCodex{fakeExternal: fakeExternal{available: true}}}
			want := mode == "api" || mode == "unused_opencode" || mode == "bound_opencode" || mode == "bound_codex"
			var wantErr error
			switch mode {
			case "removed_api":
				db.DB().Exec("DELETE FROM provider_profiles WHERE id='local'")
				wantErr = ErrBackendNotFound
			case "changed_api":
				p, _ := db.GetProviderProfile(t.Context(), "local")
				p.Model = "changed"
				db.UpsertProviderProfile(t.Context(), p)
				wantErr = ErrBackendChanged
			case "unknown":
				record.BackendID = "unknown"
				record.BackendRevision = ""
				wantErr = ErrBackendNotFound
			case "unused_opencode", "bound_opencode", "unbound_opencode", "invalid_opencode", "unavailable_opencode":
				record.BackendID = "opencode"
				record.BackendRevision = "cli:opencode"
				if mode == "unavailable_opencode" {
					adapters["opencode"] = nil
					wantErr = ErrBackendNotFound
				}
			case "bound_codex", "used_codex", "unused_codex":
				record.BackendID = "codex"
				record.BackendRevision = "cli:codex"
			}
			if err := db.UpsertSession(t.Context(), record); err != nil {
				t.Fatal(err)
			}
			if mode == "bound_opencode" || mode == "unbound_opencode" || mode == "invalid_opencode" || mode == "used_codex" || mode == "bound_codex" {
				db.Append(t.Context(), record.ID, "external_session", "external.run.started", map[string]string{"adapter": record.BackendID})
				if mode == "bound_opencode" || mode == "invalid_opencode" || mode == "bound_codex" {
					id := "ses_bound"
					if mode == "bound_codex" {
						id = "thread_bound"
					}
					if mode == "invalid_opencode" {
						id = "../invalid"
					}
					db.Append(t.Context(), record.ID, "external_session", "external.session.bound", map[string]string{"sessionId": id})
				}
				db.Append(t.Context(), record.ID, "external_session", "external.run.completed", nil)
				if mode != "bound_opencode" && mode != "bound_codex" {
					wantErr = externalagent.ErrNotResumable
				}
			}
			factories := 0
			next := NewService(t.Context(), Dependencies{Store: db, Secrets: vault, External: adapters, ProviderFactory: func(openai.Config) (agentcore.Provider, error) { factories++; return &fakeProvider{}, nil }})
			gets := vault.gets
			list, err := next.ListSessions(ListSessionsInput{WorkspaceID: record.WorkspaceID})
			if err != nil || len(list) != 1 || list[0].Resumable != want {
				t.Fatal(list, err, want)
			}
			if vault.gets != gets || factories != 0 {
				t.Fatal("listing touched provider credentials")
			}
			dto, err := next.OpenSession(OpenSessionInput{SessionID: record.ID, WorkspaceID: record.WorkspaceID})
			if mode == "invalid_opencode" {
				if !errors.Is(err, ErrSessionCorrupt) || len(next.sessions) != 0 {
					t.Fatal("corrupt history opened", err)
				}
			} else if wantErr != nil {
				if err != nil || dto.Resumable || len(next.sessions) != 1 || vault.gets != gets || factories != 0 {
					t.Fatal("history unavailable or execution admitted", dto, err)
				}
				if _, err := next.Prompt(PromptInput{SessionID: record.ID, Text: "forbidden"}); !errors.Is(err, wantErr) {
					t.Fatal(err, wantErr)
				}
				if _, err := next.ListEvents(ListEventsInput{SessionID: record.ID}); err != nil {
					t.Fatal("history unavailable", err)
				}
				if again, err := next.OpenSession(OpenSessionInput{SessionID: record.ID, WorkspaceID: record.WorkspaceID}); err != nil || again != dto {
					t.Fatal("readonly open not idempotent", again, err)
				}
			} else if err != nil || dto.Resumable != want {
				t.Fatal(dto, err)
			}
		})
	}
}

func TestCompletedCodexEvaluationReopensReadOnlyWithHistory(t *testing.T) {
	base, db, vault := setup(t)
	workspace, err := base.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	record := catalog.SessionRecord{ID: "codex-evaluation", WorkspaceID: workspace.ID, BackendID: "codex", BackendRevision: "cli:codex", Mode: "evaluation", Status: "completed", CreatedAt: now, UpdatedAt: now}
	if err := db.UpsertSession(t.Context(), record); err != nil {
		t.Fatal(err)
	}
	db.Append(t.Context(), record.ID, "external_session", "external.run.started", map[string]string{"adapter": "codex"})
	db.Append(t.Context(), record.ID, "external_session", "message.user", agentcore.Message{Role: agentcore.RoleUser, Content: "Avalie a implementação"})
	db.Append(t.Context(), record.ID, "external_session", "external.session.bound", map[string]string{"sessionId": "thread_eval_bound"})
	db.Append(t.Context(), record.ID, "external_session", "external.event", externalagent.Event{Type: "text", Text: "A avaliação terminou."})
	db.Append(t.Context(), record.ID, "external_session", "external.run.completed", map[string]string{"adapter": "codex"})
	adapter := &evaluationProbeAdapter{}
	next := NewService(t.Context(), Dependencies{Store: db, Secrets: vault, External: map[string]ExternalBackend{"codex": adapter}})

	listed, err := next.ListSessions(ListSessionsInput{WorkspaceID: workspace.ID})
	if err != nil || len(listed) != 1 || listed[0].Resumable {
		t.Fatal("evaluation must be listed as read-only", listed, err)
	}
	dto, err := next.OpenSession(OpenSessionInput{SessionID: record.ID, WorkspaceID: workspace.ID})
	if err != nil || dto.Resumable || len(next.sessions) != 1 {
		t.Fatal("evaluation history did not reopen read-only", dto, err, len(next.sessions))
	}
	events, err := next.ListEvents(ListEventsInput{SessionID: record.ID})
	if err != nil || len(events) != 5 || events[3].Type != "external.event" {
		t.Fatal("evaluation history unavailable", events, err)
	}
	if _, err := next.Prompt(PromptInput{SessionID: record.ID, Text: "continue"}); !errors.Is(err, externalagent.ErrNotResumable) {
		t.Fatal("completed evaluation accepted another prompt", err)
	}
	if adapter.detects != 0 || adapter.runs != 0 {
		t.Fatal("reopening an ephemeral evaluation invoked the CLI", adapter)
	}
}

type evaluationProbeAdapter struct{ detects, runs int }

func (a *evaluationProbeAdapter) ID() string { return "codex" }
func (a *evaluationProbeAdapter) Detect() externalagent.Detection {
	a.detects++
	return externalagent.Detection{Available: true}
}
func (*evaluationProbeAdapter) Capabilities() agentcore.Capabilities {
	return agentcore.Capabilities{Streaming: true, Resumable: true}
}
func (a *evaluationProbeAdapter) Run(context.Context, externalagent.Request) (<-chan externalagent.Event, <-chan error) {
	a.runs++
	return nil, nil
}

type resumableFakeCodex struct{ fakeExternal }

func (resumableFakeCodex) Capabilities() agentcore.Capabilities {
	return agentcore.Capabilities{Streaming: true, Resumable: true}
}

type readOnlyProbeAdapter struct{ detects, runs int }

func (a *readOnlyProbeAdapter) ID() string { return "codex" }
func (a *readOnlyProbeAdapter) Detect() externalagent.Detection {
	a.detects++
	return externalagent.Detection{Available: true}
}
func (a *readOnlyProbeAdapter) Capabilities() agentcore.Capabilities { return agentcore.Capabilities{} }
func (a *readOnlyProbeAdapter) Run(context.Context, externalagent.Request) (<-chan externalagent.Event, <-chan error) {
	a.runs++
	return nil, nil
}

func TestOpenUsedCodexRecoversAndExposesReadOnlyHistoryWithoutExecution(t *testing.T) {
	base, db, vault := setup(t)
	workspace, err := base.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	record := catalog.SessionRecord{ID: "codex-history", WorkspaceID: workspace.ID, BackendID: "codex", BackendRevision: "cli:codex", Status: "running", CreatedAt: now, UpdatedAt: now}
	if err := db.UpsertSession(t.Context(), record); err != nil {
		t.Fatal(err)
	}
	db.Append(t.Context(), record.ID, "external_session", "external.run.started", nil)
	db.Append(t.Context(), record.ID, "external_session", "message.user", agentcore.Message{Role: agentcore.RoleUser, Content: "old prompt"})
	db.Append(t.Context(), record.ID, "external_session", "external.event", externalagent.Event{Type: "text", Text: "old answer"})
	adapter := &readOnlyProbeAdapter{}
	factories := 0
	next := NewService(t.Context(), Dependencies{Store: db, Secrets: vault, External: map[string]ExternalBackend{"codex": adapter}, ProviderFactory: func(openai.Config) (agentcore.Provider, error) { factories++; return &fakeProvider{}, nil }})
	dto, err := next.OpenSession(OpenSessionInput{SessionID: record.ID, WorkspaceID: record.WorkspaceID})
	if err != nil || dto.Status != "paused" || dto.Resumable {
		t.Fatal(dto, err)
	}
	events, err := next.ListEvents(ListEventsInput{SessionID: record.ID})
	if err != nil || len(events) != 4 || events[3].Type != "run.interrupted" {
		t.Fatal(events, err)
	}
	if _, err := next.Prompt(PromptInput{SessionID: record.ID, Text: "next"}); !errors.Is(err, externalagent.ErrNotResumable) {
		t.Fatal(err)
	}
	if err := next.Cancel(record.ID); !errors.Is(err, ErrNoActiveRun) {
		t.Fatal(err)
	}
	if _, err := next.Approve(ApprovalInput{SessionID: record.ID, ApprovalID: "old", Allow: true}); !errors.Is(err, ErrApprovalUnsupported) {
		t.Fatal(err)
	}
	if again, err := next.OpenSession(OpenSessionInput{SessionID: record.ID, WorkspaceID: record.WorkspaceID}); err != nil || again != dto {
		t.Fatal(again, err)
	}
	if adapter.detects != 0 || adapter.runs != 0 || factories != 0 || vault.gets != 0 {
		t.Fatal("readonly open executed dependencies", adapter, factories, vault.gets)
	}
}

func TestRecoverCrashImmediatelyAfterExternalStarted(t *testing.T) {
	s, db, _ := setup(t)
	workspace, err := s.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	record := catalog.SessionRecord{ID: "crashed", WorkspaceID: workspace.ID, BackendID: "opencode", Status: "running", CreatedAt: now, UpdatedAt: now}
	if err := db.UpsertSession(t.Context(), record); err != nil {
		t.Fatal(err)
	}
	db.Append(t.Context(), record.ID, "external_session", "external.run.started", nil)
	if err := s.Recover(t.Context()); err != nil {
		t.Fatal(err)
	}
	events, err := db.ListAfter(t.Context(), record.ID, 0)
	if err != nil || len(events) != 2 || events[1].Type != "run.interrupted" {
		t.Fatal(events, err)
	}
}
