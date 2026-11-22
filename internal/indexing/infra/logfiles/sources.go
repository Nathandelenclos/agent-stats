package logfiles

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/Nathandelenclos/agent-stats/internal/indexing/application"
	"github.com/Nathandelenclos/agent-stats/internal/indexing/domain"
	"github.com/Nathandelenclos/agent-stats/internal/kernel"
)

// Source is a directory tree of session logs written by one harness.
type Source struct {
	Harness kernel.Harness
	Root    string
	Match   func(path string) bool
	Parse   Parser
	// Database, when set, makes Root the directory of SQLite databases that hold the sessions of the harness,
	// instead of a tree of logs: Match and Parse are not used.
	Database *OpenCodeDatabase
}

// DefaultSources lists the standard log locations, honouring CLAUDE_CONFIG_DIR and CODEX_HOME.
func DefaultSources(home string) []Source {
	claude := envOr("CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude"))
	codex := envOr("CODEX_HOME", filepath.Join(home, ".codex"))
	jsonl := func(p string) bool { return strings.HasSuffix(p, ".jsonl") }
	rollout := func(p string) bool {
		b := filepath.Base(p)
		return strings.HasPrefix(b, "rollout-") && (strings.HasSuffix(b, ".jsonl") || strings.HasSuffix(b, ".jsonl.zst"))
	}
	data := envOr("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	openCodeData, kiloData := filepath.Join(data, "opencode"), filepath.Join(data, "kilo")
	return []Source{
		{Harness: kernel.Claude, Root: filepath.Join(claude, "projects"), Match: jsonl, Parse: ParseClaude},
		{Harness: kernel.Codex, Root: filepath.Join(codex, "sessions"), Match: rollout, Parse: ParseCodex},
		{Harness: kernel.Codex, Root: filepath.Join(codex, "archived_sessions"), Match: rollout, Parse: ParseCodex},
		{Harness: kernel.OMP, Root: filepath.Join(home, ".omp", "agent", "sessions"), Match: jsonl, Parse: ParsePi(kernel.OMP)},
		{Harness: kernel.Pi, Root: filepath.Join(home, ".pi", "agent", "sessions"), Match: jsonl, Parse: ParsePi(kernel.Pi)},
		{Harness: kernel.OpenCode, Root: openCodeData, Database: &OpenCodeDatabase{Prefix: "opencode", Override: os.Getenv("OPENCODE_DB")}},
		{Harness: kernel.Kilo, Root: kiloData, Database: &OpenCodeDatabase{Prefix: "kilo", Fallback: "opencode", Override: os.Getenv("KILO_DB")}},
		{Harness: kernel.Gemini, Root: filepath.Join(envOr("GEMINI_CLI_HOME", home), ".gemini", "tmp"), Match: IsGeminiConversation, Parse: ParseGemini},
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// FileSessionLogs reads session logs from the harnesses' directories on the local disk.
// Read may be called from several goroutines at once, as IndexSessions does.
type FileSessionLogs struct {
	Sources []Source
	mu      sync.RWMutex
	parsers map[string]Parser // by path, as of the last List
	dbLogs  map[string]dbLog  // the sessions of databases, by their path "<database>#<session id>"
}

// dbLog is a session that lives in a database.
type dbLog struct {
	db      *OpenCodeDatabase
	path    string
	harness kernel.Harness
	session dbSession
}

// NewFileSessionLogs reads the logs found under sources.
func NewFileSessionLogs(sources []Source) *FileSessionLogs {
	return &FileSessionLogs{Sources: sources, parsers: map[string]Parser{}, dbLogs: map[string]dbLog{}}
}

// List walks every source. A harness that is not installed is not an error.
func (l *FileSessionLogs) List() ([]application.LogFile, []error, error) {
	var files []application.LogFile
	var problems []error
	parsers := map[string]Parser{}
	dbLogs := map[string]dbLog{}
	for _, src := range l.Sources {
		if src.Database != nil {
			for _, path := range src.Database.paths(src.Root) {
				sessions, dbProblems := src.Database.list(path)
				problems = append(problems, dbProblems...)
				for _, s := range sessions {
					log := path + DBSessionSep + s.id
					dbLogs[log] = dbLog{db: src.Database, path: path, harness: src.Harness, session: s}
					files = append(files, application.LogFile{Path: log, Harness: src.Harness, Size: s.size, ModTime: s.mod,
						Weight: min(s.size*estimatedMessageBytes, maxLogBytes)})
				}
			}
			continue
		}
		err := filepath.WalkDir(src.Root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				if os.IsNotExist(err) || os.IsPermission(err) {
					return nil
				}
				return err
			}
			if d.IsDir() || !src.Match(path) {
				return nil
			}
			if t := d.Type(); !t.IsRegular() && t&fs.ModeSymlink == 0 {
				problems = append(problems, fmt.Errorf("%s: %w", path, ErrNotRegular))
				return nil
			}
			info, err := d.Info()
			if err != nil {
				problems = append(problems, fmt.Errorf("%s: %w", path, err))
				return nil
			}
			parsers[path] = src.Parse
			files = append(files, application.LogFile{Path: path, Harness: src.Harness, Size: info.Size(), ModTime: info.ModTime(), Weight: logWeight(path, info.Size())})
			return nil
		})
		if err != nil {
			return files, problems, err
		}
	}
	l.mu.Lock()
	l.parsers, l.dbLogs = parsers, dbLogs
	l.mu.Unlock()
	return files, problems, nil
}

// Read parses a file returned by List.
func (l *FileSessionLogs) Read(f application.LogFile) (domain.Transcript, error) {
	l.mu.RLock()
	p, ok := l.parsers[f.Path]
	db, inDB := l.dbLogs[f.Path]
	l.mu.RUnlock()
	if inDB {
		return db.db.read(db.path, db.harness, db.session)
	}
	if !ok {
		return domain.Transcript{}, fmt.Errorf("%s was not listed", f.Path)
	}
	return ParseFile(f.Path, p)
}
