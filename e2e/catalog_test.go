package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// catalogHome copies the fixture configuration and logs of testdata/catalog into a temp home, with the project at
// ~/work/app, and makes it the home of the process. It returns the home and the project.
func catalogHome(t *testing.T) (home, project string) {
	t.Helper()
	dir := t.TempDir()
	home = filepath.Join(dir, "home")
	if err := os.CopyFS(home, os.DirFS("../testdata/catalog/home")); err != nil {
		t.Fatal(err)
	}
	project = filepath.Join(home, "work", "app")
	if err := os.CopyFS(project, os.DirFS("../testdata/catalog/project")); err != nil {
		t.Fatal(err)
	}
	list := filepath.Join(home, ".claude", "plugins", "installed_plugins.json")
	b, err := os.ReadFile(list)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(list, []byte(strings.ReplaceAll(string(b), "@HOME@", home)), 0o644); err != nil {
		t.Fatal(err)
	}
	fixtureEnv(t, home)
	t.Setenv("TZ", "UTC")
	return home, project
}

// catalogOutput runs the subcommand and returns what it printed on stdout and stderr.
func catalogOutput(t *testing.T, args ...string) (stdout, stderr string) {
	t.Helper()
	stdout, stderr, err := runCLI(t, "", append([]string{"catalog"}, args...)...)
	if err != nil {
		t.Fatalf("catalog %v: %v\n%s", args, err, stderr)
	}
	return stdout, stderr
}

// catalogCases are the ways the catalogue is asked for on the fixture home; each has an approved output.
var catalogCases = []struct {
	name string
	args []string
}{
	{"all", nil},
	{"json", []string{"--json"}},
	{"claude-only", []string{"--harness", "claude"}},
	{"since-2026-01-04", []string{"--since", "2026-01-04"}},
	{"never-used", []string{"--unused"}},
	{"no-project", []string{"--project", ""}},
}

// indexedCatalogHome prepares the fixture home, indexes its logs with a first run, and returns the arguments that
// ask for a catalogue of that index without reading the logs again. Dates print in the local zone, so it is UTC.
func indexedCatalogHome(t *testing.T) []string {
	t.Helper()
	_, project := catalogHome(t)
	db := filepath.Join(t.TempDir(), "index.db")
	base := []string{"--db", db, "--project", project, "--all"}
	catalogOutput(t, base...)
	return append(base, "--no-sync")
}

// TestCatalogPrintsTheApprovedOutputForEachWayOfAskingForIt locks what the subcommand prints (configuration trees, indexing of the logs, linking, tables
// and JSON) on fixtures, one approved file per way of asking. On a difference it writes <name>.received.txt next to
// the approved file: read the diff, and only copy it over if the change is intended.
func TestCatalogPrintsTheApprovedOutputForEachWayOfAskingForIt(t *testing.T) {
	// Given fixture configurations and session logs for the four harnesses, indexed by a first run
	base := indexedCatalogHome(t)
	for _, c := range catalogCases {
		t.Run(c.name, func(t *testing.T) {
			// When the catalogue is asked for this way
			out, errOut := catalogOutput(t, append(append([]string{}, base...), c.args...)...)
			received := out
			if errOut != "" {
				received += "--- stderr ---\n" + errOut
			}

			// Then it prints the approved output
			path := filepath.Join("..", "testdata", "catalog.approved", c.name+".txt")
			approved, err := os.ReadFile(path)
			if err != nil || string(approved) != received {
				if werr := os.WriteFile(strings.TrimSuffix(path, ".txt")+".received.txt", []byte(received), 0o644); werr != nil {
					t.Fatal(werr)
				}
				t.Fatalf("output differs from %s (%v); see the .received.txt next to it", path, err)
			}
			os.Remove(strings.TrimSuffix(path, ".txt") + ".received.txt")
		})
	}
}

// outputsOfEveryCase prints the catalogue every way of asking, stdout and stderr together.
func outputsOfEveryCase(t *testing.T) map[string]string {
	t.Helper()
	base := indexedCatalogHome(t)
	out := map[string]string{}
	for _, c := range catalogCases {
		stdout, stderr := catalogOutput(t, append(append([]string{}, base...), c.args...)...)
		out[c.name] = stdout + stderr
	}
	return out
}

