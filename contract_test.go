package main

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	indexing "github.com/Nathandelenclos/agent-stats/internal/indexing/application"
	"github.com/Nathandelenclos/agent-stats/internal/indexing/domain"
	"github.com/Nathandelenclos/agent-stats/internal/indexing/infra/sqlite"
	"github.com/Nathandelenclos/agent-stats/internal/kernel"
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
	err = w.Save(indexing.LogFile{Path: "/logs/s1.jsonl", Harness: kernel.Claude}, indexing.IndexedTranscript{
		Transcript: domain.Transcript{
			Session:  domain.Session{ID: id, Harness: kernel.Claude, AgentType: domain.MainAgent, Folder: hostile, Start: at, End: at},
			Requests: []domain.Request{{Key: "m1", At: at, Model: hostile, Tokens: tokens, Duration: domain.NewDuration(1500 * time.Millisecond), Failure: "boom"}},
			Tools:    []domain.ToolCall{{Key: "t1", At: at, Name: hostile, Server: "grace", IsError: true}},
		},
		Costs: []indexing.PricedCost{{Cost: cost, Priced: true}},
	})
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
		return w.Save(indexing.LogFile{Path: file, Harness: kernel.Claude}, indexing.IndexedTranscript{Transcript: t, Costs: costs, Signals: domain.AnalyzeSession(t.Prompts)})
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
	list, err := r.Sessions(report.Filter{Harness: kernel.Claude})
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
	d, err := r.Session(s.ID)
	if err != nil || len(d.Tools) != 1 || d.Tools[0].Name != hostile || len(d.Subagents) != 1 || d.Subagents[0].Title != "kid" || d.Subagents[0].Cost != 2 {
		t.Fatalf("detail = %+v %v", d, err)
	}
	if len(d.Prompts) != 2 || !strings.HasPrefix(d.Prompts[0].Text, hostile) || d.Prompts[0].Signals.Yelling != domain.Analyze(d.Prompts[0].Text).Yelling || d.Prompts[0].Signals.Negation != 1 ||
		d.Prompts[0].Signals.Repeated || !d.Prompts[1].Signals.Repeated {
		t.Errorf("prompts = %+v", d.Prompts)
	}
}

// publishedViews is the index's read model; only session_prompts may carry prompt text.
const publishedViews = "session_overview,session_prompts,session_requests,session_tool_calls,usage_requests,usage_tool_calls"

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
