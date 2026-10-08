package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/persioflexa/harflex/internal/catalog"
	"github.com/persioflexa/harflex/internal/sdd"
)

type brainstormHumanAction struct {
	Ref              catalog.BrainstormRequest
	Kind             string
	SynthesisVersion int
	Choice           string
	Feedback         string
	Reason           string
	AttemptID        string `json:",omitempty"`
}

func (s *Store) CancelBrainstormAttempt(ctx context.Context, in catalog.CancelBrainstormAttemptRequest) (catalog.BrainstormRun, error) {
	return s.applyBrainstormHumanAction(ctx, brainstormHumanAction{Ref: in.BrainstormRequest, Kind: "cancel", AttemptID: in.AttemptID})
}

func (s *Store) ResumePausedBrainstorm(ctx context.Context, in catalog.BrainstormRequest) (catalog.BrainstormRun, error) {
	return s.applyBrainstormHumanAction(ctx, brainstormHumanAction{Ref: in, Kind: "resume"})
}

func loadBrainstormHumanActions(ctx context.Context, tx *sql.Tx, runID string) ([]catalog.BrainstormHumanAction, error) {
	rows, err := tx.QueryContext(ctx, `SELECT h.request_id,r.kind,h.actor,h.synthesis_version,h.feedback,h.reason,h.choice,h.result_revision,h.result_pipeline_revision,h.created_at FROM pipeline_brainstorm_human_receipts h JOIN pipeline_brainstorm_requests r USING(run_id,request_id) WHERE h.run_id=? ORDER BY h.result_revision`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var actions []catalog.BrainstormHumanAction
	for rows.Next() {
		var a catalog.BrainstormHumanAction
		var created string
		if err := rows.Scan(&a.RequestID, &a.Kind, &a.Actor, &a.SynthesisVersion, &a.Feedback, &a.Reason, &a.Choice, &a.ResultRevision, &a.PipelineRevision, &created); err != nil {
			return nil, err
		}
		a.CreatedAt, err = time.Parse(time.RFC3339Nano, created)
		if err != nil {
			return nil, err
		}
		actions = append(actions, a)
	}
	return actions, rows.Err()
}

func (s *Store) ApproveBrainstormSynthesis(ctx context.Context, in catalog.ApproveBrainstormSynthesisRequest) (catalog.BrainstormRun, error) {
	return s.applyBrainstormHumanAction(ctx, brainstormHumanAction{Ref: in.BrainstormRequest, Kind: "approve", SynthesisVersion: in.SynthesisVersion})
}
func (s *Store) RequestBrainstormRevision(ctx context.Context, in catalog.RequestBrainstormRevisionRequest) (catalog.BrainstormRun, error) {
	return s.applyBrainstormHumanAction(ctx, brainstormHumanAction{Ref: in.BrainstormRequest, Kind: "revision", SynthesisVersion: in.SynthesisVersion, Choice: in.Choice, Feedback: in.Feedback})
}
func (s *Store) SkipBrainstormQuestions(ctx context.Context, in catalog.SkipBrainstormQuestionsRequest) (catalog.BrainstormRun, error) {
	return s.applyBrainstormHumanAction(ctx, brainstormHumanAction{Ref: in.BrainstormRequest, Kind: "bypass", Reason: in.Reason})
}
func (s *Store) ConfirmDiscoveryAfterSkip(ctx context.Context, in catalog.BrainstormRequest) (catalog.BrainstormRun, error) {
	return s.applyBrainstormHumanAction(ctx, brainstormHumanAction{Ref: in, Kind: "confirm"})
}

// Snapshot is the exact readback of the committed human command. It preserves
// internal selection identity without ever exposing it in application DTOs.
type brainstormHumanSnapshot struct {
	Run               catalog.BrainstormRun
	Selection         brainstormSelectionSnapshot
	AttemptSelections []brainstormSelectionSnapshot
}

func encodeHumanSnapshot(run catalog.BrainstormRun) ([]byte, error) {
	snapshot := brainstormHumanSnapshot{Run: run, Selection: selectionSnapshot(run.Selection)}
	for _, a := range run.Attempts {
		snapshot.AttemptSelections = append(snapshot.AttemptSelections, selectionSnapshot(a.Selection))
	}
	return json.Marshal(snapshot)
}
func decodeHumanSnapshot(data []byte) (catalog.BrainstormRun, error) {
	var snapshot brainstormHumanSnapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return catalog.BrainstormRun{}, err
	}
	run := snapshot.Run
	run.Selection.CredentialIdentity = snapshot.Selection.CredentialIdentity
	for i, choice := range snapshot.AttemptSelections {
		if i < len(run.Attempts) {
			run.Attempts[i].Selection.CredentialIdentity = choice.CredentialIdentity
		}
	}
	return run, nil
}

