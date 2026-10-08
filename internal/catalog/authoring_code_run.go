package catalog

import "time"

// AuthoringCodeRun is an immutable admission snapshot plus append-only terminal
// evidence for one API-only Code execution in a confirmed private copy.
type AuthoringCodeRun struct {
	ID                     string
	PipelineID             string
	PreparationID          string
	PreparationRequestID   string
	RequestID              string
	IntentHash             string
	WorkspaceID            string
	PipelineRevision       int64
	PlanStageRevision      int64
	PlanArtifactVersion    int64
	CodePreferenceRevision int64
	CodeSelectionHash      string
	ManifestHash           string
	PlanSourceHash         string
	PrivatePath            string
	PreferenceSnapshot     AuthoringCodeCopyPreferenceSnapshot
	ManifestSnapshot       AuthoringCodeCopyManifest
	MaxPromptBytes         int
	MaxOutputTokens        int
	MaxOutputTokensPerTurn int
	MaxTurns               int
	MaxToolCalls           int
	TimeoutMillis          int64
	SessionID              string
	Status                 string
	ErrorCode              string
	Usage                  *BrainstormUsage
	ResultManifest         *AuthoringCodeCopyManifest
	SourceManifest         *AuthoringCodeCopyManifest
	Changes                []CodeFileChange
	PatchJSON              []byte
	PatchHash              string
	CreatedAt              time.Time
	UpdatedAt              time.Time
}

type CodeFileChange struct {
	Path string `json:"path"`
	Kind string `json:"kind"`
}
