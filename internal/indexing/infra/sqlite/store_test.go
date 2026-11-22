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
	f := application.LogFile{Path: "/logs/" + hostile + ".jsonl", Harness: kernel.Claude, Size: 10, ModTime: time.Unix(0, 1767261600123456789)}
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
	if err != nil || len(known) != 1 || !known[f.Path].ModTime.Equal(f.ModTime) || known[f.Path].Size != 10 {
		t.Errorf("indexed files = %+v %v", known, err)
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

func TestAReaderStillListsTheSessionsWhenTheirListingFailsToUpdate(t *testing.T) {
	// Given a session listed for a reader on another connection, by an index that is due an update of its listing
	s := open(t)
	if err := save(s, application.LogFile{Path: "/logs/main.jsonl", Harness: kernel.Claude}, transcript(t, "main", time.UnixMilli(1767261600000))); err != nil {
		t.Fatal(err)
	}
	reader, err := sql.Open("sqlite", pathOf(t, s))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { reader.Close() })
	if _, err := s.db.Exec(`PRAGMA user_version = 0`); err != nil {
		t.Fatal(err)
	}

	// When updating the listing fails half way
	if err := migrate(s.db, []migration{sqlSteps(sessionViews, `NOT SQL`)}); err == nil {
		t.Fatal("the failing step was not reported")
	}

	// Then the reader still lists the session
	var listed int
	if err := reader.QueryRow(`SELECT COUNT(*) FROM session_overview`).Scan(&listed); err != nil || listed != 1 {
		t.Errorf("the reader lists %d sessions, %v", listed, err)
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

// indexBuiltBeforeTheCodexEndTimeFix saves a Codex log and a Claude log, then closes an index that is
// left as one built before the Codex end times were fixed.
func indexBuiltBeforeTheCodexEndTimeFix(t *testing.T) (path string, codex, claude application.LogFile) {
	t.Helper()
	s := open(t)
	at := time.UnixMilli(1767261600000)
	codex = application.LogFile{Path: "/logs/codex.jsonl", Harness: kernel.Codex, Size: 5, ModTime: time.Unix(0, 1)}
	claude = application.LogFile{Path: "/logs/claude.jsonl", Harness: kernel.Claude, Size: 7, ModTime: time.Unix(0, 2)}
	for _, f := range []application.LogFile{codex, claude} {
		if err := save(s, f, transcript(t, filepath.Base(f.Path), at)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.db.Exec(`PRAGMA user_version = 0`); err != nil {
		t.Fatal(err)
	}
	path = pathOf(t, s)
	s.Close()
	return path, codex, claude
}

func TestCodexLogsIndexedBeforeTheEndTimeFixAreReadAgain(t *testing.T) {
	// Given an index built before the Codex end times were fixed, holding a Codex log and a Claude log
	path, _, claude := indexBuiltBeforeTheCodexEndTimeFix(t)

	// When the index is opened by a build with the fix
	s := reopen(t, path)

	// Then the next sync reads the Codex log again and leaves the Claude log alone, and the sessions indexed so far stay listed
	known, err := s.IndexedFiles()
	if err != nil || len(known) != 1 || known[claude.Path].Size != 7 {
		t.Fatalf("indexed files = %+v, %v", known, err)
	}
	if listed := len(listedUnder(t, s)); listed != 2 {
		t.Errorf("%d sessions listed, want the two indexed so far", listed)
	}
}

func TestCodexLogsReadAgainAreNotForgottenASecondTime(t *testing.T) {
	// Given a Codex log read again after the index was opened by a build with the fix
	path, codex, _ := indexBuiltBeforeTheCodexEndTimeFix(t)
	s := reopen(t, path)
	if err := save(s, codex, transcript(t, "codex.jsonl", time.UnixMilli(1767261600000))); err != nil {
		t.Fatal(err)
	}
	s.Close()

	// When the index is opened again
	again := reopen(t, path)

	// Then the Codex log is still known, so it is not read a second time
	if known, err := again.IndexedFiles(); err != nil || len(known) != 2 {
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

func TestSaveAppliesABatchInOrderAndUndoesOnlyTheLogThatFails(t *testing.T) {
	s := open(t)
	at := time.UnixMilli(1767261600000)
	// Given a database that refuses the session "claude:bad"
	if _, err := s.db.Exec(`CREATE TRIGGER refuse BEFORE INSERT ON sessions WHEN NEW.id = 'claude:bad' BEGIN SELECT RAISE(ABORT, 'refused'); END`); err != nil {
		t.Fatal(err)
	}
	log := func(native string) application.IndexedLog {
		return application.IndexedLog{File: application.LogFile{Path: "/logs/" + native + ".jsonl", Harness: kernel.Claude, Size: 1}, Transcript: transcript(t, native, at)}
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

// oldSchema is the index as 80bf4b1 made it, before tool calls recorded the skill they load and before migrations:
// one Claude log with a Skill call, one without, and a Codex log whose function call kept the full name of an MCP tool.
const oldSchema = `
CREATE TABLE files (path TEXT PRIMARY KEY, harness TEXT NOT NULL, size INTEGER NOT NULL, mtime INTEGER NOT NULL);
CREATE TABLE sessions (id TEXT PRIMARY KEY, file TEXT NOT NULL, harness TEXT NOT NULL, parent_id TEXT, agent_type TEXT NOT NULL,
	title TEXT, folder TEXT, user TEXT NOT NULL, started_at INTEGER, ended_at INTEGER);
CREATE TABLE tool_calls (file TEXT NOT NULL, key TEXT NOT NULL, session_id TEXT NOT NULL, ts INTEGER NOT NULL,
	name TEXT NOT NULL, server TEXT NOT NULL, args_chars INTEGER NOT NULL, result_chars INTEGER NOT NULL,
	is_error INTEGER NOT NULL, duration_ms REAL, PRIMARY KEY (session_id, key));
CREATE VIEW usage_tool_calls AS SELECT t.ts, t.name, t.server, t.args_chars, t.result_chars, t.is_error, t.duration_ms, s.harness
	FROM tool_calls t JOIN sessions s ON s.id = t.session_id;
INSERT INTO files VALUES ('/logs/with-skill.jsonl', 'claude', 1, 1), ('/logs/without.jsonl', 'claude', 1, 1), ('/logs/codex-mcp.jsonl', 'codex', 1, 1);
INSERT INTO sessions VALUES ('claude:a', '/logs/with-skill.jsonl', 'claude', NULL, 'main', '', '', 'me@host', 1, 1),
	('claude:b', '/logs/without.jsonl', 'claude', NULL, 'main', '', '', 'me@host', 1, 1),
	('codex:c', '/logs/codex-mcp.jsonl', 'codex', NULL, 'main', '', '', 'me@host', 1, 1);
INSERT INTO tool_calls VALUES ('/logs/with-skill.jsonl', 't1', 'claude:a', 1, 'Skill', '', 0, 0, 0, NULL),
	('/logs/without.jsonl', 't2', 'claude:b', 1, 'Read', '', 0, 0, 0, NULL),
	('/logs/codex-mcp.jsonl', 't3', 'codex:c', 1, 'mcp__demo__read', '', 0, 0, 0, NULL);
`

// openIndexOf80bf4b1 opens, with the current code, the index described by oldSchema.
func openIndexOf80bf4b1(t *testing.T) (*SQLiteIndex, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "index.db")
	old, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := old.Exec(oldSchema); err != nil {
		t.Fatal(err)
	}
	old.Close()
	return reopen(t, path), path
}

// indexOfMainBeforeSkillLoads is an index as main made it before tool calls recorded their skill, with its sessions
// listing up to date: a Claude log with a Skill call, a Claude log without, a Codex log whose function call kept the
// full name of an MCP tool, and a Codex log already read the way it is now.
func indexOfMainBeforeSkillLoads(t *testing.T) (path string) {
	t.Helper()
	s := open(t)
	path = pathOf(t, s)
	at := time.UnixMilli(1767261600000)
	for _, f := range []struct {
		file         string
		harness      kernel.Harness
		name, server string
	}{
		{"/logs/with-skill.jsonl", kernel.Claude, "Skill", ""}, {"/logs/without.jsonl", kernel.Claude, "Read", ""},
		{"/logs/codex-mcp.jsonl", kernel.Codex, "mcp__demo__read", ""}, {"/logs/codex-split.jsonl", kernel.Codex, "read", "demo"},
	} {
		tr := transcript(t, filepath.Base(f.file), at)
		tr.Tools[0].Name, tr.Tools[0].Server = f.name, f.server
		if err := save(s, application.LogFile{Path: f.file, Harness: f.harness, Size: 1}, tr); err != nil {
			t.Fatal(err)
		}
	}
	// what main left: no skill column, the sessions listing already built
	for _, q := range []string{`DROP VIEW usage_tool_calls`, `ALTER TABLE tool_calls DROP COLUMN skill`, `PRAGMA user_version = 1`} {
		if _, err := s.db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	s.Close()
	return path
}

// logsToReadAgain returns the logs the next indexing does not know, among those the index of a test holds.
func logsToReadAgain(t *testing.T, s *SQLiteIndex, logs ...string) (again []string) {
	t.Helper()
	known, err := s.IndexedFiles()
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range logs {
		if _, ok := known[l]; !ok {
			again = append(again, l)
		}
	}
	return again
}

func sameLogs(got []string, want ...string) bool {
	return fmt.Sprint(got) == fmt.Sprint(want)
}

func TestAnIndexOf80bf4b1ReadsAgainTheLogsWithASkillLoadOrAnMCPCall(t *testing.T) {
	// Given the index of 80bf4b1, holding a log with a Skill call, a log without and a Codex log with an MCP call
	// When it is opened
	s, _ := openIndexOf80bf4b1(t)

	// Then the next indexing reads the Codex log and the log with a Skill call again, and not the other
	if got := logsToReadAgain(t, s, "/logs/with-skill.jsonl", "/logs/without.jsonl", "/logs/codex-mcp.jsonl"); !sameLogs(got, "/logs/with-skill.jsonl", "/logs/codex-mcp.jsonl") {
		t.Errorf("logs to read again = %v", got)
	}
}

func TestAnIndexOf80bf4b1StillListsItsSessions(t *testing.T) {
	// Given the index of 80bf4b1, holding three sessions
	// When it is opened
	s, _ := openIndexOf80bf4b1(t)

	// Then the three sessions are listed
	if listed := len(listedUnder(t, s)); listed != 3 {
		t.Errorf("%d sessions listed, want 3", listed)
	}
}

func TestAnIndexOf80bf4b1PublishesItsToolCallsAsLoadingNoSkillYet(t *testing.T) {
	// Given the index of 80bf4b1, holding three tool calls
	// When it is opened
	s, _ := openIndexOf80bf4b1(t)

	// Then the published tool calls are all three there, none of them said to load a skill until its log is read again
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM usage_tool_calls WHERE skill = ''`).Scan(&n); err != nil || n != 3 {
		t.Errorf("tool calls published without a skill = %d, %v", n, err)
	}
}

func TestAnIndexOfMainReadsAgainTheLogsWithASkillLoad(t *testing.T) {
	// Given an index of main from before skill loads were recorded
	path := indexOfMainBeforeSkillLoads(t)

	// When it is opened
	s := reopen(t, path)

	// Then the next indexing reads again the log with a Skill call
	if got := logsToReadAgain(t, s, "/logs/with-skill.jsonl"); !sameLogs(got, "/logs/with-skill.jsonl") {
		t.Errorf("logs to read again = %v", got)
	}
}

func TestAnIndexOfMainReadsAgainTheCodexLogsThatNamedAnMCPToolInFull(t *testing.T) {
	// Given an index of main from before the server of a Codex MCP call was split out of its name
	path := indexOfMainBeforeSkillLoads(t)

	// When it is opened
	s := reopen(t, path)

	// Then the next indexing reads again the Codex log with a call named in full, and not the one already split
	if got := logsToReadAgain(t, s, "/logs/codex-mcp.jsonl", "/logs/codex-split.jsonl"); !sameLogs(got, "/logs/codex-mcp.jsonl") {
		t.Errorf("logs to read again = %v", got)
	}
}

func TestAnIndexOfMainKeepsTheLogsWithNothingToReadAgain(t *testing.T) {
	// Given an index of main from before skill loads were recorded
	path := indexOfMainBeforeSkillLoads(t)

	// When it is opened
	s := reopen(t, path)

	// Then the Claude log without a Skill call is still known
	if got := logsToReadAgain(t, s, "/logs/without.jsonl"); len(got) != 0 {
		t.Errorf("logs to read again = %v", got)
	}
}

func TestAnIndexOfMainPublishesItsToolCallsAsLoadingNoSkillYet(t *testing.T) {
	// Given an index of main from before skill loads were recorded, holding four tool calls
	path := indexOfMainBeforeSkillLoads(t)

	// When it is opened
	s := reopen(t, path)

	// Then the four published tool calls say that none loads a skill until its log is read again
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM usage_tool_calls WHERE skill = ''`).Scan(&n); err != nil || n != 4 {
		t.Errorf("tool calls published without a skill = %d, %v", n, err)
	}
}

func TestAnIndexOpenedBeforeReadsNoLogAgainTheSecondTime(t *testing.T) {
	// Given an index of 80bf4b1 that was opened, then its forgotten logs read again
	s, path := openIndexOf80bf4b1(t)
	for file, harness := range map[string]kernel.Harness{"/logs/with-skill.jsonl": kernel.Claude, "/logs/codex-mcp.jsonl": kernel.Codex} {
		if err := save(s, application.LogFile{Path: file, Harness: harness, Size: 1}, transcript(t, filepath.Base(file), time.UnixMilli(1))); err != nil {
			t.Fatal(err)
		}
	}
	s.Close()

	// When it is opened again
	again := reopen(t, path)

	// Then every log is known: none is forgotten twice
	if got := logsToReadAgain(t, again, "/logs/with-skill.jsonl", "/logs/without.jsonl", "/logs/codex-mcp.jsonl"); len(got) != 0 {
		t.Errorf("logs to read again = %v", got)
	}
}

// indexAlreadyRecordingSkillLoads is an index that gives tool calls a skill but was made before indexes were migrated by
// version: an early build of the catalogue, which added the column on its own.
func indexAlreadyRecordingSkillLoads(t *testing.T) string {
	t.Helper()
	s := open(t)
	tr := transcript(t, "s1", time.UnixMilli(1767261600000))
	tr.Tools[0].Name, tr.Tools[0].Server, tr.Tools[0].Skill = "Skill", "", "review"
	if err := save(s, application.LogFile{Path: "/logs/s1.jsonl", Harness: kernel.Claude, Size: 1}, tr); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`PRAGMA user_version = 0`); err != nil {
		t.Fatal(err)
	}
	path := pathOf(t, s)
	s.Close()
	return path
}

func TestAnIndexAlreadyRecordingSkillLoadsKeepsThem(t *testing.T) {
	// Given an index that already records the skill a call loads
	path := indexAlreadyRecordingSkillLoads(t)

	// When it is opened
	s := reopen(t, path)

	// Then the skill load recorded is still published
	var skill string
	if err := s.db.QueryRow(`SELECT skill FROM usage_tool_calls WHERE name = 'Skill'`).Scan(&skill); err != nil || skill != "review" {
		t.Errorf("skill = %q, %v", skill, err)
	}
}

func TestAnIndexAlreadyRecordingSkillLoadsReadsNoLogForThem(t *testing.T) {
	// Given an index that already records the skill a call loads
	path := indexAlreadyRecordingSkillLoads(t)

	// When it is opened
	s := reopen(t, path)

	// Then the log of the skill load recorded is still known
	if got := logsToReadAgain(t, s, "/logs/s1.jsonl"); len(got) != 0 {
		t.Errorf("logs to read again = %v", got)
	}
}

func TestANewerBuildsUpgradeIsNotRunAgainBecauseThisBuildWasOpeningTheSameIndex(t *testing.T) {
	// Given an index that a newer build, with one migration more than this one, upgrades while this build is opening it
	s := open(t)
	if _, err := s.db.Exec(`PRAGMA user_version = 0`); err != nil {
		t.Fatal(err)
	}
	newerBuild, err := sql.Open("sqlite", pathOf(t, s))
	if err != nil {
		t.Fatal(err)
	}
	defer newerBuild.Close()
	newerBuild.SetMaxOpenConns(1)
	if _, err := newerBuild.Exec(`BEGIN IMMEDIATE`); err != nil {
		t.Fatal(err)
	}
	opened := make(chan error, 1)
	go func() { opened <- migrate(s.db, migrations) }()
	time.Sleep(100 * time.Millisecond)

	// When the newer build finishes its upgrade, then opens the index again
	if _, err := newerBuild.Exec(`CREATE TABLE newer_runs (n INTEGER); INSERT INTO newer_runs VALUES (1); PRAGMA user_version = 3; COMMIT`); err != nil {
		t.Fatal(err)
	}
	if err := <-opened; err != nil {
		t.Fatal(err)
	}
	newer := append(append([]migration(nil), migrations...), sqlSteps(`INSERT INTO newer_runs VALUES (2)`))
	if err := migrate(s.db, newer); err != nil {
		t.Fatal(err)
	}

	// Then its upgrade ran once
	var runs int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM newer_runs`).Scan(&runs); err != nil || runs != 1 {
		t.Errorf("runs of the newer build's upgrade = %d, %v", runs, err)
	}
}

func TestSaveRecordsTheSkillOfASkillCall(t *testing.T) {
	// Given a transcript whose tool call loads a skill
	s := open(t)
	at := time.UnixMilli(1767261600000)
	tr := transcript(t, "s1", at)
	tr.Tools[0].Name, tr.Tools[0].Server, tr.Tools[0].Skill = "Skill", "", "review"

	// When it is saved, then the published view shows the skill
	if err := save(s, application.LogFile{Path: "/logs/s1.jsonl", Harness: kernel.Claude}, tr); err != nil {
		t.Fatal(err)
	}
	var skill string
	if err := s.db.QueryRow(`SELECT skill FROM usage_tool_calls WHERE name = 'Skill'`).Scan(&skill); err != nil || skill != "review" {
		t.Errorf("skill = %q, %v", skill, err)
	}
}
