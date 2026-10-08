package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/persioflexa/harflex/internal/catalog"
	"github.com/persioflexa/harflex/internal/id"
	"github.com/persioflexa/harflex/internal/sdd"
)

const brainstormAttemptTimeout = 90 * time.Second
const brainstormActiveLimit = 10 * time.Minute

var brainstormRequestID = regexp.MustCompile(`^[A-Za-z0-9_-]{16,64}$`)
var brainstormCredentialFingerprint = regexp.MustCompile(`^[a-f0-9]{64}$`)

var ErrHistoricalReceiptUnavailable = errors.New("historical_receipt_unavailable")

// These APIs are internal storage primitives. The application must prepare the
// catalog selection and revalidate it before network execution; no provider is
// reachable from this package.
type brainstormSelectionSnapshot struct {
	catalog.ModelSelection
	CredentialIdentity string `json:"credentialIdentity"`
}

func selectionSnapshot(selection catalog.ModelSelection) brainstormSelectionSnapshot {
	return brainstormSelectionSnapshot{selection, selection.CredentialIdentity}
}

func decodeBrainstormSelection(data []byte) (catalog.ModelSelection, error) {
	var snapshot brainstormSelectionSnapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return catalog.ModelSelection{}, err
	}
	snapshot.ModelSelection.CredentialIdentity = snapshot.CredentialIdentity
	return snapshot.ModelSelection, nil
}

func brainstormHash(value any) string {
	data, _ := json.Marshal(value)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func safeBrainstormText(value string, limit int) bool {
	return strings.TrimSpace(value) != "" && len(value) <= limit && utf8.ValidString(value) && strings.IndexFunc(value, unicode.IsControl) < 0
}

func validBrainstormSelection(choice catalog.ModelSelection) bool {
	if choice.ConfirmUnverifiedManual || choice.MaxOutputTokens <= 0 || choice.MaxOutputTokens > 32768 || choice.ContextLength < 0 || choice.CheckedAt.IsZero() || choice.SessionID != "" {
		return false
	}
	if choice.Source == "codex_app_server" {
		return choice.BackendID == "codex" && choice.Status == "listed" && choice.Destination == "" && choice.CredentialIdentity == "" &&
			!choice.ConfirmUnfiltered && !choice.ConfirmJITLoad && safeBrainstormText(choice.BackendID, 128) && safeBrainstormText(choice.ModelID, 512) &&
			safeBrainstormText(choice.CatalogRevision, 128) && safeBrainstormText(choice.Source, 128) && safeBrainstormText(choice.LocalRevision, 128) &&
			safeBrainstormText(choice.ExecutablePath, 4096) && filepath.IsAbs(choice.ExecutablePath) && safeBrainstormText(choice.ExecutableVersion, 128) &&
			safeBrainstormText(choice.WorkspacePath, 4096) && filepath.IsAbs(choice.WorkspacePath) && choice.ReasoningEffort == strings.TrimSpace(choice.ReasoningEffort) &&
			len(choice.ReasoningEffort) <= 64 && utf8.ValidString(choice.ReasoningEffort) && strings.IndexFunc(choice.ReasoningEffort, unicode.IsControl) < 0 &&
			!strings.ContainsAny(choice.ReasoningEffort, " =") && !strings.HasPrefix(choice.ReasoningEffort, "-") &&
			(choice.MaxAssistantOutputBytes == 2*1024 || choice.MaxAssistantOutputBytes == 64*1024)
	}
	switch choice.Source {
	case "openai_models", "openrouter_account", "ollama_tags", "lm_studio_native":
		if choice.Status != "listed" || choice.ConfirmUnfiltered {
			return false
		}
	case "openrouter_general_unfiltered":
		if choice.Status != "listed_unfiltered" || !choice.ConfirmUnfiltered {
			return false
		}
	default:
		return false
	}
	if choice.ConfirmJITLoad && choice.Source != "lm_studio_native" {
		return false
	}
	u, err := url.Parse(choice.Destination)
	return safeBrainstormText(choice.BackendID, 128) && safeBrainstormText(choice.ModelID, 512) && safeBrainstormText(choice.CatalogRevision, 128) &&
		safeBrainstormText(choice.Source, 128) && safeBrainstormText(choice.Destination, 512) && brainstormCredentialFingerprint.MatchString(choice.CredentialIdentity) &&
		catalog.ValidAPIReasoningEffort(choice) && choice.LocalRevision == "" && choice.ExecutablePath == "" && choice.ExecutableVersion == "" && choice.WorkspacePath == "" && choice.MaxAssistantOutputBytes == 0 &&
		err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" && u.User == nil && u.RawQuery == "" && u.Fragment == ""
}

func (s *Store) beginBrainstormTx(ctx context.Context) (*sql.Tx, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	// Acquire the SQLite writer before reading any idempotency/CAS snapshot.
	// This also serializes separate Store connections without BUSY_SNAPSHOT.
	if _, err := tx.ExecContext(ctx, `UPDATE pipeline_brainstorm_runs SET revision = revision WHERE 0`); err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	return tx, nil
}

const brainstormRunColumns = `id,pipeline_id,discovery_version,start_request_id,start_payload_hash,discovery_content,discovery_hash,selection_snapshot,state,revision,question_count,current_question_id,synthesis_version,attempt_count,input_budget_remaining,output_budget_remaining,active_duration,created_at,updated_at,start_client_intent_hash`

func scanBrainstormRun(row rowScanner) (catalog.BrainstormRun, error) {
	var run catalog.BrainstormRun
	var selection []byte
	var created, updated string
	if err := row.Scan(&run.ID, &run.PipelineID, &run.DiscoveryVersion, &run.StartRequestID, &run.StartPayloadHash, &run.DiscoveryContent, &run.DiscoveryHash, &selection, &run.State, &run.Revision, &run.QuestionCount, &run.CurrentQuestionID, &run.SynthesisVersion, &run.AttemptCount, &run.InputBudgetRemaining, &run.OutputBudgetRemaining, &run.ActiveDuration, &created, &updated, &run.StartClientIntentHash); err != nil {
		return run, err
	}
	var err error
	if run.Selection, err = decodeBrainstormSelection(selection); err != nil {
		return run, err
	}
	if run.CreatedAt, err = time.Parse(time.RFC3339Nano, created); err != nil {
		return run, err
	}
	if run.UpdatedAt, err = time.Parse(time.RFC3339Nano, updated); err != nil {
		return run, err
	}
	return run, nil
}

const brainstormAttemptColumns = `id,run_id,discovery_version,synthesis_version,request_id,kind,payload_hash,status,session_id,error_code,selection_snapshot,reserved_input_tokens,reserved_output_tokens,actual_usage,result_hash,created_at,updated_at`

func scanBrainstormAttempt(row rowScanner) (catalog.BrainstormAttempt, error) {
	var attempt catalog.BrainstormAttempt
	var selection, usage []byte
	var created, updated string
	if err := row.Scan(&attempt.ID, &attempt.RunID, &attempt.DiscoveryVersion, &attempt.SynthesisVersion, &attempt.RequestID, &attempt.Kind, &attempt.PayloadHash, &attempt.Status, &attempt.SessionID, &attempt.ErrorCode, &selection, &attempt.ReservedInputTokens, &attempt.ReservedOutputTokens, &usage, &attempt.ResultHash, &created, &updated); err != nil {
		return attempt, err
	}
	var err error
	if attempt.Selection, err = decodeBrainstormSelection(selection); err != nil {
		return attempt, err
	}
	if len(usage) > 0 {
		if err := json.Unmarshal(usage, &attempt.Usage); err != nil {
			return attempt, err
		}
	}
	if attempt.CreatedAt, err = time.Parse(time.RFC3339Nano, created); err != nil {
		return attempt, err
	}
	if attempt.UpdatedAt, err = time.Parse(time.RFC3339Nano, updated); err != nil {
		return attempt, err
	}
	return attempt, nil
}

func loadBrainstorm(ctx context.Context, tx *sql.Tx, runID string) (catalog.BrainstormRun, error) {
	return scanBrainstormRun(tx.QueryRowContext(ctx, `SELECT `+brainstormRunColumns+` FROM pipeline_brainstorm_runs WHERE id=?`, runID))
}

func (s *Store) GetBrainstorming(ctx context.Context, runID string) (catalog.BrainstormRun, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return catalog.BrainstormRun{}, err
	}
	defer tx.Rollback()
	run, err := loadBrainstormSnapshot(ctx, tx, runID)
	if err != nil {
		return run, err
	}
	return run, tx.Commit()
}

