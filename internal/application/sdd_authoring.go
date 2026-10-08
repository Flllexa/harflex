package application

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/persioflexa/harflex/internal/catalog"
	"github.com/persioflexa/harflex/internal/id"
	"github.com/persioflexa/harflex/internal/sdd"
)

const maxDiscoveryBytes = 1024 * 1024

var ErrPipelineRequestConflict = errors.New("pipeline request ID was reused for different authoring content")

var authoringRequestID = regexp.MustCompile(`^[A-Za-z0-9_-]{16,64}$`)

func authoringRequestHash(fields ...string) (string, error) {
	canonical, err := json.Marshal(fields)
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(canonical)
	return hex.EncodeToString(hash[:]), nil
}

func (s *Service) resolveAuthoringRequest(workspaceID, parentID, requestID, hash string) (PipelineDTO, bool, error) {
	existing, err := s.store.GetAuthoringPipelineByRequest(s.ctx, workspaceID, parentID, requestID)
	if errors.Is(err, sql.ErrNoRows) {
		return PipelineDTO{}, false, nil
	}
	if err != nil {
		return PipelineDTO{}, false, safe("get authoring request", err)
	}
	if existing.CreationRequestHash != hash {
		return PipelineDTO{}, true, ErrPipelineRequestConflict
	}
	return pipelineDTO(existing), true, nil
}

func (s *Service) resolveAuthoringWriteFailure(workspaceID, parentID, requestID, hash, operation string, writeErr error) (PipelineDTO, error) {
	existing, found, err := s.resolveAuthoringRequest(workspaceID, parentID, requestID, hash)
	if found {
		return existing, err
	}
	if err != nil {
		return PipelineDTO{}, safe(operation, errors.Join(writeErr, err))
	}
	return PipelineDTO{}, safe(operation, writeErr)
}

func validDiscovery(value string) bool {
	return strings.TrimSpace(value) != "" && len(value) <= maxDiscoveryBytes && utf8.ValidString(value) && !strings.ContainsRune(value, 0)
}

func clipUTF8(value string, maximum int) string {
	if len(value) <= maximum {
		return value
	}
	cut := maximum
	for cut > 0 && !utf8.RuneStart(value[cut]) {
		cut--
	}
	return value[:cut]
}

func derivedPipelineText(discovery string) (title, objective string) {
	for _, line := range strings.Split(discovery, "\n") {
		line = strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(line), "#"))
		if line != "" {
			title = clipUTF8(line, 200)
			break
		}
	}
	if title == "" {
		title = "Novo trabalho"
	}
	objective = strings.TrimSpace(clipUTF8(strings.TrimSpace(discovery), 5000))
	return title, objective
}

func (s *Service) CreateAuthoringPipeline(in CreateAuthoringPipelineInput) (PipelineDTO, error) {
	if err := s.beginCall(); err != nil {
		return PipelineDTO{}, err
	}
	defer s.endCall()
	if in.WorkspaceID == "" || !authoringRequestID.MatchString(in.RequestID) || !validDiscovery(in.Discovery) {
		return PipelineDTO{}, ErrInvalidInput
	}
	hash, err := authoringRequestHash("create_authoring_pipeline", in.WorkspaceID, in.Discovery)
	if err != nil {
		return PipelineDTO{}, safe("hash authoring request", err)
	}
	if existing, found, err := s.resolveAuthoringRequest(in.WorkspaceID, "", in.RequestID, hash); found || err != nil {
		return existing, err
	}
	if _, err := s.store.GetWorkspace(s.ctx, in.WorkspaceID); errors.Is(err, sql.ErrNoRows) {
		return PipelineDTO{}, ErrWorkspaceNotFound
	} else if err != nil {
		return PipelineDTO{}, safe("get workspace", err)
	}
	title, objective := derivedPipelineText(in.Discovery)
	flow := sdd.NewFlow()
	now := time.Now().UTC()
	run := catalog.PipelineRun{ID: id.New(), WorkspaceID: in.WorkspaceID, Kind: "ai_authoring", Title: title, Objective: objective,
		Current: flow.Current, Status: flow.Status, Revision: 1, CreatedAt: now, UpdatedAt: now,
		CreationRequestID: in.RequestID, CreationRequestHash: hash}
	if err := s.store.CreateAuthoringPipeline(s.ctx, run, in.Discovery); err != nil {
		return s.resolveAuthoringWriteFailure(in.WorkspaceID, "", in.RequestID, hash, "create authoring pipeline", err)
	}
	stored, err := s.loadPipeline(run.ID)
	if err != nil {
		return PipelineDTO{}, err
	}
	return pipelineDTO(stored), nil
}

