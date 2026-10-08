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
)

// CreateDelegatedSessionWithSnapshots commits the child, its immutable inputs,
// task preview and parent relationship together. A repeated task is a no-op.
func (s *Store) CreateDelegatedSessionWithSnapshots(ctx context.Context, session catalog.SessionRecord, agent *catalog.AgentSnapshot, skills, streamKind string, link catalog.Delegation, taskPayload json.RawMessage, maxChildren int) (bool, error) {
	if agent == nil || link.TaskHash == "" || link.RequestID == "" || link.TaskPrompt == "" || len(taskPayload) == 0 || maxChildren <= 0 || (streamKind != "agent_session" && streamKind != "external_session") {
		return false, fmt.Errorf("invalid delegated session snapshot")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("begin delegation: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := upsertSession(ctx, tx, session); err != nil {
		return false, err
	}
	var existing bool
	if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM session_delegations WHERE parent_session_id=? AND request_id=?)", link.ParentSessionID, link.RequestID).Scan(&existing); err != nil {
		return false, fmt.Errorf("check delegated task: %w", err)
	}
	if existing {
		return false, nil
	}
	var children int
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM session_delegations WHERE parent_session_id=?", link.ParentSessionID).Scan(&children); err != nil {
		return false, fmt.Errorf("count delegated children: %w", err)
	}
	if children >= maxChildren {
		return false, catalog.ErrDelegationChildLimit
	}
	tools, err := json.Marshal(agent.AllowedTools)
	if err != nil {
		return false, fmt.Errorf("encode delegated tools: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO session_agent_snapshots(session_id,agent_id,instructions,allowed_tools,created_at) VALUES(?,?,?,?,?)", session.ID, agent.AgentID, agent.Instructions, tools, formatCatalogTime(agent.CreatedAt)); err != nil {
		return false, fmt.Errorf("insert delegated agent snapshot: %w", err)
	}
	if skills != "" {
		if _, err := tx.ExecContext(ctx, "INSERT INTO session_skill_snapshots(session_id,content,created_at) VALUES(?,?,?)", session.ID, skills, formatCatalogTime(session.CreatedAt)); err != nil {
			return false, fmt.Errorf("insert delegated skill snapshot: %w", err)
		}
	}
	inserted, err := tx.ExecContext(ctx, `INSERT INTO session_delegations (id,parent_session_id,child_session_id,agent_id,depth,created_at,task_prompt,task_hash,request_id)
		VALUES (?,?,?,?,?,?,?,?,?) ON CONFLICT(parent_session_id,request_id) WHERE request_id != '' DO NOTHING`, link.ID, link.ParentSessionID, link.ChildSessionID, link.AgentID, link.Depth, formatCatalogTime(link.CreatedAt), link.TaskPrompt, link.TaskHash, link.RequestID)
	if err != nil {
		return false, fmt.Errorf("insert delegation: %w", err)
	}
	rows, err := inserted.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("read delegation insertion: %w", err)
	}
	if rows == 0 {
		return false, nil
	}
	if _, err := tx.ExecContext(ctx, "INSERT OR IGNORE INTO streams (id, kind, created_at) VALUES (?, ?, ?)", link.ParentSessionID, "agent_session", formatCatalogTime(link.CreatedAt)); err != nil {
		return false, fmt.Errorf("create delegation stream: %w", err)
	}
	var sequence int64
	if err := tx.QueryRowContext(ctx, "SELECT COALESCE(MAX(sequence), 0) + 1 FROM events WHERE stream_id = ?", link.ParentSessionID).Scan(&sequence); err != nil {
		return false, fmt.Errorf("read delegation sequence: %w", err)
	}
	data, _ := json.Marshal(map[string]any{"childSessionId": link.ChildSessionID, "agentId": link.AgentID, "depth": link.Depth})
	if _, err := tx.ExecContext(ctx, "INSERT INTO events (id, stream_id, sequence, type, data, created_at) VALUES (?, ?, ?, ?, ?, ?)", id.New(), link.ParentSessionID, sequence, "subagent.delegated", data, formatCatalogTime(link.CreatedAt)); err != nil {
		return false, fmt.Errorf("record delegation: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO streams(id,kind,created_at) VALUES(?,?,?)", session.ID, streamKind, formatCatalogTime(session.CreatedAt)); err != nil {
		return false, fmt.Errorf("create delegated child stream: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO events(id,stream_id,sequence,type,data,created_at) VALUES(?,?,?,?,?,?)", id.New(), session.ID, 1, "subagent.prompt.queued", []byte(taskPayload), formatCatalogTime(session.CreatedAt)); err != nil {
		return false, fmt.Errorf("record delegated task: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit delegation: %w", err)
	}
	return true, nil
}

func (s *Store) ListDelegations(ctx context.Context, parentSessionID string) ([]catalog.Delegation, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id, parent_session_id, child_session_id, agent_id, task_prompt, task_hash, request_id, prompt_count, depth, created_at FROM session_delegations WHERE parent_session_id = ? ORDER BY created_at, id", parentSessionID)
	if err != nil {
		return nil, fmt.Errorf("list delegations: %w", err)
	}
	defer rows.Close()
	result := make([]catalog.Delegation, 0)
	for rows.Next() {
		var item catalog.Delegation
		var created string
		if err := rows.Scan(&item.ID, &item.ParentSessionID, &item.ChildSessionID, &item.AgentID, &item.TaskPrompt, &item.TaskHash, &item.RequestID, &item.PromptCount, &item.Depth, &created); err != nil {
			return nil, fmt.Errorf("scan delegation: %w", err)
		}
		item.CreatedAt, err = time.Parse(time.RFC3339Nano, created)
		if err != nil {
			return nil, fmt.Errorf("parse delegation timestamp: %w", err)
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate delegations: %w", err)
	}
	return result, nil
}

func (s *Store) GetParentDelegation(ctx context.Context, childSessionID string) (catalog.Delegation, error) {
	var item catalog.Delegation
	var created string
	if err := s.db.QueryRowContext(ctx, "SELECT id, parent_session_id, child_session_id, agent_id, task_prompt, task_hash, request_id, prompt_count, depth, created_at FROM session_delegations WHERE child_session_id = ?", childSessionID).Scan(&item.ID, &item.ParentSessionID, &item.ChildSessionID, &item.AgentID, &item.TaskPrompt, &item.TaskHash, &item.RequestID, &item.PromptCount, &item.Depth, &created); err != nil {
		return catalog.Delegation{}, fmt.Errorf("get parent delegation: %w", err)
	}
	var err error
	item.CreatedAt, err = time.Parse(time.RFC3339Nano, created)
	if err != nil {
		return catalog.Delegation{}, fmt.Errorf("parse delegation timestamp: %w", err)
	}
	return item, nil
}

func (s *Store) GetDelegationByRequestID(ctx context.Context, parentSessionID, requestID string) (catalog.Delegation, error) {
	var item catalog.Delegation
	var created string
	err := s.db.QueryRowContext(ctx, "SELECT id,parent_session_id,child_session_id,agent_id,task_prompt,task_hash,request_id,prompt_count,depth,created_at FROM session_delegations WHERE parent_session_id=? AND request_id=?", parentSessionID, requestID).Scan(&item.ID, &item.ParentSessionID, &item.ChildSessionID, &item.AgentID, &item.TaskPrompt, &item.TaskHash, &item.RequestID, &item.PromptCount, &item.Depth, &created)
	if err != nil {
		return catalog.Delegation{}, fmt.Errorf("find delegated task: %w", err)
	}
	item.CreatedAt, err = time.Parse(time.RFC3339Nano, created)
	if err != nil {
		return catalog.Delegation{}, fmt.Errorf("parse delegated timestamp: %w", err)
	}
	return item, nil
}

// ReserveDelegationPrompt counts an attempt before the runner receives it. A
// crash after reservation consumes the attempt rather than silently replaying.
func (s *Store) ReserveDelegationPrompt(ctx context.Context, childSessionID string, limit int) (int, bool, bool, error) {
	if limit <= 0 {
		return 0, false, false, fmt.Errorf("invalid delegation prompt limit")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, false, false, fmt.Errorf("begin prompt reservation: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var count int
	if err := tx.QueryRowContext(ctx, "SELECT prompt_count FROM session_delegations WHERE child_session_id=?", childSessionID).Scan(&count); err != nil {
		if err == sql.ErrNoRows {
			return 0, false, true, nil
		}
		return 0, false, false, fmt.Errorf("read prompt budget: %w", err)
	}
	if count >= limit {
		return count, true, false, nil
	}
	if _, err := tx.ExecContext(ctx, "UPDATE session_delegations SET prompt_count=prompt_count+1 WHERE child_session_id=? AND prompt_count=?", childSessionID, count); err != nil {
		return count, true, false, fmt.Errorf("reserve delegated prompt: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return count, true, false, fmt.Errorf("commit prompt reservation: %w", err)
	}
	return count + 1, true, true, nil
}

// ReleaseDelegationPrompt compensates a reservation refused before a run starts.
func (s *Store) ReleaseDelegationPrompt(ctx context.Context, childSessionID string) error {
	updated, err := s.db.ExecContext(ctx, "UPDATE session_delegations SET prompt_count=prompt_count-1 WHERE child_session_id=? AND prompt_count>0", childSessionID)
	if err != nil {
		return fmt.Errorf("release delegated prompt: %w", err)
	}
	count, err := updated.RowsAffected()
	if err != nil {
		return fmt.Errorf("read delegated prompt release: %w", err)
	}
	if count != 1 {
		return fmt.Errorf("delegated prompt reservation missing")
	}
	return nil
}

func (s *Store) GetDelegationOutcome(ctx context.Context, sessionID string) (catalog.DelegationOutcome, error) {
	var sequence int64
	var typ string
	var data []byte
	err := s.db.QueryRowContext(ctx, `SELECT sequence, type, data FROM events WHERE stream_id = ? AND type IN
		('run.started','external.run.started','approval.requested','approval.approved','approval.denied',
		'run.completed','run.failed','run.cancelled','run.interrupted',
		'external.run.completed','external.run.failed','external.run.cancelled','external.run.interrupted')
		ORDER BY sequence DESC LIMIT 1`, sessionID).Scan(&sequence, &typ, &data)
	if err == sql.ErrNoRows {
		return catalog.DelegationOutcome{Status: "ready"}, nil
	}
	if err != nil {
		return catalog.DelegationOutcome{}, fmt.Errorf("read delegated status: %w", err)
	}
	result := catalog.DelegationOutcome{Status: delegationEventStatus(typ)}
	if result.Status == "failed" {
		var terminal struct {
			Reason string `json:"reason"`
		}
		if json.Unmarshal(data, &terminal) == nil {
			switch terminal.Reason {
			case "approval_denied", "turn_limit", "execution_failed", "journal_unavailable":
				result.ErrorCode = terminal.Reason
			}
		}
	}
	if result.Status != "completed" {
		return result, nil
	}
	var runStart int64
	if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(sequence), 0) FROM events WHERE stream_id = ? AND sequence < ?
		AND type IN ('run.started','external.run.started')`, sessionID, sequence).Scan(&runStart); err != nil {
		return catalog.DelegationOutcome{}, fmt.Errorf("read delegated run start: %w", err)
	}
	var answerType string
	var answerData []byte
	err = s.db.QueryRowContext(ctx, `SELECT type, data FROM events WHERE stream_id = ? AND sequence > ? AND sequence < ?
		AND (type = 'message.assistant' OR (type = 'external.event' AND json_extract(CAST(data AS TEXT), '$.type') = 'assistant.message'))
		ORDER BY sequence DESC LIMIT 1`, sessionID, runStart, sequence).Scan(&answerType, &answerData)
	if err == sql.ErrNoRows {
		return result, nil
	}
	if err != nil {
		return catalog.DelegationOutcome{}, fmt.Errorf("read delegated answer: %w", err)
	}
	var answer struct {
		Content string `json:"content"`
		Text    string `json:"text"`
	}
	if err := json.Unmarshal(answerData, &answer); err != nil {
		return catalog.DelegationOutcome{}, fmt.Errorf("decode delegated answer: %w", err)
	}
	if answerType == "external.event" {
		result.Result = answer.Text
	} else {
		result.Result = answer.Content
	}
	result.Result = strings.TrimSpace(result.Result)
	if utf8.RuneCountInString(result.Result) > 280 {
		result.Result = string([]rune(result.Result)[:280]) + "…"
	}
	return result, nil
}

func delegationEventStatus(typ string) string {
	switch typ {
	case "run.started", "external.run.started", "approval.approved", "approval.denied":
		return "running"
	case "approval.requested":
		return "awaiting_approval"
	case "run.completed", "external.run.completed":
		return "completed"
	case "run.failed", "external.run.failed":
		return "failed"
	case "run.cancelled", "external.run.cancelled":
		return "cancelled"
	case "run.interrupted", "external.run.interrupted":
		return "paused"
	}
	return "ready"
}

func (s *Store) GetDelegationDepth(ctx context.Context, childSessionID string) (int, error) {
	var depth int
	if err := s.db.QueryRowContext(ctx, "SELECT depth FROM session_delegations WHERE child_session_id = ?", childSessionID).Scan(&depth); err != nil {
		if err == sql.ErrNoRows {
			return 0, sql.ErrNoRows
		}
		return 0, fmt.Errorf("get delegation depth: %w", err)
	}
	return depth, nil
}
