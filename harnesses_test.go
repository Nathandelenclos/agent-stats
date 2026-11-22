package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	indexing "github.com/Nathandelenclos/agent-stats/internal/indexing/application"
	"github.com/Nathandelenclos/agent-stats/internal/indexing/infra/logfiles"
	"github.com/Nathandelenclos/agent-stats/internal/indexing/infra/sqlite"
	"github.com/Nathandelenclos/agent-stats/internal/kernel"
	pricing "github.com/Nathandelenclos/agent-stats/internal/pricing/domain"
	reporting "github.com/Nathandelenclos/agent-stats/internal/reporting/application"
	reportingdomain "github.com/Nathandelenclos/agent-stats/internal/reporting/domain"
	usagedb "github.com/Nathandelenclos/agent-stats/internal/reporting/infra/sqlite"
)

// moreHome is a home with a session of each of Gemini CLI, OpenCode (events generation of the schema) and Kilo Code
// (messages generation): testdata/home-more, and the databases built from testdata/databases.
func moreHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	if err := os.CopyFS(home, os.DirFS("testdata/home-more")); err != nil {
		t.Fatal(err)
	}
	for path, script := range map[string]string{
		".local/share/opencode/opencode.db": "events.sql",
		".local/share/kilo/kilo.db":         "messages.sql",
	} {
		raw, err := os.ReadFile(filepath.Join("testdata", "databases", script))
		if err != nil {
			t.Fatal(err)
		}
		full := filepath.Join(home, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
			t.Fatal(err)
		}
		db, err := sql.Open("sqlite", full)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(string(raw)); err != nil {
			t.Fatal(err)
		}
		db.Close()
	}
	return home
}

