package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/persioflexa/harflex/internal/catalog"
	"github.com/persioflexa/harflex/internal/id"
	"github.com/persioflexa/harflex/internal/sdd"
)

// MaxPipelinePRSessions bounds how many conversations a pipeline may spend on its pull requests.
const MaxPipelinePRSessions = 8

// pipelinePRsOpen says whether the pull request stage can still be worked on. It is the current stage once QA
// is approved; a pipeline that finished before the stage existed (no status for it) can still use it.
func pipelinePRsOpen(current string, status map[string]string) bool {
	if status[string(sdd.Eval)] != string(sdd.Completed) {
		return false
	}
	if current == string(sdd.PRs) {
		return status[string(sdd.PRs)] == string(sdd.Active)
	}
	return current == "" && (status[string(sdd.PRs)] == "" || status[string(sdd.PRs)] == string(sdd.Pending))
}

func readPipelineGate(ctx context.Context, tx *sql.Tx, pipelineID string) (current string, status map[string]string, revision int64, err error) {
	var statuses string
	if err = tx.QueryRowContext(ctx, "SELECT current_stage, stage_status, revision FROM pipeline_runs WHERE id=?", pipelineID).Scan(&current, &statuses, &revision); err != nil {
		return "", nil, 0, fmt.Errorf("read pipeline for pull requests: %w", err)
	}
	if err = json.Unmarshal([]byte(statuses), &status); err != nil {
		return "", nil, 0, fmt.Errorf("decode pipeline status for pull requests: %w", err)
	}
	return current, status, revision, nil
}

// LinkPipelinePRSession records the conversation of the pull request stage. The pipeline must be in a state
// that still accepts it, which is checked in the same transaction as the insert.
func (s *Store) LinkPipelinePRSession(ctx context.Context, link catalog.PipelinePRSession) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin pull request session link: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	current, status, _, err := readPipelineGate(ctx, tx, link.PipelineID)
	if err != nil {
		return err
	}
	if !pipelinePRsOpen(current, status) {
		return ErrPipelineConflict
	}
	var linked int
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM pipeline_pr_sessions WHERE pipeline_id=?", link.PipelineID).Scan(&linked); err != nil {
		return fmt.Errorf("count pull request sessions: %w", err)
	}
	if linked >= MaxPipelinePRSessions {
		return ErrPipelineConflict
	}
	if link.ID == "" {
		link.ID = id.New()
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO pipeline_pr_sessions (id, pipeline_id, session_id, created_at) VALUES (?, ?, ?, ?)", link.ID, link.PipelineID, link.SessionID, formatCatalogTime(link.CreatedAt)); err != nil {
		return fmt.Errorf("link pull request session: %w", err)
	}
	if err := appendPipelineEvent(ctx, tx, link.PipelineID, "pipeline.session.linked", map[string]any{"sessionId": link.SessionID, "role": "publisher"}, link.CreatedAt); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit pull request session link: %w", err)
	}
	return nil
}

// GetPipelineIDForPRSession finds the pipeline whose pull request stage a conversation belongs to.
func (s *Store) GetPipelineIDForPRSession(ctx context.Context, sessionID string) (string, error) {
	var pipelineID string
	if err := s.db.QueryRowContext(ctx, "SELECT pipeline_id FROM pipeline_pr_sessions WHERE session_id=?", sessionID).Scan(&pipelineID); err != nil {
		return "", fmt.Errorf("get pipeline for pull request session: %w", err)
	}
	return pipelineID, nil
}

func (s *Store) ListPipelinePRSessions(ctx context.Context, pipelineID string) ([]catalog.PipelinePRSession, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id, pipeline_id, session_id, created_at FROM pipeline_pr_sessions WHERE pipeline_id=? ORDER BY created_at, rowid", pipelineID)
	if err != nil {
		return nil, fmt.Errorf("list pull request sessions: %w", err)
	}
	defer rows.Close()
	result := make([]catalog.PipelinePRSession, 0)
	for rows.Next() {
		var link catalog.PipelinePRSession
		var created string
		if err := rows.Scan(&link.ID, &link.PipelineID, &link.SessionID, &created); err != nil {
			return nil, fmt.Errorf("scan pull request session: %w", err)
		}
		if link.CreatedAt, err = time.Parse(time.RFC3339Nano, created); err != nil {
			return nil, fmt.Errorf("parse pull request session timestamp: %w", err)
		}
		result = append(result, link)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate pull request sessions: %w", err)
	}
	return result, nil
}

