package catalog

import "time"

type KnowledgeDocument struct {
	ID               string
	WorkspaceID      string
	Path             string
	ContentHash      string
	SourceSize       int64
	SourceModifiedAt time.Time
	IndexedAt        time.Time
	ChunkCount       int
}

type KnowledgeHit struct {
	DocumentID       string
	Ordinal          int
	Path             string
	LineStart        int
	Snippet          string
	Score            float64
	SourceModifiedAt time.Time
	IndexedAt        time.Time
}
