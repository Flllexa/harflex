package application

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"time"
	"unicode/utf8"

	"github.com/persioflexa/harflex/internal/catalog"
	"github.com/persioflexa/harflex/internal/sddworkspace"
	"github.com/persioflexa/harflex/internal/storage/sqlite"
)

const authoringCodePatchPageBytes = 256 << 10

var ErrAuthoringCodePatchUnavailable = errors.New("authoring Code patch is unavailable")

type GetAuthoringCodeRunPatchInput struct {
	PipelineID string `json:"pipelineId"`
	RunID      string `json:"runId"`
	Cursor     int64  `json:"cursor"`
}

// AuthoringCodeRunPatchPageDTO streams the exact persisted canonical patch in
// bounded UTF-8 slices. Every page repeats all snapshot bindings for readback.
type AuthoringCodeRunPatchPageDTO struct {
	PipelineID           string `json:"pipelineId"`
	RunID                string `json:"runId"`
	BaselineHash         string `json:"baselineHash"`
	SourceManifestHash   string `json:"sourceManifestHash"`
	ResultManifestHash   string `json:"resultManifestHash"`
	PatchHash            string `json:"patchHash"`
	SourceReadbackStatus string `json:"sourceReadbackStatus"`
	Cursor               int64  `json:"cursor"`
	NextCursor           *int64 `json:"nextCursor"`
	TotalBytes           int64  `json:"totalBytes"`
	Content              string `json:"content"`
}

func (s *Service) GetAuthoringCodeRunPatch(in GetAuthoringCodeRunPatchInput) (AuthoringCodeRunPatchPageDTO, error) {
	if err := s.beginCall(); err != nil {
		return AuthoringCodeRunPatchPageDTO{}, err
	}
	defer s.endCall()
	if !validSelectionText(in.PipelineID, 128) || !validSelectionText(in.RunID, 128) || in.Cursor < 0 {
		return AuthoringCodeRunPatchPageDTO{}, ErrInvalidInput
	}
	if _, err := s.loadPipeline(in.PipelineID); err != nil {
		return AuthoringCodeRunPatchPageDTO{}, err
	}
	run, err := s.store.GetAuthoringCodeRunByID(s.ctx, in.RunID)
	if errors.Is(err, sql.ErrNoRows) || err == sqlite.ErrAuthoringCodeRunNotFound {
		return AuthoringCodeRunPatchPageDTO{}, ErrAuthoringCodePatchUnavailable
	}
	if err != nil {
		return AuthoringCodeRunPatchPageDTO{}, safe("read authoring Code patch", err)
	}
	if run.PipelineID != in.PipelineID || run.Status != "completed" {
		return AuthoringCodeRunPatchPageDTO{}, ErrAuthoringCodePatchUnavailable
	}
	page, err := authoringCodePatchPage(run.PatchJSON, in.Cursor, authoringCodePatchPageBytes)
	if err != nil {
		return AuthoringCodeRunPatchPageDTO{}, err
	}
	prepared, err := s.validateAuthoringCodeEvidenceSnapshot(run)
	if err != nil {
		return AuthoringCodeRunPatchPageDTO{}, ErrAuthoringCodePatchUnavailable
	}
	readbackStatus := "unchecked"
	if in.Cursor == 0 {
		readbackStatus = s.currentAuthoringCodeReadback(run, prepared)
	}
	return AuthoringCodeRunPatchPageDTO{
		PipelineID: run.PipelineID, RunID: run.ID,
		BaselineHash: run.ManifestHash, SourceManifestHash: run.SourceManifest.Hash,
		ResultManifestHash: run.ResultManifest.Hash, PatchHash: run.PatchHash,
		SourceReadbackStatus: readbackStatus, Cursor: in.Cursor, NextCursor: page.NextCursor,
		TotalBytes: int64(len(run.PatchJSON)), Content: page.Content,
	}, nil
}

func (s *Service) verifyCurrentAuthoringCodeEvidence(run catalog.AuthoringCodeRun) error {
	prepared, err := s.validateAuthoringCodeEvidenceSnapshot(run)
	if err != nil || s.currentAuthoringCodeReadback(run, prepared) != "matched" {
		return ErrAuthoringCodePatchUnavailable
	}
	return nil
}

