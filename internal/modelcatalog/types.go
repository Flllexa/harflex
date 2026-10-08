package modelcatalog

import (
	"context"
	"errors"
	"time"
)

type Status string

const (
	StatusComplete    Status = "complete"
	StatusEmpty       Status = "empty"
	StatusPartial     Status = "partial"
	StatusInterrupted Status = "interrupted"
	StatusUnsupported Status = "unsupported"
	StatusFailed      Status = "failed"
)

const (
	MaxBytes  int64 = 8 << 20
	MaxModels       = 1000
	MaxPages        = 20
)

var (
	ErrUnsupported  = errors.New("catalog endpoint unsupported")
	ErrUnauthorized = errors.New("catalog credential refused")
	ErrLimit        = errors.New("catalog budget exceeded")
	ErrUnavailable  = errors.New("catalog unavailable")
)

type Model struct {
	ID                        string   `json:"id"`
	DisplayName               string   `json:"displayName"`
	BackendID                 string   `json:"backendId"`
	Source                    string   `json:"source"`
	Availability              string   `json:"availability"`
	Loaded                    *bool    `json:"loaded,omitempty"`
	OwnedBy                   string   `json:"ownedBy,omitempty"`
	ContextLength             int      `json:"contextLength,omitempty"`
	SupportedReasoningEfforts []string `json:"supportedReasoningEfforts,omitempty"`
	DefaultReasoningEffort    string   `json:"defaultReasoningEffort,omitempty"`
}

type Page struct {
	Models          []Model
	NextCursor      string
	Bytes           int64
	Source          string
	AccountFiltered bool
}

type Result struct {
	BackendID          string    `json:"backendId"`
	Source             string    `json:"source"`
	Destination        string    `json:"destination"`
	ProfileRevision    string    `json:"profileRevision"`
	CredentialToken    string    `json:"credentialToken,omitempty"`
	CredentialIdentity string    `json:"-"`
	LocalRevision      string    `json:"-"`
	SearchTerm         string    `json:"searchTerm"`
	Models             []Model   `json:"models"`
	NextCursor         string    `json:"nextCursor"`
	CheckedAt          time.Time `json:"checkedAt"`
	Status             Status    `json:"status"`
	Complete           bool      `json:"complete"`
	AccountFiltered    bool      `json:"accountFiltered"`
	ErrorCode          string    `json:"errorCode,omitempty"`
}

type FetchPage func(context.Context, string, int64) (Page, error)
