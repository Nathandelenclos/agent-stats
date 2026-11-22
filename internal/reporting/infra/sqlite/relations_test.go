package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Nathandelenclos/agent-stats/internal/kernel"
	"github.com/Nathandelenclos/agent-stats/internal/platform/projectfolders"
	reporting "github.com/Nathandelenclos/agent-stats/internal/reporting/application"
	report "github.com/Nathandelenclos/agent-stats/internal/reporting/domain"
)

func writeTestIndex(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func scopeRelatedFixture(t *testing.T, db *sql.DB) (kernel.Project, string) {
	t.Helper()
	root := t.TempDir()
	project, err := projectfolders.Of(filepath.Join(root, "project"))
	if err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(root, "other")
	for _, stmt := range []struct {
		query string
		args  []any
	}{
		{"CREATE TABLE usage_folders (folder TEXT)", nil},
		{"INSERT INTO usage_folders VALUES (?), (?)", []any{project.Root(), other}},
		{"UPDATE session_overview SET folder = ? WHERE id = 'claude:main'", []any{project.Root()}},
		{"UPDATE session_overview SET folder = ? WHERE id IN ('claude:main/kid', 'codex:old')", []any{other}},
	} {
		if _, err := db.Exec(stmt.query, stmt.args...); err != nil {
			t.Fatal(err)
		}
	}
	return project, other
}

func TestOpeningOnePromptReadsItsFullMultilineTextWithinTheProject(t *testing.T) {
	index := sessionsIndexed(t)
	w := writeTestIndex(t, index)
	p, _ := scopeRelatedFixture(t, w)
	body := "First line\n\n" + strings.Repeat("é", report.PromptTextLimit) + "\n  Final line"
	_, err := w.Exec(`INSERT INTO session_prompts VALUES ('codex:old', 3, 0, 'separate session', 16, 2, 0, 0, 0, 0, 0, 0);
		UPDATE session_prompts SET text = ?, chars = ? WHERE session_id = 'claude:main' AND position = 3`, body, len([]rune(body)))
	if err != nil {
		t.Fatal(err)
	}
	s := readTestSessions(t, index)
	ctx := context.Background()
	id, _ := report.NewSessionID("claude:main")
	detail, err := s.Session(ctx, id, p)
	if err != nil {
		t.Fatal(err)
	}
	preview := detail.Prompts[2]
	if preview.Position != 3 || len([]rune(preview.Text)) != report.PromptTextLimit || !strings.HasPrefix(body, preview.Text) {
		t.Fatalf("expected only a positioned preview, got position %d with length %d", preview.Position, len([]rune(preview.Text)))
	}
	query := reporting.Query{Project: p}
	show := reporting.ShowSessionPrompt{Queries: s}
	full, err := show.Run(ctx, id, preview.Position, query)
	if err != nil || full.Text != body || full.Chars != preview.Chars || !full.At.Equal(preview.At) {
		t.Errorf("full prompt differed from the indexed prompt: %d chars, %v", full.Chars, err)
	}

	outside, _ := report.NewSessionID("codex:old")
	if _, err := show.Run(ctx, outside, preview.Position, query); !errors.Is(err, report.ErrPromptNotFound) {
		t.Errorf("prompt outside the project: %v", err)
	}
	if _, err := show.Run(ctx, id, 999, query); !errors.Is(err, report.ErrPromptNotFound) {
		t.Errorf("missing prompt: %v", err)
	}
}

func TestRelatedSessionsOnlyCountRequestsInTheSelectedSlice(t *testing.T) {
	path := sessionsIndexed(t)
	w := writeTestIndex(t, path)
	project, other := scopeRelatedFixture(t, w)
	day := time.Date(2026, 3, 8, 0, 0, 0, 0, time.Local)
	dayKey, err := time.Parse(time.DateOnly, day.Format(time.DateOnly))
	if err != nil {
		t.Fatal(err)
	}
	dayStart := day.UnixMilli()
	dayEnd := day.AddDate(0, 0, 1).UnixMilli()
	_, err = w.Exec(`UPDATE session_requests SET ts = CASE
			WHEN session_id = 'claude:main/kid' THEN ?
			WHEN session_id = 'codex:old' THEN ?
			WHEN model = 'cheap' THEN ?
			ELSE ? END`, dayEnd, dayStart-1, dayStart, dayEnd-1)
	if err != nil {
		t.Fatal(err)
	}
	s := readTestSessions(t, path)
	related := reporting.ListRelatedSessions{Queries: s}
	for _, tc := range []struct {
		name string
		q    reporting.Query
		rel  reporting.SessionRelation
		ids  string
		cost report.USD
		n    int64
	}{
		{"actual calendar day", reporting.Query{}, reporting.SessionRelation{Kind: reporting.RelationDay, Day: dayKey}, "claude:main", 4, 2},
		{"time intersection", reporting.Query{Since: day.Add(time.Second)}, reporting.SessionRelation{Kind: reporting.RelationDay, Day: day}, "claude:main", 3, 1},
		{"harness intersection", reporting.Query{Harness: kernel.Codex}, reporting.SessionRelation{Kind: reporting.RelationDay, Day: day}, "", 0, 0},
		{"model", reporting.Query{Harness: kernel.Claude}, reporting.SessionRelation{Kind: reporting.RelationModel, Value: "cheap"}, "claude:main", 1.5, 2},
		{"model in project", reporting.Query{Project: project}, reporting.SessionRelation{Kind: reporting.RelationModel, Value: "cheap"}, "claude:main", 1, 1},
		{"folder matches each contribution", reporting.Query{Harness: kernel.Claude}, reporting.SessionRelation{Kind: reporting.RelationFolder, Value: other}, "claude:main", 0.5, 1},
		{"subagent contribution", reporting.Query{}, reporting.SessionRelation{Kind: reporting.RelationAgent, Value: hostile}, "claude:main", 0.5, 1},
		{"harness", reporting.Query{}, reporting.SessionRelation{Kind: reporting.RelationHarness, Value: "codex"}, "codex:old", 7, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			list, err := related.Run(context.Background(), tc.q, tc.rel)
			if err != nil || ids(list.Sessions) != tc.ids || list.Total.Cost != tc.cost || list.Total.Requests != tc.n {
				t.Fatalf("related = %q, %+v, %v", ids(list.Sessions), list.Total, err)
			}
			var requests int64
			for _, session := range list.Sessions {
				for _, model := range session.Models {
					requests += model.Requests
				}
			}
			if requests != tc.n {
				t.Errorf("models include requests outside the selected slice: %d", requests)
			}
		})
	}
}