func (s *Service) validateAuthoringCodeEvidenceSnapshot(run catalog.AuthoringCodeRun) (catalog.AuthoringCodeCopyAttempt, error) {
	var empty catalog.AuthoringCodeCopyAttempt
	if run.Status != "completed" || run.ManifestHash == "" || run.PatchHash == "" || len(run.PatchJSON) == 0 ||
		run.SourceManifest == nil || run.ResultManifest == nil {
		return empty, ErrAuthoringCodePatchUnavailable
	}
	prepared, err := s.store.GetAuthoringCodeCopyAttempt(s.ctx, run.PipelineID, run.PreparationRequestID)
	if err != nil || prepared.ID != run.PreparationID || prepared.Status != "prepared" ||
		prepared.PipelineID != run.PipelineID || prepared.ManifestHash != run.ManifestHash ||
		prepared.PrivatePath != run.PrivatePath || !reflect.DeepEqual(prepared.ManifestSnapshot, run.ManifestSnapshot) {
		return empty, ErrAuthoringCodePatchUnavailable
	}
	workspace, err := s.store.GetWorkspace(s.ctx, run.WorkspaceID)
	if err != nil || workspace.Path != prepared.SourcePath {
		return empty, ErrAuthoringCodePatchUnavailable
	}
	baseline := authoringCodeCopyManifest(run.ManifestSnapshot)
	source := authoringCodeCopyManifest(*run.SourceManifest)
	result := authoringCodeCopyManifest(*run.ResultManifest)
	if run.ManifestHash != baseline.Hash || source.Hash != baseline.Hash ||
		!reflect.DeepEqual(authoringCodeCopyManifestSnapshot(baseline), run.ManifestSnapshot) ||
		!reflect.DeepEqual(*run.SourceManifest, run.ManifestSnapshot) ||
		!reflect.DeepEqual(authoringCodeCopyManifestSnapshot(result), *run.ResultManifest) ||
		sddworkspace.ValidatePatchEvidence(run.PatchJSON, run.PatchHash, baseline, source.Hash, result) != nil {
		return empty, ErrAuthoringCodePatchUnavailable
	}
	wantChanges := sddworkspace.Compare(baseline, result)
	if len(wantChanges) != len(run.Changes) {
		return empty, ErrAuthoringCodePatchUnavailable
	}
	for index, change := range wantChanges {
		if run.Changes[index] != (catalog.CodeFileChange{Path: change.Path, Kind: string(change.Kind)}) {
			return empty, ErrAuthoringCodePatchUnavailable
		}
	}
	return prepared, nil
}

func (s *Service) currentAuthoringCodeReadback(run catalog.AuthoringCodeRun, prepared catalog.AuthoringCodeCopyAttempt) string {
	currentSource, err := s.scanAuthoringCodeRoot(prepared.SourcePath)
	if err != nil {
		return "unavailable"
	}
	currentResult, err := s.scanAuthoringCodeRoot(prepared.PrivatePath)
	if err != nil {
		return "unavailable"
	}
	if !reflect.DeepEqual(authoringCodeCopyManifestSnapshot(currentSource), *run.SourceManifest) ||
		!reflect.DeepEqual(authoringCodeCopyManifestSnapshot(currentResult), *run.ResultManifest) {
		return "drifted"
	}
	return "matched"
}

func (s *Service) scanAuthoringCodeRoot(root string) (sddworkspace.Manifest, error) {
	timeout := s.authoringCodeReadbackTimeout
	if timeout <= 0 {
		timeout = 15 * time.Minute
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(s.ctx), timeout)
	defer cancel()
	return sddworkspace.ScanNoFollow(ctx, root)
}

type authoringCodePatchPageSlice struct {
	Content    string
	NextCursor *int64
}

func authoringCodePatchPage(data []byte, cursor int64, maxBytes int) (authoringCodePatchPageSlice, error) {
	if len(data) == 0 || int64(len(data)) > sddworkspace.MaxPatchEvidenceBytes || cursor < 0 || cursor >= int64(len(data)) || maxBytes < 4 {
		return authoringCodePatchPageSlice{}, ErrInvalidInput
	}
	start := int(cursor)
	if start < len(data) && !utf8.RuneStart(data[start]) {
		return authoringCodePatchPageSlice{}, ErrInvalidInput
	}
	end := start + maxBytes
	if end > len(data) {
		end = len(data)
	}
	for end > start && end < len(data) && !utf8.RuneStart(data[end]) {
		end--
	}
	if end == start && start < len(data) {
		_, width := utf8.DecodeRune(data[start:])
		end = start + width
	}
	page := authoringCodePatchPageSlice{Content: string(data[start:end])}
	if !utf8.ValidString(page.Content) {
		return authoringCodePatchPageSlice{}, fmt.Errorf("patch page contains invalid UTF-8")
	}
	if end < len(data) {
		next := int64(end)
		page.NextCursor = &next
	}
	return page, nil
}
