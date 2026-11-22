package configfiles

import (
	"fmt"
	"path/filepath"

	"github.com/Nathandelenclos/agent-stats/internal/catalog/application"
	"github.com/Nathandelenclos/agent-stats/internal/kernel"
)

// Roots locate the configuration of the harnesses.
type Roots struct {
	Home       string
	ClaudeDir  string // ~/.claude, or $CLAUDE_CONFIG_DIR
	ClaudeJSON string // ~/.claude.json, or $CLAUDE_CONFIG_DIR/.claude.json
	CodexDir   string // ~/.codex, or $CODEX_HOME
	// Project is the folder whose project-level configuration is read; empty for none.
	Project string
}

// DefaultRoots returns the standard locations, honouring CLAUDE_CONFIG_DIR and CODEX_HOME as the log sources do.
// A project that is the home directory has no project-level configuration of its own: it is the user's.
func DefaultRoots(home, project string, getenv func(string) string) Roots {
	r := Roots{Home: home, ClaudeDir: filepath.Join(home, ".claude"), ClaudeJSON: filepath.Join(home, ".claude.json"), CodexDir: filepath.Join(home, ".codex")}
	if d := getenv("CLAUDE_CONFIG_DIR"); d != "" {
		r.ClaudeDir, r.ClaudeJSON = d, filepath.Join(d, ".claude.json")
	}
	if d := getenv("CODEX_HOME"); d != "" {
		r.CodexDir = d
	}
	if project != "" && filepath.Clean(project) != filepath.Clean(home) {
		r.Project = filepath.Clean(project)
	}
	return r
}

// Inventory lists what the harnesses have installed, reading their configuration directories.
type Inventory struct{ Roots Roots }

var _ application.Inventory = Inventory{}

// Installed lists the items of h. A configuration that is missing is not an error (harnesses are optional); one that
// cannot be used is left out and noted in Skipped.
func (i Inventory) Installed(h kernel.Harness) (application.Installed, error) {
	c := &collector{home: i.Roots.Home, harness: h}
	switch h {
	case kernel.Claude:
		i.claude(c)
	case kernel.Codex:
		i.codex(c)
	case kernel.OMP:
		i.omp(c)
	case kernel.Pi:
		i.pi(c)
	case kernel.OpenCode, kernel.Kilo, kernel.Gemini, kernel.Copilot, kernel.Zed, kernel.Grok:
		// ponytail: no configuration reader yet; these harnesses show up in the catalogue through the usage their logs record.
	default:
		return application.Installed{}, fmt.Errorf("no inventory for harness %q", h)
	}
	return application.Installed{Items: c.items, Skipped: c.skipped}, nil
}
