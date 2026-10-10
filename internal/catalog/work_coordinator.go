package catalog

// WorkCoordinator is an SDD work and the chat that coordinates it.
type WorkCoordinator struct {
	SessionID    string
	PipelineID   string
	Title        string
	CurrentStage string
	StageStatus  []byte
	Previous     []byte // JSON list of the chats that coordinated the work before
	UpdatedAt    string
}
