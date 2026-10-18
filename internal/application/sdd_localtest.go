package application

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/persioflexa/harflex/internal/catalog"
	"github.com/persioflexa/harflex/internal/id"
	"github.com/persioflexa/harflex/internal/repositories"
	"github.com/persioflexa/harflex/internal/sdd"
	"github.com/persioflexa/harflex/internal/security"
)

// ErrLocalTestConflict stops a Code decision whose automatic undo found changes made during the test: the person
// chooses on the card whether to keep or discard them.
var ErrLocalTestConflict = errors.New("files changed during the local test")

// ErrLocalTestFinished is a message to a tester whose test was undone: its folder is no longer the one it prepared.
var ErrLocalTestFinished = errors.New("this local test was undone")

// ErrLocalTestNeedsFullAccess is a Claude Code tester on a project that asks before commands.
var ErrLocalTestNeedsFullAccess = errors.New("the Claude Code tester needs Full access")

// ErrLocalTestBackendUnsupported is an executor that cannot run the tester: it needs Harflex's tools and a
// conversation it can continue (API profiles and Claude Code).
var ErrLocalTestBackendUnsupported = errors.New("this executor cannot prepare a local test")

type LocalTestServiceDTO struct {
	Name       string `json:"name"`
	TerminalID string `json:"terminalId"`
	Running    bool   `json:"running"`
}

type LocalTestConflictDTO struct {
	Edited  []string `json:"edited"`
	Created []string `json:"created"`
}

// PipelineLocalTestDTO is the Code review card's view of a local test.
type PipelineLocalTestDTO struct {
	PipelineID string `json:"pipelineId"`
	// Status is "applied" or "none".
	Status          string                `json:"status"`
	ArtifactVersion int64                 `json:"artifactVersion"`
	SessionID       string                `json:"sessionId"`
	TesterStatus    string                `json:"testerStatus"`
	Services        []LocalTestServiceDTO `json:"services"`
	// ServicesLost: the services were started, and none of their terminals is open (the app restarted).
	ServicesLost  bool                  `json:"servicesLost"`
	BackupMissing bool                  `json:"backupMissing"`
	Report        *LocalTestReportDTO   `json:"report,omitempty"`
	Conflict      *LocalTestConflictDTO `json:"conflict,omitempty"`
	// HeldBy names the other pipeline whose test holds the project, when this one has none.
	HeldBy   string `json:"heldBy,omitempty"`
	HeldByID string `json:"heldById,omitempty"`
}

type UndoPipelineLocalTestInput struct {
	PipelineID string `json:"pipelineId"`
	Discard    bool   `json:"discard"`
}

// localTestState is what a test record keeps to undo it: the Coder link whose copy was applied, and the apply.
type localTestState struct {
	CoderLinkID string                      `json:"coderLinkId"`
	Apply       repositories.LocalTestApply `json:"apply"`
}

