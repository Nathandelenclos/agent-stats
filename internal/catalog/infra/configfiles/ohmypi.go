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
		c.add(domain.Plugin, withoutCredentials(src), scope, domain.Name{}, path, false)
	}
}

// userinfo matches "user@" or "user:secret@" at the start of a source that has no "://".
var userinfo = regexp.MustCompile(`^[^\s/@:]+(?::[^\s/@]*)?@`)

// withoutCredentials drops what a package source may carry that is secret. A source string may hold several, so each
// blank-separated one is cleaned alone: a remote one (it has "://" or "@", or starts with "git:") loses everything from its first "?" or "#",
// then the user and password of every "scheme://user:secret@host" and of the forms without "://" that pi accepts
// ("git:user:token@host/path", "user:token@host:path", "user@host:path"). An npm name ("pkg@1.2", "@org/pkg") and a
// path are kept as is. A token written inside a path is not a credential form and is not recognised.
func withoutCredentials(src string) string {
	words := strings.Fields(src)
	for i, w := range words {
		isGit := strings.HasPrefix(strings.ToLower(w), "git:")
		if !isGit && !strings.Contains(w, "://") && !strings.Contains(w, "@") {
			continue
		}
		if j := strings.IndexAny(w, "?#"); j >= 0 {
			w = w[:j]
		}
		switch {
		case strings.Contains(w, "://"):
			w = stripURLUserinfo(w)
		case isGit:
			w = w[:len("git:")] + userinfo.ReplaceAllString(w[len("git:"):], "")
		case strings.Contains(w, ":") && !strings.HasPrefix(strings.ToLower(w), "npm:"):
			w = userinfo.ReplaceAllString(w, "")
		}
		words[i] = w
	}
	return strings.Join(words, " ")
}

// stripURLUserinfo removes the user and password of every "scheme://user:secret@host" in src.
func stripURLUserinfo(src string) string {
	for from := 0; ; {
		i := strings.Index(src[from:], "://")
		if i < 0 {
			return src
		}
		start := from + i + len("://")
		end := len(src)
		if j := strings.IndexAny(src[start:], "/?#"); j >= 0 {
			end = start + j
		}
		if at := strings.LastIndex(src[start:end], "@"); at >= 0 {
			src = src[:start] + src[start+at+1:]
			end -= at + 1
		}
		from = end
	}
}
