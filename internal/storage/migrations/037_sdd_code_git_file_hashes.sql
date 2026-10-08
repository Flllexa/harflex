ALTER TABLE pipeline_sessions ADD COLUMN baseline_git_file_hashes TEXT NOT NULL DEFAULT '{}';
