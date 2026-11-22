package main

import (
	"database/sql"
	"path/filepath"
	"testing"
)

// Fixtures of the index as d15e0d2, the main branch before tool calls recorded their skill, made it: its schema, its
// session views, its user_version, and the statements it wrote logs with. They are copied, not shared with the
// indexing adapter, so that they stay what that build did whatever this one becomes.

// mainIndexSchema is the schema d15e0d2 created when it opened an index.
const mainIndexSchema = `
PRAGMA page_size = 16384;
PRAGMA journal_mode = WAL;
PRAGMA synchronous = NORMAL;
CREATE TABLE IF NOT EXISTS files (path TEXT PRIMARY KEY, harness TEXT NOT NULL, size INTEGER NOT NULL, mtime INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS sessions (
	id TEXT PRIMARY KEY, file TEXT NOT NULL, harness TEXT NOT NULL, parent_id TEXT, agent_type TEXT NOT NULL,
	title TEXT, folder TEXT, user TEXT NOT NULL, started_at INTEGER, ended_at INTEGER);
CREATE TABLE IF NOT EXISTS requests (
	file TEXT NOT NULL, key TEXT NOT NULL, session_id TEXT NOT NULL, ts INTEGER NOT NULL,
	model TEXT NOT NULL, provider TEXT NOT NULL,
	input INTEGER NOT NULL, output INTEGER NOT NULL, cache_read INTEGER NOT NULL, cache_write INTEGER NOT NULL, reasoning INTEGER NOT NULL,
	cost REAL NOT NULL, priced INTEGER NOT NULL, duration_ms REAL, ttft_ms REAL, stop_reason TEXT, error TEXT,
	PRIMARY KEY (session_id, key));
CREATE TABLE IF NOT EXISTS tool_calls (
	file TEXT NOT NULL, key TEXT NOT NULL, session_id TEXT NOT NULL, ts INTEGER NOT NULL,
	name TEXT NOT NULL, server TEXT NOT NULL, args_chars INTEGER NOT NULL, result_chars INTEGER NOT NULL,
	is_error INTEGER NOT NULL, duration_ms REAL,
	PRIMARY KEY (session_id, key));
CREATE TABLE IF NOT EXISTS prompts (
	file TEXT NOT NULL, key TEXT NOT NULL, session_id TEXT NOT NULL, ts INTEGER NOT NULL, text TEXT NOT NULL,
	chars INTEGER NOT NULL, words INTEGER NOT NULL, yelling INTEGER NOT NULL, profanity INTEGER NOT NULL,
	anguish INTEGER NOT NULL, negation INTEGER NOT NULL, repetition INTEGER NOT NULL, blame INTEGER NOT NULL,
	PRIMARY KEY (session_id, key));
CREATE INDEX IF NOT EXISTS requests_ts ON requests (ts);
CREATE INDEX IF NOT EXISTS requests_file ON requests (file);
CREATE INDEX IF NOT EXISTS tool_calls_file ON tool_calls (file);
CREATE INDEX IF NOT EXISTS prompts_file ON prompts (file);
CREATE INDEX IF NOT EXISTS sessions_file ON sessions (file);
CREATE INDEX IF NOT EXISTS sessions_parent ON sessions (parent_id);
CREATE INDEX IF NOT EXISTS tool_calls_ts ON tool_calls (ts);
CREATE INDEX IF NOT EXISTS prompts_ts ON prompts (ts);
CREATE INDEX IF NOT EXISTS sessions_started ON sessions (started_at);
-- Published read model: the only part of the index other contexts may read. Prompt texts stay private,
-- except in session_prompts, which exists to show a session's own prompts.
CREATE VIEW IF NOT EXISTS usage_requests AS
	SELECT r.ts, r.model, r.input, r.output, r.cache_read, r.cache_write, r.cost, r.priced, r.duration_ms, r.ttft_ms, r.error,
		s.harness, s.agent_type, s.folder
	FROM requests r JOIN sessions s ON s.id = r.session_id;
CREATE VIEW IF NOT EXISTS usage_tool_calls AS
	SELECT t.ts, t.name, t.server, t.args_chars, t.result_chars, t.is_error, t.duration_ms, s.harness
	FROM tool_calls t JOIN sessions s ON s.id = t.session_id;
-- position is the prompt's place in its session: rows are inserted in log order.
CREATE VIEW IF NOT EXISTS session_prompts AS
	SELECT p.session_id, p.rowid AS position, p.ts, p.text, p.chars, p.words, p.yelling, p.profanity, p.anguish, p.negation,
		p.repetition, p.blame
	FROM prompts p;
`

