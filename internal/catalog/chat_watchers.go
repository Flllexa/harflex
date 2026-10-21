package catalog

import "time"

// ChatWatcher is a check Harflex runs in the background for a conversation: a command, every IntervalSeconds, until
// it exits 0, or (without a command) a timer at NextCheckAt. When it ends it brings the conversation back.
type ChatWatcher struct {
	ID              string
	SessionID       string
	WorkspaceID     string
	Title           string
	Command         string
	IntervalSeconds int
	DeadlineAt      time.Time
	// Status is watching, fired (the check passed or the timer rang), expired, failed or stopped.
	Status       string
	Reason       string
	LastOutput   string
	LastExitCode int
	Checks       int
	Failures     int
	LastCheckAt  time.Time
	NextCheckAt  time.Time
	// Delivered says the message that brings the conversation back was sent (or is not due: stopped).
	Delivered bool
	CreatedAt time.Time
	EndedAt   time.Time
}
