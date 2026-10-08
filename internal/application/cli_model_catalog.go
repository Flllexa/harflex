package application

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/persioflexa/harflex/internal/modelcatalog"
)

type CLIModelCatalogQuery struct {
	WorkspaceID string `json:"workspaceId"`
	BackendID   string `json:"backendId"`
}

type cliModelCataloger interface {
	QueryModels(context.Context, string) (modelcatalog.Result, error)
}

// QueryCLIModelCatalog is an explicit, read-only discovery action scoped to a
// registered workspace. A model list is never treated as execution approval.
func (s *Service) QueryCLIModelCatalog(ctx context.Context, in CLIModelCatalogQuery) (modelcatalog.Result, error) {
	if err := s.beginCall(); err != nil {
		return modelcatalog.Result{}, err
	}
	defer s.endCall()
	if ctx == nil || in.WorkspaceID == "" || in.BackendID == "" {
		return modelcatalog.Result{}, ErrInvalidInput
	}
	if err := ctx.Err(); err != nil {
		return modelcatalog.Result{}, err
	}
	adapter, ok := s.external[in.BackendID]
	if !ok || adapter == nil || adapter.ID() != in.BackendID {
		return modelcatalog.Result{}, ErrBackendNotFound
	}
	cataloger, ok := adapter.(cliModelCataloger)
	if !ok {
		return modelcatalog.Result{BackendID: in.BackendID, Status: modelcatalog.StatusUnsupported, ErrorCode: "catalog_unsupported"}, nil
	}
	workspace, err := s.store.GetWorkspace(ctx, in.WorkspaceID)
	if errors.Is(err, sql.ErrNoRows) {
		return modelcatalog.Result{}, ErrWorkspaceNotFound
	}
	if err != nil {
		return modelcatalog.Result{}, safe("read catalog workspace", err)
	}
	actual, err := filepath.EvalSymlinks(workspace.Path)
	if err != nil || actual != workspace.Path {
		return modelcatalog.Result{BackendID: in.BackendID, Status: modelcatalog.StatusFailed, ErrorCode: "catalog_workspace_unavailable"}, nil
	}
	if info, statErr := os.Stat(actual); statErr != nil || !info.IsDir() {
		return modelcatalog.Result{BackendID: in.BackendID, Status: modelcatalog.StatusFailed, ErrorCode: "catalog_workspace_unavailable"}, nil
	}
	before := adapter.Detect()
	if !before.Available || before.Path == "" {
		return modelcatalog.Result{BackendID: in.BackendID, Status: modelcatalog.StatusFailed, ErrorCode: "catalog_cli_unavailable"}, nil
	}
	beforeRevision, err := cliCatalogRevision(in.BackendID, before.Path, before.Version, actual)
	if err != nil {
		return modelcatalog.Result{BackendID: in.BackendID, Status: modelcatalog.StatusFailed, ErrorCode: "catalog_config_unavailable"}, nil
	}
	result, err := cataloger.QueryModels(ctx, actual)
	if err != nil {
		if ctx.Err() != nil {
			return modelcatalog.Result{}, ctx.Err()
		}
		return modelcatalog.Result{BackendID: in.BackendID, Status: modelcatalog.StatusFailed, ErrorCode: "catalog_unavailable"}, nil
	}
	if result.BackendID != in.BackendID {
		return modelcatalog.Result{BackendID: in.BackendID, Status: modelcatalog.StatusFailed, ErrorCode: "catalog_unavailable"}, nil
	}
	if err := ctx.Err(); err != nil {
		return modelcatalog.Result{}, err
	}
	after := adapter.Detect()
	afterRevision, revisionErr := cliCatalogRevision(in.BackendID, after.Path, after.Version, actual)
	if !after.Available || revisionErr != nil || beforeRevision != afterRevision {
		result.Models = nil
		result.Complete = false
		result.Status = modelcatalog.StatusPartial
		result.ErrorCode = "catalog_cli_changed"
		return result, nil
	}
	contextRevision := result.ProfileRevision
	result.LocalRevision = beforeRevision
	if in.BackendID == "codex" && result.Complete {
		if contextRevision == "" {
			result.Status, result.Complete, result.ErrorCode = modelcatalog.StatusFailed, false, "catalog_context_unverified"
			result.Models = nil
		} else {
			digest := sha256.Sum256([]byte(beforeRevision + "\x00" + contextRevision))
			result.ProfileRevision = hex.EncodeToString(digest[:])
		}
	} else if in.BackendID != "codex" {
		result.ProfileRevision = beforeRevision
	}
	if result.CheckedAt.IsZero() {
		result.CheckedAt = time.Now().UTC()
	}
	return result, nil
}