func loadBrainstormSnapshot(ctx context.Context, tx *sql.Tx, runID string) (catalog.BrainstormRun, error) {
	run, err := loadBrainstorm(ctx, tx, runID)
	if err != nil {
		return run, err
	}
	if err := tx.QueryRowContext(ctx, `SELECT revision FROM pipeline_runs WHERE id=?`, run.PipelineID).Scan(&run.PipelineRevision); err != nil {
		return run, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT `+brainstormAttemptColumns+` FROM pipeline_brainstorm_attempts WHERE run_id=? ORDER BY created_at,id`, runID)
	if err != nil {
		return run, err
	}
	for rows.Next() {
		attempt, err := scanBrainstormAttempt(rows)
		if err != nil {
			rows.Close()
			return run, err
		}
		run.Attempts = append(run.Attempts, attempt)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return run, err
	}
	if err := rows.Close(); err != nil {
		return run, err
	}
	rows, err = tx.QueryContext(ctx, `SELECT number,question_id,question,answer,source_session_id,status,created_at,updated_at FROM pipeline_brainstorm_turns WHERE run_id=? ORDER BY number`, runID)
	if err != nil {
		return run, err
	}
	for rows.Next() {
		var turn catalog.BrainstormTurn
		var created, updated string
		if err := rows.Scan(&turn.Number, &turn.QuestionID, &turn.Question, &turn.Answer, &turn.SourceSessionID, &turn.Status, &created, &updated); err != nil {
			rows.Close()
			return run, err
		}
		if turn.CreatedAt, err = time.Parse(time.RFC3339Nano, created); err != nil {
			rows.Close()
			return run, err
		}
		if turn.UpdatedAt, err = time.Parse(time.RFC3339Nano, updated); err != nil {
			rows.Close()
			return run, err
		}
		run.Turns = append(run.Turns, turn)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return run, err
	}
	if err := rows.Close(); err != nil {
		return run, err
	}
	rows, err = tx.QueryContext(ctx, `SELECT version,discovery_version,content,source_session_id,status,created_at FROM pipeline_brainstorm_syntheses WHERE run_id=? ORDER BY version`, runID)
	if err != nil {
		return run, err
	}
	for rows.Next() {
		var synthesis catalog.BrainstormSynthesis
		var content []byte
		var created string
		if err := rows.Scan(&synthesis.Version, &synthesis.DiscoveryVersion, &content, &synthesis.SourceSessionID, &synthesis.Status, &created); err != nil {
			rows.Close()
			return run, err
		}
		if err := json.Unmarshal(content, &synthesis.Content); err != nil {
			rows.Close()
			return run, err
		}
		if synthesis.CreatedAt, err = time.Parse(time.RFC3339Nano, created); err != nil {
			rows.Close()
			return run, err
		}
		run.Syntheses = append(run.Syntheses, synthesis)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return run, err
	}
	if err := rows.Close(); err != nil {
		return run, err
	}
	run.HumanActions, err = loadBrainstormHumanActions(ctx, tx, run.ID)
	if err != nil {
		return run, err
	}
	return run, nil
}

func (s *Store) GetBrainstormingByStartRequest(ctx context.Context, pipelineID, requestID string) (catalog.BrainstormRun, error) {
	var runID string
	if err := s.db.QueryRowContext(ctx, `SELECT id FROM pipeline_brainstorm_runs WHERE pipeline_id=? AND start_request_id=?`, pipelineID, requestID).Scan(&runID); err != nil {
		return catalog.BrainstormRun{}, err
	}
	return s.GetBrainstorming(ctx, runID)
}

func (s *Store) GetBrainstormingByPipeline(ctx context.Context, pipelineID string, version int64) (catalog.BrainstormRun, error) {
	var runID string
	if err := s.db.QueryRowContext(ctx, `SELECT id FROM pipeline_brainstorm_runs WHERE pipeline_id=? AND discovery_version=?`, pipelineID, version).Scan(&runID); err != nil {
		return catalog.BrainstormRun{}, err
	}
	return s.GetBrainstorming(ctx, runID)
}

const (
	defaultBrainstormHistoryLimit = 50
	maxBrainstormHistoryLimit     = 100
)

// ListBrainstormingByPipeline returns a bounded, newest-version-first index of
// durable rounds. Each item is then read through GetBrainstorming's coherent
// run/pipeline/artifact snapshot before it is exposed to the application.
func (s *Store) ListBrainstormingByPipeline(ctx context.Context, pipelineID string, limit int) ([]catalog.BrainstormRun, error) {
	if strings.TrimSpace(pipelineID) == "" || limit < 0 || limit > maxBrainstormHistoryLimit {
		return nil, errors.New("invalid brainstorm history query")
	}
	if limit == 0 {
		limit = defaultBrainstormHistoryLimit
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, fmt.Errorf("begin brainstorm history index: %w", err)
	}
	rows, err := tx.QueryContext(ctx, `SELECT id FROM pipeline_brainstorm_runs WHERE pipeline_id=? ORDER BY discovery_version DESC,id DESC LIMIT ?`, pipelineID, limit)
	if err != nil {
		_ = tx.Rollback()
		return nil, fmt.Errorf("list brainstorm history index: %w", err)
	}
	ids := make([]string, 0, limit)
	for rows.Next() {
		var runID string
		if err := rows.Scan(&runID); err != nil {
			_ = rows.Close()
			_ = tx.Rollback()
			return nil, fmt.Errorf("scan brainstorm history ID: %w", err)
		}
		ids = append(ids, runID)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		_ = tx.Rollback()
		return nil, fmt.Errorf("iterate brainstorm history index: %w", err)
	}
	if err := rows.Close(); err != nil {
		_ = tx.Rollback()
		return nil, fmt.Errorf("close brainstorm history index: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit brainstorm history index: %w", err)
	}
	items := make([]catalog.BrainstormRun, 0, len(ids))
	for _, runID := range ids {
		item, err := s.GetBrainstorming(ctx, runID)
		if err != nil {
			return nil, fmt.Errorf("read brainstorm history item: %w", err)
		}
		items = append(items, item)
	}
	return items, nil
}

func (s *Store) GetBrainstormCommand(ctx context.Context, runID, requestID string) (catalog.BrainstormCommandReceipt, error) {
	var receipt catalog.BrainstormCommandReceipt
	var snapshotSize int
	err := s.db.QueryRowContext(ctx, `SELECT kind,client_intent_hash,result_id,result_run_revision,result_pipeline_revision,actor,COALESCE(length(result_snapshot),0) FROM pipeline_brainstorm_requests WHERE run_id=? AND request_id=?`, runID, requestID).Scan(&receipt.Action, &receipt.ClientIntentHash, &receipt.AttemptID, &receipt.ResultRunRevision, &receipt.ResultPipelineRevision, &receipt.Actor, &snapshotSize)
	if err == nil && (receipt.ResultRunRevision < 1 || receipt.ResultPipelineRevision < 1 || snapshotSize == 0) {
		return receipt, ErrHistoricalReceiptUnavailable
	}
	return receipt, err
}

func brainstormPipelineCAS(ctx context.Context, tx *sql.Tx, pipelineID string, revision, version int64, now time.Time) error {
	if err := legacyPipelineDesignFence(ctx, tx, pipelineID); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE pipeline_runs SET revision=revision+1,updated_at=? WHERE id=? AND revision=? AND kind='ai_authoring' AND current_stage='discovery' AND discovery_frozen_version=0 AND EXISTS (SELECT 1 FROM pipeline_artifacts WHERE pipeline_id=? AND stage='discovery' AND version=? AND author='user')`, formatCatalogTime(now), pipelineID, revision, pipelineID, version)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed != 1 {
		return ErrPipelineConflict
	}
	return nil
}