func moreIndex(t *testing.T, home, dbPath string) (indexing.IndexReport, *sqlite.SQLiteIndex) {
	t.Helper()
	prices := pricing.PriceList{
		"claude-test": mustRates(t, pricing.RatesPerToken{Input: 3e-6, Output: 15e-6, CacheRead: 0.3e-6, CacheWrite: 3.75e-6}),
		"gpt-test":    mustRates(t, pricing.RatesPerToken{Input: 1.25e-6, Output: 10e-6, CacheRead: 0.125e-6}),
		"gemini-test": mustRates(t, pricing.RatesPerToken{Input: 1e-6, Output: 4e-6, CacheRead: 0.1e-6}),
	}
	index, err := sqlite.Open(sqlite.Options{Path: dbPath, User: "me@host"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { index.Close() })
	rep, err := indexing.IndexSessions{Logs: logfiles.NewFileSessionLogs(logfiles.DefaultSources(home)), Store: index, Prices: prices}.Run(nil)
	if err != nil {
		t.Fatal(err)
	}
	return rep, index
}

// TestNewHarnessesReportApproved locks the pipeline on the fixtures of Gemini CLI, OpenCode and Kilo Code, as
// TestReportApproved does for the others: on a difference it writes testdata/report-more.received.json.
func TestNewHarnessesReportApproved(t *testing.T) {
	// Given a home with sessions of Gemini CLI, OpenCode and Kilo Code
	home := moreHome(t)
	dbPath := filepath.Join(t.TempDir(), "index.db")

	// When it is indexed
	rep, _ := moreIndex(t, home, dbPath)

	// Then every session was indexed and nothing was refused
	if len(rep.Errors) > 0 || rep.Indexed != 11 {
		t.Fatalf("index: %+v", rep)
	}
	usage, err := usagedb.OpenReadOnly(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer usage.Close()
	got := map[string]jsonReport{}
	for _, h := range []kernel.Harness{{}, kernel.Gemini, kernel.OpenCode, kernel.Kilo} {
		r, err := reporting.BuildReport{Queries: usage}.Run(context.Background(), reporting.Query{Harness: h})
		if err != nil {
			t.Fatal(err)
		}
		name := h.String()
		if h.IsZero() {
			name = "all"
		}
		got[name] = toJSON(r)
	}
	received, err := json.MarshalIndent(got, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	received = append(received, '\n')
	approved, err := os.ReadFile("testdata/report-more.approved.json")
	if err != nil || !bytes.Equal(approved, received) {
		if werr := os.WriteFile("testdata/report-more.received.json", received, 0o644); werr != nil {
			t.Fatal(werr)
		}
		t.Fatalf("report differs from testdata/report-more.approved.json (%v); see testdata/report-more.received.json", err)
	}
	os.Remove("testdata/report-more.received.json")
}

func TestNothingIsReadAgainWhenNothingChangedInTheDatabases(t *testing.T) {
	// Given a home that was indexed once
	home := moreHome(t)
	dbPath := filepath.Join(t.TempDir(), "index.db")
	moreIndex(t, home, dbPath)

	// When nothing changed and it is indexed again
	rep, _ := moreIndex(t, home, dbPath)

	// Then nothing is read again
	if rep.Indexed != 0 || len(rep.Errors) > 0 {
		t.Fatalf("second run: %+v", rep)
	}
}

// kiloTypedWhileRunning indexes a home, then has Kilo Code (running, its database open for writing) save one more prompt
// in the session ses_fork, and indexes again.
func kiloTypedWhileRunning(t *testing.T) (rep indexing.IndexReport, dbPath string) {
	t.Helper()
	home := moreHome(t)
	dbPath = filepath.Join(t.TempDir(), "index.db")
	moreIndex(t, home, dbPath)
	writer, err := sql.Open("sqlite", filepath.Join(home, ".local/share/kilo/kilo.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { writer.Close() })
	writer.SetMaxOpenConns(1)
	for _, q := range []string{
		`PRAGMA journal_mode = WAL`,
		`INSERT INTO message VALUES ('msg_90', 'ses_fork', 1767226500000, 1767226500000, '{"role":"user","time":{"created":1767226500000}}')`,
		`INSERT INTO part VALUES ('prt_90', 'msg_90', 'ses_fork', 1767226500000, 1767226500000, '{"type":"text","text":"One more thing"}')`,
	} {
		if _, err := writer.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	rep, _ = moreIndex(t, home, dbPath)
	return rep, dbPath
}

func TestOnlyTheSessionThatGainedAMessageIsReadAgain(t *testing.T) {
	// Given a home that was indexed once, and one prompt typed since in one session of Kilo Code
	// When the logs are indexed again
	rep, _ := kiloTypedWhileRunning(t)

	// Then that session alone is read again
	if rep.Indexed != 1 || len(rep.Errors) > 0 {
		t.Fatalf("third run: %+v", rep)
	}
}

func TestAPromptTypedWhileKiloCodeRunsIsInTheReportOfItsSession(t *testing.T) {
	// Given a prompt typed since the last indexing in one session of Kilo Code
	_, dbPath := kiloTypedWhileRunning(t)

	// When the report of that session is shown
	usage, err := usagedb.OpenReadOnly(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer usage.Close()
	id, err := reporting.NewSessionID("kilo:ses_fork")
	if err != nil {
		t.Fatal(err)
	}
	detail, err := reporting.ShowSession{Queries: usage.Sessions()}.Run(context.Background(), id, reporting.Query{})

	// Then it lists the new prompt after the first
	if err != nil || len(detail.Prompts) != 2 || detail.Prompts[1].Text != "One more thing" {
		t.Errorf("prompts of the session = %+v, %v", detail.Prompts, err)
	}
}

// sessionShown is the report of one session as the usage report shows it.
func sessionShown(t *testing.T, dbPath, session string) reportingdomain.SessionDetail {
	t.Helper()
	usage, err := usagedb.OpenReadOnly(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer usage.Close()
	id, err := reporting.NewSessionID(session)
	if err != nil {
		t.Fatal(err)
	}
	detail, err := reporting.ShowSession{Queries: usage.Sessions()}.Run(context.Background(), id, reporting.Query{})
	if err != nil {
		t.Fatal(err)
	}
	return detail
}

const geminiSession = "gemini:11111111-aaaa-bbbb-cccc-000000000001"

func TestAConversationCutShortDoesNotReplaceTheSessionIndexedWhole(t *testing.T) {
	// Given a home that was indexed once, with a Gemini CLI conversation of two answers
	home := moreHome(t)
	dbPath := filepath.Join(t.TempDir(), "index.db")
	moreIndex(t, home, dbPath)
	whole := sessionShown(t, dbPath, geminiSession).Requests
	chat := filepath.Join(home, ".gemini/tmp/demo-app/chats/session-2026-02-01T10-00-11111111.json")
	raw, err := os.ReadFile(chat)
	if err != nil {
		t.Fatal(err)
	}

	// When the file is cut short, with only its last closing brace missing, and the logs are indexed again
	if err := os.WriteFile(chat, bytes.TrimRight(raw, "}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	rep, _ := moreIndex(t, home, dbPath)

	// Then the conversation is reported as unreadable and the session still shows what it showed
	if shown := sessionShown(t, dbPath, geminiSession); rep.Indexed != 0 || len(rep.Errors) != 1 || whole == 0 || shown.Requests != whole {
		t.Errorf("report %+v, requests %d then %d", rep, whole, shown.Requests)
	}
}

func TestTwoGeminiFilesOfOneSessionGiveTheSameSessionWhateverTheOrderTheyAppearIn(t *testing.T) {
	// Given a Gemini CLI conversation, and a second file of the same session that stops earlier and words its prompt otherwise
	earlier := `{"sessionId":"11111111-aaaa-bbbb-cccc-000000000001","projectHash":"h","startTime":"2026-02-01T10:00:00.000Z","messages":[
{"id":"m1","timestamp":"2026-02-01T10:00:01.000Z","type":"user","content":"An earlier way to say it"}]}`
	shown := func(dbPath string) string {
		s := sessionShown(t, dbPath, geminiSession)
		prompts := make([]string, len(s.Prompts))
		for i, p := range s.Prompts {
			prompts[i] = p.Text
		}
		return fmt.Sprint(s.Title, " ", s.End.UnixMilli(), " ", prompts)
	}
	extra := ".gemini/tmp/demo-app/chats/session-2026-01-31T09-00-11111111.json"

	// When both files are indexed together, and when the second appears after the first was indexed
	fresh := moreHome(t)
	if err := os.WriteFile(filepath.Join(fresh, extra), []byte(earlier), 0o600); err != nil {
		t.Fatal(err)
	}
	freshDB := filepath.Join(t.TempDir(), "index.db")
	moreIndex(t, fresh, freshDB)
	updated := moreHome(t)
	updatedDB := filepath.Join(t.TempDir(), "index.db")
	moreIndex(t, updated, updatedDB)
	if err := os.WriteFile(filepath.Join(updated, extra), []byte(earlier), 0o600); err != nil {
		t.Fatal(err)
	}
	moreIndex(t, updated, updatedDB)

	// Then the session and its prompts are those of the file that goes on, in both indexes
	if a, b := shown(freshDB), shown(updatedDB); a != b || !strings.HasPrefix(a, "Add a cache to the loader ") {
		t.Errorf("fresh index: %s, updated index: %s", a, b)
	}
}
