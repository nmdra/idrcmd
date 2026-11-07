CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL);
CREATE TABLE registry_index (id INTEGER PRIMARY KEY CHECK (id = 1), model_id TEXT NOT NULL, dimensions INTEGER NOT NULL, index_version TEXT NOT NULL);
CREATE TABLE registry_tools (name TEXT PRIMARY KEY, description TEXT NOT NULL, command_json TEXT NOT NULL, content_hash TEXT NOT NULL, trusted INTEGER NOT NULL CHECK (trusted IN (0,1)), enabled INTEGER NOT NULL CHECK (enabled IN (0,1)), model_id TEXT NOT NULL, dimensions INTEGER NOT NULL, index_version TEXT NOT NULL, embedding_hash TEXT NOT NULL, embedding BLOB NOT NULL, created_at TEXT NOT NULL, updated_at TEXT NOT NULL);
INSERT INTO schema_migrations (version, applied_at) VALUES (1, datetime('now'));
