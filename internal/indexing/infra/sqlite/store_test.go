package sqlite

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Nathandelenclos/agent-stats/internal/indexing/application"
	"github.com/Nathandelenclos/agent-stats/internal/indexing/domain"
	"github.com/Nathandelenclos/agent-stats/internal/kernel"
)

func open(t *testing.T) *SQLiteIndex {
	t.Helper()
	s, err := Open(Options{Path: filepath.Join(t.TempDir(), "index.db"), User: "me@host"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// pathOf is the file the index lives in.
func pathOf(t *testing.T, s *SQLiteIndex) string {
	t.Helper()
	var path string
	if err := s.db.QueryRow(`SELECT file FROM pragma_database_list WHERE name = 'main'`).Scan(&path); err != nil {
		t.Fatal(err)
	}
	return path
}

// hostile carries SQL metacharacters in every text value that reaches the database.
const hostile = `o'brian"; DROP TABLE requests; --`

func transcript(t *testing.T, native string, at time.Time) application.IndexedTranscript {
	t.Helper()
	sessionID, err := domain.NewSessionID(kernel.Claude, native)
	if err != nil {
		t.Fatal(err)
	}
	tokens, _ := kernel.NewTokens(kernel.TokenCounts{Input: 1000, Output: 100})
	cost, _ := kernel.NewCost(2)
	return application.IndexedTranscript{
		Transcript: domain.Transcript{
			Session: domain.Session{ID: sessionID, Harness: kernel.Claude, AgentType: domain.MainAgent, Folder: hostile, Start: at, End: at},
			Requests: []domain.Request{{Key: "m1", At: at, Model: hostile, Provider: "anthropic",
				Tokens: tokens, Duration: domain.NewDuration(1500 * time.Millisecond)}},
			Tools:   []domain.ToolCall{{Key: "t1", At: at, Name: hostile, Server: "grace", IsError: true}},
			Prompts: []domain.Prompt{{Key: "p1", At: at, Text: hostile}},
		},
		Costs:   []application.PricedCost{{Cost: cost, Priced: true}},
		Signals: []domain.Signals{{Chars: 3, Repeated: true}},
	}
}

func TestSaveKeepsValuesAsData(t *testing.T) {
	s := open(t)
	at := time.UnixMilli(1767261600000)
	f := application.LogFile{Path: "/logs/" + hostile + ".jsonl", Harness: kernel.Claude, Size: 10, ModTime: time.Unix(0, 1767261600123456789)}
	if err := s.Save(f, transcript(t, "s1", at)); err != nil {
		t.Fatal(err)
	}
	var model, file string
	var input int64
	var cost, duration float64
	if err := s.db.QueryRow(`SELECT model, file, input, cost, duration_ms FROM requests`).Scan(&model, &file, &input, &cost, &duration); err != nil ||
		model != hostile || file != f.Path || input != 1000 || cost != 2 || duration != 1500 {
		t.Errorf("request row = %q %q %d %v %v, %v", model, file, input, cost, duration, err)
	}
	var tool, folder, text string
	var repetition int
	if err := s.db.QueryRow(`SELECT t.name, s.folder, p.text, p.repetition FROM tool_calls t, sessions s, prompts p`).Scan(&tool, &folder, &text, &repetition); err != nil ||
		tool != hostile || folder != hostile || text != hostile || repetition != 1 {
		t.Errorf("rows = %q %q %q %d, %v", tool, folder, text, repetition, err)
	}
	known, err := s.IndexedFiles()
	if err != nil || len(known) != 1 || !known[f.Path].ModTime.Equal(f.ModTime) || known[f.Path].Size != 10 {
		t.Errorf("indexed files = %+v %v", known, err)
	}
}

func TestMovedLogIsNotCountedTwice(t *testing.T) {
	s := open(t)
	at := time.UnixMilli(1767261600000)
	for _, path := range []string{"/logs/sessions/s1.jsonl", "/logs/archived/s1.jsonl"} {
		if err := s.Save(application.LogFile{Path: path, Harness: kernel.Claude}, transcript(t, "s1", at)); err != nil {
			t.Fatal(err)
		}
	}
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM requests`).Scan(&n); err != nil || n != 1 {
		t.Errorf("after move: %d requests, %v", n, err)
	}
}

func TestIndexIsPrivate(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "cache", "agent-stats")
	path := filepath.Join(dir, "index.db")
	s, err := Open(Options{Path: path, User: "me@host"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.Save(application.LogFile{Path: "/l.jsonl", Harness: kernel.Claude}, transcript(t, "s1", time.UnixMilli(1))); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{dir, path, path + "-wal", path + "-shm"} {
		info, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm()&0o077 != 0 {
			t.Errorf("%s is %v, want no group or other access", p, info.Mode().Perm())
		}
	}

	// An index created by an older version is restricted when opened again.
	s.Close()
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	s2, err := Open(Options{Path: path, User: "me@host"})
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
		t.Errorf("reopened index is %v", info.Mode().Perm())
	}
}

// subagentOf returns the transcript of a subagent of parent (an indexed session or not).
func subagentOf(t *testing.T, parent, name string, at time.Time) application.IndexedTranscript {
	t.Helper()
	p, err := domain.NewSessionID(kernel.Claude, parent)
	if err != nil {
		t.Fatal(err)
	}
	id, err := p.Subagent(name)
	if err != nil {
		t.Fatal(err)
	}
	tr := transcript(t, "unused", at)
	tr.Session.ID, tr.Session.ParentID, tr.Session.AgentType = id, p, domain.Subagent
	return tr
}

// listedUnder reads how the index groups sessions for a listing: each session, with the session it is listed under.
func listedUnder(t *testing.T, s *SQLiteIndex) map[string]string {
	t.Helper()
	listed := map[string]string{}
	rows, err := s.db.Query(`SELECT id, root_id FROM session_overview`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, root string
		if err := rows.Scan(&id, &root); err != nil {
			t.Fatal(err)
		}
		listed[id] = root
	}
	return listed
}

// requestsListedUnder counts the requests a listing adds up for a session: its own and its subagents'.
func requestsListedUnder(t *testing.T, s *SQLiteIndex, session string) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM session_requests WHERE root_id = ?`, session).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestASubagentIsListedUnderItsSession(t *testing.T) {
	// Given a session with a subagent
	s := open(t)
	at := time.UnixMilli(1767261600000)
	for _, f := range []struct {
		path string
		tr   application.IndexedTranscript
	}{
		{"/logs/main.jsonl", transcript(t, "main", at)},
		{"/logs/child.jsonl", subagentOf(t, "main", "child", at)},
	} {
		if err := s.Save(application.LogFile{Path: f.path, Harness: kernel.Claude}, f.tr); err != nil {
			t.Fatal(err)
		}
	}

	// When the sessions are listed
	listed := listedUnder(t, s)

	// Then the subagent belongs to its session, and its requests count for it
	if listed["claude:main/child"] != "claude:main" || listed["claude:main"] != "claude:main" {
		t.Errorf("listed under = %v", listed)
	}
	if n := requestsListedUnder(t, s, "claude:main"); n != 2 {
		t.Errorf("requests of the session = %d, want its own and its subagent's", n)
	}
}

func TestASubagentWhoseSessionWasNeverIndexedIsListedOnItsOwn(t *testing.T) {
	// Given a subagent whose session log was never indexed
	s := open(t)
	if err := s.Save(application.LogFile{Path: "/logs/orphan.jsonl", Harness: kernel.Claude}, subagentOf(t, "gone", "orphan", time.UnixMilli(1767261600000))); err != nil {
		t.Fatal(err)
	}

	// When the sessions are listed, then it stands for itself
	if listed := listedUnder(t, s); listed["claude:gone/orphan"] != "claude:gone/orphan" {
		t.Errorf("listed under = %v", listed)
	}
}

func saveAll(t *testing.T, s *SQLiteIndex, logs map[string]application.IndexedTranscript) {
	t.Helper()
	for path, tr := range logs {
		if err := s.Save(application.LogFile{Path: path, Harness: kernel.Claude}, tr); err != nil {
			t.Fatal(err)
		}
	}
}

func TestNestedSubagentsAreListedUnderTheTopSession(t *testing.T) {
	// Given a session whose subagent started a subagent of its own
	s := open(t)
	at := time.UnixMilli(1767261600000)
	saveAll(t, s, map[string]application.IndexedTranscript{
		"/logs/a.jsonl": transcript(t, "a", at),
		"/logs/b.jsonl": subagentOf(t, "a", "b", at),
		"/logs/c.jsonl": subagentOf(t, "a/b", "c", at),
	})

	// When the sessions are listed
	listed := listedUnder(t, s)

	// Then both subagents belong to the top session, and the requests of the three count for it
	if listed["claude:a"] != "claude:a" || listed["claude:a/b"] != "claude:a" || listed["claude:a/b/c"] != "claude:a" {
		t.Errorf("listed under = %v", listed)
	}
	if n := requestsListedUnder(t, s, "claude:a"); n != 3 {
		t.Errorf("requests of the session = %d, want its own and both subagents'", n)
	}
}

func TestAForkedConversationIsListedOnItsOwn(t *testing.T) {
	// Given a main conversation forked from another one (not a subagent of it)
	s := open(t)
	at := time.UnixMilli(1767261600000)
	fork := transcript(t, "fork", at)
	fork.Session.ParentID, _ = domain.NewSessionID(kernel.Claude, "origin")
	saveAll(t, s, map[string]application.IndexedTranscript{"/logs/origin.jsonl": transcript(t, "origin", at), "/logs/fork.jsonl": fork})

	// When the sessions are listed, then each stands for itself
	listed := listedUnder(t, s)
	if listed["claude:fork"] != "claude:fork" || listed["claude:origin"] != "claude:origin" {
		t.Errorf("listed under = %v", listed)
	}
}

func TestARunLastsUntilItsLastSubagentActivity(t *testing.T) {
	// Given a session that ended at 10 minutes and a subagent that ended at 20
	s := open(t)
	at := time.UnixMilli(1767261600000)
	parent := transcript(t, "a", at)
	parent.Session.End = at.Add(10 * time.Minute)
	child := subagentOf(t, "a", "b", at.Add(time.Minute))
	child.Session.End = at.Add(20 * time.Minute)
	saveAll(t, s, map[string]application.IndexedTranscript{"/logs/a.jsonl": parent, "/logs/b.jsonl": child})

	// When the run of the session is read
	var start, end int64
	if err := s.db.QueryRow(`SELECT run_started_at, run_ended_at FROM session_overview WHERE id = 'claude:a'`).Scan(&start, &end); err != nil {
		t.Fatal(err)
	}

	// Then it spans from the session's start to the subagent's last activity
	if start != at.UnixMilli() || end != at.Add(20*time.Minute).UnixMilli() {
		t.Errorf("run = %d → %d", start, end)
	}
}

func TestPromptsKeepTheOrderTheyWereTypedInAndTheirSignals(t *testing.T) {
	// Given a session whose prompts were typed in a different order than their keys sort
	s := open(t)
	at := time.UnixMilli(1767261600000)
	tr := transcript(t, "s1", at)
	tr.Prompts = []domain.Prompt{{Key: "z", At: at, Text: "first"}, {Key: "a", At: at, Text: hostile}}
	tr.Signals = []domain.Signals{{Chars: 5, Words: 1}, {Chars: 9, Words: 2, Yelling: 3, Profanity: 1, Anguish: 2, Negation: 4, Blame: 5, Repeated: true}}
	if err := s.Save(application.LogFile{Path: "/logs/s1.jsonl", Harness: kernel.Claude}, tr); err != nil {
		t.Fatal(err)
	}

	// When the prompts of the session are read
	rows, err := s.db.Query(`SELECT text, chars, yelling, profanity, anguish, negation, repetition, blame
		FROM session_prompts WHERE session_id = 'claude:s1' ORDER BY position`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var texts []string
	var last [7]int
	for rows.Next() {
		var text string
		if err := rows.Scan(&text, &last[0], &last[1], &last[2], &last[3], &last[4], &last[5], &last[6]); err != nil {
			t.Fatal(err)
		}
		texts = append(texts, text)
	}

	// Then they come back in the order they were typed in, each with its signals
	if len(texts) != 2 || texts[0] != "first" || texts[1] != hostile {
		t.Errorf("prompts = %q", texts)
	}
	if last != [7]int{9, 3, 1, 2, 4, 1, 5} {
		t.Errorf("signals of the second prompt = %v", last)
	}
}

func TestAFailedViewReplacementLeavesTheViewsAReaderUses(t *testing.T) {
	// Given an index another process (here, a second connection) is reading the sessions from
	s := open(t)
	reader, err := sql.Open("sqlite", pathOf(t, s))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { reader.Close() })
	if _, err := s.db.Exec(`PRAGMA user_version = 0`); err != nil { // an index from before the views were versioned
		t.Fatal(err)
	}

	// When replacing the views fails half way
	if err := migrate(s.db, schemaVersion, sessionViews, `NOT SQL`); err == nil {
		t.Fatal("the failing step was not reported")
	}

	// Then the reader still sees the previous views, and the index still asks to be migrated
	var n int
	if err := reader.QueryRow(`SELECT COUNT(*) FROM session_overview`).Scan(&n); err != nil {
		t.Errorf("the reader lost the views: %v", err)
	}
	var version int
	if err := reader.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil || version != 0 {
		t.Errorf("version = %d, %v", version, err)
	}
}

func TestAnIndexFromANewerVersionIsLeftAlone(t *testing.T) {
	// Given an index a newer build migrated
	s := open(t)
	if _, err := s.db.Exec(`PRAGMA user_version = 99; DROP VIEW session_overview; DROP VIEW session_requests; DROP VIEW session_tool_calls`); err != nil {
		t.Fatal(err)
	}
	path := pathOf(t, s)
	s.Close()

	// When this build opens it
	again, err := Open(Options{Path: path, User: "me@host"})
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()

	// Then it does not migrate it back
	var version int
	if err := again.db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil || version != 99 {
		t.Errorf("version = %d, %v", version, err)
	}
}

func TestCodexLogsIndexedBeforeTheEndTimeFixAreReadAgainOnce(t *testing.T) {
	// Given an index built before the Codex end times were fixed, holding a Codex log and a Claude log
	s := open(t)
	at := time.UnixMilli(1767261600000)
	codex := application.LogFile{Path: "/logs/codex.jsonl", Harness: kernel.Codex, Size: 5, ModTime: time.Unix(0, 1)}
	claude := application.LogFile{Path: "/logs/claude.jsonl", Harness: kernel.Claude, Size: 7, ModTime: time.Unix(0, 2)}
	for _, f := range []application.LogFile{codex, claude} {
		if err := s.Save(f, transcript(t, filepath.Base(f.Path), at)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.db.Exec(`PRAGMA user_version = 0`); err != nil {
		t.Fatal(err)
	}
	path := pathOf(t, s)
	s.Close()

	// When the index is opened by a build with the fix
	s = reopen(t, path)

	// Then the Codex log is no longer known, so the next sync reads it again, and the Claude log is kept
	known, err := s.IndexedFiles()
	if err != nil || len(known) != 1 || known[claude.Path].Size != 7 {
		t.Fatalf("indexed files = %+v, %v", known, err)
	}
	var sessions int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM sessions`).Scan(&sessions); err != nil || sessions != 2 {
		t.Errorf("sessions = %d, %v: what was indexed stays until the log is read again", sessions, err)
	}

	// And once it is read again, opening the index does not forget it a second time
	if err := s.Save(codex, transcript(t, "codex.jsonl", at)); err != nil {
		t.Fatal(err)
	}
	s.Close()
	if known, err := reopen(t, path).IndexedFiles(); err != nil || len(known) != 2 {
		t.Errorf("indexed files = %+v, %v", known, err)
	}
}

func reopen(t *testing.T, path string) *SQLiteIndex {
	t.Helper()
	s, err := Open(Options{Path: path, User: "me@host"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// subagentNamed returns the transcript of a subagent with the given session id, whatever its id says about its parent.
func subagentNamed(t *testing.T, id, parent string, at time.Time) application.IndexedTranscript {
	t.Helper()
	tr := transcript(t, id, at)
	tr.Session.ParentID, _ = domain.NewSessionID(kernel.Claude, parent)
	tr.Session.AgentType = domain.Subagent
	return tr
}

// assertEverySessionAndRequestIsListedOnce checks that the sessions views drop and double nothing.
func assertEverySessionAndRequestIsListedOnce(t *testing.T, s *SQLiteIndex, sessions int) map[string]string {
	t.Helper()
	listed := listedUnder(t, s)
	var rows, requests int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM session_overview`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM session_requests`).Scan(&requests); err != nil {
		t.Fatal(err)
	}
	if rows != sessions || len(listed) != sessions || requests != sessions { // every transcript holds one request
		t.Errorf("%d sessions listed as %d rows with %d requests, want %d of each", len(listed), rows, requests, sessions)
	}
	return listed
}

func TestAVeryDeepChainOfSubagentsIsListedUnderItsTopSession(t *testing.T) {
	// Given a session and a chain of 40 subagents, each started by the previous one
	s := open(t)
	at := time.UnixMilli(1767261600000)
	logs := map[string]application.IndexedTranscript{"/logs/top.jsonl": transcript(t, "top", at)}
	parent := "top"
	for i := 1; i <= 40; i++ {
		name := fmt.Sprintf("s%d", i)
		logs["/logs/"+name+".jsonl"] = subagentOf(t, parent, name, at)
		parent += "/" + name
	}
	saveAll(t, s, logs)

	// When the sessions are listed
	listed := assertEverySessionAndRequestIsListedOnce(t, s, 41)

	// Then the whole chain belongs to the top session, however deep
	for id, root := range listed {
		if root != "claude:top" {
			t.Errorf("%s is listed under %s", id, root)
		}
	}
}

func TestSubagentsThatNameEachOtherAsParentAreEachListedOnce(t *testing.T) {
	// Given two subagents that name each other as parent, a third one started by the first, and a subagent that names itself
	s := open(t)
	at := time.UnixMilli(1767261600000)
	saveAll(t, s, map[string]application.IndexedTranscript{
		"/logs/a.jsonl":    subagentNamed(t, "a", "b", at),
		"/logs/b.jsonl":    subagentNamed(t, "b", "a", at),
		"/logs/c.jsonl":    subagentNamed(t, "c", "a", at),
		"/logs/self.jsonl": subagentNamed(t, "self", "self", at),
		"/logs/main.jsonl": transcript(t, "main", at),
	})

	// When the sessions are listed
	first := assertEverySessionAndRequestIsListedOnce(t, s, 5)

	// Then each session is listed under a session of the list, and the same way every time
	for id, root := range first {
		if _, ok := first[root]; !ok {
			t.Errorf("%s is listed under %s, which is not listed", id, root)
		}
	}
	if again := listedUnder(t, s); fmt.Sprint(again) != fmt.Sprint(first) {
		t.Errorf("listed %v, then %v", first, again)
	}
}

func TestASubagentIndexedAsAMainConversationIsStillListedUnderItsSession(t *testing.T) {
	// Given a subagent an earlier build indexed as a main conversation (its meta file said "main")
	s := open(t)
	at := time.UnixMilli(1767261600000)
	child := subagentOf(t, "main", "child", at)
	child.Session.AgentType = domain.MainAgent
	saveAll(t, s, map[string]application.IndexedTranscript{"/logs/main.jsonl": transcript(t, "main", at), "/logs/child.jsonl": child})

	// When the sessions are listed
	listed := listedUnder(t, s)

	// Then it belongs to its session like any subagent, and its requests count for it
	if listed["claude:main/child"] != "claude:main" || requestsListedUnder(t, s, "claude:main") != 2 {
		t.Errorf("listed under = %v", listed)
	}
}
