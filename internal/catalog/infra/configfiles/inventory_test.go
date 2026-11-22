package configfiles

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"

	"github.com/Nathandelenclos/agent-stats/internal/catalog/application"
	"github.com/Nathandelenclos/agent-stats/internal/catalog/domain"
	"github.com/Nathandelenclos/agent-stats/internal/kernel"
)

const fixtures = "../../../../testdata/catalog"

// fixtureRoots copies the fixture configuration trees into a temp dir (their plugin install paths name it) and
// returns the roots that read it.
func fixtureRoots(t *testing.T) Roots {
	t.Helper()
	dir := t.TempDir()
	if err := os.CopyFS(dir, os.DirFS(fixtures)); err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(dir, "home")
	list := filepath.Join(home, ".claude", "plugins", "installed_plugins.json")
	b, err := os.ReadFile(list)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(list, []byte(strings.ReplaceAll(string(b), "@HOME@", home)), 0o644); err != nil {
		t.Fatal(err)
	}
	return DefaultRoots(home, filepath.Join(dir, "project"), func(string) string { return "" })
}

func installed(t *testing.T, r Roots, h kernel.Harness) application.Installed {
	t.Helper()
	in, err := Inventory{Roots: r}.Installed(h)
	if err != nil {
		t.Fatal(err)
	}
	return in
}

// describe lists items as "kind:name@scope", plus "(plugin)" for what a plugin carries and "(off)" for the disabled.
func describe(in application.Installed) []string {
	var out []string
	for _, it := range in.Items {
		s := fmt.Sprintf("%s:%s@%s", it.Kind, it.Name, it.Scope)
		if !it.Origin.IsZero() {
			s += "(" + it.Origin.String() + ")"
		}
		if it.Disabled {
			s += "(off)"
		}
		out = append(out, s)
	}
	slices.Sort(out)
	return out
}

func expect(t *testing.T, got []string, want ...string) {
	t.Helper()
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("items\n got %q\nwant %q", got, want)
	}
}

// noSecret fails when anything an inventory returns (items and skipped notes) holds a fixture secret.
func noSecret(t *testing.T, in application.Installed) {
	t.Helper()
	all := fmt.Sprintf("%+v %q", in.Items, in.Skipped)
	if strings.Contains(all, "SECRET") {
		t.Errorf("a secret reached the inventory: %s", all)
	}
}

func TestClaudeInventoryListsEveryKindWithItsScope(t *testing.T) {
	// Given a Claude Code configuration with servers (user, per project, .mcp.json), skills, agents and plugins
	r := fixtureRoots(t)

	// When its inventory is read
	in := installed(t, r, kernel.Claude)

	// Then each item is there under the name Claude Code uses, with the scope it is defined in
	expect(t, describe(in),
		"mcp:docs@user", "mcp:remote@user", "mcp:local-db@project", "mcp:project-server@project",
		"skill:review-notes@user", "skill:frontmatter-name@user", "skill:no-frontmatter@user", "skill:project-skill@project",
		"agent:reviewer@user", "agent:plain-agent@user", "agent:project-agent@project",
		"plugin:demo@market@user", "plugin:off@market@user(off)", "plugin:escape@market@user",
		"plugin:missing@market@user", "plugin:manifest@market@user", "plugin:named@market@user", "skill:named:kept@plugin(named@market)",
		"mcp:helper@plugin(demo@market)", "mcp:inline-server@plugin(demo@market)",
		"skill:demo:audit@plugin(demo@market)", "agent:demo:checker@plugin(demo@market)")
}

func TestClaudeInventoryNeverReadsASecret(t *testing.T) {
	// Given configurations whose MCP env, headers and URLs carry credentials
	r := fixtureRoots(t)

	// When every harness is inventoried
	for _, h := range kernel.Harnesses {
		in := installed(t, r, h)

		// Then no secret is anywhere in what comes back
		noSecret(t, in)
	}
}

// hasItemNamed reports whether any item of the inventory has that name.
func hasItemNamed(in application.Installed, name string) bool {
	return slices.ContainsFunc(in.Items, func(it domain.Item) bool { return it.Name.String() == name })
}

