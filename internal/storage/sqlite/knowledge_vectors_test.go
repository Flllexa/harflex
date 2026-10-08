package sqlite_test

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/persioflexa/harflex/internal/catalog"
	"github.com/persioflexa/harflex/internal/embeddings"
	"github.com/persioflexa/harflex/internal/knowledge"
)

func TestKnowledgeVectorsResumeAndStayScopedToProfileAndSource(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vectors.db")
	store := openStore(t, path)
	ctx := t.Context()
	workspace := catalog.Workspace{ID: "workspace-1", Path: t.TempDir(), CreatedAt: time.Now().UTC()}
	if err := store.UpsertWorkspace(ctx, workspace); err != nil {
		t.Fatal(err)
	}
	stamp := time.Now().UTC()
	document := catalog.KnowledgeDocument{ID: "document-1", WorkspaceID: workspace.ID, Path: "source.md", ContentHash: "source-v1", SourceSize: 20, SourceModifiedAt: stamp, IndexedAt: stamp, ChunkCount: 2}
	chunks := []knowledge.Chunk{{Ordinal: 0, LineStart: 1, Content: "first chunk"}, {Ordinal: 1, LineStart: 2, Content: "second chunk"}}
	if _, err := store.ReplaceKnowledge(ctx, document, chunks); err != nil {
		t.Fatal(err)
	}
	profile, _ := embeddings.ValidateProfile(embeddings.Profile{Kind: embeddings.Ollama, BaseURL: "http://127.0.0.1:11434", Model: "model-a"})
	if err := store.SaveEmbeddingProfile(ctx, workspace.ID, profile); err != nil {
		t.Fatal(err)
	}
	pending, err := store.PendingVectorChunks(ctx, workspace.ID, profile.Fingerprint, 8)
	if err != nil || len(pending) != 2 {
		t.Fatalf("pending = %+v, %v", pending, err)
	}
	if err := store.SaveVectorBatch(ctx, workspace.ID, profile.Fingerprint, []knowledge.VectorWrite{{DocumentID: document.ID, Ordinal: 0, ContentHash: pending[0].ContentHash, Values: []float64{1, 0}}}); err != nil {
		t.Fatal(err)
	}
	progress, err := store.VectorProgress(ctx, workspace.ID, profile.Fingerprint)
	if err != nil || progress.Indexed != 1 || progress.Total != 2 || progress.Dimension != 2 {
		t.Fatalf("progress = %+v, %v", progress, err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store = openStore(t, path)
	pending, err = store.PendingVectorChunks(ctx, workspace.ID, profile.Fingerprint, 8)
	if err != nil || len(pending) != 1 || pending[0].Ordinal != 1 {
		t.Fatalf("restart lost checkpoint: %+v, %v", pending, err)
	}
	if err := store.SaveVectorBatch(ctx, workspace.ID, profile.Fingerprint, []knowledge.VectorWrite{{DocumentID: document.ID, Ordinal: 1, ContentHash: pending[0].ContentHash, Values: []float64{0, 1, 0}}}); err == nil {
		t.Fatal("changed model dimension was accepted")
	}
	progress, err = store.VectorProgress(ctx, workspace.ID, profile.Fingerprint)
	if err != nil || progress.Indexed != 1 {
		t.Fatalf("rejected batch changed checkpoint: %+v, %v", progress, err)
	}
	if err := store.SaveVectorBatch(ctx, workspace.ID, profile.Fingerprint, []knowledge.VectorWrite{{DocumentID: document.ID, Ordinal: 1, ContentHash: pending[0].ContentHash, Values: []float64{0, 1}}}); err != nil {
		t.Fatal(err)
	}
	hits, err := store.SearchVectors(ctx, workspace.ID, "", profile.Fingerprint, []float64{0, 1}, 2)
	if err != nil || len(hits) != 2 || hits[0].Ordinal != 1 || hits[0].Path != "source.md" || hits[0].LineStart != 2 {
		t.Fatalf("vector ranking/provenance = %+v, %v", hits, err)
	}
	changed, _ := embeddings.ValidateProfile(embeddings.Profile{Kind: embeddings.LMStudio, BaseURL: "http://127.0.0.1:1234", Model: "model-b"})
	if err := store.SaveEmbeddingProfile(ctx, workspace.ID, changed); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveVectorBatch(ctx, workspace.ID, profile.Fingerprint, []knowledge.VectorWrite{{DocumentID: document.ID, Ordinal: 0, ContentHash: pending[0].ContentHash, Values: []float64{1, 0}}}); !errors.Is(err, knowledge.ErrProfileChanged) {
		t.Fatalf("stale batch accepted: %v", err)
	}
	progress, err = store.VectorProgress(ctx, workspace.ID, changed.Fingerprint)
	if err != nil || progress.Indexed != 0 || progress.Total != 2 {
		t.Fatalf("new profile reused old vectors: %+v, %v", progress, err)
	}
	if err := store.SaveEmbeddingProfile(ctx, workspace.ID, profile); err != nil {
		t.Fatal(err)
	}
	document.ContentHash = "source-v2"
	chunks[1].Content = "changed second chunk"
	if _, err := store.ReplaceKnowledge(ctx, document, chunks); err != nil {
		t.Fatal(err)
	}
	progress, err = store.VectorProgress(ctx, workspace.ID, profile.Fingerprint)
	if err != nil || progress.Indexed != 1 || progress.Total != 2 {
		t.Fatalf("unchanged chunk lost or stale chunk reused: %+v, %v", progress, err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
}