// mainIndexSessionViews is the sessions read model d15e0d2 built when it migrated an index to version 1.
const mainIndexSessionViews = `
DROP VIEW IF EXISTS session_requests;
DROP VIEW IF EXISTS session_tool_calls;
DROP VIEW IF EXISTS session_overview;
-- Session views. root_id is the session a listing shows: the session itself, or the top session whose chain of
-- subagents it belongs to. A subagent whose parent log was never indexed (or was deleted) is its own root, and so
-- is a main conversation forked from another one: only subagents roll up into their parent. A session named
-- "<parent id>/<name>" is a subagent whatever type its log declared.
-- A session whose chain of parents never reaches a root (subagents naming each other, or themselves, as parent, and
-- whatever hangs below them) is its own root: every session is listed once, and so are its requests.
-- run_started_at and run_ended_at span the whole run (the root and its subagents): a session is active until its
-- last subagent stops. 0 stands for unknown.
CREATE VIEW session_overview AS
	WITH RECURSIVE tree(id, root_id) AS (
		SELECT s.id, s.id FROM sessions s
		WHERE NOT EXISTS (SELECT 1 FROM sessions p WHERE p.id = s.parent_id
			AND (s.agent_type <> 'main' OR substr(s.id, 1, length(p.id) + 1) = p.id || '/'))
		UNION ALL
		SELECT c.id, t.root_id FROM tree t JOIN sessions c ON c.parent_id = t.id
			AND (c.agent_type <> 'main' OR substr(c.id, 1, length(t.id) + 1) = t.id || '/')
	),
	placed(id, root_id) AS (
		SELECT id, root_id FROM tree
		UNION ALL
		SELECT s.id, s.id FROM sessions s WHERE s.id NOT IN (SELECT id FROM tree)
	)
	SELECT s.id, t.root_id, s.harness, s.agent_type, s.title, s.folder, s.started_at, s.ended_at,
		COALESCE(MIN(NULLIF(s.started_at, 0)) OVER (PARTITION BY t.root_id), 0) AS run_started_at,
		MAX(s.ended_at) OVER (PARTITION BY t.root_id) AS run_ended_at
	FROM placed t JOIN sessions s ON s.id = t.id;
CREATE VIEW session_requests AS
	SELECT o.root_id, r.session_id, r.ts, r.model, r.input, r.output, r.cache_read, r.cache_write, r.cost, r.priced,
		r.duration_ms, r.ttft_ms, r.error
	FROM requests r JOIN session_overview o ON o.id = r.session_id;
CREATE VIEW session_tool_calls AS
	SELECT o.root_id, t.session_id, t.ts, t.name, t.server, t.args_chars, t.result_chars, t.is_error, t.duration_ms
	FROM tool_calls t JOIN session_overview o ON o.id = t.session_id;
`