func (s *Store) applyBrainstormHumanAction(ctx context.Context, in brainstormHumanAction) (catalog.BrainstormRun, error) {
	empty := catalog.BrainstormRun{}
	ref := in.Ref
	if !brainstormRequestID.MatchString(ref.RequestID) || !safeBrainstormText(ref.RunID, 128) || ref.RunRevision < 1 || ref.PipelineRevision < 1 || ref.DiscoveryVersion < 1 {
		return empty, sdd.ErrInvalidTransition
	}
	validText := func(v string) bool { return strings.TrimSpace(v) != "" && len(v) <= 16*1024 && utf8.ValidString(v) }
	if (in.Kind == "approve" || in.Kind == "revision") && (in.SynthesisVersion < 1 || in.SynthesisVersion > 3) {
		return empty, sdd.ErrInvalidTransition
	}
	if in.Kind == "revision" && (!validText(in.Feedback) || (in.Choice != "more_questions" && in.Choice != "new_synthesis")) {
		return empty, sdd.ErrInvalidTransition
	}
	if in.Kind == "bypass" && !validText(in.Reason) {
		return empty, sdd.ErrInvalidTransition
	}
	if in.Kind == "cancel" && !safeBrainstormText(in.AttemptID, 128) {
		return empty, sdd.ErrInvalidTransition
	}
	hash := brainstormHash(in)
	tx, err := s.beginBrainstormTx(ctx)
	if err != nil {
		return empty, err
	}
	defer tx.Rollback()
	_, exists, err := brainstormRequestResult(ctx, tx, ref, in.Kind, hash)
	if err != nil {
		return empty, err
	}
	if exists {
		var data []byte
		if err := tx.QueryRowContext(ctx, `SELECT result_snapshot FROM pipeline_brainstorm_requests WHERE run_id=? AND request_id=?`, ref.RunID, ref.RequestID).Scan(&data); err != nil {
			return empty, err
		}
		run, err := decodeHumanSnapshot(data)
		if err != nil {
			return empty, err
		}
		return run, tx.Commit()
	}
	run, err := loadBrainstorm(ctx, tx, ref.RunID)
	if err != nil {
		return empty, err
	}
	if err := checkBrainstormRun(run, ref); err != nil {
		return empty, err
	}
	designOwned, err := pipelineDesignOwnsAuthoring(ctx, tx, run.PipelineID)
	if err != nil {
		return empty, err
	}
	if designOwned && in.Kind != "cancel" {
		return empty, ErrPipelineConflict
	}
	var statusData []byte
	if err := tx.QueryRowContext(ctx, `SELECT stage_status FROM pipeline_runs WHERE id=?`, run.PipelineID).Scan(&statusData); err != nil {
		return empty, err
	}
	var statuses map[sdd.Stage]sdd.Status
	if err := json.Unmarshal(statusData, &statuses); err != nil {
		return empty, err
	}
	now := time.Now().UTC()
	nextState := run.State
	var cancelled catalog.BrainstormAttempt
	switch in.Kind {
	case "cancel":
		cancelled, err = scanBrainstormAttempt(tx.QueryRowContext(ctx, `SELECT `+brainstormAttemptColumns+` FROM pipeline_brainstorm_attempts WHERE id=? AND run_id=?`, in.AttemptID, run.ID))
		if err != nil {
			return empty, err
		}
		if cancelled.Status != "running" || run.State != "running_"+cancelled.Kind || (!designOwned && statuses[sdd.Discovery] != sdd.Active) {
			return empty, ErrPipelineConflict
		}
		nextState = "paused"
	case "resume":
		if run.State != "paused" || statuses[sdd.Discovery] != sdd.Active {
			return empty, ErrPipelineConflict
		}
		var running, pending int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM pipeline_brainstorm_attempts WHERE run_id=? AND status='running'`, run.ID).Scan(&running); err != nil {
			return empty, err
		}
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM pipeline_brainstorm_turns WHERE run_id=? AND status='waiting_answer'`, run.ID).Scan(&pending); err != nil {
			return empty, err
		}
		last, err := scanBrainstormAttempt(tx.QueryRowContext(ctx, `SELECT `+brainstormAttemptColumns+` FROM pipeline_brainstorm_attempts WHERE run_id=? ORDER BY created_at DESC,id DESC LIMIT 1`, run.ID))
		if err != nil {
			return empty, err
		}
		if running != 0 || pending != 0 || (last.Status != "failed" && last.Status != "interrupted") {
			return empty, ErrPipelineConflict
		}
		nextState = "ready"
		if last.Kind == "synthesis" {
			nextState = "ready_for_synthesis"
		}
	case "approve", "revision":
		if run.State != "waiting_user" || statuses[sdd.Discovery] != sdd.WaitingUser || run.SynthesisVersion != in.SynthesisVersion {
			return empty, ErrPipelineConflict
		}
		var synthesisStatus string
		if err := tx.QueryRowContext(ctx, `SELECT status FROM pipeline_brainstorm_syntheses WHERE run_id=? AND version=? AND discovery_version=?`, run.ID, in.SynthesisVersion, ref.DiscoveryVersion).Scan(&synthesisStatus); err != nil {
			return empty, err
		}
		if synthesisStatus != "completed" {
			return empty, ErrPipelineConflict
		}
		nextState = "approved"
		if in.Kind == "revision" {
			nextState = "ready_for_synthesis"
			if in.Choice == "more_questions" {
				if run.QuestionCount >= 5 {
					return empty, sdd.ErrInvalidTransition
				}
				nextState = "ready"
			}
		}
	case "bypass":
		if (run.State != "ready" && run.State != "waiting_answer" && run.State != "paused") || statuses[sdd.Discovery] != sdd.Active {
			return empty, sdd.ErrInvalidTransition
		}
		var answers, running int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM pipeline_brainstorm_turns WHERE run_id=? AND status='answered'`, run.ID).Scan(&answers); err != nil {
			return empty, err
		}
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM pipeline_brainstorm_attempts WHERE run_id=? AND status='running'`, run.ID).Scan(&running); err != nil {
			return empty, err
		}
		if answers != 0 || running != 0 || run.SynthesisVersion != 0 {
			return empty, sdd.ErrInvalidTransition
		}
		nextState = "skipped_waiting_confirmation"
	case "confirm":
		if run.State != "skipped_waiting_confirmation" || statuses[sdd.Discovery] != sdd.WaitingUser || run.SynthesisVersion != 0 {
			return empty, sdd.ErrInvalidTransition
		}
		nextState = "approved"
	default:
		return empty, sdd.ErrInvalidTransition
	}
	if in.Kind == "cancel" {
		if err := brainstormPipelineSettlementCAS(ctx, tx, run.PipelineID, ref.PipelineRevision, ref.DiscoveryVersion, now); err != nil {
			return empty, err
		}
	} else {
		if err := brainstormPipelineCAS(ctx, tx, run.PipelineID, ref.PipelineRevision, ref.DiscoveryVersion, now); err != nil {
			return empty, err
		}
	}
	if in.Kind == "cancel" {
		if _, err := tx.ExecContext(ctx, `UPDATE pipeline_brainstorm_attempts SET status='interrupted',error_code='cancelled',updated_at=? WHERE id=? AND status='running'`, formatCatalogTime(now), cancelled.ID); err != nil {
			return empty, err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE pipeline_brainstorm_runs SET active_duration=active_duration+? WHERE id=?`, min(brainstormAttemptTimeout, max(time.Duration(0), now.Sub(cancelled.CreatedAt))), run.ID); err != nil {
			return empty, err
		}
	}
	if in.Kind == "approve" || in.Kind == "confirm" {
		next, err := sdd.ApproveDiscovery(sdd.Flow{Current: sdd.Discovery, Status: statuses})
		if err != nil {
			return empty, err
		}
		data, err := json.Marshal(next.Status)
		if err != nil {
			return empty, err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE pipeline_runs SET current_stage=?,stage_status=?,discovery_frozen_version=? WHERE id=?`, next.Current, data, ref.DiscoveryVersion, run.PipelineID); err != nil {
			return empty, err
		}
	} else if !designOwned {
		status := sdd.Active
		if in.Kind == "bypass" {
			status = sdd.WaitingUser
		}
		if err := setDiscoveryStatus(ctx, tx, run.PipelineID, status); err != nil {
			return empty, err
		}
	}
	if in.Kind == "approve" || in.Kind == "revision" {
		status := "approved"
		if in.Kind == "revision" {
			status = "rejected"
		}
		if _, err := tx.ExecContext(ctx, `UPDATE pipeline_brainstorm_syntheses SET status=?,updated_at=? WHERE run_id=? AND version=?`, status, formatCatalogTime(now), run.ID, in.SynthesisVersion); err != nil {
			return empty, err
		}
	}
	if in.Kind == "bypass" {
		if _, err := tx.ExecContext(ctx, `UPDATE pipeline_brainstorm_turns SET status='skipped',updated_at=? WHERE run_id=? AND status='waiting_answer'`, formatCatalogTime(now), run.ID); err != nil {
			return empty, err
		}
	}
	result, err := tx.ExecContext(ctx, `UPDATE pipeline_brainstorm_runs SET state=?,current_question_id='',revision=revision+1,updated_at=? WHERE id=? AND revision=?`, nextState, formatCatalogTime(now), run.ID, ref.RunRevision)
	if err != nil {
		return empty, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return empty, err
	}
	if n != 1 {
		return empty, ErrPipelineConflict
	}
	run, err = loadBrainstormSnapshot(ctx, tx, run.ID)
	if err != nil {
		return empty, err
	}
	run.HumanActions = append(run.HumanActions, catalog.BrainstormHumanAction{RequestID: ref.RequestID, Kind: in.Kind, Actor: "local_user", SynthesisVersion: in.SynthesisVersion, Feedback: in.Feedback, Reason: in.Reason, Choice: in.Choice, ResultRevision: run.Revision, PipelineRevision: run.PipelineRevision, CreatedAt: now})
	if err := insertBrainstormSnapshotRequest(ctx, tx, ref, in.Kind, hash, strconv.FormatInt(run.Revision, 10), hash, now, run); err != nil {
		return empty, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO pipeline_brainstorm_human_receipts(run_id,request_id,actor,synthesis_version,feedback,reason,choice,result_revision,result_pipeline_revision,created_at) VALUES (?,?,'local_user',?,?,?,?,?,?,?)`, run.ID, ref.RequestID, in.SynthesisVersion, in.Feedback, in.Reason, in.Choice, run.Revision, run.PipelineRevision, formatCatalogTime(now)); err != nil {
		return empty, err
	}
	if err := brainstormEvent(ctx, tx, run, in.Kind, now); err != nil {
		return empty, err
	}
	return run, tx.Commit()
}
