CREATE TABLE knowledge_documents (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL REFERENCES workspaces(id),
    path TEXT NOT NULL,
    content_hash TEXT NOT NULL,
    source_size INTEGER NOT NULL,
    source_modified_at TEXT NOT NULL,
    indexed_at TEXT NOT NULL,
    chunk_count INTEGER NOT NULL,
    UNIQUE(workspace_id, path)
);
CREATE INDEX knowledge_documents_workspace ON knowledge_documents(workspace_id, path);

CREATE TABLE knowledge_chunks (
    id INTEGER PRIMARY KEY,
    document_id TEXT NOT NULL REFERENCES knowledge_documents(id) ON DELETE CASCADE,
    ordinal INTEGER NOT NULL,
    line_start INTEGER NOT NULL,
    content TEXT NOT NULL,
    UNIQUE(document_id, ordinal)
);
CREATE VIRTUAL TABLE knowledge_chunks_fts USING fts5(content, content='knowledge_chunks', content_rowid='id', tokenize='unicode61 remove_diacritics 2');
CREATE TRIGGER knowledge_chunks_insert AFTER INSERT ON knowledge_chunks BEGIN
    INSERT INTO knowledge_chunks_fts(rowid, content) VALUES (new.id, new.content);
END;
CREATE TRIGGER knowledge_chunks_delete AFTER DELETE ON knowledge_chunks BEGIN
    INSERT INTO knowledge_chunks_fts(knowledge_chunks_fts, rowid, content) VALUES ('delete', old.id, old.content);
END;
CREATE TRIGGER knowledge_chunks_update AFTER UPDATE ON knowledge_chunks BEGIN
    INSERT INTO knowledge_chunks_fts(knowledge_chunks_fts, rowid, content) VALUES ('delete', old.id, old.content);
    INSERT INTO knowledge_chunks_fts(rowid, content) VALUES (new.id, new.content);
END;
