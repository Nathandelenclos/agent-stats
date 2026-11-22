package configfiles

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"

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

// piPackages adds the packages of a pi settings.json: each is a source string, or an object with a "source". The
// source is declared by the user and can hold anything (a token in a path, a password in a URL), so nothing of it is
// shown, and no label is made from it: see piPackageLabel.
func (c *collector) piPackages(path string, scope domain.Scope) {
	var s struct {
		Packages []jsonValue `json:"packages"`
	}
	if err := readJSON(path, &s); err != nil {
		c.problem(path, err)
		return
	}
	for rank, raw := range s.Packages {
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
		c.add(domain.Plugin, c.piPackageLabel(filepath.Dir(path), src, rank+1), scope, domain.Name{}, path, false)
	}
}

// npmName is what a package name is allowed to look like (npm's own grammar: at most 214 characters, lower case, an
// optional "@scope/"). It guards the name read from a package.json and the directory looked for.
var npmName = regexp.MustCompile(`^(?:@[a-z0-9][a-z0-9._~-]*/)?[a-z0-9][a-z0-9._~-]*$`)

// npmSource is an npm package source: "npm:" is optional, and the name may be followed by a version, which starts like
// one (a digit or a range operator) so that "user@host" is not taken for "name@version". Group 1 is the name.
var npmSource = regexp.MustCompile(`^(?:npm:)?((?:@[a-z0-9][a-z0-9._~-]*/)?[a-z0-9][a-z0-9._~-]*)(?:@[\^~<>=]*[0-9*][A-Za-z0-9.*+-]*)?$`)

const maxNpmNameLen = 214

// piPackageLabel is how a pi package is shown: by the "name" its installed package.json declares, else by its rank in
// the settings file ("pi package #3"). Never by anything of the source string: a token or a password can be written
// anywhere in a source, in a path, a host, a query or a fragment, and no rule that picks a part of it is safe. The
// source only says which directory to look in, for an npm package; the label is read from that package.
// ponytail: only npm packages are looked up, in pi's npm directory (pi >= 0.83: <settings dir>/npm/node_modules); a
// git package, whose directory would have to be derived from its URL, keeps its rank.
func (c *collector) piPackageLabel(settingsDir, src string, rank int) string {
	if name, ok := installedPackageName(settingsDir, src); ok {
		return name
	}
	return fmt.Sprintf("pi package #%d", rank)
}

// installedPackageName reads the name of the npm package a source designates from its package.json: a regular file
// of bounded size that, once links are resolved, lies in pi's npm directory, naming itself by npm's grammar.
func installedPackageName(settingsDir, src string) (string, bool) {
	m := npmSource.FindStringSubmatch(src)
	if m == nil {
		return "", false
	}
	nodeModules, err := filepath.EvalSymlinks(filepath.Join(settingsDir, "npm", "node_modules"))
	if err != nil {
		return "", false
	}
	file, err := filepath.EvalSymlinks(filepath.Join(nodeModules, m[1], "package.json"))
	if err != nil || !within(file, nodeModules) {
		return "", false
	}
	b, err := readLimited(file, maxHeadBytes)
	if err != nil {
		return "", false
	}
	var p struct {
		Name string `json:"name"`
	}
	if json.Unmarshal(b, &p) != nil || len(p.Name) > maxNpmNameLen || !npmName.MatchString(p.Name) {
		return "", false
	}
	return p.Name, true
}