func TestCatalogOutputNeverCarriesACredential(t *testing.T) {
	// Given fixture configurations holding fake credentials in MCP env, headers, URLs and package sources
	// When the catalogue is printed any way
	for name, out := range outputsOfEveryCase(t) {
		// Then no credential reaches the output
		if strings.Contains(out, "SECRET") {
			t.Errorf("%s: a credential reached the output", name)
		}
	}
}

func TestCatalogHoldsNothingTheConfigurationDeclaresAsASource(t *testing.T) {
	// Given a fixture tree whose pi package sources and Claude Code plugin paths carry markers in every place they can:
	// last path segment, host alone, query with spaces, encoded characters, an install path that does not exist, an
	// install directory named after one, a manifest declaring a server file outside its plugin
	home, project := catalogHome(t)
	var markers int
	filepath.WalkDir(home, func(path string, d os.DirEntry, err error) error {
		if err == nil && strings.Contains(path, "SECRET") && d.IsDir() {
			markers++
		}
		if b, rerr := os.ReadFile(path); err == nil && !d.IsDir() && rerr == nil && !strings.HasSuffix(path, ".jsonl") {
			markers += strings.Count(string(b), "SECRET")
		}
		return nil
	})
	if markers < 20 {
		t.Fatalf("the fixture tree holds %d markers: it no longer covers every shape", markers)
	}
	db := filepath.Join(t.TempDir(), "index.db")
	base := []string{"--db", db, "--project", project, "--all"}
	catalogOutput(t, base...)
	base = append(base, "--no-sync")

	// When the catalogue is printed as text, as JSON, and limited to what is unused
	for _, args := range [][]string{nil, {"--json"}, {"--unused"}, {"--unused", "--json"}, {"--harness", "pi"}, {"--harness", "claude", "--json"}} {
		stdout, stderr := catalogOutput(t, append(append([]string{}, base...), args...)...)

		// Then the word SECRET, which every marker holds, is nowhere on stdout or stderr
		if strings.Contains(stdout+stderr, "SECRET") {
			t.Errorf("catalog %v prints a declared source:\n%s%s", args, stdout, stderr)
		}
	}
}

func TestCatalogOutputNeverCarriesControlCharacters(t *testing.T) {
	// Given fixture configurations and logs, indexed
	// When the catalogue is printed any way
	for name, out := range outputsOfEveryCase(t) {
		// Then no control character other than line breaks reaches the output
		if strings.ContainsFunc(strings.ReplaceAll(out, "\n", ""), func(r rune) bool { return r < ' ' || r == 0x7f }) {
			t.Errorf("%s: control characters in the output", name)
		}
	}
}

func TestCatalogJSONTellsUnknownUsageFromNone(t *testing.T) {
	// Given the fixture home, indexed
	_, project := catalogHome(t)
	db := filepath.Join(t.TempDir(), "index.db")
	out, _ := catalogOutput(t, "--db", db, "--project", project, "--all", "--json")

	// When the JSON is read
	var c jsonCatalog
	if err := json.Unmarshal([]byte(out), &c); err != nil {
		t.Fatal(err)
	}

	// Then a Claude agent nobody used has 0 uses, and a Codex agent, which the logs do not name, has none (null)
	uses := map[string]*int64{}
	for _, e := range c.Entries {
		if e.Kind == "agent" && e.Installed {
			uses[e.Harness+"/"+e.Name] = e.Uses
		}
	}
	if u := uses["claude/plain-agent"]; u == nil || *u != 0 {
		t.Errorf("claude agent: %v", u)
	}
	if u, ok := uses["codex/planner"]; !ok || u != nil {
		t.Errorf("codex agent: %v %v", u, ok)
	}
}

