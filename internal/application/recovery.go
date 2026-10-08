package application

import (
	"context"
	"errors"
	"time"

	"github.com/persioflexa/harflex/internal/agentcore"
	"github.com/persioflexa/harflex/internal/catalog"
	"github.com/persioflexa/harflex/internal/events"
)

const maxHistoryEvents = 1_000_000

// Recover closes unfinished journal runs before the desktop admits commands.
// Approvals and tool calls remain evidence; recovery never constructs a runner.
//
//wails:ignore
func (s *Service) Recover(ctx context.Context) error {
	if err := s.beginCall(); err != nil {
		return err
	}
	defer s.endCall()
	s.recoveryGate.Lock()
	defer s.recoveryGate.Unlock()
	s.mu.RLock()
	hasRunners := len(s.sessions) != 0 || len(s.authoringActive) != 0 || len(s.authoringCodeOwners) != 0
	s.mu.RUnlock()
	if hasRunners {
		return errors.New("recovery requires a service without admitted sessions")
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(s.ctx, cancel)
	defer stop()
	if err := s.store.InterruptRunningBrainstormAttempts(ctx); err != nil {
		return safe("recover brainstorm attempts", err)
	}
	if err := s.store.InterruptRunningAuthoringStages(ctx); err != nil {
		return safe("recover authoring stages", err)
	}
	if err := s.store.InterruptPreparingAuthoringCodeCopyAttempts(ctx); err != nil {
		return safe("recover authoring Code copies", err)
	}
	if err := s.store.InterruptRunningAuthoringCodeRuns(ctx); err != nil {
		return safe("recover authoring Code runs", err)
	}
	blockedSessions, err := s.recoverAuthoringStops(ctx)
	if err != nil {
		return err
	}
	sessions, err := s.store.ListAllSessions(ctx)
	if err != nil {
		return safe("list recovery sessions", err)
	}
	var failures []error
	for _, record := range sessions {
		if blockedSessions[record.ID] {
			continue
		}
		if err := ctx.Err(); err != nil {
			failures = append(failures, err)
			break
		}
		if err := s.recoverSession(ctx, record); err != nil {
			failures = append(failures, safe("recover session", err))
		}
	}
	return errors.Join(failures...)
}

func (s *Service) recoverSession(ctx context.Context, record catalog.SessionRecord) (err error) {
	if record.Status == "history_too_large" {
		return nil
	}
	defer func() {
		if errors.Is(err, events.ErrStreamBudgetExceeded) || errors.Is(err, events.ErrEventBudgetExceeded) {
			record.Status, record.UpdatedAt = "history_too_large", time.Now().UTC()
			err = s.store.UpsertSession(ctx, record)
		}
	}()
	size, err := s.store.StreamDataBytes(ctx, record.ID)
	if err != nil {
		return err
	}
	if size > events.MaxStreamDataBytes {
		return events.ErrStreamBudgetExceeded
	}
	history, err := s.restoreHistory(ctx, record.ID, true)
	if err != nil {
		return err
	}
	status, statusAt := history.status, history.statusAt
	if history.active || (history.interruptedSequence > 0 && len(history.pending) > 0) {
		request := events.RecoveryRequest{StartSequence: history.startedSequence, ObservedSequence: history.lastSequence, InterruptedSequence: history.interruptedSequence}
		for _, call := range history.pending {
			code, text := "not_executed", "tool call not executed"
			if history.called[call.ID] {
				code, text = "outcome_unknown", "tool outcome unknown after interruption"
			}
			request.Closures = append(request.Closures, events.ToolClosure{ToolCallID: call.ID, Name: call.Name, ErrorCode: code, Error: text})
		}
		appended, err := s.store.RepairInterruptedRun(ctx, record.ID, request)
		if err != nil {
			return err
		}
		if len(appended) == 0 {
			return nil
		}
		// Only committed events cross the desktop boundary; no redaction or alias
		// regeneration is needed for already-public historical correlation IDs.
		journal := eventJournal{service: s}
		for _, event := range appended {
			journal.emit(event)
		}
		status, statusAt = "paused", appended[len(appended)-1].CreatedAt
	}
	if status != "" && (record.Status != status || record.UpdatedAt.Before(statusAt)) {
		record.Status, record.UpdatedAt = status, statusAt
		return s.store.UpsertSession(ctx, record)
	}
	return nil
}

func (s *Service) walkEvents(ctx context.Context, id string, visit func(events.Event) error) error {
	var after int64
	return s.store.WalkAfter(ctx, id, 0, maxHistoryEvents, agentcore.MaxHistoryBytes, func(event events.Event) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if event.StreamID != id || event.Sequence != after+1 {
			return ErrSessionCorrupt
		}
		if err := visit(event); err != nil {
			return err
		}
		after = event.Sequence
		return nil
	})
}
