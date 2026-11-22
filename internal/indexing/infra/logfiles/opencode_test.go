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

	"github.com/Nathandelenclos/agent-stats/internal/indexing/application"
	"github.com/Nathandelenclos/agent-stats/internal/indexing/domain"
	"github.com/Nathandelenclos/agent-stats/internal/kernel"
)

// The scenarios read what a harness left on the machine through the port the index uses: the logs List finds,
// and the transcript Read makes of each. The databases are built from the scripts of testdata/databases.

// buildDB creates the database a harness would have written.
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

// harnessLogs reads the databases of one harness found under root.
func harnessLogs(h kernel.Harness, root string, d *OpenCodeDatabase) *FileSessionLogs {
	return NewFileSessionLogs([]Source{{Harness: h, Root: root, Database: d}})
}

// existingLogs lists the logs and fails on any problem; they are keyed by session id.
func existingLogs(t *testing.T, logs *FileSessionLogs) map[string]application.LogFile {
	t.Helper()
	files, problems, err := logs.List()
	if err != nil || len(problems) > 0 {
		t.Fatalf("list: %v, problems %v", err, problems)
	}
	out := map[string]application.LogFile{}
	for _, f := range files {
		_, id, _ := strings.Cut(filepath.Base(f.Path), DBSessionSep)
		out[id] = f
	}
	return out
}

