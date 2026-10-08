package sqlite

import (
	"context"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/persioflexa/harflex/internal/catalog"
	"github.com/persioflexa/harflex/internal/embeddings"
	"github.com/persioflexa/harflex/internal/knowledge"
)

func (s *Store) SaveEmbeddingProfile(ctx context.Context, workspaceID string, input embeddings.Profile) error {
	profile, err := embeddings.ValidateProfile(input)
	if err != nil || workspaceID == "" {
		return embeddings.ErrInvalidProfile
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO knowledge_embedding_profiles(workspace_id,kind,base_url,model,fingerprint,updated_at)
		VALUES(?,?,?,?,?,?) ON CONFLICT(workspace_id) DO UPDATE SET kind=excluded.kind,base_url=excluded.base_url,model=excluded.model,fingerprint=excluded.fingerprint,updated_at=excluded.updated_at`, workspaceID, profile.Kind, profile.BaseURL, profile.Model, profile.Fingerprint, formatCatalogTime(time.Now().UTC()))
	if err != nil {
		return fmt.Errorf("save local embedding profile: %w", err)
	}
	return nil
}

func (s *Store) GetEmbeddingProfile(ctx context.Context, workspaceID string) (embeddings.Profile, error) {
	var profile embeddings.Profile
	err := s.db.QueryRowContext(ctx, `SELECT kind,base_url,model,fingerprint FROM knowledge_embedding_profiles WHERE workspace_id=?`, workspaceID).Scan(&profile.Kind, &profile.BaseURL, &profile.Model, &profile.Fingerprint)
	if err != nil {
		return embeddings.Profile{}, fmt.Errorf("get local embedding profile: %w", err)
	}
	validated, err := embeddings.ValidateProfile(profile)
	if err != nil || validated.Fingerprint != profile.Fingerprint {
		return embeddings.Profile{}, embeddings.ErrInvalidProfile
	}
	return validated, nil
}

func (s *Store) PendingVectorChunks(ctx context.Context, workspaceID, fingerprint string, limit int) ([]knowledge.VectorChunk, error) {
	if limit < 1 || limit > embeddings.MaxBatchSize {
		return nil, fmt.Errorf("invalid vector batch size")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT d.id,c.ordinal,c.content,c.content_hash FROM knowledge_chunks c
		JOIN knowledge_documents d ON d.id=c.document_id
		LEFT JOIN knowledge_vectors v ON v.document_id=c.document_id AND v.ordinal=c.ordinal AND v.fingerprint=?
		WHERE d.workspace_id=? AND (v.document_id IS NULL OR c.content_hash='' OR v.chunk_hash<>c.content_hash)
		ORDER BY d.path,c.ordinal LIMIT ?`, fingerprint, workspaceID, limit)
	if err != nil {
		return nil, fmt.Errorf("list pending vector chunks: %w", err)
	}
	defer rows.Close()
	items := make([]knowledge.VectorChunk, 0, limit)
	for rows.Next() {
		var item knowledge.VectorChunk
		var storedHash string
		if err := rows.Scan(&item.DocumentID, &item.Ordinal, &item.Content, &storedHash); err != nil {
			return nil, fmt.Errorf("scan vector chunk: %w", err)
		}
		item.ContentHash = knowledge.HashChunk(item.Content)
		if storedHash != "" && storedHash != item.ContentHash {
			return nil, knowledge.ErrVectorSourceChanged
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate pending vector chunks: %w", err)
	}
	return items, nil
}

func (s *Store) VectorProgress(ctx context.Context, workspaceID, fingerprint string) (knowledge.VectorProgress, error) {
	var progress knowledge.VectorProgress
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*),COUNT(v.document_id),COALESCE(MAX(v.dimension),0) FROM knowledge_chunks c
		JOIN knowledge_documents d ON d.id=c.document_id
		LEFT JOIN knowledge_vectors v ON v.document_id=c.document_id AND v.ordinal=c.ordinal AND v.fingerprint=? AND v.chunk_hash=c.content_hash
		WHERE d.workspace_id=?`, fingerprint, workspaceID).Scan(&progress.Total, &progress.Indexed, &progress.Dimension)
	if err != nil {
		return knowledge.VectorProgress{}, fmt.Errorf("read vector progress: %w", err)
	}
	return progress, nil
}

func vectorBlob(values []float64) ([]byte, error) {
	if len(values) == 0 || len(values) > embeddings.MaxDimensions {
		return nil, embeddings.ErrInvalidResponse
	}
	blob := make([]byte, len(values)*4)
	var norm float64
	for index, value := range values {
		if math.IsNaN(value) || math.IsInf(value, 0) || math.Abs(value) > math.MaxFloat32 {
			return nil, embeddings.ErrInvalidResponse
		}
		encoded := float32(value)
		binary.LittleEndian.PutUint32(blob[index*4:], math.Float32bits(encoded))
		norm += float64(encoded) * float64(encoded)
	}
	if norm == 0 || math.IsInf(norm, 0) {
		return nil, embeddings.ErrInvalidResponse
	}
	return blob, nil
}

func (s *Store) SaveVectorBatch(ctx context.Context, workspaceID, fingerprint string, writes []knowledge.VectorWrite) error {
	if len(writes) == 0 || len(writes) > embeddings.MaxBatchSize {
		return embeddings.ErrInvalidResponse
	}
	dimension := len(writes[0].Values)
	encoded := make([][]byte, len(writes))
	for index, item := range writes {
		if item.DocumentID == "" || item.Ordinal < 0 || item.ContentHash == "" || len(item.Values) != dimension {
			return embeddings.ErrInvalidResponse
		}
		blob, err := vectorBlob(item.Values)
		if err != nil {
			return err
		}
		encoded[index] = blob
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin vector batch: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var active string
	err = tx.QueryRowContext(ctx, "SELECT fingerprint FROM knowledge_embedding_profiles WHERE workspace_id=?", workspaceID).Scan(&active)
	if errors.Is(err, sql.ErrNoRows) {
		return knowledge.ErrProfileChanged
	}
	if err != nil {
		return fmt.Errorf("check vector profile: %w", err)
	}
	if active != fingerprint {
		return knowledge.ErrProfileChanged
	}
	var prior int
	err = tx.QueryRowContext(ctx, `SELECT v.dimension FROM knowledge_vectors v JOIN knowledge_documents d ON d.id=v.document_id WHERE d.workspace_id=? AND v.fingerprint=? LIMIT 1`, workspaceID, fingerprint).Scan(&prior)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("check vector dimension: %w", err)
	}
	if err == nil && prior != dimension {
		return embeddings.ErrInvalidResponse
	}
	for index, item := range writes {
		var chunkID int64
		var content, storedHash string
		err := tx.QueryRowContext(ctx, `SELECT c.id,c.content,c.content_hash FROM knowledge_chunks c JOIN knowledge_documents d ON d.id=c.document_id
			WHERE d.workspace_id=? AND c.document_id=? AND c.ordinal=?`, workspaceID, item.DocumentID, item.Ordinal).Scan(&chunkID, &content, &storedHash)
		if errors.Is(err, sql.ErrNoRows) {
			return knowledge.ErrVectorSourceChanged
		}
		if err != nil {
			return fmt.Errorf("check vector source: %w", err)
		}
		if knowledge.HashChunk(content) != item.ContentHash || (storedHash != "" && storedHash != item.ContentHash) {
			return knowledge.ErrVectorSourceChanged
		}
		if storedHash == "" {
			if _, err := tx.ExecContext(ctx, "UPDATE knowledge_chunks SET content_hash=? WHERE id=?", item.ContentHash, chunkID); err != nil {
				return fmt.Errorf("backfill chunk hash: %w", err)
			}
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO knowledge_vectors(document_id,ordinal,fingerprint,chunk_hash,dimension,vector,indexed_at)
			VALUES(?,?,?,?,?,?,?) ON CONFLICT(document_id,ordinal,fingerprint) DO UPDATE SET chunk_hash=excluded.chunk_hash,dimension=excluded.dimension,vector=excluded.vector,indexed_at=excluded.indexed_at`, item.DocumentID, item.Ordinal, fingerprint, item.ContentHash, dimension, encoded[index], formatCatalogTime(time.Now().UTC()))
		if err != nil {
			return fmt.Errorf("save vector: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit vector batch: %w", err)
	}
	return nil
}

