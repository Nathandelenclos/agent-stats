package configfiles

import (
	"encoding/json"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Nathandelenclos/agent-stats/internal/catalog/domain"
)

// omp lists what oh-my-pi has installed: skills (its own and the shared ~/.agents/skills), agents, MCP servers from
// mcp.json, and plugins from installed_plugins.json.
func (i Inventory) omp(c *collector) {
	r := i.Roots
	agent := filepath.Join(r.Home, ".omp", "agent")
	c.serversFile(filepath.Join(agent, "mcp.json"), domain.ScopeUser, domain.Name{})
	c.skillsDir(filepath.Join(agent, "skills"), domain.ScopeUser, domain.Name{})
	c.skillsDir(filepath.Join(r.Home, ".agents", "skills"), domain.ScopeUser, domain.Name{})
	c.agentFiles(filepath.Join(agent, "agents"), ".md", domain.ScopeUser, domain.Name{}, frontmatterName)
	i.pluginsIn(c, filepath.Join(r.Home, ".omp", "plugins"), "")
	if p := r.Project; p != "" {
		c.serversFile(filepath.Join(p, ".omp", "mcp.json"), domain.ScopeProject, domain.Name{})
		c.serversFile(filepath.Join(p, ".mcp.json"), domain.ScopeProject, domain.Name{})
		c.skillsDir(filepath.Join(p, ".omp", "skills"), domain.ScopeProject, domain.Name{})
		c.skillsDir(filepath.Join(p, ".agents", "skills"), domain.ScopeProject, domain.Name{})
		c.agentFiles(filepath.Join(p, ".omp", "agents"), ".md", domain.ScopeProject, domain.Name{}, frontmatterName)
	}
}

// pi lists what pi has installed: skills and the packages of its settings. pi has no MCP and no subagents of its own.
func (i Inventory) pi(c *collector) {
	r := i.Roots
	c.skillsDir(filepath.Join(r.Home, ".pi", "agent", "skills"), domain.ScopeUser, domain.Name{})
	c.skillsDir(filepath.Join(r.Home, ".agents", "skills"), domain.ScopeUser, domain.Name{})
	c.piPackages(filepath.Join(r.Home, ".pi", "agent", "settings.json"), domain.ScopeUser)
	if p := r.Project; p != "" {
		c.skillsDir(filepath.Join(p, ".pi", "skills"), domain.ScopeProject, domain.Name{})
		c.skillsDir(filepath.Join(p, ".agents", "skills"), domain.ScopeProject, domain.Name{})
		c.piPackages(filepath.Join(p, ".pi", "settings.json"), domain.ScopeProject)
	}
}

// piPackages adds the packages of a pi settings.json: each is a source string, or an object with a "source".
func (c *collector) piPackages(path string, scope domain.Scope) {
	var s struct {
		Packages []jsonValue `json:"packages"`
	}
	if err := readJSON(path, &s); err != nil {
		c.problem(path, err)
		return
	}
	for _, raw := range s.Packages {
		var src string
		var obj struct {
			Source string `json:"source"`
		}
		if json.Unmarshal(raw, &src) != nil {
			if json.Unmarshal(raw, &obj) != nil {
				continue
			}
			src = obj.Source
		}
		c.add(domain.Plugin, packageLabel(src), scope, domain.Name{}, path, false)
	}
}

// npmSource is a package name, with or without "npm:", scope and version: the whole source is then safe to show. A
// version starts like one (a digit or a range operator), so that "user@host" is not taken for "name@version".
var npmSource = regexp.MustCompile(`^(?:npm:)?(?:@[A-Za-z0-9._-]+/)?[A-Za-z0-9._-]+(?:@[\^~<>=]*[0-9*][A-Za-z0-9.*+-]*)?$`)

// pathName is what the last path segment of a source must look like to be shown.
var pathName = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// packageLabel is how a pi package source is shown. A source can hold anything, a token in a path as well as a
// password in a URL, so nothing of it is shown that is not needed to recognise the package: a package name as is,
// otherwise the last path segment (without ".git") when it is a plain name, else "<package>". The host, the user,
// the password, the query, the fragment and the other segments never appear. A source string may hold several
// blank-separated sources, each labelled alone.
func packageLabel(src string) string {
	words := strings.Fields(src)
	for i, w := range words {
		words[i] = wordLabel(w)
	}
	if len(words) == 0 {
		return "<package>"
	}
	return strings.Join(words, " ")
}

func wordLabel(w string) string {
	if j := strings.IndexAny(w, "?#"); j >= 0 {
		w = w[:j]
	}
	if npmSource.MatchString(w) {
		return w
	}
	last := w[strings.LastIndex(w, "/")+1:]
	if name := strings.TrimSuffix(last, ".git"); name != "" && pathName.MatchString(name) {
		return name
	}
	return "<package>"
}
