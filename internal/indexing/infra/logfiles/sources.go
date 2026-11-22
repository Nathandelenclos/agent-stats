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
	return []Source{
		{kernel.Claude, filepath.Join(claude, "projects"), jsonl, ParseClaude},
		{kernel.Codex, filepath.Join(codex, "sessions"), rollout, ParseCodex},
		{kernel.Codex, filepath.Join(codex, "archived_sessions"), rollout, ParseCodex},
		{kernel.OMP, filepath.Join(home, ".omp", "agent", "sessions"), jsonl, ParsePi(kernel.OMP)},
		{kernel.Pi, filepath.Join(home, ".pi", "agent", "sessions"), jsonl, ParsePi(kernel.Pi)},
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
}

// NewFileSessionLogs reads the logs found under sources.
func NewFileSessionLogs(sources []Source) *FileSessionLogs {
	return &FileSessionLogs{Sources: sources, parsers: map[string]Parser{}}
}

// List walks every source. A harness that is not installed is not an error.
func (l *FileSessionLogs) List() ([]application.LogFile, []error, error) {
	var files []application.LogFile
	var problems []error
	parsers := map[string]Parser{}
	for _, src := range l.Sources {
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
			files = append(files, application.LogFile{Path: path, Harness: src.Harness, Size: info.Size(), ModTime: info.ModTime()})
			return nil
		})
		if err != nil {
			return files, problems, err
		}
	}
	l.mu.Lock()
	l.parsers = parsers
	l.mu.Unlock()
	return files, problems, nil
}

// Read parses a file returned by List.
func (l *FileSessionLogs) Read(f application.LogFile) (domain.Transcript, error) {
	l.mu.RLock()
	p, ok := l.parsers[f.Path]
	l.mu.RUnlock()
	if !ok {
		return domain.Transcript{}, fmt.Errorf("%s was not listed", f.Path)
	}
	return ParseFile(f.Path, p)
}
