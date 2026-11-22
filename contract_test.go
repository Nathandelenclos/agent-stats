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

// TestReadModelHidesPromptTexts checks that the published views expose no prompt text.
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
		if strings.Contains(def, "prompts") || strings.Contains(def, "text") {
			t.Errorf("view %s exposes prompts: %s", name, def)
		}
		views = append(views, name)
	}
	if strings.Join(views, ",") != "usage_requests,usage_tool_calls" {
		t.Errorf("views = %v", views)
	}
}
