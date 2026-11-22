package main

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	indexing "github.com/Nathandelenclos/agent-stats/internal/indexing/application"
	"github.com/Nathandelenclos/agent-stats/internal/indexing/domain"
	"github.com/Nathandelenclos/agent-stats/internal/indexing/infra/sqlite"
	"github.com/Nathandelenclos/agent-stats/internal/kernel"
	"github.com/Nathandelenclos/agent-stats/internal/platform/projectfolders"
	reporting "github.com/Nathandelenclos/agent-stats/internal/reporting/application"
	report "github.com/Nathandelenclos/agent-stats/internal/reporting/domain"
	usagedb "github.com/Nathandelenclos/agent-stats/internal/reporting/infra/sqlite"
)

// TestIndexReadModelContract writes through the indexing adapter and reads through the reporting adapter:
// the two contexts share nothing but the index's published read model, and this test pins it.
func TestIndexReadModelContract(t *testing.T) {
	const hostile = `o'brian"; DROP TABLE requests; --`
	path := filepath.Join(t.TempDir(), "index.db")
	w, err := sqlite.Open(sqlite.Options{Path: path, User: "me@host"})
	if err != nil {
		t.Fatal(err)
	}
	at := time.UnixMilli(1767261600000)
	id, _ := domain.NewSessionID(kernel.Claude, "s1")
	tokens, _ := kernel.NewTokens(kernel.TokenCounts{Input: 1000, Output: 100, CacheRead: 300})
	cost, _ := kernel.NewCost(2)
	err = w.Save([]indexing.IndexedLog{{File: indexing.LogFile{Path: "/logs/s1.jsonl", Harness: kernel.Claude}, Transcript: indexing.IndexedTranscript{
		Transcript: domain.Transcript{
			Session:  domain.Session{ID: id, Harness: kernel.Claude, AgentType: domain.MainAgent, Folder: hostile, Start: at, End: at},
			Requests: []domain.Request{{Key: "m1", At: at, Model: hostile, Tokens: tokens, Duration: domain.NewDuration(1500 * time.Millisecond), Failure: "boom"}},
			Tools:    []domain.ToolCall{{Key: "t1", At: at, Name: hostile, Server: "grace", IsError: true}},
		},
		Costs: []indexing.PricedCost{{Cost: cost, Priced: true}},
	}}})[0]
	w.Close()
	if err != nil {
		t.Fatal(err)
	}

	r, err := usagedb.OpenReadOnly(path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	u, err := r.Total(report.Filter{Harness: kernel.Claude})
	if err != nil || u.Requests != 1 || u.Failed != 1 || u.Cost != 2 || u.CacheRead != 300 || u.AvgDuration != 1500 || !u.First.Equal(at) {
		t.Fatalf("total = %+v %v", u, err)
	}
	if g, err := r.UsageBy(report.Filter{}, report.ByFolder); err != nil || len(g) != 1 || g[0].Group != hostile {
		t.Errorf("by folder = %+v %v", g, err)
	}
	if g, err := r.UsageBy(report.Filter{}, report.ByAgentType); err != nil || len(g) != 1 || g[0].Group != "main" {
		t.Errorf("by agent type = %+v %v", g, err)
	}
	if tools, err := r.Tools(report.Filter{}); err != nil || len(tools) != 1 || tools[0].Name != hostile || tools[0].Errors != 1 {
		t.Errorf("tools = %+v %v", tools, err)
	}
}

// TestSessionReadModelContract writes sessions through the indexing adapter and reads them through the reporting adapter,
// with hostile values in every text the sessions view shows.
func TestSessionReadModelContract(t *testing.T) {
	const hostile = `o'brian"; DROP TABLE prompts; --`
	path := filepath.Join(t.TempDir(), "index.db")
	w, err := sqlite.Open(sqlite.Options{Path: path, User: "me@host"})
	if err != nil {
		t.Fatal(err)
	}
	at := time.UnixMilli(1767261600000)
	main, _ := domain.NewSessionID(kernel.Claude, "s1")
	kid, _ := main.Subagent("kid")
	cost, _ := kernel.NewCost(2)
	tokens, _ := kernel.NewTokens(kernel.TokenCounts{Input: 1000, Output: 100})
	save := func(file string, t domain.Transcript) error {
		costs := make([]indexing.PricedCost, len(t.Requests))
		for i := range costs {
			costs[i] = indexing.PricedCost{Cost: cost, Priced: true}
		}
		return w.Save([]indexing.IndexedLog{{File: indexing.LogFile{Path: file, Harness: kernel.Claude}, Transcript: indexing.IndexedTranscript{Transcript: t, Costs: costs, Signals: domain.AnalyzeSession(t.Prompts)}}})[0]
	}
	err = save("/logs/s1.jsonl", domain.Transcript{
		Session:  domain.Session{ID: main, Harness: kernel.Claude, AgentType: domain.MainAgent, Title: hostile, Folder: hostile, Start: at, End: at.Add(time.Hour)},
		Requests: []domain.Request{{Key: "m1", At: at, Model: hostile, Tokens: tokens}},
		Tools:    []domain.ToolCall{{Key: "t1", At: at, Name: hostile}},
		Prompts: []domain.Prompt{
			{Key: "p1", At: at, Text: hostile + " STOP SHOUTING NOW"},
			{Key: "p2", At: at, Text: hostile + " stop shouting now"},
		},
	})
	if err == nil {
		err = save("/logs/s1/kid.jsonl", domain.Transcript{
			Session:  domain.Session{ID: kid, Harness: kernel.Claude, ParentID: main, AgentType: domain.Subagent, Title: "kid", Start: at, End: at},
			Requests: []domain.Request{{Key: "m1", At: at, Model: "small", Tokens: tokens}},
			Prompts:  []domain.Prompt{{Key: "brief", At: at, Text: "a brief"}},
		})
	}
	w.Close()
	if err != nil {
		t.Fatal(err)
	}

	usage, err := usagedb.OpenReadOnly(path)
	if err != nil {
		t.Fatal(err)
	}
	defer usage.Close()
	r := usage.Sessions()
	list, err := r.Sessions(context.Background(), report.Filter{Harness: kernel.Claude})
	if err != nil || len(list) != 1 {
		t.Fatalf("sessions = %+v %v", list, err)
	}
	s := list[0]
	if s.Title != hostile || s.Folder != hostile || s.Requests != 2 || s.Cost != 4 || s.SubagentCount != 1 || s.PromptCount != 2 || len(s.Models) != 2 {
		t.Errorf("summary = %+v", s)
	}
	if d, ok := s.Duration(); !ok || d != time.Hour {
		t.Errorf("duration = %v %v", d, ok)
	}
	d, err := r.Session(context.Background(), s.ID, kernel.Project{})
	if err != nil || len(d.Tools) != 1 || d.Tools[0].Name != hostile || len(d.Subagents) != 1 || d.Subagents[0].Title != "kid" || d.Subagents[0].Cost != 2 {
		t.Fatalf("detail = %+v %v", d, err)
	}
	if len(d.Prompts) != 2 || !strings.HasPrefix(d.Prompts[0].Text, hostile) || d.Prompts[0].Signals.Yelling != domain.Analyze(d.Prompts[0].Text).Yelling || d.Prompts[0].Signals.Negation != 1 ||
		d.Prompts[0].Signals.Repeated || !d.Prompts[1].Signals.Repeated {
		t.Errorf("prompts = %+v", d.Prompts)
	}
}

// nestedRunsIndexed writes, through the indexing adapter, a session a whose subagent b started a subagent c: three
// generations of agents, each on its own model and last active 10, 15 and 20 minutes after at. It returns the index and at.
func nestedRunsIndexed(t *testing.T) (string, time.Time) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "index.db")
	w, err := sqlite.Open(sqlite.Options{Path: path, User: "me@host"})
	if err != nil {
		t.Fatal(err)
	}
	at := time.UnixMilli(1767261600000)
	a, _ := domain.NewSessionID(kernel.Claude, "a")
	b, _ := a.Subagent("b")
	c, _ := b.Subagent("c")
	cost, _ := kernel.NewCost(1)
	tokens, _ := kernel.NewTokens(kernel.TokenCounts{Input: 10, Output: 1})
	for i, s := range []domain.Session{
		{ID: a, Harness: kernel.Claude, AgentType: domain.MainAgent, Start: at, End: at.Add(10 * time.Minute)},
		{ID: b, Harness: kernel.Claude, ParentID: a, AgentType: domain.Subagent, Start: at, End: at.Add(15 * time.Minute)},
		{ID: c, Harness: kernel.Claude, ParentID: b, AgentType: domain.Subagent, Start: at, End: at.Add(20 * time.Minute)},
	} {
		model := []string{"opus", "sonnet", "haiku"}[i]
		err = w.Save([]indexing.IndexedLog{{File: indexing.LogFile{Path: "/logs/" + s.ID.String(), Harness: kernel.Claude}, Transcript: indexing.IndexedTranscript{
			Transcript: domain.Transcript{Session: s, Requests: []domain.Request{{Key: "m", At: at, Model: model, Tokens: tokens}}},
			Costs:      []indexing.PricedCost{{Cost: cost, Priced: true}},
		}}})[0]
		if err != nil {
			t.Fatal(err)
		}
	}
	w.Close()

	return path, at
}

