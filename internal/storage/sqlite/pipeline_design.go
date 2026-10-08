package sqlite

import (
	"context"
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

var ErrPipelineDesignDerivationRequired = errors.New("pipeline design requires a derived pipeline after Code starts")
var ErrPipelineDesignSpecStale = errors.New("pipeline design SPEC is stale or missing")

var pipelineDesignStages = [...]sdd.Stage{sdd.Discovery, sdd.Spec, sdd.Plan}

func pipelineDesignOwnsAuthoring(ctx context.Context, tx *sql.Tx, pipelineID string) (bool, error) {
	var owned bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pipeline_design_workspaces WHERE pipeline_id=?)`, pipelineID).Scan(&owned)
	return owned, err
}

func legacyPipelineDesignOwned(ctx context.Context, tx *sql.Tx, pipelineID string) (bool, error) {
	// Acquire the common SQLite writer before checking ownership. An OpenDesign
	// racing this legacy transaction must either lose its CAS or own all writes.
	if _, err := tx.ExecContext(ctx, `UPDATE pipeline_runs SET revision=revision WHERE 0`); err != nil {
		return false, err
	}
	return pipelineDesignOwnsAuthoring(ctx, tx, pipelineID)
}

func legacyPipelineDesignFence(ctx context.Context, tx *sql.Tx, pipelineID string) error {
	owned, err := legacyPipelineDesignOwned(ctx, tx, pipelineID)
	if err != nil {
		return err
	}
	if owned {
		return ErrPipelineConflict
	}
	return nil
}

func legacyPipelineAuthoringActive(ctx context.Context, tx *sql.Tx, pipelineID string) (bool, error) {
	var active bool
	err := tx.QueryRowContext(ctx, `SELECT
		EXISTS(SELECT 1 FROM pipeline_brainstorm_runs WHERE pipeline_id=? AND state IN ('running_question','running_synthesis'))
		OR EXISTS(SELECT 1 FROM pipeline_brainstorm_attempts a JOIN pipeline_brainstorm_runs r ON r.id=a.run_id WHERE r.pipeline_id=? AND a.status='running')
		OR EXISTS(SELECT 1 FROM pipeline_authoring_stages WHERE pipeline_id=? AND state='running')
		OR EXISTS(SELECT 1 FROM pipeline_authoring_attempts WHERE pipeline_id=? AND status='running')
		OR EXISTS(SELECT 1 FROM pipeline_authoring_stops WHERE pipeline_id=? AND state='pending')`, pipelineID, pipelineID, pipelineID, pipelineID, pipelineID).Scan(&active)
	return active, err
}

func (s *Store) beginPipelineDesignTx(ctx context.Context) (*sql.Tx, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	// Serialize writers before reading CAS/idempotency snapshots, including
	// separate Store connections to the same database.
	if _, err := tx.ExecContext(ctx, `UPDATE pipeline_design_workspaces SET revision=revision WHERE 0`); err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	return tx, nil
}

func validPipelineDesignStage(stage sdd.Stage) bool {
	return stage == sdd.Discovery || stage == sdd.Spec || stage == sdd.Plan
}

func validPipelineDesignTarget(target string) bool {
	return target == "all" || validPipelineDesignStage(sdd.Stage(target))
}

func validPipelineDesignText(content string, limit int) bool {
	return strings.TrimSpace(content) != "" && len(content) <= limit && utf8.ValidString(content) && !strings.ContainsRune(content, 0)
}

func validPipelineDesignRef(ref catalog.PipelineDesignRef) bool {
	return safeBrainstormText(ref.PipelineID, 128) && safeBrainstormText(ref.RequestID, 128) && ref.PipelineRevision > 0 && ref.DesignRevision > 0
}

func pipelineDesignNeedsDerivation(run catalog.PipelineRun) bool {
	return run.Current == sdd.Code || run.Current == sdd.Eval || run.Current == sdd.PRs || run.Current == "" || run.Artifacts[sdd.Code].Version > 0 || run.Artifacts[sdd.Eval].Version > 0 ||
		(run.Status[sdd.Code] != "" && run.Status[sdd.Code] != sdd.Pending)
}

func emptyPipelineDesign() catalog.PipelineDesignWorkspace {
	workspace := catalog.PipelineDesignWorkspace{
		Documents: make(map[sdd.Stage]catalog.PipelineDesignDocument, 3), Versions: make(map[sdd.Stage][]catalog.PipelineDesignDocumentVersion, 3),
		Messages: []catalog.PipelineDesignMessage{}, Attempts: []catalog.PipelineDesignAttempt{},
	}
	for _, stage := range pipelineDesignStages {
		workspace.Documents[stage] = catalog.PipelineDesignDocument{Stage: stage, ContentDigest: catalog.PipelineDesignContentDigest("")}
		workspace.Versions[stage] = []catalog.PipelineDesignDocumentVersion{}
	}
	return workspace
}

func loadPipelineDesign(ctx context.Context, tx *sql.Tx, pipelineID string) (catalog.PipelineDesignWorkspace, error) {
	workspace := emptyPipelineDesign()
	var created, updated string
	if err := tx.QueryRowContext(ctx, `SELECT pipeline_id,pipeline_revision,revision,state,phase,active_attempt_id,created_at,updated_at FROM pipeline_design_workspaces WHERE pipeline_id=?`, pipelineID).
		Scan(&workspace.PipelineID, &workspace.PipelineRevision, &workspace.Revision, &workspace.State, &workspace.Phase, &workspace.ActiveAttemptID, &created, &updated); err != nil {
		return workspace, fmt.Errorf("read pipeline design workspace: %w", err)
	}
	run, err := readPipelineSnapshot(ctx, tx, pipelineID)
	if err != nil {
		return workspace, err
	}
	workspace.WorkspaceID = run.WorkspaceID
	workspace.CurrentPipelineRevision = run.Revision
	workspace.NeedsDerivation = pipelineDesignNeedsDerivation(run)
	if workspace.CreatedAt, err = time.Parse(time.RFC3339Nano, created); err != nil {
		return workspace, fmt.Errorf("parse pipeline design created time: %w", err)
	}
	if workspace.UpdatedAt, err = time.Parse(time.RFC3339Nano, updated); err != nil {
		return workspace, fmt.Errorf("parse pipeline design updated time: %w", err)
	}
	rows, err := tx.QueryContext(ctx, `SELECT v.stage,v.version,v.content,v.content_digest,v.author,v.source_session_id,v.source_digest,v.selection_snapshot,v.attempt_id,v.reason,v.restored_from_version,v.created_at,COALESCE(d.current_version,0),COALESCE(d.stale,0),COALESCE(d.updated_at,v.created_at) FROM pipeline_design_versions v LEFT JOIN pipeline_design_documents d ON d.pipeline_id=v.pipeline_id AND d.stage=v.stage WHERE v.pipeline_id=? ORDER BY v.stage,v.version`, pipelineID)
	if err != nil {
		return workspace, fmt.Errorf("list pipeline design versions: %w", err)
	}
	for rows.Next() {
		var version catalog.PipelineDesignDocumentVersion
		var selection []byte
		var currentVersion int64
		var stale bool
		if err := rows.Scan(&version.Stage, &version.Version, &version.Content, &version.ContentDigest, &version.Author, &version.SourceSessionID, &version.SourceDigest, &selection, &version.AttemptID, &version.Reason, &version.RestoredFromVersion, &created, &currentVersion, &stale, &updated); err != nil {
			_ = rows.Close()
			return workspace, fmt.Errorf("scan pipeline design version: %w", err)
		}
		version.Selection, err = decodeBrainstormSelection(selection)
		if err != nil {
			_ = rows.Close()
			return workspace, fmt.Errorf("decode pipeline design version selection: %w", err)
		}
		if version.CreatedAt, err = time.Parse(time.RFC3339Nano, created); err != nil {
			_ = rows.Close()
			return workspace, err
		}
		version.UpdatedAt = version.CreatedAt
		workspace.Versions[version.Stage] = append(workspace.Versions[version.Stage], version)
		if version.Version == currentVersion {
			current := version.PipelineDesignDocument
			current.Stale = stale
			if current.UpdatedAt, err = time.Parse(time.RFC3339Nano, updated); err != nil {
				_ = rows.Close()
				return workspace, err
			}
			workspace.Documents[version.Stage] = current
		}
	}
	if err = rows.Err(); err != nil {
		_ = rows.Close()
		return workspace, err
	}
	if err = rows.Close(); err != nil {
		return workspace, err
	}
	rows, err = tx.QueryContext(ctx, `SELECT id,role,content,target,attempt_id,created_at FROM pipeline_design_messages WHERE pipeline_id=? ORDER BY created_at,rowid`, pipelineID)
	if err != nil {
		return workspace, fmt.Errorf("list pipeline design messages: %w", err)
	}
	for rows.Next() {
		var message catalog.PipelineDesignMessage
		if err := rows.Scan(&message.ID, &message.Role, &message.Content, &message.Target, &message.AttemptID, &created); err != nil {
			_ = rows.Close()
			return workspace, err
		}
		if message.CreatedAt, err = time.Parse(time.RFC3339Nano, created); err != nil {
			_ = rows.Close()
			return workspace, err
		}
		workspace.Messages = append(workspace.Messages, message)
	}
	if err = rows.Err(); err != nil {
		_ = rows.Close()
		return workspace, err
	}
	if err = rows.Close(); err != nil {
		return workspace, err
	}
	rows, err = tx.QueryContext(ctx, `SELECT id,request_id,intent_hash,owner_pid,target,status,phase,source_revision,source_pipeline_revision,input_snapshot,selection_snapshot,error_code,result_hash,created_at,updated_at FROM pipeline_design_attempts WHERE pipeline_id=? ORDER BY created_at,rowid`, pipelineID)
	if err != nil {
		return workspace, fmt.Errorf("list pipeline design attempts: %w", err)
	}
	for rows.Next() {
		var attempt catalog.PipelineDesignAttempt
		var input, selections []byte
		if err := rows.Scan(&attempt.ID, &attempt.RequestID, &attempt.IntentHash, &attempt.OwnerPID, &attempt.Target, &attempt.Status, &attempt.Phase, &attempt.SourceRevision, &attempt.SourcePipelineRevision, &input, &selections, &attempt.ErrorCode, &attempt.ResultHash, &created, &updated); err != nil {
			_ = rows.Close()
			return workspace, err
		}
		attempt.PipelineID = pipelineID
		attempt.SessionIDs = make(map[sdd.Stage]string)
		if err := json.Unmarshal(input, &attempt.InputDocuments); err != nil {
			_ = rows.Close()
			return workspace, err
		}
		var snapshots map[sdd.Stage]brainstormSelectionSnapshot
		if err := json.Unmarshal(selections, &snapshots); err != nil {
			_ = rows.Close()
			return workspace, err
		}
		attempt.Selections = make(map[sdd.Stage]catalog.ModelSelection, len(snapshots))
		for stage, snapshot := range snapshots {
			choice := snapshot.ModelSelection
			choice.CredentialIdentity = snapshot.CredentialIdentity
			attempt.Selections[stage] = choice
		}
		if attempt.CreatedAt, err = time.Parse(time.RFC3339Nano, created); err != nil {
			_ = rows.Close()
			return workspace, err
		}
		if attempt.UpdatedAt, err = time.Parse(time.RFC3339Nano, updated); err != nil {
			_ = rows.Close()
			return workspace, err
		}
		workspace.Attempts = append(workspace.Attempts, attempt)
	}
	if err = rows.Err(); err != nil {
		_ = rows.Close()
		return workspace, err
	}
	if err = rows.Close(); err != nil {
		return workspace, err
	}
	rows, err = tx.QueryContext(ctx, `SELECT l.attempt_id,l.stage,l.session_id FROM pipeline_design_attempt_sessions l JOIN pipeline_design_attempts a ON a.id=l.attempt_id WHERE a.pipeline_id=?`, pipelineID)
	if err != nil {
		return workspace, fmt.Errorf("list pipeline design source sessions: %w", err)
	}
	for rows.Next() {
		var attemptID, sessionID string
		var stage sdd.Stage
		if err := rows.Scan(&attemptID, &stage, &sessionID); err != nil {
			_ = rows.Close()
			return workspace, err
		}
		for i := range workspace.Attempts {
			if workspace.Attempts[i].ID == attemptID {
				workspace.Attempts[i].SessionIDs[stage] = sessionID
				selection := workspace.Attempts[i].Selections[stage]
				selection.SessionID = sessionID
				workspace.Attempts[i].Selections[stage] = selection
				break
			}
		}
	}
	if err = rows.Err(); err != nil {
		_ = rows.Close()
		return workspace, err
	}
	if err = rows.Close(); err != nil {
		return workspace, err
	}
	return workspace, nil
}

func (s *Store) GetPipelineDesign(ctx context.Context, pipelineID string) (catalog.PipelineDesignWorkspace, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return catalog.PipelineDesignWorkspace{}, fmt.Errorf("begin pipeline design read: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	workspace, err := loadPipelineDesign(ctx, tx, pipelineID)
	if err != nil {
		return workspace, err
	}
	return workspace, tx.Commit()
}

func writePipelineDesignVersion(ctx context.Context, tx *sql.Tx, pipelineID string, document catalog.PipelineDesignDocument, reason string, restoredFrom int64, now time.Time) error {
	selection, err := json.Marshal(selectionSnapshot(document.Selection))
	if err != nil {
		return fmt.Errorf("encode pipeline design provenance: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO pipeline_design_versions(pipeline_id,stage,version,content,content_digest,author,source_session_id,source_digest,selection_snapshot,attempt_id,reason,restored_from_version,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`, pipelineID, document.Stage, document.Version, document.Content, document.ContentDigest, document.Author, document.SourceSessionID, document.SourceDigest, selection, document.AttemptID, reason, restoredFrom, formatCatalogTime(now)); err != nil {
		return fmt.Errorf("append pipeline design version: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO pipeline_design_documents(pipeline_id,stage,current_version,stale,updated_at) VALUES(?,?,?,?,?) ON CONFLICT(pipeline_id,stage) DO UPDATE SET current_version=excluded.current_version,stale=excluded.stale,updated_at=excluded.updated_at`, pipelineID, document.Stage, document.Version, document.Stale, formatCatalogTime(now)); err != nil {
		return fmt.Errorf("update pipeline design document pointer: %w", err)
	}
	return nil
}