func TestPluginThatEscapesItsDirectoryIsListedButNotRead(t *testing.T) {
	// Given a plugin whose install path is /etc, outside the plugins directory
	in := installed(t, fixtureRoots(t), kernel.Claude)

	// Then the plugin is listed, and the escape is noted
	if !slices.Contains(describe(in), "plugin:escape@market@user") {
		t.Error("the escaping plugin is not listed")
	}
	if !slices.ContainsFunc(in.Skipped, func(s string) bool {
		return strings.HasSuffix(s, "installed_plugins.json: plugin escape@market: install path points outside its directory")
	}) {
		t.Errorf("skipped: %q", in.Skipped)
	}
}

// withInstalledPlugins adds entries (id -> installPath, all user scope) to the plugins the Claude fixture lists.
func withInstalledPlugins(t *testing.T, r Roots, extra map[string]string) {
	t.Helper()
	list := filepath.Join(r.ClaudeDir, "plugins", "installed_plugins.json")
	var doc struct {
		Version int                         `json:"version"`
		Plugins map[string][]map[string]any `json:"plugins"`
	}
	b, err := os.ReadFile(list)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	for id, path := range extra {
		doc.Plugins[id] = []map[string]any{{"scope": "user", "installPath": path}}
	}
	out, _ := json.Marshal(doc)
	mustWrite(t, list, string(out))
}

func TestPluginInstallPathThatIsRejectedIsNeverShown(t *testing.T) {
	// Given plugins whose install path is a URL, a relative path and a path outside the plugins directory, each holding a marker
	r := fixtureRoots(t)
	withInstalledPlugins(t, r, map[string]string{
		"url@market":  "https://host/SECRET-URL-PATH?token=SECRET-URL-QUERY",
		"rel@market":  "../SECRET-REL",
		"away@market": "/SECRET-ABS/elsewhere",
	})

	// When the inventory is read
	in := installed(t, r, kernel.Claude)

	// Then the plugins are listed, no marker is anywhere, and each note names the file and the plugin
	noSecret(t, in)
	for _, id := range []string{"url@market", "rel@market", "away@market"} {
		if !slices.Contains(describe(in), "plugin:"+id+"@user") {
			t.Errorf("%s is not listed: %q", id, describe(in))
		}
		want := "installed_plugins.json: plugin " + id + ": install path points outside its directory"
		if !slices.ContainsFunc(in.Skipped, func(s string) bool { return strings.HasSuffix(s, want) }) {
			t.Errorf("no note %q in %q", want, in.Skipped)
		}
	}
}

func TestPluginManifestPointingOutOfThePluginNamesNeitherTheTargetNorItsPath(t *testing.T) {
	// Given a plugin whose manifest declares the server file "../../SECRET-MANIFEST.json"
	in := installed(t, fixtureRoots(t), kernel.Claude)

	// Then the file is refused, and the note names the manifest and the plugin, never the declared path
	want := "plugin manifest@market: .claude-plugin/plugin.json: a declared server file points outside its plugin"
	if !slices.ContainsFunc(in.Skipped, func(n string) bool { return strings.HasSuffix(n, want) }) {
		t.Errorf("no note %q in %q", want, in.Skipped)
	}
	noSecret(t, in)
}

func TestPluginWhoseInstallPathDoesNotExistIsNotInstalledAndShowsNoPath(t *testing.T) {
	// Given a plugin whose install path lies under the plugins directory but does not exist
	in := installed(t, fixtureRoots(t), kernel.Claude)

	// Then it is reported as not installed, by file and plugin name, and read as nothing
	want := "installed_plugins.json: plugin missing@market: not installed, its install path is not an existing directory"
	if !slices.ContainsFunc(in.Skipped, func(n string) bool { return strings.HasSuffix(n, want) }) {
		t.Errorf("no note %q in %q", want, in.Skipped)
	}
	noSecret(t, in)
}

func TestPluginIsShownByItsNameAndScopeAlone(t *testing.T) {
	// Given plugins installed in directories with arbitrary names, one of them holding a skill and a malformed manifest
	in := installed(t, fixtureRoots(t), kernel.Claude)

	// Then the plugin, whatever its install path, and what it carries show nothing of that path
	for _, it := range in.Items {
		switch {
		case it.Kind == domain.Plugin && it.Source != "plugin "+it.Name.String()+" (user)":
			t.Errorf("plugin %s: source %q", it.Name, it.Source)
		case it.Scope == domain.ScopePlugin && !strings.HasPrefix(it.Source, "plugin "+it.Origin.String()+": "):
			t.Errorf("%s %s: source %q", it.Kind, it.Name, it.Source)
		}
	}
	if !slices.ContainsFunc(in.Skipped, func(n string) bool { return n == "plugin named@market: .claude-plugin/plugin.json: malformed" }) {
		t.Errorf("notes: %q", in.Skipped)
	}
	noSecret(t, in)
}

