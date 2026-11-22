package configfiles

import (
	"path/filepath"

	"github.com/Nathandelenclos/agent-stats/internal/catalog/domain"
)

// codex lists what Codex has installed: MCP servers and plugins from config.toml, skills (its own and the shared
// ~/.agents/skills) and agents (one TOML file each). The contents of plugins are not read.
func (i Inventory) codex(c *collector) {
	r := i.Roots
	c.codexConfig(filepath.Join(r.CodexDir, "config.toml"), domain.ScopeUser)
	c.skillsDir(filepath.Join(r.CodexDir, "skills"), domain.ScopeUser, domain.Name{})
	c.skillsDir(filepath.Join(r.Home, ".agents", "skills"), domain.ScopeUser, domain.Name{})
	c.agentFiles(filepath.Join(r.CodexDir, "agents"), ".toml", domain.ScopeUser, domain.Name{}, tomlName)
	if p := r.Project; p != "" {
		c.codexConfig(filepath.Join(p, ".codex", "config.toml"), domain.ScopeProject)
		c.skillsDir(filepath.Join(p, ".agents", "skills"), domain.ScopeProject, domain.Name{})
		c.agentFiles(filepath.Join(p, ".codex", "agents"), ".toml", domain.ScopeProject, domain.Name{}, tomlName)
	}
}

// codexConfig adds the [mcp_servers.<name>] and [plugins."<id>"] tables of a config.toml, with their "enabled" flag.
func (c *collector) codexConfig(path string, scope domain.Scope) {
	b, err := readLimited(path, maxConfigBytes)
	if err != nil {
		c.problem(path, err)
		return
	}
	for _, t := range tomlTables(b) {
		if len(t.Path) != 2 {
			continue
		}
		off := t.Enabled != nil && !*t.Enabled
		switch t.Path[0] {
		case "mcp_servers":
			c.add(domain.MCPServer, t.Path[1], scope, domain.Name{}, path, off)
		case "plugins":
			c.add(domain.Plugin, t.Path[1], scope, domain.Name{}, path, off)
		}
	}
}
