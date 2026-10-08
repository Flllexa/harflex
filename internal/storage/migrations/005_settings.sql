CREATE TABLE app_settings (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    default_backend_id TEXT NOT NULL DEFAULT ''
);
INSERT INTO app_settings (id, default_backend_id) VALUES (1, '');
