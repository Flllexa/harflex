package application

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/persioflexa/harflex/internal/catalog"
	"github.com/persioflexa/harflex/internal/events"
	"github.com/persioflexa/harflex/internal/sdd"
	"github.com/persioflexa/harflex/internal/storage/sqlite"
)

func TestRecoverPausesBrainstormAggregateWithoutProvider(t *testing.T) {
	s, db, vault := setup(t)
	w, err := s.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	flow := sdd.NewFlow()
	pipeline := catalog.PipelineRun{ID: "brainstorm-pipeline", WorkspaceID: w.ID, Kind: "ai_authoring", Title: "title", Objective: "objective", Current: flow.Current, Status: flow.Status, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := db.CreateAuthoringPipeline(t.Context(), pipeline, "Discovery"); err != nil {
		t.Fatal(err)
	}
	choice := catalog.ModelSelection{BackendID: "profile", ModelID: "model", Source: "openai_models", Destination: "https://example.test", CatalogRevision: "revision", CredentialIdentity: strings.Repeat("a", 64), Status: "listed", CheckedAt: now, MaxOutputTokens: 1024}
	run, err := db.StartBrainstorming(t.Context(), catalog.StartBrainstormingRequest{PipelineID: pipeline.ID, RequestID: "start_request_001", PipelineRevision: 1, DiscoveryVersion: 1, Selection: choice})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.BeginBrainstormAttempt(t.Context(), catalog.BeginBrainstormAttemptRequest{BrainstormRequest: catalog.BrainstormRequest{RunID: run.ID, RequestID: "question_req_0011", PipelineRevision: 2, RunRevision: 1, DiscoveryVersion: 1}, Kind: "question", Selection: choice, EstimatedInputTokens: 1000}); err != nil {
		t.Fatal(err)
	}
	if err := s.Recover(t.Context()); err != nil {
		t.Fatal(err)
	}
	got, err := db.GetBrainstorming(t.Context(), run.ID)
	if err != nil || got.State != "paused" || got.Attempts[0].Status != "interrupted" || got.DiscoveryContent != "Discovery" || vault.gets != 0 || len(s.sessions) != 0 {
		t.Fatalf("recovery %+v %v", got, err)
	}
	revision := got.Revision
	if err := s.Recover(t.Context()); err != nil {
		t.Fatal(err)
	}
	got, err = db.GetBrainstorming(t.Context(), run.ID)
	if err != nil || got.Revision != revision {
		t.Fatalf("recovery duplicate %+v %v", got, err)
	}
}

func TestRecoverPausesAuthoringStageWithoutProvider(t *testing.T) {
	s, db, vault := setup(t)
	w, err := s.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	flow := sdd.NewFlow()
	pipeline := catalog.PipelineRun{ID: "stage-recovery-pipeline", WorkspaceID: w.ID, Kind: "ai_authoring", Title: "Title", Objective: "Objective", Current: flow.Current, Status: flow.Status, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := db.CreateAuthoringPipeline(t.Context(), pipeline, "User Discovery"); err != nil {
		t.Fatal(err)
	}
	choice := catalog.ModelSelection{BackendID: "profile", ModelID: "model", Source: "openai_models", Destination: "https://example.test", CatalogRevision: "revision", CredentialIdentity: strings.Repeat("a", 64), Status: "listed", CheckedAt: now, MaxOutputTokens: 4096}
	brain, err := db.StartBrainstorming(t.Context(), catalog.StartBrainstormingRequest{PipelineID: pipeline.ID, RequestID: "start_stage_brain_1", PipelineRevision: 1, DiscoveryVersion: 1, Selection: choice})
	if err != nil {
		t.Fatal(err)
	}
	ref := catalog.BrainstormRequest{RunID: brain.ID, RequestID: "skip_stage_brain_01", PipelineRevision: 2, RunRevision: 1, DiscoveryVersion: 1}
	if _, err := db.SkipBrainstormQuestions(t.Context(), catalog.SkipBrainstormQuestionsRequest{BrainstormRequest: ref, Reason: "Discovery is enough"}); err != nil {
		t.Fatal(err)
	}
	ref.RequestID = "confirm_stage_brain"
	ref.PipelineRevision++
	ref.RunRevision++
	if _, err := db.ConfirmDiscoveryAfterSkip(t.Context(), ref); err != nil {
		t.Fatal(err)
	}
	stageRef := catalog.AuthoringStageRequest{PipelineID: pipeline.ID, Stage: sdd.Spec, RequestID: "generate_stage_001", PipelineRevision: 4, DiscoveryVersion: 1}
	a, admitted, err := db.BeginAuthoringStage(t.Context(), catalog.BeginAuthoringStageRequest{Ref: stageRef, Selection: choice, ModelMode: "inherit", EffortMode: "inherit", PreferenceSource: "brainstorm", PreferenceRevision: 0, EstimatedInputTokens: 4096, ClientIntentHash: strings.Repeat("b", 64)})
	if err != nil || !admitted {
		t.Fatalf("admit: %v %v", admitted, err)
	}
	stageRef.PipelineRevision++
	stageRef.StageRevision++
	record := catalog.SessionRecord{ID: "stage-recovery-session", WorkspaceID: w.ID, BackendID: choice.BackendID, BackendRevision: choice.CatalogRevision, Mode: "sdd_readonly", Status: "ready", CreatedAt: now, UpdatedAt: now}
	if err := db.CreateAuthoringStageSession(t.Context(), stageRef, a, record); err != nil {
		t.Fatal(err)
	}
	if err := s.Recover(t.Context()); err != nil {
		t.Fatal(err)
	}
	run, err := db.GetAuthoringStage(t.Context(), pipeline.ID, sdd.Spec)
	if err != nil || run.State != "cancellation_pending" || !run.CancellationPending || run.Attempts[0].Status != "interrupted" || run.AttemptCount != 1 || len(run.Artifacts) != 0 || vault.gets != 0 || len(s.sessions) != 0 {
		t.Fatalf("recovery: %+v %v", run, err)
	}
	if err := s.Recover(t.Context()); err != nil {
		t.Fatal(err)
	}
	again, err := db.GetAuthoringStage(t.Context(), pipeline.ID, sdd.Spec)
	if err != nil || again.Revision != run.Revision || again.InputBudgetRemaining != run.InputBudgetRemaining {
		t.Fatalf("repeated recovery: %+v %v", again, err)
	}
}

func recoverService(t *testing.T, s *Service, ctx context.Context) error {
	t.Helper()
	r, ok := any(s).(interface{ Recover(context.Context) error })
	if !ok {
		t.Fatal("service does not implement durable recovery")
	}
	return r.Recover(ctx)
}

func persistedSession(t *testing.T, s *Service, db *sqlite.Store, id string, types ...string) catalog.SessionRecord {
	t.Helper()
	w, err := s.OpenWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	old := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	record := catalog.SessionRecord{ID: id, WorkspaceID: w.ID, BackendID: "local", Status: "running", CreatedAt: old, UpdatedAt: old}
	if err := db.UpsertSession(t.Context(), record); err != nil {
		t.Fatal(err)
	}
	for _, typ := range types {
		if _, err := db.Append(t.Context(), id, "agent_session", typ, map[string]string{"evidence": "untouched"}); err != nil {
			t.Fatal(err)
		}
	}
	return record
}

func TestRecoverPausesOnlyUnfinishedRunsWithoutExecution(t *testing.T) {
	for _, history := range [][]string{{"run.started"}, {"run.started", "tool.called", "approval.requested"}, {"run.started", "run.completed", "run.started"}, {"external.run.started", "external.output"}} {
		t.Run(strings.Join(history, "_"), func(t *testing.T) {
			s, db, v := setup(t)
			before := persistedSession(t, s, db, "session", history...)
			var count atomic.Int32
			SetEmitter(s, func(_ string, payload any) {
				dto := payload.(EventDTO)
				stored, err := db.ListAfter(t.Context(), dto.StreamID, dto.Sequence-1)
				if err != nil || len(stored) != 1 || stored[0].ID != dto.ID {
					t.Error("event emitted before commit")
				}
				count.Add(1)
			})
			if err := recoverService(t, s, t.Context()); err != nil {
				t.Fatal(err)
			}
			items, err := db.ListAfter(t.Context(), "session", 0)
			if err != nil || len(items) != len(history)+1 {
				t.Fatal(len(items), err)
			}
			last := items[len(items)-1]
			if last.Type != "run.interrupted" || string(last.Data) != `{"reason":"app_restart"}` {
				t.Fatal(last)
			}
			after, err := db.GetSession(t.Context(), "session")
			if err != nil || after.Status != "paused" || !after.UpdatedAt.After(before.UpdatedAt) || after.UpdatedAt.Location() != time.UTC {
				t.Fatal(after, err)
			}
			if len(s.sessions) != 0 || v.gets != 0 || count.Load() != 1 {
				t.Fatal("recovery executed or emitted unexpected work")
			}
			if err := recoverService(t, s, t.Context()); err != nil {
				t.Fatal(err)
			}
			again, _ := db.GetSession(t.Context(), "session")
			items, _ = db.ListAfter(t.Context(), "session", 0)
			if len(items) != len(history)+1 || count.Load() != 1 || !again.UpdatedAt.Equal(after.UpdatedAt) {
				t.Fatal("recovery is not idempotent")
			}
		})
	}
}

func TestRecoverLeavesTerminalEmptyAndOrphanStreamsUntouched(t *testing.T) {
	s, db, _ := setup(t)
	for _, terminal := range []string{"run.completed", "run.failed", "run.cancelled", "run.interrupted", "external.run.completed", "external.run.failed", "external.run.cancelled"} {
		start := "run.started"
		if strings.HasPrefix(terminal, "external.") {
			start = "external.run.started"
		}
		before := persistedSession(t, s, db, terminal, start, terminal)
		// Terminal journal evidence repairs a stale catalog without replaying work.
		if err := recoverService(t, s, t.Context()); err != nil {
			t.Fatal(err)
		}
		after, _ := db.GetSession(t.Context(), terminal)
		items, _ := db.ListAfter(t.Context(), terminal, 0)
		if after.ID != before.ID || after.Status != catalogStatus(terminal) || len(items) != 2 {
			t.Fatal("terminal session changed", terminal)
		}
	}
	before := persistedSession(t, s, db, "empty")
	if _, err := db.Append(t.Context(), "orphan", "agent_session", "run.started", nil); err != nil {
		t.Fatal(err)
	}
	if err := recoverService(t, s, t.Context()); err != nil {
		t.Fatal(err)
	}
	after, _ := db.GetSession(t.Context(), "empty")
	orphan, _ := db.ListAfter(t.Context(), "orphan", 0)
	if before != after || len(orphan) != 1 {
		t.Fatal("empty or orphan changed")
	}
}

func TestRecoverConcurrentAcrossServicesEmitsExactlyOnce(t *testing.T) {
	s, db, v := setup(t)
	for _, id := range []string{"first", "second"} {
		persistedSession(t, s, db, id, "run.started", "approval.requested")
	}
	var count atomic.Int32
	var wg sync.WaitGroup
	errs := make(chan error, 16)
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			service := NewService(t.Context(), Dependencies{Store: db, Secrets: v, Emit: func(string, any) { count.Add(1) }})
			errs <- recoverService(t, service, t.Context())
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if count.Load() != 2 {
		t.Fatal("duplicate recovery events", count.Load())
	}
	for _, id := range []string{"first", "second"} {
		items, _ := db.ListAfter(t.Context(), id, 0)
		record, _ := db.GetSession(t.Context(), id)
		if len(items) != 3 || record.Status != "paused" {
			t.Fatal(id, len(items), record.Status)
		}
	}
}

type recoveryFailure struct {
	Store
	appendFailure, updateFailure bool
}

func (f recoveryFailure) RepairInterruptedRun(ctx context.Context, id string, request events.RecoveryRequest) ([]events.Event, error) {
	if f.appendFailure {
		return nil, errors.New("private-canary")
	}
	return f.Store.RepairInterruptedRun(ctx, id, request)
}
func (f recoveryFailure) UpsertSession(ctx context.Context, r catalog.SessionRecord) error {
	if f.updateFailure {
		return errors.New("private-canary")
	}
	return f.Store.UpsertSession(ctx, r)
}

func TestRecoverFailureDoesNotDuplicateAndRetriesStatus(t *testing.T) {
	for _, appendFailure := range []bool{false, true} {
		t.Run(map[bool]string{false: "catalog", true: "append"}[appendFailure], func(t *testing.T) {
			s, db, _ := setup(t)
			persistedSession(t, s, db, "session", "run.started")
			emitted := 0
			SetEmitter(s, func(string, any) { emitted++ })
			s.store = recoveryFailure{Store: db, appendFailure: appendFailure, updateFailure: !appendFailure}
			if err := recoverService(t, s, t.Context()); err == nil || strings.Contains(err.Error(), "private-canary") {
				t.Fatal(err)
			}
			items, _ := db.ListAfter(t.Context(), "session", 0)
			want := 2
			if appendFailure {
				want = 1
			}
			if len(items) != want || emitted != want-1 {
				t.Fatal(len(items), emitted)
			}
			s.store = db
			if err := recoverService(t, s, t.Context()); err != nil {
				t.Fatal(err)
			}
			items, _ = db.ListAfter(t.Context(), "session", 0)
			record, _ := db.GetSession(t.Context(), "session")
			if len(items) != 2 || record.Status != "paused" || emitted != 1 {
				t.Fatal("failed recovery was not safely reconciled")
			}
		})
	}
}

func TestRecoverCancellation(t *testing.T) {
	s, db, _ := setup(t)
	persistedSession(t, s, db, "session", "run.started")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := recoverService(t, s, ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	items, _ := db.ListAfter(t.Context(), "session", 0)
	if len(items) != 1 {
		t.Fatal("cancelled recovery changed journal")
	}
}

func TestRecoverCannotInterruptAnAdmittedLiveSession(t *testing.T) {
	p := &fakeProvider{block: true, started: make(chan struct{})}
	s, db, _, session := sessionSetup(t, p)
	done := make(chan error, 1)
	go func() { done <- cancelledRun(s.Prompt(PromptInput{SessionID: session.ID, Text: "hello"})) }()
	<-p.started
	err := recoverService(t, s, t.Context())
	if err == nil {
		t.Error("recovery admitted after a live session was created")
	}
	items, _ := db.ListAfter(t.Context(), session.ID, 0)
	for _, item := range items {
		if item.Type == "run.interrupted" {
			t.Error("recovery interrupted a live runner")
		}
	}
	if err := s.Cancel(session.ID); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestRecoverRefreshesPreviouslyPausedCatalogRecord(t *testing.T) {
	s, db, _ := setup(t)
	before := persistedSession(t, s, db, "session", "run.started")
	before.Status = "paused"
	if err := db.UpsertSession(t.Context(), before); err != nil {
		t.Fatal(err)
	}
	if err := recoverService(t, s, t.Context()); err != nil {
		t.Fatal(err)
	}
	after, _ := db.GetSession(t.Context(), "session")
	if !after.UpdatedAt.After(before.UpdatedAt) {
		t.Fatal("new interruption did not update the paused catalog timestamp")
	}
}
