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
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/Nathandelenclos/agent-stats/internal/kernel"
	reporting "github.com/Nathandelenclos/agent-stats/internal/reporting/application"
)

// A workplace is a home folder and, next to it, an app in a Git repository (with a sub-folder), a sibling whose name
// starts with the app's and a link to the app's sub-folder. One Claude session worked in each of the app (1 request), its
// sub-folder (2), and the sibling (4): the number of requests a view counts tells which sessions it covers.
type workplace struct {
	home, app, sub, sibling, link string
	db                            string
}

func newScopedWorkplace(t *testing.T) workplace {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	w := workplace{home: filepath.Join(root, "home"), app: filepath.Join(root, "work", "app"), sibling: filepath.Join(root, "work", "app-2"),
		link: filepath.Join(root, "shortcut"), db: filepath.Join(root, "index.db")}
	w.sub = filepath.Join(w.app, "web")
	for _, d := range []string{w.home, filepath.Join(w.app, ".git"), w.sub, w.sibling} {
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
		"a folder no session worked in":   {w.home, "0", ""},
	} {
		// When the report is printed from the home folder for that project
		out, _, err := w.report(t, w.home, "--project", c.dir)
		if err != nil {
			t.Fatal(err)
		}

		// Then it counts that project, whether sessions worked in it or not
		if c.requests == "0" {
			if !strings.HasPrefix(out, "Project: home ("+w.home+")\nNo requests in this range.") {
				t.Errorf("%s: %q", name, out)
			}
			continue
		}
		if requests, first := requestsOf(t, out); requests != c.requests || first != c.first {
			t.Errorf("%s: %s requests, first line %q", name, requests, first)
		}
	}
}

func TestTheFlagsAllAndProjectExcludeEachOther(t *testing.T) {
	w := newScopedWorkplace(t)
	if _, _, err := w.report(t, w.app, "--all", "--project", w.sibling); err == nil || !strings.Contains(err.Error(), "exclude each other") {
		t.Errorf("err = %v", err)
	}
}

func TestTheJSONOfAProjectKeepsItsFormatAndNamesTheProjectOnStderr(t *testing.T) {
	// Given the same sessions
	w := newScopedWorkplace(t)

	// When the JSON report is printed from the repository, and from the home folder
	out, errOut, err := w.report(t, w.app, "--json")
	if err != nil {
		t.Fatal(err)
	}
	whole, wholeErr, err := w.report(t, w.home, "--json")
	if err != nil {
		t.Fatal(err)
	}

	// Then both have the same fields, the project's counts only its sessions, and stdout holds nothing but the JSON
	var scoped, global map[string]json.RawMessage
	if err := json.Unmarshal([]byte(out), &scoped); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, out)
	}
	if err := json.Unmarshal([]byte(whole), &global); err != nil {
		t.Fatal(err)
	}
	if len(scoped) != len(global) || len(scoped) != 6 {
		t.Errorf("fields: %d scoped, %d global", len(scoped), len(global))
	}
	var total struct{ Requests int }
	_ = json.Unmarshal(scoped["total"], &total)
	if total.Requests != 3 {
		t.Errorf("requests = %d", total.Requests)
	}
	if !strings.Contains(errOut, "Project: app ("+w.app+")") || strings.Contains(wholeErr, "Project:") {
		t.Errorf("stderr: %q and %q", errOut, wholeErr)
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

func TestTheCatalogProjectFlagKeepsItsMeaningAndScopesToo(t *testing.T) {
	// Given the same workplace
	w := newScopedWorkplace(t)

	// When the catalogue is printed from the home folder for the sibling project, then with no project at all
	sibling, err := w.catalogFrom(t, w.home, "--project", w.sibling)
	if err != nil {
		t.Fatal(err)
	}
	none, err := w.catalogFrom(t, w.app, "--project", "")
	if err != nil {
		t.Fatal(err)
	}

	// Then the first is about the sibling alone, and the second reads no project-level configuration and counts everyone
	if !strings.HasPrefix(sibling, "Project: app-2 (") || !strings.Contains(sibling, "Grep") || strings.Contains(sibling, "Read") {
		t.Errorf("sibling:\n%s", sibling)
	}
	if !strings.HasPrefix(none, "All projects") || strings.Contains(none, "app-skill") || !strings.Contains(none, "Grep") {
		t.Errorf("no project:\n%s", none)
	}
}

// foundIn is a project index that holds sessions of the projects it is given.
type foundIn []kernel.Project

func (f foundIn) HasSessions(p kernel.Project) (bool, error) {
	for _, known := range f {
		if known == p {
			return true, nil
		}
	}
	return false, nil
}

func TestTheScopeFollowsWhereSessionsWorkedAndWhatTheUserAskedFor(t *testing.T) {
	app, _ := kernel.NewProject("/work/app")
	home, _ := kernel.NewProject("/home/me")
	other, _ := kernel.NewProject("/work/other")
	sessions := reporting.DetectProject{Queries: foundIn{app}}
	for name, c := range map[string]struct {
		all            bool
		given, working kernel.Project
		want           scope
	}{
		"the working folder's project, with sessions":      {false, kernel.Project{}, app, scope{app, true}},
		"a working folder with no session":                 {false, kernel.Project{}, home, scope{}},
		"--all, which keeps the project to switch to":      {true, kernel.Project{}, app, scope{app, false}},
		"--all from a folder with no session":              {true, kernel.Project{}, home, scope{}},
		"--project, even where no session worked":          {false, other, app, scope{other, true}},
		"--project with --all (the catalogue allows both)": {true, other, app, scope{other, false}},
		"no working folder":                                {false, kernel.Project{}, kernel.Project{}, scope{}},
	} {
		got, err := chooseScope(c.all, c.given, c.working, sessions)
		if err != nil || got != c.want {
			t.Errorf("%s: scope = %+v, %v, want %+v", name, got, err, c.want)
		}
	}
}

func TestTheScopeOfAnIndexThatCannotBeReadIsAnError(t *testing.T) {
	boom := errors.New("boom")
	app, _ := kernel.NewProject("/work/app")
	if _, err := chooseScope(false, kernel.Project{}, app, reporting.DetectProject{Queries: failing{boom}}); !errors.Is(err, boom) {
		t.Errorf("err = %v", err)
	}
}

type failing struct{ err error }

func (f failing) HasSessions(kernel.Project) (bool, error) { return false, f.err }
