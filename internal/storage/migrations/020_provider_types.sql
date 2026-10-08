ALTER TABLE provider_profiles ADD COLUMN provider_type TEXT NOT NULL DEFAULT 'generic'
    CHECK (provider_type IN ('openai', 'openrouter', 'lm_studio', 'ollama', 'generic'));
