// Package store keeps parsed sessions in a local SQLite index and answers stats queries.
package store

import (
	"crypto/sha256"
	"database/sql"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/Nathandelenclos/agent-stats/internal/ingest"
	"github.com/Nathandelenclos/agent-stats/internal/pricing"
	_ "modernc.org/sqlite"
)

const schema = `
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
`

// Store is the SQLite index.
type Store struct {
	DB     *sql.DB
	prices pricing.Table
	user   string
}

// Open creates or opens the index at path.
func Open(path string, prices pricing.Table, user string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
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
	return &Store{DB: db, prices: prices, user: user}, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.DB.Close() }

// SyncReport says what a Sync did.
type SyncReport struct {
	Scanned, Parsed int
	Errors          []string
}

// Sync re-parses every log file whose size or mtime changed since the last run.
// A changed file is parsed again from the start and replaces its previous rows, which also covers truncation.
// Rows of files deleted by the harness are kept, so history survives log rotation.
// Rows are keyed by session, not file, so a log moved elsewhere (Codex archiving) is not counted twice.
func (s *Store) Sync(sources []ingest.Source, progress func(SyncReport)) (SyncReport, error) {
	known := map[string][2]int64{}
	rows, err := s.DB.Query(`SELECT path, size, mtime FROM files`)
	if err != nil {
		return SyncReport{}, err
	}
	for rows.Next() {
		var p string
		var size, mtime int64
		if err := rows.Scan(&p, &size, &mtime); err != nil {
			rows.Close()
			return SyncReport{}, err
		}
		known[p] = [2]int64{size, mtime}
	}
	rows.Close()

	var rep SyncReport
	for _, src := range sources {
		err := filepath.WalkDir(src.Root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				if os.IsNotExist(err) || os.IsPermission(err) {
					return nil // a harness that is not installed is not an error
				}
				return err
			}
			if d.IsDir() || !src.Match(path) {
				return nil
			}
			rep.Scanned++
			info, err := d.Info()
			if err != nil {
				return nil
			}
			stamp := [2]int64{info.Size(), info.ModTime().UnixNano()}
			if known[path] == stamp {
				return nil
			}
			parsed, err := ingest.ParseFile(path, src.Parse)
			if err == nil {
				err = s.replace(path, src.Harness, stamp, parsed)
			}
			if err != nil {
				rep.Errors = append(rep.Errors, fmt.Sprintf("%s: %v", path, err))
				return nil
			}
			rep.Parsed++
			if progress != nil && rep.Parsed%50 == 0 {
				progress(rep)
			}
			return nil
		})
		if err != nil {
			return rep, err
		}
	}
	return rep, nil
}

func (s *Store) replace(path, harness string, stamp [2]int64, p *ingest.Parsed) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, t := range []string{"sessions", "requests", "tool_calls", "prompts"} {
		if _, err := tx.Exec(`DELETE FROM `+t+` WHERE file = ?`, path); err != nil {
			return err
		}
	}
	ss := p.Session
	if _, err := tx.Exec(`INSERT OR REPLACE INTO sessions VALUES (?,?,?,?,?,?,?,?,?,?)`,
		ss.ID, path, harness, nullStr(ss.ParentID), ss.AgentType, ss.Title, ss.Folder, s.user, ss.Start, ss.End); err != nil {
		return err
	}

	reqStmt, err := tx.Prepare(`INSERT OR REPLACE INTO requests VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		return err
	}
	for _, r := range p.Requests {
		cost, priced := r.Cost, r.Cost > 0
		if !priced {
			cost, priced = s.prices.Cost(r.Model, r.Input, r.Output, r.CacheRead, r.CacheWrite, r.CacheWrite1h)
		}
		if _, err := reqStmt.Exec(path, r.Key, ss.ID, r.TS, r.Model, r.Provider, r.Input, r.Output, r.CacheRead, r.CacheWrite, r.Reasoning,
			cost, priced, nullFloat(r.DurationMs), nullFloat(r.TTFTMs), r.StopReason, nullStr(r.Error)); err != nil {
			return err
		}
	}

	toolStmt, err := tx.Prepare(`INSERT OR REPLACE INTO tool_calls VALUES (?,?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		return err
	}
	for _, t := range p.Tools {
		if _, err := toolStmt.Exec(path, t.Key, ss.ID, t.TS, t.Name, t.Server, t.ArgsChars, t.ResultChars, t.IsError, nullFloat(t.DurationMs)); err != nil {
			return err
		}
	}

	promptStmt, err := tx.Prepare(`INSERT OR REPLACE INTO prompts VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		return err
	}
	seen := map[[32]byte]bool{}
	for _, pr := range p.Prompts {
		sig := ingest.Analyze(pr.Text)
		h := sha256.Sum256([]byte(strings.ToLower(strings.Join(strings.Fields(pr.Text), " "))))
		repeated := seen[h]
		seen[h] = true
		if _, err := promptStmt.Exec(path, pr.Key, ss.ID, pr.TS, pr.Text, sig.Chars, sig.Words, sig.Yelling, sig.Profanity,
			sig.Anguish, sig.Negation, repeated, sig.Blame); err != nil {
			return err
		}
	}

	if _, err := tx.Exec(`INSERT OR REPLACE INTO files VALUES (?,?,?,?)`, path, harness, stamp[0], stamp[1]); err != nil {
		return err
	}
	return tx.Commit()
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nullFloat(f float64) any {
	if f <= 0 {
		return nil
	}
	return f
}