func brainstormPipelineSettlementCAS(ctx context.Context, tx *sql.Tx, pipelineID string, revision, version int64, now time.Time) error {
	owned, err := legacyPipelineDesignOwned(ctx, tx, pipelineID)
	if err != nil || owned {
		return err
	}
	return brainstormPipelineCAS(ctx, tx, pipelineID, revision, version, now)
}

func checkBrainstormRun(run catalog.BrainstormRun, in catalog.BrainstormRequest) error {
	if run.Revision != in.RunRevision || run.DiscoveryVersion != in.DiscoveryVersion || run.State == "invalidated" || run.State == "approved" {
		return ErrPipelineConflict
	}
	return nil
}

func brainstormEvent(ctx context.Context, tx *sql.Tx, run catalog.BrainstormRun, kind string, now time.Time) error {
	// Events carry bounded references, never content or provider errors.
	return appendPipelineEvent(ctx, tx, run.PipelineID, "pipeline.brainstorm."+kind, map[string]any{"runId": run.ID, "discoveryVersion": run.DiscoveryVersion, "revision": run.Revision, "state": run.State}, now)
}

func (s *Store) StartBrainstorming(ctx context.Context, in catalog.StartBrainstormingRequest) (catalog.BrainstormRun, error) {
	if !brainstormRequestID.MatchString(in.RequestID) || !validBrainstormSelection(in.Selection) || in.DiscoveryVersion < 1 || in.PipelineRevision < 1 || (in.ClientIntentHash != "" && !brainstormCredentialFingerprint.MatchString(in.ClientIntentHash)) {
		return catalog.BrainstormRun{}, sdd.ErrInvalidTransition
	}
	hash := brainstormHash(struct {
		Request  catalog.StartBrainstormingRequest
		Identity string
	}{in, in.Selection.CredentialIdentity})
	tx, err := s.beginBrainstormTx(ctx)
	if err != nil {
		return catalog.BrainstormRun{}, err
	}
	defer tx.Rollback()
	existing, err := scanBrainstormRun(tx.QueryRowContext(ctx, `SELECT `+brainstormRunColumns+` FROM pipeline_brainstorm_runs WHERE pipeline_id=? AND (start_request_id=? OR discovery_version=?)`, in.PipelineID, in.RequestID, in.DiscoveryVersion))
	if err == nil {
		if existing.StartRequestID != in.RequestID || existing.StartPayloadHash != hash {
			return catalog.BrainstormRun{}, ErrPipelineConflict
		}
		return existing, tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return catalog.BrainstormRun{}, err
	}
	now := time.Now().UTC()
	if err := brainstormPipelineCAS(ctx, tx, in.PipelineID, in.PipelineRevision, in.DiscoveryVersion, now); err != nil {
		return catalog.BrainstormRun{}, err
	}
	var content string
	if err := tx.QueryRowContext(ctx, `SELECT content FROM pipeline_artifacts WHERE pipeline_id=? AND stage='discovery' AND version=?`, in.PipelineID, in.DiscoveryVersion).Scan(&content); err != nil {
		return catalog.BrainstormRun{}, err
	}
	selection, _ := json.Marshal(selectionSnapshot(in.Selection))
	runID := id.New()
	_, err = tx.ExecContext(ctx, `INSERT INTO pipeline_brainstorm_runs (id,pipeline_id,discovery_version,start_request_id,start_payload_hash,discovery_content,discovery_hash,selection_snapshot,state,revision,created_at,updated_at,start_client_intent_hash) VALUES (?,?,?,?,?,?,?,?,'ready',1,?,?,?)`, runID, in.PipelineID, in.DiscoveryVersion, in.RequestID, hash, content, brainstormHash(content), selection, formatCatalogTime(now), formatCatalogTime(now), in.ClientIntentHash)
	if err != nil {
		return catalog.BrainstormRun{}, err
	}
	run, err := loadBrainstorm(ctx, tx, runID)
	if err != nil {
		return run, err
	}
	if err := brainstormEvent(ctx, tx, run, "started", now); err != nil {
		return run, err
	}
	return run, tx.Commit()
}

