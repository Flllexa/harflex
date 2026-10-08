ALTER TABLE session_model_selections ADD COLUMN context_length INTEGER NOT NULL DEFAULT 0 CHECK(context_length >= 0);
