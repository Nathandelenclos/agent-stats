//go:build unix

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/Nathandelenclos/agent-stats/internal/kernel"
	"github.com/Nathandelenclos/agent-stats/internal/platform/projectfolders"
	reporting "github.com/Nathandelenclos/agent-stats/internal/reporting/application"
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
	t.Setenv("HOME", w.home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("CODEX_HOME", "")
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

// report runs `agent-stats args` from the folder cwd and returns what it printed. The command reads the process's
// arguments and its working folder, so those are set for the run.
func (w workplace) report(t *testing.T, cwd string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	t.Chdir(cwd)
	defer func(a []string, f *flag.FlagSet, o, e *os.File) {
		os.Args, flag.CommandLine, os.Stdout, os.Stderr = a, f, o, e
	}(os.Args, flag.CommandLine, os.Stdout, os.Stderr)
	os.Args = append([]string{"agent-stats", "--db", w.db}, args...)
	flag.CommandLine = flag.NewFlagSet("agent-stats", flag.ContinueOnError)
	outR, outW, _ := os.Pipe()
	errR, errW, _ := os.Pipe()
	os.Stdout, os.Stderr = outW, errW
	err = run()
	outW.Close()
	errW.Close()
	out, _ := io.ReadAll(outR)
	errOut, _ := io.ReadAll(errR)
	return string(out), string(errOut), err
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

func TestTheHomeFolderIsReadFromTheUserDatabaseWhenHOMEIsNotSet(t *testing.T) {
	// Given a user whose environment has no HOME
	u, err := user.Current()
	if err != nil || u.HomeDir == "" {
		t.Skip("no home folder in the user database")
	}
	want, err := projectfolders.Of(u.HomeDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", "")

	// When the home folder is asked for
	got := homeProject()

	// Then it is the one the user database gives, not no folder at all
	if got != want {
		t.Errorf("home = %q, want %q", got.Root(), want.Root())
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

// catalogFrom runs `agent-stats catalog args` from the folder cwd.
func (w workplace) catalogFrom(t *testing.T, cwd string, args ...string) (string, error) {
	t.Helper()
	t.Chdir(cwd)
	var out bytes.Buffer
	err := runCatalog(append([]string{"--db", w.db}, args...), &out, io.Discard)
	return out.String(), err
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

// detectedIn is the DetectProject use case of an index that holds sessions of the projects it is given, with the home folder.
func detectedIn(home kernel.Project, projects ...kernel.Project) func(kernel.Project) (kernel.Project, error) {
	return reporting.DetectProject{Queries: sessionsIn(projects), Home: home}.Run
}

type sessionsIn []kernel.Project

func (s sessionsIn) HasSessions(p kernel.Project) (bool, error) { return slices.Contains(s, p), nil }

func TestTheCoverageFollowsWhereSessionsWorkedAndWhatTheUserAskedFor(t *testing.T) {
	app, _ := kernel.NewProject("/work/app")
	home, _ := kernel.NewProject("/home/me")
	other, _ := kernel.NewProject("/work/other")
	detect := detectedIn(home, app, home)
	for name, c := range map[string]struct {
		all            bool
		given, working kernel.Project
		want           coverage
	}{
		"the working folder's project, with sessions":      {false, kernel.Project{}, app, coverage{project: app, restricted: true}},
		"a working folder with no session":                 {false, kernel.Project{}, other, coverage{}},
		"the home folder, though sessions worked below it": {false, kernel.Project{}, home, coverage{}},
		"--all, which keeps the project to switch to":      {true, kernel.Project{}, app, coverage{project: app, restricted: false}},
		"--all from a folder with no session":              {true, kernel.Project{}, other, coverage{}},
		"--project, even where no session worked":          {false, other, app, coverage{project: other, restricted: true}},
		"--project with --all, which wins":                 {true, other, app, coverage{project: other, restricted: false}},
		"no working folder":                                {false, kernel.Project{}, kernel.Project{}, coverage{}},
		"--project naming the home folder":                 {false, home, app, coverage{viaHome: true}},
		"--project naming the home folder with --all":      {true, home, app, coverage{viaHome: true}},
	} {
		got, err := chooseCoverage(c.all, c.given, c.working, home, detect)
		if err != nil || got != c.want {
			t.Errorf("%s: coverage = %+v, %v, want %+v", name, got, err, c.want)
		}
	}
}

func TestTheCoverageOfAnIndexThatCannotBeReadIsAnError(t *testing.T) {
	boom := errors.New("boom")
	app, _ := kernel.NewProject("/work/app")
	_, err := chooseCoverage(false, kernel.Project{}, app, kernel.Project{}, func(kernel.Project) (kernel.Project, error) { return kernel.Project{}, boom })
	if !errors.Is(err, boom) {
		t.Errorf("err = %v", err)
	}
}
