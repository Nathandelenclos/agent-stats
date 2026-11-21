package ingest

import (
	"os"
	"path/filepath"
	"strings"
)

// Source is a directory tree of session logs written by one harness.
type Source struct {
	Harness string
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
		{"claude", filepath.Join(claude, "projects"), jsonl, ParseClaude},
		{"codex", filepath.Join(codex, "sessions"), rollout, ParseCodex},
		{"codex", filepath.Join(codex, "archived_sessions"), rollout, ParseCodex},
		{"omp", filepath.Join(home, ".omp", "agent", "sessions"), jsonl, ParsePi("omp")},
		{"pi", filepath.Join(home, ".pi", "agent", "sessions"), jsonl, ParsePi("pi")},
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