// TestNestedSubagentsCountForTheTopSession reads the run of three generations of agents through the reporting adapter.
func TestNestedSubagentsCountForTheTopSession(t *testing.T) {
	path, _ := nestedRunsIndexed(t)
	usage, err := usagedb.OpenReadOnly(path)
	if err != nil {
		t.Fatal(err)
	}
	defer usage.Close()

	list, err := usage.Sessions().Sessions(context.Background(), report.Filter{})
	if err != nil || len(list) != 1 {
		t.Fatalf("sessions = %+v %v", list, err)
	}
	if s := list[0]; s.ID.String() != "claude:a" || s.Requests != 3 || s.Cost != 3 || s.SubagentCount != 2 || len(s.Models) != 3 {
		t.Errorf("summary = %+v", s)
	}
}

// TestASessionIsActiveUntilItsLastSubagentStops asks for the sessions active since a time that falls after the top
// session's own last entry and before its last subagent's.
func TestASessionIsActiveUntilItsLastSubagentStops(t *testing.T) {
	path, at := nestedRunsIndexed(t)
	usage, err := usagedb.OpenReadOnly(path)
	if err != nil {
		t.Fatal(err)
	}
	defer usage.Close()

	list, err := usage.Sessions().Sessions(context.Background(), report.Filter{Since: at.Add(15 * time.Minute)})
	if err != nil || len(list) != 1 {
		t.Fatalf("sessions = %+v %v", list, err)
	}
	s := list[0]
	if d, ok := s.Duration(); !ok || d != 20*time.Minute || !s.End.Equal(at.Add(20*time.Minute)) {
		t.Errorf("run = %v → %v (%v %v)", s.Start, s.End, d, ok)
	}
}

