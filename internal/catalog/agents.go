package catalog

import (
	"errors"
	"time"
)

var ErrDelegationChildLimit = errors.New("delegated child limit reached")

type Agent struct {
	ID           string
	Name         string
	Description  string
	Instructions string
	BackendID    string
	AllowedTools []string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type AgentSnapshot struct {
	SessionID    string
	AgentID      string
	Instructions string
	AllowedTools []string
	CreatedAt    time.Time
}

type Delegation struct {
	ID              string
	ParentSessionID string
	ChildSessionID  string
	AgentID         string
	TaskPrompt      string
	TaskHash        string
	RequestID       string
	PromptCount     int
	Depth           int
	CreatedAt       time.Time
}

type DelegationOutcome struct {
	Status    string
	Result    string
	ErrorCode string
}
