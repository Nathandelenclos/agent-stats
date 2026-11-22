//go:build unix

package e2e

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// A workplace is a home folder and, next to it, an app in a Git repository (with a sub-folder), a sibling whose name
// starts with the app's and a link to the app's sub-folder. One Claude session worked in each of the app (1 request), its
// sub-folder (2), and the sibling (4): the number of requests a view counts tells which sessions it covers.
type workplace struct {
	home, app, sub, sibling, link, empty string
	db                                   string
}

func newScopedWorkplace(t *testing.T) workplace {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	w := workplace{home: filepath.Join(root, "home"), app: filepath.Join(root, "work", "app"), sibling: filepath.Join(root, "work", "app-2"),
		link: filepath.Join(root, "shortcut"), empty: filepath.Join(root, "empty"), db: filepath.Join(root, "index.db")}
	w.sub = filepath.Join(w.app, "web")
	for _, d := range []string{w.home, filepath.Join(w.app, ".git"), w.sub, w.sibling, w.empty} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(w.sub, w.link); err != nil {
		t.Fatal(err)
	}
	for name, s := range map[string]struct {
		folder   string
		requests int
		tool     string
	}{"app": {w.app, 1, "Read"}, "sub": {w.sub, 2, "Read"}, "sibling": {w.sibling, 4, "Grep"}} {
		w.writeSession(t, name, s.folder, s.requests, s.tool)
	}
	if err := os.MkdirAll(filepath.Join(w.app, ".claude", "skills", "app-skill"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(w.app, ".claude", "skills", "app-skill", "SKILL.md"), []byte("---\nname: app-skill\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fixtureEnv(t, w.home)
	return w
}

// writeSession writes the log of a Claude session that worked in folder and made that many requests, each calling tool.
func (w workplace) writeSession(t *testing.T, name, folder string, requests int, tool string) {
	t.Helper()
	var log strings.Builder
	fmt.Fprintf(&log, `{"type":"user","uuid":"u","timestamp":"2026-01-01T10:00:00Z","cwd":%q,"message":{"role":"user","content":"hi"}}`+"\n", folder)
	for i := range requests {
		fmt.Fprintf(&log, `{"type":"assistant","timestamp":"2026-01-01T10:00:0%dZ","message":{"id":"m%d","model":"claude-x","content":[{"type":"tool_use","id":"t%d","name":%q,"input":{}}],"usage":{"input_tokens":5,"output_tokens":7}}}`+"\n", i+1, i, i, tool)
	}
	dir := filepath.Join(w.home, ".claude", "projects", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name+".jsonl"), []byte(log.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}

// report runs the report from cwd with the workplace's index.
func (w workplace) report(t *testing.T, cwd string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	return runCLI(t, cwd, append([]string{"--db", w.db}, args...)...)
}

var requestsLine = regexp.MustCompile(`(?m)^(\d+) requests \(`)

// requestsOf reads the number of requests a text report counts, and its first line.
func requestsOf(t *testing.T, report string) (requests, firstLine string) {
	t.Helper()
	m := requestsLine.FindStringSubmatch(report)
	if m == nil {
		t.Fatalf("no total line in the report:\n%s", report)
	}
	return m[1], strings.SplitN(report, "\n", 2)[0]
}

func TestTheReportFromAProjectFolderCountsThatProjectOnly(t *testing.T) {
	// Given sessions in a repository, in its sub-folder and in a sibling whose name starts with the repository's
	w := newScopedWorkplace(t)

	for name, cwd := range map[string]string{
		"its top":      w.app,
		"a sub-folder": w.sub,
		"a link to it": w.link,
	} {
		// When the report is printed from that folder
		out, _, err := w.report(t, cwd)
		if err != nil {
			t.Fatal(err)
		}

		// Then it says it covers the project, and counts its sessions and those below it, not the sibling's
		requests, first := requestsOf(t, out)
		if requests != "3" || first != "Project: app ("+w.app+")" {
			t.Errorf("from %s: %s requests, first line %q\n%s", name, requests, first, out)
		}
	}
}

func TestTheReportFromAFolderNoSessionWorkedInCountsEveryProject(t *testing.T) {
	// Given the same sessions, and the home folder, in which none worked
	w := newScopedWorkplace(t)

	// When the report is printed from the home folder
	out, _, err := w.report(t, w.home)
	if err != nil {
		t.Fatal(err)
	}

	// Then every project is counted
	if requests, first := requestsOf(t, out); requests != "7" || first != "All projects" {
		t.Errorf("%s requests, first line %q\n%s", requests, first, out)
	}
}

func TestTheHomeFolderIsNotAProjectEvenWhenSessionsWorkedBelowIt(t *testing.T) {
	// Given a session that worked in a folder of the home, which makes the home the parent of sessions
	w := newScopedWorkplace(t)
	w.writeSession(t, "notes", filepath.Join(w.home, "notes"), 8, "Read")

	// When the report is printed from the home folder
	out, _, err := w.report(t, w.home)

	// Then every project is counted: the home folder holds them all, it is not one of them
	if requests, first := requestsOf(t, out); err != nil || requests != "15" || first != "All projects" {
		t.Errorf("%s requests, first line %q, %v", requests, first, err)
	}
}

func TestTheFlagAllCountsEveryProjectFromAProjectFolder(t *testing.T) {
	w := newScopedWorkplace(t)
	out, _, err := w.report(t, w.app, "--all")
	if requests, first := requestsOf(t, out); err != nil || requests != "7" || first != "All projects" {
		t.Errorf("%s requests, first line %q, %v", requests, first, err)
	}
}

func TestTheFlagProjectCountsTheProjectItNamesFromAnyFolder(t *testing.T) {
	// Given the same sessions
	w := newScopedWorkplace(t)

	for name, c := range map[string]struct{ dir, requests, first string }{
		"the sibling":                     {w.sibling, "4", "Project: app-2 (" + w.sibling + ")"},
		"the sub-folder, by a link to it": {w.link, "2", "Project: web (" + w.sub + ")"},
		"a folder no session worked in":   {w.empty, "0", ""},
	} {
		// When the report is printed from the home folder for that project
		out, _, err := w.report(t, w.home, "--project", c.dir)
		if err != nil {
			t.Fatal(err)
		}

		// Then it counts that project, whether sessions worked in it or not
		if c.requests == "0" {
			if !strings.HasPrefix(out, "Project: empty ("+w.empty+")\nNo requests in this range.") {
				t.Errorf("%s: %q", name, out)
			}
			continue
		}
		if requests, first := requestsOf(t, out); requests != c.requests || first != c.first {
			t.Errorf("%s: %s requests, first line %q", name, requests, first)
		}
	}
}

func TestTheFlagProjectNamingTheHomeFolderCountsEveryProjectAndSaysSo(t *testing.T) {
	// Given the same sessions, and the home folder that holds every one of them
	w := newScopedWorkplace(t)

	// When the report is printed for the project that is the home folder
	out, _, err := w.report(t, w.app, "--project", w.home)

	// Then every project is counted, and the first line says why
	requests, first := requestsOf(t, out)
	if err != nil || requests != "7" || first != "All projects (the home folder is not a project)" {
		t.Errorf("%s requests, first line %q, %v", requests, first, err)
	}
}

func TestTheFlagAllWinsOverTheFlagProject(t *testing.T) {
	// Given the same sessions
	w := newScopedWorkplace(t)

	// When the report is printed for every project and for a project at once
	out, _, err := w.report(t, w.app, "--all", "--project", w.sibling)

	// Then every project is counted
	if requests, first := requestsOf(t, out); err != nil || requests != "7" || first != "All projects" {
		t.Errorf("%s requests, first line %q, %v", requests, first, err)
	}
}

func TestTheJSONOfAProjectKeepsItsFormatAndCountsTheProjectOnly(t *testing.T) {
	// Given the same sessions
	w := newScopedWorkplace(t)

	// When the JSON report is printed from the repository
	out, _, err := w.report(t, w.app, "--json")
	if err != nil {
		t.Fatal(err)
	}

	// Then it has its usual fields and counts the project's sessions
	var report map[string]json.RawMessage
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, out)
	}
	for _, field := range []string{"total", "byHarness", "byModel", "byFolder", "byAgentType", "tools"} {
		if _, ok := report[field]; !ok {
			t.Errorf("field %q is missing", field)
		}
	}
	var total struct{ Requests int }
	_ = json.Unmarshal(report["total"], &total)
	if len(report) != 6 || total.Requests != 3 {
		t.Errorf("%d fields, %d requests", len(report), total.Requests)
	}
}

func TestTheJSONOfEveryProjectCountsThemAll(t *testing.T) {
	// Given the same sessions
	w := newScopedWorkplace(t)

	// When the JSON report is printed from the home folder
	out, _, err := w.report(t, w.home, "--json")

	// Then every session is counted
	var report struct{ Total struct{ Requests int } }
	if jsonErr := json.Unmarshal([]byte(out), &report); err != nil || jsonErr != nil || report.Total.Requests != 7 {
		t.Errorf("requests = %d (%v, %v)", report.Total.Requests, err, jsonErr)
	}
}

// catalogFrom runs the catalogue from cwd with the workplace's index.
func (w workplace) catalogFrom(t *testing.T, cwd string, args ...string) (string, error) {
	t.Helper()
	out, stderr, err := runCLI(t, cwd, append([]string{"catalog", "--db", w.db}, args...)...)
	if err != nil {
		return out, fmt.Errorf("catalog: %w\n%s", err, stderr)
	}
	return out, nil
}

func TestTheCatalogFromAProjectFolderCoversThatProject(t *testing.T) {
	// Given sessions that read in two projects, and a skill the app defines
	w := newScopedWorkplace(t)

	// When the catalogue is printed from a sub-folder of the app
	out, err := w.catalogFrom(t, w.sub)
	if err != nil {
		t.Fatal(err)
	}

	// Then it says it covers the app, lists the app's own skill, counts the tool its sessions called and not the sibling's
	if first := strings.SplitN(out, "\n", 2)[0]; first != "Project: app ("+w.app+")" {
		t.Errorf("first line %q", first)
	}
	if !regexp.MustCompile(`skill\s+app-skill\s+project`).MatchString(out) || !regexp.MustCompile(`Read\s+logs\s+3`).MatchString(out) || strings.Contains(out, "Grep") {
		t.Errorf("catalogue:\n%s", out)
	}
}

func TestTheCatalogFromAFolderNoSessionWorkedInCoversEveryProject(t *testing.T) {
	w := newScopedWorkplace(t)
	out, err := w.catalogFrom(t, w.home)
	if err != nil || strings.SplitN(out, "\n", 2)[0] != "All projects" || !strings.Contains(out, "Grep") || !strings.Contains(out, "Read") {
		t.Errorf("catalogue (%v):\n%s", err, out)
	}
}

func TestTheCatalogWithAllCountsEveryProjectButStillListsTheProjectsOwnItems(t *testing.T) {
	w := newScopedWorkplace(t)
	out, err := w.catalogFrom(t, w.app, "--all")
	if err != nil || strings.SplitN(out, "\n", 2)[0] != "All projects" || !strings.Contains(out, "Grep") || !regexp.MustCompile(`skill\s+app-skill\s+project`).MatchString(out) {
		t.Errorf("catalogue (%v):\n%s", err, out)
	}
}

func TestTheCatalogProjectFlagNamesTheProjectWhoseSessionsCount(t *testing.T) {
	// Given the same workplace
	w := newScopedWorkplace(t)

	// When the catalogue is printed from the home folder for the sibling project
	out, err := w.catalogFrom(t, w.home, "--project", w.sibling)

	// Then it is about the sibling alone
	if err != nil || !strings.HasPrefix(out, "Project: app-2 (") || !strings.Contains(out, "Grep") || strings.Contains(out, "Read") {
		t.Errorf("catalogue (%v):\n%s", err, out)
	}
}

func TestTheCatalogWithAnEmptyProjectReadsNoProjectLevelConfigurationAndCountsEveryone(t *testing.T) {
	// Given the same workplace
	w := newScopedWorkplace(t)

	// When the catalogue is printed from the app with no project at all
	out, err := w.catalogFrom(t, w.app, "--project", "")

	// Then the app's own skill is not listed and every project's usage counts
	if err != nil || !strings.HasPrefix(out, "All projects") || strings.Contains(out, "app-skill") || !strings.Contains(out, "Grep") {
		t.Errorf("catalogue (%v):\n%s", err, out)
	}
}
