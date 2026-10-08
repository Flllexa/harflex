package catalog

import "time"

// MaxProjectMemoryBytes bounds the memory a project keeps, so it always fits beside the documents of a phase.
const MaxProjectMemoryBytes = 64 * 1024

// ProjectMemory is what the Harflex knows about a project: technologies, domains, repositories and services, written
// by the AI from the project's own files (Sources) or edited by the person (Edited).
type ProjectMemory struct {
	WorkspaceID string
	// Status is reading, ready, failed or needs_model (no AI was chosen to read the project).
	Status    string
	Content   string
	Sources   []string
	BackendID string
	ModelID   string
	ErrorCode string
	Edited    bool
	UpdatedAt time.Time
}
