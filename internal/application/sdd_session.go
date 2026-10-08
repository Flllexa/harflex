package application

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/persioflexa/harflex/internal/agentcore"
	"github.com/persioflexa/harflex/internal/catalog"
	"github.com/persioflexa/harflex/internal/id"
)

const sddAttemptTimeout = 90 * time.Second

// Admission is internal: the public CreateSession validation never accepts this
// mode. Persistence precedes provider composition and catalog network checks.
func (s *Service) createSDDReadOnlySession(ref catalog.BrainstormRequest, attempt catalog.BrainstormAttempt) (result SessionDTO, err error) {
	if err := s.beginCall(); err != nil {
		return SessionDTO{}, err
	}
	defer s.endCall()
	s.recoveryGate.RLock()
	defer s.recoveryGate.RUnlock()
	if attempt.RunID != ref.RunID || attempt.Status != "running" || attempt.SessionID != "" {
		return SessionDTO{}, ErrInvalidInput
	}
	run, err := s.store.GetBrainstorming(s.ctx, ref.RunID)
	if err != nil {
		return SessionDTO{}, safe("read brainstorm", err)
	}
	pipeline, err := s.store.GetPipeline(s.ctx, run.PipelineID)
	if err != nil {
		return SessionDTO{}, safe("read brainstorm pipeline", err)
	}
	workspace, err := s.store.GetWorkspace(s.ctx, pipeline.WorkspaceID)
	if err != nil {
		return SessionDTO{}, safe("read brainstorm workspace", err)
	}
	backendRevision := ""
	if attempt.Selection.Source == "codex_app_server" && attempt.Selection.BackendID == "codex" {
		if s.external["codex"] == nil || attempt.Selection.MaxAssistantOutputBytes <= 0 {
			return SessionDTO{}, ErrBackendNotFound
		}
		backendRevision = "cli:codex"
	} else {
		if _, external := s.external[attempt.Selection.BackendID]; external {
			return SessionDTO{}, ErrInvalidInput
		}
		profile, profileErr := s.store.GetProviderProfile(s.ctx, attempt.Selection.BackendID)
		if profileErr != nil {
			return SessionDTO{}, safe("read brainstorm profile", profileErr)
		}
		if profile.Kind != "openai_compatible" || profileRevision(profile) != attempt.Selection.CatalogRevision {
			return SessionDTO{}, ErrBackendChanged
		}
		backendRevision = profileRevision(profile)
	}
	now := time.Now().UTC()
	record := catalog.SessionRecord{ID: id.New(), WorkspaceID: workspace.ID, BackendID: attempt.Selection.BackendID, BackendRevision: backendRevision, Mode: "sdd_readonly", Status: "ready", CreatedAt: now, UpdatedAt: now}
	if err := s.store.CreateBrainstormSession(s.ctx, ref, attempt, record); err != nil {
		return SessionDTO{}, safe("link brainstorm session", err)
	}
	installed := false
	defer func() {
		if installed {
			return
		}
		// A committed link cannot be retried. Publish a terminal attempt outcome
		// even if composition or shutdown fails before the runner is installed.
		ctx, cancel := context.WithTimeout(context.WithoutCancel(s.ctx), 2*time.Second)
		defer cancel()
		code := "provider_failed"
		if errors.Is(err, context.Canceled) {
			code = "cancelled"
		}
		_, pauseErr := s.store.FailBrainstormAttempt(ctx, catalog.FailBrainstormAttemptRequest{BrainstormRequest: ref, AttemptID: attempt.ID, ErrorCode: code})
		if pauseErr != nil {
			err = errors.Join(err, safe("pause failed brainstorm admission", pauseErr))
		}
	}()
	linked, err := s.store.ValidateBrainstormSession(s.ctx, ref, attempt.ID, record.ID)
	if err != nil {
		return SessionDTO{}, safe("read brainstorm session link", err)
	}
	selection, err := s.store.GetSessionModelSelection(s.ctx, record.ID)
	if err != nil {
		return SessionDTO{}, safe("read brainstorm selection", err)
	}
	if selection.Source == "codex_app_server" {
		selection.MaxAssistantOutputBytes = attempt.Selection.MaxAssistantOutputBytes
	}
	runner, journal, err := s.makeRunner(&record, workspace, restoredHistory{}, false, nil, "", &selection)
	if err != nil {
		return SessionDTO{}, err
	}
	limit := sddQuestionOutputBytes
	if attempt.Kind == "synthesis" {
		limit = sddSynthesisOutputBytes
	} else if attempt.Kind != "question" {
		journal.Close()
		return SessionDTO{}, ErrInvalidInput
	}
	// Catalog lookup can block while another operation invalidates the attempt.
	// Recheck the durable fence after that lookup, immediately before inference.
	validateAttempt := func(ctx context.Context) error {
		current, err := s.store.ValidateBrainstormSession(ctx, ref, linked.ID, linked.SessionID)
		if err != nil {
			return safe("validate brainstorm before inference", err)
		}
		if !reflect.DeepEqual(current, linked) {
			return ErrInvalidInput
		}
		return nil
	}
	switch selected := runner.(type) {
	case *selectedAPIRunner:
		core, ok := selected.base.(*agentcore.Session)
		if !ok {
			journal.Close()
			return SessionDTO{}, ErrInvalidInput
		}
		if err := core.SetAssistantOutputByteLimit(limit); err != nil {
			journal.Close()
			return SessionDTO{}, err
		}
		selected.validateAttempt = validateAttempt
	case *selectedCLIRunner:
		if selected.selection.MaxAssistantOutputBytes != limit {
			journal.Close()
			return SessionDTO{}, ErrBackendChanged
		}
		selected.validateAttempt = validateAttempt
	default:
		journal.Close()
		return SessionDTO{}, ErrInvalidInput
	}
	s.mu.Lock()
	if s.closing {
		s.mu.Unlock()
		journal.Close()
		return SessionDTO{}, context.Canceled
	}
	s.sessions[record.ID] = &sddAttemptRunner{sessionRunner: runner, attempt: linked, ref: ref}
	s.journals[record.ID] = journal
	s.mu.Unlock()
	installed = true
	return s.sessionDTO(record), nil
}

