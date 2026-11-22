package configfiles

import (
	"encoding/json"
	"errors"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode"

	"github.com/Nathandelenclos/agent-stats/internal/catalog/domain"
	"github.com/Nathandelenclos/agent-stats/internal/kernel"
)

// collector gathers the items of one harness and notes what it had to leave out.
type collector struct {
	home    string
	harness kernel.Harness
	items   []domain.Item
	skipped []string
	// confine, when set, is a real directory that everything read must stay in, whatever links lead elsewhere.
	confine string
	// pluginRoot, while a plugin is read, is its install directory, which the plugin declares: paths under it are shown
	// as pluginLabel and the path relative to it, never as the directory itself.
	pluginRoot, pluginLabel string
}

// inside reports whether path, once its symbolic links are resolved, lies in the confinement directory; a path
// outside it is noted and not read. A path that does not exist is inside: reading it finds nothing.
// ponytail: checked before reading, not atomically; a link swapped in between is out of scope for a read-only lister.
func (c *collector) inside(path string) bool { return c.insideAs(path, path) }

// insideAs is inside, naming shown in the note instead of path.
func (c *collector) insideAs(path, shown string) bool {
	if c.confine == "" {
		return true
	}
	real, err := filepath.EvalSymlinks(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return true
	case err != nil:
		c.problem(shown, err)
		return false
	case !within(real, c.confine):
		c.problem(shown, errOutsideDir)
		return false
	}
	return true
}

// show writes a path for display, the home directory as "~" and characters a terminal could misread as "?". A path in
// the plugin being read is written relative to it, under the plugin's name.
func (c *collector) show(path string) string {
	if c.pluginRoot != "" && within(path, c.pluginRoot) {
		rel, _ := filepath.Rel(c.pluginRoot, path)
		path = c.pluginLabel
		if rel != "." {
			path += ": " + filepath.ToSlash(rel)
		}
		return strings.Map(printable, path)
	}
	path = strings.Map(printable, path)
	if c.home != "" {
		if rel, err := filepath.Rel(c.home, path); err == nil && within(path, c.home) {
			return filepath.Join("~", rel)
		}
	}
	return path
}

func printable(r rune) rune {
	if unicode.IsPrint(r) {
		return r
	}
	return '?'
}

// problem notes a path that could not be used. A path that does not exist is not a problem: harnesses are optional.
func (c *collector) problem(path string, err error) {
	if err == nil || errors.Is(err, fs.ErrNotExist) {
		return
	}
	c.skipped = append(c.skipped, c.show(path)+": "+reason(err))
}

// add records an item. A name the domain refuses (control characters, too long) is skipped and noted.
func (c *collector) add(kind domain.Kind, name string, scope domain.Scope, origin domain.Name, source string, disabled bool) {
	n, err := domain.NewName(name)
	if err != nil {
		c.skipped = append(c.skipped, c.show(source)+": an item name is not usable")
		return
	}
	c.items = append(c.items, domain.Item{Harness: c.harness, Kind: kind, Name: n, Scope: scope, Origin: origin, Source: c.show(source), Disabled: disabled})
}

// serverNames adds one MCP server per name, defined in file.
func (c *collector) serverNames(names []string, scope domain.Scope, origin domain.Name, file string) {
	for _, n := range names {
		c.add(domain.MCPServer, n, scope, origin, file, false)
	}
}

// serversFile adds the MCP servers a JSON file defines: under "mcpServers", or as its top-level keys (a plugin's .mcp.json).
func (c *collector) serversFile(path string, scope domain.Scope, origin domain.Name) {
	c.serversFileAs(path, path, scope, origin)
}

// serversFileAs is serversFile for a file whose own path must not be shown, because it was declared: shown stands for
// it in the items and the notes.
func (c *collector) serversFileAs(path, shown string, scope domain.Scope, origin domain.Name) {
	var raw map[string]jsonValue
	if !c.insideAs(path, shown) {
		return
	}
	if err := readJSON(path, &raw); err != nil {
		c.problem(shown, err)
		return
	}
	if nested, ok := raw["mcpServers"]; ok {
		var servers map[string]jsonValue
		if json.Unmarshal(nested, &servers) != nil {
			c.problem(shown, errMalformed)
			return
		}
		raw = servers
	}
	c.serverNames(keys(raw), scope, origin, shown)
}

// skillsDir adds the skills found one level below dir: a directory with a SKILL.md, whose frontmatter may name it.
// A plugin's skills are named "plugin:skill".
func (c *collector) skillsDir(dir string, scope domain.Scope, origin domain.Name) {
	if !c.inside(dir) {
		return
	}
	entries, err := listDir(dir)
	c.problem(dir, err)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		if !isDir(path) || !c.inside(path) || !c.inside(filepath.Join(path, "SKILL.md")) {
			continue
		}
		head, err := readHead(filepath.Join(path, "SKILL.md"), maxHeadBytes)
		if err != nil {
			c.problem(filepath.Join(path, "SKILL.md"), err) // a directory without SKILL.md is not a skill: silent
			continue
		}
		c.add(domain.Skill, qualified(origin, frontmatterName(head, e.Name())), scope, origin, path, false)
	}
}

// agentFiles adds the agents defined by the files of dir with the extension ext; read says how to find a name in one.
func (c *collector) agentFiles(dir, ext string, scope domain.Scope, origin domain.Name, nameIn func(head []byte, fallback string) string) {
	if !c.inside(dir) {
		return
	}
	entries, err := listDir(dir)
	c.problem(dir, err)
	for _, e := range entries {
		base, ok := strings.CutSuffix(e.Name(), ext)
		if !ok || strings.HasPrefix(base, ".") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		if !c.inside(path) {
			continue
		}
		head, err := readHead(path, maxHeadBytes)
		if err != nil {
			c.problem(path, err)
			continue
		}
		c.add(domain.Agent, qualified(origin, nameIn(head, base)), scope, origin, path, false)
	}
}

func qualified(origin domain.Name, name string) string {
	if origin.IsZero() {
		return name
	}
	return domain.PluginName(origin) + ":" + name
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func keys[V any](m map[string]V) []string { return slices.Sorted(maps.Keys(m)) }
