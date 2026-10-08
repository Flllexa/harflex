package catalog

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/persioflexa/harflex/internal/sdd"
)

const MaxPipelineDesignDocumentBytes = 64 * 1024
const MaxPipelineDesignMessageBytes = 16 * 1024

// PipelineDesignRef binds an explicit command to both aggregate revisions.
type PipelineDesignRef struct {
	PipelineID       string `json:"pipelineId"`
	RequestID        string `json:"requestId"`
	PipelineRevision int64  `json:"pipelineRevision"`
	DesignRevision   int64  `json:"designRevision"`
}

type PipelineDesignDocument struct {
	Stage           sdd.Stage      `json:"stage"`
	Version         int64          `json:"version"`
	Content         string         `json:"content"`
	ContentDigest   string         `json:"contentDigest"`
	Author          string         `json:"author"`
	SourceSessionID string         `json:"sourceSessionId"`
	SourceDigest    string         `json:"sourceDigest"`
	Selection       ModelSelection `json:"selection"`
	AttemptID       string         `json:"attemptId"`
	Stale           bool           `json:"stale"`
	UpdatedAt       time.Time      `json:"updatedAt"`
}

type PipelineDesignDocumentVersion struct {
	PipelineDesignDocument
	Reason              string    `json:"reason"`
	RestoredFromVersion int64     `json:"restoredFromVersion"`
	CreatedAt           time.Time `json:"createdAt"`
}

type PipelineDesignMessage struct {
	ID        string    `json:"id"`
	Role      string    `json:"role"`
	Content   string    `json:"content"`
	Target    string    `json:"target"`
	AttemptID string    `json:"attemptId"`
	CreatedAt time.Time `json:"createdAt"`
}

type PipelineDesignAttempt struct {
	ID                     string                               `json:"id"`
	PipelineID             string                               `json:"pipelineId"`
	RequestID              string                               `json:"requestId"`
	IntentHash             string                               `json:"intentHash"`
	OwnerPID               int                                  `json:"-"`
	Target                 string                               `json:"target"`
	Status                 string                               `json:"status"`
	Phase                  sdd.Stage                            `json:"phase"`
	SourceRevision         int64                                `json:"sourceRevision"`
	SourcePipelineRevision int64                                `json:"sourcePipelineRevision"`
	InputDocuments         map[sdd.Stage]PipelineDesignDocument `json:"inputDocuments"`
	Selections             map[sdd.Stage]ModelSelection         `json:"selections"`
	SessionIDs             map[sdd.Stage]string                 `json:"sessionIds"`
	ErrorCode              string                               `json:"errorCode"`
	ResultHash             string                               `json:"resultHash"`
	CreatedAt              time.Time                            `json:"createdAt"`
	UpdatedAt              time.Time                            `json:"updatedAt"`
}

type PipelineDesignWorkspace struct {
	PipelineID              string                                        `json:"pipelineId"`
	WorkspaceID             string                                        `json:"workspaceId"`
	PipelineRevision        int64                                         `json:"pipelineRevision"`
	CurrentPipelineRevision int64                                         `json:"currentPipelineRevision"`
	Revision                int64                                         `json:"revision"`
	State                   string                                        `json:"state"`
	Phase                   sdd.Stage                                     `json:"phase"`
	ActiveAttemptID         string                                        `json:"activeAttemptId"`
	NeedsDerivation         bool                                          `json:"needsDerivation"`
	Documents               map[sdd.Stage]PipelineDesignDocument          `json:"documents"`
	Versions                map[sdd.Stage][]PipelineDesignDocumentVersion `json:"versions"`
	Messages                []PipelineDesignMessage                       `json:"messages"`
	Attempts                []PipelineDesignAttempt                       `json:"attempts"`
	CreatedAt               time.Time                                     `json:"createdAt"`
	UpdatedAt               time.Time                                     `json:"updatedAt"`
}

// DocumentInput is used only at import and generation boundaries. Store verifies
// AI session and selection provenance against the durable attempt before writing.
type PipelineDesignDocumentInput struct {
	Content         string         `json:"content"`
	Author          string         `json:"author"`
	SourceSessionID string         `json:"sourceSessionId"`
	SourceDigest    string         `json:"sourceDigest"`
	Selection       ModelSelection `json:"selection"`
}

type PipelineDesignAttemptRequest struct {
	Ref        PipelineDesignRef            `json:"ref"`
	Message    string                       `json:"message"`
	Target     string                       `json:"target"`
	IntentHash string                       `json:"intentHash"`
	OwnerPID   int                          `json:"-"`
	Selections map[sdd.Stage]ModelSelection `json:"selections"`
}

type PipelineDesignCompleteRequest struct {
	PipelineID     string                                    `json:"pipelineId"`
	AttemptID      string                                    `json:"attemptId"`
	SourceRevision int64                                     `json:"sourceRevision"`
	Documents      map[sdd.Stage]PipelineDesignDocumentInput `json:"documents"`
	Summary        string                                    `json:"summary"`
}

type PipelineDesignFailRequest struct {
	PipelineID string `json:"pipelineId"`
	AttemptID  string `json:"attemptId"`
	Status     string `json:"status"`
	ErrorCode  string `json:"errorCode"`
}

type PipelineDesignEditRequest struct {
	Ref     PipelineDesignRef `json:"ref"`
	Stage   sdd.Stage         `json:"stage"`
	Content string            `json:"content"`
}

type PipelineDesignRestoreRequest struct {
	Ref     PipelineDesignRef `json:"ref"`
	Stage   sdd.Stage         `json:"stage"`
	Version int64             `json:"version"`
}

type PipelineDesignApproveRequest struct {
	Ref     PipelineDesignRef    `json:"ref"`
	Digests map[sdd.Stage]string `json:"digests"`
}

// Hash excludes transient execution snapshots so the application can consult
// durable intent before reloading a catalog or credentials on a retry.
func (in PipelineDesignAttemptRequest) Hash() (string, error) {
	return pipelineDesignHash(struct {
		Ref     PipelineDesignRef
		Message string
		Target  string
	}{in.Ref, in.Message, in.Target})
}

func (in PipelineDesignEditRequest) Hash() (string, error)    { return pipelineDesignHash(in) }
func (in PipelineDesignRestoreRequest) Hash() (string, error) { return pipelineDesignHash(in) }
func (in PipelineDesignApproveRequest) Hash() (string, error) { return pipelineDesignHash(in) }

func pipelineDesignHash(value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func PipelineDesignContentDigest(content string) string {
	digest := sha256.Sum256([]byte(content))
	return hex.EncodeToString(digest[:])
}

// SourceDigest is independent of local versions: it records exact upstream text.
func PipelineDesignSourceDigest(stage sdd.Stage, documents map[sdd.Stage]PipelineDesignDocument) string {
	if stage == sdd.Discovery {
		return ""
	}
	sources := []string{documents[sdd.Discovery].ContentDigest}
	if stage == sdd.Plan {
		sources = append(sources, documents[sdd.Spec].ContentDigest)
	}
	digest, _ := pipelineDesignHash(sources)
	return digest
}
