package configfiles

import (
	"encoding/json"
	"errors"
	"io/fs"
	"path/filepath"

	"github.com/Nathandelenclos/agent-stats/internal/catalog/domain"
)

// claude lists what Claude Code has installed: MCP servers (user, per project, .mcp.json), skills, agents and plugins
// with what each carries.
func (i Inventory) claude(c *collector) {
	r := i.Roots

	var cfg struct {
		MCPServers map[string]jsonValue `json:"mcpServers"`
		Projects   map[string]jsonValue `json:"projects"`
	}
	if err := readJSON(r.ClaudeJSON, &cfg); err != nil {
		c.problem(r.ClaudeJSON, err)
	}
	c.serverNames(keys(cfg.MCPServers), domain.ScopeUser, domain.Name{}, r.ClaudeJSON)
	for _, dir := range keys(cfg.Projects) { // a project's own servers; every project is listed once per server name
		var p struct {
			MCPServers map[string]jsonValue `json:"mcpServers"`
		}
		if json.Unmarshal(cfg.Projects[dir], &p) == nil {
			c.serverNames(keys(p.MCPServers), domain.ScopeProject, domain.Name{}, r.ClaudeJSON)
		}
	}

	c.skillsDir(filepath.Join(r.ClaudeDir, "skills"), domain.ScopeUser, domain.Name{})
	c.agentFiles(filepath.Join(r.ClaudeDir, "agents"), ".md", domain.ScopeUser, domain.Name{}, frontmatterName)
	i.claudePlugins(c)

	if p := r.Project; p != "" {
		c.serversFile(filepath.Join(p, ".mcp.json"), domain.ScopeProject, domain.Name{})
		c.skillsDir(filepath.Join(p, ".claude", "skills"), domain.ScopeProject, domain.Name{})
		c.agentFiles(filepath.Join(p, ".claude", "agents"), ".md", domain.ScopeProject, domain.Name{}, frontmatterName)
	}
}

// installedPlugins is the shape of installed_plugins.json, shared by Claude Code and oh-my-pi.
type installedPlugins struct {
	Plugins map[string][]struct {
		Scope       string `json:"scope"`
		InstallPath string `json:"installPath"`
		ProjectPath string `json:"projectPath"`
	} `json:"plugins"`
}

// claudePlugins lists the enabled plugins of the user, or of the project, and what they carry.
func (i Inventory) claudePlugins(c *collector) {
	i.pluginsIn(c, filepath.Join(i.Roots.ClaudeDir, "plugins"), filepath.Join(i.Roots.ClaudeDir, "settings.json"))
}

// pluginsIn lists the plugins installed_plugins.json of pluginsDir lists. A plugin that settings turns off is listed
// as disabled and its contents are not read. Install paths come from the file, so only those inside pluginsDir are read.
func (i Inventory) pluginsIn(c *collector, pluginsDir, settings string) {
	list := filepath.Join(pluginsDir, "installed_plugins.json")
	var installed installedPlugins
	if err := readJSON(list, &installed); err != nil {
		c.problem(list, err)
		return
	}
	var s struct {
		Enabled map[string]bool `json:"enabledPlugins"`
	}
	if settings != "" {
		if err := readJSON(settings, &s); err != nil {
			c.problem(settings, err)
		}
	}
	for _, id := range keys(installed.Plugins) {
		for _, in := range installed.Plugins[id] {
			scope := domain.ScopeUser
			if in.Scope != "user" {
				if in.ProjectPath == "" || in.ProjectPath != i.Roots.Project {
					continue // installed for another project
				}
				scope = domain.ScopeProject
			}
			name, err := domain.NewName(id)
			if err != nil {
				c.skipped = append(c.skipped, c.show(list)+": a plugin name is not usable")
				continue
			}
			off := false
			if enabled, ok := s.Enabled[id]; ok && !enabled {
				off = true
			}
			// The install path comes from the file: it is shown, and read, only once it is known to lie inside pluginsDir.
			root := filepath.Clean(in.InstallPath)
			ok := insidePluginsDir(root, pluginsDir)
			source := root
			if !ok {
				source = list
				c.skipped = append(c.skipped, c.show(list)+": plugin "+id+": install path "+reason(errOutsideDir))
			}
			c.add(domain.Plugin, id, scope, domain.Name{}, source, off)
			if off || !ok {
				continue
			}
			c.pluginContents(root, name)
		}
	}
}

// insidePluginsDir reports whether an install path is absolute and, once symbolic links are resolved, under
// pluginsDir (resolved too). A path that does not exist is accepted: reading it finds nothing.
func insidePluginsDir(root, pluginsDir string) bool {
	if !filepath.IsAbs(root) || !within(root, pluginsDir) {
		return false
	}
	base, err := filepath.EvalSymlinks(pluginsDir)
	if err != nil {
		base = filepath.Clean(pluginsDir)
	}
	real, err := filepath.EvalSymlinks(root)
	return errors.Is(err, fs.ErrNotExist) || err == nil && within(real, base)
}

// pluginContents lists the skills, agents and MCP servers a plugin carries.
func (c *collector) pluginContents(root string, plugin domain.Name) {
	// everything read below must stay in the plugin once links are followed
	c.confine = root
	if real, err := filepath.EvalSymlinks(root); err == nil {
		c.confine = real
	}
	defer func() { c.confine = "" }()
	c.skillsDir(filepath.Join(root, "skills"), domain.ScopePlugin, plugin)
	c.agentFiles(filepath.Join(root, "agents"), ".md", domain.ScopePlugin, plugin, frontmatterName)
	c.serversFile(filepath.Join(root, ".mcp.json"), domain.ScopePlugin, plugin)

	// plugin.json may declare servers inline, as a path, or as a list of either
	manifest := filepath.Join(root, ".claude-plugin", "plugin.json")
	if !c.inside(manifest) {
		return
	}
	var m struct {
		MCPServers jsonValue `json:"mcpServers"`
	}
	if err := readJSON(manifest, &m); err != nil {
		c.problem(manifest, err)
		return
	}
	var sources []jsonValue
	if len(m.MCPServers) > 0 && m.MCPServers[0] == '[' {
		if json.Unmarshal(m.MCPServers, &sources) != nil {
			c.problem(manifest, errMalformed)
			return
		}
	} else if len(m.MCPServers) > 0 {
		sources = []jsonValue{m.MCPServers}
	}
	for _, src := range sources {
		var path string
		if json.Unmarshal(src, &path) == nil {
			file := filepath.Join(root, path)
			if !within(file, root) {
				c.problem(file, errOutsideDir)
				continue
			}
			c.serversFile(file, domain.ScopePlugin, plugin)
			continue
		}
		var inline map[string]jsonValue
		if json.Unmarshal(src, &inline) != nil {
			c.problem(manifest, errMalformed)
			continue
		}
		c.serverNames(keys(inline), domain.ScopePlugin, plugin, manifest)
	}
}
