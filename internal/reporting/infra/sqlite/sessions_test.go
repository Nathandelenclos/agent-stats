package sqlite

import (
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Nathandelenclos/agent-stats/internal/kernel"
	report "github.com/Nathandelenclos/agent-stats/internal/reporting/domain"
)

// sessionModel has the shape of the session_* views this adapter reads, the indexing context's published read model.
// contract_test.go at the module root checks the adapter against the indexing adapter's real views.
const sessionModel = `
CREATE TABLE session_overview (id TEXT NOT NULL, root_id TEXT NOT NULL, harness TEXT NOT NULL, agent_type TEXT NOT NULL,
	title TEXT, folder TEXT, started_at INTEGER, ended_at INTEGER);
CREATE TABLE session_requests (root_id TEXT NOT NULL, session_id TEXT NOT NULL, ts INTEGER NOT NULL, model TEXT NOT NULL,
	input INTEGER NOT NULL, output INTEGER NOT NULL, cache_read INTEGER NOT NULL, cache_write INTEGER NOT NULL,
	cost REAL NOT NULL, priced INTEGER NOT NULL, duration_ms REAL, ttft_ms REAL, error TEXT);
CREATE TABLE session_tool_calls (root_id TEXT NOT NULL, session_id TEXT NOT NULL, ts INTEGER NOT NULL, name TEXT NOT NULL,
	server TEXT NOT NULL, args_chars INTEGER NOT NULL, result_chars INTEGER NOT NULL, is_error INTEGER NOT NULL, duration_ms REAL);
CREATE TABLE session_prompts (session_id TEXT NOT NULL, position INTEGER NOT NULL, ts INTEGER NOT NULL, text TEXT NOT NULL,
	chars INTEGER NOT NULL, words INTEGER NOT NULL, yelling INTEGER NOT NULL, profanity INTEGER NOT NULL, anguish INTEGER NOT NULL,
	negation INTEGER NOT NULL, repetition INTEGER NOT NULL, blame INTEGER NOT NULL);
`

const (
	t0 = int64(1767261600000) // when the older session ended
	t1 = t0 + 3_600_000       // when the newer one did
)

// sessionsIndexed writes three sessions into a fresh read model and returns the database path:
//   - claude:main (ended t1), with a subagent and prompts, on two models, one of them named hostile;
//   - codex:old (ended t0);
//   - claude:gone/orphan, a subagent whose parent was never indexed, with no timestamps at all.
func sessionsIndexed(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "index.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	long := strings.Repeat("é", report.PromptTextLimit+50)
	for _, stmt := range []struct {
		sql  string
		args []any
	}{
		{sessionModel, nil},
		{`INSERT INTO session_overview VALUES
			('claude:main', 'claude:main', 'claude', 'main', ?, ?, ?, ?),
			('claude:main/kid', 'claude:main', 'claude', ?, ?, NULL, ?, ?),
			('codex:old', 'codex:old', 'codex', 'main', NULL, NULL, ?, ?),
			('claude:gone/orphan', 'claude:gone/orphan', 'claude', 'subagent', 'orphan', NULL, 0, 0)`,
			[]any{hostile, hostile, t0, t1, hostile, "kid", t0, t1, t0 - 60_000, t0}},
		{`INSERT INTO session_requests VALUES
			('claude:main', 'claude:main', ?, 'cheap', 100, 10, 0, 0, 1, 1, 1000, NULL, NULL),
			('claude:main', 'claude:main', ?, ?, 100, 10, 0, 0, 3, 1, 2000, NULL, 'boom'),
			('claude:main', 'claude:main/kid', ?, 'cheap', 50, 5, 0, 0, 0.5, 1, NULL, NULL, NULL),
			('codex:old', 'codex:old', ?, 'cheap', 1, 1, 0, 0, 7, 1, NULL, NULL, NULL)`,
			[]any{t0, t1, hostile, t1, t0}},
		{`INSERT INTO session_tool_calls VALUES
			('claude:main', 'claude:main', ?, 'Bash', '', 10, 20, 0, NULL),
			('claude:main', 'claude:main/kid', ?, 'Bash', '', 1, 2, 1, 50),
			('claude:main', 'claude:main', ?, ?, 'grace', 0, 0, 0, NULL)`,
			[]any{t0, t1, t1, hostile}},
		{`INSERT INTO session_prompts VALUES
			('claude:main', 2, ?, 'second', 6, 1, 0, 0, 0, 0, 0, 0),
			('claude:main', 1, ?, ?, 12, 2, 3, 1, 2, 4, 1, 5),
			('claude:main', 3, ?, ?, ?, 1, 0, 0, 0, 0, 0, 0),
			('claude:main/kid', 1, ?, 'brief to the subagent', 21, 4, 0, 0, 0, 0, 0, 0)`,
			[]any{t0, t1, hostile + "\x1b[2J", t1, long, len([]rune(long)), t0}},
	} {
		if _, err := db.Exec(stmt.sql, stmt.args...); err != nil {
			t.Fatalf("%v: %s", err, stmt.sql)
		}
	}
	return path
}