func TestPluginReachedThroughASymbolicLinkOutOfItsDirectoryIsNotRead(t *testing.T) {
	// Given a plugin whose install path is a link, inside the plugins directory, to a directory outside it that holds servers and skills
	r := fixtureRoots(t)
	outside := filepath.Join(t.TempDir(), "outside")
	mustMkdir(t, filepath.Join(outside, "skills", "stolen-skill"))
	mustWrite(t, filepath.Join(outside, "skills", "stolen-skill", "SKILL.md"), "x\n")
	mustWrite(t, filepath.Join(outside, ".mcp.json"), `{"stolen-server": {}}`)
	link := filepath.Join(r.ClaudeDir, "plugins", "cache", "linked")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	withInstalledPlugins(t, r, map[string]string{"linked@market": link})

	// When the inventory is read
	in := installed(t, r, kernel.Claude)

	// Then the plugin is listed, nothing it points to is, and the note holds no path
	if !slices.Contains(describe(in), "plugin:linked@market@user") || hasItemNamed(in, "stolen-server") || hasItemNamed(in, "linked:stolen-skill") {
		t.Errorf("items: %q", describe(in))
	}
	if notes := strings.Join(in.Skipped, "\n"); !strings.Contains(notes, "plugin linked@market: install path points outside its directory") || strings.Contains(notes, outside) {
		t.Errorf("notes: %q", in.Skipped)
	}
}

func TestFilesOfAPluginThatLinkOutOfItAreNotRead(t *testing.T) {
	// Given a plugin whose .mcp.json, skills directory and agent file are links to files outside the plugin
	r := fixtureRoots(t)
	root := filepath.Join(r.ClaudeDir, "plugins", "cache", "leaky")
	outside := t.TempDir()
	mustWrite(t, filepath.Join(outside, "servers.json"), `{"stolen-server": {}}`)
	mustMkdir(t, filepath.Join(outside, "skills", "stolen-skill"))
	mustWrite(t, filepath.Join(outside, "skills", "stolen-skill", "SKILL.md"), "x\n")
	mustWrite(t, filepath.Join(outside, "stolen-agent.md"), "x\n")
	mustMkdir(t, filepath.Join(root, "agents"))
	for link, target := range map[string]string{
		filepath.Join(root, ".mcp.json"):                 filepath.Join(outside, "servers.json"),
		filepath.Join(root, "skills"):                    filepath.Join(outside, "skills"),
		filepath.Join(root, "agents", "stolen-agent.md"): filepath.Join(outside, "stolen-agent.md"),
	} {
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
	}
	withInstalledPlugins(t, r, map[string]string{"leaky@market": root})

	// When the inventory is read
	in := installed(t, r, kernel.Claude)

	// Then the plugin is listed and nothing the links lead to is
	for _, n := range []string{"stolen-server", "leaky:stolen-skill", "leaky:stolen-agent"} {
		if hasItemNamed(in, n) {
			t.Errorf("%s was read through a link: %q", n, describe(in))
		}
	}
}

func TestPluginManifestLinkedOutOfThePluginIsNotRead(t *testing.T) {
	// Given a plugin whose .claude-plugin/plugin.json is a link to a file outside it, declaring a server
	r := fixtureRoots(t)
	root := filepath.Join(r.ClaudeDir, "plugins", "cache", "linked-manifest")
	outside := filepath.Join(t.TempDir(), "plugin.json")
	mustWrite(t, outside, `{"mcpServers": {"stolen-inline": {}}}`)
	mustMkdir(t, filepath.Join(root, ".claude-plugin"))
	if err := os.Symlink(outside, filepath.Join(root, ".claude-plugin", "plugin.json")); err != nil {
		t.Fatal(err)
	}
	withInstalledPlugins(t, r, map[string]string{"linkedmanifest@market": root})

	// When the inventory is read, then the plugin is listed and the server its manifest declares is not
	in := installed(t, r, kernel.Claude)
	if !slices.Contains(describe(in), "plugin:linkedmanifest@market@user") || hasItemNamed(in, "stolen-inline") {
		t.Errorf("items: %q", describe(in))
	}
}

