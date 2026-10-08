-- Preserve old request keys while adding distinct explicit synthesis commands.
CREATE TABLE pipeline_brainstorm_requests_next (
    run_id TEXT NOT NULL REFERENCES pipeline_brainstorm_runs(id) ON DELETE RESTRICT,
    request_id TEXT NOT NULL,
    kind TEXT NOT NULL CHECK (kind IN ('question','synthesis','questions_sufficient','finish_synthesis','answer','stop','resume','revision','bypass','confirm','approve','cancel')),
    payload_hash TEXT NOT NULL,
    result_id TEXT NOT NULL,
    created_at TEXT NOT NULL,
    PRIMARY KEY (run_id, request_id)
);
INSERT INTO pipeline_brainstorm_requests_next SELECT * FROM pipeline_brainstorm_requests;
DROP TABLE pipeline_brainstorm_requests;
ALTER TABLE pipeline_brainstorm_requests_next RENAME TO pipeline_brainstorm_requests;