// publishedViews is the index's read model; only session_prompts may carry prompt text.
const publishedViews = "session_overview,session_prompts,session_requests,session_tool_calls,usage_folders,usage_requests,usage_tool_calls"

// TestReadModelHidesPromptTexts checks that the published views expose no prompt text, except the one made for it.
func TestReadModelHidesPromptTexts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "index.db")
	w, err := sqlite.Open(sqlite.Options{Path: path, User: "me@host"})
	if err != nil {
		t.Fatal(err)
	}
	w.Close()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := db.Query(`SELECT name, sql FROM sqlite_master WHERE type = 'view' ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var views []string
	for rows.Next() {
		var name, def string
		if err := rows.Scan(&name, &def); err != nil {
			t.Fatal(err)
		}
		if name != "session_prompts" && (strings.Contains(def, "prompts") || strings.Contains(def, "text")) {
			t.Errorf("view %s exposes prompts: %s", name, def)
		}
		views = append(views, name)
	}
	if strings.Join(views, ",") != publishedViews {
		t.Errorf("views = %v", views)
	}
}

// projectIndexed writes sessions of two folders through the indexing adapter, the app's sub-folder and a sibling that shares
// the app's name as a prefix, and returns the reporting adapter over the index and the app as a project.
func projectIndexed(t *testing.T) (*usagedb.SQLiteUsage, kernel.Project) {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	app, sibling := filepath.Join(dir, "app"), filepath.Join(dir, "app-2")
	path := filepath.Join(dir, "index.db")
	w, err := sqlite.Open(sqlite.Options{Path: path, User: "me@host"})
	if err != nil {
		t.Fatal(err)
	}
	at := time.UnixMilli(1767261600000)
	cost, _ := kernel.NewCost(1)
	tokens, _ := kernel.NewTokens(kernel.TokenCounts{Input: 10, Output: 1})
	for native, folder := range map[string]string{"in-app": filepath.Join(app, "web"), "in-sibling": sibling} {
		id, _ := domain.NewSessionID(kernel.Claude, native)
		prompts := []domain.Prompt{{Key: "p", At: at, Text: "NO NO KEEP THIS FILE"}}
		err := w.Save([]indexing.IndexedLog{{File: indexing.LogFile{Path: "/logs/" + native, Harness: kernel.Claude}, Transcript: indexing.IndexedTranscript{
			Transcript: domain.Transcript{
				Session:  domain.Session{ID: id, Harness: kernel.Claude, AgentType: domain.MainAgent, Folder: folder, Start: at, End: at},
				Requests: []domain.Request{{Key: "m", At: at, Model: "m", Tokens: tokens}},
				Tools:    []domain.ToolCall{{Key: "t", At: at, Name: native}},
				Prompts:  prompts,
			},
			Costs:   []indexing.PricedCost{{Cost: cost, Priced: true}},
			Signals: domain.AnalyzeSession(prompts),
		}}})[0]
		if err != nil {
			t.Fatal(err)
		}
	}
	w.Close()
	usage, err := usagedb.OpenReadOnly(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { usage.Close() })
	project, err := projectfolders.Of(app)
	if err != nil {
		t.Fatal(err)
	}
	return usage, project
}

func TestTheRequestsOfAProjectAreThoseOfTheSessionsThatWorkedBelowIt(t *testing.T) {
	// Given sessions of the index in the app and in a sibling sharing its name as a prefix
	usage, app := projectIndexed(t)

	// When the usage of the app is asked for
	total, err := usage.Total(report.Filter{Project: app})

	// Then only the session below it is counted
	if err != nil || total.Requests != 1 {
		t.Errorf("total = %+v, %v", total, err)
	}
}

func TestTheToolCallsOfAProjectAreThoseOfTheSessionsThatWorkedBelowIt(t *testing.T) {
	usage, app := projectIndexed(t)

	tools, err := usage.Tools(report.Filter{Project: app})

	if err != nil || len(tools) != 1 || tools[0].Name != "in-app" {
		t.Errorf("tools = %+v, %v", tools, err)
	}
}

func TestTheSessionsOfAProjectAreThoseThatWorkedBelowIt(t *testing.T) {
	usage, app := projectIndexed(t)

	sessions, err := usage.Sessions().Sessions(context.Background(), report.Filter{Project: app})

	if err != nil || len(sessions) != 1 || sessions[0].ID.String() != "claude:in-app" {
		t.Errorf("sessions = %+v, %v", sessions, err)
	}
}

func TestSessionsAreFoundInAProjectTheyWorkedBelow(t *testing.T) {
	usage, app := projectIndexed(t)

	found, err := usage.HasSessions(app)

	if err != nil || !found {
		t.Errorf("found = %v, %v", found, err)
	}
}

// TestFlaggedPromptsReadModelContract writes flagged prompts through the indexing adapter, in a session and in the subagent
// it started, and reads them through the reporting adapter: the review lists the user's own prompts, with their session.
func TestFlaggedPromptsReadModelContract(t *testing.T) {
	const hostile = `o'brian"; DROP TABLE prompts; --`
	path := filepath.Join(t.TempDir(), "index.db")
	w, err := sqlite.Open(sqlite.Options{Path: path, User: "me@host"})
	if err != nil {
		t.Fatal(err)
	}
	at := time.UnixMilli(1767261600000)
	main, _ := domain.NewSessionID(kernel.Claude, "s1")
	kid, _ := main.Subagent("kid")
	save := func(file string, s domain.Session, prompts []domain.Prompt) error {
		return w.Save([]indexing.IndexedLog{{File: indexing.LogFile{Path: file, Harness: kernel.Claude}, Transcript: indexing.IndexedTranscript{
			Transcript: domain.Transcript{Session: s, Prompts: prompts}, Signals: domain.AnalyzeSession(prompts)}}})[0]
	}
	err = save("/logs/s1.jsonl", domain.Session{ID: main, Harness: kernel.Claude, AgentType: domain.MainAgent, Title: hostile, Folder: hostile, Start: at, End: at},
		[]domain.Prompt{
			{Key: "p1", At: at, Text: "add a test"},
			{Key: "p2", At: at.Add(time.Minute), Text: hostile + " NO, STOP SHOUTING NOW"},
		})
	if err == nil {
		err = save("/logs/s1/kid.jsonl", domain.Session{ID: kid, Harness: kernel.Claude, ParentID: main, AgentType: domain.Subagent, Title: "kid", Start: at, End: at},
			[]domain.Prompt{{Key: "brief", At: at, Text: "NO NO NO brief"}})
	}
	w.Close()
	if err != nil {
		t.Fatal(err)
	}

	usage, err := usagedb.OpenReadOnly(path)
	if err != nil {
		t.Fatal(err)
	}
	defer usage.Close()
	got, err := reporting.ReviewPrompts{Queries: usage.Prompts()}.Run(reporting.Query{Since: at, Harness: kernel.Claude})
	if err != nil || len(got.Prompts) != 1 {
		t.Fatalf("review = %+v %v", got, err)
	}
	p := got.Prompts[0]
	if p.Session.String() != main.String() || p.Title != hostile || p.Folder != hostile || !strings.HasPrefix(p.Text, hostile) || p.Signals.Negation == 0 || p.Signals.Yelling == 0 {
		t.Errorf("prompt = %+v", p)
	}
	if len(got.Folders) != 1 || got.Folders[0].Folder != hostile || got.Folders[0].Prompts != 1 {
		t.Errorf("folders = %+v", got.Folders)
	}
}

func TestThePromptsReviewOnlyIncludesSessionsInTheProject(t *testing.T) {
	usage, app := projectIndexed(t)
	got, err := (reporting.ReviewPrompts{Queries: usage.Prompts()}).Run(reporting.Query{Project: app})
	if err != nil || len(got.Prompts) != 1 || got.Prompts[0].Session.String() != "claude:in-app" {
		t.Fatalf("project prompts = %+v, %v", got.Prompts, err)
	}
}