func TestPluginLinkedWithinItsDirectoryIsStillRead(t *testing.T) {
	// Given a plugin whose skills directory is a link to another directory of the same plugin
	r := fixtureRoots(t)
	root := filepath.Join(r.ClaudeDir, "plugins", "cache", "tidy")
	mustMkdir(t, filepath.Join(root, "shared", "kept"))
	mustWrite(t, filepath.Join(root, "shared", "kept", "SKILL.md"), "x\n")
	if err := os.Symlink(filepath.Join(root, "shared"), filepath.Join(root, "skills")); err != nil {
		t.Fatal(err)
	}
	withInstalledPlugins(t, r, map[string]string{"tidy@market": root})

	// When the inventory is read, then its skill is listed
	if in := installed(t, r, kernel.Claude); !hasItemNamed(in, "tidy:kept") {
		t.Errorf("items: %q", describe(in))
	}
}

func TestDisabledPluginIsListedWithoutItsContents(t *testing.T) {
	// Given a plugin that the settings turn off, which has a skill on disk
	in := installed(t, fixtureRoots(t), kernel.Claude)

	// Then it is listed as disabled and its skill is not
	if !slices.Contains(describe(in), "plugin:off@market@user(off)") || hasItemNamed(in, "off:hidden") {
		t.Errorf("items: %q", describe(in))
	}
}

func TestPluginInstalledForAnotherProjectIsLeftOut(t *testing.T) {
	// Given a plugin installed for a project other than the one read
	in := installed(t, fixtureRoots(t), kernel.Claude)

	// Then it is not listed
	if hasItemNamed(in, "elsewhere@market") {
		t.Errorf("items: %q", describe(in))
	}
}

func TestCodexInventoryReadsTablesAndSkipsMultilineStrings(t *testing.T) {
	// Given a config.toml with servers (one quoted, one disabled), plugins, a table-looking line inside a multi-line
	// string, and an array of tables
	in := installed(t, fixtureRoots(t), kernel.Codex)

	// Then servers and plugins are listed with their state, and the spoofed header is not a server
	expect(t, describe(in),
		"mcp:github@user", "mcp:quoted.name@user", "mcp:paused@user(off)",
		"plugin:helper@market@user", "plugin:stopped@market@user(off)",
		"agent:planner@user", "agent:fallback-agent@user",
		"skill:codex-skill@user", "skill:shared-skill@user")
}

func TestOhMyPiInventory(t *testing.T) {
	// Given oh-my-pi's mcp.json, skills and agents, and a project .mcp.json
	// When its inventory is read
	omp := installed(t, fixtureRoots(t), kernel.OMP)

	// Then it lists its server, skills (own and shared), agent, and the project's server
	expect(t, describe(omp), "mcp:omp-docs@user", "mcp:project-server@project", "skill:omp-skill@user", "skill:shared-skill@user", "agent:worker@user")
}

func TestPiInventoryLabelsPackagesByTheirInstalledNameOrByTheirRank(t *testing.T) {
	// Given pi's settings listing eleven package sources, two of them installed (their package.json names them)
	// When its inventory is read
	pi := installed(t, fixtureRoots(t), kernel.Pi)

	// Then an installed package shows the name it declares, every other one shows its rank in the file, and no
	// source, however it is written, shows at all
	expect(t, describe(pi), "skill:shared-skill@user", "plugin:pi-extras@user", "plugin:@org/pkg@user",
		"plugin:pi package #2@user", "plugin:pi package #3@user", "plugin:pi package #4@user", "plugin:pi package #6@user",
		"plugin:pi package #7@user", "plugin:pi package #8@user", "plugin:pi package #9@user", "plugin:pi package #10@user",
		"plugin:pi package #11@user")
	noSecret(t, pi)
}

