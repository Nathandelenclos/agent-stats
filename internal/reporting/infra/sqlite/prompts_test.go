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
//   - codex:old (ended t0), with one flagged prompt (t0).
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
			('codex:old', 'codex:old', 'codex', 'main', NULL, NULL, ?, ?, ?, ?)`,
			[]any{hostile, hostile, t0, t1, t0, t1, t0, t1, t0, t1, t0, t0, t0, t0}},
		{`INSERT INTO session_prompts VALUES
			('claude:main', 1, ?, 'add a test', 10, 3, 0, 0, 0, 0, 0, 0),
			('claude:main', 2, ?, ?, 20, 3, 3, 1, 2, 4, 0, 5),
			('claude:main', 3, ?, 'add a test', 10, 3, 0, 0, 0, 0, 1, 0),
			('claude:main', 4, ?, ?, ?, 1, 0, 0, 0, 1, 0, 0),
			('claude:main/kid', 1, ?, 'NO NO brief to the subagent', 27, 5, 2, 0, 0, 0, 0, 0),
			('codex:old', 1, ?, 'stop that', 9, 2, 0, 0, 0, 1, 0, 0)`,
			[]any{t0, t0, hostile + "\x1b[2J", t1, t1, long, len([]rune(long)), t0, t0}},
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
	list, err := p.FlaggedPrompts(report.Filter{})
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
	// And a prompt keeps its session's identity, its signals and its time
	hostilePrompt := list[2]
	if hostilePrompt.Session.String() != "claude:main" || hostilePrompt.Harness != kernel.Claude || hostilePrompt.Title != hostile || hostilePrompt.Folder != hostile {
		t.Errorf("session = %+v", hostilePrompt)
	}
	if !hostilePrompt.At.Equal(time.UnixMilli(t0)) || hostilePrompt.Signals != (report.PromptSignals{Yelling: 3, Profanity: 1, Anguish: 2, Negation: 4, Blame: 5}) {
		t.Errorf("prompt = %+v", hostilePrompt)
	}
}

func TestFlaggedPromptsKeepHostileTextsAsData(t *testing.T) {
	// Given a prompt that carries SQL metacharacters and a terminal escape, and a text over the limit
	p := openPrompts(t)

	// When the prompts are listed
	list, err := p.FlaggedPrompts(report.Filter{Harness: kernel.Claude})
	if err != nil {
		t.Fatal(err)
	}

	// Then the hostile text comes back as it was written, the table still exists, and the long text is cut
	var seen, cut bool
	for _, x := range list {
		seen = seen || x.Text == hostile+"\x1b[2J"
		cut = cut || len([]rune(x.Text)) == report.PromptTextLimit
	}
	if !seen || !cut {
		t.Errorf("hostile text kept: %v, long text cut: %v", seen, cut)
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
		list, err := p.FlaggedPrompts(c.f)
		// Then only the prompts typed in that range and harness come back
		if err != nil || len(list) != c.want {
			t.Errorf("%s: %v, %v; want %d", c.name, promptTexts(list), err, c.want)
		}
	}
}
