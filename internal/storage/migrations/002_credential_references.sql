-- Published references survive profile replacement/deletion for audit redaction.
-- Deliberately no cascading profile foreign key: this is a historical inventory.
CREATE TABLE credential_references (
    profile_id TEXT NOT NULL,
    provider TEXT NOT NULL,
    account TEXT NOT NULL,
    created_at TEXT NOT NULL,
    PRIMARY KEY (provider, account)
);

INSERT INTO credential_references (profile_id, provider, account, created_at)
SELECT id, credential_provider, credential_account, updated_at
FROM provider_profiles
WHERE credential_provider <> '' AND credential_account <> ''
ORDER BY updated_at, id
ON CONFLICT(provider, account) DO NOTHING;