// piWithPackage writes a pi settings file with one package source, and the package.json of the npm package "pkg"
// under pi's npm directory (content "" writes none), and returns the roots and the label the inventory gives it.
func piWithPackage(t *testing.T, source, packageJSON string) string {
	t.Helper()
	r := fixtureRoots(t)
	agent := filepath.Join(r.Home, ".pi", "agent")
	b, _ := json.Marshal(map[string]any{"packages": []string{source}})
	mustWrite(t, filepath.Join(agent, "settings.json"), string(b))
	mustMkdir(t, filepath.Join(agent, "npm", "node_modules", "pkg"))
	if packageJSON != "" {
		mustWrite(t, filepath.Join(agent, "npm", "node_modules", "pkg", "package.json"), packageJSON)
	}
	in := installed(t, r, kernel.Pi)
	noSecret(t, in)
	for _, it := range in.Items {
		if it.Kind == domain.Plugin {
			return it.Name.String()
		}
	}
	t.Fatalf("no package listed: %q", describe(in))
	return ""
}

func TestPiPackageShowsTheNameItsInstalledPackageDeclares(t *testing.T) {
	// Given an npm package installed under pi's npm directory whose package.json names it
	// When the inventory is read
	got := piWithPackage(t, "npm:pkg@1.0.0", `{"name": "pkg"}`)

	// Then the package is shown under that name
	if got != "pkg" {
		t.Errorf("label %q", got)
	}
}

func TestPiPackageWhoseDeclaredNameIsNotAnNpmNameShowsItsRank(t *testing.T) {
	for name, content := range map[string]string{
		"a marker in the name":     `{"name": "pkg SECRET-NAME"}`,
		"an uppercase name":        `{"name": "SECRET-UPPER"}`,
		"a path in the name":       `{"name": "../SECRET-PATH"}`,
		"a name too long":          `{"name": "` + strings.Repeat("a", 215) + `"}`,
		"a name that is no string": `{"name": ["SECRET-LIST"]}`,
		"malformed json":           `{"name": "pkg", SECRET-MALFORMED`,
		"no package.json":          ``,
	} {
		t.Run(name, func(t *testing.T) {
			// Given an installed package whose package.json does not carry a name npm would accept
			// When the inventory is read, then the package is shown by its rank, and no marker is anywhere
			if got := piWithPackage(t, "npm:pkg", content); got != "pi package #1" {
				t.Errorf("label %q", got)
			}
		})
	}
}

func TestPiPackageJSONThatIsNotAnInsideRegularBoundedFileIsNotRead(t *testing.T) {
	// Given a package whose package.json is a link out of pi's npm directory, a FIFO, or far over the bound
	r := fixtureRoots(t)
	agent := filepath.Join(r.Home, ".pi", "agent")
	mustWrite(t, filepath.Join(agent, "settings.json"), `{"packages": ["npm:linked", "npm:fifo", "npm:huge", "npm:escape"]}`)
	nm := filepath.Join(agent, "npm", "node_modules")
	outside := filepath.Join(t.TempDir(), "package.json")
	mustWrite(t, outside, `{"name": "stolen"}`)
	mustMkdir(t, filepath.Join(nm, "linked"))
	if err := os.Symlink(outside, filepath.Join(nm, "linked", "package.json")); err != nil {
		t.Fatal(err)
	}
	mustMkdir(t, filepath.Join(nm, "fifo"))
	mustFifo(t, filepath.Join(nm, "fifo", "package.json"))
	mustMkdir(t, filepath.Join(nm, "huge"))
	mustWrite(t, filepath.Join(nm, "huge", "package.json"), `{"name": "huge", "x": "`+strings.Repeat("x", 1<<20)+`"}`)
	elsewhere := t.TempDir()
	mustWrite(t, filepath.Join(elsewhere, "package.json"), `{"name": "stolen-dir"}`)
	if err := os.Symlink(elsewhere, filepath.Join(nm, "escape")); err != nil {
		t.Fatal(err)
	}

	// When the inventory is read, then none is read: each shows its rank
	in := installed(t, r, kernel.Pi)
	expect(t, describe(in), "skill:shared-skill@user", "plugin:pi package #1@user", "plugin:pi package #2@user", "plugin:pi package #3@user", "plugin:pi package #4@user")
}

