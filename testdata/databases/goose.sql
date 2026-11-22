-- Session and message columns used by Goose at 36b29113af, with invented data.
CREATE TABLE sessions (
    id TEXT PRIMARY KEY, name TEXT, working_dir TEXT NOT NULL,
    created_at TIMESTAMP, updated_at TIMESTAMP, parent_session_id TEXT,
    accumulated_input_tokens INTEGER, accumulated_cost REAL
);
CREATE TABLE messages (
    id INTEGER PRIMARY KEY AUTOINCREMENT, message_id TEXT, session_id TEXT NOT NULL REFERENCES sessions(id),
    role TEXT NOT NULL, content_json TEXT NOT NULL, created_timestamp INTEGER NOT NULL,
    tokens INTEGER, metadata_json TEXT
);
INSERT INTO sessions VALUES
    ('example', 'Example', '/work/example', '2026-02-01 10:00:00', '2026-02-01 10:00:09', NULL, 280, 1),
    ('child', '', '/work/example', '2026-02-01 10:00:00', '2026-02-01 10:00:01', 'example', NULL, NULL);
INSERT INTO messages (message_id, session_id, role, content_json, created_timestamp, tokens, metadata_json) VALUES
    ('prompt', 'example', 'user', '[{"type":"text","text":"Check the example"}]', 1769940000, NULL, NULL),
    ('context', 'example', 'user', '[{"type":"text","text":"Synthetic context"}]', 1769940001, NULL, '{"userVisible":false,"turnContext":true}'),
    ('answer', 'example', 'assistant', '[{"type":"toolRequest","id":"call-1","toolCall":{"status":"success","value":{"name":"lookup","arguments":{"key":"é"}}}},{"type":"toolRequest","id":"call-2","toolCall":{"status":"success","value":{"name":"lookup"}}}]', 1769940002003, 120, '{"inference":{"provider":"sample","requestedModel":"requested","resolvedModel":"resolved"},"usage":{"inputTokens":100,"outputTokens":20,"cacheReadTokens":30,"cacheWriteTokens":10,"cost":0.03,"elapsedMs":300,"timeToFirstTokenMs":120}}'),
    ('answer', 'example', 'assistant', '[]', 1769940002003, NULL, '{"usage":{"inputTokens":100,"outputTokens":20}}'),
    ('tools', 'example', 'user', '[{"type":"toolResponse","id":"call-1","toolResult":{"status":"success","value":{"content":[{"type":"text","text":"missing"}],"isError":true}}},{"type":"toolResponse","id":"call-2","toolResult":{"status":"error","error":"unavailable"}}]', 1769940003000, NULL, NULL),
    ('compact', 'example', 'assistant', '[{"type":"text","text":"Summary"}]', 1769940004000, NULL, '{"compactionSummary":true,"usage":{"isCompaction":true,"inputTokens":80}}'),
    ('failure', 'example', 'assistant', '[{"type":"error","kind":"creditsExhausted","message":"Example error"}]', 1769940005000, NULL, '{"inference":{"provider":"sample","requestedModel":"requested"}}'),
    ('invalid', 'example', 'assistant', '[]', 1769940006000, NULL, '{"usage":{"inputTokens":10,"cacheReadTokens":11}}');
