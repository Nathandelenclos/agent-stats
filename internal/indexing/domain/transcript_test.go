package domain

import (
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/Nathandelenclos/agent-stats/internal/kernel"
)

func TestSessionIDs(t *testing.T) {
	id, err := NewSessionID(kernel.Claude, "s1")
	if err != nil || id.String() != "claude:s1" {
		t.Fatalf("id = %q %v", id, err)
	}
	if sub, err := id.Subagent("agent-a1"); err != nil || sub.String() != "claude:s1/agent-a1" {
		t.Errorf("subagent = %q %v", sub, err)
	}
	if _, err := NewSessionID(kernel.Codex, ""); !errors.Is(err, ErrEmptySessionID) {
		t.Errorf("empty native id accepted: %v", err)
	}
	if _, err := id.Subagent(""); !errors.Is(err, ErrEmptySessionID) {
		t.Errorf("empty subagent name accepted: %v", err)
	}
	if _, err := (SessionID{}).Subagent("x"); !errors.Is(err, ErrEmptySessionID) {
		t.Errorf("subagent of no session accepted: %v", err)
	}
	if _, err := NewSessionID(kernel.Harness{}, "x"); !errors.Is(err, ErrEmptySessionID) {
		t.Errorf("session of no harness accepted: %v", err)
	}
}

func TestRequestCostTrustsOnlyAPositiveLoggedCost(t *testing.T) {
	listed, _ := kernel.NewCost(10)
	logged, _ := kernel.NewCost(0.5)
	if c, ok := (Request{LoggedCost: logged}).Cost(listed, true); !ok || c.USD() != 0.5 {
		t.Errorf("logged cost: %v %v", c, ok)
	}
	// A logged 0 is priced from the list: oh-my-pi writes 0 for tokens it did bill.
	if c, ok := (Request{}).Cost(listed, true); !ok || c.USD() != 10 {
		t.Errorf("zero logged cost: %v %v", c, ok)
	}
	if _, ok := (Request{}).Cost(kernel.Cost{}, false); ok {
		t.Error("unknown price reported as priced")
	}
}

