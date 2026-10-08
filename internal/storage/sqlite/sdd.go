package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/persioflexa/harflex/internal/catalog"
	"github.com/persioflexa/harflex/internal/id"
	"github.com/persioflexa/harflex/internal/sdd"
)

var ErrPipelineConflict = errors.New("pipeline changed concurrently")

func appendPipelineEvent(ctx context.Context, tx *sql.Tx, pipelineID, eventType string, data any, now time.Time) error {
	encoded, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("marshal pipeline event: %w", err)
	}
	var sequence int64
	if err := tx.QueryRowContext(ctx, "SELECT COALESCE(MAX(sequence), 0) + 1 FROM events WHERE stream_id = ?", pipelineID).Scan(&sequence); err != nil {
		return fmt.Errorf("read pipeline sequence: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO events (id, stream_id, sequence, type, data, created_at) VALUES (?, ?, ?, ?, ?, ?)", id.New(), pipelineID, sequence, eventType, encoded, formatCatalogTime(now)); err != nil {
		return fmt.Errorf("append pipeline event: %w", err)
	}
	return nil
}

func (s *Store) CreatePipeline(ctx context.Context, run catalog.PipelineRun) error {
	status, err := json.Marshal(run.Status)
	if err != nil {
		return fmt.Errorf("marshal pipeline status: %w", err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin pipeline: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `INSERT INTO pipeline_runs (id, workspace_id, title, objective, current_stage, stage_status, revision, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, run.ID, run.WorkspaceID, run.Title, run.Objective, run.Current, status, run.Revision, formatCatalogTime(run.CreatedAt), formatCatalogTime(run.UpdatedAt)); err != nil {
		return fmt.Errorf("insert pipeline: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO streams (id, kind, created_at) VALUES (?, ?, ?)", run.ID, "sdd_pipeline", formatCatalogTime(run.CreatedAt)); err != nil {
		return fmt.Errorf("create pipeline stream: %w", err)
	}
	if err := appendPipelineEvent(ctx, tx, run.ID, "pipeline.created", map[string]any{"stage": run.Current}, run.CreatedAt); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit pipeline: %w", err)
	}
	return nil
}

func validAuthoringRun(run catalog.PipelineRun, discoveryContent string) bool {
	return run.Kind == "ai_authoring" && run.Current == sdd.Discovery && run.Revision == 1 &&
		run.DiscoveryFrozenVersion == 0 && strings.TrimSpace(discoveryContent) != "" &&
		(run.CreationRequestID == "") == (run.CreationRequestHash == "")
}

func insertAuthoringPipeline(ctx context.Context, tx *sql.Tx, run catalog.PipelineRun, discoveryContent string) error {
	status, err := json.Marshal(run.Status)
	if err != nil {
		return fmt.Errorf("marshal authoring pipeline status: %w", err)
	}
	var parentID any
	if run.DerivedFromPipelineID != "" {
		parentID = run.DerivedFromPipelineID
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO pipeline_runs (id, workspace_id, title, objective, current_stage, stage_status, revision, created_at, updated_at, kind, derived_from_pipeline_id, discovery_frozen_version, creation_request_id, creation_request_hash)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, run.ID, run.WorkspaceID, run.Title, run.Objective, run.Current, status, run.Revision, formatCatalogTime(run.CreatedAt), formatCatalogTime(run.UpdatedAt), run.Kind, parentID, run.DiscoveryFrozenVersion, run.CreationRequestID, run.CreationRequestHash); err != nil {
		return fmt.Errorf("insert authoring pipeline: %w", err)
	}
	return insertAuthoringPipelineDetails(ctx, tx, run, discoveryContent)
}

func insertAuthoringPipelineDetails(ctx context.Context, tx *sql.Tx, run catalog.PipelineRun, discoveryContent string) error {
	if _, err := tx.ExecContext(ctx, "INSERT INTO streams (id, kind, created_at) VALUES (?, ?, ?)", run.ID, "sdd_pipeline", formatCatalogTime(run.CreatedAt)); err != nil {
		return fmt.Errorf("create authoring pipeline stream: %w", err)
	}
	if err := appendPipelineEvent(ctx, tx, run.ID, "pipeline.created", map[string]any{"stage": run.Current, "kind": run.Kind, "derivedFromPipelineId": run.DerivedFromPipelineID}, run.CreatedAt); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO pipeline_artifacts (pipeline_id, stage, version, content, author, source_session_id, updated_at) VALUES (?, ?, 1, ?, 'user', '', ?)`, run.ID, sdd.Discovery, discoveryContent, formatCatalogTime(run.CreatedAt)); err != nil {
		return fmt.Errorf("insert authoring Discovery: %w", err)
	}
	if err := appendPipelineEvent(ctx, tx, run.ID, "pipeline.artifact.saved", map[string]any{"stage": sdd.Discovery, "version": 1, "author": "user"}, run.CreatedAt); err != nil {
		return err
	}
	return nil
}

func (s *Store) CreateAuthoringPipeline(ctx context.Context, run catalog.PipelineRun, discoveryContent string) error {
	if !validAuthoringRun(run, discoveryContent) || run.DerivedFromPipelineID != "" {
		return sdd.ErrInvalidTransition
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin authoring pipeline: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := insertAuthoringPipeline(ctx, tx, run, discoveryContent); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit authoring pipeline: %w", err)
	}
	return nil
}

func (s *Store) FreezeDiscovery(ctx context.Context, pipelineID string, expectedRevision, expectedVersion int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin Discovery freeze: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := legacyPipelineDesignFence(ctx, tx, pipelineID); err != nil {
		return err
	}
	now := time.Now().UTC()
	result, err := tx.ExecContext(ctx, `UPDATE pipeline_runs SET discovery_frozen_version = ?, revision = revision + 1, updated_at = ?
		WHERE id = ? AND kind = 'ai_authoring' AND current_stage = 'discovery' AND revision = ? AND discovery_frozen_version = 0
		AND EXISTS (SELECT 1 FROM pipeline_artifacts WHERE pipeline_id = ? AND stage = 'discovery' AND version = ? AND author = 'user')`,
		expectedVersion, formatCatalogTime(now), pipelineID, expectedRevision, pipelineID, expectedVersion)
	if err != nil {
		return fmt.Errorf("freeze Discovery: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read Discovery freeze: %w", err)
	}
	if changed != 1 {
		return ErrPipelineConflict
	}
	if err := appendPipelineEvent(ctx, tx, pipelineID, "pipeline.discovery.frozen", map[string]any{"version": expectedVersion}, now); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit Discovery freeze: %w", err)
	}
	return nil
}