// The bound runner is single-use, including failed/cancelled preflight. A retry
// needs a new durable attempt and its own budget; history can never recreate it.
type sddAttemptRunner struct {
	sessionRunner
	attempt catalog.BrainstormAttempt
	ref     catalog.BrainstormRequest
	mu      sync.Mutex
	used    bool
}

func (r *sddAttemptRunner) Abort(ctx context.Context) error {
	r.mu.Lock()
	r.used = true
	r.mu.Unlock()
	if aborter, ok := r.sessionRunner.(interface{ Abort(context.Context) error }); ok {
		return aborter.Abort(ctx)
	}
	r.sessionRunner.Cancel()
	return nil
}

func (s *Service) promptSDDAttempt(parent context.Context, ref catalog.BrainstormRequest, expected catalog.BrainstormAttempt, text string) (RunResultDTO, error) {
	if err := s.beginCall(); err != nil {
		return RunResultDTO{}, err
	}
	defer s.endCall()
	if strings.TrimSpace(text) == "" || !utf8.ValidString(text) || len(text) > maxPromptBytes || int64(len(text))+512 > expected.ReservedInputTokens {
		return RunResultDTO{}, ErrInvalidInput
	}
	runner, err := s.runner(expected.SessionID)
	if err != nil {
		return RunResultDTO{}, err
	}
	bound, ok := runner.(*sddAttemptRunner)
	if !ok || bound.ref != ref || !reflect.DeepEqual(bound.attempt, expected) {
		return RunResultDTO{}, ErrInvalidInput
	}
	ctx, cancel := context.WithDeadline(s.ctx, expected.CreatedAt.Add(sddAttemptTimeout))
	defer cancel()
	stop := context.AfterFunc(parent, cancel)
	defer stop()
	if err := parent.Err(); err != nil {
		return RunResultDTO{}, err
	}
	if err := ctx.Err(); err != nil {
		return RunResultDTO{}, err
	}
	current, err := s.store.ValidateBrainstormSession(ctx, ref, expected.ID, expected.SessionID)
	if err != nil {
		return RunResultDTO{}, safe("validate brainstorm attempt", err)
	}
	if !reflect.DeepEqual(current, expected) {
		return RunResultDTO{}, ErrInvalidInput
	}
	bound.mu.Lock()
	if bound.used {
		bound.mu.Unlock()
		return RunResultDTO{}, ErrInvalidInput
	}
	bound.used = true
	bound.mu.Unlock()
	runErr := bound.Prompt(ctx, text)
	// The public session outcome intentionally collapses cancellation details;
	// settlement needs the original safe classification for this bounded attempt.
	if errors.Is(runErr, context.DeadlineExceeded) {
		return RunResultDTO{}, context.DeadlineExceeded
	}
	if errors.Is(runErr, context.Canceled) {
		return RunResultDTO{}, context.Canceled
	}
	return s.sessionResult(expected.SessionID, "prompt", runErr)
}