func brainstormRequestResult(ctx context.Context, tx *sql.Tx, in catalog.BrainstormRequest, kind, hash string) (string, bool, error) {
	var storedKind, storedHash, resultID string
	var runRevision, pipelineRevision int64
	var snapshotSize int
	err := tx.QueryRowContext(ctx, `SELECT kind,payload_hash,result_id,result_run_revision,result_pipeline_revision,COALESCE(length(result_snapshot),0) FROM pipeline_brainstorm_requests WHERE run_id=? AND request_id=?`, in.RunID, in.RequestID).Scan(&storedKind, &storedHash, &resultID, &runRevision, &pipelineRevision, &snapshotSize)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if storedKind != kind || storedHash != hash {
		return "", false, ErrPipelineConflict
	}
	if runRevision < 1 || pipelineRevision < 1 || snapshotSize == 0 {
		return "", false, ErrHistoricalReceiptUnavailable
	}
	return resultID, true, nil
}

func insertBrainstormRequest(ctx context.Context, tx *sql.Tx, in catalog.BrainstormRequest, kind, hash, resultID, intentHash string, now time.Time) error {
	run, err := loadBrainstormSnapshot(ctx, tx, in.RunID)
	if err != nil {
		return err
	}
	return insertBrainstormSnapshotRequest(ctx, tx, in, kind, hash, resultID, intentHash, now, run)
}

