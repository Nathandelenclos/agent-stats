// Package sqlite is the SQLite index of the indexing context. It owns the schema and every write;
// other contexts read only the usage_* views, the index's published read model.
package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"

	"github.com/Nathandelenclos/agent-stats/internal/indexing/application"
	"github.com/Nathandelenclos/agent-stats/internal/indexing/domain"
	"github.com/Nathandelenclos/agent-stats/internal/kernel"
)

// The prompts table keeps each prompt's frustration signals (domain.Signals). The sessions view reads them
// through the session_prompts view below, which closes the debt of indexing them before anything read them
// (2026-11-21, closed 2026-11-22): no re-index was needed.
//
// page_size only applies to an index created now, and an index made by an older version keeps its 4 KiB pages.
// Fewer, larger pages make a cold run of 2 070 logs 18% faster (2.95 s to 2.42 s) and a report 14% (0.58 s to 0.50 s).
const schema = `
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
-- except in session_prompts, which exists to show a session's own prompts. usage_tool_calls is defined by
-- migration, as it gained a column (see addSkill).
CREATE VIEW IF NOT EXISTS usage_requests AS
	SELECT r.ts, r.model, r.input, r.output, r.cache_read, r.cache_write, r.cost, r.priced, r.duration_ms, r.ttft_ms, r.error,
		s.harness, s.agent_type, s.folder
	FROM requests r JOIN sessions s ON s.id = r.session_id;
-- position is the prompt's place in its session: rows are inserted in log order.
CREATE VIEW IF NOT EXISTS session_prompts AS
	SELECT p.session_id, p.rowid AS position, p.ts, p.text, p.chars, p.words, p.yelling, p.profanity, p.anguish, p.negation,
		p.repetition, p.blame
	FROM prompts p;
`

// forgetCodexLogs makes the next sync read every Codex log again: a Codex session indexed before its end was
// taken from its last tool call or prompt ended too late, and an unchanged log is not read twice.
// Only the bookkeeping goes; rows stay until their log is read again, so a log Codex since deleted keeps its history.
const forgetCodexLogs = `DELETE FROM files WHERE harness = 'codex'`

// sessionViews is the sessions read model. It holds no data, so migrate replaces it whenever its definition changes.
const sessionViews = `
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

// A migration brings the index up by one version. It runs inside migrate's transaction, so it must not manage one.
type migration func(ctx context.Context, conn *sql.Conn) error

// sqlSteps is a migration made of statements only.
func sqlSteps(steps ...string) migration {
	return func(ctx context.Context, conn *sql.Conn) error {
		for _, step := range steps {
			if _, err := conn.ExecContext(ctx, step); err != nil {
				return err
			}
		}
		return nil
	}
}

// migrations is the history of the index, oldest first. An index's user_version is how many of them it has run: 0 is
// an index from before migrations, and a fresh one runs them all on empty tables. Append a migration, never edit one,
// for an index that has run it is not migrated again. Each one sees the index as the previous ones left it.
var migrations = []migration{
	sqlSteps(sessionViews, forgetCodexLogs),
	sqlSteps(recordSkills),
	sqlSteps(recordRevisions),
}

// recordSkills makes the index record the skill each Skill call loads. It is a table of its own, not a column of
// tool_calls: a build from before it writes tool_calls with positional statements that a column more would refuse,
// so that build keeps writing an index this one has migrated (its Skill calls then await their log, see below).
//
// The published usage_tool_calls says, in its skill column, what the index knows of a call:
//   - on a Skill call (no server): the skill it loaded; "" when it named no valid skill; NULL while the index has no
//     record of it, because its log was not read again since the index records skills (a pending call);
//   - on any other call: "".
//
// The files holding a Skill call are forgotten, so that the next indexing reads them again and records them.
// So are those holding a call named "mcp__<server>__<tool>" under no server, how a Codex function call was
// recorded before the server was split out of its name.
const recordSkills = `
CREATE TABLE tool_call_skills (
	file TEXT NOT NULL, session_id TEXT NOT NULL, key TEXT NOT NULL, skill TEXT NOT NULL,
	PRIMARY KEY (session_id, key)) WITHOUT ROWID;
