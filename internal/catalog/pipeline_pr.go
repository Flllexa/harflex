package catalog

import "time"

// PipelinePRSession links a pipeline to a conversation in which the agent opens its pull requests.
type PipelinePRSession struct {
	ID         string
	PipelineID string
	SessionID  string
	CreatedAt  time.Time
}

// PipelinePRsFinish ends the pull request stage of a pipeline: with the agent's report as its artifact, or skipped.
type PipelinePRsFinish struct {
	PipelineID       string
	ExpectedRevision int64
	// Outcome is "completed" or "skipped".
	Outcome string
	// SessionID is the linked conversation whose final answer is Report (completed only).
	SessionID string
	Report    string
	// Reason is why the stage was skipped.
	Reason string
}

// PipelinePullRequest is a pull request the PRs conversation opened, with its review watch.
type PipelinePullRequest struct {
	ID            string
	PipelineID    string
	SessionID     string
	URL           string
	Title         string
	Branch        string
	State         string // open, merged, closed
	Watch         bool
	LastCheckedAt time.Time
	Timeline      []PullRequestEvent
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// PullRequestEvent is one line of a pull request's timeline: opened, checked, fixed, merged…
type PullRequestEvent struct {
	At      time.Time `json:"at"`
	Kind    string    `json:"kind"`
	Summary string    `json:"summary"`
}