func TestPiPackageSourceNeverShapesItsLabel(t *testing.T) {
	// Given sources written every way a source can be, none of them installed
	r := fixtureRoots(t)
	sources := []string{
		"git:github.com/org/SECRET-LAST", "https://SECRET-HOST.example.test", "github.com/org/repo?token=a SECRET-SECOND-WORD",
		"https://host/org/SECRET%2DX", "https://u:SECRET-PW@host/x.git?token=SECRET-Q#SECRET-F", "git@github.com:org/SECRET-SCP",
		"./SECRET-REL/dir", "SECRET-TOKEN@host.example", "", "npm:SECRET-UPPER@1.0.0",
	}
	b, _ := json.Marshal(map[string]any{"packages": sources})
	mustWrite(t, filepath.Join(r.Home, ".pi", "agent", "settings.json"), string(b))

	// When the inventory is read
	in := installed(t, r, kernel.Pi)

	// Then each is shown by its rank, and no marker is anywhere
	var want []string
	for i := range sources {
		want = append(want, fmt.Sprintf("plugin:pi package #%d@user", i+1))
	}
	expect(t, describe(in), append(want, "skill:shared-skill@user")...)
	noSecret(t, in)
}

func TestHomeIsNotAProject(t *testing.T) {
	// Given roots whose project is the home directory
	r := DefaultRoots("/home/x", "/home/x", func(string) string { return "" })

	// Then there is no project-level configuration to read twice
	if r.Project != "" {
		t.Errorf("project = %q", r.Project)
	}
}

func TestRootsHonourTheHarnessDirectoryVariables(t *testing.T) {
	// Given CLAUDE_CONFIG_DIR and CODEX_HOME
	env := map[string]string{"CLAUDE_CONFIG_DIR": "/cfg/claude", "CODEX_HOME": "/cfg/codex"}

	// When the roots are built
	r := DefaultRoots("/home/x", "", func(k string) string { return env[k] })

	// Then Claude's global file lives in its directory too
	if r.ClaudeDir != "/cfg/claude" || r.ClaudeJSON != "/cfg/claude/.claude.json" || r.CodexDir != "/cfg/codex" {
		t.Errorf("roots: %+v", r)
	}
}

func TestMissingHarnessesAreNotErrors(t *testing.T) {
	// Given a home with no harness installed
	r := DefaultRoots(t.TempDir(), "", func(string) string { return "" })

	// When any harness is inventoried, then nothing is listed and nothing is reported
	for _, h := range kernel.Harnesses {
		if in := installed(t, r, h); len(in.Items) != 0 || len(in.Skipped) != 0 {
			t.Errorf("%s: %+v", h, in)
		}
	}
}

func TestItemNamesAThemTerminalCouldMisreadAreNotListed(t *testing.T) {
	// Given skills whose directory names hold an escape sequence and a newline
	r := fixtureRoots(t)
	skills := filepath.Join(r.ClaudeDir, "skills")
	mustMkdir(t, filepath.Join(skills, "bad\x1b[31mname"))
	mustWrite(t, filepath.Join(skills, "bad\x1b[31mname", "SKILL.md"), "plain\n")
	mustMkdir(t, filepath.Join(skills, "new\nline"))
	mustWrite(t, filepath.Join(skills, "new\nline", "SKILL.md"), "plain\n")

	// When the inventory is read
	in := installed(t, r, kernel.Claude)

	// Then neither is listed, a note says names were unusable, and the notes hold no control character
	for _, it := range in.Items {
		if strings.ContainsAny(it.Name.String(), "\x1b\n") {
			t.Errorf("unprintable name listed: %q", it.Name)
		}
	}
	notes := strings.Join(in.Skipped, "\n")
	if !strings.Contains(notes, "an item name is not usable") || strings.Contains(notes, "\x1b") {
		t.Errorf("notes: %q", notes)
	}
}

func TestAFifoInPlaceOfAFileIsRefusedWithoutHanging(t *testing.T) {
	// Given a SKILL.md and a skills entry that are FIFOs, which would block a reader forever
	r := fixtureRoots(t)
	skills := filepath.Join(r.ClaudeDir, "skills")
	mustMkdir(t, filepath.Join(skills, "fifo-skill"))
	mustFifo(t, filepath.Join(skills, "fifo-skill", "SKILL.md"))
	mustFifo(t, filepath.Join(skills, "fifo-entry"))

	// When the inventory is read
	in := installed(t, r, kernel.Claude)

	// Then it returns, the FIFO skill file is refused, and the other skills are still listed
	if notes := strings.Join(in.Skipped, "\n"); !strings.Contains(notes, "fifo-skill/SKILL.md: not a regular file") {
		t.Errorf("notes: %s", notes)
	}
	if !slices.Contains(describe(in), "skill:review-notes@user") {
		t.Error("the good skill vanished with the FIFO")
	}
}