func appendPipelineDesignMessage(ctx context.Context, tx *sql.Tx, pipelineID, role, content, target, attemptID string, now time.Time) error {
	if _, err := tx.ExecContext(ctx, `INSERT INTO pipeline_design_messages(id,pipeline_id,role,content,target,attempt_id,created_at) VALUES(?,?,?,?,?,?,?)`, id.New(), pipelineID, role, content, target, attemptID, formatCatalogTime(now)); err != nil {
		return fmt.Errorf("append pipeline design message: %w", err)
	}
	return nil
}

func (s *Store) OpenPipelineDesign(ctx context.Context, pipelineID string, expectedPipelineRevision int64, initialDocs map[sdd.Stage]catalog.PipelineDesignDocumentInput) (catalog.PipelineDesignWorkspace, error) {
	if !safeBrainstormText(pipelineID, 128) || expectedPipelineRevision < 1 {
		return catalog.PipelineDesignWorkspace{}, sdd.ErrInvalidTransition
	}
	tx, err := s.beginPipelineDesignTx(ctx)
	if err != nil {
		return catalog.PipelineDesignWorkspace{}, fmt.Errorf("begin pipeline design open: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var exists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pipeline_design_workspaces WHERE pipeline_id=?)`, pipelineID).Scan(&exists); err != nil {
		return catalog.PipelineDesignWorkspace{}, err
	}
	if exists {
		workspace, err := loadPipelineDesign(ctx, tx, pipelineID)
		if err != nil {
			return workspace, err
		}
		return workspace, tx.Commit()
	}
	run, err := readPipelineSnapshot(ctx, tx, pipelineID)
	if err != nil {
		return catalog.PipelineDesignWorkspace{}, err
	}
	if run.Revision != expectedPipelineRevision {
		return catalog.PipelineDesignWorkspace{}, ErrPipelineConflict
	}
	activeLegacy, err := legacyPipelineAuthoringActive(ctx, tx, pipelineID)
	if err != nil {
		return catalog.PipelineDesignWorkspace{}, err
	}
	if activeLegacy {
		return catalog.PipelineDesignWorkspace{}, sdd.ErrAuthoringCancellationPending
	}
	for stage := range initialDocs {
		if !validPipelineDesignStage(stage) {
			return catalog.PipelineDesignWorkspace{}, sdd.ErrInvalidTransition
		}
	}
	now := time.Now().UTC()
	state := "ready"
	if pipelineDesignNeedsDerivation(run) {
		state = "approved"
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO pipeline_design_workspaces(pipeline_id,pipeline_revision,revision,state,created_at,updated_at) VALUES(?,?,1,?,?,?)`, pipelineID, run.Revision, state, formatCatalogTime(now), formatCatalogTime(now)); err != nil {
		return catalog.PipelineDesignWorkspace{}, fmt.Errorf("create pipeline design workspace: %w", err)
	}
	documents := emptyPipelineDesign().Documents
	for _, stage := range pipelineDesignStages {
		input, supplied := initialDocs[stage]
		artifact := run.Artifacts[stage]
		if !supplied {
			input = catalog.PipelineDesignDocumentInput{Content: artifact.Content, Author: artifact.Author, SourceSessionID: artifact.SourceSessionID}
		}
		if input.Content == "" {
			continue
		}
		if !validPipelineDesignText(input.Content, catalog.MaxPipelineDesignDocumentBytes) {
			return catalog.PipelineDesignWorkspace{}, sdd.ErrInvalidDocument
		}
		if input.Author == "" {
			input.Author = "user"
		}
		if input.Author != "user" && input.Author != "ai" && input.Author != "legacy/manual" {
			return catalog.PipelineDesignWorkspace{}, sdd.ErrInvalidDocument
		}
		if input.SourceSessionID != "" {
			var workspaceID string
			if err := tx.QueryRowContext(ctx, `SELECT workspace_id FROM sessions WHERE id=?`, input.SourceSessionID).Scan(&workspaceID); err != nil || workspaceID != run.WorkspaceID {
				return catalog.PipelineDesignWorkspace{}, sdd.ErrInvalidDocument
			}
			actual, err := readPipelineDesignSessionSelection(ctx, tx, input.SourceSessionID)
			if err == nil {
				if input.Selection.BackendID != "" && !pipelineDesignSelectionsMatch(input.Selection, actual) {
					return catalog.PipelineDesignWorkspace{}, sdd.ErrInvalidDocument
				}
				input.Selection = actual
			} else if !errors.Is(err, sql.ErrNoRows) || input.Selection.BackendID != "" {
				return catalog.PipelineDesignWorkspace{}, sdd.ErrInvalidDocument
			}
		} else if input.Author == "ai" {
			return catalog.PipelineDesignWorkspace{}, sdd.ErrInvalidDocument
		}
		document := catalog.PipelineDesignDocument{Stage: stage, Version: 1, Content: input.Content, ContentDigest: catalog.PipelineDesignContentDigest(input.Content), Author: input.Author, SourceSessionID: input.SourceSessionID, Selection: input.Selection, UpdatedAt: now}
		document.SourceDigest = catalog.PipelineDesignSourceDigest(stage, documents)
		if stage != sdd.Discovery && documents[sdd.Discovery].Version == 0 || stage == sdd.Plan && documents[sdd.Spec].Version == 0 {
			document.Stale = true
		}
		if input.SourceDigest != "" && input.SourceDigest != document.SourceDigest {
			document.SourceDigest, document.Stale = input.SourceDigest, true
		}
		documents[stage] = document
		if err := writePipelineDesignVersion(ctx, tx, pipelineID, document, "import", 0, now); err != nil {
			return catalog.PipelineDesignWorkspace{}, err
		}
	}
	if documents[sdd.Discovery].Content != "" {
		if err := appendPipelineDesignMessage(ctx, tx, pipelineID, "user", documents[sdd.Discovery].Content, "discovery", "", now); err != nil {
			return catalog.PipelineDesignWorkspace{}, err
		}
	}
	if err := appendPipelineEvent(ctx, tx, pipelineID, "pipeline.design.opened", map[string]any{"revision": 1, "pipelineRevision": run.Revision, "needsDerivation": pipelineDesignNeedsDerivation(run)}, now); err != nil {
		return catalog.PipelineDesignWorkspace{}, err
	}
	workspace, err := loadPipelineDesign(ctx, tx, pipelineID)
	if err != nil {
		return workspace, err
	}
	return workspace, tx.Commit()
}

func validatePipelineDesignMutation(workspace catalog.PipelineDesignWorkspace, ref catalog.PipelineDesignRef) error {
	if workspace.NeedsDerivation || workspace.State == "approved" {
		return ErrPipelineDesignDerivationRequired
	}
	if workspace.PipelineRevision != workspace.CurrentPipelineRevision || workspace.PipelineRevision != ref.PipelineRevision || workspace.Revision != ref.DesignRevision {
		return ErrPipelineConflict
	}
	return nil
}

func pipelineDesignCAS(ctx context.Context, tx *sql.Tx, workspace catalog.PipelineDesignWorkspace, state string, phase sdd.Stage, activeAttemptID string, now time.Time) error {
	result, err := tx.ExecContext(ctx, `UPDATE pipeline_runs SET revision=revision+1,updated_at=? WHERE id=? AND revision=? AND current_stage IN ('discovery','spec','plan')`, formatCatalogTime(now), workspace.PipelineID, workspace.PipelineRevision)
	if err != nil {
		return fmt.Errorf("update pipeline design boundary: %w", err)
	}
	if changed, err := result.RowsAffected(); err != nil || changed != 1 {
		return ErrPipelineConflict
	}
	result, err = tx.ExecContext(ctx, `UPDATE pipeline_design_workspaces SET pipeline_revision=pipeline_revision+1,revision=revision+1,state=?,phase=?,active_attempt_id=?,updated_at=? WHERE pipeline_id=? AND revision=? AND pipeline_revision=?`, state, phase, activeAttemptID, formatCatalogTime(now), workspace.PipelineID, workspace.Revision, workspace.PipelineRevision)
	if err != nil {
		return fmt.Errorf("update pipeline design workspace boundary: %w", err)
	}
	if changed, err := result.RowsAffected(); err != nil || changed != 1 {
		return ErrPipelineConflict
	}
	return nil
}

func readPipelineDesignCommand(ctx context.Context, tx *sql.Tx, pipelineID, requestID, action, intentHash string) (string, []byte, bool, error) {
	var storedAction, storedHash, attemptID string
	var snapshot []byte
	err := tx.QueryRowContext(ctx, `SELECT action,intent_hash,attempt_id,result_snapshot FROM pipeline_design_commands WHERE pipeline_id=? AND request_id=?`, pipelineID, requestID).Scan(&storedAction, &storedHash, &attemptID, &snapshot)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil, false, nil
	}
	if err != nil {
		return "", nil, false, fmt.Errorf("read pipeline design intent: %w", err)
	}
	if storedAction != action || storedHash != intentHash {
		return "", nil, true, ErrPipelineConflict
	}
	return attemptID, snapshot, true, nil
}

func recordPipelineDesignCommand(ctx context.Context, tx *sql.Tx, ref catalog.PipelineDesignRef, action, intentHash, attemptID string, snapshot any, now time.Time) error {
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO pipeline_design_commands(pipeline_id,request_id,action,intent_hash,attempt_id,result_snapshot,created_at) VALUES(?,?,?,?,?,?,?)`, ref.PipelineID, ref.RequestID, action, intentHash, attemptID, encoded, formatCatalogTime(now)); err != nil {
		return fmt.Errorf("persist pipeline design command: %w", err)
	}
	return nil
}

func pipelineDesignAttemptByID(workspace catalog.PipelineDesignWorkspace, attemptID string) (catalog.PipelineDesignAttempt, bool) {
	for _, attempt := range workspace.Attempts {
		if attempt.ID == attemptID {
			return attempt, true
		}
	}
	return catalog.PipelineDesignAttempt{}, false
}

func pipelineDesignAttemptHash(in catalog.PipelineDesignAttemptRequest) (string, error) {
	coreHash, err := in.Hash()
	if err != nil {
		return "", err
	}
	return catalog.PipelineDesignContentDigest(coreHash + ":" + in.IntentHash), nil
}

func (s *Store) BeginPipelineDesignAttempt(ctx context.Context, in catalog.PipelineDesignAttemptRequest) (catalog.PipelineDesignWorkspace, catalog.PipelineDesignAttempt, bool, error) {
	var empty catalog.PipelineDesignWorkspace
	var noAttempt catalog.PipelineDesignAttempt
	if !validPipelineDesignRef(in.Ref) || !validPipelineDesignTarget(in.Target) || !validPipelineDesignText(in.Message, catalog.MaxPipelineDesignMessageBytes) || len(in.IntentHash) != 64 || in.OwnerPID < 0 {
		return empty, noAttempt, false, sdd.ErrInvalidTransition
	}
	if _, err := hex.DecodeString(in.IntentHash); err != nil {
		return empty, noAttempt, false, sdd.ErrInvalidTransition
	}
	intentHash, err := pipelineDesignAttemptHash(in)
	if err != nil {
		return empty, noAttempt, false, err
	}
	tx, err := s.beginPipelineDesignTx(ctx)
	if err != nil {
		return empty, noAttempt, false, err
	}
	defer func() { _ = tx.Rollback() }()
	attemptID, _, found, err := readPipelineDesignCommand(ctx, tx, in.Ref.PipelineID, in.Ref.RequestID, "prepare", intentHash)
	if err != nil {
		return empty, noAttempt, false, err
	}
	workspace, err := loadPipelineDesign(ctx, tx, in.Ref.PipelineID)
	if err != nil {
		return empty, noAttempt, false, err
	}
	if found {
		attempt, found := pipelineDesignAttemptByID(workspace, attemptID)
		if !found {
			return empty, noAttempt, false, ErrPipelineConflict
		}
		return workspace, attempt, false, tx.Commit()
	}
	if err := validatePipelineDesignMutation(workspace, in.Ref); err != nil {
		return empty, noAttempt, false, err
	}
	if workspace.ActiveAttemptID != "" || workspace.State == "cancellation_pending" {
		return workspace, noAttempt, false, sdd.ErrAuthoringCancellationPending
	}
	if !validPipelineDesignText(workspace.Documents[sdd.Discovery].Content, catalog.MaxPipelineDesignDocumentBytes) {
		return workspace, noAttempt, false, sdd.ErrEvidenceRequired
	}
	if in.Target == "plan" && (workspace.Documents[sdd.Spec].Stale || workspace.Documents[sdd.Spec].Version == 0) {
		return workspace, noAttempt, false, ErrPipelineDesignSpecStale
	}
	for stage, selection := range in.Selections {
		if !validPipelineDesignStage(stage) || selection.SessionID != "" || !safeBrainstormText(selection.BackendID, 128) || !safeBrainstormText(selection.ModelID, 512) {
			return empty, noAttempt, false, sdd.ErrInvalidTransition
		}
	}
	if len(in.Selections) == 0 {
		return empty, noAttempt, false, sdd.ErrInvalidTransition
	}
	requiredStages := []sdd.Stage{sdd.Plan}
	if in.Target != "plan" {
		requiredStages = append(requiredStages, sdd.Spec)
	}
	if in.Target == "discovery" {
		requiredStages = append(requiredStages, sdd.Discovery)
	}
	for _, stage := range requiredStages {
		if _, selected := in.Selections[stage]; !selected {
			return empty, noAttempt, false, sdd.ErrInvalidTransition
		}
	}
	phase := sdd.Stage(in.Target)
	if in.Target == "all" {
		phase = sdd.Spec
	}
	now := time.Now().UTC()
	attempt := catalog.PipelineDesignAttempt{ID: id.New(), PipelineID: in.Ref.PipelineID, RequestID: in.Ref.RequestID, IntentHash: in.IntentHash, Target: in.Target, Status: "running", Phase: phase,
		OwnerPID: in.OwnerPID, SourceRevision: workspace.Revision + 1, SourcePipelineRevision: workspace.PipelineRevision + 1, InputDocuments: workspace.Documents, Selections: in.Selections, SessionIDs: map[sdd.Stage]string{}, CreatedAt: now, UpdatedAt: now}
	input, err := json.Marshal(attempt.InputDocuments)
	if err != nil {
		return empty, noAttempt, false, err
	}
	snapshots := make(map[sdd.Stage]brainstormSelectionSnapshot, len(attempt.Selections))
	for stage, choice := range attempt.Selections {
		snapshots[stage] = selectionSnapshot(choice)
	}
	selections, err := json.Marshal(snapshots)
	if err != nil {
		return empty, noAttempt, false, err
	}
	if err := pipelineDesignCAS(ctx, tx, workspace, "running", phase, attempt.ID, now); err != nil {
		return empty, noAttempt, false, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO pipeline_design_attempts(id,pipeline_id,request_id,intent_hash,owner_pid,target,status,phase,source_revision,source_pipeline_revision,input_snapshot,selection_snapshot,created_at,updated_at) VALUES(?,?,?,?,?,?,'running',?,?,?,?,?,?,?)`, attempt.ID, attempt.PipelineID, attempt.RequestID, attempt.IntentHash, attempt.OwnerPID, attempt.Target, attempt.Phase, attempt.SourceRevision, attempt.SourcePipelineRevision, input, selections, formatCatalogTime(now), formatCatalogTime(now)); err != nil {
		return empty, noAttempt, false, fmt.Errorf("admit pipeline design attempt: %w", err)
	}
	if err := appendPipelineDesignMessage(ctx, tx, workspace.PipelineID, "user", in.Message, in.Target, attempt.ID, now); err != nil {
		return empty, noAttempt, false, err
	}
	if err := recordPipelineDesignCommand(ctx, tx, in.Ref, "prepare", intentHash, attempt.ID, struct{}{}, now); err != nil {
		return empty, noAttempt, false, err
	}
	if err := appendPipelineEvent(ctx, tx, workspace.PipelineID, "pipeline.design.attempt.admitted", map[string]any{"attemptId": attempt.ID, "requestId": in.Ref.RequestID, "target": in.Target, "phase": phase, "sourceRevision": attempt.SourceRevision}, now); err != nil {
		return empty, noAttempt, false, err
	}
	workspace, err = loadPipelineDesign(ctx, tx, workspace.PipelineID)
	if err != nil {
		return empty, noAttempt, false, err
	}
	attempt, _ = pipelineDesignAttemptByID(workspace, attempt.ID)
	return workspace, attempt, true, tx.Commit()
}

func validatePipelineDesignOwner(workspace catalog.PipelineDesignWorkspace, attemptID string, requireSource bool) (catalog.PipelineDesignAttempt, error) {
	attempt, found := pipelineDesignAttemptByID(workspace, attemptID)
	if !found || workspace.ActiveAttemptID != attemptID || attempt.Status != "running" || workspace.State != "running" {
		return catalog.PipelineDesignAttempt{}, ErrPipelineConflict
	}
	if workspace.NeedsDerivation || workspace.CurrentPipelineRevision != workspace.PipelineRevision ||
		(requireSource && (attempt.SourceRevision != workspace.Revision || attempt.SourcePipelineRevision != workspace.PipelineRevision)) {
		return catalog.PipelineDesignAttempt{}, ErrPipelineConflict
	}
	return attempt, nil
}

func (s *Store) SetPipelineDesignPhase(ctx context.Context, pipelineID, attemptID string, phase sdd.Stage) (catalog.PipelineDesignWorkspace, error) {
	if !safeBrainstormText(pipelineID, 128) || !safeBrainstormText(attemptID, 128) || !validPipelineDesignStage(phase) {
		return catalog.PipelineDesignWorkspace{}, sdd.ErrInvalidTransition
	}
	tx, err := s.beginPipelineDesignTx(ctx)
	if err != nil {
		return catalog.PipelineDesignWorkspace{}, err
	}
	defer func() { _ = tx.Rollback() }()
	workspace, err := loadPipelineDesign(ctx, tx, pipelineID)
	if err != nil {
		return workspace, err
	}
	attempt, err := validatePipelineDesignOwner(workspace, attemptID, true)
	if err != nil {
		return workspace, err
	}
	if _, ok := attempt.Selections[phase]; !ok || attempt.Target == "plan" && phase != sdd.Plan || attempt.Target == "spec" && phase == sdd.Discovery {
		return workspace, sdd.ErrInvalidTransition
	}
	if attempt.Phase == phase && workspace.Phase == phase {
		return workspace, tx.Commit()
	}
	now := time.Now().UTC()
	if _, err := tx.ExecContext(ctx, `UPDATE pipeline_design_attempts SET phase=?,updated_at=? WHERE id=? AND status='running'`, phase, formatCatalogTime(now), attemptID); err != nil {
		return workspace, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE pipeline_design_workspaces SET phase=?,updated_at=? WHERE pipeline_id=? AND active_attempt_id=?`, phase, formatCatalogTime(now), pipelineID, attemptID); err != nil {
		return workspace, err
	}
	if err := appendPipelineEvent(ctx, tx, pipelineID, "pipeline.design.phase.started", map[string]any{"attemptId": attemptID, "phase": phase}, now); err != nil {
		return workspace, err
	}
	workspace, err = loadPipelineDesign(ctx, tx, pipelineID)
	if err != nil {
		return workspace, err
	}
	return workspace, tx.Commit()
}

func readPipelineDesignSessionSelection(ctx context.Context, tx *sql.Tx, sessionID string) (catalog.ModelSelection, error) {
	var selection catalog.ModelSelection
	var checked, efforts string
	if err := tx.QueryRowContext(ctx, `SELECT session_id,backend_id,model_id,reasoning_effort,catalog_revision,local_revision,source,executable_path,executable_version,workspace_path,checked_at,destination,credential_identity,status,confirm_unverified_manual,confirm_unfiltered,confirm_jit_load,max_output_tokens,context_length,supported_reasoning_efforts FROM session_model_selections WHERE session_id=?`, sessionID).Scan(
		&selection.SessionID, &selection.BackendID, &selection.ModelID, &selection.ReasoningEffort, &selection.CatalogRevision, &selection.LocalRevision, &selection.Source, &selection.ExecutablePath, &selection.ExecutableVersion, &selection.WorkspacePath, &checked, &selection.Destination, &selection.CredentialIdentity, &selection.Status, &selection.ConfirmUnverifiedManual, &selection.ConfirmUnfiltered, &selection.ConfirmJITLoad, &selection.MaxOutputTokens, &selection.ContextLength, &efforts); err != nil {
		return selection, fmt.Errorf("read design session model selection: %w", err)
	}
	if len(efforts) > 4096 || json.Unmarshal([]byte(efforts), &selection.SupportedReasoningEfforts) != nil {
		return selection, fmt.Errorf("invalid stored reasoning capability")
	}
	var err error
	selection.CheckedAt, err = time.Parse(time.RFC3339Nano, checked)
	return selection, err
}

func pipelineDesignSelectionsMatch(expected, actual catalog.ModelSelection) bool {
	expected.SessionID, actual.SessionID = "", ""
	expected.CheckedAt, actual.CheckedAt = expected.CheckedAt.UTC(), actual.CheckedAt.UTC()
	// This execution limit is not represented in the existing session selection
	// table. The design attempt is its immutable canonical snapshot.
	actual.MaxAssistantOutputBytes = expected.MaxAssistantOutputBytes
	expectedJSON, err := json.Marshal(selectionSnapshot(expected))
	if err != nil {
		return false
	}
	actualJSON, err := json.Marshal(selectionSnapshot(actual))
	return err == nil && string(expectedJSON) == string(actualJSON)
}

func (s *Store) LinkPipelineDesignSession(ctx context.Context, pipelineID, attemptID string, stage sdd.Stage, sessionID string) (catalog.PipelineDesignWorkspace, error) {
	if !safeBrainstormText(pipelineID, 128) || !safeBrainstormText(attemptID, 128) || !safeBrainstormText(sessionID, 128) || !validPipelineDesignStage(stage) {
		return catalog.PipelineDesignWorkspace{}, sdd.ErrInvalidTransition
	}
	tx, err := s.beginPipelineDesignTx(ctx)
	if err != nil {
		return catalog.PipelineDesignWorkspace{}, err
	}
	defer func() { _ = tx.Rollback() }()
	workspace, err := loadPipelineDesign(ctx, tx, pipelineID)
	if err != nil {
		return workspace, err
	}
	attempt, found := pipelineDesignAttemptByID(workspace, attemptID)
	if !found {
		return workspace, ErrPipelineConflict
	}
	if linked := attempt.SessionIDs[stage]; linked != "" {
		if linked != sessionID {
			return workspace, ErrPipelineConflict
		}
		return workspace, tx.Commit()
	}
	attempt, err = validatePipelineDesignOwner(workspace, attemptID, true)
	if err != nil {
		return workspace, err
	}
	choice, exists := attempt.Selections[stage]
	if !exists || attempt.Target == "plan" && stage != sdd.Plan || attempt.Target == "spec" && stage == sdd.Discovery {
		return workspace, sdd.ErrInvalidTransition
	}
	var workspaceID, backendID string
	if err := tx.QueryRowContext(ctx, `SELECT workspace_id,backend_id FROM sessions WHERE id=?`, sessionID).Scan(&workspaceID, &backendID); err != nil || workspaceID != workspace.WorkspaceID || backendID != choice.BackendID {
		return workspace, sdd.ErrInvalidTransition
	}
	actual, err := readPipelineDesignSessionSelection(ctx, tx, sessionID)
	if err != nil || !pipelineDesignSelectionsMatch(choice, actual) {
		return workspace, sdd.ErrInvalidTransition
	}
	now := time.Now().UTC()
	if _, err := tx.ExecContext(ctx, `INSERT INTO pipeline_design_attempt_sessions(attempt_id,stage,session_id,created_at) VALUES(?,?,?,?)`, attemptID, stage, sessionID, formatCatalogTime(now)); err != nil {
		return workspace, fmt.Errorf("link pipeline design source session: %w", err)
	}
	if err := appendPipelineEvent(ctx, tx, pipelineID, "pipeline.design.session.linked", map[string]any{"attemptId": attemptID, "stage": stage, "sessionId": sessionID}, now); err != nil {
		return workspace, err
	}
	workspace, err = loadPipelineDesign(ctx, tx, pipelineID)
	if err != nil {
		return workspace, err
	}
	return workspace, tx.Commit()
}

func pipelineDesignCompleteHash(in catalog.PipelineDesignCompleteRequest) (string, error) {
	encoded, err := json.Marshal(in)
	if err != nil {
		return "", err
	}
	return catalog.PipelineDesignContentDigest(string(encoded)), nil
}

func validPipelineDesignGeneratedSet(target string, documents map[sdd.Stage]catalog.PipelineDesignDocumentInput) bool {
	for stage := range documents {
		if !validPipelineDesignStage(stage) {
			return false
		}
	}
	_, discovery := documents[sdd.Discovery]
	_, spec := documents[sdd.Spec]
	_, plan := documents[sdd.Plan]
	switch target {
	case "all":
		return spec && plan
	case "discovery":
		return discovery && spec && plan
	case "spec":
		return !discovery && spec && plan
	case "plan":
		return !discovery && !spec && plan
	default:
		return false
	}
}

func stalePipelineDesignDependents(ctx context.Context, tx *sql.Tx, pipelineID string, stage sdd.Stage, now time.Time) error {
	if stage == sdd.Plan {
		return nil
	}
	stages := []sdd.Stage{sdd.Plan}
	if stage == sdd.Discovery {
		stages = append(stages, sdd.Spec)
	}
	for _, dependent := range stages {
		if _, err := tx.ExecContext(ctx, `UPDATE pipeline_design_documents SET stale=1,updated_at=? WHERE pipeline_id=? AND stage=?`, formatCatalogTime(now), pipelineID, dependent); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) CompletePipelineDesignAttempt(ctx context.Context, in catalog.PipelineDesignCompleteRequest) (catalog.PipelineDesignWorkspace, error) {
	if !safeBrainstormText(in.PipelineID, 128) || !safeBrainstormText(in.AttemptID, 128) || in.SourceRevision < 1 || len(in.Documents) == 0 || len(in.Summary) > catalog.MaxPipelineDesignMessageBytes || !utf8.ValidString(in.Summary) || strings.ContainsRune(in.Summary, 0) {
		return catalog.PipelineDesignWorkspace{}, sdd.ErrInvalidTransition
	}
	resultHash, err := pipelineDesignCompleteHash(in)
	if err != nil {
		return catalog.PipelineDesignWorkspace{}, err
	}
	tx, err := s.beginPipelineDesignTx(ctx)
	if err != nil {
		return catalog.PipelineDesignWorkspace{}, err
	}
	defer func() { _ = tx.Rollback() }()
	workspace, err := loadPipelineDesign(ctx, tx, in.PipelineID)
	if err != nil {
		return workspace, err
	}
	attempt, found := pipelineDesignAttemptByID(workspace, in.AttemptID)
	if !found {
		return workspace, ErrPipelineConflict
	}
	if attempt.Status == "completed" {
		if attempt.ResultHash != resultHash {
			return workspace, ErrPipelineConflict
		}
		return workspace, tx.Commit()
	}
	attempt, err = validatePipelineDesignOwner(workspace, in.AttemptID, true)
	if err != nil || attempt.SourceRevision != in.SourceRevision {
		return workspace, ErrPipelineConflict
	}
	if !validPipelineDesignGeneratedSet(attempt.Target, in.Documents) {
		return workspace, sdd.ErrInvalidDocument
	}
	if attempt.Target == "plan" && (workspace.Documents[sdd.Spec].Stale || workspace.Documents[sdd.Spec].Version == 0) {
		return workspace, ErrPipelineDesignSpecStale
	}
	now := time.Now().UTC()
	documents := make(map[sdd.Stage]catalog.PipelineDesignDocument, 3)
	for stage, document := range workspace.Documents {
		documents[stage] = document
	}
	for _, stage := range pipelineDesignStages {
		input, updated := in.Documents[stage]
		if !updated {
			continue
		}
		sourceSessionID := attempt.SessionIDs[stage]
		selection, selected := attempt.Selections[stage]
		if !validPipelineDesignText(input.Content, catalog.MaxPipelineDesignDocumentBytes) || (input.Author != "" && input.Author != "ai") || sourceSessionID == "" || input.SourceSessionID != sourceSessionID || !selected ||
			(input.Selection.BackendID != "" && !pipelineDesignSelectionsMatch(selection, input.Selection)) || (input.Selection.SessionID != "" && input.Selection.SessionID != sourceSessionID) {
			return workspace, sdd.ErrInvalidDocument
		}
		document := catalog.PipelineDesignDocument{Stage: stage, Version: documents[stage].Version + 1, Content: input.Content, ContentDigest: catalog.PipelineDesignContentDigest(input.Content), Author: "ai", SourceSessionID: sourceSessionID, Selection: selection, AttemptID: attempt.ID, UpdatedAt: now}
		document.SourceDigest = catalog.PipelineDesignSourceDigest(stage, documents)
		if input.SourceDigest != "" && input.SourceDigest != document.SourceDigest {
			return workspace, ErrPipelineConflict
		}
		documents[stage] = document
	}
	if err := pipelineDesignCAS(ctx, tx, workspace, "ready", "", "", now); err != nil {
		return workspace, err
	}
	for _, stage := range pipelineDesignStages {
		if _, updated := in.Documents[stage]; !updated {
			continue
		}
		if err := stalePipelineDesignDependents(ctx, tx, workspace.PipelineID, stage, now); err != nil {
			return workspace, err
		}
		if err := writePipelineDesignVersion(ctx, tx, workspace.PipelineID, documents[stage], "generated", 0, now); err != nil {
			return workspace, err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE pipeline_design_attempts SET status='completed',error_code='',result_hash=?,updated_at=? WHERE id=? AND status='running'`, resultHash, formatCatalogTime(now), attempt.ID); err != nil {
		return workspace, err
	}
	if strings.TrimSpace(in.Summary) != "" {
		if err := appendPipelineDesignMessage(ctx, tx, workspace.PipelineID, "assistant", in.Summary, attempt.Target, attempt.ID, now); err != nil {
			return workspace, err
		}
	}
	if err := appendPipelineEvent(ctx, tx, workspace.PipelineID, "pipeline.design.attempt.completed", map[string]any{"attemptId": attempt.ID, "resultHash": resultHash, "target": attempt.Target}, now); err != nil {
		return workspace, err
	}
	workspace, err = loadPipelineDesign(ctx, tx, in.PipelineID)
	if err != nil {
		return workspace, err
	}
	return workspace, tx.Commit()
}

func (s *Store) RequestPipelineDesignCancellation(ctx context.Context, pipelineID, attemptID string) (catalog.PipelineDesignWorkspace, error) {
	if !safeBrainstormText(pipelineID, 128) || !safeBrainstormText(attemptID, 128) {
		return catalog.PipelineDesignWorkspace{}, sdd.ErrInvalidTransition
	}
	tx, err := s.beginPipelineDesignTx(ctx)
	if err != nil {
		return catalog.PipelineDesignWorkspace{}, err
	}
	defer func() { _ = tx.Rollback() }()
	workspace, err := loadPipelineDesign(ctx, tx, pipelineID)
	if err != nil {
		return workspace, err
	}
	attempt, found := pipelineDesignAttemptByID(workspace, attemptID)
	if !found {
		return workspace, ErrPipelineConflict
	}
	if attempt.Status == "cancellation_pending" && workspace.ActiveAttemptID == attemptID || attempt.Status == "cancelled" {
		return workspace, tx.Commit()
	}
	if workspace.ActiveAttemptID != attemptID || attempt.Status != "running" || workspace.NeedsDerivation || workspace.CurrentPipelineRevision != workspace.PipelineRevision {
		return workspace, ErrPipelineConflict
	}
	now := time.Now().UTC()
	if _, err := tx.ExecContext(ctx, `UPDATE pipeline_design_attempts SET status='cancellation_pending',updated_at=? WHERE id=? AND status='running'`, formatCatalogTime(now), attemptID); err != nil {
		return workspace, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE pipeline_design_workspaces SET state='cancellation_pending',updated_at=? WHERE pipeline_id=? AND active_attempt_id=?`, formatCatalogTime(now), pipelineID, attemptID); err != nil {
		return workspace, err
	}
	if err := appendPipelineEvent(ctx, tx, pipelineID, "pipeline.design.cancellation.requested", map[string]any{"attemptId": attemptID}, now); err != nil {
		return workspace, err
	}
	workspace, err = loadPipelineDesign(ctx, tx, pipelineID)
	if err != nil {
		return workspace, err
	}
	return workspace, tx.Commit()
}

func pipelineDesignErrorCode(code string) bool {
	if len(code) > 64 {
		return false
	}
	for _, char := range code {
		if (char < 'a' || char > 'z') && (char < '0' || char > '9') && char != '_' {
			return false
		}
	}
	return true
}

func settlePipelineDesignAttempt(ctx context.Context, tx *sql.Tx, workspace catalog.PipelineDesignWorkspace, attempt catalog.PipelineDesignAttempt, status, errorCode string, now time.Time) error {
	if workspace.PipelineRevision == workspace.CurrentPipelineRevision && !workspace.NeedsDerivation {
		if err := pipelineDesignCAS(ctx, tx, workspace, "paused", "", "", now); err != nil {
			return err
		}
	} else {
		// An external pipeline mutation must not be overwritten while this owner
		// settles. Only the design attempt is closed; new commands still conflict.
		result, err := tx.ExecContext(ctx, `UPDATE pipeline_design_workspaces SET revision=revision+1,state='paused',phase='',active_attempt_id='',updated_at=? WHERE pipeline_id=? AND revision=? AND active_attempt_id=?`, formatCatalogTime(now), workspace.PipelineID, workspace.Revision, attempt.ID)
		if err != nil {
			return err
		}
		if changed, err := result.RowsAffected(); err != nil || changed != 1 {
			return ErrPipelineConflict
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE pipeline_design_attempts SET status=?,error_code=?,updated_at=? WHERE id=? AND status IN ('running','cancellation_pending')`, status, errorCode, formatCatalogTime(now), attempt.ID); err != nil {
		return err
	}
	return appendPipelineEvent(ctx, tx, workspace.PipelineID, "pipeline.design.attempt."+status, map[string]any{"attemptId": attempt.ID, "errorCode": errorCode}, now)
}

func (s *Store) FailPipelineDesignAttempt(ctx context.Context, in catalog.PipelineDesignFailRequest) (catalog.PipelineDesignWorkspace, error) {
	if !safeBrainstormText(in.PipelineID, 128) || !safeBrainstormText(in.AttemptID, 128) || !pipelineDesignErrorCode(in.ErrorCode) ||
		(in.Status != "failed" && in.Status != "cancelled" && in.Status != "interrupted" && in.Status != "stale") {
		return catalog.PipelineDesignWorkspace{}, sdd.ErrInvalidTransition
	}
	tx, err := s.beginPipelineDesignTx(ctx)
	if err != nil {
		return catalog.PipelineDesignWorkspace{}, err
	}
	defer func() { _ = tx.Rollback() }()
	workspace, err := loadPipelineDesign(ctx, tx, in.PipelineID)
	if err != nil {
		return workspace, err
	}
	attempt, found := pipelineDesignAttemptByID(workspace, in.AttemptID)
	if !found {
		return workspace, ErrPipelineConflict
	}
	if attempt.Status == in.Status && attempt.ErrorCode == in.ErrorCode {
		return workspace, tx.Commit()
	}
	if workspace.ActiveAttemptID != in.AttemptID || (attempt.Status != "running" && attempt.Status != "cancellation_pending") {
		return workspace, ErrPipelineConflict
	}
	if attempt.Status == "cancellation_pending" && in.Status == "failed" {
		return workspace, sdd.ErrAuthoringCancellationPending
	}
	if err := settlePipelineDesignAttempt(ctx, tx, workspace, attempt, in.Status, in.ErrorCode, time.Now().UTC()); err != nil {
		return workspace, err
	}
	workspace, err = loadPipelineDesign(ctx, tx, in.PipelineID)
	if err != nil {
		return workspace, err
	}
	return workspace, tx.Commit()
}

func (s *Store) EditPipelineDesignDocument(ctx context.Context, in catalog.PipelineDesignEditRequest) (catalog.PipelineDesignWorkspace, error) {
	if !validPipelineDesignRef(in.Ref) || !validPipelineDesignStage(in.Stage) || !validPipelineDesignText(in.Content, catalog.MaxPipelineDesignDocumentBytes) {
		return catalog.PipelineDesignWorkspace{}, sdd.ErrInvalidDocument
	}
	intentHash, err := in.Hash()
	if err != nil {
		return catalog.PipelineDesignWorkspace{}, err
	}
	return s.mutatePipelineDesignDocument(ctx, in.Ref, in.Stage, in.Content, 0, "edit", intentHash)
}

func (s *Store) RestorePipelineDesignDocument(ctx context.Context, in catalog.PipelineDesignRestoreRequest) (catalog.PipelineDesignWorkspace, error) {
	if !validPipelineDesignRef(in.Ref) || !validPipelineDesignStage(in.Stage) || in.Version < 1 {
		return catalog.PipelineDesignWorkspace{}, sdd.ErrInvalidDocument
	}
	intentHash, err := in.Hash()
	if err != nil {
		return catalog.PipelineDesignWorkspace{}, err
	}
	return s.mutatePipelineDesignDocument(ctx, in.Ref, in.Stage, "", in.Version, "restore", intentHash)
}

func (s *Store) mutatePipelineDesignDocument(ctx context.Context, ref catalog.PipelineDesignRef, stage sdd.Stage, content string, restoredFrom int64, action, intentHash string) (catalog.PipelineDesignWorkspace, error) {
	tx, err := s.beginPipelineDesignTx(ctx)
	if err != nil {
		return catalog.PipelineDesignWorkspace{}, err
	}
	defer func() { _ = tx.Rollback() }()
	_, _, replay, err := readPipelineDesignCommand(ctx, tx, ref.PipelineID, ref.RequestID, action, intentHash)
	if err != nil {
		return catalog.PipelineDesignWorkspace{}, err
	}
	workspace, err := loadPipelineDesign(ctx, tx, ref.PipelineID)
	if err != nil {
		return workspace, err
	}
	if replay {
		return workspace, tx.Commit()
	}
	if err := validatePipelineDesignMutation(workspace, ref); err != nil {
		return workspace, err
	}
	now := time.Now().UTC()
	document := catalog.PipelineDesignDocument{Stage: stage, Version: workspace.Documents[stage].Version + 1, Content: content, Author: "user", UpdatedAt: now}
	reason := "manual"
	if restoredFrom > 0 {
		var previous *catalog.PipelineDesignDocumentVersion
		for i := range workspace.Versions[stage] {
			if workspace.Versions[stage][i].Version == restoredFrom {
				previous = &workspace.Versions[stage][i]
				break
			}
		}
		if previous == nil {
			return workspace, sdd.ErrInvalidDocument
		}
		document.Content = previous.Content
		reason = "restored"
	}
	document.ContentDigest = catalog.PipelineDesignContentDigest(document.Content)
	document.SourceDigest = catalog.PipelineDesignSourceDigest(stage, workspace.Documents)
	if stage == sdd.Plan && (workspace.Documents[sdd.Spec].Version == 0 || workspace.Documents[sdd.Spec].Stale) || stage != sdd.Discovery && workspace.Documents[sdd.Discovery].Version == 0 {
		document.Stale = true
	}
	if err := pipelineDesignCAS(ctx, tx, workspace, workspace.State, workspace.Phase, workspace.ActiveAttemptID, now); err != nil {
		return workspace, err
	}
	if err := stalePipelineDesignDependents(ctx, tx, ref.PipelineID, stage, now); err != nil {
		return workspace, err
	}
	if err := writePipelineDesignVersion(ctx, tx, ref.PipelineID, document, reason, restoredFrom, now); err != nil {
		return workspace, err
	}
	if err := appendPipelineEvent(ctx, tx, ref.PipelineID, "pipeline.design.document."+reason, map[string]any{"stage": stage, "version": document.Version, "contentDigest": document.ContentDigest, "restoredFromVersion": restoredFrom, "actor": "local_user"}, now); err != nil {
		return workspace, err
	}
	workspace, err = loadPipelineDesign(ctx, tx, ref.PipelineID)
	if err != nil {
		return workspace, err
	}
	if err := recordPipelineDesignCommand(ctx, tx, ref, action, intentHash, "", workspace, now); err != nil {
		return workspace, err
	}
	return workspace, tx.Commit()
}

func (s *Store) ApprovePipelineDesign(ctx context.Context, in catalog.PipelineDesignApproveRequest) (catalog.PipelineRun, error) {
	if !validPipelineDesignRef(in.Ref) || len(in.Digests) != 3 {
		return catalog.PipelineRun{}, sdd.ErrInvalidTransition
	}
	for stage, digest := range in.Digests {
		if !validPipelineDesignStage(stage) || len(digest) != 64 {
			return catalog.PipelineRun{}, sdd.ErrInvalidTransition
		}
		if _, err := hex.DecodeString(digest); err != nil {
			return catalog.PipelineRun{}, sdd.ErrInvalidTransition
		}
	}
	intentHash, err := in.Hash()
	if err != nil {
		return catalog.PipelineRun{}, err
	}
	tx, err := s.beginPipelineDesignTx(ctx)
	if err != nil {
		return catalog.PipelineRun{}, err
	}
	defer func() { _ = tx.Rollback() }()
	_, snapshot, replay, err := readPipelineDesignCommand(ctx, tx, in.Ref.PipelineID, in.Ref.RequestID, "approve", intentHash)
	if err != nil {
		return catalog.PipelineRun{}, err
	}
	if replay {
		var run catalog.PipelineRun
		if err := json.Unmarshal(snapshot, &run); err != nil || run.ID != in.Ref.PipelineID {
			return catalog.PipelineRun{}, ErrPipelineConflict
		}
		return run, tx.Commit()
	}
	workspace, err := loadPipelineDesign(ctx, tx, in.Ref.PipelineID)
	if err != nil {
		return catalog.PipelineRun{}, err
	}
	if err := validatePipelineDesignMutation(workspace, in.Ref); err != nil {
		return catalog.PipelineRun{}, err
	}
	if workspace.ActiveAttemptID != "" || workspace.State == "running" || workspace.State == "cancellation_pending" {
		return catalog.PipelineRun{}, sdd.ErrAuthoringCancellationPending
	}
	for _, stage := range pipelineDesignStages {
		document := workspace.Documents[stage]
		if document.Version < 1 || !validPipelineDesignText(document.Content, catalog.MaxPipelineDesignDocumentBytes) || document.Stale || document.ContentDigest != in.Digests[stage] ||
			document.SourceDigest != catalog.PipelineDesignSourceDigest(stage, workspace.Documents) {
			return catalog.PipelineRun{}, sdd.ErrEvidenceRequired
		}
		if document.Author == "ai" {
			if document.SourceSessionID == "" {
				return catalog.PipelineRun{}, sdd.ErrEvidenceRequired
			}
			var sessionWorkspace string
			if err := tx.QueryRowContext(ctx, `SELECT workspace_id FROM sessions WHERE id=?`, document.SourceSessionID).Scan(&sessionWorkspace); err != nil || sessionWorkspace != workspace.WorkspaceID {
				return catalog.PipelineRun{}, sdd.ErrEvidenceRequired
			}
			if document.AttemptID != "" {
				var linked bool
				if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pipeline_design_attempt_sessions WHERE attempt_id=? AND stage=? AND session_id=?)`, document.AttemptID, stage, document.SourceSessionID).Scan(&linked); err != nil || !linked {
					return catalog.PipelineRun{}, sdd.ErrEvidenceRequired
				}
			}
		}
	}
	now := time.Now().UTC()
	publishedVersions := make(map[sdd.Stage]int64, 3)
	for _, stage := range pipelineDesignStages {
		var previousVersion int64
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT version FROM pipeline_artifacts WHERE pipeline_id=? AND stage=?),0)`, in.Ref.PipelineID, stage).Scan(&previousVersion); err != nil {
			return catalog.PipelineRun{}, err
		}
		publishedVersions[stage] = max(workspace.Documents[stage].Version, previousVersion+1)
	}
	status := map[sdd.Stage]sdd.Status{sdd.Discovery: sdd.Completed, sdd.Spec: sdd.Completed, sdd.Plan: sdd.Completed, sdd.Code: sdd.Active, sdd.Eval: sdd.Pending}
	encodedStatus, err := json.Marshal(status)
	if err != nil {
		return catalog.PipelineRun{}, err
	}
	result, err := tx.ExecContext(ctx, `UPDATE pipeline_runs SET current_stage='code',stage_status=?,revision=revision+1,discovery_frozen_version=?,updated_at=? WHERE id=? AND revision=? AND current_stage IN ('discovery','spec','plan')`, encodedStatus, publishedVersions[sdd.Discovery], formatCatalogTime(now), in.Ref.PipelineID, in.Ref.PipelineRevision)
	if err != nil {
		return catalog.PipelineRun{}, err
	}
	if changed, err := result.RowsAffected(); err != nil || changed != 1 {
		return catalog.PipelineRun{}, ErrPipelineConflict
	}
	for _, stage := range pipelineDesignStages {
		document := workspace.Documents[stage]
		author, sourceSessionID := document.Author, document.SourceSessionID
		if author == "legacy/manual" {
			author = "user"
		}
		if stage == sdd.Discovery {
			author, sourceSessionID = "user", ""
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO pipeline_artifacts(pipeline_id,stage,version,content,author,source_session_id,updated_at) VALUES(?,?,?,?,?,?,?) ON CONFLICT(pipeline_id,stage) DO UPDATE SET version=excluded.version,content=excluded.content,author=excluded.author,source_session_id=excluded.source_session_id,updated_at=excluded.updated_at`, in.Ref.PipelineID, stage, publishedVersions[stage], document.Content, author, sourceSessionID, formatCatalogTime(now)); err != nil {
			return catalog.PipelineRun{}, fmt.Errorf("publish approved design document: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO pipeline_transitions(id,pipeline_id,stage,action,actor,reason,created_at) VALUES(?,?,?,'approve','local_user',?,?)`, id.New(), in.Ref.PipelineID, stage, "Approved design document "+document.ContentDigest, formatCatalogTime(now)); err != nil {
			return catalog.PipelineRun{}, err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE pipeline_design_workspaces SET pipeline_revision=pipeline_revision+1,revision=revision+1,state='approved',phase='',active_attempt_id='',updated_at=? WHERE pipeline_id=? AND revision=? AND pipeline_revision=?`, formatCatalogTime(now), in.Ref.PipelineID, in.Ref.DesignRevision, in.Ref.PipelineRevision); err != nil {
		return catalog.PipelineRun{}, err
	}
	if err := appendPipelineEvent(ctx, tx, in.Ref.PipelineID, "pipeline.design.approved", map[string]any{"requestId": in.Ref.RequestID, "digests": in.Digests, "actor": "local_user", "nextStage": sdd.Code}, now); err != nil {
		return catalog.PipelineRun{}, err
	}
	run, err := readPipelineSnapshot(ctx, tx, in.Ref.PipelineID)
	if err != nil {
		return catalog.PipelineRun{}, err
	}
	if err := recordPipelineDesignCommand(ctx, tx, in.Ref, "approve", intentHash, "", run, now); err != nil {
		return catalog.PipelineRun{}, err
	}
	return run, tx.Commit()
}

// Recovery is a passive startup checkpoint. The caller provides its known live
// owners and a cutoff; it never replays inference or publishes document output.
func (s *Store) RecoverPipelineDesignAttempts(ctx context.Context, startedBefore time.Time, activeAttemptIDs []string) (int, error) {
	if startedBefore.IsZero() {
		return 0, sdd.ErrInvalidTransition
	}
	live := make(map[string]bool, len(activeAttemptIDs))
	for _, attemptID := range activeAttemptIDs {
		if !safeBrainstormText(attemptID, 128) {
			return 0, sdd.ErrInvalidTransition
		}
		live[attemptID] = true
	}
	tx, err := s.beginPipelineDesignTx(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	rows, err := tx.QueryContext(ctx, `SELECT id,pipeline_id FROM pipeline_design_attempts WHERE status IN ('running','cancellation_pending') AND created_at<? ORDER BY created_at`, formatCatalogTime(startedBefore))
	if err != nil {
		return 0, err
	}
	type owner struct{ attemptID, pipelineID string }
	owners := []owner{}
	for rows.Next() {
		var item owner
		if err := rows.Scan(&item.attemptID, &item.pipelineID); err != nil {
			_ = rows.Close()
			return 0, err
		}
		if !live[item.attemptID] {
			owners = append(owners, item)
		}
	}
	if err = rows.Err(); err != nil {
		_ = rows.Close()
		return 0, err
	}
	if err = rows.Close(); err != nil {
		return 0, err
	}
	now := time.Now().UTC()
	for _, owner := range owners {
		workspace, err := loadPipelineDesign(ctx, tx, owner.pipelineID)
		if err != nil {
			return 0, err
		}
		attempt, found := pipelineDesignAttemptByID(workspace, owner.attemptID)
		if !found || workspace.ActiveAttemptID != attempt.ID {
			return 0, ErrPipelineConflict
		}
		if err := settlePipelineDesignAttempt(ctx, tx, workspace, attempt, "interrupted", "interrupted", now); err != nil {
			return 0, err
		}
	}
	return len(owners), tx.Commit()
}
