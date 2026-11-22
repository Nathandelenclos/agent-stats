-- Goose v1.10.0 SQLite projection, before metadata_json and message_id were added.
CREATE TABLE sessions (
    id TEXT PRIMARY KEY, working_dir TEXT NOT NULL,
    created_at TIMESTAMP, updated_at TIMESTAMP, input_tokens INTEGER
);
CREATE TABLE messages (
    id INTEGER PRIMARY KEY AUTOINCREMENT, session_id TEXT NOT NULL REFERENCES sessions(id),
    role TEXT NOT NULL, content_json TEXT NOT NULL, created_timestamp INTEGER NOT NULL, tokens INTEGER
);
INSERT INTO sessions VALUES ('old', '/work/old', '2026-02-01 10:00:00', '2026-02-01 10:00:02', 50);
INSERT INTO messages (session_id, role, content_json, created_timestamp, tokens) VALUES
    ('old', 'user', '[{"type":"text","text":"Check an old example"}]', 1769940000, NULL),
    ('old', 'assistant', '[{"type":"toolRequest","id":"old-call","toolCall":{"status":"success","value":{"name":"lookup"}}}]', 1769940001, 10),
    ('old', 'user', '[{"type":"toolResponse","id":"old-call","toolResult":{"status":"success","value":[{"type":"text","text":"found"}]}}]', 1769940002, NULL);