func (s *Store) SearchVectors(ctx context.Context, workspaceID, documentID, fingerprint string, query []float64, limit int) ([]catalog.KnowledgeHit, error) {
	if limit < 1 || limit > 50 {
		return nil, embeddings.ErrInvalidResponse
	}
	queryBlob, err := vectorBlob(query)
	if err != nil {
		return nil, err
	}
	_ = queryBlob
	rows, err := s.db.QueryContext(ctx, `SELECT d.id,c.ordinal,d.path,c.line_start,c.content,d.source_modified_at,d.indexed_at,v.dimension,v.vector
		FROM knowledge_vectors v JOIN knowledge_documents d ON d.id=v.document_id
		JOIN knowledge_chunks c ON c.document_id=v.document_id AND c.ordinal=v.ordinal AND c.content_hash=v.chunk_hash
		WHERE d.workspace_id=? AND (?='' OR d.id=?) AND v.fingerprint=? ORDER BY d.path,c.ordinal LIMIT 10001`, workspaceID, documentID, documentID, fingerprint)
	if err != nil {
		return nil, fmt.Errorf("search local vectors: %w", err)
	}
	defer rows.Close()
	hits := make([]catalog.KnowledgeHit, 0, limit)
	count := 0
	for rows.Next() {
		count++
		if count > 10000 {
			return nil, knowledge.ErrVectorLimit
		}
		var hit catalog.KnowledgeHit
		var modified, indexed string
		var dimension int
		var blob []byte
		if err := rows.Scan(&hit.DocumentID, &hit.Ordinal, &hit.Path, &hit.LineStart, &hit.Snippet, &modified, &indexed, &dimension, &blob); err != nil {
			return nil, fmt.Errorf("scan local vector: %w", err)
		}
		if dimension != len(query) || len(blob) != dimension*4 {
			return nil, embeddings.ErrInvalidResponse
		}
		hit.SourceModifiedAt, err = time.Parse(time.RFC3339Nano, modified)
		if err != nil {
			return nil, fmt.Errorf("parse vector source time: %w", err)
		}
		hit.IndexedAt, err = time.Parse(time.RFC3339Nano, indexed)
		if err != nil {
			return nil, fmt.Errorf("parse vector index time: %w", err)
		}
		var dot, queryNorm, rowNorm float64
		for index, value := range query {
			other := float64(math.Float32frombits(binary.LittleEndian.Uint32(blob[index*4:])))
			if math.IsNaN(other) || math.IsInf(other, 0) {
				return nil, embeddings.ErrInvalidResponse
			}
			dot += value * other
			queryNorm += value * value
			rowNorm += other * other
		}
		if rowNorm == 0 {
			return nil, embeddings.ErrInvalidResponse
		}
		hit.Score = dot / math.Sqrt(queryNorm*rowNorm)
		if len(hits) < limit {
			hits = append(hits, hit)
			continue
		}
		worst := 0
		for i := 1; i < len(hits); i++ {
			if hits[i].Score < hits[worst].Score {
				worst = i
			}
		}
		if hit.Score > hits[worst].Score {
			hits[worst] = hit
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate local vectors: %w", err)
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].Score != hits[j].Score {
			return hits[i].Score > hits[j].Score
		}
		if hits[i].Path != hits[j].Path {
			return hits[i].Path < hits[j].Path
		}
		return hits[i].Ordinal < hits[j].Ordinal
	})
	return hits, nil
}
