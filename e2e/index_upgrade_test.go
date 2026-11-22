package e2e

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	indexing "github.com/Nathandelenclos/agent-stats/internal/indexing/application"
	"github.com/Nathandelenclos/agent-stats/internal/indexing/infra/logfiles"
	"github.com/Nathandelenclos/agent-stats/internal/indexing/infra/sqlite"
	"github.com/Nathandelenclos/agent-stats/internal/kernel"
	pricing "github.com/Nathandelenclos/agent-stats/internal/pricing/domain"
	report "github.com/Nathandelenclos/agent-stats/internal/reporting/domain"
	usagedb "github.com/Nathandelenclos/agent-stats/internal/reporting/infra/sqlite"
)

// These tests open, with this build, an index an older build made, and observe what the report and the catalogue
// show of it, and what the next indexing reads again.

// sessionsListed counts the sessions the reporting adapter lists from the index at path.
func sessionsListed(t *testing.T, path string) int {
	t.Helper()
	r, err := usagedb.OpenReadOnly(path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	list, err := r.Sessions().Sessions(context.Background(), report.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	return len(list)
}

// indexOfACodexAndAClaudeLogBeforeMigrations is the index of main from before the Codex end times were fixed.
func indexOfACodexAndAClaudeLogBeforeMigrations(t *testing.T) string {
	t.Helper()
	path, db := indexAsMainMadeItBeforeItsMigrations(t)
	mainWritesALog(t, db, "codex", "/logs/codex.jsonl", "read", "demo")
	mainWritesALog(t, db, "claude", "/logs/claude.jsonl", "Read", "")
	return path
}

func TestCodexLogsIndexedBeforeTheEndTimeFixAreReadAgain(t *testing.T) {
	// Given an index built before the Codex end times were fixed, holding a Codex log and a Claude log
	path := indexOfACodexAndAClaudeLogBeforeMigrations(t)

	// When the index is opened by a build with the fix
	// Then the next indexing reads the Codex log again and leaves the Claude log alone
	if got := logsToReadAgain(t, path, "/logs/codex.jsonl", "/logs/claude.jsonl"); !slices.Equal(got, []string{"/logs/codex.jsonl"}) {
		t.Errorf("logs to read again = %v", got)
	}
}

func TestSessionsIndexedBeforeTheEndTimeFixStayListedUntilTheirLogIsReadAgain(t *testing.T) {
	// Given an index built before the Codex end times were fixed, holding a Codex log and a Claude log
	path := indexOfACodexAndAClaudeLogBeforeMigrations(t)

	// When the index is opened by a build with the fix
	openIndex(t, path)

	// Then both sessions are listed
	if n := sessionsListed(t, path); n != 2 {
		t.Errorf("%d sessions listed, want the two indexed so far", n)
	}
}

func TestCodexLogsReadAgainAreNotForgottenASecondTime(t *testing.T) {
	// Given a Codex log read again after the index was opened by a build with the fix
	path := indexOfACodexAndAClaudeLogBeforeMigrations(t)
	w := openIndex(t, path)
	saveLog(t, w, "/logs/codex.jsonl")
	w.Close()

	// When the index is opened again
	// Then the Codex log is still known, so it is not read a second time
	if got := logsToReadAgain(t, path, "/logs/codex.jsonl", "/logs/claude.jsonl"); len(got) != 0 {
		t.Errorf("logs to read again = %v", got)
	}
}

// indexWhoseUpgradeFails is an index of main holding a log with a Skill call, whose upgrade by this build is refused
// halfway by the database: it forbids the index to forget a log. It returns the index and what lifts the refusal.
func indexWhoseUpgradeFails(t *testing.T) (path string, lift func()) {
	t.Helper()
	path, db := indexAsMainMadeIt(t)
	mainWritesALog(t, db, "claude", "/logs/with-skill.jsonl", "Skill", "")
	if _, err := db.Exec(`CREATE TRIGGER refuse BEFORE DELETE ON files BEGIN SELECT RAISE(ABORT, 'refused'); END`); err != nil {
		t.Fatal(err)
	}
	return path, func() {
		if _, err := db.Exec(`DROP TRIGGER refuse`); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSessionsStayListedWhenTheUpgradeOfTheIndexFails(t *testing.T) {
	// Given an index due an upgrade the database refuses halfway
	path, _ := indexWhoseUpgradeFails(t)

	// When this build tries to open it
	_, err := sqlite.Open(sqlite.Options{Path: path, User: "me@host"})

	// Then it says so, and the sessions are still listed
	if err == nil {
		t.Fatal("the failing upgrade was not reported")
	}
	if n := sessionsListed(t, path); n != 1 {
		t.Errorf("%d sessions listed, want 1", n)
	}
}

func TestAFailedUpgradeOfTheIndexIsRunAgainOnceItsCauseIsGone(t *testing.T) {
	// Given an index whose upgrade failed halfway
	path, lift := indexWhoseUpgradeFails(t)
	if _, err := sqlite.Open(sqlite.Options{Path: path, User: "me@host"}); err == nil {
		t.Fatal("the failing upgrade was not reported")
	}

	// When the cause is gone and the index is opened again
	lift()
	openIndex(t, path)

	// Then it is upgraded: the Skill call awaits its log
	if a := activityOf(t, path); !a.PendingSkillLoads[kernel.Claude] {
		t.Errorf("activity: %+v", a)
	}
}

func TestAnUpgradeByANewerBuildWhileThisOneOpensTheIndexIsNotRunAgain(t *testing.T) {
	// Given an index due an upgrade that a newer build runs while this build is opening it
	path, _ := indexAsMainMadeItBeforeItsMigrations(t)
	newerBuild, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer newerBuild.Close()
	newerBuild.SetMaxOpenConns(1)
	if _, err := newerBuild.Exec(`BEGIN IMMEDIATE`); err != nil {
		t.Fatal(err)
	}
	opened := make(chan error, 1)
	go func() {
		w, err := sqlite.Open(sqlite.Options{Path: path, User: "me@host"})
		if err == nil {
			w.Close()
		}
		opened <- err
	}()
	time.Sleep(100 * time.Millisecond)

	// When the newer build commits its upgrade, this build finishes opening the index, and the newer build opens it again
	if _, err := newerBuild.Exec(`CREATE TABLE newer_runs (n INTEGER); INSERT INTO newer_runs VALUES (1); PRAGMA user_version = 99; COMMIT`); err != nil {
		t.Fatal(err)
	}
	if err := <-opened; err != nil {
		t.Fatal(err)
	}
	var version int
	if err := newerBuild.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version < 99 { // what a build does when its upgrade has not run: it runs it
		if _, err := newerBuild.Exec(`INSERT INTO newer_runs VALUES (2); PRAGMA user_version = 99`); err != nil {
			t.Fatal(err)
		}
	}

	// Then the upgrade of the newer build ran once
	var runs int
	if err := newerBuild.QueryRow(`SELECT COUNT(*) FROM newer_runs`).Scan(&runs); err != nil || runs != 1 {
		t.Errorf("runs of the newer build's upgrade = %d, %v", runs, err)
	}
}

// writeLogAsMainDoes writes through the statements of mainIndexInserts a Claude log holding a Skill call.
func writeLogAsMainDoes(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mainWritesALog(t, db, "claude", "/logs/written-by-main.jsonl", "Skill", "")
}

func TestAnIndexUpgradedByThisBuildIsStillWrittenByTheStatementsOfMain(t *testing.T) {
	for name, upgraded := range map[string]func(*testing.T) string{
		"new": func(t *testing.T) string {
			path := t.TempDir() + "/index.db"
			openIndex(t, path).Close()
			return path
		},
		"upgraded": func(t *testing.T) string {
			path, _ := indexAsMainMadeIt(t)
			openIndex(t, path).Close()
			return path
		},
	} {
		t.Run(name+" index", func(t *testing.T) {
			// Given an index this build created or upgraded
			path := upgraded(t)

			// When main, which writes with positional statements, writes a log with a Skill call
			writeLogAsMainDoes(t, path)

			// Then the write is accepted, and the call awaits its log, as main does not record skills
			if a := activityOf(t, path); !a.PendingSkillLoads[kernel.Claude] {
				t.Errorf("activity: %+v", a)
			}
		})
	}
}

func TestAnIndexALaterBuildGaveANewColumnToEveryTableIsStillWrittenByThisBuild(t *testing.T) {
	// Given an index in which a later build added a column to each table, as the build before this one could not
	path := t.TempDir() + "/index.db"
	openIndex(t, path).Close()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, table := range []string{"files", "sessions", "requests", "tool_calls", "tool_call_skills", "prompts"} {
		if _, err := db.Exec(`ALTER TABLE ` + table + ` ADD COLUMN added_later TEXT`); err != nil {
			t.Fatal(err)
		}
	}

	// When this build writes a log holding a Skill call and a prompt
	w := openIndex(t, path)
	saveLogWithAPrompt(t, w, "/logs/s1.jsonl", skillCall("t1", "review"))

	// Then the log is indexed and the skill it loaded is counted
	known, err := w.IndexedFiles()
	if err != nil || len(known) != 1 {
		t.Fatalf("indexed files = %+v, %v", known, err)
	}
	if a := activityOf(t, path); len(a.Skills) != 1 || a.Skills[0].Skill != "review" {
		t.Errorf("activity: %+v", a)
	}
}

// aClaudeLogOnDisk copies a Claude log into a directory of its own and returns the directory and the log.
func aClaudeLogOnDisk(t *testing.T) (dir, log string) {
	t.Helper()
	raw, err := os.ReadFile("../testdata/home/.claude/projects/-repo/abc.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	dir = t.TempDir()
	log = filepath.Join(dir, "abc.jsonl")
	if err := os.WriteFile(log, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return dir, log
}

// indexClaudeLogs indexes the Claude logs under dir into the index and says what the run read.
func indexClaudeLogs(t *testing.T, w *sqlite.SQLiteIndex, dir string) indexing.IndexReport {
	t.Helper()
	logs := logfiles.NewFileSessionLogs([]logfiles.Source{{Harness: kernel.Claude, Root: dir,
		Match: func(p string) bool { return strings.HasSuffix(p, ".jsonl") }, Parse: logfiles.ParseClaude}})
	rep, err := indexing.IndexSessions{Logs: logs, Store: w, Prices: pricing.PriceList{}}.Run(nil)
	if err != nil || len(rep.Errors) > 0 {
		t.Fatalf("index: %+v, %v", rep, err)
	}
	return rep
}

// mainRecordsTheFile is what main writes when it has indexed a log: the size and the modification time its file had.
func mainRecordsTheFile(t *testing.T, db *sql.DB, log string) {
	t.Helper()
	info, err := os.Stat(log)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(mainIndexInserts["files"], log, "claude", info.Size(), info.ModTime().UnixNano()); err != nil {
		t.Fatal(err)
	}
}

func TestALogAnEarlierVersionIndexedIsNotReadAgainWhileItsFileIsUnchanged(t *testing.T) {
	// Given an index an earlier version wrote, which indexed a Claude log
	dir, log := aClaudeLogOnDisk(t)
	path, db := indexAsMainMadeItBeforeItsMigrations(t)
	mainRecordsTheFile(t, db, log)

	// When this version indexes the logs, the file being as the earlier version saw it
	rep := indexClaudeLogs(t, openIndex(t, path), dir)

	// Then the log is not read again
	if rep.Scanned != 1 || rep.Indexed != 0 {
		t.Errorf("report = %+v", rep)
	}
}

func TestALogAnEarlierVersionIndexedAfterThisOneDidIsNotReadAgainWhileItsFileIsUnchanged(t *testing.T) {
	// Given a Claude log this version indexed, then grew, and an earlier version indexed in its new state
	dir, log := aClaudeLogOnDisk(t)
	path, db := indexAsMainMadeItBeforeItsMigrations(t)
	w := openIndex(t, path)
	if rep := indexClaudeLogs(t, w, dir); rep.Indexed != 1 {
		t.Fatalf("first run: %+v", rep)
	}
	f, err := os.OpenFile(log, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()
	mainRecordsTheFile(t, db, log)

	// When this version indexes the logs again
	rep := indexClaudeLogs(t, w, dir)

	// Then the log is not read again: the file was already read as it now is
	if rep.Indexed != 0 {
		t.Errorf("report = %+v", rep)
	}
}