func TestAConfigurationOverTheSizeBoundIsLeftOut(t *testing.T) {
	// Given a settings file larger than any configuration should be
	r := fixtureRoots(t)
	mustWrite(t, filepath.Join(r.ClaudeDir, "settings.json"), `{"enabledPlugins":{"x":true},"pad":"`+strings.Repeat("a", maxConfigBytes)+`"}`)

	// When the inventory is read, then it is noted as too large
	if notes := strings.Join(installed(t, r, kernel.Claude).Skipped, "\n"); !strings.Contains(notes, "settings.json: too large") {
		t.Errorf("notes: %s", notes)
	}
}

func TestMalformedConfigurationIsNotedWithoutItsContent(t *testing.T) {
	// Given a global file that is not valid JSON and holds a credential
	r := fixtureRoots(t)
	mustWrite(t, r.ClaudeJSON, `{"mcpServers": {"a": {"env": {"K": "SECRET-IN-MALFORMED"}} oops`)

	// When the inventory is read
	in := installed(t, r, kernel.Claude)

	// Then it is noted as malformed, and the note does not echo the file
	notes := strings.Join(in.Skipped, "\n")
	if !strings.Contains(notes, ".claude.json: malformed") || strings.Contains(notes, "SECRET") {
		t.Errorf("notes: %s", notes)
	}
}

func TestASymbolicLinkLoopDoesNotStopTheInventory(t *testing.T) {
	// Given a skills entry that is a symbolic link to itself
	r := fixtureRoots(t)
	loop := filepath.Join(r.ClaudeDir, "skills", "loop")
	if err := os.Symlink(loop, loop); err != nil {
		t.Fatal(err)
	}

	// When the inventory is read, then the real skills are still listed
	if !slices.Contains(describe(installed(t, r, kernel.Claude)), "skill:review-notes@user") {
		t.Error("the good skill vanished with the loop")
	}
}

func TestSkillsMayBeSymbolicLinks(t *testing.T) {
	// Given a skills directory whose entries are symbolic links to directories elsewhere (how ~/.agents/skills is shared)
	r := fixtureRoots(t)
	target := filepath.Join(t.TempDir(), "shared")
	mustMkdir(t, target)
	mustWrite(t, filepath.Join(target, "SKILL.md"), "---\nname: linked\n---\n")
	if err := os.Symlink(target, filepath.Join(r.ClaudeDir, "skills", "linked-dir")); err != nil {
		t.Fatal(err)
	}

	// When the inventory is read, then the linked skill is listed
	if !slices.Contains(describe(installed(t, r, kernel.Claude)), "skill:linked@user") {
		t.Error("a symlinked skill directory was not followed")
	}
}

func TestAHugeSkillIsReadOnlyAsFarAsItsFrontmatter(t *testing.T) {
	// Given a skill file far larger than the head that is read
	r := fixtureRoots(t)
	d := filepath.Join(r.ClaudeDir, "skills", "big")
	mustMkdir(t, d)
	mustWrite(t, filepath.Join(d, "SKILL.md"), "---\nname: big-skill\n---\n"+strings.Repeat("x", 4*maxHeadBytes))

	// When the inventory is read, then the skill is listed under its name and no note is made about it
	in := installed(t, r, kernel.Claude)
	if !slices.Contains(describe(in), "skill:big-skill@user") || strings.Contains(strings.Join(in.Skipped, "\n"), "big") {
		t.Errorf("items %q skipped %q", describe(in), in.Skipped)
	}
}

func TestADirectoryOverTheEntryBoundIsReadUpToIt(t *testing.T) {
	// Given a skills directory with more entries than the bound
	r := fixtureRoots(t)
	d := filepath.Join(r.ClaudeDir, "skills")
	for i := range maxEntries + 5 {
		mustMkdir(t, filepath.Join(d, fmt.Sprintf("z%05d", i)))
	}

	// When the inventory is read, then it says so
	if notes := strings.Join(installed(t, r, kernel.Claude).Skipped, "\n"); !strings.Contains(notes, "too many entries") {
		t.Errorf("notes: %s", notes)
	}
}