func insertBrainstormSnapshotRequest(ctx context.Context, tx *sql.Tx, in catalog.BrainstormRequest, kind, hash, resultID, intentHash string, now time.Time, run catalog.BrainstormRun) error {
	data, err := encodeHumanSnapshot(run)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO pipeline_brainstorm_requests (run_id,request_id,kind,payload_hash,result_id,client_intent_hash,created_at,result_run_revision,result_pipeline_revision,actor,result_snapshot) VALUES (?,?,?,?,?,?,?,?,?,'local_user',?)`, in.RunID, in.RequestID, kind, hash, resultID, intentHash, formatCatalogTime(now), run.Revision, run.PipelineRevision, data)
	return err
}

// BeginBrainstormAttempt is the legacy question storage primitive. Inference
// orchestration must use BeginBrainstormQuestion and honor its admission flag.
func (s *Store) BeginBrainstormAttempt(ctx context.Context, in catalog.BeginBrainstormAttemptRequest) (catalog.BrainstormAttempt, error) {
	attempt, _, err := s.BeginBrainstormQuestion(ctx, in)
	return attempt, err
}

// BeginBrainstormQuestion authorizes inference only for a newly committed
// admission. Duplicate requests return their durable attempt with admitted=false.
func (s *Store) BeginBrainstormQuestion(ctx context.Context, in catalog.BeginBrainstormAttemptRequest) (attempt catalog.BrainstormAttempt, admitted bool, err error) {
	if in.Kind != "question" {
		return catalog.BrainstormAttempt{}, false, sdd.ErrInvalidTransition
	}
	result, err := s.beginBrainstormAttempt(ctx, in, "question")
	return result.Attempt, result.Admitted, err
}

// QuestionsSufficient atomically skips a pending question and admits synthesis.
// admitted is true only after a newly created attempt commits successfully.
// A replay or uncertain commit must never authorize another provider call.
func (s *Store) QuestionsSufficient(ctx context.Context, in catalog.GenerateBrainstormSynthesisRequest) (attempt catalog.BrainstormAttempt, admitted bool, err error) {
	result, err := s.beginBrainstormAttempt(ctx, catalog.BeginBrainstormAttemptRequest{BrainstormRequest: in.BrainstormRequest, Kind: "synthesis", Selection: in.Selection, EstimatedInputTokens: in.EstimatedInputTokens, ClientIntentHash: in.ClientIntentHash}, "questions_sufficient")
	return result.Attempt, result.Admitted, err
}

// FinishAndGenerateSynthesis admits synthesis only from ready_for_synthesis.
func (s *Store) FinishAndGenerateSynthesis(ctx context.Context, in catalog.GenerateBrainstormSynthesisRequest) (attempt catalog.BrainstormAttempt, admitted bool, err error) {
	result, err := s.beginBrainstormAttempt(ctx, catalog.BeginBrainstormAttemptRequest{BrainstormRequest: in.BrainstormRequest, Kind: "synthesis", Selection: in.Selection, EstimatedInputTokens: in.EstimatedInputTokens, ClientIntentHash: in.ClientIntentHash}, "finish_synthesis")
	return result.Attempt, result.Admitted, err
}

type brainstormAdmission struct {
	Attempt  catalog.BrainstormAttempt
	Admitted bool
}

func (s *Store) beginBrainstormAttempt(ctx context.Context, in catalog.BeginBrainstormAttemptRequest, action string) (brainstormAdmission, error) {
	if !brainstormRequestID.MatchString(in.RequestID) || !validBrainstormSelection(in.Selection) || (in.Kind != "question" && in.Kind != "synthesis") || in.EstimatedInputTokens <= 0 || in.EstimatedInputTokens > 262144 || (in.ClientIntentHash != "" && !brainstormCredentialFingerprint.MatchString(in.ClientIntentHash)) {
		return brainstormAdmission{}, sdd.ErrInvalidTransition
	}
	output := int64(256)
	if in.Kind == "synthesis" {
		output = 1024
	}
	if in.Selection.MaxOutputTokens < int(output) {
		return brainstormAdmission{}, sdd.ErrInvalidTransition
	}
	hash := brainstormHash(struct {
		Request  catalog.BeginBrainstormAttemptRequest
		Identity string
	}{in, in.Selection.CredentialIdentity})
	tx, err := s.beginBrainstormTx(ctx)
	if err != nil {
		return brainstormAdmission{}, err
	}
	defer tx.Rollback()
	resultID, exists, err := brainstormRequestResult(ctx, tx, in.BrainstormRequest, action, hash)
	if err != nil {
		return brainstormAdmission{}, err
	}
	if exists {
		attempt, err := scanBrainstormAttempt(tx.QueryRowContext(ctx, `SELECT `+brainstormAttemptColumns+` FROM pipeline_brainstorm_attempts WHERE id=?`, resultID))
		if err != nil {
			return brainstormAdmission{}, err
		}
		return brainstormAdmission{Attempt: attempt}, tx.Commit()
	}
	run, err := loadBrainstorm(ctx, tx, in.RunID)
	if err != nil {
		return brainstormAdmission{}, err
	}
	if err := checkBrainstormRun(run, in.BrainstormRequest); err != nil {
		return brainstormAdmission{}, err
	}
	if (action == "question" && (run.State != "ready" || run.QuestionCount >= 5)) ||
		(action == "questions_sufficient" && run.State != "ready" && run.State != "waiting_answer") ||
		(action == "finish_synthesis" && run.State != "ready_for_synthesis") {
		return brainstormAdmission{}, sdd.ErrInvalidTransition
	}
	var synthesisAttempts int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM pipeline_brainstorm_attempts WHERE run_id=? AND kind='synthesis'`, run.ID).Scan(&synthesisAttempts); err != nil {
		return brainstormAdmission{}, err
	}
	if in.Kind == "synthesis" {
		var answered int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM pipeline_brainstorm_turns WHERE run_id=? AND status='answered'`, run.ID).Scan(&answered); err != nil {
			return brainstormAdmission{}, err
		}
		if answered == 0 {
			return brainstormAdmission{}, sdd.ErrInvalidTransition
		}
	}
	// UTF-8 bytes conservatively bound tokenization of stored input; application
	// estimates must additionally include the actual prompt template and framing.
	var historyBytes int64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(SUM(length(CAST(question AS BLOB))+length(CAST(answer AS BLOB))),0) FROM pipeline_brainstorm_turns WHERE run_id=?`, run.ID).Scan(&historyBytes); err != nil {
		return brainstormAdmission{}, err
	}
	input := max(in.EstimatedInputTokens, int64(len(run.DiscoveryContent))+historyBytes+512)
	if run.AttemptCount >= 8 || (in.Kind == "synthesis" && (synthesisAttempts >= 3 || run.SynthesisVersion >= 3)) || run.ActiveDuration+brainstormAttemptTimeout > brainstormActiveLimit || input > run.InputBudgetRemaining || output > run.OutputBudgetRemaining {
		return brainstormAdmission{}, sdd.ErrInvalidTransition
	}
	now := time.Now().UTC()
	if err := brainstormPipelineCAS(ctx, tx, run.PipelineID, in.PipelineRevision, in.DiscoveryVersion, now); err != nil {
		return brainstormAdmission{}, err
	}
	if action == "questions_sufficient" && run.State == "waiting_answer" {
		result, err := tx.ExecContext(ctx, `UPDATE pipeline_brainstorm_turns SET status='skipped',updated_at=? WHERE run_id=? AND question_id=? AND status='waiting_answer'`, formatCatalogTime(now), run.ID, run.CurrentQuestionID)
		if err != nil {
			return brainstormAdmission{}, err
		}
		changed, err := result.RowsAffected()
		if err != nil {
			return brainstormAdmission{}, err
		}
		if changed != 1 {
			return brainstormAdmission{}, ErrPipelineConflict
		}
	}
	attemptID := id.New()
	synthesisVersion := 0
	if in.Kind == "synthesis" {
		synthesisVersion = run.SynthesisVersion + 1
	}
	in.Selection.MaxOutputTokens = int(output)
	selection, _ := json.Marshal(selectionSnapshot(in.Selection))
	_, err = tx.ExecContext(ctx, `INSERT INTO pipeline_brainstorm_attempts (id,run_id,discovery_version,synthesis_version,request_id,kind,payload_hash,status,selection_snapshot,reserved_input_tokens,reserved_output_tokens,created_at,updated_at) VALUES (?,?,?,?,?,?,?,'running',?,?,?,?,?)`, attemptID, run.ID, run.DiscoveryVersion, synthesisVersion, in.RequestID, in.Kind, hash, selection, input, output, formatCatalogTime(now), formatCatalogTime(now))
	if err != nil {
		return brainstormAdmission{}, err
	}
	run.State = "running_" + in.Kind
	run.Revision++
	_, err = tx.ExecContext(ctx, `UPDATE pipeline_brainstorm_runs SET state=?,current_question_id='',revision=revision+1,attempt_count=attempt_count+1,input_budget_remaining=input_budget_remaining-?,output_budget_remaining=output_budget_remaining-?,updated_at=? WHERE id=?`, run.State, input, output, formatCatalogTime(now), run.ID)
	if err != nil {
		return brainstormAdmission{}, err
	}
	if err := insertBrainstormRequest(ctx, tx, in.BrainstormRequest, action, hash, attemptID, in.ClientIntentHash, now); err != nil {
		return brainstormAdmission{}, err
	}
	if err := brainstormEvent(ctx, tx, run, "attempt_started", now); err != nil {
		return brainstormAdmission{}, err
	}
	attempt, err := scanBrainstormAttempt(tx.QueryRowContext(ctx, `SELECT `+brainstormAttemptColumns+` FROM pipeline_brainstorm_attempts WHERE id=?`, attemptID))
	if err != nil {
		return brainstormAdmission{}, err
	}
	if err := tx.Commit(); err != nil {
		return brainstormAdmission{}, err
	}
	return brainstormAdmission{Attempt: attempt, Admitted: true}, nil
}