func (s *Store) DeriveAuthoringPipeline(ctx context.Context, parentID string, expectedParentRevision int64, childRun catalog.PipelineRun, discoveryContent string) error {
	if !validAuthoringRun(childRun, discoveryContent) || childRun.DerivedFromPipelineID != parentID || childRun.ID == parentID {
		return sdd.ErrInvalidTransition
	}
	status, err := json.Marshal(childRun.Status)
	if err != nil {
		return fmt.Errorf("marshal derived pipeline status: %w", err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin derived authoring pipeline: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	result, err := tx.ExecContext(ctx, `INSERT INTO pipeline_runs (id, workspace_id, title, objective, current_stage, stage_status, revision, created_at, updated_at, kind, derived_from_pipeline_id, discovery_frozen_version, creation_request_id, creation_request_hash)
		SELECT ?, parent.workspace_id, ?, ?, ?, ?, ?, ?, ?, ?, parent.id, ?, ?, ? FROM pipeline_runs AS parent
		WHERE parent.id = ? AND parent.workspace_id = ? AND parent.revision = ?
		AND (parent.kind = 'legacy' OR (parent.kind = 'ai_authoring' AND parent.discovery_frozen_version > 0))
		AND EXISTS (SELECT 1 FROM pipeline_artifacts AS discovery WHERE discovery.pipeline_id=parent.id AND discovery.stage='discovery' AND length(trim(discovery.content))>0)`,
		childRun.ID, childRun.Title, childRun.Objective, childRun.Current, status, childRun.Revision, formatCatalogTime(childRun.CreatedAt), formatCatalogTime(childRun.UpdatedAt), childRun.Kind, childRun.DiscoveryFrozenVersion, childRun.CreationRequestID, childRun.CreationRequestHash,
		parentID, childRun.WorkspaceID, expectedParentRevision)
	if err != nil {
		return fmt.Errorf("insert derived authoring pipeline: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read derived authoring pipeline insert: %w", err)
	}
	if changed != 1 {
		return ErrPipelineConflict
	}
	if err := insertAuthoringPipelineDetails(ctx, tx, childRun, discoveryContent); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit derived authoring pipeline: %w", err)
	}
	return nil
}

func (s *Store) ReviseAuthoringDiscovery(ctx context.Context, pipelineID string, expectedRevision, expectedVersion int64, content, title, objective string) error {
	if strings.TrimSpace(content) == "" {
		return sdd.ErrInvalidTransition
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin authoring Discovery revision: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := legacyPipelineDesignFence(ctx, tx, pipelineID); err != nil {
		return err
	}
	now := time.Now().UTC()
	result, err := tx.ExecContext(ctx, `UPDATE pipeline_runs SET title = ?, objective = ?, revision = revision + 1, updated_at = ?
		WHERE id = ? AND kind = 'ai_authoring' AND current_stage = 'discovery' AND discovery_frozen_version = 0 AND revision = ?
		AND EXISTS (SELECT 1 FROM pipeline_artifacts WHERE pipeline_id = ? AND stage = 'discovery' AND version = ? AND author = 'user')`,
		title, objective, formatCatalogTime(now), pipelineID, expectedRevision, pipelineID, expectedVersion)
	if err != nil {
		return fmt.Errorf("update authoring pipeline summary: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read authoring pipeline revision: %w", err)
	}
	if changed != 1 {
		return ErrPipelineConflict
	}
	result, err = tx.ExecContext(ctx, `UPDATE pipeline_artifacts SET version = version + 1, content = ?, updated_at = ?
		WHERE pipeline_id = ? AND stage = 'discovery' AND version = ? AND author = 'user'`, content, formatCatalogTime(now), pipelineID, expectedVersion)
	if err != nil {
		return fmt.Errorf("update authoring Discovery: %w", err)
	}
	changed, err = result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read authoring Discovery revision: %w", err)
	}
	if changed != 1 {
		return ErrPipelineConflict
	}
	if err := appendPipelineEvent(ctx, tx, pipelineID, "pipeline.artifact.saved", map[string]any{"stage": sdd.Discovery, "version": expectedVersion + 1, "author": "user"}, now); err != nil {
		return err
	}
	if err := invalidateBrainstormDiscovery(ctx, tx, pipelineID, expectedVersion, now); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit authoring Discovery revision: %w", err)
	}
	return nil
}

type rowScanner interface{ Scan(...any) error }

