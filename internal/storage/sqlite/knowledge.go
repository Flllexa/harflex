package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/persioflexa/harflex/internal/catalog"
	"github.com/persioflexa/harflex/internal/knowledge"
)

const knowledgeDocumentColumns = "id,workspace_id,path,content_hash,source_size,source_modified_at,indexed_at,chunk_count"

func scanKnowledgeDocument(row rowScanner) (catalog.KnowledgeDocument, error) {
	var item catalog.KnowledgeDocument
	var modified, indexed string
	if err := row.Scan(&item.ID, &item.WorkspaceID, &item.Path, &item.ContentHash, &item.SourceSize, &modified, &indexed, &item.ChunkCount); err != nil {
		return catalog.KnowledgeDocument{}, err
	}
	var err error
	item.SourceModifiedAt, err = time.Parse(time.RFC3339Nano, modified)
	if err != nil {
		return catalog.KnowledgeDocument{}, fmt.Errorf("parse source modified timestamp: %w", err)
	}
	item.IndexedAt, err = time.Parse(time.RFC3339Nano, indexed)
	if err != nil {
		return catalog.KnowledgeDocument{}, fmt.Errorf("parse knowledge indexed timestamp: %w", err)
	}
	return item, nil
}

func (s *Store) ReplaceKnowledge(ctx context.Context, item catalog.KnowledgeDocument, chunks []knowledge.Chunk) (catalog.KnowledgeDocument, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return catalog.KnowledgeDocument{}, fmt.Errorf("begin knowledge replace: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	previous, err := scanKnowledgeDocument(tx.QueryRowContext(ctx, "SELECT "+knowledgeDocumentColumns+" FROM knowledge_documents WHERE workspace_id=? AND path=?", item.WorkspaceID, item.Path))
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return catalog.KnowledgeDocument{}, fmt.Errorf("read knowledge document: %w", err)
	}
	if err == nil {
		item.ID = previous.ID
		if previous.ContentHash == item.ContentHash {
			item.IndexedAt = previous.IndexedAt
			item.ChunkCount = previous.ChunkCount
			if previous.SourceSize != item.SourceSize || !previous.SourceModifiedAt.Equal(item.SourceModifiedAt) {
				if _, err := tx.ExecContext(ctx, "UPDATE knowledge_documents SET source_size=?,source_modified_at=? WHERE id=?", item.SourceSize, formatCatalogTime(item.SourceModifiedAt), item.ID); err != nil {
					return catalog.KnowledgeDocument{}, fmt.Errorf("update knowledge source metadata: %w", err)
				}
			}
			if err := tx.Commit(); err != nil {
				return catalog.KnowledgeDocument{}, fmt.Errorf("commit unchanged knowledge: %w", err)
			}
			return item, nil
		}
		if _, err := tx.ExecContext(ctx, "UPDATE knowledge_documents SET content_hash=?,source_size=?,source_modified_at=?,indexed_at=?,chunk_count=? WHERE id=?", item.ContentHash, item.SourceSize, formatCatalogTime(item.SourceModifiedAt), formatCatalogTime(item.IndexedAt), item.ChunkCount, item.ID); err != nil {
			return catalog.KnowledgeDocument{}, fmt.Errorf("update knowledge document: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM knowledge_chunks WHERE document_id=?", item.ID); err != nil {
			return catalog.KnowledgeDocument{}, fmt.Errorf("delete old knowledge chunks: %w", err)
		}
	} else {
		if _, err := tx.ExecContext(ctx, "INSERT INTO knowledge_documents(id,workspace_id,path,content_hash,source_size,source_modified_at,indexed_at,chunk_count) VALUES(?,?,?,?,?,?,?,?)", item.ID, item.WorkspaceID, item.Path, item.ContentHash, item.SourceSize, formatCatalogTime(item.SourceModifiedAt), formatCatalogTime(item.IndexedAt), item.ChunkCount); err != nil {
			return catalog.KnowledgeDocument{}, fmt.Errorf("insert knowledge document: %w", err)
		}
	}
	for _, chunk := range chunks {
		if _, err := tx.ExecContext(ctx, "INSERT INTO knowledge_chunks(document_id,ordinal,line_start,content,content_hash) VALUES(?,?,?,?,?)", item.ID, chunk.Ordinal, chunk.LineStart, chunk.Content, knowledge.HashChunk(chunk.Content)); err != nil {
			return catalog.KnowledgeDocument{}, fmt.Errorf("insert knowledge chunk: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM knowledge_vectors WHERE document_id=? AND NOT EXISTS
		(SELECT 1 FROM knowledge_chunks c WHERE c.document_id=knowledge_vectors.document_id AND c.ordinal=knowledge_vectors.ordinal AND c.content_hash=knowledge_vectors.chunk_hash)`, item.ID); err != nil {
		return catalog.KnowledgeDocument{}, fmt.Errorf("remove stale knowledge vectors: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return catalog.KnowledgeDocument{}, fmt.Errorf("commit knowledge replace: %w", err)
	}
	return item, nil
}

func (s *Store) GetKnowledge(ctx context.Context, workspaceID, documentID string) (catalog.KnowledgeDocument, error) {
	item, err := scanKnowledgeDocument(s.db.QueryRowContext(ctx, "SELECT "+knowledgeDocumentColumns+" FROM knowledge_documents WHERE workspace_id=? AND id=?", workspaceID, documentID))
	if err != nil {
		return catalog.KnowledgeDocument{}, fmt.Errorf("get knowledge document: %w", err)
	}
	return item, nil
}

func (s *Store) ListKnowledge(ctx context.Context, workspaceID string) ([]catalog.KnowledgeDocument, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+knowledgeDocumentColumns+" FROM knowledge_documents WHERE workspace_id=? ORDER BY indexed_at DESC,path", workspaceID)
	if err != nil {
		return nil, fmt.Errorf("list knowledge documents: %w", err)
	}
	defer rows.Close()
	items := make([]catalog.KnowledgeDocument, 0)
	for rows.Next() {
		item, err := scanKnowledgeDocument(rows)
		if err != nil {
			return nil, fmt.Errorf("scan knowledge document: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate knowledge documents: %w", err)
	}
	return items, nil
}

func (s *Store) SearchKnowledge(ctx context.Context, workspaceID, documentID, query string, limit int) ([]catalog.KnowledgeHit, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT d.id,c.ordinal,d.path,c.line_start,snippet(knowledge_chunks_fts,0,'','','…',24),d.source_modified_at,d.indexed_at
		FROM knowledge_chunks_fts JOIN knowledge_chunks c ON c.id=knowledge_chunks_fts.rowid
		JOIN knowledge_documents d ON d.id=c.document_id
		WHERE d.workspace_id=? AND (?='' OR d.id=?) AND knowledge_chunks_fts MATCH ?
		ORDER BY bm25(knowledge_chunks_fts),d.path,c.ordinal LIMIT ?`, workspaceID, documentID, documentID, query, limit)
	if err != nil {
		return nil, fmt.Errorf("search knowledge: %w", err)
	}
	defer rows.Close()
	items := make([]catalog.KnowledgeHit, 0)
	for rows.Next() {
		var item catalog.KnowledgeHit
		var modified, indexed string
		if err := rows.Scan(&item.DocumentID, &item.Ordinal, &item.Path, &item.LineStart, &item.Snippet, &modified, &indexed); err != nil {
			return nil, fmt.Errorf("scan knowledge hit: %w", err)
		}
		item.SourceModifiedAt, err = time.Parse(time.RFC3339Nano, modified)
		if err != nil {
			return nil, fmt.Errorf("parse knowledge source time: %w", err)
		}
		item.IndexedAt, err = time.Parse(time.RFC3339Nano, indexed)
		if err != nil {
			return nil, fmt.Errorf("parse knowledge index time: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate knowledge hits: %w", err)
	}
	return items, nil
}

func (s *Store) RemoveKnowledge(ctx context.Context, workspaceID, documentID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin knowledge removal: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, "DELETE FROM knowledge_chunks WHERE document_id IN (SELECT id FROM knowledge_documents WHERE id=? AND workspace_id=?)", documentID, workspaceID); err != nil {
		return fmt.Errorf("delete knowledge chunks: %w", err)
	}
	result, err := tx.ExecContext(ctx, "DELETE FROM knowledge_documents WHERE id=? AND workspace_id=?", documentID, workspaceID)
	if err != nil {
		return fmt.Errorf("delete knowledge document: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("count deleted knowledge documents: %w", err)
	}
	if count == 0 {
		return sql.ErrNoRows
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit knowledge removal: %w", err)
	}
	return nil
}