func validBrainstormQuestion(question string) bool {
	return safeBrainstormText(question, 2048) && utf8.RuneCountInString(question) <= 280 && strings.Count(question, "?") == 1 && !strings.ContainsAny(question, "\u2028\u2029")
}

func validBrainstormSynthesis(content *catalog.BrainstormSynthesisContent) bool {
	if content == nil || strings.TrimSpace(content.Scope) == "" || len(content.Decisions) == 0 || !utf8.ValidString(content.Scope) {
		return false
	}
	for _, items := range [][]string{content.Decisions, content.OpenQuestions} {
		for _, item := range items {
			if strings.TrimSpace(item) == "" || !utf8.ValidString(item) {
				return false
			}
		}
	}
	data, _ := json.Marshal(content)
	return len(data) <= 64*1024
}

func setDiscoveryStatus(ctx context.Context, tx *sql.Tx, pipelineID string, status sdd.Status) error {
	if err := legacyPipelineDesignFence(ctx, tx, pipelineID); err != nil {
		return err
	}
	var data []byte
	if err := tx.QueryRowContext(ctx, `SELECT stage_status FROM pipeline_runs WHERE id=?`, pipelineID).Scan(&data); err != nil {
		return err
	}
	var statuses map[sdd.Stage]sdd.Status
	if err := json.Unmarshal(data, &statuses); err != nil {
		return err
	}
	statuses[sdd.Discovery] = status
	data, err := json.Marshal(statuses)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE pipeline_runs SET stage_status=? WHERE id=?`, data, pipelineID)
	return err
}

// CompleteBrainstormAttempt publishes only within the reserved budget. A budget
// overrun returns ErrBrainstormBudgetExceeded after committing a paused run and
// failed attempt; the caller must not treat that error as an unrecorded attempt.
func (s *Store) CompleteBrainstormAttempt(ctx context.Context, in catalog.CompleteBrainstormAttemptRequest) (catalog.BrainstormRun, error) {
	if !safeBrainstormText(in.SessionID, 128) || (in.Usage != nil && (in.Usage.InputTokens < 0 || in.Usage.OutputTokens < 0 || (in.Usage.CostUSD != nil && (*in.Usage.CostUSD < 0 || math.IsNaN(*in.Usage.CostUSD) || math.IsInf(*in.Usage.CostUSD, 0))))) {
		return catalog.BrainstormRun{}, sdd.ErrInvalidTransition
	}
	tx, err := s.beginBrainstormTx(ctx)
	if err != nil {
		return catalog.BrainstormRun{}, err
	}
	defer tx.Rollback()
	run, err := loadBrainstorm(ctx, tx, in.RunID)
	if err != nil {
		return run, err
	}
	attempt, err := scanBrainstormAttempt(tx.QueryRowContext(ctx, `SELECT `+brainstormAttemptColumns+` FROM pipeline_brainstorm_attempts WHERE id=? AND run_id=?`, in.AttemptID, in.RunID))
	if err != nil {
		return run, err
	}
	// The durable attempt is the result publication idempotency key.
	hash := brainstormHash(struct {
		SessionID, Question string
		Synthesis           *catalog.BrainstormSynthesisContent
		Usage               *catalog.BrainstormUsage
	}{in.SessionID, in.Question, in.Synthesis, in.Usage})
	if attempt.Status == "completed" && attempt.ResultHash == hash {
		return run, tx.Commit()
	}
	if attempt.Status == "failed" && attempt.ErrorCode == "budget_overrun" && attempt.ResultHash == hash {
		if err := tx.Commit(); err != nil {
			return run, err
		}
		return run, sdd.ErrBrainstormBudgetExceeded
	}
	if err := checkBrainstormRun(run, in.BrainstormRequest); err != nil {
		return run, err
	}
	if attempt.Status != "running" || run.State != "running_"+attempt.Kind || attempt.DiscoveryVersion != run.DiscoveryVersion || (attempt.Kind == "synthesis" && attempt.SynthesisVersion != run.SynthesisVersion+1) {
		return run, ErrPipelineConflict
	}
	if attempt.SessionID != "" && attempt.SessionID != in.SessionID {
		return run, ErrPipelineConflict
	}
	now := time.Now().UTC()
	if now.Sub(attempt.CreatedAt) > brainstormAttemptTimeout {
		return run, ErrPipelineConflict
	}
	if in.Usage != nil && (in.Usage.InputTokens > attempt.ReservedInputTokens || in.Usage.OutputTokens > attempt.ReservedOutputTokens) {
		if err := brainstormPipelineSettlementCAS(ctx, tx, run.PipelineID, in.PipelineRevision, in.DiscoveryVersion, now); err != nil {
			return run, err
		}
		usage, _ := json.Marshal(in.Usage)
		_, err = tx.ExecContext(ctx, `UPDATE pipeline_brainstorm_attempts SET status='failed',error_code='budget_overrun',session_id=?,actual_usage=?,result_hash=?,updated_at=? WHERE id=?`, in.SessionID, usage, hash, formatCatalogTime(now), attempt.ID)
		if err != nil {
			return run, err
		}
		_, err = tx.ExecContext(ctx, `UPDATE pipeline_brainstorm_runs SET state='paused',revision=revision+1,active_duration=active_duration+?,input_budget_remaining=MAX(0,input_budget_remaining-?),output_budget_remaining=MAX(0,output_budget_remaining-?),updated_at=? WHERE id=?`, max(time.Duration(0), now.Sub(attempt.CreatedAt)), max(0, in.Usage.InputTokens-attempt.ReservedInputTokens), max(0, in.Usage.OutputTokens-attempt.ReservedOutputTokens), formatCatalogTime(now), run.ID)
		if err != nil {
			return run, err
		}
		run, err = loadBrainstorm(ctx, tx, run.ID)
		if err != nil {
			return run, err
		}
		if err := brainstormEvent(ctx, tx, run, "attempt_failed", now); err != nil {
			return run, err
		}
		if err := tx.Commit(); err != nil {
			return run, err
		}
		return run, sdd.ErrBrainstormBudgetExceeded
	}
	if err := brainstormPipelineCAS(ctx, tx, run.PipelineID, in.PipelineRevision, in.DiscoveryVersion, now); err != nil {
		return run, err
	}
	if (attempt.Kind == "question" && (!validBrainstormQuestion(in.Question) || in.Synthesis != nil)) || (attempt.Kind == "synthesis" && (!validBrainstormSynthesis(in.Synthesis) || in.Question != "")) {
		return run, sdd.ErrInvalidTransition
	}
	if attempt.Kind == "question" {
		run.QuestionCount++
		run.CurrentQuestionID = id.New()
		run.State = "waiting_answer"
		_, err = tx.ExecContext(ctx, `INSERT INTO pipeline_brainstorm_turns (run_id,number,question_id,question,source_session_id,status,created_at,updated_at) VALUES (?,?,?,?,?,'waiting_answer',?,?)`, run.ID, run.QuestionCount, run.CurrentQuestionID, in.Question, in.SessionID, formatCatalogTime(now), formatCatalogTime(now))
	} else {
		run.SynthesisVersion++
		run.State = "waiting_user"
		content, _ := json.Marshal(in.Synthesis)
		_, err = tx.ExecContext(ctx, `INSERT INTO pipeline_brainstorm_syntheses (run_id,version,discovery_version,content,source_session_id,status,created_at,updated_at) VALUES (?,?,?,?,?,'completed',?,?)`, run.ID, run.SynthesisVersion, run.DiscoveryVersion, content, in.SessionID, formatCatalogTime(now), formatCatalogTime(now))
		if err == nil {
			err = setDiscoveryStatus(ctx, tx, run.PipelineID, sdd.WaitingUser)
		}
	}
	if err != nil {
		return run, err
	}
	var usage any
	if in.Usage != nil {
		data, _ := json.Marshal(in.Usage)
		usage = data
	}
	// Reservations are never refunded, including when provider usage is absent.
	_, err = tx.ExecContext(ctx, `UPDATE pipeline_brainstorm_attempts SET status='completed',session_id=?,actual_usage=?,result_hash=?,updated_at=? WHERE id=? AND status='running'`, in.SessionID, usage, hash, formatCatalogTime(now), attempt.ID)
	if err != nil {
		return run, err
	}
	_, err = tx.ExecContext(ctx, `UPDATE pipeline_brainstorm_runs SET state=?,revision=revision+1,question_count=?,current_question_id=?,synthesis_version=?,active_duration=active_duration+?,updated_at=? WHERE id=?`, run.State, run.QuestionCount, run.CurrentQuestionID, run.SynthesisVersion, max(time.Duration(0), now.Sub(attempt.CreatedAt)), formatCatalogTime(now), run.ID)
	if err != nil {
		return run, err
	}
	run, err = loadBrainstorm(ctx, tx, run.ID)
	if err != nil {
		return run, err
	}
	if err := brainstormEvent(ctx, tx, run, "attempt_completed", now); err != nil {
		return run, err
	}
	return run, tx.Commit()
}

func (s *Store) AnswerBrainstormQuestion(ctx context.Context, in catalog.AnswerBrainstormRequest) (catalog.BrainstormRun, error) {
	if !brainstormRequestID.MatchString(in.RequestID) || !safeBrainstormText(in.QuestionID, 128) || len(in.Answer) > 16*1024 || strings.TrimSpace(in.Answer) == "" || !utf8.ValidString(in.Answer) || (in.ClientIntentHash != "" && !brainstormCredentialFingerprint.MatchString(in.ClientIntentHash)) {
		return catalog.BrainstormRun{}, sdd.ErrInvalidTransition
	}
	hash := brainstormHash(in)
	tx, err := s.beginBrainstormTx(ctx)
	if err != nil {
		return catalog.BrainstormRun{}, err
	}
	defer tx.Rollback()
	_, exists, err := brainstormRequestResult(ctx, tx, in.BrainstormRequest, "answer", hash)
	if err != nil {
		return catalog.BrainstormRun{}, err
	}
	run, err := loadBrainstorm(ctx, tx, in.RunID)
	if err != nil {
		return run, err
	}
	if exists {
		var snapshot []byte
		if err := tx.QueryRowContext(ctx, `SELECT result_snapshot FROM pipeline_brainstorm_requests WHERE run_id=? AND request_id=?`, in.RunID, in.RequestID).Scan(&snapshot); err != nil {
			return run, err
		}
		run, err = decodeHumanSnapshot(snapshot)
		if err != nil {
			return run, err
		}
		return run, tx.Commit()
	}
	if err := checkBrainstormRun(run, in.BrainstormRequest); err != nil {
		return run, err
	}
	if run.State != "waiting_answer" {
		return run, sdd.ErrInvalidTransition
	}
	if run.CurrentQuestionID != in.QuestionID {
		return run, ErrPipelineConflict
	}
	now := time.Now().UTC()
	if err := brainstormPipelineCAS(ctx, tx, run.PipelineID, in.PipelineRevision, in.DiscoveryVersion, now); err != nil {
		return run, err
	}
	result, err := tx.ExecContext(ctx, `UPDATE pipeline_brainstorm_turns SET answer=?,status='answered',updated_at=? WHERE run_id=? AND question_id=? AND status='waiting_answer'`, in.Answer, formatCatalogTime(now), run.ID, in.QuestionID)
	if err != nil {
		return run, err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return run, err
	}
	if changed != 1 {
		return run, ErrPipelineConflict
	}
	run.State = "ready"
	if run.QuestionCount == 5 {
		run.State = "ready_for_synthesis"
	}
	_, err = tx.ExecContext(ctx, `UPDATE pipeline_brainstorm_runs SET state=?,current_question_id='',revision=revision+1,updated_at=? WHERE id=?`, run.State, formatCatalogTime(now), run.ID)
	if err != nil {
		return run, err
	}
	if err := insertBrainstormRequest(ctx, tx, in.BrainstormRequest, "answer", hash, in.QuestionID, in.ClientIntentHash, now); err != nil {
		return run, err
	}
	run, err = loadBrainstormSnapshot(ctx, tx, run.ID)
	if err != nil {
		return run, err
	}
	if err := brainstormEvent(ctx, tx, run, "answered", now); err != nil {
		return run, err
	}
	return run, tx.Commit()
}

func (s *Store) FailBrainstormAttempt(ctx context.Context, in catalog.FailBrainstormAttemptRequest) (catalog.BrainstormRun, error) {
	switch in.ErrorCode {
	case "cancelled", "timeout", "provider_failed", "invalid_output", "budget_overrun", "output_overflow":
	default:
		return catalog.BrainstormRun{}, sdd.ErrInvalidTransition
	}
	tx, err := s.beginBrainstormTx(ctx)
	if err != nil {
		return catalog.BrainstormRun{}, err
	}
	defer tx.Rollback()
	run, err := loadBrainstorm(ctx, tx, in.RunID)
	if err != nil {
		return run, err
	}
	attempt, err := scanBrainstormAttempt(tx.QueryRowContext(ctx, `SELECT `+brainstormAttemptColumns+` FROM pipeline_brainstorm_attempts WHERE id=? AND run_id=?`, in.AttemptID, in.RunID))
	if err != nil {
		return run, err
	}
	if attempt.Status == "failed" && attempt.ErrorCode == in.ErrorCode {
		return run, tx.Commit()
	}
	if err := checkBrainstormRun(run, in.BrainstormRequest); err != nil {
		return run, err
	}
	if attempt.Status != "running" || run.State != "running_"+attempt.Kind {
		return run, ErrPipelineConflict
	}
	now := time.Now().UTC()
	if err := brainstormPipelineSettlementCAS(ctx, tx, run.PipelineID, in.PipelineRevision, in.DiscoveryVersion, now); err != nil {
		return run, err
	}
	_, err = tx.ExecContext(ctx, `UPDATE pipeline_brainstorm_attempts SET status='failed',error_code=?,updated_at=? WHERE id=?`, in.ErrorCode, formatCatalogTime(now), attempt.ID)
	if err != nil {
		return run, err
	}
	_, err = tx.ExecContext(ctx, `UPDATE pipeline_brainstorm_runs SET state='paused',revision=revision+1,active_duration=active_duration+?,updated_at=? WHERE id=?`, min(brainstormAttemptTimeout, max(time.Duration(0), now.Sub(attempt.CreatedAt))), formatCatalogTime(now), run.ID)
	if err != nil {
		return run, err
	}
	run, err = loadBrainstorm(ctx, tx, run.ID)
	if err != nil {
		return run, err
	}
	if err := brainstormEvent(ctx, tx, run, "attempt_failed", now); err != nil {
		return run, err
	}
	return run, tx.Commit()
}

// InterruptRunningBrainstormAttempts is called by startup recovery, before
// admitting new workers. Merely opening an extra DB connection is not a restart.
func (s *Store) InterruptRunningBrainstormAttempts(ctx context.Context) error {
	tx, err := s.beginBrainstormTx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT `+brainstormRunColumns+` FROM pipeline_brainstorm_runs WHERE state IN ('running_question','running_synthesis')`)
	if err != nil {
		return err
	}
	var runs []catalog.BrainstormRun
	for rows.Next() {
		run, err := scanBrainstormRun(rows)
		if err != nil {
			rows.Close()
			return err
		}
		runs = append(runs, run)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	now := time.Now().UTC()
	for _, run := range runs {
		_, err = tx.ExecContext(ctx, `UPDATE pipeline_brainstorm_attempts SET status='interrupted',error_code='interrupted',updated_at=? WHERE run_id=? AND status='running'`, formatCatalogTime(now), run.ID)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE pipeline_brainstorm_runs SET state='paused',revision=revision+1,active_duration=MIN(600000000000,active_duration+?),updated_at=? WHERE id=? AND revision=?`, brainstormAttemptTimeout, formatCatalogTime(now), run.ID, run.Revision)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE pipeline_runs SET revision=revision+1,updated_at=? WHERE id=? AND NOT EXISTS(SELECT 1 FROM pipeline_design_workspaces WHERE pipeline_id=?)`, formatCatalogTime(now), run.PipelineID, run.PipelineID)
		if err != nil {
			return err
		}
		run.State = "paused"
		run.Revision++
		if err := brainstormEvent(ctx, tx, run, "interrupted", now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func invalidateBrainstormDiscovery(ctx context.Context, tx *sql.Tx, pipelineID string, version int64, now time.Time) error {
	run, err := scanBrainstormRun(tx.QueryRowContext(ctx, `SELECT `+brainstormRunColumns+` FROM pipeline_brainstorm_runs WHERE pipeline_id=? AND discovery_version=? AND state NOT IN ('approved','invalidated')`, pipelineID, version))
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read brainstorm invalidation: %w", err)
	}
	_, err = tx.ExecContext(ctx, `UPDATE pipeline_brainstorm_attempts SET status='stale',error_code='discovery_revised',updated_at=? WHERE run_id=? AND status='running'`, formatCatalogTime(now), run.ID)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE pipeline_brainstorm_runs SET state='invalidated',revision=revision+1,updated_at=? WHERE id=?`, formatCatalogTime(now), run.ID)
	if err != nil {
		return err
	}
	if err := setDiscoveryStatus(ctx, tx, pipelineID, sdd.Active); err != nil {
		return err
	}
	run.State = "invalidated"
	run.Revision++
	return brainstormEvent(ctx, tx, run, "invalidated", now)
}