// FinishPipelinePRs ends the pull request stage and, with it, the pipeline. Completing needs the report of one of
// the stage's own conversations, which becomes the stage artifact; skipping needs nothing but the person's decision.
func (s *Store) FinishPipelinePRs(ctx context.Context, in catalog.PipelinePRsFinish) error {
	completed := in.Outcome == string(sdd.Completed)
	if in.Outcome != string(sdd.Completed) && in.Outcome != string(sdd.Skipped) {
		return sdd.ErrInvalidTransition
	}
	if completed && (!safeBrainstormText(in.SessionID, 128) || strings.TrimSpace(in.Report) == "" || len(in.Report) > 1024*1024 || !utf8.ValidString(in.Report)) {
		return sdd.ErrEvidenceRequired
	}
	if len(in.Reason) > 1000 || !utf8.ValidString(in.Reason) {
		return sdd.ErrInvalidTransition
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin pull request stage end: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	run, err := readPipelineSnapshot(ctx, tx, in.PipelineID)
	if err != nil {
		return fmt.Errorf("read pipeline before ending pull requests: %w", err)
	}
	current := string(run.Current)
	status := make(map[string]string, len(run.Status))
	for stage, value := range run.Status {
		status[string(stage)] = string(value)
	}
	if run.Revision != in.ExpectedRevision || !pipelinePRsOpen(current, status) {
		return ErrPipelineConflict
	}
	now := time.Now().UTC()
	if completed {
		var linked bool
		if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM pipeline_pr_sessions WHERE pipeline_id=? AND session_id=?)", in.PipelineID, in.SessionID).Scan(&linked); err != nil {
			return fmt.Errorf("validate pull request report source: %w", err)
		}
		if !linked {
			return sdd.ErrInvalidTransition
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO pipeline_artifacts (pipeline_id,stage,version,content,author,source_session_id,updated_at) VALUES (?,?,1,?,'ai',?,?)
			ON CONFLICT(pipeline_id,stage) DO UPDATE SET version=pipeline_artifacts.version+1,content=excluded.content,author='ai',source_session_id=excluded.source_session_id,updated_at=excluded.updated_at`,
			in.PipelineID, sdd.PRs, in.Report, in.SessionID, formatCatalogTime(now)); err != nil {
			return fmt.Errorf("save pull request report: %w", err)
		}
	}
	status[string(sdd.PRs)] = in.Outcome
	encodedStatus, err := json.Marshal(status)
	if err != nil {
		return fmt.Errorf("marshal pipeline status after pull requests: %w", err)
	}
	result, err := tx.ExecContext(ctx, `UPDATE pipeline_runs SET current_stage='',stage_status=?,revision=revision+1,updated_at=? WHERE id=? AND revision=? AND current_stage=?`,
		encodedStatus, formatCatalogTime(now), in.PipelineID, in.ExpectedRevision, current)
	if err != nil {
		return fmt.Errorf("end pull request stage: %w", err)
	}
	if changed, err := result.RowsAffected(); err != nil || changed != 1 {
		if err != nil {
			return fmt.Errorf("read pull request stage update: %w", err)
		}
		return ErrPipelineConflict
	}
	action, event := "complete", "pipeline.stage.completed"
	if !completed {
		action, event = "skip", "pipeline.stage.skipped"
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO pipeline_transitions (id,pipeline_id,stage,action,actor,reason,created_at) VALUES (?,?,?,?,?,?,?)`, id.New(), in.PipelineID, sdd.PRs, action, "local_user", strings.TrimSpace(in.Reason), formatCatalogTime(now)); err != nil {
		return fmt.Errorf("record pull request stage transition: %w", err)
	}
	if err := appendPipelineEvent(ctx, tx, in.PipelineID, event, map[string]any{"stage": sdd.PRs, "nextStage": "", "action": action}, now); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit pull request stage end: %w", err)
	}
	return nil
}
