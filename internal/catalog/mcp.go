package catalog

import (
	"encoding/json"
	"time"
)

type MCPTool struct {
	Name        string          `json:"name"`
	AgentName   string          `json:"agentName"`
	Description string          `json:"description"`
	Schema      json.RawMessage `json:"schema"`
}

type MCPServer struct {
	ID                 string    `json:"id"`
	WorkspaceID        string    `json:"workspaceId"`
	Name               string    `json:"name"`
	Transport          string    `json:"transport"`
	Command            string    `json:"command"`
	Args               []string  `json:"args"`
	URL                string    `json:"url"`
	TokenEnvVar        string    `json:"tokenEnvVar"`
	AuthScheme         string    `json:"authScheme"` // "bearer" or "basic"; only HTTP servers use it
	Enabled            bool      `json:"enabled"`
	Tools              []MCPTool `json:"tools"`
	CredentialProvider string
	CredentialAccount  string
	CreatedAt          time.Time `json:"createdAt"`
	UpdatedAt          time.Time `json:"updatedAt"`
}