func TestCatalogShowsHostileLoggedNamesEscaped(t *testing.T) {
	// Given a log whose tool name holds an escape sequence that would clear a terminal
	home, project := catalogHome(t)
	hostile := `{"type":"assistant","timestamp":"2026-01-06T10:00:00Z","message":{"id":"mh","model":"claude-x","content":[{"type":"tool_use","id":"th","name":"bad\u001b[2Jtool","input":{}}],"usage":{"input_tokens":1,"output_tokens":1}}}` + "\n"
	if err := os.WriteFile(filepath.Join(home, ".claude", "projects", "-work-app", "hostile.jsonl"), []byte(hostile), 0o644); err != nil {
		t.Fatal(err)
	}

	// When the catalogue is printed as a table and as JSON
	db := filepath.Join(t.TempDir(), "index.db")
	text, _ := catalogOutput(t, "--db", db, "--project", project, "--all")
	asJSON, _ := catalogOutput(t, "--db", db, "--project", project, "--all", "--json", "--no-sync")

	// Then the tool is listed with the sequence defanged, in both
	if strings.Contains(text+asJSON, "\x1b") || !strings.Contains(text, "bad?[2Jtool") {
		t.Errorf("hostile name not defanged:\n%s", text)
	}
}

type jsonEntry struct {
	Harness   string `json:"harness"`
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Installed bool   `json:"installed"`
	Uses      *int64 `json:"uses"`
}

type jsonCatalog struct {
	Entries []jsonEntry `json:"entries"`
}

// catalogWithARefusedSkillCall prints, as JSON, the catalogue of the fixture home plus a log with a Skill call that
// names a skill no harness could have (an escape sequence).
func catalogWithARefusedSkillCall(t *testing.T) jsonCatalog {
	t.Helper()
	home, project := catalogHome(t)
	refused := `{"type":"assistant","timestamp":"2026-01-06T10:00:00Z","message":{"id":"mr","model":"claude-x","content":[{"type":"tool_use","id":"tr","name":"Skill","input":{"skill":"bad\u001b[2Jskill"}}],"usage":{"input_tokens":1,"output_tokens":1}}}` + "\n"
	if err := os.WriteFile(filepath.Join(home, ".claude", "projects", "-work-app", "refused.jsonl"), []byte(refused), 0o644); err != nil {
		t.Fatal(err)
	}
	db := filepath.Join(t.TempDir(), "index.db")
	out, _ := catalogOutput(t, "--db", db, "--project", project, "--all", "--json")
	var c jsonCatalog
	if err := json.Unmarshal([]byte(out), &c); err != nil {
		t.Fatal(err)
	}
	return c
}

func TestCatalogListsSkillCallsThatNamedNoValidSkillApart(t *testing.T) {
	// Given a log with a Skill call that names no valid skill
	// When the catalogue is printed as JSON
	c := catalogWithARefusedSkillCall(t)

	// Then the call is listed as unnamed Skill calls, defined nowhere
	var unnamed *jsonEntry
	for i, e := range c.Entries {
		if e.Harness == "claude" && e.Kind == "skill" && e.Name == "(unnamed Skill calls)" {
			unnamed = &c.Entries[i]
		}
	}
	if unnamed == nil || unnamed.Installed || unnamed.Uses == nil || *unnamed.Uses != 1 {
		t.Errorf("unnamed Skill calls: %+v", unnamed)
	}
}

func TestCatalogKeepsSkillUsageKnownWhenASkillCallNamedNoValidSkill(t *testing.T) {
	// Given a log with a Skill call that names no valid skill, and an installed skill nobody loaded
	// When the catalogue is printed as JSON
	c := catalogWithARefusedSkillCall(t)

	// Then the usage of that skill is known to be none, not unknown
	var unloaded *jsonEntry
	for i, e := range c.Entries {
		if e.Harness == "claude" && e.Kind == "skill" && e.Name == "frontmatter-name" {
			unloaded = &c.Entries[i]
		}
	}
	if unloaded == nil || unloaded.Uses == nil || *unloaded.Uses != 0 {
		t.Errorf("skill nobody loaded: %+v", unloaded)
	}
}

func TestCatalogRejectsWhatItDoesNotUnderstand(t *testing.T) {
	catalogHome(t)
	db := filepath.Join(t.TempDir(), "index.db")
	for name, args := range map[string][]string{
		"unknown harness": {"--harness", "vim"},
		"bad period":      {"--since", "yesterday"},
		"stray argument":  {"stray"},
		"unknown flag":    {"--nope"},
	} {
		// Given invalid arguments, when the subcommand runs, then it fails instead of printing a partial catalogue
		out, _, err := runCLI(t, "", append([]string{"catalog", "--db", db, "--no-sync"}, args...)...)
		if err == nil {
			t.Errorf("%s: accepted", name)
		}
		if out != "" {
			t.Errorf("%s: printed %q", name, out)
		}
	}
}
