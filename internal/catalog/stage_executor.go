package catalog

import (
	"time"

	"github.com/persioflexa/harflex/internal/sdd"
)

// StageExecutor is a project's choice of who works on one phase of its pipelines. BackendID is a provider profile or
// a CLI backend; ModelID is empty when the executor's own model is meant. The phase asks the catalog about the model
// when it runs, so this keeps the choice and nothing that expires.
type StageExecutor struct {
	WorkspaceID string
	Stage       sdd.Stage
	BackendID   string
	ModelID     string
	UpdatedAt   time.Time
}
