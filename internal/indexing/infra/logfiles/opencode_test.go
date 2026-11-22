package logfiles

import (
	"crypto/sha256"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Nathandelenclos/agent-stats/internal/indexing/domain"
	"github.com/Nathandelenclos/agent-stats/internal/kernel"
)

// buildDB creates the database a harness would have written, from the scripts of testdata/databases.
func buildDB(t *testing.T, path string, scripts ...string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, s := range scripts {
		raw, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "testdata", "databases", s))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(string(raw)); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
}

// sessionsOf lists the sessions of a database and fails on any problem.
func sessionsOf(t *testing.T, d *OpenCodeDatabase, path string) map[string]dbSession {
	t.Helper()
	sessions, problems := d.list(path)
	if len(problems) > 0 {
		t.Fatalf("problems: %v", problems)
	}
	out := map[string]dbSession{}
	for _, s := range sessions {
		out[s.id] = s
	}
	return out
}

func readSession(t *testing.T, d *OpenCodeDatabase, path, id string) domain.Transcript {
	t.Helper()
	s, ok := sessionsOf(t, d, path)[id]
	if !ok {
		t.Fatalf("session %s is not listed", id)
	}
	p, err := d.read(path, kernel.OpenCode, s)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func newDB(t *testing.T, scripts ...string) (*OpenCodeDatabase, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "opencode.db")
	buildDB(t, path, scripts...)
	return &OpenCodeDatabase{Prefix: "opencode"}, path
}

func ms(v int64) time.Time { return time.UnixMilli(v) }

func TestSessionsOfADatabaseAreListedWithWhatTellsThatTheyChanged(t *testing.T) {
	// Given a database of the messages generation with five sessions, one of which has no message
	d, path := newDB(t, "messages.sql")

	// When it is listed
	got := sessionsOf(t, d, path)

	// Then every session with messages is there, the empty one is not, and each carries its message count and last update
	if len(got) != 4 || got["ses_empty"].id != "" {
		t.Fatalf("sessions = %+v", got)
	}
	if s := got["ses_main"]; s.size != 5 || !s.mod.Equal(ms(1767225660000)) || s.events || s.table != "session" {
		t.Errorf("ses_main = %+v", s)
	}
	if s := got["ses_child"]; s.size != 2 || !s.mod.Equal(ms(1767225630000)) {
		t.Errorf("ses_child = %+v", s)
	}
}

