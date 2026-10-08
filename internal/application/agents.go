package application

import (
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/persioflexa/harflex/internal/catalog"
	"github.com/persioflexa/harflex/internal/id"
)

var ErrAgentNotFound = errors.New("agent not found")

var knownAgentTools = map[string]bool{"read": true, "write": true, "edit": true, "bash": true, "powershell": true, "grep": true, "find": true, "ls": true, "knowledge_search": true}

func agentDTO(item catalog.Agent) AgentDTO {
	return AgentDTO{ID: item.ID, Name: item.Name, Description: item.Description, Instructions: item.Instructions, BackendID: item.BackendID, AllowedTools: append([]string(nil), item.AllowedTools...), CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt}
}

func (s *Service) SaveAgent(in SaveAgentInput) (AgentDTO, error) {
	if err := s.beginCall(); err != nil {
		return AgentDTO{}, err
	}
	defer s.endCall()
	in.Name, in.Description, in.Instructions = strings.TrimSpace(in.Name), strings.TrimSpace(in.Description), strings.TrimSpace(in.Instructions)
	if len(in.Name) == 0 || len(in.Name) > 128 || len(in.Description) > 500 || len(in.Instructions) == 0 || len(in.Instructions) > 64*1024 || in.BackendID == "" || len(in.AllowedTools) > len(knownAgentTools)+256 {
		return AgentDTO{}, ErrInvalidInput
	}
	seen := make(map[string]bool, len(in.AllowedTools))
	for _, name := range in.AllowedTools {
		mcpToolName := strings.HasPrefix(name, "mcp_") && len(name) <= 57 && profileID.MatchString(name)
		if (!knownAgentTools[name] && !mcpToolName) || seen[name] {
			return AgentDTO{}, ErrInvalidInput
		}
		seen[name] = true
	}
	if _, external := s.external[in.BackendID]; !external {
		if _, err := s.store.GetProviderProfile(s.ctx, in.BackendID); errors.Is(err, sql.ErrNoRows) {
			return AgentDTO{}, ErrBackendNotFound
		} else if err != nil {
			return AgentDTO{}, safe("validate agent backend", err)
		}
	}
	now := time.Now().UTC()
	created := now
	if in.ID != "" {
		previous, err := s.store.GetAgent(s.ctx, in.ID)
		if errors.Is(err, sql.ErrNoRows) {
			return AgentDTO{}, ErrAgentNotFound
		}
		if err != nil {
			return AgentDTO{}, safe("get agent", err)
		}
		created = previous.CreatedAt
	} else {
		in.ID = id.New()
	}
	agent := catalog.Agent{ID: in.ID, Name: in.Name, Description: in.Description, Instructions: in.Instructions, BackendID: in.BackendID, AllowedTools: append([]string(nil), in.AllowedTools...), CreatedAt: created, UpdatedAt: now}
	if err := s.store.UpsertAgent(s.ctx, agent); err != nil {
		return AgentDTO{}, safe("save agent", err)
	}
	return agentDTO(agent), nil
}

func (s *Service) ListAgents() ([]AgentDTO, error) {
	if err := s.beginCall(); err != nil {
		return nil, err
	}
	defer s.endCall()
	items, err := s.store.ListAgents(s.ctx)
	if err != nil {
		return nil, safe("list agents", err)
	}
	result := make([]AgentDTO, 0, len(items))
	for _, item := range items {
		result = append(result, agentDTO(item))
	}
	return result, nil
}
