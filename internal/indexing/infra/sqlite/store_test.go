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

// save saves one log on its own.
func save(s *SQLiteIndex, f application.LogFile, t application.IndexedTranscript) error {
	return s.Save([]application.IndexedLog{{File: f, Transcript: t}})[0]
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
	f := application.LogFile{Path: "/logs/" + hostile + ".jsonl", Harness: kernel.Claude, Revision: "10:1767261600123456789"}
	if err := save(s, f, transcript(t, "s1", at)); err != nil {
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
	if err != nil || len(known) != 1 || known[f.Path].Revision != f.Revision {
		t.Errorf("indexed files = %+v %v", known, err)
	}
}

func TestTwoLogsOfOneSessionGiveTheSameSessionWhateverTheOrderTheyAreIndexedIn(t *testing.T) {
	at := time.UnixMilli(1767261600000)
	// Given two logs that name the same session: one that stops early and one that goes on, or stops at the same time
	log := func(path, title string, end time.Duration) application.IndexedLog {
		tr := transcript(t, "s1", at)
		tr.Session.Title, tr.Session.End = title, at.Add(end)
		return application.IndexedLog{File: application.LogFile{Path: path, Harness: kernel.Claude, Revision: path}, Transcript: tr}
	}
	sessionOf := func(s *SQLiteIndex) string {
		var file, title string
		var start, end int64
		if err := s.db.QueryRow(`SELECT file, title, started_at, ended_at FROM sessions`).Scan(&file, &title, &start, &end); err != nil {
			t.Fatal(err)
		}
		return fmt.Sprint(file, title, start, end)
	}
	for name, pair := range map[string][2]application.IndexedLog{
		"one goes on after the other stops":  {log("/logs/b.jsonl", "Early", time.Minute), log("/logs/a.jsonl", "Later", time.Hour)},
		"both stop at the same time":         {log("/logs/a.jsonl", "First", time.Minute), log("/logs/b.jsonl", "Second", time.Minute)},
		"the one that goes on is named last": {log("/logs/a.jsonl", "Later", time.Hour), log("/logs/b.jsonl", "Early", time.Minute)},
	} {
		t.Run(name, func(t *testing.T) {
			// When they are indexed together, one after the other, and the other way round
			together := open(t)
			if errs := together.Save(pair[:]); errs[0] != nil || errs[1] != nil {
				t.Fatal(errs)
			}
			inOrder, reversed := open(t), open(t)
			for _, l := range pair {
				if err := save(inOrder, l.File, l.Transcript); err != nil {
					t.Fatal(err)
				}
			}
			for _, l := range []application.IndexedLog{pair[1], pair[0]} {
				if err := save(reversed, l.File, l.Transcript); err != nil {
					t.Fatal(err)
				}
			}

			// Then the session is the same in the three indexes
			if a, b, c := sessionOf(together), sessionOf(inOrder), sessionOf(reversed); a != b || b != c {
				t.Errorf("together %s, in order %s, reversed %s", a, b, c)
			}
		})
	}
}

func TestMovedLogIsNotCountedTwice(t *testing.T) {
	s := open(t)
	at := time.UnixMilli(1767261600000)
	for _, path := range []string{"/logs/sessions/s1.jsonl", "/logs/archived/s1.jsonl"} {
		if err := save(s, application.LogFile{Path: path, Harness: kernel.Claude}, transcript(t, "s1", at)); err != nil {
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
	if err := save(s, application.LogFile{Path: "/l.jsonl", Harness: kernel.Claude}, transcript(t, "s1", time.UnixMilli(1))); err != nil {
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
		if err := save(s, application.LogFile{Path: f.path, Harness: kernel.Claude}, f.tr); err != nil {
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
	if err := save(s, application.LogFile{Path: "/logs/orphan.jsonl", Harness: kernel.Claude}, subagentOf(t, "gone", "orphan", time.UnixMilli(1767261600000))); err != nil {
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
		if err := save(s, application.LogFile{Path: path, Harness: kernel.Claude}, tr); err != nil {
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
	if err := save(s, application.LogFile{Path: "/logs/s1.jsonl", Harness: kernel.Claude}, tr); err != nil {
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

func TestASessionListingFromANewerBuildIsLeftAsItIs(t *testing.T) {
	// Given an index whose listing of sessions a newer build changed
	s := open(t)
	if _, err := s.db.Exec(`PRAGMA user_version = 99; DROP VIEW session_requests; DROP VIEW session_tool_calls; DROP VIEW session_overview;
		CREATE VIEW session_overview AS SELECT 'listed by a newer build' AS root_id`); err != nil {
		t.Fatal(err)
	}
	path := pathOf(t, s)
	s.Close()

	// When this build opens it
	again := reopen(t, path)

	// Then the sessions are still listed the way the newer build lists them
	var root string
	if err := again.db.QueryRow(`SELECT root_id FROM session_overview`).Scan(&root); err != nil || root != "listed by a newer build" {
		t.Errorf("listing = %q, %v", root, err)
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

func TestSaveAppliesABatchInOrderAndUndoesOnlyTheLogThatFails(t *testing.T) {
	s := open(t)
	at := time.UnixMilli(1767261600000)
	// Given a database that refuses the session "claude:bad"
	if _, err := s.db.Exec(`CREATE TRIGGER refuse BEFORE INSERT ON sessions WHEN NEW.id = 'claude:bad' BEGIN SELECT RAISE(ABORT, 'refused'); END`); err != nil {
		t.Fatal(err)
	}
	log := func(native string) application.IndexedLog {
		return application.IndexedLog{File: application.LogFile{Path: "/logs/" + native + ".jsonl", Harness: kernel.Claude, Revision: "1"}, Transcript: transcript(t, native, at)}
	}

	// When one batch holds a good log, the bad one, and a good one again
	errs := s.Save([]application.IndexedLog{log("a"), log("bad"), log("c")})

	// Then only the bad one fails and leaves nothing behind, the others are saved
	if errs[0] != nil || errs[1] == nil || errs[2] != nil {
		t.Fatalf("errors = %v", errs)
	}
	var sessions, requests, files int
	if err := s.db.QueryRow(`SELECT (SELECT COUNT(*) FROM sessions), (SELECT COUNT(*) FROM requests), (SELECT COUNT(*) FROM files)`).Scan(&sessions, &requests, &files); err != nil ||
		sessions != 2 || requests != 2 || files != 2 {
		t.Errorf("sessions %d, requests %d, files %d, %v; want 2 each", sessions, requests, files, err)
	}
}

func TestSaveOfNothingIsHarmless(t *testing.T) {
	if errs := open(t).Save(nil); len(errs) != 0 {
		t.Errorf("errors = %v", errs)
	}
}

func pageSize(t *testing.T, s *SQLiteIndex) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow(`PRAGMA page_size`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestNewIndexUsesLargePagesAndAnOlderOneKeepsItsOwn(t *testing.T) {
	// Given an index made by an older version, with 4 KiB pages
	path := filepath.Join(t.TempDir(), "old.db")
	old, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := old.Exec(`PRAGMA page_size = 4096; PRAGMA journal_mode = WAL; CREATE TABLE files (path TEXT PRIMARY KEY, harness TEXT NOT NULL, size INTEGER NOT NULL, mtime INTEGER NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	old.Close()

	// When both it and a new index are opened and written to
	older, err := Open(Options{Path: path, User: "me@host"})
	if err != nil {
		t.Fatal(err)
	}
	defer older.Close()
	fresh := open(t)
	for _, s := range []*SQLiteIndex{older, fresh} {
		if err := save(s, application.LogFile{Path: "/l.jsonl", Harness: kernel.Claude}, transcript(t, "s1", time.UnixMilli(1))); err != nil {
			t.Fatal(err)
		}
	}

	// Then the new one has 16 KiB pages and the old one is untouched
	if got := pageSize(t, fresh); got != 16384 {
		t.Errorf("new index page size = %d, want 16384", got)
	}
	if got := pageSize(t, older); got != 4096 {
		t.Errorf("older index page size = %d, want 4096", got)
	}
}

func TestTheFolderASessionWorkedInIsPublishedWithItsToolCallsAndInTheListOfFolders(t *testing.T) {
	// Given two sessions that worked in two folders, one of them with no folder at all
	s := open(t)
	at := time.UnixMilli(1767261600000)
	in := func(native, folder string) application.IndexedTranscript {
		tr := transcript(t, native, at)
		tr.Session.Folder = folder
		return tr
	}
	for native, folder := range map[string]string{"a": "/work/app", "b": "/work/lib", "c": "", "d": "/work/app"} {
		if err := save(s, application.LogFile{Path: "/logs/" + native, Harness: kernel.Claude}, in(native, folder)); err != nil {
			t.Fatal(err)
		}
	}

	// When the published views are read
	folders := func(query string) (out []string) {
		rows, err := s.db.Query(query)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		for rows.Next() {
			var f sql.NullString
			if err := rows.Scan(&f); err != nil {
				t.Fatal(err)
			}
			out = append(out, f.String)
		}
		return out
	}

	// Then each tool call names the folder of its session, and every folder is listed once, the empty one left out
	if got := fmt.Sprint(folders(`SELECT folder FROM usage_tool_calls ORDER BY folder`)); got != "[ /work/app /work/app /work/lib]" {
		t.Errorf("folders of the tool calls = %s", got)
	}
	if got := fmt.Sprint(folders(`SELECT folder FROM usage_folders ORDER BY folder`)); got != "[/work/app /work/lib]" {
		t.Errorf("listed folders = %s", got)
	}
}

func TestRequestDetailsViewMigrationPreservesIndexedCalls(t *testing.T) {
	s := open(t)
	row := transcript(t, "s1", time.UnixMilli(1767261600000))
	row.Transcript.Requests[0].StopReason = "stop"
	f := application.LogFile{Path: "/logs/s1.jsonl", Harness: kernel.Claude, Revision: "10:1767261600123456789"}
	if err := save(s, f, row); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(schemaBeforeRequestDetails); err != nil {
		t.Fatal(err)
	}
	path := pathOf(t, s)
	s.Close()

	upgraded := reopen(t, path)
	var provider, stop, key string
	var cost float64
	if err := upgraded.db.QueryRow(`SELECT provider, stop_reason, key, cost FROM session_requests`).Scan(&provider, &stop, &key, &cost); err != nil {
		t.Fatal(err)
	}
	if provider != "anthropic" || stop != "stop" || key != "m1" || cost != 2 {
		t.Fatalf("indexed call after upgrade: %q %q %q %v", provider, stop, key, cost)
	}
	if err := upgraded.db.QueryRow(`SELECT provider FROM usage_requests`).Scan(&provider); err != nil || provider != "anthropic" {
		t.Fatalf("usage provider after upgrade: %q %v", provider, err)
	}
}

const schemaBeforeRequestDetails = `
PRAGMA user_version = 4;
DROP TABLE request_metadata;
DROP TABLE quota_snapshots;
DROP VIEW session_requests;
CREATE VIEW session_requests AS SELECT session_id, cost FROM requests;
DROP VIEW usage_requests;
CREATE VIEW usage_requests AS SELECT ts, model FROM requests;
`

func TestRequestMetadataFollowsTheIndexedRequest(t *testing.T) {
	s := open(t)
	log := application.LogFile{Path: "/logs/s.jsonl", Harness: kernel.Claude}
	tr := transcript(t, "s", time.UnixMilli(1767261600000))
	req := &tr.Transcript.Requests[0]
	req.Purpose, req.ServiceTier, req.PremiumUnits = "judge", "priority", 0.5
	if err := save(s, log, tr); err != nil {
		t.Fatal(err)
	}
	var purpose, tier string
	var units float64
	if err := s.db.QueryRow(`SELECT purpose, service_tier, premium_units FROM session_requests`).Scan(&purpose, &tier, &units); err != nil ||
		purpose != "judge" || tier != "priority" || units != 0.5 {
		t.Fatalf("metadata = %q %q %v, %v", purpose, tier, units, err)
	}
	req.Purpose, req.ServiceTier, req.PremiumUnits = "", "", 0
	if err := save(s, log, tr); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(`SELECT premium_units FROM usage_requests`).Scan(&units); err != nil || units != 0 {
		t.Fatalf("replaced metadata = %v, %v", units, err)
	}
}