func openSessions(t *testing.T) *SQLiteSessions {
	t.Helper()
	r, err := OpenReadOnly(sessionsIndexed(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	return r.Sessions()
}

func ids(list []report.SessionSummary) string {
	var out []string
	for _, s := range list {
		out = append(out, s.ID.String())
	}
	return strings.Join(out, " ")
}

func TestSessionsListEachRunWithItsSubagentsAndModels(t *testing.T) {
	// Given a session with a subagent, an older session and a subagent whose parent was not indexed
	s := openSessions(t)

	// When every session is listed
	list, err := s.Sessions(report.Filter{})
	if err != nil {
		t.Fatal(err)
	}

	// Then the most recently active comes first, and a subagent is listed only when its parent is not indexed
	if got := ids(list); got != "claude:main codex:old claude:gone/orphan" {
		t.Fatalf("sessions = %q", got)
	}
	main := list[0]
	if main.Title != hostile || main.Folder != hostile || main.Harness != kernel.Claude || main.AgentType != "main" {
		t.Errorf("identity = %+v", main)
	}
	if !main.Start.Equal(time.UnixMilli(t0)) || !main.End.Equal(time.UnixMilli(t1)) {
		t.Errorf("span = %v → %v", main.Start, main.End)
	}
	if d, ok := main.Duration(); !ok || d != time.Hour {
		t.Errorf("duration = %v %v", d, ok)
	}

	// And its usage adds up the session and its subagent
	if main.Requests != 3 || main.Failed != 1 || main.Cost != 4.5 || main.SubagentCount != 1 {
		t.Errorf("usage = %+v, subagents %d", main.Usage, main.SubagentCount)
	}
	// And the models come with their share, most expensive first, a hostile name staying a name
	if len(main.Models) != 2 || main.Models[0].Group != hostile || main.Models[0].Cost != 3 || main.Models[1].Group != "cheap" || main.Models[1].Requests != 2 {
		t.Errorf("models = %+v", main.Models)
	}
	// And the prompts are the session's own: the brief sent to the subagent is not counted
	if main.PromptCount != 3 || main.PromptChars != 6+12+int64(report.PromptTextLimit+50) {
		t.Errorf("prompts = %d, %d chars", main.PromptCount, main.PromptChars)
	}

	// And a session that logged no time or request is still listed, with nothing to show for it
	orphan := list[2]
	if orphan.AgentType != "subagent" || !orphan.Start.IsZero() || orphan.Requests != 0 || len(orphan.Models) != 0 {
		t.Errorf("orphan = %+v", orphan)
	}
	if _, ok := orphan.Duration(); ok {
		t.Error("an unknown time span has a duration")
	}
}

func TestSessionsFollowThePeriodAndHarnessFilters(t *testing.T) {
	s := openSessions(t)
	for _, c := range []struct {
		name string
		f    report.Filter
		want string
	}{
		{"since the older session's end", report.Filter{Since: time.UnixMilli(t0)}, "claude:main codex:old"},
		{"since after the older session", report.Filter{Since: time.UnixMilli(t0 + 1)}, "claude:main"},
		{"since after everything", report.Filter{Since: time.UnixMilli(t1 + 1)}, ""},
		{"one harness", report.Filter{Harness: kernel.Codex}, "codex:old"},
		{"harness and period", report.Filter{Harness: kernel.Claude, Since: time.UnixMilli(t0)}, "claude:main"},
	} {
		// When the sessions are listed with the filter
		list, err := s.Sessions(c.f)
		// Then only the matching ones come back, each with the usage of its whole run
		if err != nil || ids(list) != c.want {
			t.Errorf("%s: %q, %v; want %q", c.name, ids(list), err, c.want)
		}
	}
	list, _ := s.Sessions(report.Filter{Harness: kernel.Claude, Since: time.UnixMilli(t0)})
	if len(list) != 1 || list[0].Requests != 3 || list[0].Cost != 4.5 {
		t.Errorf("filtered usage = %+v", list)
	}
}

func TestSessionDetailTellsWhatTheSessionDid(t *testing.T) {
	s := openSessions(t)
	id, _ := report.NewSessionID("claude:main")

	// When the session is described
	d, err := s.Session(id)
	if err != nil {
		t.Fatal(err)
	}

	// Then it carries the summary of the listing
	if d.Title != hostile || d.Cost != 4.5 || len(d.Models) != 2 {
		t.Errorf("summary = %+v", d.SessionSummary)
	}
	// And the tools of the whole run, most used first
	if len(d.Tools) != 2 || d.Tools[0].Name != "Bash" || d.Tools[0].Calls != 2 || d.Tools[0].Errors != 1 || d.Tools[1].Name != hostile || d.Tools[1].Server != "grace" {
		t.Errorf("tools = %+v", d.Tools)
	}
	// And the subagents with what each used, their type kept as data
	if len(d.Subagents) != 1 || d.Subagents[0].AgentType != hostile || d.Subagents[0].Cost != 0.5 || d.Subagents[0].Title != "kid" {
		t.Errorf("subagents = %+v", d.Subagents)
	}
	// And the prompts in log order, whatever their time (the first logged carries the later time), with their signals
	if len(d.Prompts) != 3 {
		t.Fatalf("prompts = %+v", d.Prompts)
	}
	first, second, third := d.Prompts[0], d.Prompts[1], d.Prompts[2]
	if first.Text != hostile+"\x1b[2J" || second.Text != "second" {
		t.Errorf("order = %q, %q", first.Text, second.Text)
	}
	if want := (report.PromptSignals{Yelling: 3, Profanity: 1, Anguish: 2, Negation: 4, Blame: 5, Repeated: true}); first.Signals != want || first.Chars != 12 || first.Words != 2 {
		t.Errorf("signals = %+v", first)
	}
	if !first.Signals.Any() || second.Signals.Any() {
		t.Errorf("Any = %v, %v", first.Signals.Any(), second.Signals.Any())
	}
	// And a very long prompt is cut to the limit, in characters, while its real length is kept
	if got := len([]rune(third.Text)); got != report.PromptTextLimit || third.Chars != report.PromptTextLimit+50 {
		t.Errorf("long prompt: %d characters kept of %d", got, third.Chars)
	}
}

func TestSessionDetailRefusesWhatTheListingDoesNotShow(t *testing.T) {
	s := openSessions(t)
	for _, raw := range []string{
		"claude:main/kid", // a subagent belongs to its session
		"claude:nope",
		`x' OR '1'='1`,
		`claude:main'; DROP TABLE session_overview; --`,
	} {
		// When an identity that is no listed session is asked for
		id, err := report.NewSessionID(raw)
		if err != nil {
			t.Fatal(err)
		}
		_, err = s.Session(id)

		// Then it is not found, and the value was only ever data
		if !errors.Is(err, report.ErrSessionNotFound) {
			t.Errorf("%q: err = %v", raw, err)
		}
	}
	if list, err := s.Sessions(report.Filter{}); err != nil || len(list) != 3 {
		t.Errorf("after hostile identities: %d sessions, %v", len(list), err)
	}
}

func TestSessionIDMustNameASession(t *testing.T) {
	if _, err := report.NewSessionID(""); !errors.Is(err, report.ErrEmptySessionID) {
		t.Errorf("empty identity accepted: %v", err)
	}
}

func TestSessionsAnEmptyIndexListsNothing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "index.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(sessionModel); err != nil {
		t.Fatal(err)
	}
	db.Close()
	r, err := OpenReadOnly(path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if list, err := r.Sessions().Sessions(report.Filter{}); err != nil || len(list) != 0 {
		t.Errorf("sessions = %+v, %v", list, err)
	}
}

func TestSessionsReaderCannotWrite(t *testing.T) {
	s := openSessions(t)
	if _, err := s.db.Exec(`DELETE FROM session_requests`); err == nil {
		t.Error("read-only index accepted a write")
	}
}