CREATE INDEX tool_call_skills_file ON tool_call_skills (file);
DELETE FROM files WHERE path IN (SELECT file FROM tool_calls WHERE name = 'Skill' AND server = '');
DELETE FROM files WHERE path IN (SELECT file FROM tool_calls WHERE server = '' AND name LIKE 'mcp\_\_%' ESCAPE '\');
DROP VIEW IF EXISTS usage_tool_calls;
CREATE VIEW usage_tool_calls AS
	SELECT t.ts, t.name, t.server, CASE WHEN t.name = 'Skill' AND t.server = '' THEN k.skill ELSE '' END AS skill,
		t.args_chars, t.result_chars, t.is_error, t.duration_ms, s.harness
	FROM tool_calls t JOIN sessions s ON s.id = t.session_id
		LEFT JOIN tool_call_skills k ON k.session_id = t.session_id AND k.key = t.key;
`

// recordRevisions keeps the revision a reader gives each log (application.LogFile.Revision) in a table of its own,
// for the reason recordSkills is one: a build from before it writes files with positional statements that a column
// more would refuse. A log this build saved has size and mtime 0 in files and its revision here. A row with another
// size or mtime was written by an earlier build, which saw a file of that size and modification time, and its
// revision is those two as "<size>:<mtime>", how the readers of files give theirs: nothing is read again for it.
const recordRevisions = `
CREATE TABLE file_revisions (path TEXT PRIMARY KEY, revision TEXT NOT NULL) WITHOUT ROWID;
`

// migrate runs, in one transaction, the migrations the index has not run, then records how many it has run: another
// connection keeps reading the previous state until it commits, and a failure leaves it untouched.
// An index that is up to date, or made by a newer build, is not locked for writing and not touched.
func migrate(db *sql.DB, migrations []migration) (err error) {
	ctx := context.Background()
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	userVersion := func() (v int, err error) {
		err = conn.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&v)
		return v, err
	}
	if v, err := userVersion(); err != nil || v >= len(migrations) {
		return err
	}
	if _, err := conn.ExecContext(ctx, `PRAGMA busy_timeout = 5000`); err != nil {
		return err
	}
	// IMMEDIATE: two processes opening the same old index queue up instead of failing on the upgrade.
	if _, err := conn.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return err
	}
	defer func() {
		if err != nil {
			conn.ExecContext(ctx, `ROLLBACK`)
		}
	}()
	v, err := userVersion() // read again under the lock: another process may have migrated, or a newer build upgraded, meanwhile
	if err != nil {
		return err
	}
	if v >= len(migrations) {
		_, err = conn.ExecContext(ctx, `COMMIT`)
		return err
	}
	for ; v < len(migrations); v++ {
		if err := migrations[v](ctx, conn); err != nil {
			return err
		}
	}
	if _, err := conn.ExecContext(ctx, fmt.Sprintf(`PRAGMA user_version = %d`, len(migrations))); err != nil {
		return err
	}
	_, err = conn.ExecContext(ctx, `COMMIT`)
	return err
}

// SQLiteIndex is the usage index kept in a local SQLite file.
type SQLiteIndex struct {
	db   *sql.DB
	user string
}

var _ application.TranscriptStore = (*SQLiteIndex)(nil)

// Options locate the index and name who it belongs to.
type Options struct {
	Path string // the SQLite file
	User string // "name@host", recorded on every session
}

// Open creates or opens the index. It holds prompt texts, so it is readable by its owner only.
func Open(o Options) (*SQLiteIndex, error) {
	path := o.Path
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	if err := private(path); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1) // ponytail: one connection, SQLite serialises writes anyway
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, err
	}
	if err := migrate(db, migrations); err != nil {
		db.Close()
		return nil, err
	}
	return &SQLiteIndex{db: db, user: o.User}, nil
}

// private creates path if needed and restricts it, and SQLite's companion files, to their owner.
// SQLite creates the -wal and -shm files with the permissions of the database file.
func private(path string) error {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return err
	}
	f.Close()
	for _, p := range []string{path, path + "-wal", path + "-shm"} {
		if err := os.Chmod(p, 0o600); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

// Close closes the database.
func (s *SQLiteIndex) Close() error { return s.db.Close() }

// IndexedFiles returns every log file indexed so far, with its revision at that point.
func (s *SQLiteIndex) IndexedFiles() (map[string]application.LogFile, error) {
	rows, err := s.db.Query(`SELECT f.path, f.harness,
			CASE WHEN f.size = 0 AND f.mtime = 0 THEN COALESCE(r.revision, '') ELSE f.size || ':' || f.mtime END
		FROM files f LEFT JOIN file_revisions r ON r.path = f.path`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	known := map[string]application.LogFile{}
	for rows.Next() {
		var f application.LogFile
		var harness string
		if err := rows.Scan(&f.Path, &harness, &f.Revision); err != nil {
			return nil, err
		}
		if f.Harness, err = kernel.ParseHarness(harness); err != nil {
			return nil, err
		}
		known[f.Path] = f
	}
	return known, rows.Err()
}

// statements are the prepared statements that write one log, bound to the transaction of a batch.
type statements struct {
	deletes                                               [5]*sql.Stmt // the rows of a file, in each of deletedTables
	session, request, tool, skill, prompt, file, revision *sql.Stmt
}

var deletedTables = [...]string{"sessions", "requests", "tool_calls", "tool_call_skills", "prompts"}

func prepare(tx *sql.Tx) (st statements, err error) {
	prep := func(query string) *sql.Stmt {
		if err != nil {
			return nil
		}
		var stmt *sql.Stmt
		stmt, err = tx.Prepare(query)
		return stmt
	}
	for i, table := range deletedTables {
		st.deletes[i] = prep(`DELETE FROM ` + table + ` WHERE file = ?`)
	}
	// Columns are named, so that a column another build adds to a table does not break these statements.
	st.session = prep(`INSERT OR REPLACE INTO sessions (id, file, harness, parent_id, agent_type, title, folder, user, started_at, ended_at)
		VALUES (?,?,?,?,?,?,?,?,?,?)`)
	st.request = prep(`INSERT OR REPLACE INTO requests (file, key, session_id, ts, model, provider, input, output, cache_read, cache_write, reasoning,
		cost, priced, duration_ms, ttft_ms, stop_reason, error) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`)
	st.tool = prep(`INSERT OR REPLACE INTO tool_calls (file, key, session_id, ts, name, server, args_chars, result_chars, is_error, duration_ms)
		VALUES (?,?,?,?,?,?,?,?,?,?)`)
	st.skill = prep(`INSERT OR REPLACE INTO tool_call_skills (file, session_id, key, skill) VALUES (?,?,?,?)`)
	st.prompt = prep(`INSERT OR REPLACE INTO prompts (file, key, session_id, ts, text, chars, words, yelling, profanity, anguish, negation, repetition, blame)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`)
	st.file = prep(`INSERT OR REPLACE INTO files (path, harness, size, mtime) VALUES (?,?,0,0)`) // the revision is in file_revisions
	st.revision = prep(`INSERT OR REPLACE INTO file_revisions (path, revision) VALUES (?,?)`)
	return st, err
}

func (st statements) close() {
	for _, stmt := range append(st.deletes[:], st.session, st.request, st.tool, st.skill, st.prompt, st.file, st.revision) {
		if stmt != nil {
			stmt.Close()
		}
	}
}

// Save replaces the rows indexed from each log, all in one transaction: committing once per log made the
// writes wait on the write-ahead log thousands of times. Each log is saved under its own savepoint, so one
// that fails is undone alone and the others are kept. Rows are keyed by session, not file, so a log moved
// elsewhere (Codex archiving) is not counted twice; later logs of the batch replace earlier ones.
func (s *SQLiteIndex) Save(batch []application.IndexedLog) []error {
	errs := make([]error, len(batch))
	failAll := func(err error) []error { // the transaction itself failed: whatever was not refused already is lost
		for i := range errs {
			if errs[i] == nil {
				errs[i] = err
			}
		}
		return errs
	}
	if len(batch) == 0 {
		return errs
	}
	tx, err := s.db.Begin()
	if err != nil {
		return failAll(err)
	}
	defer tx.Rollback()
	st, err := prepare(tx)
	defer st.close()
	if err != nil {
		return failAll(err)
	}
	for i, l := range batch {
		if _, err := tx.Exec(`SAVEPOINT log`); err != nil {
			return failAll(err)
		}
		if errs[i] = s.saveLog(st, l.File, l.Transcript); errs[i] != nil {
			if _, err := tx.Exec(`ROLLBACK TO log`); err != nil {
				return failAll(err)
			}
		}
		if _, err := tx.Exec(`RELEASE log`); err != nil {
			return failAll(err)
		}
	}
	if err := tx.Commit(); err != nil {
		return failAll(err)
	}
	return errs
}

func (s *SQLiteIndex) saveLog(st statements, file application.LogFile, t application.IndexedTranscript) error {
	path := file.Path
	for _, del := range st.deletes {
		if _, err := del.Exec(path); err != nil {
			return err
		}
	}
	ss := t.Session
	if _, err := st.session.Exec(ss.ID.String(), path, file.Harness.String(), nullStr(ss.ParentID.String()), ss.AgentType.String(), ss.Title, ss.Folder, s.user,
		kernel.UnixMilli(ss.Start), kernel.UnixMilli(ss.End)); err != nil {
		return err
	}
	for i, r := range t.Requests {
		c, n := t.Costs[i], r.Tokens.Counts()
		if _, err := st.request.Exec(path, r.Key, ss.ID.String(), kernel.UnixMilli(r.At), r.Model, r.Provider, n.Input, n.Output, n.CacheRead, n.CacheWrite, n.Reasoning,
			c.Cost.USD(), c.Priced, nullMillis(r.Duration), nullMillis(r.TTFT), r.StopReason, nullStr(r.Failure)); err != nil {
			return err
		}
	}
	for _, c := range t.Tools {
		if _, err := st.tool.Exec(path, c.Key, ss.ID.String(), kernel.UnixMilli(c.At), c.Name, c.Server, c.ArgsChars, c.ResultChars, c.IsError, nullMillis(c.Duration)); err != nil {
			return err
		}
		if c.LoadsSkill() {
			if _, err := st.skill.Exec(path, ss.ID.String(), c.Key, c.Skill); err != nil {
				return err
			}
		}
	}
	for i, pr := range t.Prompts {
		sig := t.Signals[i]
		if _, err := st.prompt.Exec(path, pr.Key, ss.ID.String(), kernel.UnixMilli(pr.At), pr.Text, sig.Chars, sig.Words, sig.Yelling, sig.Profanity,
			sig.Anguish, sig.Negation, sig.Repeated, sig.Blame); err != nil {
			return err
		}
	}
	if _, err := st.file.Exec(path, file.Harness.String()); err != nil {
		return err
	}
	_, err := st.revision.Exec(path, file.Revision)
	return err
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// nullMillis stores a duration in milliseconds, NULL when unknown.
func nullMillis(d domain.Duration) any {
	if !d.Known() {
		return nil
	}
	return float64(d.Value()) / float64(time.Millisecond)
}
