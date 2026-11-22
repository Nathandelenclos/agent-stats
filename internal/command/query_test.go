package command

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Nathandelenclos/agent-stats/internal/indexing/infra/sqlite"
	"github.com/Nathandelenclos/agent-stats/internal/quota"
)

func queryFixture(t *testing.T) (string, string) {
	t.Helper()
	project := t.TempDir()
	path := filepath.Join(t.TempDir(), "index.db")
	index, err := sqlite.Open(sqlite.Options{Path: path, User: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { index.Close() })
	now := time.Now().Add(-time.Hour)
	if err := index.RecordQuotaWindows(t.Context(), []quota.Window{
		{Provider: "codex", Label: "weekly", Remaining: 45, Observed: now, ResetAt: now.Add(time.Hour)},
		{Provider: "codex", Label: "weekly", Remaining: 60, Observed: now.Add(-time.Hour), ResetAt: now.Add(time.Hour)},
	}); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, session := range []struct {
		id, folder, provider string
	}{
		{"codex:fixture/main", project, "first"},
		{"codex:other", t.TempDir(), "second"},
	} {
		if _, err := db.Exec(`INSERT INTO sessions VALUES (?, ?, 'codex', NULL, 'main', 'Fixture', ?, 'tester', ?, ?)`,
			session.id, session.id, session.folder, now.UnixMilli(), now.UnixMilli()); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO requests VALUES (?, 'r', ?, ?, 'example', ?, 100, 20, 300, 40, 0, 1.25, 1, 2000, 300, NULL, NULL)`,
			session.id, session.id, now.UnixMilli(), session.provider); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`INSERT INTO prompts VALUES ('codex:fixture/main', 'p', 'codex:fixture/main', ?, 'Fixture prompt text', 19, 3, 1, 0, 0, 0, 0, 0);
		INSERT INTO tool_calls VALUES ('codex:fixture/main', 't', 'codex:fixture/main', ?, 'read', 'memory', 10, 20, 0, 5)`,
		now.UnixMilli(), now.UnixMilli()); err != nil {
		t.Fatal(err)
	}
	return path, project
}

func queryResult(t *testing.T, db string, args ...string) queryEnvelope {
	t.Helper()
	var out, errOut bytes.Buffer
	err := runQuery(t.Context(), append(args, "--db", db), &out, &errOut)
	if err != nil {
		t.Fatalf("query %v: %v (%s)", args, err, &errOut)
	}
	var result queryEnvelope
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("query %v: invalid JSON: %v (%q)", args, err, &out)
	}
	return result
}

func TestQueryLinksScopedOverviewModelsAndRequests(t *testing.T) {
	db, project := queryFixture(t)
	overview := queryResult(t, db, "overview", "--since", "7d", "--project", project, "--json")
	var totals struct {
		Total struct{ Requests int }
	}
	if err := json.Unmarshal(overview.Data, &totals); err != nil || totals.Total.Requests != 1 || overview.Scope != "project" || overview.Project == "" {
		t.Fatalf("overview = %+v, data = %s, %v", overview, overview.Data, err)
	}

	models := queryResult(t, db, "models", "--all", "--since", "7d")
	var series struct {
		Samples []struct{ Name, Source string }
	}
	if err := json.Unmarshal(models.Data, &series); err != nil || len(series.Samples) != 2 {
		t.Fatalf("models = %s, %v", models.Data, err)
	}
	requests := queryResult(t, db, "requests", "--all", "--model", "example", "--provider", "first", "--limit", "1")
	var page struct {
		Requests []struct{ SessionID, Provider string }
		HasMore  bool
	}
	if err := json.Unmarshal(requests.Data, &page); err != nil || len(page.Requests) != 1 ||
		page.Requests[0].SessionID != "codex:fixture/main" || page.Requests[0].Provider != "first" || page.HasMore {
		t.Fatalf("requests = %s, %v", requests.Data, err)
	}
}

func TestQueryPaginatesSessionsAndOnlyOpensAnExplicitPrompt(t *testing.T) {
	db, project := queryFixture(t)
	first := queryResult(t, db, "sessions", "--all", "--limit", "1")
	second := queryResult(t, db, "sessions", "--all", "--limit", "1", "--offset", "1")
	var a, b struct {
		Count    int
		Sessions []struct{ ID string }
	}
	if err := json.Unmarshal(first.Data, &a); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(second.Data, &b); err != nil || a.Count != 2 || b.Count != 2 || len(a.Sessions) != 1 ||
		len(b.Sessions) != 1 || a.Sessions[0].ID == b.Sessions[0].ID {
		t.Fatalf("pages = %s and %s, %v", first.Data, second.Data, err)
	}
	if bytes.Contains(first.Data, []byte("Fixture prompt text")) {
		t.Fatal("listing exposed the prompt text")
	}

	session := queryResult(t, db, "session", "--project", project, "--session", "codex:fixture/main")
	if bytes.Contains(session.Data, []byte("Fixture prompt text")) || !bytes.Contains(session.Data, []byte(`"position":1`)) {
		t.Fatalf("session must keep prompt references without excerpts: %s", session.Data)
	}
	flagged := queryResult(t, db, "prompts", "--project", project, "--excerpts", "--limit", "1")
	if !bytes.Contains(flagged.Data, []byte(`"excerpt":"Fixture prompt text"`)) {
		t.Fatalf("explicit excerpt request = %s", flagged.Data)
	}

	prompt := queryResult(t, db, "prompt", "--project", project, "--session", "codex:fixture/main", "--position", "1")
	var body struct{ Text string }
	if err := json.Unmarshal(prompt.Data, &body); err != nil || body.Text != "Fixture prompt text" {
		t.Fatalf("prompt = %s, %v", prompt.Data, err)
	}
}

func TestQueryReadsQuotaHistoryWithoutPollingAndRejectsInvalidInputs(t *testing.T) {
	db, _ := queryFixture(t)
	page := queryResult(t, db, "quotas", "--since", "24h", "--limit", "1", "--offset", "1")
	var history struct {
		Windows []quota.Window
		HasMore bool
	}
	if err := json.Unmarshal(page.Data, &history); err != nil || page.Scope != "account" ||
		len(history.Windows) != 1 || history.Windows[0].Remaining != 60 || history.HasMore {
		t.Fatalf("quota history = %s, %v", page.Data, err)
	}
	for _, args := range [][]string{
		{"requests", "--provider", "first"},
		{"overview", "--limit", "5"},
		{"prompt", "--position", "1"},
		{"quotas", "--project", "."},
	} {
		var out, errOut bytes.Buffer
		if err := runQuery(context.Background(), append(args, "--db", db), &out, &errOut); err == nil || out.Len() != 0 {
			t.Fatalf("invalid query %v = %v, %s", args, err, &out)
		}
	}
}

func TestQueryDoesNotCreateMissingIndex(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.db")
	var out, errOut bytes.Buffer
	if err := runQuery(t.Context(), []string{"overview", "--all", "--db", path}, &out, &errOut); err == nil || out.Len() != 0 {
		t.Fatalf("missing index = %v, %s", err, &out)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("query created an index: %v", err)
	}
}

func TestQueryOpensTheOtherAnalysisDatasets(t *testing.T) {
	db, project := queryFixture(t)
	t.Setenv("HOME", t.TempDir())
	for _, args := range [][]string{
		{"tools"},
		{"tool-models"},
		{"session", "--session", "codex:fixture/main"},
		{"requests", "--session", "codex:fixture/main"},
		{"trace", "--session", "codex:fixture/main", "--limit", "1"},
		{"related-sessions", "--kind", "tool", "--value", "read", "--server", "memory"},
		{"errors", "--limit", "3"},
		{"prompts", "--limit", "1", "--offset", "1"},
		{"prompt-signals"},
		{"catalog", "--unused"},
	} {
		t.Run(args[0], func(t *testing.T) {
			result := queryResult(t, db, append(args, "--project", project)...)
			if result.Dataset != args[0] || result.SchemaVersion != 1 || result.Scope != "project" || result.Since != "all" {
				t.Fatalf("unexpected result metadata: %+v", result)
			}
		})
	}
}

func TestQueryDoesNotWidenAnEmptyCurrentProject(t *testing.T) {
	db, _ := queryFixture(t)
	t.Chdir(t.TempDir())
	result := queryResult(t, db, "sessions")
	var listing struct{ Count int }
	if err := json.Unmarshal(result.Data, &listing); err != nil || result.Scope != "project" || listing.Count != 0 {
		t.Fatalf("empty project scope = %+v, %v", result, err)
	}
}