func scanPipeline(row rowScanner) (catalog.PipelineRun, error) {
	var run catalog.PipelineRun
	var status []byte
	var created, updated string
	var parentID sql.NullString
	if err := row.Scan(&run.ID, &run.WorkspaceID, &run.Title, &run.Objective, &run.Current, &status, &run.Revision, &created, &updated, &run.Kind, &parentID, &run.DiscoveryFrozenVersion, &run.CreationRequestID, &run.CreationRequestHash); err != nil {
		return catalog.PipelineRun{}, err
	}
	run.DerivedFromPipelineID = parentID.String
	if err := json.Unmarshal(status, &run.Status); err != nil {
		return catalog.PipelineRun{}, fmt.Errorf("decode pipeline status: %w", err)
	}
	var err error
	run.CreatedAt, err = time.Parse(time.RFC3339Nano, created)
	if err != nil {
		return catalog.PipelineRun{}, fmt.Errorf("parse pipeline created timestamp: %w", err)
	}
	run.UpdatedAt, err = time.Parse(time.RFC3339Nano, updated)
	if err != nil {
		return catalog.PipelineRun{}, fmt.Errorf("parse pipeline updated timestamp: %w", err)
	}
	run.Artifacts = make(map[sdd.Stage]catalog.PipelineArtifact)
	return run, nil
}

const pipelineColumns = "id, workspace_id, title, objective, current_stage, stage_status, revision, created_at, updated_at, kind, derived_from_pipeline_id, discovery_frozen_version, creation_request_id, creation_request_hash"

func (s *Store) GetAuthoringPipelineByRequest(ctx context.Context, workspaceID, parentID, requestID string) (catalog.PipelineRun, error) {
	var pipelineID string
	if err := s.db.QueryRowContext(ctx, `SELECT id FROM pipeline_runs
		WHERE kind = 'ai_authoring' AND workspace_id = ? AND COALESCE(derived_from_pipeline_id, '') = ?
		AND creation_request_id = ? AND creation_request_id <> ''`, workspaceID, parentID, requestID).Scan(&pipelineID); err != nil {
		return catalog.PipelineRun{}, fmt.Errorf("get authoring pipeline by request: %w", err)
	}
	return s.GetPipeline(ctx, pipelineID)
}

type pipelineQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func readPipelineSnapshot(ctx context.Context, queryer pipelineQueryer, pipelineID string) (catalog.PipelineRun, error) {
	run, err := scanPipeline(queryer.QueryRowContext(ctx, "SELECT "+pipelineColumns+" FROM pipeline_runs WHERE id = ?", pipelineID))
	if err != nil {
		return catalog.PipelineRun{}, fmt.Errorf("get pipeline: %w", err)
	}
	rows, err := queryer.QueryContext(ctx, "SELECT stage, version, content, author, source_session_id, updated_at FROM pipeline_artifacts WHERE pipeline_id = ?", pipelineID)
	if err != nil {
		return catalog.PipelineRun{}, fmt.Errorf("list pipeline artifacts: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var artifact catalog.PipelineArtifact
		var updated string
		if err := rows.Scan(&artifact.Stage, &artifact.Version, &artifact.Content, &artifact.Author, &artifact.SourceSessionID, &updated); err != nil {
			return catalog.PipelineRun{}, fmt.Errorf("scan pipeline artifact: %w", err)
		}
		artifact.UpdatedAt, err = time.Parse(time.RFC3339Nano, updated)
		if err != nil {
			return catalog.PipelineRun{}, fmt.Errorf("parse artifact timestamp: %w", err)
		}
		run.Artifacts[artifact.Stage] = artifact
	}
	if err := rows.Err(); err != nil {
		return catalog.PipelineRun{}, fmt.Errorf("iterate pipeline artifacts: %w", err)
	}
	if err := rows.Close(); err != nil {
		return catalog.PipelineRun{}, fmt.Errorf("close pipeline artifacts: %w", err)
	}
	reviews, err := queryer.QueryContext(ctx, `SELECT request_id,stage,artifact_version,artifact_digest,artifact_content,source_session_id,decision,actor,feedback,result_revision,created_at FROM pipeline_execution_reviews WHERE pipeline_id=? ORDER BY created_at,stage,artifact_version`, pipelineID)
	if err != nil {
		return catalog.PipelineRun{}, fmt.Errorf("list pipeline execution reviews: %w", err)
	}
	for reviews.Next() {
		var review catalog.PipelineExecutionReview
		var created string
		if err := reviews.Scan(&review.RequestID, &review.Stage, &review.ArtifactVersion, &review.ArtifactDigest, &review.ArtifactContent, &review.SourceSessionID, &review.Decision, &review.Actor, &review.Feedback, &review.ResultRevision, &created); err != nil {
			_ = reviews.Close()
			return catalog.PipelineRun{}, fmt.Errorf("scan pipeline execution review: %w", err)
		}
		review.PipelineID = pipelineID
		review.CreatedAt, err = time.Parse(time.RFC3339Nano, created)
		if err != nil {
			_ = reviews.Close()
			return catalog.PipelineRun{}, fmt.Errorf("parse pipeline execution review timestamp: %w", err)
		}
		run.ExecutionReviews = append(run.ExecutionReviews, review)
	}
	if err := reviews.Err(); err != nil {
		_ = reviews.Close()
		return catalog.PipelineRun{}, fmt.Errorf("iterate pipeline execution reviews: %w", err)
	}
	if err := reviews.Close(); err != nil {
		return catalog.PipelineRun{}, fmt.Errorf("close pipeline execution reviews: %w", err)
	}
	archived, err := queryer.QueryContext(ctx, `SELECT stage,version,content,author,source_session_id,content_digest,reason,created_at FROM pipeline_archived_artifacts WHERE pipeline_id=? ORDER BY stage,version`, pipelineID)
	if err != nil {
		return catalog.PipelineRun{}, fmt.Errorf("list archived pipeline artifacts: %w", err)
	}
	defer archived.Close()
	for archived.Next() {
		var artifact catalog.PipelineArchivedArtifact
		var created string
		if err := archived.Scan(&artifact.Stage, &artifact.Version, &artifact.Content, &artifact.Author, &artifact.SourceSessionID, &artifact.ContentDigest, &artifact.Reason, &created); err != nil {
			return catalog.PipelineRun{}, fmt.Errorf("scan archived pipeline artifact: %w", err)
		}
		artifact.CreatedAt, err = time.Parse(time.RFC3339Nano, created)
		if err != nil {
			return catalog.PipelineRun{}, fmt.Errorf("parse archived artifact timestamp: %w", err)
		}
		run.ArchivedArtifacts = append(run.ArchivedArtifacts, artifact)
	}
	if err := archived.Err(); err != nil {
		return catalog.PipelineRun{}, fmt.Errorf("iterate archived pipeline artifacts: %w", err)
	}
	return run, nil
}

func (s *Store) GetPipeline(ctx context.Context, pipelineID string) (catalog.PipelineRun, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return catalog.PipelineRun{}, fmt.Errorf("begin pipeline read: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	run, err := readPipelineSnapshot(ctx, tx, pipelineID)
	if err != nil {
		return catalog.PipelineRun{}, err
	}
	if err := tx.Commit(); err != nil {
		return catalog.PipelineRun{}, fmt.Errorf("commit pipeline read: %w", err)
	}
	return run, nil
}

func (s *Store) ListPipelines(ctx context.Context, workspaceID string) ([]catalog.PipelineRun, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+pipelineColumns+" FROM pipeline_runs WHERE workspace_id = ? ORDER BY updated_at DESC, id", workspaceID)
	if err != nil {
		return nil, fmt.Errorf("list pipelines: %w", err)
	}
	defer rows.Close()
	result := make([]catalog.PipelineRun, 0)
	for rows.Next() {
		run, err := scanPipeline(rows)
		if err != nil {
			return nil, fmt.Errorf("scan pipeline: %w", err)
		}
		result = append(result, run)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate pipelines: %w", err)
	}
	return result, nil
}

func pipelineRevision(ctx context.Context, tx *sql.Tx, pipelineID, currentStage string, revision int64, now time.Time) error {
	result, err := tx.ExecContext(ctx, "UPDATE pipeline_runs SET revision = revision + 1, updated_at = ? WHERE id = ? AND kind = 'legacy' AND current_stage = ? AND revision = ?", formatCatalogTime(now), pipelineID, currentStage, revision)
	if err != nil {
		return fmt.Errorf("update pipeline revision: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read pipeline revision: %w", err)
	}
	if changed != 1 {
		return ErrPipelineConflict
	}
	return nil
}

func (s *Store) SavePipelineArtifact(ctx context.Context, pipelineID string, stage sdd.Stage, content string, revision int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin pipeline artifact: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if validPipelineDesignStage(stage) {
		if err := legacyPipelineDesignFence(ctx, tx, pipelineID); err != nil {
			return err
		}
	}
	now := time.Now().UTC()
	if err := pipelineRevision(ctx, tx, pipelineID, string(stage), revision, now); err != nil {
		if !errors.Is(err, ErrPipelineConflict) {
			return err
		}
		var kind string
		if err := tx.QueryRowContext(ctx, "SELECT kind FROM pipeline_runs WHERE id = ?", pipelineID).Scan(&kind); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("read pipeline artifact policy: %w", err)
		}
		if kind == "ai_authoring" {
			return sdd.ErrInvalidTransition
		}
		return ErrPipelineConflict
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO pipeline_artifacts (pipeline_id, stage, version, content, author, source_session_id, updated_at) VALUES (?, ?, 1, ?, 'legacy/manual', '', ?)
		ON CONFLICT(pipeline_id, stage) DO UPDATE SET version = pipeline_artifacts.version + 1, content = excluded.content, author = excluded.author, source_session_id = excluded.source_session_id, updated_at = excluded.updated_at`, pipelineID, stage, content, formatCatalogTime(now)); err != nil {
		return fmt.Errorf("save pipeline artifact: %w", err)
	}
	if err := appendPipelineEvent(ctx, tx, pipelineID, "pipeline.artifact.saved", map[string]any{"stage": stage}, now); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit pipeline artifact: %w", err)
	}
	return nil
}

// SavePipelineExecutionArtifact persists Code/Eval output with its linked
// session provenance. AI-authored Discovery/SPEC/Plan still use their stricter
// stage-specific approval and artifact contracts.
func (s *Store) SavePipelineExecutionArtifact(ctx context.Context, pipelineID string, stage sdd.Stage, content, sessionID string, revision int64) error {
	if (stage != sdd.Code && stage != sdd.Eval) || strings.TrimSpace(content) == "" || len(content) > 1024*1024 || !utf8.ValidString(content) || !safeBrainstormText(sessionID, 128) {
		return sdd.ErrInvalidTransition
	}
	role := "coder"
	if stage == sdd.Eval {
		role = "evaluator"
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin pipeline execution artifact: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	run, err := scanPipeline(tx.QueryRowContext(ctx, "SELECT "+pipelineColumns+" FROM pipeline_runs WHERE id=?", pipelineID))
	if err != nil {
		return fmt.Errorf("read pipeline execution state: %w", err)
	}
	if run.Current != stage || run.Revision != revision || run.Status[stage] != sdd.Active || (run.Kind != "legacy" && run.Kind != "ai_authoring") {
		return ErrPipelineConflict
	}
	if run.Kind == "ai_authoring" {
		if run.Status[sdd.Plan] != sdd.Completed {
			return sdd.ErrInvalidTransition
		}
		var plan string
		if err := tx.QueryRowContext(ctx, `SELECT content FROM pipeline_artifacts WHERE pipeline_id=? AND stage='plan'`, pipelineID).Scan(&plan); err != nil || strings.TrimSpace(plan) == "" {
			return sdd.ErrEvidenceRequired
		}
		if stage == sdd.Eval {
			if run.Status[sdd.Code] != sdd.Completed {
				return sdd.ErrInvalidTransition
			}
			var code string
			if err := tx.QueryRowContext(ctx, `SELECT content FROM pipeline_artifacts WHERE pipeline_id=? AND stage='code'`, pipelineID).Scan(&code); err != nil || strings.TrimSpace(code) == "" {
				return sdd.ErrEvidenceRequired
			}
		}
	}
	var linked bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pipeline_sessions WHERE pipeline_id=? AND session_id=? AND role=?)`, pipelineID, sessionID, role).Scan(&linked); err != nil {
		return fmt.Errorf("validate pipeline execution source: %w", err)
	}
	if !linked {
		return sdd.ErrInvalidTransition
	}
	now := time.Now().UTC()
	status := make(map[sdd.Stage]sdd.Status, len(run.Status))
	for key, value := range run.Status {
		status[key] = value
	}
	status[stage] = sdd.WaitingUser
	if stage == sdd.Code {
		status[sdd.Eval] = sdd.Pending
	}
	encodedStatus, err := json.Marshal(status)
	if err != nil {
		return fmt.Errorf("marshal pipeline review status: %w", err)
	}
	result, err := tx.ExecContext(ctx, `UPDATE pipeline_runs SET stage_status=?,revision=revision+1,updated_at=? WHERE id=? AND current_stage=? AND revision=? AND (kind='legacy' OR (kind='ai_authoring' AND current_stage IN ('code','eval')))`, encodedStatus, formatCatalogTime(now), pipelineID, stage, revision)
	if err != nil {
		return fmt.Errorf("advance pipeline execution revision: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read pipeline execution revision update: %w", err)
	}
	if changed != 1 {
		return ErrPipelineConflict
	}
	var previous catalog.PipelineArtifact
	previousErr := tx.QueryRowContext(ctx, `SELECT stage,version,content,author,source_session_id FROM pipeline_artifacts WHERE pipeline_id=? AND stage=?`, pipelineID, stage).
		Scan(&previous.Stage, &previous.Version, &previous.Content, &previous.Author, &previous.SourceSessionID)
	if previousErr != nil && !errors.Is(previousErr, sql.ErrNoRows) {
		return fmt.Errorf("read previous execution artifact: %w", previousErr)
	}
	if previousErr == nil {
		digest := sha256.Sum256([]byte(previous.Content))
		encodedDigest := hex.EncodeToString(digest[:])
		if _, err := tx.ExecContext(ctx, `INSERT INTO pipeline_archived_artifacts (pipeline_id,stage,version,content,author,source_session_id,content_digest,reason,created_at) VALUES (?,?,?,?,?,?,?,?,?)`,
			pipelineID, previous.Stage, previous.Version, previous.Content, previous.Author, previous.SourceSessionID, encodedDigest, "superseded", formatCatalogTime(now)); err != nil {
			return fmt.Errorf("archive superseded execution artifact: %w", err)
		}
		if err := appendPipelineEvent(ctx, tx, pipelineID, "pipeline.artifact.archived", map[string]any{"stage": previous.Stage, "version": previous.Version, "contentDigest": encodedDigest, "reason": "superseded"}, now); err != nil {
			return err
		}
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO pipeline_artifacts (pipeline_id,stage,version,content,author,source_session_id,updated_at) VALUES (?,?,1,?,'ai',?,?)
		ON CONFLICT(pipeline_id,stage) DO UPDATE SET version=pipeline_artifacts.version+1,content=excluded.content,author='ai',source_session_id=excluded.source_session_id,updated_at=excluded.updated_at`, pipelineID, stage, content, sessionID, formatCatalogTime(now))
	if err != nil {
		return fmt.Errorf("save pipeline execution artifact: %w", err)
	}
	if err := appendPipelineEvent(ctx, tx, pipelineID, "pipeline.artifact.saved", map[string]any{"stage": stage, "author": "ai", "sourceSessionId": sessionID}, now); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit pipeline execution artifact: %w", err)
	}
	return nil
}

func (s *Store) TransitionPipeline(ctx context.Context, pipelineID string, previous sdd.Stage, next sdd.Flow, action, reason string, revision int64) error {
	status, err := json.Marshal(next.Status)
	if err != nil {
		return fmt.Errorf("marshal next pipeline status: %w", err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin pipeline transition: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if validPipelineDesignStage(previous) {
		if err := legacyPipelineDesignFence(ctx, tx, pipelineID); err != nil {
			return err
		}
	}
	now := time.Now().UTC()
	result, err := tx.ExecContext(ctx, "UPDATE pipeline_runs SET current_stage = ?, stage_status = ?, revision = revision + 1, updated_at = ? WHERE id = ? AND current_stage = ? AND revision = ? AND (kind = 'legacy' OR (kind = 'ai_authoring' AND current_stage IN ('code','eval')))", next.Current, status, formatCatalogTime(now), pipelineID, previous, revision)
	if err != nil {
		return fmt.Errorf("update pipeline transition: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read pipeline transition update: %w", err)
	}
	if changed != 1 {
		var kind string
		if err := tx.QueryRowContext(ctx, "SELECT kind FROM pipeline_runs WHERE id = ?", pipelineID).Scan(&kind); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("read pipeline transition policy: %w", err)
		}
		if kind == "ai_authoring" {
			return sdd.ErrInvalidTransition
		}
		return ErrPipelineConflict
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO pipeline_transitions (id, pipeline_id, stage, action, actor, reason, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)", id.New(), pipelineID, previous, action, "local_user", reason, formatCatalogTime(now)); err != nil {
		return fmt.Errorf("record pipeline transition: %w", err)
	}
	eventType := "pipeline.stage.completed"
	if action == "skip" {
		eventType = "pipeline.stage.skipped"
	} else if action == "revise" {
		eventType = "pipeline.stage.revised"
	}
	if err := appendPipelineEvent(ctx, tx, pipelineID, eventType, map[string]any{"stage": previous, "nextStage": next.Current, "action": action}, now); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit pipeline transition: %w", err)
	}
	return nil
}

func (s *Store) DecidePipelineExecutionArtifact(ctx context.Context, in catalog.PipelineExecutionReviewRequest) (catalog.PipelineRun, error) {
	if !safeBrainstormText(in.PipelineID, 128) || !safeBrainstormText(in.RequestID, 128) ||
		(in.Stage != sdd.Code && in.Stage != sdd.Eval) || in.ArtifactVersion < 1 || in.ExpectedRevision < 1 ||
		len(in.ArtifactDigest) != 64 ||
		(in.Decision != "approve" && in.Decision != "request_revision") || len(in.Feedback) > 8192 || !utf8.ValidString(in.Feedback) ||
		(in.Decision == "approve" && in.Feedback != "") || (in.Decision == "request_revision" && strings.TrimSpace(in.Feedback) == "") {
		return catalog.PipelineRun{}, sdd.ErrInvalidTransition
	}
	if _, err := hex.DecodeString(in.ArtifactDigest); err != nil {
		return catalog.PipelineRun{}, sdd.ErrInvalidTransition
	}
	requestHash, err := in.Hash()
	if err != nil {
		return catalog.PipelineRun{}, fmt.Errorf("hash pipeline execution review: %w", err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return catalog.PipelineRun{}, fmt.Errorf("begin pipeline execution review: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var storedHash string
	var storedSnapshot []byte
	err = tx.QueryRowContext(ctx, `SELECT request_hash,result_snapshot FROM pipeline_execution_reviews WHERE pipeline_id=? AND request_id=?`, in.PipelineID, in.RequestID).Scan(&storedHash, &storedSnapshot)
	if err == nil {
		if storedHash != requestHash {
			return catalog.PipelineRun{}, ErrPipelineConflict
		}
		var replay catalog.PipelineRun
		if err := json.Unmarshal(storedSnapshot, &replay); err != nil || replay.ID != in.PipelineID {
			return catalog.PipelineRun{}, fmt.Errorf("decode pipeline execution review receipt: %w", err)
		}
		return replay, tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return catalog.PipelineRun{}, fmt.Errorf("read pipeline execution review receipt: %w", err)
	}
	run, err := readPipelineSnapshot(ctx, tx, in.PipelineID)
	if err != nil {
		return catalog.PipelineRun{}, fmt.Errorf("read pipeline execution review pipeline: %w", err)
	}
	if run.Revision != in.ExpectedRevision || run.Current != in.Stage || run.Status[in.Stage] != sdd.WaitingUser {
		return catalog.PipelineRun{}, ErrPipelineConflict
	}
	artifact := run.Artifacts[in.Stage]
	if artifact.Version != in.ArtifactVersion || artifact.Author != "ai" || artifact.Content == "" || artifact.SourceSessionID == "" {
		return catalog.PipelineRun{}, ErrPipelineConflict
	}
	currentDigest := sha256.Sum256([]byte(artifact.Content))
	if hex.EncodeToString(currentDigest[:]) != in.ArtifactDigest {
		return catalog.PipelineRun{}, ErrPipelineConflict
	}
	var linked bool
	role := "coder"
	if in.Stage == sdd.Eval {
		role = "evaluator"
		if run.Status[sdd.Code] != sdd.Completed || !pipelineArtifactHasApprovedReview(run, sdd.Code) {
			return catalog.PipelineRun{}, sdd.ErrInvalidTransition
		}
	}
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pipeline_sessions WHERE pipeline_id=? AND session_id=? AND role=?)`, in.PipelineID, artifact.SourceSessionID, role).Scan(&linked); err != nil {
		return catalog.PipelineRun{}, fmt.Errorf("validate pipeline execution review source: %w", err)
	}
	if !linked {
		return catalog.PipelineRun{}, sdd.ErrInvalidTransition
	}
	if in.Stage == sdd.Eval && in.Decision == "approve" {
		var verdict struct {
			Passed *bool `json:"passed"`
		}
		if json.Unmarshal([]byte(artifact.Content), &verdict) != nil || verdict.Passed == nil || !*verdict.Passed {
			return catalog.PipelineRun{}, sdd.ErrEvidenceRequired
		}
	}
	status := make(map[sdd.Stage]sdd.Status, len(run.Status))
	for stage, value := range run.Status {
		status[stage] = value
	}
	nextStage := run.Current
	if in.Decision == "request_revision" {
		if in.Stage == sdd.Code {
			status[sdd.Code] = sdd.Active
			status[sdd.Eval] = sdd.Pending
			nextStage = sdd.Code
		} else {
			status[sdd.Eval] = sdd.Failed
			status[sdd.Code] = sdd.Active
			nextStage = sdd.Code
		}
	} else if in.Stage == sdd.Code {
		status[sdd.Code] = sdd.Completed
		status[sdd.Eval] = sdd.Active
		nextStage = sdd.Eval
	} else {
		// QA approved: the pipeline's last step is handing the applied change to the agent as pull requests.
		status[sdd.Eval] = sdd.Completed
		status[sdd.PRs] = sdd.Active
		nextStage = sdd.PRs
	}
	encodedStatus, err := json.Marshal(status)
	if err != nil {
		return catalog.PipelineRun{}, fmt.Errorf("marshal reviewed pipeline status: %w", err)
	}
	now := time.Now().UTC()
	result, err := tx.ExecContext(ctx, `UPDATE pipeline_runs SET current_stage=?,stage_status=?,revision=revision+1,updated_at=? WHERE id=? AND current_stage=? AND revision=?`, nextStage, encodedStatus, formatCatalogTime(now), in.PipelineID, in.Stage, in.ExpectedRevision)
	if err != nil {
		return catalog.PipelineRun{}, fmt.Errorf("update reviewed pipeline: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return catalog.PipelineRun{}, fmt.Errorf("read reviewed pipeline update: %w", err)
	}
	if changed != 1 {
		return catalog.PipelineRun{}, ErrPipelineConflict
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO pipeline_transitions (id,pipeline_id,stage,action,actor,reason,created_at) VALUES (?,?,?,?,?,?,?)`, id.New(), in.PipelineID, in.Stage, in.Decision, "local_user", in.Feedback, formatCatalogTime(now)); err != nil {
		return catalog.PipelineRun{}, fmt.Errorf("record pipeline execution review decision: %w", err)
	}
	review := catalog.PipelineExecutionReview{PipelineID: in.PipelineID, RequestID: in.RequestID, Stage: in.Stage, ArtifactVersion: artifact.Version,
		ArtifactDigest: in.ArtifactDigest, ArtifactContent: artifact.Content, SourceSessionID: artifact.SourceSessionID, Decision: in.Decision,
		Actor: "local_user", Feedback: in.Feedback, ResultRevision: run.Revision + 1, CreatedAt: now}
	run.Current, run.Status, run.Revision, run.UpdatedAt = nextStage, status, run.Revision+1, now
	run.ExecutionReviews = append(run.ExecutionReviews, review)
	snapshot, err := json.Marshal(run)
	if err != nil {
		return catalog.PipelineRun{}, fmt.Errorf("marshal pipeline execution review result: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO pipeline_execution_reviews (pipeline_id,request_id,stage,artifact_version,artifact_digest,artifact_content,source_session_id,decision,actor,feedback,request_hash,result_revision,result_snapshot,created_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		in.PipelineID, in.RequestID, in.Stage, artifact.Version, in.ArtifactDigest, artifact.Content, artifact.SourceSessionID, in.Decision, "local_user", in.Feedback, requestHash, run.Revision, snapshot, formatCatalogTime(now)); err != nil {
		return catalog.PipelineRun{}, fmt.Errorf("persist pipeline execution review: %w", err)
	}
	if err := appendPipelineEvent(ctx, tx, in.PipelineID, "pipeline.execution.reviewed", map[string]any{"stage": in.Stage, "artifactVersion": artifact.Version, "artifactDigest": in.ArtifactDigest, "decision": in.Decision, "actor": "local_user", "feedback": in.Feedback, "nextStage": nextStage}, now); err != nil {
		return catalog.PipelineRun{}, err
	}
	if err := tx.Commit(); err != nil {
		return catalog.PipelineRun{}, fmt.Errorf("commit pipeline execution review: %w", err)
	}
	return run, nil
}

// ReopenPipelineCodeReview makes a pre-review Code artifact reachable from the
// current UI again. It never approves legacy output; the user must decide on
// the exact existing artifact before a new Eval can run.
func (s *Store) ReopenPipelineCodeReview(ctx context.Context, in catalog.PipelineExecutionReviewRecoveryRequest) (catalog.PipelineRun, error) {
	if !safeBrainstormText(in.PipelineID, 128) || in.ArtifactVersion < 1 || in.ExpectedRevision < 1 || len(in.ArtifactDigest) != 64 {
		return catalog.PipelineRun{}, sdd.ErrInvalidTransition
	}
	if _, err := hex.DecodeString(in.ArtifactDigest); err != nil {
		return catalog.PipelineRun{}, sdd.ErrInvalidTransition
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return catalog.PipelineRun{}, fmt.Errorf("begin Code review recovery: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	run, err := readPipelineSnapshot(ctx, tx, in.PipelineID)
	if err != nil {
		return catalog.PipelineRun{}, fmt.Errorf("read Code review recovery pipeline: %w", err)
	}
	artifact := run.Artifacts[sdd.Code]
	currentDigest := sha256.Sum256([]byte(artifact.Content))
	if artifact.Version != in.ArtifactVersion || artifact.Author != "ai" || artifact.Content == "" || artifact.SourceSessionID == "" || hex.EncodeToString(currentDigest[:]) != in.ArtifactDigest {
		return catalog.PipelineRun{}, sdd.ErrInvalidTransition
	}
	if run.Current == sdd.Code && run.Status[sdd.Code] == sdd.WaitingUser {
		return run, tx.Commit()
	}
	if run.Revision != in.ExpectedRevision || (run.Current != sdd.Eval && run.Current != "") || run.Status[sdd.Code] != sdd.Completed || pipelineArtifactHasApprovedReview(run, sdd.Code) {
		return catalog.PipelineRun{}, sdd.ErrInvalidTransition
	}
	for _, review := range run.ExecutionReviews {
		if review.Stage == sdd.Code && review.ArtifactVersion == artifact.Version && review.ArtifactDigest == in.ArtifactDigest {
			return catalog.PipelineRun{}, sdd.ErrInvalidTransition
		}
	}
	var linked bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pipeline_sessions WHERE pipeline_id=? AND session_id=? AND role='coder')`, in.PipelineID, artifact.SourceSessionID).Scan(&linked); err != nil {
		return catalog.PipelineRun{}, fmt.Errorf("validate legacy Coder source: %w", err)
	}
	if !linked {
		return catalog.PipelineRun{}, sdd.ErrInvalidTransition
	}
	status := make(map[sdd.Stage]sdd.Status, len(run.Status))
	for stage, value := range run.Status {
		status[stage] = value
	}
	status[sdd.Code], status[sdd.Eval] = sdd.WaitingUser, sdd.Pending
	encodedStatus, err := json.Marshal(status)
	if err != nil {
		return catalog.PipelineRun{}, fmt.Errorf("marshal recovered review status: %w", err)
	}
	now := time.Now().UTC()
	result, err := tx.ExecContext(ctx, `UPDATE pipeline_runs SET current_stage='code',stage_status=?,revision=revision+1,updated_at=? WHERE id=? AND current_stage=? AND revision=?`, encodedStatus, formatCatalogTime(now), in.PipelineID, run.Current, in.ExpectedRevision)
	if err != nil {
		return catalog.PipelineRun{}, fmt.Errorf("reopen Code review: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return catalog.PipelineRun{}, fmt.Errorf("read Code review recovery update: %w", err)
	}
	if changed != 1 {
		return catalog.PipelineRun{}, ErrPipelineConflict
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO pipeline_transitions (id,pipeline_id,stage,action,actor,reason,created_at) VALUES (?,?,?,?,?,?,?)`, id.New(), in.PipelineID, sdd.Code, "review_reopened", "local_user", "Legacy Code requires explicit review", formatCatalogTime(now)); err != nil {
		return catalog.PipelineRun{}, fmt.Errorf("record Code review recovery: %w", err)
	}
	if err := appendPipelineEvent(ctx, tx, in.PipelineID, "pipeline.execution.review_reopened", map[string]any{"stage": sdd.Code, "artifactVersion": artifact.Version, "artifactDigest": in.ArtifactDigest, "sourceSessionId": artifact.SourceSessionID, "actor": "local_user"}, now); err != nil {
		return catalog.PipelineRun{}, err
	}
	run.Current, run.Status, run.Revision, run.UpdatedAt = sdd.Code, status, run.Revision+1, now
	if err := tx.Commit(); err != nil {
		return catalog.PipelineRun{}, fmt.Errorf("commit Code review recovery: %w", err)
	}
	return run, nil
}

func pipelineArtifactHasApprovedReview(run catalog.PipelineRun, stage sdd.Stage) bool {
	artifact := run.Artifacts[stage]
	digest := sha256.Sum256([]byte(artifact.Content))
	for _, review := range run.ExecutionReviews {
		if review.Stage == stage && review.ArtifactVersion == artifact.Version && review.ArtifactDigest == hex.EncodeToString(digest[:]) && review.Decision == "approve" {
			return true
		}
	}
	return false
}

func (s *Store) GetPipelineStageSkipReason(ctx context.Context, pipelineID string, stage sdd.Stage) (string, error) {
	var reason string
	err := s.db.QueryRowContext(ctx, `SELECT reason FROM pipeline_transitions WHERE pipeline_id=? AND stage=? AND action='skip' ORDER BY created_at DESC,id DESC LIMIT 1`, pipelineID, stage).Scan(&reason)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read pipeline stage bypass reason: %w", err)
	}
	return reason, nil
}

func (s *Store) GetPipelineExecutionReviewResult(ctx context.Context, pipelineID, requestID, requestHash string) (catalog.PipelineRun, bool, error) {
	var storedHash string
	var snapshot []byte
	err := s.db.QueryRowContext(ctx, `SELECT request_hash,result_snapshot FROM pipeline_execution_reviews WHERE pipeline_id=? AND request_id=?`, pipelineID, requestID).Scan(&storedHash, &snapshot)
	if errors.Is(err, sql.ErrNoRows) {
		return catalog.PipelineRun{}, false, nil
	}
	if err != nil {
		return catalog.PipelineRun{}, false, fmt.Errorf("read pipeline execution review receipt: %w", err)
	}
	if storedHash != requestHash {
		return catalog.PipelineRun{}, true, ErrPipelineConflict
	}
	var run catalog.PipelineRun
	if err := json.Unmarshal(snapshot, &run); err != nil {
		return catalog.PipelineRun{}, true, fmt.Errorf("decode pipeline execution review receipt: %w", err)
	}
	if run.ID != pipelineID {
		return catalog.PipelineRun{}, true, ErrPipelineConflict
	}
	return run, true, nil
}
