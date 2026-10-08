package application

import (
	"database/sql"
	"errors"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strings"
)

const defaultChatsPerProject = 8

// ChatProjectDTO is one project of the Casual sidebar with its most recent conversations. Total counts the project's
// conversations, so the sidebar can offer the rest; pinned ones are always included.
type ChatProjectDTO struct {
	Workspace WorkspaceSummaryDTO `json:"workspace"`
	Chats     []SessionDTO        `json:"chats"`
	Total     int                 `json:"total"`
}

type ListChatProjectsInput struct {
	// PerProject is how many recent conversations each project brings; zero means the default.
	PerProject int `json:"perProject"`
}

type SetSessionPinnedInput struct {
	SessionID string `json:"sessionId"`
	Pinned    bool   `json:"pinned"`
}

var internalSessionModes = map[string]bool{"sdd_readonly": true, "sdd_code": true, "evaluation": true}

// ListChatProjects returns every active project (archived ones are left out) with its recent conversations, most
// recently opened project first.
func (s *Service) ListChatProjects(in ListChatProjectsInput) ([]ChatProjectDTO, error) {
	if err := s.beginCall(); err != nil {
		return nil, err
	}
	defer s.endCall()
	per := in.PerProject
	if per <= 0 || per > 50 {
		per = defaultChatsPerProject
	}
	workspaces, err := s.store.ListWorkspaces(s.ctx)
	if err != nil {
		return nil, safe("list workspaces", err)
	}
	pins, err := s.store.ListSessionPins(s.ctx)
	if err != nil {
		return nil, safe("list pinned chats", err)
	}
	out := make([]ChatProjectDTO, 0, len(workspaces))
	for _, workspace := range workspaces {
		if workspace.Archived {
			continue
		}
		records, err := s.store.ListSessions(s.ctx, workspace.ID)
		if err != nil {
			return nil, safe("list sessions", err)
		}
		sort.SliceStable(records, func(i, j int) bool { return records[i].UpdatedAt.After(records[j].UpdatedAt) })
		project := ChatProjectDTO{Workspace: workspaceSummary(workspace.ID, workspace.Path, workspace.Profile, false), Chats: []SessionDTO{}}
		cache := make(map[string]cliContinuationSnapshot)
		for _, record := range records {
			if record.WorkspaceID != workspace.ID || internalSessionModes[record.Mode] || strings.HasPrefix(record.Mode, "sdd_") {
				continue
			}
			_, pinned := pins[record.ID]
			if len(project.Chats) >= per && !pinned {
				project.Total++
				continue
			}
			dto := s.sessionDTOCached(record, cache)
			if dto.Purpose != "chat" {
				continue
			}
			project.Total++
			if dto.Title == "" {
				dto.Title = s.sessionTitle(dto.ID)
			}
			dto.Pinned = pinned
			project.Chats = append(project.Chats, dto)
		}
		out = append(out, project)
	}
	return out, nil
}

// SetSessionPinned pins a conversation to the top of the Casual sidebar, or takes the pin off.
func (s *Service) SetSessionPinned(in SetSessionPinnedInput) error {
	if err := s.beginCall(); err != nil {
		return err
	}
	defer s.endCall()
	if strings.TrimSpace(in.SessionID) == "" {
		return ErrInvalidInput
	}
	if _, err := s.store.GetSession(s.ctx, in.SessionID); errors.Is(err, sql.ErrNoRows) {
		return ErrSessionNotFound
	} else if err != nil {
		return safe("get session", err)
	}
	if err := s.store.SetSessionPinned(s.ctx, in.SessionID, in.Pinned); err != nil {
		return safe("pin session", err)
	}
	return nil
}

// RevealWorkspace opens the project's folder in the system file manager.
func (s *Service) RevealWorkspace(workspaceID string) error {
	if err := s.beginCall(); err != nil {
		return err
	}
	defer s.endCall()
	workspace, err := s.knowledgeWorkspace(workspaceID)
	if err != nil {
		return err
	}
	if info, err := os.Stat(workspace.Path); err != nil || !info.IsDir() {
		return ErrInvalidInput
	}
	name, args := "xdg-open", []string{workspace.Path}
	switch runtime.GOOS {
	case "darwin":
		name = "open"
	case "windows":
		name = "explorer"
	}
	cmd := exec.Command(name, args...)
	if err := cmd.Start(); err != nil {
		return safe("open the project folder", err)
	}
	go func() { _ = cmd.Wait() }()
	return nil
}
