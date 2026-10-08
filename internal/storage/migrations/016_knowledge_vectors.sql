ALTER TABLE knowledge_chunks ADD COLUMN content_hash TEXT NOT NULL DEFAULT '';

CREATE TABLE knowledge_embedding_profiles (
    workspace_id TEXT PRIMARY KEY REFERENCES workspaces(id) ON DELETE CASCADE,
    kind TEXT NOT NULL,
    base_url TEXT NOT NULL,
    model TEXT NOT NULL,
    fingerprint TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE knowledge_vectors (
    document_id TEXT NOT NULL REFERENCES knowledge_documents(id) ON DELETE CASCADE,
    ordinal INTEGER NOT NULL,
    fingerprint TEXT NOT NULL,
    chunk_hash TEXT NOT NULL,
    dimension INTEGER NOT NULL,
    vector BLOB NOT NULL,
    indexed_at TEXT NOT NULL,
    PRIMARY KEY(document_id, ordinal, fingerprint)
);
CREATE INDEX knowledge_vectors_profile ON knowledge_vectors(fingerprint, document_id, ordinal);
