package usage

import (
	"errors"
	"math"
	"testing"
	"time"
)

func rates(t *testing.T, input, output, cacheRead, cacheWrite float64) Rates {
	t.Helper()
	r, err := NewRates(input, output, cacheRead, cacheWrite)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestLookupStripsProviderAndDate(t *testing.T) {
	l := PriceList{"claude-x": rates(t, 1, 2, 3, 4)}
	for _, m := range []string{"claude-x", "anthropic/claude-x", "Claude-X-20260101", "openai-codex/claude-x-20260101"} {
		if _, ok := l.Lookup(m); !ok {
			t.Errorf("%s not found", m)
		}
	}
	if _, ok := l.Lookup("other"); ok {
		t.Error("unknown model found")
	}
}

func TestCostBills1hCacheWritesAtTwiceInput(t *testing.T) {
	got := rates(t, 1, 10, 0.1, 1.25).Cost(Tokens{Input: 100, Output: 10, CacheRead: 1000, CacheWrite: 30, CacheWrite1h: 20})
	want := 100.0 + 100 + 100 + 10*1.25 + 20*2
	if math.Abs(float64(got)-want) > 1e-9 {
		t.Fatalf("cost = %v, want %v", got, want)
	}
}

func TestNewRatesRefusesNegativeAndNonFinite(t *testing.T) {
	for _, bad := range [][4]float64{{-1, 0, 0, 0}, {0, math.NaN(), 0, 0}, {0, 0, math.Inf(1), 0}} {
		if _, err := NewRates(bad[0], bad[1], bad[2], bad[3]); !errors.Is(err, ErrInvalidRates) {
			t.Errorf("NewRates%v = %v", bad, err)
		}
	}
}

func TestPriceTrustsOnlyAPositiveLoggedCost(t *testing.T) {
	l := PriceList{"m": rates(t, 1, 0, 0, 0)}
	if c, ok := l.Price(Request{Model: "m", LoggedCost: 0.5, Tokens: Tokens{Input: 10}}); !ok || c != 0.5 {
		t.Errorf("logged cost: %v %v", c, ok)
	}
	// A logged 0 is priced from the list: oh-my-pi writes 0 for tokens it did bill.
	if c, ok := l.Price(Request{Model: "m", Tokens: Tokens{Input: 10}}); !ok || c != 10 {
		t.Errorf("zero logged cost: %v %v", c, ok)
	}
	if _, ok := l.Price(Request{Model: "unknown"}); ok {
		t.Error("unknown model priced")
	}
}

func TestParseHarness(t *testing.T) {
	if h, err := ParseHarness("omp"); err != nil || h != OMP {
		t.Errorf("omp: %v %v", h, err)
	}
	for _, bad := range []string{"", "Claude", "codex ", "x' OR 1=1"} {
		if _, err := ParseHarness(bad); !errors.Is(err, ErrUnknownHarness) {
			t.Errorf("ParseHarness(%q) = %v", bad, err)
		}
	}
}

func TestTitleFromFirstPrompt(t *testing.T) {
	long := Transcript{Prompts: []Prompt{{Text: "  fix\n the   " + string(make([]rune, 100))}}}
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