func (s *Service) DeriveAuthoringPipeline(in DeriveAuthoringPipelineInput) (PipelineDTO, error) {
	if err := s.beginCall(); err != nil {
		return PipelineDTO{}, err
	}
	defer s.endCall()
	if in.ParentPipelineID == "" || !authoringRequestID.MatchString(in.RequestID) || in.ExpectedRevision < 1 || !validDiscovery(in.Discovery) {
		return PipelineDTO{}, ErrInvalidInput
	}
	parent, err := s.loadPipeline(in.ParentPipelineID)
	if err != nil {
		return PipelineDTO{}, err
	}
	hash, err := authoringRequestHash("derive_authoring_pipeline", parent.ID, parent.WorkspaceID, strconv.FormatInt(in.ExpectedRevision, 10), in.Discovery)
	if err != nil {
		return PipelineDTO{}, safe("hash authoring request", err)
	}
	if existing, found, err := s.resolveAuthoringRequest(parent.WorkspaceID, parent.ID, in.RequestID, hash); found || err != nil {
		return existing, err
	}
	canDerive := parent.Kind == "ai_authoring" && parent.DiscoveryFrozenVersion > 0
	if parent.Kind == "legacy" {
		canDerive = strings.TrimSpace(parent.Artifacts[sdd.Discovery].Content) != ""
	}
	if !canDerive {
		return PipelineDTO{}, sdd.ErrInvalidTransition
	}
	title, objective := derivedPipelineText(in.Discovery)
	flow := sdd.NewFlow()
	now := time.Now().UTC()
	child := catalog.PipelineRun{ID: id.New(), WorkspaceID: parent.WorkspaceID, Kind: "ai_authoring", DerivedFromPipelineID: parent.ID,
		Title: title, Objective: objective, Current: flow.Current, Status: flow.Status, Revision: 1, CreatedAt: now, UpdatedAt: now,
		CreationRequestID: in.RequestID, CreationRequestHash: hash}
	if err := s.store.DeriveAuthoringPipeline(s.ctx, parent.ID, in.ExpectedRevision, child, in.Discovery); err != nil {
		return s.resolveAuthoringWriteFailure(parent.WorkspaceID, parent.ID, in.RequestID, hash, "derive authoring pipeline", err)
	}
	stored, err := s.loadPipeline(child.ID)
	if err != nil {
		return PipelineDTO{}, err
	}
	return pipelineDTO(stored), nil
}

func (s *Service) ReviseAuthoringDiscovery(in ReviseAuthoringDiscoveryInput) (PipelineDTO, error) {
	if err := s.beginCall(); err != nil {
		return PipelineDTO{}, err
	}
	defer s.endCall()
	if in.PipelineID == "" || in.ExpectedRevision < 1 || in.ExpectedVersion < 1 || !validDiscovery(in.Discovery) {
		return PipelineDTO{}, ErrInvalidInput
	}
	run, err := s.loadPipeline(in.PipelineID)
	if err != nil {
		return PipelineDTO{}, err
	}
	if run.Kind != "ai_authoring" || run.Current != sdd.Discovery || run.DiscoveryFrozenVersion != 0 {
		return PipelineDTO{}, sdd.ErrInvalidTransition
	}
	title, objective := derivedPipelineText(in.Discovery)
	if err := s.store.ReviseAuthoringDiscovery(s.ctx, run.ID, in.ExpectedRevision, in.ExpectedVersion, in.Discovery, title, objective); err != nil {
		return PipelineDTO{}, safe("revise authoring Discovery", err)
	}
	updated, err := s.loadPipeline(run.ID)
	if err != nil {
		return PipelineDTO{}, err
	}
	return pipelineDTO(updated), nil
}
