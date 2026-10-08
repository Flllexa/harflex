package catalog

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"
)

// AuthoringCodeCopyAttempt is a durable receipt for preparing a private Code
// source copy. Snapshots contain local metadata only and never project bytes.
type AuthoringCodeCopySelection struct {
	BackendID                 string    `json:"backendId"`
	ModelID                   string    `json:"modelId"`
	ReasoningEffort           string    `json:"reasoningEffort"`
	SupportedReasoningEfforts []string  `json:"supportedReasoningEfforts"`
	CatalogRevision           string    `json:"catalogRevision"`
	Source                    string    `json:"source"`
	Destination               string    `json:"destination"`
	Status                    string    `json:"status"`
	ConfirmUnfiltered         bool      `json:"confirmUnfiltered"`
	ConfirmJITLoad            bool      `json:"confirmJitLoad"`
	MaxOutputTokens           int       `json:"maxOutputTokens"`
	ContextLength             int       `json:"contextLength"`
	CheckedAt                 time.Time `json:"checkedAt"`
}

type AuthoringCodeCopyPreferenceSnapshot struct {
	PipelineID                string                     `json:"pipelineId"`
	Stage                     string                     `json:"stage"`
	ModelMode                 string                     `json:"modelMode"`
	EffortMode                string                     `json:"effortMode"`
	ExplicitEffort            string                     `json:"explicitEffort"`
	PreferenceRevision        int64                      `json:"preferenceRevision"`
	Resolution                string                     `json:"resolution"`
	ModelSource               string                     `json:"modelSource"`
	EffortSource              string                     `json:"effortSource"`
	InheritedFrom             string                     `json:"inheritedFrom"`
	CatalogValidationRequired bool                       `json:"catalogValidationRequired"`
	SelectionHash             string                     `json:"selectionHash"`
	ErrorCode                 string                     `json:"errorCode"`
	Selection                 AuthoringCodeCopySelection `json:"selection"`
}

type authoringCodeSelectionFingerprint struct {
	PipelineID                string                     `json:"pipelineId"`
	Stage                     string                     `json:"stage"`
	ModelMode                 string                     `json:"modelMode"`
	EffortMode                string                     `json:"effortMode"`
	ExplicitEffort            string                     `json:"explicitEffort"`
	Resolution                string                     `json:"resolution"`
	ModelSource               string                     `json:"modelSource"`
	EffortSource              string                     `json:"effortSource"`
	InheritedFrom             string                     `json:"inheritedFrom"`
	CatalogValidationRequired bool                       `json:"catalogValidationRequired"`
	Selection                 AuthoringCodeCopySelection `json:"selection"`
}

// HashAuthoringCodeSelection binds confirmation to the exact safe local
// selection and its resolution provenance shown by the preflight.
func HashAuthoringCodeSelection(snapshot AuthoringCodeCopyPreferenceSnapshot) (string, error) {
	selection := snapshot.Selection
	selection.SupportedReasoningEfforts = append([]string(nil), selection.SupportedReasoningEfforts...)
	fingerprint := authoringCodeSelectionFingerprint{
		PipelineID: snapshot.PipelineID, Stage: snapshot.Stage, ModelMode: snapshot.ModelMode,
		EffortMode: snapshot.EffortMode, ExplicitEffort: snapshot.ExplicitEffort,
		Resolution: snapshot.Resolution, ModelSource: snapshot.ModelSource,
		EffortSource: snapshot.EffortSource, InheritedFrom: snapshot.InheritedFrom,
		CatalogValidationRequired: snapshot.CatalogValidationRequired, Selection: selection,
	}
	data, err := json.Marshal(fingerprint)
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:]), nil
}

type AuthoringCodeCopyEntry struct {
	Path   string `json:"path"`
	Type   string `json:"type"`
	Mode   uint32 `json:"mode"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
	Target string `json:"target,omitempty"`
}

type AuthoringCodeCopyExclusion struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

type AuthoringCodeCopyManifest struct {
	Version    int                          `json:"version"`
	Entries    []AuthoringCodeCopyEntry     `json:"entries"`
	Excluded   []AuthoringCodeCopyExclusion `json:"excluded"`
	FileCount  int                          `json:"fileCount"`
	TotalBytes int64                        `json:"totalBytes"`
	Hash       string                       `json:"hash"`
}

type AuthoringCodeCopyAttempt struct {
	ID                     string                              `json:"id"`
	PipelineID             string                              `json:"pipelineId"`
	WorkspaceID            string                              `json:"workspaceId"`
	RequestID              string                              `json:"requestId"`
	IntentHash             string                              `json:"intentHash"`
	PipelineRevision       int64                               `json:"pipelineRevision"`
	CodePreferenceRevision int64                               `json:"codePreferenceRevision"`
	SourcePath             string                              `json:"sourcePath"`
	PrivatePath            string                              `json:"privatePath"`
	ManifestHash           string                              `json:"manifestHash"`
	PreferenceSnapshot     AuthoringCodeCopyPreferenceSnapshot `json:"preferenceSnapshot"`
	ManifestSnapshot       AuthoringCodeCopyManifest           `json:"manifestSnapshot"`
	Status                 string                              `json:"status"`
	ErrorCode              string                              `json:"errorCode,omitempty"`
	CreatedAt              time.Time                           `json:"createdAt"`
	UpdatedAt              time.Time                           `json:"updatedAt"`
}