func TestASessionThatGrowsShowsANewFingerprintWhileOthersDoNot(t *testing.T) {
	// Given an index of a database that is being written to, in write-ahead mode
	d, path := newDB(t, "messages.sql")
	writer, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	writer.SetMaxOpenConns(1)
	for _, q := range []string{`PRAGMA journal_mode = WAL`, `PRAGMA wal_autocheckpoint = 0`} {
		if _, err := writer.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	before := sessionsOf(t, d, path)

	// When a message is added to one session, and nothing is checkpointed into the database file
	if _, err := writer.Exec(`INSERT INTO message VALUES ('msg_99', 'ses_child', 1767225700000, 1767225700000, '{"role":"user","time":{"created":1767225700000}}')`); err != nil {
		t.Fatal(err)
	}
	after := sessionsOf(t, d, path)

	// Then only that session shows a new fingerprint: the reader sees what is in the -wal file
	for id, s := range after {
		changed := s.size != before[id].size || !s.mod.Equal(before[id].mod)
		if changed != (id == "ses_child") {
			t.Errorf("%s: changed = %v (%+v -> %+v)", id, changed, before[id], s)
		}
	}
}

func TestMessagesGenerationFillsTheTranscriptOfASession(t *testing.T) {
	// Given a session with a prompt, two tool calls, a priced answer, an unpriced one, a failed one and one still being written
	d, path := newDB(t, "messages.sql")

	// When it is read
	p := readSession(t, d, path, "ses_main")

	// Then the session is named, placed and spans its messages
	s := p.Session
	if s.ID.String() != "opencode:ses_main" || s.Harness != kernel.OpenCode || s.AgentType != domain.MainAgent || !s.ParentID.IsZero() ||
		s.Folder != "/work/demo-app" || s.Title != "Add a cache" || !s.Start.Equal(ms(1767225600000)) || !s.End.Equal(ms(1767225630000)) {
		t.Errorf("session = %+v", s)
	}
	// And the prompt is the text the user typed, not the synthetic or ignored parts
	if len(p.Prompts) != 1 || p.Prompts[0].Text != "Add a \ncache" || p.Prompts[0].Key != "msg_01" {
		t.Errorf("prompts = %+v", p.Prompts)
	}
	// And each answer that used the model is a request, with its own usage: the cache and the reasoning are counted apart
	if len(p.Requests) != 3 {
		t.Fatalf("requests = %+v", p.Requests)
	}
	first := p.Requests[0]
	if first.Key != "msg_02" || first.Model != "claude-test" || first.Provider != "anthropic" || first.StopReason != "tool-calls" || first.LoggedCost.USD() != 0.25 || first.Failed() {
		t.Errorf("first = %+v", first)
	}
	if want := (kernel.TokenCounts{Input: 100, Output: 50, Reasoning: 10, CacheRead: 600, CacheWrite: 30}); first.Tokens.Counts() != want {
		t.Errorf("first tokens = %+v, want %+v", first.Tokens.Counts(), want)
	}
	if second := p.Requests[1]; !second.LoggedCost.IsZero() || second.Tokens.Counts().CacheRead != 700 || second.Failed() {
		t.Errorf("second = %+v", second)
	}
	// And the answer that failed is a failed request, whose error is named
	if third := p.Requests[2]; third.Failure != "APIError" || third.Key != "msg_04" {
		t.Errorf("third = %+v", third)
	}
	// And the tool calls keep their sizes, their durations and whether they failed
	if len(p.Tools) != 2 {
		t.Fatalf("tools = %+v", p.Tools)
	}
	bash, lookup := p.Tools[0], p.Tools[1]
	if bash.Name != "bash" || bash.Key != "prt_02b" || bash.ArgsChars != len(`{"command":"ls"}`) || bash.ResultChars != len(`"a.go\nb.go\n"`) || bash.IsError || bash.Duration.Value() != 1500*time.Millisecond {
		t.Errorf("bash = %+v", bash)
	}
	if lookup.Name != "demo_lookup" || !lookup.IsError || lookup.ResultChars != len(`"not found"`) || lookup.Duration.Value() != 200*time.Millisecond {
		t.Errorf("lookup = %+v", lookup)
	}
	if p.SkippedLines != 0 {
		t.Errorf("skipped %d", p.SkippedLines)
	}
}

func TestMessagesGenerationLinksASubagentToItsParentAndDropsTheHistoryOfAFork(t *testing.T) {
	d, path := newDB(t, "messages.sql")

	// Given a child session
	child := readSession(t, d, path, "ses_child")
	// Then it is a subagent of its parent
	if child.Session.ParentID.String() != "opencode:ses_main" || child.Session.AgentType != domain.Subagent || len(child.Requests) != 1 || child.Requests[0].Provider != "openai" {
		t.Errorf("child = %+v", child)
	}

	// Given a session forked from another one, which holds a copy of the messages of the original
	fork := readSession(t, d, path, "ses_fork")
	// Then only what was said since the fork is its own: the copied answer is not counted a second time
	if len(fork.Requests) != 0 || len(fork.Prompts) != 1 || fork.Prompts[0].Text != "Try another way" {
		t.Errorf("fork: requests %+v, prompts %+v", fork.Requests, fork.Prompts)
	}
}

func TestMessagesGenerationRefusesWhatTheDatabaseStatesImpossibly(t *testing.T) {
	// Given a session whose rows are not JSON, have negative or huge counts, a negative cost, an error name with an escape sequence and a folder with one
	d, path := newDB(t, "messages.sql")

	// When it is read
	p := readSession(t, d, path, "ses_odd")

	// Then the rows that cannot be used are skipped and counted, the error name is replaced, and the folder is unknown
	if p.SkippedLines != 4 || len(p.Requests) != 1 || p.Requests[0].Key != "msg_34" || p.Requests[0].Failure != domain.UnspecifiedFailure {
		t.Errorf("skipped %d, requests %+v", p.SkippedLines, p.Requests)
	}
	if p.Session.Folder != "" {
		t.Errorf("folder = %q", p.Session.Folder)
	}
}

func TestEventsGenerationGivesTheTimeToFirstTokenAndTheDurationOfTheStream(t *testing.T) {
	// Given a database of the events generation
	d, path := newDB(t, "events.sql")

	// When its main session is read
	p := readSession(t, d, path, "ses_a")

	// Then the synthetic message is no prompt, and the title is the session's
	if len(p.Prompts) != 1 || p.Prompts[0].Text != "Refactor the loader" || p.Session.Title != "Refactor the loader" || p.Session.Folder != "/work/demo-app" {
		t.Errorf("prompts %+v, session %+v", p.Prompts, p.Session)
	}
	// And the answers and the compaction are requests, the first with the time to first token and the end of the stream
	if len(p.Requests) != 3 {
		t.Fatalf("requests = %+v", p.Requests)
	}
	a3, a4, a5 := p.Requests[0], p.Requests[1], p.Requests[2]
	if a3.TTFT.Value() != 800*time.Millisecond || a3.Duration.Value() != 6*time.Second || a3.LoggedCost.USD() != 0.5 || a3.StopReason != "tool-calls" {
		t.Errorf("a3 = %+v", a3)
	}
	if want := (kernel.TokenCounts{Input: 200, Output: 80, Reasoning: 20, CacheRead: 1000, CacheWrite: 50}); a3.Tokens.Counts() != want {
		t.Errorf("a3 tokens = %+v, want %+v", a3.Tokens.Counts(), want)
	}
	if a4.Failure != "ProviderOverloaded" || a4.TTFT.Known() || a4.Duration.Value() != 5*time.Second {
		t.Errorf("a4 = %+v", a4)
	}
	if a5.Key != "msg_a5" || a5.Tokens.Counts().Input != 500 || a5.LoggedCost.USD() != 0.1 {
		t.Errorf("a5 = %+v", a5)
	}
	// And the tools come from the parts and from the message that holds one itself
	if len(p.Tools) != 3 {
		t.Fatalf("tools = %+v", p.Tools)
	}
	read, search, write := p.Tools[0], p.Tools[1], p.Tools[2]
	if read.Name != "read" || read.ArgsChars != len(`{"path":"loader.go"}`) || read.ResultChars != len(`[{"type":"text","text":"package loader"}]`) || read.Duration.Value() != 500*time.Millisecond {
		t.Errorf("read = %+v", read)
	}
	if search.Name != "demo_search" || !search.IsError || search.ResultChars != len(`{"type":"NotFound","message":"nothing"}`) || search.Duration.Value() != 300*time.Millisecond {
		t.Errorf("search = %+v", search)
	}
	if write.Name != "write" || write.Key != "msg_a4:c0" || write.IsError || write.Duration.Value() != time.Second {
		t.Errorf("write = %+v", write)
	}
}

func TestEventsGenerationKeepsOnlyWhatAForkSaidSinceItWasForked(t *testing.T) {
	d, path := newDB(t, "events.sql")

	sub := readSession(t, d, path, "ses_b")
	if sub.Session.ParentID.String() != "opencode:ses_a" || sub.Session.AgentType != domain.Subagent || sub.Session.Title != "Find the callers" ||
		len(sub.Requests) != 1 || sub.Requests[0].TTFT.Value() != 500*time.Millisecond {
		t.Errorf("subagent = %+v", sub)
	}

	fork := readSession(t, d, path, "ses_c")
	if !fork.Session.ParentID.IsZero() || fork.Session.AgentType != domain.MainAgent {
		t.Errorf("a fork is not a subagent: %+v", fork.Session)
	}
	if len(fork.Requests) != 1 || fork.Requests[0].Key != "msg_c4" || len(fork.Prompts) != 1 || fork.Prompts[0].Text != "Do it with generics" {
		t.Errorf("fork: requests %+v, prompts %+v", fork.Requests, fork.Prompts)
	}
}

func TestADatabaseWithBothGenerationsGivesEachSessionItsEventsWhenItHasThem(t *testing.T) {
	// Given a database where the same session has rows in both generations
	d, path := newDB(t, "messages.sql")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, q := range []string{
		`CREATE TABLE session_message (id text PRIMARY KEY, session_id text NOT NULL, type text NOT NULL, seq integer, time_created integer NOT NULL, time_updated integer NOT NULL, data text NOT NULL)`,
		`INSERT INTO session_message VALUES ('ev_1', 'ses_child', 'user', 1, 1767225640000, 1767225640000, '{"text":"from the events"}')`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}

	// When the session is read, then its events are read and its messages are not counted too
	p := readSession(t, d, path, "ses_child")
	if len(p.Prompts) != 1 || p.Prompts[0].Text != "from the events" || len(p.Requests) != 0 {
		t.Errorf("prompts %+v, requests %+v", p.Prompts, p.Requests)
	}
}

func TestADatabaseIsOpenedForReadingOnly(t *testing.T) {
	for _, mode := range []string{"DELETE", "WAL"} {
		t.Run(mode, func(t *testing.T) {
			// Given a database, in a directory nobody may write to
			dir := t.TempDir()
			path := filepath.Join(dir, "opencode.db")
			buildDB(t, path, "messages.sql")
			if mode == "WAL" { // as OpenCode keeps it; closing the last connection leaves no -wal file behind
				db, err := sql.Open("sqlite", path)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := db.Exec(`PRAGMA journal_mode = WAL`); err != nil {
					t.Fatal(err)
				}
				db.Close()
			}
			sum := func() [32]byte {
				raw, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				return sha256.Sum256(raw)
			}
			before := sum()
			if err := os.Chmod(dir, 0o500); err != nil {
				t.Fatal(err)
			}
			defer os.Chmod(dir, 0o700)
			d := &OpenCodeDatabase{Prefix: "opencode"}

			// When every session is listed and read
			for id, s := range sessionsOf(t, d, path) {
				if _, err := d.read(path, kernel.OpenCode, s); err != nil {
					t.Errorf("%s: %v", id, err)
				}
			}

			// Then the database is as it was, and the connection refuses to write
			if sum() != before {
				t.Error("the database file changed")
			}
			db, err := d.handle(path)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(`DELETE FROM message`); err == nil {
				t.Error("the connection accepted a write")
			}
		})
	}
}

func TestDatabasesThatAreNotUsableAreProblemsNotFailures(t *testing.T) {
	dir := t.TempDir()
	notADB := filepath.Join(dir, "garbage.db")
	if err := os.WriteFile(notADB, []byte("this is not a database, but it is long enough to be mistaken for the header of one"), 0o600); err != nil {
		t.Fatal(err)
	}
	unknown := filepath.Join(dir, "unknown.db")
	buildDB(t, unknown)
	db, err := sql.Open("sqlite", unknown)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE session (id text, title text); CREATE TABLE message (id text, data text)`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	fifo := filepath.Join(dir, "pipe.db")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skip("mkfifo unavailable:", err)
	}
	missing := filepath.Join(dir, "absent.db")

	for name, tc := range map[string]struct {
		path string
		want error
	}{"not a database": {notADB, nil}, "unknown schema": {unknown, ErrUnknownSchema}, "fifo": {fifo, ErrNotRegular}, "missing": {missing, nil}} {
		t.Run(name, func(t *testing.T) {
			// When a database like that is listed
			done := make(chan struct{})
			var sessions []dbSession
			var problems []error
			go func() {
				defer close(done)
				sessions, problems = (&OpenCodeDatabase{Prefix: "opencode"}).list(tc.path)
			}()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("list blocks")
			}

			// Then it yields no session and one problem, which names the cause when it is a known one
			if len(sessions) != 0 || len(problems) == 0 {
				t.Fatalf("sessions %+v, problems %v", sessions, problems)
			}
			if tc.want != nil && !errors.Is(problems[0], tc.want) {
				t.Errorf("problem = %v, want %v", problems[0], tc.want)
			}
		})
	}
}

func TestOversizedRowsAndSessionsAreBounded(t *testing.T) {
	d, path := newDB(t, "messages.sql")

	// Given a bound on a row that the first message of ses_main exceeds
	defer func(n int) { maxDBRowBytes = n }(maxDBRowBytes)
	maxDBRowBytes = 150
	p := readSession(t, d, path, "ses_main")

	// Then the longer rows are skipped and counted, not read
	if p.SkippedLines == 0 {
		t.Error("no row was skipped")
	}
	maxDBRowBytes = 64 << 20

	// Given a bound on what a session may hold
	defer func(n int64) { maxDBSessionBytes = n }(maxDBSessionBytes)
	maxDBSessionBytes = 300
	s := sessionsOf(t, d, path)["ses_main"]
	if _, err := d.read(path, kernel.OpenCode, s); !errors.Is(err, ErrLogTooLarge) {
		t.Errorf("err = %v, want ErrLogTooLarge", err)
	}
}

func TestSessionsWithAnUnusableIdAreNotListed(t *testing.T) {
	d, path := newDB(t, "messages.sql")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, q := range []string{
		`INSERT INTO session VALUES ('../x', 'prj', NULL, 's', '/w', 't', '1', 1, 1)`,
		`INSERT INTO message VALUES ('m1', '../x', 1, 1, '{}')`,
		`INSERT INTO session VALUES ('ses_a/b', 'prj', NULL, 's', '/w', 't', '1', 1, 1)`,
		`INSERT INTO message VALUES ('m2', 'ses_a/b', 1, 1, '{}')`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	sessions, problems := d.list(path)
	if len(sessions) != 4 || len(problems) != 2 {
		t.Errorf("sessions %d, problems %v", len(sessions), problems)
	}
}

func TestDatabasesAreFoundByTheirNameInTheDataDirectoryOfTheHarness(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"opencode.db", "opencode-dev.db", "opencode.db-wal", "opencode.db-shm", "other.db", "notes.txt", "kilo.db", "kilo-beta.db", "opencode-beta.db"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, "opencode-x.db"), 0o700); err != nil {
		t.Fatal(err)
	}
	names := func(paths []string) []string {
		var out []string
		for _, p := range paths {
			out = append(out, filepath.Base(p))
		}
		return out
	}

	// When OpenCode's databases are looked for, then the database and its channel databases are, but not their companion files
	if got := names((&OpenCodeDatabase{Prefix: "opencode"}).paths(dir)); !slices.Equal(got, []string{"opencode-beta.db", "opencode-dev.db", "opencode.db"}) {
		t.Errorf("opencode: %v", got)
	}
	// And Kilo reads the channel databases it names itself, and one named the old way only where it has none of its own
	kilo := &OpenCodeDatabase{Prefix: "kilo", Fallback: "opencode"}
	if got := names(kilo.paths(dir)); !slices.Equal(got, []string{"kilo-beta.db", "kilo.db", "opencode-dev.db"}) {
		t.Errorf("kilo: %v", got)
	}
	// And a path the user sets replaces the search, relative to the data directory unless it is absolute
	if got := (&OpenCodeDatabase{Prefix: "opencode", Override: "mine.db"}).paths(dir); !slices.Equal(got, []string{filepath.Join(dir, "mine.db")}) {
		t.Errorf("override: %v", got)
	}
	if got := (&OpenCodeDatabase{Prefix: "opencode", Override: "/elsewhere/x.db"}).paths(dir); !slices.Equal(got, []string{"/elsewhere/x.db"}) {
		t.Errorf("absolute override: %v", got)
	}
	if got := (&OpenCodeDatabase{Prefix: "opencode"}).paths(filepath.Join(dir, "absent")); got != nil {
		t.Errorf("missing directory: %v", got)
	}
}

func TestSessionsOfADatabaseAreLogsOfTheirOwnForTheIndex(t *testing.T) {
	// Given a data directory with a database, read as a source of the harness
	root := t.TempDir()
	buildDB(t, filepath.Join(root, "opencode.db"), "messages.sql")
	logs := NewFileSessionLogs([]Source{{Harness: kernel.Kilo, Root: root, Database: &OpenCodeDatabase{Prefix: "opencode"}}})

	// When the logs are listed and one is read
	files, problems, err := logs.List()
	if err != nil || len(problems) != 0 {
		t.Fatal(files, problems, err)
	}

	// Then each session is a log named after its database and its id, with the harness of the source
	var names []string
	for _, f := range files {
		names = append(names, strings.TrimPrefix(f.Path, root+"/"))
		if f.Harness != kernel.Kilo || f.Size == 0 || f.ModTime.IsZero() || f.Weight <= 0 {
			t.Errorf("file = %+v", f)
		}
	}
	if want := []string{"opencode.db#ses_child", "opencode.db#ses_fork", "opencode.db#ses_main", "opencode.db#ses_odd"}; !slices.Equal(names, want) {
		t.Errorf("logs = %v", names)
	}
	p, err := logs.Read(files[2])
	if err != nil || p.Session.ID.String() != "kilo:ses_main" || p.Session.Harness != kernel.Kilo {
		t.Errorf("read = %+v, %v", p.Session, err)
	}
}

func TestReadingADatabaseNobodyHasOpenLeavesNoFileBesideIt(t *testing.T) {
	// Given a database in write-ahead mode that was closed cleanly, in a directory that may be written to
	dir := t.TempDir()
	path := filepath.Join(dir, "opencode.db")
	buildDB(t, path, "messages.sql")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`PRAGMA journal_mode = WAL`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	files := func() []string {
		entries, _ := os.ReadDir(dir)
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		return names
	}
	before := files()

	// When it is listed and read
	d := &OpenCodeDatabase{Prefix: "opencode"}
	for _, s := range sessionsOf(t, d, path) {
		if _, err := d.read(path, kernel.OpenCode, s); err != nil {
			t.Fatal(err)
		}
	}

	// Then no -wal or -shm file appeared
	if after := files(); !slices.Equal(before, after) {
		t.Errorf("files %v, then %v", before, after)
	}
}
