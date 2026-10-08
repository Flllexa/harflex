CREATE TABLE openrouter_catalog_credentials (
    profile_id TEXT PRIMARY KEY REFERENCES provider_profiles(id),
    origin TEXT NOT NULL,
    credential_provider TEXT NOT NULL,
    credential_account TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
