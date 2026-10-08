package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/persioflexa/harflex/internal/catalog"
	"github.com/persioflexa/harflex/internal/id"
)

func (s *Store) UpsertSkill(ctx context.Context, item catalog.Skill) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO skills(id,workspace_id,name,description,content,enabled,revision,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?)
	ON CONFLICT(id) DO UPDATE SET name=excluded.name,description=excluded.description,content=excluded.content,enabled=excluded.enabled,revision=skills.revision+1,updated_at=excluded.updated_at`,
		item.ID, item.WorkspaceID, item.Name, item.Description, item.Content, item.Enabled, item.Revision, formatCatalogTime(item.CreatedAt), formatCatalogTime(item.UpdatedAt))
	if err != nil {
		return fmt.Errorf("upsert skill: %w", err)
	}
	return nil
}

const skillColumns = "id,workspace_id,name,description,content,enabled,revision,created_at,updated_at"

func scanSkill(row rowScanner) (catalog.Skill, error) {
	var item catalog.Skill
	var created, updated string
	if err := row.Scan(&item.ID, &item.WorkspaceID, &item.Name, &item.Description, &item.Content, &item.Enabled, &item.Revision, &created, &updated); err != nil {
		return catalog.Skill{}, err
	}
	var err error
	item.CreatedAt, err = time.Parse(time.RFC3339Nano, created)
	if err != nil {
		return catalog.Skill{}, fmt.Errorf("parse skill created timestamp: %w", err)
	}
	item.UpdatedAt, err = time.Parse(time.RFC3339Nano, updated)
	if err != nil {
		return catalog.Skill{}, fmt.Errorf("parse skill updated timestamp: %w", err)
	}
	return item, nil
}

func (s *Store) GetSkill(ctx context.Context, id string) (catalog.Skill, error) {
	item, err := scanSkill(s.db.QueryRowContext(ctx, "SELECT "+skillColumns+" FROM skills WHERE id=?", id))
	if err != nil {
		return catalog.Skill{}, fmt.Errorf("get skill: %w", err)
	}
	return item, nil
}

func (s *Store) ListSkills(ctx context.Context, workspaceID string) ([]catalog.Skill, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+skillColumns+" FROM skills WHERE workspace_id=? ORDER BY updated_at DESC,id", workspaceID)
	if err != nil {
		return nil, fmt.Errorf("list skills: %w", err)
	}
	defer rows.Close()
	items := make([]catalog.Skill, 0)
	for rows.Next() {
		item, err := scanSkill(rows)
		if err != nil {
			return nil, fmt.Errorf("scan skill: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate skills: %w", err)
	}
	return items, nil
}

func (s *Store) CreateSessionWithSnapshots(ctx context.Context, session catalog.SessionRecord, agent *catalog.AgentSnapshot, skills string, bypass json.RawMessage, streamKind string, selection *catalog.ModelSelection) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin session snapshot: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := createSessionWithSnapshots(ctx, tx, session, agent, skills, bypass, streamKind, selection); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit session snapshot: %w", err)
	}
	return nil
}

func createSessionWithSnapshots(ctx context.Context, tx *sql.Tx, session catalog.SessionRecord, agent *catalog.AgentSnapshot, skills string, bypass json.RawMessage, streamKind string, selection *catalog.ModelSelection) error {
	if err := upsertSession(ctx, tx, session); err != nil {
		return err
	}
	if selection != nil {
		if !validSessionModelSelection(session, *selection) {
			return fmt.Errorf("invalid session model selection")
		}
		efforts, err := json.Marshal(selection.SupportedReasoningEfforts)
		if err != nil {
			return fmt.Errorf("encode reasoning capability: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO session_model_selections(session_id,backend_id,model_id,reasoning_effort,catalog_revision,local_revision,source,executable_path,executable_version,workspace_path,checked_at,destination,credential_identity,status,confirm_unverified_manual,confirm_unfiltered,confirm_jit_load,max_output_tokens,context_length,supported_reasoning_efforts) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			selection.SessionID, selection.BackendID, selection.ModelID, selection.ReasoningEffort, selection.CatalogRevision, selection.LocalRevision, selection.Source, selection.ExecutablePath, selection.ExecutableVersion, selection.WorkspacePath, formatCatalogTime(selection.CheckedAt), selection.Destination, selection.CredentialIdentity, selection.Status, selection.ConfirmUnverifiedManual, selection.ConfirmUnfiltered, selection.ConfirmJITLoad, selection.MaxOutputTokens, selection.ContextLength, string(efforts)); err != nil {
			return fmt.Errorf("insert session model selection: %w", err)
		}
	}
	if agent != nil {
		tools, err := json.Marshal(agent.AllowedTools)
		if err != nil {
			return fmt.Errorf("encode agent tools: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO session_agent_snapshots(session_id,agent_id,instructions,allowed_tools,created_at) VALUES(?,?,?,?,?)", session.ID, agent.AgentID, agent.Instructions, tools, formatCatalogTime(agent.CreatedAt)); err != nil {
			return fmt.Errorf("insert agent snapshot: %w", err)
		}
	}
	if skills != "" {
		if _, err := tx.ExecContext(ctx, "INSERT INTO session_skill_snapshots(session_id,content,created_at) VALUES(?,?,?)", session.ID, skills, formatCatalogTime(session.CreatedAt)); err != nil {
			return fmt.Errorf("insert skill snapshot: %w", err)
		}
	}
	if len(bypass) > 0 {
		if streamKind != "agent_session" && streamKind != "external_session" {
			return fmt.Errorf("invalid bypass stream kind")
		}
		created := formatCatalogTime(session.CreatedAt)
		if _, err := tx.ExecContext(ctx, "INSERT INTO streams(id,kind,created_at) VALUES(?,?,?)", session.ID, streamKind, created); err != nil {
			return fmt.Errorf("create bypass stream: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO events(id,stream_id,sequence,type,data,created_at) VALUES(?,?,?,?,?,?)", id.New(), session.ID, 1, "sdd.bypassed", []byte(bypass), created); err != nil {
			return fmt.Errorf("record SDD bypass: %w", err)
		}
	}
	return nil
}

// cliSelectionSourceMatches pairs each CLI with the catalog source that lists its models.
func cliSelectionSourceMatches(backendID, source string) bool {
	return backendID == "codex" && source == "codex_app_server" || backendID == "opencode" && source == "opencode_cli" || backendID == "claude" && source == "claude_cli"
}

func validSessionModelSelection(session catalog.SessionRecord, selection catalog.ModelSelection) bool {
	if selection.SessionID != session.ID || selection.BackendID != session.BackendID || !safeBrainstormText(selection.ModelID, 512) || !safeBrainstormText(selection.CatalogRevision, 128) ||
		!safeBrainstormText(selection.Source, 128) || !safeBrainstormText(selection.WorkspacePath, 4096) || !filepath.IsAbs(selection.WorkspacePath) || selection.CheckedAt.IsZero() ||
		selection.MaxOutputTokens < 0 || selection.MaxOutputTokens > 32768 || selection.ContextLength < 0 {
		return false
	}
	if selection.Source == "codex_app_server" || selection.Source == "opencode_cli" || selection.Source == "claude_cli" {
		if !cliSelectionSourceMatches(selection.BackendID, selection.Source) || selection.LocalRevision == "" || !safeBrainstormText(selection.LocalRevision, 128) ||
			!safeBrainstormText(selection.ExecutablePath, 4096) || !filepath.IsAbs(selection.ExecutablePath) || !safeBrainstormText(selection.ExecutableVersion, 128) ||
			selection.Destination != "" || selection.CredentialIdentity != "" || selection.ConfirmUnverifiedManual || selection.ConfirmUnfiltered || selection.ConfirmJITLoad ||
			len(selection.ReasoningEffort) > 64 || !utf8.ValidString(selection.ReasoningEffort) || strings.IndexFunc(selection.ReasoningEffort, unicode.IsControl) >= 0 ||
			strings.ContainsAny(selection.ReasoningEffort, " =") || strings.HasPrefix(selection.ReasoningEffort, "-") || (selection.Status != "" && selection.Status != "listed") {
			return false
		}
		// Only a CLI that can work as a model alone runs the SDD modes, each with its fixed answer limit.
		documentCLI := selection.Status == "listed" && (selection.BackendID == "codex" || selection.BackendID == "claude")
		if session.Mode == "sdd_readonly" {
			return documentCLI && (selection.MaxAssistantOutputBytes == 2*1024 || selection.MaxAssistantOutputBytes == 64*1024)
		}
		if session.Mode == "sdd_code" {
			return documentCLI && selection.MaxAssistantOutputBytes == 64*1024
		}
		if session.Mode == "evaluation" && selection.MaxAssistantOutputBytes > 0 {
			return documentCLI && selection.MaxAssistantOutputBytes == 64*1024
		}
		return selection.MaxAssistantOutputBytes == 0
	}
	if selection.MaxAssistantOutputBytes != 0 || (selection.Status == "" && selection.LocalRevision == "") ||
		(selection.Status != "" && (selection.Destination == "" || selection.CredentialIdentity == "" || selection.LocalRevision != "" || selection.ExecutablePath != "" || selection.ExecutableVersion != "" || !catalog.ValidAPIReasoningEffort(selection))) ||
		(selection.Status == "listed_unfiltered" && !selection.ConfirmUnfiltered) ||
		(selection.Status == "unverified_manual" && (!selection.ConfirmUnverifiedManual || selection.MaxOutputTokens != 0 || selection.Source != "generic_manual" || selection.ReasoningEffort != "")) ||
		(selection.Status != "" && selection.Status != "listed" && selection.Status != "listed_unfiltered" && selection.Status != "unverified_manual") {
		return false
	}
	return true
}

func (s *Store) GetSessionModelSelection(ctx context.Context, sessionID string) (catalog.ModelSelection, error) {
	var item catalog.ModelSelection
	var checkedAt, efforts string
	if err := s.db.QueryRowContext(ctx, `SELECT session_id,backend_id,model_id,reasoning_effort,catalog_revision,local_revision,source,executable_path,executable_version,workspace_path,checked_at,destination,credential_identity,status,confirm_unverified_manual,confirm_unfiltered,confirm_jit_load,max_output_tokens,context_length,supported_reasoning_efforts FROM session_model_selections WHERE session_id=?`, sessionID).Scan(
		&item.SessionID, &item.BackendID, &item.ModelID, &item.ReasoningEffort, &item.CatalogRevision, &item.LocalRevision, &item.Source, &item.ExecutablePath, &item.ExecutableVersion, &item.WorkspacePath, &checkedAt, &item.Destination, &item.CredentialIdentity, &item.Status, &item.ConfirmUnverifiedManual, &item.ConfirmUnfiltered, &item.ConfirmJITLoad, &item.MaxOutputTokens, &item.ContextLength, &efforts); err != nil {
		return catalog.ModelSelection{}, fmt.Errorf("get session model selection: %w", err)
	}
	if len(efforts) > 4096 || json.Unmarshal([]byte(efforts), &item.SupportedReasoningEfforts) != nil || (item.Status != "" && !catalog.CLIModelSelection(item) && !catalog.ValidAPIReasoningEffort(item)) {
		return catalog.ModelSelection{}, fmt.Errorf("invalid stored reasoning capability")
	}
	var err error
	item.CheckedAt, err = time.Parse(time.RFC3339Nano, checkedAt)
	if err != nil {
		return catalog.ModelSelection{}, fmt.Errorf("parse session model selection timestamp: %w", err)
	}
	return item, nil
}

func (s *Store) GetSessionSkillSnapshot(ctx context.Context, sessionID string) (string, error) {
	var content string
	if err := s.db.QueryRowContext(ctx, "SELECT content FROM session_skill_snapshots WHERE session_id=?", sessionID).Scan(&content); err != nil {
		if err == sql.ErrNoRows {
			return "", sql.ErrNoRows
		}
		return "", fmt.Errorf("get skill snapshot: %w", err)
	}
	return content, nil
}