func TestParseAgentType(t *testing.T) {
	for _, ok := range []string{"Explore", "code-reviewer", "grace-review-high", "general purpose"} {
		if _, valid := ParseAgentType(ok); !valid {
			t.Errorf("%q refused", ok)
		}
	}
	for _, bad := range []string{"", " lead", "<b>", strings.Repeat("a", 65), "a\nb"} {
		if _, valid := ParseAgentType(bad); valid {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestTitleFromFirstPrompt(t *testing.T) {
	long := Transcript{Prompts: []Prompt{{Text: "  fix\n the   " + strings.Repeat("x", 100)}}}
	long.TitleFromFirstPrompt()
	if r := []rune(long.Session.Title); len(r) != 81 || string(r[:8]) != "fix the " || r[80] != '…' {
		t.Errorf("title = %q", long.Session.Title)
	}
	named := Transcript{Session: Session{Title: "kept"}, Prompts: []Prompt{{Text: "x"}}}
	named.TitleFromFirstPrompt()
	if named.Session.Title != "kept" {
		t.Errorf("title overwritten: %q", named.Session.Title)
	}
}

func TestNewPromptRefusesBlankText(t *testing.T) {
	if _, ok := NewPrompt("k", time.Time{}, " \n\t"); ok {
		t.Error("blank prompt accepted")
	}
	if p, ok := NewPrompt("k", time.Time{}, "hi"); !ok || p.Text != "hi" {
		t.Errorf("prompt = %+v %v", p, ok)
	}
}

func TestSessionSpanIgnoresUnknownTimes(t *testing.T) {
	var s Session
	t1, t2 := time.UnixMilli(2000), time.UnixMilli(1000)
	s.Span(t1)
	s.Span(time.Time{})
	s.Span(t2)
	if !s.Start.Equal(t2) || !s.End.Equal(t1) {
		t.Errorf("span = %v..%v", s.Start, s.End)
	}
}

func TestAnalyze(t *testing.T) {
	if s := Analyze("Update the API and the JSON schema"); s.Yelling != 0 || s.Profanity != 0 || s.Blame != 0 {
		t.Errorf("lone acronyms: %+v", s)
	}
	if s := Analyze("putain, tu as cassé le build"); s.Profanity != 1 || s.Blame != 1 {
		t.Errorf("profanity and blame: %+v", s)
	}
	if s := Analyze("POURQUOI ÇA CASSE ENCORE ??"); s.Yelling != 2 || s.Anguish < 2 {
		t.Errorf("yelling: %+v", s)
	}
}

func TestAnalyzeSessionFlagsRepeatedPrompts(t *testing.T) {
	got := AnalyzeSession([]Prompt{{Text: "Fix  the bug"}, {Text: "other"}, {Text: "fix the\nBUG"}})
	if got[0].Repeated || got[1].Repeated || !got[2].Repeated {
		t.Errorf("repeated = %v %v %v", got[0].Repeated, got[1].Repeated, got[2].Repeated)
	}
}

func TestLoggedDurationBounds(t *testing.T) {
	for ms, want := range map[float64]time.Duration{
		1200.5:      1200500 * time.Microsecond,
		-5:          0,
		0:           0,
		math.NaN():  0,
		math.Inf(1): 0,
		float64(8 * 24 * time.Hour / time.Millisecond): 0,
	} {
		if got := LoggedDuration(ms).Value(); got != want {
			t.Errorf("LoggedDuration(%v) = %v, want %v", ms, got, want)
		}
	}
}

func TestElapsedBounds(t *testing.T) {
	t0 := time.UnixMilli(1767261600000)
	for name, c := range map[string]struct {
		start, end time.Time
		want       time.Duration
	}{
		"normal":        {t0, t0.Add(1500 * time.Millisecond), 1500 * time.Millisecond},
		"missing start": {time.Time{}, t0, 0},
		"missing end":   {t0, time.Time{}, 0},
		"backwards":     {t0, t0.Add(-time.Second), 0},
		"over a week":   {t0, t0.Add(8 * 24 * time.Hour), 0},
		"year 9999":     {t0, time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC), 0},
	} {
		if got := Elapsed(c.start, c.end).Value(); got != c.want {
			t.Errorf("%s: Elapsed = %v, want %v", name, got, c.want)
		}
	}
}

func TestNewDurationKeepsOnlyBoundedValues(t *testing.T) {
	if d := NewDuration(time.Second); !d.Known() || d.Value() != time.Second {
		t.Errorf("1s = %v %v", d.Value(), d.Known())
	}
	for _, bad := range []time.Duration{0, -time.Second, MaxLoggedDuration + 1} {
		if d := NewDuration(bad); d.Known() || d != (Duration{}) {
			t.Errorf("NewDuration(%v) kept", bad)
		}
	}
}

func TestParseSkillNameAcceptsThePlainAndPluginQualifiedNamesAHarnessLogs(t *testing.T) {
	// Given the names a harness logs for the skills it loads
	for _, name := range []string{"review", "demo:audit", "claude-api", "a.b_c d"} {
		// When one is read, then it is accepted
		if _, valid := ParseSkillName(name); !valid {
			t.Errorf("ParseSkillName(%q) refused", name)
		}
	}
}

func TestParseSkillNameRefusesWhatIsNotTheNameOfASkill(t *testing.T) {
	// Given text a log could hold in the place of a skill name: nothing, a leading dash, a control sequence, a line break, a very long run
	for _, text := range []string{"", "-lead", "bad\x1b[2J", "new\nline", strings.Repeat("x", 129)} {
		// When it is read, then it is refused
		if _, valid := ParseSkillName(text); valid {
			t.Errorf("ParseSkillName(%q) accepted", text)
		}
	}
}