func TestRelatedToolSessionsCountMatchingCallsWithoutAttributingCost(t *testing.T) {
	s := openSessions(t)
	ctx := context.Background()
	related := reporting.ListRelatedSessions{Queries: s}
	rel := reporting.SessionRelation{Kind: reporting.RelationTool, Value: "Bash"}
	list, err := related.Run(ctx, reporting.Query{Since: time.UnixMilli(t0 + 1)}, rel)
	if err != nil || ids(list.Sessions) != "claude:main" {
		t.Fatalf("tool sessions = %q, %v", ids(list.Sessions), err)
	}
	match := list.Sessions[0]
	if match.RelatedTool == nil || match.RelatedTool.Calls != 1 || match.RelatedTool.Errors != 1 || !match.RelatedTool.Last.Equal(time.UnixMilli(t1)) {
		t.Errorf("tool counts include activity outside the selected slice: %+v", match.RelatedTool)
	}
	if match.Requests != 0 || match.Cost != 0 || len(match.Models) != 0 {
		t.Errorf("model usage was attributed to the tool: %+v", match)
	}

	rel.Value, rel.Server = hostile, "another"
	missing, err := related.Run(ctx, reporting.Query{}, rel)
	if err != nil || len(missing.Sessions) != 0 {
		t.Errorf("different MCP server matched: %+v, %v", missing, err)
	}
	rel.Server = "grace"
	found, err := related.Run(ctx, reporting.Query{}, rel)
	if err != nil || len(found.Sessions) != 1 || found.Sessions[0].RelatedTool == nil || found.Sessions[0].RelatedTool.Server != "grace" {
		t.Errorf("MCP tool did not match: %+v, %v", found, err)
	}
}

func TestRelatedMCPAggregatesMatchingCommandsOfTheServer(t *testing.T) {
	path := sessionsIndexed(t)
	w := writeTestIndex(t, path)
	if _, err := w.Exec(`INSERT INTO session_tool_calls VALUES
		('claude:main', 'claude:main/kid', ?, 'another command', 'grace', 1, 2, 1, 50),
		('claude:main', 'claude:main/kid', ?, 'another command', 'grace-example', 1, 2, 0, 50),
		('claude:main', 'claude:main/kid', ?, 'alias command', 'grace_alias', 1, 2, 0, 50)`, t1, t1, t1); err != nil {
		t.Fatal(err)
	}
	related := reporting.ListRelatedSessions{Queries: readTestSessions(t, path)}
	rel := reporting.SessionRelation{Kind: reporting.RelationMCP, Value: "grace", Servers: []string{"grace", "grace_alias"}, Harness: kernel.Claude}
	list, err := related.Run(t.Context(), reporting.Query{Harness: kernel.Claude}, rel)
	if err != nil || ids(list.Sessions) != "claude:main" {
		t.Fatalf("MCP sessions = %q, %v", ids(list.Sessions), err)
	}
	match := list.Sessions[0]
	if match.RelatedTool == nil || match.RelatedTool.Calls != 3 || match.RelatedTool.Errors != 1 ||
		match.RelatedTool.Server != "grace" || match.RelatedTool.Name != "" || match.Cost != 0 {
		t.Errorf("server usage = %+v, model cost = %v", match.RelatedTool, match.Cost)
	}
}