// mainIndexInserts are the statements d15e0d2 wrote a log with, one per table: positional, so they only fit tables of the shape it created.
var mainIndexInserts = map[string]string{
	"sessions":   `INSERT OR REPLACE INTO sessions VALUES (?,?,?,?,?,?,?,?,?,?)`,
	"requests":   `INSERT OR REPLACE INTO requests VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
	"tool_calls": `INSERT OR REPLACE INTO tool_calls VALUES (?,?,?,?,?,?,?,?,?,?)`,
	"prompts":    `INSERT OR REPLACE INTO prompts VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`,
	"files":      `INSERT OR REPLACE INTO files VALUES (?,?,?,?)`,
}

// indexAsMainMadeIt creates the index d15e0d2 left after opening it: schema, session views, version 1.
func indexAsMainMadeIt(t *testing.T) (path string, db *sql.DB) {
	t.Helper()
	path = filepath.Join(t.TempDir(), "index.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(mainIndexSchema + mainIndexSessionViews + `PRAGMA user_version = 1`); err != nil {
		t.Fatal(err)
	}
	return path, db
}

// indexBeforeVersioning is the index of 80bf4b1, before tool calls recorded the skill they load and before indexes were
// versioned (user_version 0, no session views): one Claude log with a Skill call, one without, and a Codex log whose
// function call kept the full name of an MCP tool.
const indexBeforeVersioning = `
CREATE TABLE files (path TEXT PRIMARY KEY, harness TEXT NOT NULL, size INTEGER NOT NULL, mtime INTEGER NOT NULL);
CREATE TABLE sessions (id TEXT PRIMARY KEY, file TEXT NOT NULL, harness TEXT NOT NULL, parent_id TEXT, agent_type TEXT NOT NULL,
	title TEXT, folder TEXT, user TEXT NOT NULL, started_at INTEGER, ended_at INTEGER);
CREATE TABLE tool_calls (file TEXT NOT NULL, key TEXT NOT NULL, session_id TEXT NOT NULL, ts INTEGER NOT NULL,
	name TEXT NOT NULL, server TEXT NOT NULL, args_chars INTEGER NOT NULL, result_chars INTEGER NOT NULL,
	is_error INTEGER NOT NULL, duration_ms REAL, PRIMARY KEY (session_id, key));
CREATE VIEW usage_tool_calls AS SELECT t.ts, t.name, t.server, t.args_chars, t.result_chars, t.is_error, t.duration_ms, s.harness
	FROM tool_calls t JOIN sessions s ON s.id = t.session_id;
INSERT INTO files VALUES ('/logs/with-skill.jsonl', 'claude', 1, 1), ('/logs/without.jsonl', 'claude', 1, 1), ('/logs/codex-mcp.jsonl', 'codex', 1, 1);
INSERT INTO sessions VALUES ('claude:a', '/logs/with-skill.jsonl', 'claude', NULL, 'main', '', '', 'me@host', 1, 1),
	('claude:b', '/logs/without.jsonl', 'claude', NULL, 'main', '', '', 'me@host', 1, 1),
	('codex:c', '/logs/codex-mcp.jsonl', 'codex', NULL, 'main', '', '', 'me@host', 1, 1);
INSERT INTO tool_calls VALUES ('/logs/with-skill.jsonl', 't1', 'claude:a', 1, 'Skill', '', 0, 0, 0, NULL),
	('/logs/without.jsonl', 't2', 'claude:b', 1, 'Read', '', 0, 0, 0, NULL),
	('/logs/codex-mcp.jsonl', 't3', 'codex:c', 1, 'mcp__demo__read', '', 0, 0, 0, NULL);
`

// indexBeforeVersioningAt creates the index of indexBeforeVersioning.
func indexBeforeVersioningAt(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "index.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(indexBeforeVersioning); err != nil {
		t.Fatal(err)
	}
	return path
}

// indexAsMainMadeItBeforeItsMigrations is the index of main as it was before indexes were versioned: its schema and
// no session views, user_version 0.
func indexAsMainMadeItBeforeItsMigrations(t *testing.T) (path string, db *sql.DB) {
	t.Helper()
	path = filepath.Join(t.TempDir(), "index.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(mainIndexSchema); err != nil {
		t.Fatal(err)
	}
	return path, db
}

// mainWritesALog writes, with the statements of mainIndexInserts, one log of a harness holding one tool call.
func mainWritesALog(t *testing.T, db *sql.DB, harness, file, tool, server string) {
	t.Helper()
	id := harness + ":" + filepath.Base(file)
	for table, args := range map[string][]any{
		"files":      {file, harness, 1, 1},
		"sessions":   {id, file, harness, nil, "main", "", "", "me@host", 1, 1},
		"requests":   {file, "m1", id, 1, "model", "anthropic", 1, 1, 0, 0, 0, 0.5, 1, nil, nil, nil, nil},
		"tool_calls": {file, "t1", id, 1, tool, server, 0, 0, 0, nil},
		"prompts":    {file, "p1", id, 1, "text", 4, 1, 0, 0, 0, 0, 0, 0},
	} {
		if _, err := db.Exec(mainIndexInserts[table], args...); err != nil {
			t.Fatalf("main's statement for %s: %v", table, err)
		}
	}
}
