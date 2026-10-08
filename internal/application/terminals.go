package application

import (
	"errors"
	"strings"

	"github.com/persioflexa/harflex/internal/terminal"
)

// TerminalEvent is the event that carries a terminal's output to the panel.
const TerminalEvent = "harflex:terminal"

type StartTerminalInput struct {
	WorkspaceID string `json:"workspaceId"`
	Cols        int    `json:"cols"`
	Rows        int    `json:"rows"`
}

type TerminalDTO struct {
	ID    string `json:"id"`
	Shell string `json:"shell"`
	Path  string `json:"path"`
}

type WriteTerminalInput struct {
	ID   string `json:"id"`
	Data string `json:"data"`
}

type ResizeTerminalInput struct {
	ID   string `json:"id"`
	Cols int    `json:"cols"`
	Rows int    `json:"rows"`
}

var ErrTerminalNotFound = errors.New("terminal not found")

func (s *Service) terminalManager() *terminal.Manager {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.terminals == nil {
		s.terminals = terminal.NewManager(func(out terminal.Output) {
			s.mu.RLock()
			emit := s.emit
			s.mu.RUnlock()
			if emit != nil {
				emit(TerminalEvent, out)
			}
		})
	}
	return s.terminals
}

func terminalError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, terminal.ErrNotFound):
		return ErrTerminalNotFound
	case errors.Is(err, terminal.ErrUnsupported):
		return err
	}
	return safe("terminal", err)
}

// StartTerminal opens the person's shell in the project folder, for the terminal panel.
func (s *Service) StartTerminal(in StartTerminalInput) (TerminalDTO, error) {
	if err := s.beginCall(); err != nil {
		return TerminalDTO{}, err
	}
	defer s.endCall()
	workspace, err := s.knowledgeWorkspace(in.WorkspaceID)
	if err != nil {
		return TerminalDTO{}, err
	}
	id, err := s.terminalManager().Start(workspace.Path, in.Cols, in.Rows)
	if err != nil {
		return TerminalDTO{}, terminalError(err)
	}
	return TerminalDTO{ID: id, Shell: terminal.Shell(), Path: workspace.Path}, nil
}

// WriteTerminal types into a terminal.
func (s *Service) WriteTerminal(in WriteTerminalInput) error {
	if err := s.beginCall(); err != nil {
		return err
	}
	defer s.endCall()
	if strings.TrimSpace(in.ID) == "" || len(in.Data) > 64*1024 {
		return ErrInvalidInput
	}
	return terminalError(s.terminalManager().Write(in.ID, in.Data))
}

// ResizeTerminal follows the panel's size.
func (s *Service) ResizeTerminal(in ResizeTerminalInput) error {
	if err := s.beginCall(); err != nil {
		return err
	}
	defer s.endCall()
	return terminalError(s.terminalManager().Resize(in.ID, in.Cols, in.Rows))
}

// CloseTerminal ends a terminal's shell.
func (s *Service) CloseTerminal(id string) error {
	if err := s.beginCall(); err != nil {
		return err
	}
	defer s.endCall()
	return terminalError(s.terminalManager().Close(id))
}
