// Package sqlite is the SQLite index of the indexing context. It owns the schema and every write;
// other contexts read only the usage_* views, the index's published read model.
package sqlite

import (
	"database/sql"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"

	"github.com/Nathandelenclos/agent-stats/internal/indexing/application"
	"github.com/Nathandelenclos/agent-stats/internal/indexing/domain"
	"github.com/Nathandelenclos/agent-stats/internal/kernel"
)

// The prompts table keeps each prompt's frustration signals (usage.Signals). No query reads them yet:
// they are indexed now for the sessions TUI (delivery step 2), so a re-index is not needed then (2026-11-21).
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
CREATE INDEX IF NOT EXISTS tool_calls_ts ON tool_calls (ts);
CREATE INDEX IF NOT EXISTS prompts_ts ON prompts (ts);
CREATE INDEX IF NOT EXISTS sessions_started ON sessions (started_at);
-- Published read model: the only part of the index other contexts may read. Prompt texts stay private.
CREATE VIEW IF NOT EXISTS usage_requests AS
	SELECT r.ts, r.model, r.input, r.output, r.cache_read, r.cache_write, r.cost, r.priced, r.duration_ms, r.ttft_ms, r.error,
		s.harness, s.agent_type, s.folder
	FROM requests r JOIN sessions s ON s.id = r.session_id;
CREATE VIEW IF NOT EXISTS usage_tool_calls AS
	SELECT t.ts, t.name, t.server, t.args_chars, t.result_chars, t.is_error, t.duration_ms, s.harness
	FROM tool_calls t JOIN sessions s ON s.id = t.session_id;
`

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

// IndexedFiles returns every log file indexed so far, with its size and modification time at that point.
func (s *SQLiteIndex) IndexedFiles() (map[string]application.LogFile, error) {
	rows, err := s.db.Query(`SELECT path, harness, size, mtime FROM files`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	known := map[string]application.LogFile{}
	for rows.Next() {
		var f application.LogFile
		var harness string
		var mtime int64
		if err := rows.Scan(&f.Path, &harness, &f.Size, &mtime); err != nil {
			return nil, err
		}
		if f.Harness, err = kernel.ParseHarness(harness); err != nil {
			return nil, err
		}
		f.ModTime = time.Unix(0, mtime)
		known[f.Path] = f
	}
	return known, rows.Err()
}

// statements are the prepared statements that write one log, bound to the transaction of a batch.
type statements struct {
	deletes                              [4]*sql.Stmt // sessions, requests, tool_calls, prompts: the rows of a file
	session, request, tool, prompt, file *sql.Stmt
}

var deletedTables = [...]string{"sessions", "requests", "tool_calls", "prompts"}

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
	st.session = prep(`INSERT OR REPLACE INTO sessions VALUES (?,?,?,?,?,?,?,?,?,?)`)
	st.request = prep(`INSERT OR REPLACE INTO requests VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`)
	st.tool = prep(`INSERT OR REPLACE INTO tool_calls VALUES (?,?,?,?,?,?,?,?,?,?)`)
	st.prompt = prep(`INSERT OR REPLACE INTO prompts VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`)
	st.file = prep(`INSERT OR REPLACE INTO files VALUES (?,?,?,?)`)
	return st, err
}

func (st statements) close() {
	for _, stmt := range append(st.deletes[:], st.session, st.request, st.tool, st.prompt, st.file) {
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
	}
	for i, pr := range t.Prompts {
		sig := t.Signals[i]
		if _, err := st.prompt.Exec(path, pr.Key, ss.ID.String(), kernel.UnixMilli(pr.At), pr.Text, sig.Chars, sig.Words, sig.Yelling, sig.Profanity,
			sig.Anguish, sig.Negation, sig.Repeated, sig.Blame); err != nil {
			return err
		}
	}
	_, err := st.file.Exec(path, file.Harness.String(), file.Size, file.ModTime.UnixNano())
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
