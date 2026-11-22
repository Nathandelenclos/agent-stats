package main

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"testing"

	indexing "github.com/Nathandelenclos/agent-stats/internal/indexing/application"
	"github.com/Nathandelenclos/agent-stats/internal/indexing/infra/logfiles"
	indexdb "github.com/Nathandelenclos/agent-stats/internal/indexing/infra/sqlite"
	"github.com/Nathandelenclos/agent-stats/internal/kernel"
	pricing "github.com/Nathandelenclos/agent-stats/internal/pricing/domain"
	reporting "github.com/Nathandelenclos/agent-stats/internal/reporting/application"
	usagedb "github.com/Nathandelenclos/agent-stats/internal/reporting/infra/sqlite"
)

func TestGrokImportsOnlyAttributedActivity(t *testing.T) {
	// Given Grok sessions with multipart prompts, tool updates, and both single-call and aggregate usage
	home := t.TempDir()
	grokHome := filepath.Join(home, "grok-profile")
	t.Setenv("GROK_HOME", grokHome)
	if err := os.CopyFS(grokHome, os.DirFS("testdata/home-grok")); err != nil {
		t.Fatal(err)
	}
	logs := logfiles.NewFileSessionLogs(logfiles.DefaultSources(home))
	files, problems, err := logs.List()
	if err != nil || len(problems) != 0 || len(files) != 3 {
		t.Fatalf("discovery: %d logs, %v, %v", len(files), problems, err)
	}

	// When the source reads those sessions
	for _, file := range files {
		tr, err := logs.Read(file)
		if err != nil {
			t.Fatal(err)
		}
		switch tr.Session.ID.String() {
		case "grok:example":
			if tr.Session.Folder != "/example/work" || tr.Session.Title != "Neutral example" ||
				len(tr.Prompts) != 1 || tr.Prompts[0].Text != "Check the sample" || tr.SkippedLines != 1 {
				t.Fatal("session metadata, prompt fragments or malformed line were mishandled")
			}
			if len(tr.Tools) != 1 {
				t.Fatalf("tools = %d", len(tr.Tools))
			}
			call := tr.Tools[0]
			if call.Name != "read_file" || call.ArgsChars != 12 || call.ResultChars != 18 || !call.IsError {
				t.Fatalf("tool = %+v", call)
			}
			if len(tr.Requests) != 1 {
				t.Fatalf("requests = %d, want only the single-call turn", len(tr.Requests))
			}
			req := tr.Requests[0]
			if got := req.Tokens.Counts(); got != (kernel.TokenCounts{Input: 7, Output: 4, CacheRead: 2, CacheWrite: 1, Reasoning: 1}) ||
				req.Model != "sample-model" || math.Abs(req.LoggedCost.USD()-0.01) > 1e-12 || req.Duration.Known() || req.TTFT.Known() {
				t.Fatalf("request = %+v", req)
			}
		case "grok:leader":
			if len(tr.Requests) != 0 || tr.Session.Folder != "/example/work" {
				t.Fatal("leader usage included a child's work or lost the encoded folder")
			}
		case "grok:child":
			if tr.Session.ParentID.String() != "grok:leader" || tr.Session.AgentType.String() != "general-purpose" ||
				len(tr.Requests) != 1 || len(tr.Prompts) != 1 || tr.Prompts[0].Text != "Check the child" {
				t.Fatal("child activity or parent reference was lost")
			}
		default:
			t.Fatal("unexpected session")
		}
	}

	// Then only the selected project's attributed calls reach the usage report
	db := filepath.Join(t.TempDir(), "index.db")
	idx, err := indexdb.Open(indexdb.Options{Path: db})
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()
	indexer := indexing.IndexSessions{Logs: logs, Store: idx, Prices: pricing.PriceList{}, Workers: 1}
	indexed, err := indexer.Run(nil)
	if err != nil || indexed.Indexed != 3 || len(indexed.Errors) != 0 {
		t.Fatalf("index: %+v, %v", indexed, err)
	}
	usage, err := usagedb.OpenReadOnly(db)
	if err != nil {
		t.Fatal(err)
	}
	defer usage.Close()
	project, err := kernel.NewProject("/example/work")
	if err != nil {
		t.Fatal(err)
	}
	report, err := (reporting.BuildReport{Queries: usage}).Run(context.Background(), reporting.Query{Harness: kernel.Grok, Project: project})
	if err != nil || report.Total.Requests != 2 {
		t.Fatalf("project calls = %d, err = %v", report.Total.Requests, err)
	}

	// Given a change to a companion file only, the indexed session is read again
	path := filepath.Join(grokHome, "sessions", "long-path--123", "example", "usage.json")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("\n"); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	again, err := indexer.Run(nil)
	if err != nil || again.Indexed != 1 || len(again.Errors) != 0 {
		t.Fatalf("reindex: %+v, %v", again, err)
	}
}
