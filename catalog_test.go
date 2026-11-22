package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// catalogHome copies the fixture configuration and logs of testdata/catalog into a temp home, with the project at
// ~/work/app, and makes it the home of the process. It returns the home and the project.
func catalogHome(t *testing.T) (home, project string) {
	t.Helper()
	dir := t.TempDir()
	home = filepath.Join(dir, "home")
	if err := os.CopyFS(home, os.DirFS("testdata/catalog/home")); err != nil {
		t.Fatal(err)
	}
	project = filepath.Join(home, "work", "app")
	if err := os.CopyFS(project, os.DirFS("testdata/catalog/project")); err != nil {
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
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("CODEX_HOME", "")
	return home, project
}

// catalogOutput runs the subcommand and returns what it printed on stdout and stderr.
func catalogOutput(t *testing.T, args ...string) (stdout, stderr string) {
	t.Helper()
	var out, errOut bytes.Buffer
	if err := runCatalog(args, &out, &errOut); err != nil {
		t.Fatalf("catalog %v: %v\n%s", args, err, errOut.String())
	}
	return out.String(), errOut.String()
}

// TestCatalogApproved locks the whole subcommand (configuration trees, indexing of the logs, linking, tables and
// JSON) on fixtures. On a difference it writes testdata/catalog.received.txt: read the diff, and only copy it over
// catalog.approved.txt if the change is intended.
func TestCatalogApproved(t *testing.T) {
	// Given fixture configurations and session logs for the four harnesses, indexed by the first run
	time.Local = time.UTC // dates print in the local zone
	t.Cleanup(func() { time.Local = time.Now().Location() })
	_, project := catalogHome(t)
	db := filepath.Join(t.TempDir(), "index.db")
	base := []string{"--db", db, "--project", project}
	catalogOutput(t, base...)
	base = append(base, "--no-sync")

	// When the catalogue is asked for in each way
	var got strings.Builder
	for _, c := range []struct {
		name string
		args []string
	}{
		{"all", nil},
		{"json", []string{"--json"}},
		{"claude only", []string{"--harness", "claude"}},
		{"since 2026-01-04", []string{"--since", "2026-01-04"}},
		{"never used", []string{"--unused"}},
		{"no project", []string{"--project", ""}},
	} {
		out, errOut := catalogOutput(t, append(append([]string{}, base...), c.args...)...)
		got.WriteString("=== " + c.name + " ===\n" + out)
		if errOut != "" {
			got.WriteString("--- stderr ---\n" + errOut)
		}

		// Then no secret and no control character other than line breaks reaches the output
		if strings.Contains(out+errOut, "SECRET") {
			t.Errorf("%s: a secret reached the output", c.name)
		}
		if strings.ContainsFunc(strings.ReplaceAll(out+errOut, "\n", ""), func(r rune) bool { return r < ' ' || r == 0x7f }) {
			t.Errorf("%s: control characters in the output", c.name)
		}
	}

	// Then the output is the approved one
	received := []byte(got.String())
	approved, err := os.ReadFile("testdata/catalog.approved.txt")
	if err != nil || !bytes.Equal(approved, received) {
		if werr := os.WriteFile("testdata/catalog.received.txt", received, 0o644); werr != nil {
			t.Fatal(werr)
		}
		t.Fatalf("output differs from testdata/catalog.approved.txt (%v); see testdata/catalog.received.txt", err)
	}
	os.Remove("testdata/catalog.received.txt")
}

func TestCatalogJSONTellsUnknownUsageFromNone(t *testing.T) {
	// Given the fixture home, indexed
	_, project := catalogHome(t)
	db := filepath.Join(t.TempDir(), "index.db")
	out, _ := catalogOutput(t, "--db", db, "--project", project, "--json")

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
	text, _ := catalogOutput(t, "--db", db, "--project", project)
	asJSON, _ := catalogOutput(t, "--db", db, "--project", project, "--json", "--no-sync")

	// Then the tool is listed with the sequence defanged, in both
	if strings.Contains(text+asJSON, "\x1b") || !strings.Contains(text, "bad?[2Jtool") {
		t.Errorf("hostile name not defanged:\n%s", text)
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
		var out, errOut bytes.Buffer
		if err := runCatalog(append([]string{"--db", db, "--no-sync"}, args...), &out, &errOut); err == nil {
			t.Errorf("%s: accepted", name)
		}
		if out.Len() != 0 {
			t.Errorf("%s: printed %q", name, out.String())
		}
	}
}
