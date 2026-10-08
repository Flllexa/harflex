package sqlite

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/persioflexa/harflex/internal/catalog"
)

func (s *Store) UpsertAgent(ctx context.Context, agent catalog.Agent) error {
	tools, err := json.Marshal(agent.AllowedTools)
	if err != nil {
		return fmt.Errorf("encode agent tools: %w", err)
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO agents (id, name, description, instructions, backend_id, allowed_tools, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET name=excluded.name, description=excluded.description, instructions=excluded.instructions, backend_id=excluded.backend_id, allowed_tools=excluded.allowed_tools, updated_at=excluded.updated_at`,
		agent.ID, agent.Name, agent.Description, agent.Instructions, agent.BackendID, tools, formatCatalogTime(agent.CreatedAt), formatCatalogTime(agent.UpdatedAt))
	if err != nil {
		return fmt.Errorf("upsert agent: %w", err)
	}
	return nil
}

type agentScanner interface{ Scan(...any) error }

func scanAgent(row agentScanner) (catalog.Agent, error) {
	var item catalog.Agent
	var tools []byte
	var created, updated string
	if err := row.Scan(&item.ID, &item.Name, &item.Description, &item.Instructions, &item.BackendID, &tools, &created, &updated); err != nil {
		return catalog.Agent{}, err
	}
	if err := json.Unmarshal(tools, &item.AllowedTools); err != nil {
		return catalog.Agent{}, fmt.Errorf("decode agent tools: %w", err)
	}
	var err error
	item.CreatedAt, err = time.Parse(time.RFC3339Nano, created)
	if err != nil {
		return catalog.Agent{}, fmt.Errorf("parse agent created timestamp: %w", err)
	}
	item.UpdatedAt, err = time.Parse(time.RFC3339Nano, updated)
	if err != nil {
		return catalog.Agent{}, fmt.Errorf("parse agent updated timestamp: %w", err)
	}
	return item, nil
}

const agentColumns = "id, name, description, instructions, backend_id, allowed_tools, created_at, updated_at"

func (s *Store) GetAgent(ctx context.Context, id string) (catalog.Agent, error) {
	item, err := scanAgent(s.db.QueryRowContext(ctx, "SELECT "+agentColumns+" FROM agents WHERE id = ?", id))
	if err != nil {
		return catalog.Agent{}, fmt.Errorf("get agent: %w", err)
	}
	return item, nil
}

func (s *Store) ListAgents(ctx context.Context) ([]catalog.Agent, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+agentColumns+" FROM agents ORDER BY updated_at DESC, id")
	if err != nil {
		return nil, fmt.Errorf("list agents: %w", err)
	}
	defer rows.Close()
	result := make([]catalog.Agent, 0)
	for rows.Next() {
		item, err := scanAgent(rows)
		if err != nil {
			return nil, fmt.Errorf("scan agent: %w", err)
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate agents: %w", err)
	}
	return result, nil
}

func (s *Store) GetSessionAgentSnapshot(ctx context.Context, sessionID string) (catalog.AgentSnapshot, error) {
	var value catalog.AgentSnapshot
	var tools []byte
	var created string
	if err := s.db.QueryRowContext(ctx, "SELECT session_id, agent_id, instructions, allowed_tools, created_at FROM session_agent_snapshots WHERE session_id = ?", sessionID).Scan(&value.SessionID, &value.AgentID, &value.Instructions, &tools, &created); err != nil {
		return catalog.AgentSnapshot{}, fmt.Errorf("get session agent snapshot: %w", err)
	}
	if err := json.Unmarshal(tools, &value.AllowedTools); err != nil {
		return catalog.AgentSnapshot{}, fmt.Errorf("decode session agent tools: %w", err)
	}
	var err error
	value.CreatedAt, err = time.Parse(time.RFC3339Nano, created)
	if err != nil {
		return catalog.AgentSnapshot{}, fmt.Errorf("parse agent snapshot timestamp: %w", err)
	}
	return value, nil
}

func (s *Store) UpsertSessionWithAgentSnapshot(ctx context.Context, session catalog.SessionRecord, snapshot catalog.AgentSnapshot) error {
	tools, err := json.Marshal(snapshot.AllowedTools)
	if err != nil {
		return fmt.Errorf("encode session agent tools: %w", err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin agent session: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := upsertSession(ctx, tx, session); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO session_agent_snapshots (session_id, agent_id, instructions, allowed_tools, created_at) VALUES (?, ?, ?, ?, ?)", session.ID, snapshot.AgentID, snapshot.Instructions, tools, formatCatalogTime(snapshot.CreatedAt)); err != nil {
		return fmt.Errorf("insert agent snapshot: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit agent session: %w", err)
	}
	return nil
}
