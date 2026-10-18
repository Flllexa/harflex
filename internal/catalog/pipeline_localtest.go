package catalog

import (
	"encoding/json"
	"errors"
	"time"
)

// ErrLocalTestBusy is a second local test in a project that already has one applied.
var ErrLocalTestBusy = errors.New("another local test is applied in this project")

// LocalTestBusyError names the pipeline whose test holds the project.
type LocalTestBusyError struct{ PipelineID string }

func (e *LocalTestBusyError) Error() string        { return ErrLocalTestBusy.Error() }
func (e *LocalTestBusyError) Is(target error) bool { return target == ErrLocalTestBusy }

// PipelineLocalTest is the Code of a pipeline applied to the project's own folder for the person to try before
// approving it. Applied holds what undo needs (repositories.LocalTestApply), BackupPath where the originals are.
type PipelineLocalTest struct {
	ID              string
	PipelineID      string
	WorkspaceID     string
	ArtifactVersion int
	SessionID       string
	BackupPath      string
	Applied         json.RawMessage
	Services        []LocalTestService
	// Status is "applied" or "undone".
	Status    string
	CreatedAt time.Time
	UndoneAt  time.Time
}

// LocalTestService is a process the tester started in a named terminal.
type LocalTestService struct {
	Name       string `json:"name"`
	TerminalID string `json:"terminalId"`
	Command    string `json:"command"`
}
