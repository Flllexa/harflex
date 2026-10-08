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
	"github.com/persioflexa/harflex/internal/sdd"
)

type authoringStageRunner struct {
	sessionRunner
	ref     catalog.AuthoringStageRequest
	attempt catalog.AuthoringStageAttempt
	mu      sync.Mutex
	used    bool
}

func (r *authoringStageRunner) Abort(ctx context.Context) error {
	r.mu.Lock()
	r.used = true
	r.mu.Unlock()
	if aborter, ok := r.sessionRunner.(interface{ Abort(context.Context) error }); ok {
		return aborter.Abort(ctx)
	}
	r.sessionRunner.Cancel()
	return sdd.ErrAuthoringCancellationPending
}

// Only a newly admitted owner may call this helper. The durable session link
// and immutable selection are committed before provider composition can occur.
func (s *Service) createAuthoringReadOnlySession(ref catalog.AuthoringStageRequest, attempt catalog.AuthoringStageAttempt) (session SessionDTO, err error) {
	if err = s.beginCall(); err != nil {
		return session, err
	}
	defer s.endCall()
	if attempt.Selection.BackendID == "codex" || attempt.Selection.Source == "codex_app_server" || s.external[attempt.Selection.BackendID] != nil {
		return session, ErrSDDCLIReadIsolationUnavailable
	}
	s.recoveryGate.RLock()
	defer s.recoveryGate.RUnlock()
	if attempt.PipelineID != ref.PipelineID || attempt.Stage != ref.Stage || attempt.Status != "running" || attempt.SessionID != "" {
		return session, ErrInvalidInput
	}
	pipeline, err := s.store.GetPipeline(s.ctx, ref.PipelineID)
	if err != nil {
		return session, safe("read authoring pipeline", err)
	}
	workspace, err := s.store.GetWorkspace(s.ctx, pipeline.WorkspaceID)
	if err != nil {
		return session, safe("read authoring workspace", err)
	}
	backendRevision := ""
	if attempt.Selection.Source == "codex_app_server" && attempt.Selection.BackendID == "codex" {
		if s.external["codex"] == nil || attempt.Selection.MaxAssistantOutputBytes != sdd.MaxAuthoringDocumentBytes {
			return session, ErrBackendNotFound
		}
		backendRevision = "cli:codex"
	} else {
		if _, external := s.external[attempt.Selection.BackendID]; external {
			return session, ErrInvalidInput
		}
		profile, profileErr := s.store.GetProviderProfile(s.ctx, attempt.Selection.BackendID)
		if profileErr != nil {
			return session, safe("read authoring profile", profileErr)
		}
		if profile.Kind != "openai_compatible" || profileRevision(profile) != attempt.Selection.CatalogRevision {
			return session, ErrBackendChanged
		}
		backendRevision = profileRevision(profile)
	}
	now := time.Now().UTC()
	record := catalog.SessionRecord{ID: id.New(), WorkspaceID: workspace.ID, BackendID: attempt.Selection.BackendID, BackendRevision: backendRevision, Mode: "sdd_readonly", Status: "ready", CreatedAt: now, UpdatedAt: now}
	if err = s.store.CreateAuthoringStageSession(s.ctx, ref, attempt, record); err != nil {
		return session, safe("link authoring session", err)
	}
	installed := false
	defer func() {
		if installed {
			return
		}
		ctx, cancel := context.WithTimeout(context.WithoutCancel(s.ctx), 2*time.Second)
		defer cancel()
		code := authoringFailureCode(RunResultDTO{}, err, attempt)
		if _, pauseErr := s.store.FailAuthoringStage(ctx, catalog.FailAuthoringStageRequest{Ref: ref, AttemptID: attempt.ID, ErrorCode: code}); pauseErr != nil {
			err = errors.Join(err, safe("pause authoring composition", pauseErr))
		}
	}()
	linked, err := s.store.ValidateAuthoringStageSession(s.ctx, ref, attempt.ID, record.ID)
	if err != nil {
		return session, safe("read authoring link", err)
	}
	selection, err := s.store.GetSessionModelSelection(s.ctx, record.ID)
	if err != nil {
		return session, safe("read authoring selection", err)
	}
	if selection.Source == "codex_app_server" {
		selection.MaxAssistantOutputBytes = attempt.Selection.MaxAssistantOutputBytes
	}
	runner, journal, err := s.makeRunner(&record, workspace, restoredHistory{}, false, nil, "", &selection)
	if err != nil {
		return session, err
	}
	validateAttempt := func(ctx context.Context) error {
		current, err := s.store.ValidateAuthoringStageSession(ctx, ref, linked.ID, linked.SessionID)
		if err != nil {
			return safe("validate authoring before inference", err)
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
			return session, ErrInvalidInput
		}
		if err = core.SetAssistantOutputByteLimit(sdd.MaxAuthoringDocumentBytes); err != nil {
			journal.Close()
			return session, err
		}
		selected.validateAttempt = validateAttempt
	case *selectedCLIRunner:
		if selected.selection.MaxAssistantOutputBytes != sdd.MaxAuthoringDocumentBytes {
			journal.Close()
			return session, ErrBackendChanged
		}
		selected.validateAttempt = validateAttempt
	default:
		journal.Close()
		return session, ErrInvalidInput
	}
	s.mu.Lock()
	if s.closing {
		s.mu.Unlock()
		journal.Close()
		return session, context.Canceled
	}
	s.sessions[record.ID] = &authoringStageRunner{sessionRunner: runner, ref: ref, attempt: linked}
	s.journals[record.ID] = journal
	s.mu.Unlock()
	installed = true
	return s.sessionDTO(record), nil
}

func (s *Service) promptAuthoringStage(parent context.Context, ref catalog.AuthoringStageRequest, expected catalog.AuthoringStageAttempt, text string) (RunResultDTO, error) {
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
	bound, ok := runner.(*authoringStageRunner)
	if !ok || bound.ref != ref || !reflect.DeepEqual(bound.attempt, expected) {
		return RunResultDTO{}, ErrInvalidInput
	}
	ctx, cancel := context.WithDeadline(s.ctx, expected.CreatedAt.Add(sdd.AuthoringAttemptTimeout))
	defer cancel()
	stop := context.AfterFunc(parent, cancel)
	defer stop()
	if err := parent.Err(); err != nil {
		return RunResultDTO{}, err
	}
	if err := ctx.Err(); err != nil {
		return RunResultDTO{}, err
	}
	current, err := s.store.ValidateAuthoringStageSession(ctx, ref, expected.ID, expected.SessionID)
	if err != nil {
		return RunResultDTO{}, safe("validate authoring prompt", err)
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
	if errors.Is(runErr, context.DeadlineExceeded) {
		return RunResultDTO{}, context.DeadlineExceeded
	}
	if errors.Is(runErr, context.Canceled) {
		return RunResultDTO{}, context.Canceled
	}
	return s.sessionResult(expected.SessionID, "authoring prompt", runErr)
}