func transcriptOf(t *testing.T, logs *FileSessionLogs, id string) domain.Transcript {
	t.Helper()
	f, ok := existingLogs(t, logs)[id]
	if !ok {
		t.Fatalf("session %s is not listed", id)
	}
	p, err := logs.Read(f)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// newOpenCode builds a database named opencode.db from the scripts and returns the logs that read it.
func newOpenCode(t *testing.T, scripts ...string) (logs *FileSessionLogs, db string) {
	t.Helper()
	root := t.TempDir()
	db = filepath.Join(root, "opencode.db")
	buildDB(t, db, scripts...)
	return harnessLogs(kernel.OpenCode, root, &OpenCodeDatabase{Prefix: "opencode"}), db
}

func ms(v int64) time.Time { return time.UnixMilli(v) }

func TestEachSessionOfADatabaseIsALogOfItsOwnNamedAfterItsDatabaseAndItsId(t *testing.T) {
	// Given the database of a harness with five sessions, one of which has no message
	logs, db := newOpenCode(t, "messages.sql")

	// When its logs are listed
	got := existingLogs(t, logs)

	// Then the four sessions that hold messages are listed, each named after the database and its id, and the empty one is not
	if len(got) != 4 {
		t.Fatalf("logs = %v", got)
	}
	if _, ok := got["ses_empty"]; ok {
		t.Error("an empty session is listed")
	}
	for id, f := range got {
		if f.Path != db+DBSessionSep+id || f.Harness != kernel.OpenCode || f.Size == 0 || f.ModTime.IsZero() || f.Weight <= 0 {
			t.Errorf("%s: %+v", id, f)
		}
	}
}

func TestASessionIsReadForTheHarnessOfItsSource(t *testing.T) {
	// Given the same database, read as the database of Kilo Code
	root := t.TempDir()
	buildDB(t, filepath.Join(root, "kilo.db"), "messages.sql")
	logs := harnessLogs(kernel.Kilo, root, &OpenCodeDatabase{Prefix: "kilo"})

	// When a session is read
	p := transcriptOf(t, logs, "ses_main")

	// Then it belongs to that harness
	if p.Session.ID.String() != "kilo:ses_main" || p.Session.Harness != kernel.Kilo {
		t.Errorf("session = %+v", p.Session)
	}
}

func TestASessionThatGrowsWhileItsHarnessRunsIsListedAsChangedAndTheOthersAreNot(t *testing.T) {
	// Given a harness that is running, with a session list that was read once
	logs, db := newOpenCode(t, "messages.sql")
	running, err := sql.Open("sqlite", db)
	if err != nil {
		t.Fatal(err)
	}
	defer running.Close()
	running.SetMaxOpenConns(1)
	for _, q := range []string{`PRAGMA journal_mode = WAL`, `PRAGMA wal_autocheckpoint = 0`} {
		if _, err := running.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	before := existingLogs(t, logs)

	// When a message is added to one session, before the harness has saved it into its database file
	if _, err := running.Exec(`INSERT INTO message VALUES ('msg_99', 'ses_child', 1767225700000, 1767225700000, '{"role":"user","time":{"created":1767225700000}}')`); err != nil {
		t.Fatal(err)
	}
	after := existingLogs(t, logs)

	// Then only that session is listed with a different size and modification time
	for id, f := range after {
		changed := f.Size != before[id].Size || !f.ModTime.Equal(before[id].ModTime)
		if changed != (id == "ses_child") {
			t.Errorf("%s: changed = %v (%+v -> %+v)", id, changed, before[id], f)
		}
	}
}

func TestSessionOfTheMessagesGenerationKeepsItsPlaceAndItsPrompt(t *testing.T) {
	// Given a session with a prompt made of several parts, some of which the user did not type
	logs, _ := newOpenCode(t, "messages.sql")

	// When it is read
	p := transcriptOf(t, logs, "ses_main")

	// Then it is named, placed in its folder and spans its messages
	s := p.Session
	if s.ID.String() != "opencode:ses_main" || s.Harness != kernel.OpenCode || s.AgentType != domain.MainAgent || !s.ParentID.IsZero() ||
		s.Folder != "/work/demo-app" || s.Title != "Add a cache" || !s.Start.Equal(ms(1767225600000)) || !s.End.Equal(ms(1767225630000)) {
		t.Errorf("session = %+v", s)
	}
	// And the prompt is what the user typed, not the synthetic or ignored parts
	if len(p.Prompts) != 1 || p.Prompts[0].Text != "Add a \ncache" || p.Prompts[0].Key != "msg_01" {
		t.Errorf("prompts = %+v", p.Prompts)
	}
}

func TestEachAnswerThatUsedTheModelIsARequestWithItsOwnUsage(t *testing.T) {
	// Given a session with a priced answer, an unpriced one, a failed one and one that is still being written
	logs, _ := newOpenCode(t, "messages.sql")

	// When it is read
	p := transcriptOf(t, logs, "ses_main")

	// Then the answers that used the model are requests, and the unfinished one is not
	if len(p.Requests) != 3 {
		t.Fatalf("requests = %+v", p.Requests)
	}
	// And the first states its model, its cost and its usage, the cache and the reasoning counted apart
	first := p.Requests[0]
	if first.Key != "msg_02" || first.Model != "claude-test" || first.Provider != "anthropic" || first.StopReason != "tool-calls" || first.LoggedCost.USD() != 0.25 || first.Failed() {
		t.Errorf("first = %+v", first)
	}
	if want := (kernel.TokenCounts{Input: 100, Output: 50, Reasoning: 10, CacheRead: 600, CacheWrite: 30}); first.Tokens.Counts() != want {
		t.Errorf("first tokens = %+v, want %+v", first.Tokens.Counts(), want)
	}
	// And the second logged no cost, so it is priced from the table
	if second := p.Requests[1]; !second.LoggedCost.IsZero() || second.Tokens.Counts().CacheRead != 700 || second.Failed() {
		t.Errorf("second = %+v", second)
	}
	// And the answer that failed is a failed request, whose error is named
	if third := p.Requests[2]; third.Failure != "APIError" || third.Key != "msg_04" {
		t.Errorf("third = %+v", third)
	}
}

func TestToolCallsKeepTheirSizesTheirDurationsAndWhetherTheyFailed(t *testing.T) {
	// Given an answer that called two tools, one of which failed
	logs, _ := newOpenCode(t, "messages.sql")

	// When the session is read
	p := transcriptOf(t, logs, "ses_main")

	// Then each call states its size, its duration and its outcome
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
}

func TestASessionWithAParentIsASubagentOfIt(t *testing.T) {
	// Given a session that a parent session started
	logs, _ := newOpenCode(t, "messages.sql")

	// When it is read
	child := transcriptOf(t, logs, "ses_child")

	// Then it is a subagent of its parent, and its own requests are its own
	if child.Session.ParentID.String() != "opencode:ses_main" || child.Session.AgentType != domain.Subagent || len(child.Requests) != 1 || child.Requests[0].Provider != "openai" {
		t.Errorf("child = %+v", child)
	}
}

func TestAForkedSessionCountsOnlyWhatWasSaidSinceTheFork(t *testing.T) {
	// Given a session forked from another one, which holds a copy of the messages of the original
	logs, _ := newOpenCode(t, "messages.sql")

	// When it is read
	fork := transcriptOf(t, logs, "ses_fork")

	// Then the copied answer is not counted a second time: only the prompt that was typed since the fork is its own
	if len(fork.Requests) != 0 || len(fork.Prompts) != 1 || fork.Prompts[0].Text != "Try another way" {
		t.Errorf("fork: requests %+v, prompts %+v", fork.Requests, fork.Prompts)
	}
}

func TestMessagesThatCannotBeUsedAreSkippedAndCounted(t *testing.T) {
	// Given a session with a message that is not JSON, usage that is negative or beyond any model's window, a negative cost,
	// an error name with an escape sequence, and a folder that has one
	logs, _ := newOpenCode(t, "messages.sql")

	// When it is read
	p := transcriptOf(t, logs, "ses_odd")

	// Then the messages that cannot be used are skipped and counted, and the error name is replaced by a plain one
	if p.SkippedLines != 4 || len(p.Requests) != 1 || p.Requests[0].Key != "msg_34" || p.Requests[0].Failure != domain.UnspecifiedFailure {
		t.Errorf("skipped %d, requests %+v", p.SkippedLines, p.Requests)
	}
	// And the folder is unknown
	if p.Session.Folder != "" {
		t.Errorf("folder = %q", p.Session.Folder)
	}
}

func TestPromptsAndTitleOfTheEventsGenerationAreWhatTheUserTyped(t *testing.T) {
	// Given a session of the events generation that holds a message the user did not type
	logs, _ := newOpenCode(t, "events.sql")

	// When it is read
	p := transcriptOf(t, logs, "ses_a")

	// Then only what the user typed is a prompt, and the title and folder are the session's
	if len(p.Prompts) != 1 || p.Prompts[0].Text != "Refactor the loader" || p.Session.Title != "Refactor the loader" || p.Session.Folder != "/work/demo-app" {
		t.Errorf("prompts %+v, session %+v", p.Prompts, p.Session)
	}
}

func TestEventsGenerationGivesTheTimeToFirstTokenAndTheDurationOfTheStream(t *testing.T) {
	// Given a session of the events generation with two answers and a compaction
	logs, _ := newOpenCode(t, "events.sql")

	// When it is read
	p := transcriptOf(t, logs, "ses_a")

	// Then the answers and the compaction are requests
	if len(p.Requests) != 3 {
		t.Fatalf("requests = %+v", p.Requests)
	}
	a3, a4, a5 := p.Requests[0], p.Requests[1], p.Requests[2]
	// And the first states when the first token came and when the stream ended
	if a3.TTFT.Value() != 800*time.Millisecond || a3.Duration.Value() != 6*time.Second || a3.LoggedCost.USD() != 0.5 || a3.StopReason != "tool-calls" {
		t.Errorf("a3 = %+v", a3)
	}
	if want := (kernel.TokenCounts{Input: 200, Output: 80, Reasoning: 20, CacheRead: 1000, CacheWrite: 50}); a3.Tokens.Counts() != want {
		t.Errorf("a3 tokens = %+v, want %+v", a3.Tokens.Counts(), want)
	}
	// And the second failed and logged no time to first token
	if a4.Failure != "ProviderOverloaded" || a4.TTFT.Known() || a4.Duration.Value() != 5*time.Second {
		t.Errorf("a4 = %+v", a4)
	}
	// And the compaction counts as a request of its own
	if a5.Key != "msg_a5" || a5.Tokens.Counts().Input != 500 || a5.LoggedCost.USD() != 0.1 {
		t.Errorf("a5 = %+v", a5)
	}
}

func TestEventsGenerationTakesToolCallsFromTheirPartsAndFromTheMessageThatHoldsOne(t *testing.T) {
	// Given a session whose first answer has its tool calls as parts, and whose second holds its tool call itself
	logs, _ := newOpenCode(t, "events.sql")

	// When it is read
	p := transcriptOf(t, logs, "ses_a")

	// Then all three calls are there, with their sizes, durations and outcomes
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

func TestASessionOfTheEventsGenerationWithAParentIsASubagentOfIt(t *testing.T) {
	logs, _ := newOpenCode(t, "events.sql")

	sub := transcriptOf(t, logs, "ses_b")

	if sub.Session.ParentID.String() != "opencode:ses_a" || sub.Session.AgentType != domain.Subagent || sub.Session.Title != "Find the callers" ||
		len(sub.Requests) != 1 || sub.Requests[0].TTFT.Value() != 500*time.Millisecond {
		t.Errorf("subagent = %+v", sub)
	}
}

func TestAForkedSessionOfTheEventsGenerationIsNotASubagentAndCountsOnlyWhatWasSaidSinceTheFork(t *testing.T) {
	logs, _ := newOpenCode(t, "events.sql")

	fork := transcriptOf(t, logs, "ses_c")

	if !fork.Session.ParentID.IsZero() || fork.Session.AgentType != domain.MainAgent {
		t.Errorf("a fork is not a subagent: %+v", fork.Session)
	}
	if len(fork.Requests) != 1 || fork.Requests[0].Key != "msg_c4" || len(fork.Prompts) != 1 || fork.Prompts[0].Text != "Do it with generics" {
		t.Errorf("fork: requests %+v, prompts %+v", fork.Requests, fork.Prompts)
	}
}

func TestASessionKnownToBothGenerationsIsReadFromItsEventsOnly(t *testing.T) {
	// Given a session that has messages in both generations of the schema
	logs, db := newOpenCode(t, "messages.sql")
	writer, err := sql.Open("sqlite", db)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	for _, q := range []string{
		`CREATE TABLE session_message (id text PRIMARY KEY, session_id text NOT NULL, type text NOT NULL, seq integer, time_created integer NOT NULL, time_updated integer NOT NULL, data text NOT NULL)`,
		`INSERT INTO session_message VALUES ('ev_1', 'ses_child', 'user', 1, 1767225640000, 1767225640000, '{"text":"from the events"}')`,
	} {
		if _, err := writer.Exec(q); err != nil {
			t.Fatal(err)
		}
	}

	// When it is read
	p := transcriptOf(t, logs, "ses_child")

	// Then what it said in the events is read, and its messages are not counted too
	if len(p.Prompts) != 1 || p.Prompts[0].Text != "from the events" || len(p.Requests) != 0 {
		t.Errorf("prompts %+v, requests %+v", p.Prompts, p.Requests)
	}
}

func TestReadingADatabaseNeverChangesIt(t *testing.T) {
	for _, mode := range []string{"DELETE", "WAL"} {
		t.Run(mode, func(t *testing.T) {
			// Given a database nobody has open, in a directory nobody may write to
			root := t.TempDir()
			db := filepath.Join(root, "opencode.db")
			buildDB(t, db, "messages.sql")
			if mode == "WAL" { // as OpenCode keeps it; closing the last connection leaves nothing beside the database
				w, err := sql.Open("sqlite", db)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := w.Exec(`PRAGMA journal_mode = WAL`); err != nil {
					t.Fatal(err)
				}
				w.Close()
			}
			sum := func() [32]byte {
				raw, err := os.ReadFile(db)
				if err != nil {
					t.Fatal(err)
				}
				return sha256.Sum256(raw)
			}
			before := sum()
			if err := os.Chmod(root, 0o500); err != nil {
				t.Fatal(err)
			}
			defer os.Chmod(root, 0o700)
			logs := harnessLogs(kernel.OpenCode, root, &OpenCodeDatabase{Prefix: "opencode"})

			// When every session is listed and read
			for id, f := range existingLogs(t, logs) {
				if _, err := logs.Read(f); err != nil {
					t.Errorf("%s: %v", id, err)
				}
			}

			// Then the database is as it was
			if sum() != before {
				t.Error("the database changed")
			}
		})
	}
}

func TestReadingADatabaseNobodyHasOpenLeavesNothingBesideIt(t *testing.T) {
	// Given a database in write-ahead mode that was closed cleanly, in a directory that may be written to
	root := t.TempDir()
	db := filepath.Join(root, "opencode.db")
	buildDB(t, db, "messages.sql")
	w, err := sql.Open("sqlite", db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Exec(`PRAGMA journal_mode = WAL`); err != nil {
		t.Fatal(err)
	}
	w.Close()
	names := func() []string {
		entries, _ := os.ReadDir(root)
		var out []string
		for _, e := range entries {
			out = append(out, e.Name())
		}
		return out
	}
	before := names()
	logs := harnessLogs(kernel.OpenCode, root, &OpenCodeDatabase{Prefix: "opencode"})

	// When it is listed and read
	for _, f := range existingLogs(t, logs) {
		if _, err := logs.Read(f); err != nil {
			t.Fatal(err)
		}
	}

	// Then no file appeared beside it
	if after := names(); !slices.Equal(before, after) {
		t.Errorf("files %v, then %v", before, after)
	}
}

func TestDatabasesThatCannotBeUsedAreReportedAndYieldNoSession(t *testing.T) {
	root := t.TempDir()
	notADB := filepath.Join(root, "garbage.db")
	if err := os.WriteFile(notADB, []byte("this is not a database, but it is long enough to be mistaken for the header of one"), 0o600); err != nil {
		t.Fatal(err)
	}
	unknown := filepath.Join(root, "unknown.db")
	buildDB(t, unknown)
	w, err := sql.Open("sqlite", unknown)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Exec(`CREATE TABLE session (id text, title text); CREATE TABLE message (id text, data text)`); err != nil {
		t.Fatal(err)
	}
	w.Close()
	fifo := filepath.Join(root, "pipe.db")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skip("mkfifo unavailable:", err)
	}

	for name, tc := range map[string]struct {
		path string
		want error // nil: the database is not there, which is no problem
	}{
		"a file that is not a database": {notADB, ErrDatabaseUnreadable},
		"a database of another schema":  {unknown, ErrUnknownSchema},
		"a pipe":                        {fifo, ErrNotRegular},
		"a database that is not there":  {filepath.Join(root, "absent.db"), nil},
	} {
		t.Run(name, func(t *testing.T) {
			// When the database is listed
			logs := harnessLogs(kernel.OpenCode, root, &OpenCodeDatabase{Prefix: "opencode", Override: tc.path})
			done := make(chan struct{})
			var files []application.LogFile
			var problems []error
			go func() {
				defer close(done)
				files, problems, _ = logs.List()
			}()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("list blocks")
			}

			// Then it yields no session, and a problem that says why unless the database is simply not there
			if len(files) != 0 {
				t.Fatalf("files %+v", files)
			}
			if tc.want == nil && len(problems) != 0 || tc.want != nil && (len(problems) != 1 || !errors.Is(problems[0], tc.want)) {
				t.Errorf("problems %v, want %v", problems, tc.want)
			}
		})
	}
}

func TestASessionThatWasDeletedAfterTheListingIsReportedGone(t *testing.T) {
	// Given a harness that is running, and a session that was listed and then deleted by it
	logs, db := newOpenCode(t, "messages.sql")
	running, err := sql.Open("sqlite", db)
	if err != nil {
		t.Fatal(err)
	}
	defer running.Close()
	running.SetMaxOpenConns(1)
	for _, q := range []string{`PRAGMA journal_mode = WAL`, `UPDATE session SET title = title || '!' WHERE id = 'ses_child'`} {
		if _, err := running.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	f := existingLogs(t, logs)["ses_main"]
	if _, err := running.Exec(`DELETE FROM session WHERE id = 'ses_main'`); err != nil {
		t.Fatal(err)
	}

	// When it is read
	_, err = logs.Read(f)

	// Then the failure says the session is gone, in the vocabulary of the reader and not of its driver
	if !errors.Is(err, ErrSessionGone) || errors.Is(err, sql.ErrNoRows) {
		t.Errorf("err = %v, want ErrSessionGone", err)
	}
}

func TestMessagesLargerThanTheBoundAreSkippedAndASessionLargerThanItsBoundIsRefused(t *testing.T) {
	// Given a bound on the size of one message that the first message of ses_main exceeds
	logs, _ := newOpenCode(t, "messages.sql")
	defer func(n int) { maxDBRowBytes = n }(maxDBRowBytes)
	maxDBRowBytes = 150

	// When the session is read, then the longer messages are skipped and counted, not read
	if p := transcriptOf(t, logs, "ses_main"); p.SkippedLines == 0 {
		t.Error("no message was skipped")
	}
	maxDBRowBytes = 64 << 20

	// Given a bound on what one session may hold
	defer func(n int64) { maxDBSessionBytes = n }(maxDBSessionBytes)
	maxDBSessionBytes = 300

	// When the session is read, then it is refused as too large
	f := existingLogs(t, logs)["ses_main"]
	if _, err := logs.Read(f); !errors.Is(err, ErrLogTooLarge) {
		t.Errorf("err = %v, want ErrLogTooLarge", err)
	}
}

func TestSessionsWithAnUnusableIdAreReportedAndNotListed(t *testing.T) {
	// Given sessions whose ids could be taken for a path
	logs, db := newOpenCode(t, "messages.sql")
	w, err := sql.Open("sqlite", db)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	for _, q := range []string{
		`INSERT INTO session VALUES ('../x', 'prj', NULL, 's', '/w', 't', '1', 1, 1)`,
		`INSERT INTO message VALUES ('m1', '../x', 1, 1, '{}')`,
		`INSERT INTO session VALUES ('ses_a/b', 'prj', NULL, 's', '/w', 't', '1', 1, 1)`,
		`INSERT INTO message VALUES ('m2', 'ses_a/b', 1, 1, '{}')`,
	} {
		if _, err := w.Exec(q); err != nil {
			t.Fatal(err)
		}
	}

	// When the logs are listed
	files, problems, err := logs.List()

	// Then the usable sessions are listed and each unusable one is reported
	if err != nil || len(files) != 4 || len(problems) != 2 {
		t.Errorf("files %d, problems %v, err %v", len(files), problems, err)
	}
}

func TestOnlyTheDatabasesOfTheHarnessAreRead(t *testing.T) {
	// Given a data directory with the databases of a channel, companion files, a database of another name and one named the old way
	root := t.TempDir()
	seed := filepath.Join(t.TempDir(), "seed.db")
	buildDB(t, seed, "messages.sql")
	raw, err := os.ReadFile(seed)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"opencode.db", "opencode-dev.db", "other.db", "kilo.db", "kilo-beta.db", "opencode-beta.db"} {
		if err := os.WriteFile(filepath.Join(root, name), raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"opencode.db-wal", "opencode.db-shm", "notes.txt"} {
		if err := os.WriteFile(filepath.Join(root, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(root, "opencode-x.db"), 0o700); err != nil {
		t.Fatal(err)
	}
	readBy := func(d *OpenCodeDatabase) []string {
		files, _, err := harnessLogs(kernel.OpenCode, root, d).List()
		if err != nil {
			t.Fatal(err)
		}
		seen := map[string]bool{}
		for _, f := range files {
			name, _, _ := strings.Cut(filepath.Base(f.Path), DBSessionSep)
			seen[name] = true
		}
		var out []string
		for name := range seen {
			out = append(out, name)
		}
		slices.Sort(out)
		return out
	}

	// When OpenCode's databases are read, then the database and its channels are, but not their companion files
	if got := readBy(&OpenCodeDatabase{Prefix: "opencode"}); !slices.Equal(got, []string{"opencode-beta.db", "opencode-dev.db", "opencode.db"}) {
		t.Errorf("opencode: %v", got)
	}
	// And Kilo reads the channel databases it names itself, and one named the old way only where it has none of its own
	if got := readBy(&OpenCodeDatabase{Prefix: "kilo", Fallback: "opencode"}); !slices.Equal(got, []string{"kilo-beta.db", "kilo.db", "opencode-dev.db"}) {
		t.Errorf("kilo: %v", got)
	}
	// And a path the user sets replaces the search, relative to the data directory unless it is absolute
	if got := readBy(&OpenCodeDatabase{Prefix: "opencode", Override: "other.db"}); !slices.Equal(got, []string{"other.db"}) {
		t.Errorf("relative override: %v", got)
	}
	if got := readBy(&OpenCodeDatabase{Prefix: "opencode", Override: filepath.Join(root, "kilo.db")}); !slices.Equal(got, []string{"kilo.db"}) {
		t.Errorf("absolute override: %v", got)
	}
	// And a data directory that is not there holds nothing
	if files, _, err := harnessLogs(kernel.OpenCode, filepath.Join(root, "absent"), &OpenCodeDatabase{Prefix: "opencode"}).List(); err != nil || len(files) != 0 {
		t.Errorf("missing directory: %v, %v", files, err)
	}
}
