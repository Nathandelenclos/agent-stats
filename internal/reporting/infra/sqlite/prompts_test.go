package sqlite

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Nathandelenclos/agent-stats/internal/kernel"
	report "github.com/Nathandelenclos/agent-stats/internal/reporting/domain"
)

// promptsIndexed writes, into a fresh read model, sessions whose prompts are all about the review:
//   - claude:main (hostile title and folder, last active t1), with a calm prompt, a flagged one with a hostile text and
//     control characters (t0), a repeated one (t1) and a flagged one longer than the text limit (t1); its subagent
//     claude:main/kid got a flagged brief that is not the user's;
//   - codex:old (ended t0), with one flagged prompt (t0);
//   - claude:gone/orphan, a subagent whose parent was never indexed, and codex:explore, a subagent of a named type: each
//     is its own root in the listing, and each got a flagged brief that is not the user's.
func promptsIndexed(t *testing.T) string {
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
			('claude:main', 'claude:main', 'claude', 'main', ?, ?, ?, ?, ?, ?),
			('claude:main/kid', 'claude:main', 'claude', 'subagent', 'kid', NULL, ?, ?, ?, ?),
			('codex:old', 'codex:old', 'codex', 'main', NULL, NULL, ?, ?, ?, ?),
			('claude:gone/orphan', 'claude:gone/orphan', 'claude', 'subagent', 'orphan', NULL, ?, ?, ?, ?),
			('codex:explore', 'codex:explore', 'codex', 'Explore', NULL, NULL, ?, ?, ?, ?)`,
			[]any{hostile, hostile, t0, t1, t0, t1, t0, t1, t0, t1, t0, t0, t0, t0, t0, t0, t0, t0, t0, t0, t0, t0}},
		{`INSERT INTO session_prompts VALUES
			('claude:main', 1, ?, 'add a test', 10, 3, 0, 0, 0, 0, 0, 0),
			('claude:main', 2, ?, ?, 20, 3, 3, 1, 2, 4, 0, 5),
			('claude:main', 3, ?, 'add a test', 10, 3, 0, 0, 0, 0, 1, 0),
			('claude:main', 4, ?, ?, ?, 1, 0, 0, 0, 1, 0, 0),
			('claude:main', 5, ?, ?, 86, 15, 2, 0, 0, 1, 0, 0),
			('claude:main/kid', 1, ?, 'NO NO brief to the subagent', 27, 5, 2, 0, 0, 0, 0, 0),
			('codex:old', 1, ?, 'stop that', 9, 2, 0, 0, 0, 1, 0, 0),
			('claude:gone/orphan', 1, ?, 'NO NO brief for the orphan', 26, 5, 2, 0, 0, 0, 0, 0),
			('codex:explore', 1, ?, 'STOP STOP brief for the explorer', 32, 6, 2, 0, 0, 2, 0, 0)`,
			[]any{t0, t0, hostile + "\x1b[2J", t1, t1, long, len([]rune(long)), t0,
				"[T3 Code: the agent in T3 thread \"thread-id\" sent this message, not the user.]\n\nNO NO", t0, t0, t0, t0}},
	} {
		if _, err := db.Exec(stmt.sql, stmt.args...); err != nil {
			t.Fatalf("%v: %s", err, stmt.sql)
		}
	}
	return path
}

func openPrompts(t *testing.T) *SQLitePrompts {
	t.Helper()
	r, err := OpenReadOnly(promptsIndexed(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	return r.Prompts()
}

func promptTexts(list []report.FlaggedPrompt) []string {
	var out []string
	for _, p := range list {
		out = append(out, p.Session.String()+"@"+time.UnixMilli(p.At.UnixMilli()).UTC().Format("15:04")+":"+p.Text[:min(len(p.Text), 8)])
	}
	return out
}

func TestFlaggedPromptsAreTheUsersOwnPromptsThatShowASignal(t *testing.T) {
	// Given sessions with calm, flagged and repeated prompts, and a flagged brief sent to a subagent
	p := openPrompts(t)

	// When the flagged prompts are listed
	list, err := p.FlaggedPrompts(t.Context(), report.Filter{})
	if err != nil {
		t.Fatal(err)
	}

	// Then the calm prompt and the subagent's brief are left out, and the most recent come first
	if len(list) != 4 {
		t.Fatalf("prompts = %v", promptTexts(list))
	}
	for _, x := range list {
		if strings.Contains(x.Text, "brief") || x.Text == "add a test" && !x.Signals.Repeated {
			t.Errorf("not the user's flagged prompt: %+v", x)
		}
	}
}

func TestFlaggedPromptsComeMostRecentFirst(t *testing.T) {
	// Given flagged prompts typed at two different times
	p := openPrompts(t)

	// When the flagged prompts are listed
	list, err := p.FlaggedPrompts(t.Context(), report.Filter{})
	if err != nil {
		t.Fatal(err)
	}

	// Then no prompt is typed later than the one before it
	for i := 1; i < len(list); i++ {
		if list[i].At.After(list[i-1].At) {
			t.Errorf("not most recent first: %v", promptTexts(list))
		}
	}
}

func TestAFlaggedPromptKeepsItsSessionAndItsSignals(t *testing.T) {
	// Given a flagged prompt typed in a session that has a title and a folder
	p := openPrompts(t)

	// When the flagged prompts are listed
	list, err := p.FlaggedPrompts(t.Context(), report.Filter{Harness: kernel.Claude, Since: time.UnixMilli(t0)})
	if err != nil {
		t.Fatal(err)
	}

	// Then the prompt carries its session's identity, title and folder, its signals and the time it was typed
	var got report.FlaggedPrompt
	for _, x := range list {
		if strings.HasPrefix(x.Text, hostile) {
			got = x
		}
	}
	if got.Session.String() != "claude:main" || got.Harness != kernel.Claude || got.Title != hostile || got.Folder != hostile {
		t.Errorf("session = %+v", got)
	}
	if !got.At.Equal(time.UnixMilli(t0)) || got.Signals != (report.PromptSignals{Yelling: 3, Profanity: 1, Anguish: 2, Negation: 4, Blame: 5}) {
		t.Errorf("prompt = %+v", got)
	}
}

func TestABriefSentToASubagentWithoutIndexedParentIsNotTheUsersPrompt(t *testing.T) {
	// Given subagents that are their own roots in the listing: one whose parent was never indexed, one of a named type
	p := openPrompts(t)

	// When the flagged prompts are listed
	list, err := p.FlaggedPrompts(t.Context(), report.Filter{})
	if err != nil {
		t.Fatal(err)
	}

	// Then their briefs are not among them, while the user's own flagged prompts all are
	for _, x := range list {
		if strings.Contains(x.Text, "brief for") || strings.Contains(x.Session.String(), "orphan") || x.Session.String() == "codex:explore" {
			t.Errorf("a subagent's brief was listed: %+v", x)
		}
	}
	if len(list) != 4 {
		t.Errorf("prompts = %v", promptTexts(list))
	}
}

func TestFlaggedPromptsKeepHostileTextsAsData(t *testing.T) {
	// Given a prompt that carries SQL metacharacters and a terminal escape
	p := openPrompts(t)

	// When the prompts are listed
	list, err := p.FlaggedPrompts(t.Context(), report.Filter{Harness: kernel.Claude})
	if err != nil {
		t.Fatal(err)
	}

	// Then the hostile text comes back as it was written, and the table still exists
	var seen bool
	for _, x := range list {
		seen = seen || x.Text == hostile+"\x1b[2J"
	}
	if !seen {
		t.Errorf("hostile text lost: %v", promptTexts(list))
	}
}

func TestFlaggedPromptsKeepTheFullTextForRecurrence(t *testing.T) {
	// Given a flagged prompt longer than the text limit
	p := openPrompts(t)

	// When the prompts are listed
	list, err := p.FlaggedPrompts(t.Context(), report.Filter{Harness: kernel.Claude})
	if err != nil {
		t.Fatal(err)
	}

	// Then its full text can be used to decide recurrence
	var full bool
	for _, x := range list {
		full = full || len([]rune(x.Text)) == report.PromptTextLimit+50
	}
	if !full {
		t.Errorf("long text was truncated: %v", promptTexts(list))
	}
}

func TestFlaggedPromptsFollowThePeriodAndHarnessFilters(t *testing.T) {
	p := openPrompts(t)
	for _, c := range []struct {
		name string
		f    report.Filter
		want int
	}{
		{"everything", report.Filter{}, 4},
		{"since the older prompts", report.Filter{Since: time.UnixMilli(t0)}, 4},
		{"since after the older prompts", report.Filter{Since: time.UnixMilli(t0 + 1)}, 2},
		{"since after everything", report.Filter{Since: time.UnixMilli(t1 + 1)}, 0},
		{"one harness", report.Filter{Harness: kernel.Codex}, 1},
		{"harness and period", report.Filter{Harness: kernel.Claude, Since: time.UnixMilli(t0 + 1)}, 2},
	} {
		// When the flagged prompts are listed with the filter
		list, err := p.FlaggedPrompts(t.Context(), c.f)
		// Then only the prompts typed in that range and harness come back
		if err != nil || len(list) != c.want {
			t.Errorf("%s: %v, %v; want %d", c.name, promptTexts(list), err, c.want)
		}
	}
}

func TestFlaggedPromptsExcludeAgentMessagesInAMainSession(t *testing.T) {
	p := openPrompts(t)
	list, err := p.FlaggedPrompts(t.Context(), report.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	for _, x := range list {
		if strings.HasPrefix(x.Text, "[T3 Code:") {
			t.Fatal("agent message appeared in the user's prompts")
		}
	}
}
