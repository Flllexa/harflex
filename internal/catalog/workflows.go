package catalog

import "time"

type WorkflowStep struct {
	Name   string `json:"name"`
	Prompt string `json:"prompt"`
}

type Workflow struct {
	ID          string         `json:"id"`
	WorkspaceID string         `json:"workspaceId"`
	Name        string         `json:"name"`
	Steps       []WorkflowStep `json:"steps"`
	Revision    int64          `json:"revision"`
	CreatedAt   time.Time      `json:"createdAt"`
	UpdatedAt   time.Time      `json:"updatedAt"`
}

type WorkflowRun struct {
	ID            string         `json:"id"`
	WorkflowID    string         `json:"workflowId"`
	WorkspaceID   string         `json:"workspaceId"`
	BackendID     string         `json:"backendId"`
	Steps         []WorkflowStep `json:"steps"`
	CurrentStep   int            `json:"currentStep"`
	Status        string         `json:"status"`
	LastSessionID string         `json:"lastSessionId"`
	CreatedAt     time.Time      `json:"createdAt"`
	UpdatedAt     time.Time      `json:"updatedAt"`
}