func TestCodexConfigTablesAreReadWhateverTheQuotingOfTheirHeader(t *testing.T) {
	// Given a Codex config.toml with a quoted key holding a dot and a comment after the header, a single-quoted key
	// holding a space, a sub-table of a server, a multi-line literal string with a header inside, an array of tables,
	// a plugin, and a header that is never closed
	r := fixtureRoots(t)
	mustWrite(t, filepath.Join(r.CodexDir, "config.toml"), `[mcp_servers."b.c"] # trailing
enabled = false
[mcp_servers.'x y']
enabled = true # on
v = '''
[mcp_servers.fake]
'''
[mcp_servers.deep]
[mcp_servers.deep.env]
key = "value"
[[mcp_servers.arr]]
enabled = false
[plugins.after]
[mcp_servers.bad
`)

	// When the inventory is read
	var servers []string
	for _, it := range installed(t, r, kernel.Codex).Items {
		if it.Kind == domain.MCPServer || it.Kind == domain.Plugin {
			servers = append(servers, fmt.Sprintf("%s:%s(off=%v)", it.Kind, it.Name, it.Disabled))
		}
	}

	// Then keys are split on dots outside quotes, enabled is read per table, and neither the text of the string,
	// the sub-table, the array of tables nor the unclosed header adds a server
	slices.Sort(servers)
	expect(t, servers, "mcp:b.c(off=true)", "mcp:x y(off=false)", "mcp:deep(off=false)", "plugin:after(off=false)")
}

// skillNamed writes a skill directory holding SKILL.md with the given content, and returns the name of the directory.
func skillNamed(t *testing.T, r Roots, dir, content string) {
	t.Helper()
	mustMkdir(t, filepath.Join(r.ClaudeDir, "skills", dir))
	mustWrite(t, filepath.Join(r.ClaudeDir, "skills", dir, "SKILL.md"), content)
}

func TestASkillIsNamedByItsFrontmatterOtherwiseByItsDirectory(t *testing.T) {
	r := fixtureRoots(t)
	for dir, c := range map[string]struct{ content, want string }{
		"crlf-skill":     {"---\r\nname: crlf\r\n---\r\n", "crlf"},
		"bom-skill":      {"\xef\xbb\xbf---\nname: 'bom'\n---\n", "bom"},
		"body-skill":     {"---\ndescription: x\n---\nname: body-not-frontmatter\n", "body-skill"},
		"fence-less":     {"name: no-fence\n", "fence-less"},
		"anonymous-name": {"---\nname:\n---\n", "anonymous-name"},
	} {
		// Given a skill whose SKILL.md is written this way
		skillNamed(t, r, dir, c.content)

		// When the inventory is read, then the skill is listed under the name it says, or its directory's
		if got := describe(installed(t, r, kernel.Claude)); !slices.Contains(got, "skill:"+c.want+"@user") {
			t.Errorf("%s: skills %q lack %q", dir, got, c.want)
		}
	}
}

func TestACodexAgentIsNamedByTheTopLevelNameOfItsFileOtherwiseByItsFile(t *testing.T) {
	r := fixtureRoots(t)
	for file, c := range map[string]struct{ content, want string }{
		"literal-agent":  {"name = 'lit'\n", "lit"},
		"nested-agent":   {"[t]\nname = \"nested\"\n", "nested-agent"},
		"unclosed-agent": {"name = \"unterminated\n", "unclosed-agent"},
	} {
		// Given an agent file written this way
		mustWrite(t, filepath.Join(r.CodexDir, "agents", file+".toml"), c.content)

		// When the inventory is read, then the agent is listed under the name it says, or its file's
		if got := describe(installed(t, r, kernel.Codex)); !slices.Contains(got, "agent:"+c.want+"@user") {
			t.Errorf("%s: agents %q lack %q", file, got, c.want)
		}
	}
}

func TestUnknownHarnessIsRefused(t *testing.T) {
	if _, err := (Inventory{}).Installed(kernel.Harness{}); err == nil {
		t.Error("the zero harness was accepted")
	}
}

func mustMkdir(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
}

func mustWrite(t *testing.T, p, s string) {
	t.Helper()
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

func mustFifo(t *testing.T, p string) {
	t.Helper()
	if err := syscall.Mkfifo(p, 0o644); err != nil {
		t.Fatal(err)
	}
}