func (s *Service) localTestCacheRoot() (string, error) {
	if s.executionCacheRoot != "" {
		return filepath.Join(filepath.Dir(s.executionCacheRoot), "sdd-local-tests"), nil
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(cache, "Harflex", "sdd-local-tests"), nil
}

// createPipelineLocalTestSession applies the Code under review to the project's folder, with a backup, and opens the
// conversation that brings the project up for the person to try.
func (s *Service) createPipelineLocalTestSession(in CreatePipelineSessionInput) (PipelineSessionDTO, error) {
	s.localTestMu.Lock()
	defer s.localTestMu.Unlock()
	adapter, external := s.external[in.BackendID]
	if in.BackendID == "" || external && in.BackendID != "claude" {
		return PipelineSessionDTO{}, ErrLocalTestBackendUnsupported
	}
	run, err := s.loadPipeline(in.PipelineID)
	if err != nil {
		return PipelineSessionDTO{}, err
	}
	// Claude Code reaches start_service over MCP, where nothing asks before a tool runs: on a project that asks before
	// commands it would run them in the person's folder unasked, so it takes Full access.
	if external {
		workspace, err := s.store.GetWorkspace(s.ctx, run.WorkspaceID)
		if err != nil {
			return PipelineSessionDTO{}, ErrWorkspaceNotFound
		}
		if security.Profile(workspace.Profile) != security.FullAccess {
			return PipelineSessionDTO{}, ErrLocalTestNeedsFullAccess
		}
		if !adapter.Capabilities().Resumable || !adapter.Detect().Available {
			return PipelineSessionDTO{}, ErrLocalTestBackendUnsupported
		}
	}
	artifact := run.Artifacts[sdd.Code]
	if run.Current != sdd.Code || run.Status[sdd.Code] != sdd.WaitingUser || artifact.Content == "" {
		return PipelineSessionDTO{}, sdd.ErrInvalidTransition
	}
	if active, err := s.store.ActivePipelineLocalTest(s.ctx, run.WorkspaceID); err == nil {
		return PipelineSessionDTO{}, &catalog.LocalTestBusyError{PipelineID: active.PipelineID}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return PipelineSessionDTO{}, safe("read the project's local test", err)
	}
	link, err := s.latestPipelineSession(run.ID, "coder")
	if err != nil || link.SessionID != artifact.SourceSessionID {
		return PipelineSessionDTO{}, ErrPipelineCodeSnapshotUnavailable
	}
	snapshot, err := s.executionSnapshotForLink(link)
	if err != nil {
		return PipelineSessionDTO{}, err
	}
	workspace, err := s.store.GetWorkspace(s.ctx, run.WorkspaceID)
	if err != nil {
		return PipelineSessionDTO{}, ErrWorkspaceNotFound
	}
	modelSelection, err := s.pipelineChatModelSelection(run, in, external)
	if err != nil {
		return PipelineSessionDTO{}, err
	}
	// The change reviewed is the change applied: the copy must still hold exactly that version.
	evidence, _, err := pipelineCodeEvidence(s.ctx, snapshot, catalog.MaxExecutionEvidenceBytes)
	if err != nil && !errors.Is(err, repositories.ErrExecutionSourceDrift) {
		return PipelineSessionDTO{}, err
	}
	if err == nil && evidence != artifact.Content {
		return PipelineSessionDTO{}, repositories.ErrExecutionSourceDrift
	}
	cache, err := s.localTestCacheRoot()
	if err != nil {
		return PipelineSessionDTO{}, safe("locate the local test backup", err)
	}
	testID := id.New()
	backup := filepath.Join(cache, fmt.Sprintf("%s-v%d-%s", run.ID, artifact.Version, testID[:8]))
	apply, err := repositories.ApplyExecutionForLocalTest(s.ctx, snapshot, backup, catalog.MaxExecutionEvidenceBytes)
	if err != nil {
		return PipelineSessionDTO{}, err
	}
	state, _ := json.Marshal(localTestState{CoderLinkID: link.ID, Apply: apply})
	record := catalog.PipelineLocalTest{ID: testID, PipelineID: run.ID, WorkspaceID: run.WorkspaceID, ArtifactVersion: int(artifact.Version), BackupPath: backup, Applied: state, Status: "applied", CreatedAt: time.Now().UTC()}
	undo := func(cause error) (PipelineSessionDTO, error) {
		_, _ = repositories.UndoExecutionLocalTest(s.ctx, snapshot, backup, apply, false)
		return PipelineSessionDTO{}, cause
	}
	if err := s.store.CreatePipelineLocalTest(s.ctx, record); err != nil {
		return undo(err)
	}
	session, err := s.createSessionWithExecutionRoot(CreateSessionInput{WorkspaceID: run.WorkspaceID, BackendID: in.BackendID, Mode: localTestMode}, nil, modelSelection, "")
	if err == nil {
		err = s.store.SetPipelineLocalTestSession(s.ctx, testID, session.ID)
	}
	if err != nil {
		_ = s.store.FinishPipelineLocalTest(s.ctx, testID, time.Now().UTC())
		return undo(err)
	}
	return PipelineSessionDTO{Session: session, Prompt: localTestPrompt(run, workspace.Path), Role: testerRole}, nil
}

// GetPipelineLocalTest is what the Code review card shows about the pipeline's local test.
func (s *Service) GetPipelineLocalTest(pipelineID string) (PipelineLocalTestDTO, error) {
	if err := s.beginCall(); err != nil {
		return PipelineLocalTestDTO{}, err
	}
	defer s.endCall()
	return s.localTestView(pipelineID, nil)
}

func (s *Service) localTestView(pipelineID string, conflict *repositories.LocalTestConflict) (PipelineLocalTestDTO, error) {
	run, err := s.loadPipeline(pipelineID)
	if err != nil {
		return PipelineLocalTestDTO{}, err
	}
	view := PipelineLocalTestDTO{PipelineID: pipelineID, Status: "none", Services: []LocalTestServiceDTO{}}
	test, err := s.store.LatestPipelineLocalTest(s.ctx, pipelineID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return PipelineLocalTestDTO{}, safe("read local test", err)
	}
	if err != nil || test.Status != "applied" {
		if active, err := s.store.ActivePipelineLocalTest(s.ctx, run.WorkspaceID); err == nil {
			view.HeldByID = active.PipelineID
			if holder, err := s.loadPipeline(active.PipelineID); err == nil {
				view.HeldBy = holder.Title
			}
		}
		return view, nil
	}
	view.Status, view.ArtifactVersion, view.SessionID = "applied", int64(test.ArtifactVersion), test.SessionID
	if record, err := s.store.GetSession(s.ctx, test.SessionID); err == nil {
		view.TesterStatus = record.Status
	}
	running := map[string]bool{}
	for _, info := range s.terminalManager().List() {
		running[info.ID] = info.Running
	}
	open := 0
	for _, service := range test.Services {
		alive, known := running[service.TerminalID]
		if known {
			open++
		}
		view.Services = append(view.Services, LocalTestServiceDTO{Name: service.Name, TerminalID: service.TerminalID, Running: alive})
	}
	view.ServicesLost = len(test.Services) > 0 && open == 0
	if _, err := os.Stat(test.BackupPath); err != nil {
		view.BackupMissing = true
	}
	if test.SessionID != "" {
		if _, text, err := s.coderTurn(test.SessionID); err == nil {
			if report, ok := localTestReport(text); ok {
				view.Report = &report
			}
		}
	}
	if conflict == nil {
		if snapshot, state, err := s.localTestSnapshot(run.ID, test); err == nil {
			if found, err := repositories.LocalTestConflicts(s.ctx, snapshot, state.Apply); err == nil && (len(found.Edited) > 0 || len(found.Created) > 0) {
				conflict = &found
			}
		}
	}
	if conflict != nil {
		view.Conflict = &LocalTestConflictDTO{Edited: nonNilStrings(conflict.Edited), Created: nonNilStrings(conflict.Created)}
	}
	return view, nil
}

func nonNilStrings(items []string) []string {
	if items == nil {
		return []string{}
	}
	return items
}

func (s *Service) localTestSnapshot(pipelineID string, test catalog.PipelineLocalTest) (repositories.ExecutionSnapshot, localTestState, error) {
	var state localTestState
	if err := json.Unmarshal(test.Applied, &state); err != nil {
		return repositories.ExecutionSnapshot{}, localTestState{}, safe("decode local test", err)
	}
	links, err := s.store.ListPipelineSessions(s.ctx, pipelineID, "coder")
	if err != nil {
		return repositories.ExecutionSnapshot{}, localTestState{}, safe("read Coder links", err)
	}
	for _, link := range links {
		if link.ID == state.CoderLinkID {
			snapshot, err := s.executionSnapshotForLink(link)
			return snapshot, state, err
		}
	}
	return repositories.ExecutionSnapshot{}, localTestState{}, ErrPipelineCodeSnapshotUnavailable
}

// UndoPipelineLocalTest stops the test's services and conversation and puts the folder back as it was. Changes made
// during the test stop it and come back as the card's conflict, unless the person chose to discard them.
func (s *Service) UndoPipelineLocalTest(in UndoPipelineLocalTestInput) (PipelineLocalTestDTO, error) {
	if err := s.beginCall(); err != nil {
		return PipelineLocalTestDTO{}, err
	}
	defer s.endCall()
	conflict, err := s.undoLocalTest(in.PipelineID, in.Discard)
	if err != nil {
		return PipelineLocalTestDTO{}, err
	}
	return s.localTestView(in.PipelineID, conflict)
}

// undoLocalTest returns the conflict that stopped it, or nil once the folder is back (or there was no test).
func (s *Service) undoLocalTest(pipelineID string, discard bool) (*repositories.LocalTestConflict, error) {
	s.localTestMu.Lock()
	defer s.localTestMu.Unlock()
	test, err := s.store.LatestPipelineLocalTest(s.ctx, pipelineID)
	if errors.Is(err, sql.ErrNoRows) || err == nil && test.Status != "applied" {
		return nil, nil
	}
	if err != nil {
		return nil, safe("read local test", err)
	}
	snapshot, state, err := s.localTestSnapshot(pipelineID, test)
	if err != nil {
		return nil, err
	}
	// The services and the conversation stop first: a server writing its logs would otherwise race the restore.
	if test.SessionID != "" {
		s.mu.RLock()
		runner := s.sessions[test.SessionID]
		s.mu.RUnlock()
		if runner != nil {
			runner.Cancel()
		}
	}
	manager := s.terminalManager()
	for _, service := range test.Services {
		_ = manager.Close(service.TerminalID)
	}
	conflict, err := repositories.UndoExecutionLocalTest(s.ctx, snapshot, test.BackupPath, state.Apply, discard)
	if err != nil {
		return nil, err
	}
	if len(conflict.Edited) > 0 || len(conflict.Created) > 0 {
		return &conflict, nil
	}
	for _, service := range test.Services {
		manager.Forget(service.TerminalID)
	}
	if err := s.store.FinishPipelineLocalTest(s.ctx, test.ID, time.Now().UTC()); err != nil {
		return nil, safe("finish local test", err)
	}
	return nil, nil
}

// releaseLocalTest undoes the pipeline's local test before something that needs the folder as it was (a Code
// decision, a new Code version). Changes made during the test stop it with ErrLocalTestConflict.
func (s *Service) releaseLocalTest(pipelineID string) error {
	conflict, err := s.undoLocalTest(pipelineID, false)
	if err != nil {
		return err
	}
	if conflict != nil {
		return ErrLocalTestConflict
	}
	return nil
}

// followsWorkspaceProfile: an ordinary chat and the local test take the project's permission profile, Full access
// included; the Code and QA conversations have their own.
func followsWorkspaceProfile(mode string) bool { return mode == "" || mode == localTestMode }

// localTestStillApplied says whether a tester conversation's test is still applied.
func (s *Service) localTestStillApplied(record catalog.SessionRecord) bool {
	test, err := s.store.ActivePipelineLocalTest(s.ctx, record.WorkspaceID)
	return err == nil && test.SessionID == record.ID
}

// holdLocalTest keeps a project's folder as it was before a pipeline copies or applies it: the pipeline's own test is
// undone first; another pipeline's test holds the project, and the person is pointed to it.
func (s *Service) holdLocalTest(pipelineID, workspaceID string) error {
	active, err := s.store.ActivePipelineLocalTest(s.ctx, workspaceID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return safe("read the project's local test", err)
	}
	if active.PipelineID != pipelineID {
		return &catalog.LocalTestBusyError{PipelineID: active.PipelineID}
	}
	return s.releaseLocalTest(pipelineID)
}
