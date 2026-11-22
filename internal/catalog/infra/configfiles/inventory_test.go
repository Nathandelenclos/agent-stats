package configfiles

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"

	"github.com/Nathandelenclos/agent-stats/internal/catalog/application"
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

func TestPluginThatEscapesItsDirectoryOrIsOffIsNotRead(t *testing.T) {
	// Given a plugin whose install path is /etc, one switched off, and one installed for another project
	in := installed(t, fixtureRoots(t), kernel.Claude)

	// Then the plugin is listed but its contents are not (nor the disabled one's), the escape is noted, and the plugin
	// of another project is left out
	for _, it := range in.Items {
		if it.Name.String() == "hidden" || it.Name.String() == "off:hidden" || it.Name.String() == "elsewhere@market" {
			t.Errorf("unexpected item %+v", it)
		}
	}
	if !slices.ContainsFunc(in.Skipped, func(s string) bool { return strings.HasSuffix(s, "/etc: points outside its directory") }) {
		t.Errorf("skipped: %q", in.Skipped)
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

func TestOhMyPiAndPiInventories(t *testing.T) {
	// Given oh-my-pi's mcp.json, skills and agents, and pi's settings with credentialed package sources
	r := fixtureRoots(t)

	// When both are inventoried
	omp, pi := installed(t, r, kernel.OMP), installed(t, r, kernel.Pi)

	// Then oh-my-pi lists its server, skills (own and shared), agent, and the project's .mcp.json
	expect(t, describe(omp), "mcp:omp-docs@user", "mcp:project-server@project", "skill:omp-skill@user", "skill:shared-skill@user", "agent:worker@user")
	// And pi lists its shared skill and its packages, whose URLs lost their credentials, query and fragment
	expect(t, describe(pi), "skill:shared-skill@user", "plugin:pi-extras@user",
		"plugin:git+https://git.example.test/org/pkg.git@user", "plugin:git:https://git.example.test/org/other.git@user", "plugin:git:git.example.test/org/third.git@user", "plugin:npm:@org/pkg@user")
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

func TestPackageSourcesLoseTheirCredentials(t *testing.T) {
	for in, want := range map[string]string{
		"https://u:p@host/x.git?token=t#f":          "https://host/x.git",
		"npm:@org/pkg":                              "npm:@org/pkg",
		"./local/dir":                               "./local/dir",
		"git:https://u:TOKEN@host/org/pkg":          "git:https://host/org/pkg",
		"npm:https://u:p@w@h/pkg?x=1":               "npm:https://h/pkg",
		"https://h/a@b":                             "https://h/a@b",
		"git+ssh://git@h/org/pkg#ref":               "git+ssh://h/org/pkg",
		"https://a:b@h/x https://c:d@i/y":           "https://h/x https://i/y",
		"git:user:TOKEN@github.com/org/r":           "git:github.com/org/r",
		"git:github.com/org/r?token=T":              "git:github.com/org/r",
		"git@github.com:org/repo":                   "github.com:org/repo",
		"user:TOKEN@host:org/repo#ref":              "host:org/repo",
		"HTTPS://u:p@h/x":                           "HTTPS://h/x",
		"git:user:TOKEN@github.com/org/r#https://x": "git:github.com/org/r",
		"user:TOKEN@host:org/repo?mirror=https://m": "host:org/repo",
		"user:TOKEN@github.com/org/repo":            "github.com/org/repo",
		"GIT:user:TOKEN@host/r":                     "GIT:host/r",
		"git:a:T@h/x git:b:U@i/y":                   "git:h/x git:i/y",
		"  git:user:TOKEN@host/r  ":                 "git:host/r",
		"pkg@1.2.3":                                 "pkg@1.2.3",
		"npm:pkg@1.2.3":                             "npm:pkg@1.2.3",
		"@org/pkg@1.2.3":                            "@org/pkg@1.2.3",
	} {
		// Given a package source, when its credentials are dropped, then only what is safe to show remains
		if got := withoutCredentials(in); got != want {
			t.Errorf("withoutCredentials(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTomlTables(t *testing.T) {
	// Given a TOML file with quoted keys, comments, single-quoted strings, a multi-line literal string and arrays of tables
	src := "[a.\"b.c\".d] # trailing\nenabled = false\n['x y']\nenabled = true # on\nv = '''\n[fake]\n'''\n[[arr]]\nenabled = false\n[after]\n[bad\n"

	// When its tables are read
	var got []string
	for _, tb := range tomlTables([]byte(src)) {
		on := "?"
		if tb.Enabled != nil {
			on = fmt.Sprint(*tb.Enabled)
		}
		got = append(got, strings.Join(tb.Path, "|")+"="+on)
	}

	// Then headers are split on dots outside quotes, enabled is read per table, and neither the multi-line text nor
	// the array of tables nor the malformed header leaks in
	want := []string{"a|b.c|d=false", "x y=true", "after=?"}
	if !slices.Equal(got, want) {
		t.Errorf("tables %q, want %q", got, want)
	}
}

func TestFrontmatterAndTomlNames(t *testing.T) {
	for _, c := range []struct{ got, want string }{
		{frontmatterName([]byte("---\r\nname: crlf\r\n---\r\n"), "f"), "crlf"},
		{frontmatterName([]byte("\xef\xbb\xbf---\nname: 'bom'\n---\n"), "f"), "bom"},
		{frontmatterName([]byte("---\ndescription: x\n---\nname: body-not-frontmatter\n"), "f"), "f"},
		{frontmatterName([]byte("name: no-fence\n"), "f"), "f"},
		{tomlName([]byte("name = 'lit'\n"), "f"), "lit"},
		{tomlName([]byte("[t]\nname = \"nested\"\n"), "f"), "f"},
		{tomlName([]byte("name = \"unterminated\n"), "f"), "f"},
	} {
		if c.got != c.want {
			t.Errorf("got %q, want %q", c.got, c.want)
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
