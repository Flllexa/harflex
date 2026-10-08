package catalog

import "time"

type LocalChannel struct {
	ID          string
	WorkspaceID string
	Name        string
	Folder      string
	Status      string
	LastError   string
	InboxError  string
	OutboxError string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type ChannelMessage struct {
	ID          string
	ChannelID   string
	RequestID   string
	Direction   string
	FileName    string
	ContentHash string
	Content     string
	Status      string
	ErrorCode   string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}
