package textfmt

import (
	"testing"
	"time"
)

func TestNumber(t *testing.T) {
	for in, want := range map[int64]string{0: "0", 999: "999", 1000: "1 000", 1234567: "1 234 567", -1234: "-1 234"} {
		if got := Number(in); got != want {
			t.Errorf("Number(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestShorten(t *testing.T) {
	if got := Shorten("/very/long/path", 6); got != "…/path" {
		t.Errorf("Shorten = %q", got)
	}
	if got := Shorten("short", 6); got != "short" {
		t.Errorf("Shorten kept = %q", got)
	}
}

func TestPrintableNeutralisesControlCharacters(t *testing.T) {
	if got := Printable("mcp\x1b[31mred\ttab\nline é"); got != "mcp�[31mred�tab�line é" {
		t.Errorf("Printable = %q", got)
	}
}

func TestHead(t *testing.T) {
	for _, c := range []struct {
		in   string
		n    int
		want string
	}{{"abcdef", 4, "abc…"}, {"abc", 3, "abc"}, {"éèàùç", 3, "éè…"}, {"abc", 0, "…"}} {
		if got := Head(c.in, c.n); got != c.want {
			t.Errorf("Head(%q, %d) = %q, want %q", c.in, c.n, got, c.want)
		}
	}
}

func TestDuration(t *testing.T) {
	for in, want := range map[time.Duration]string{
		0: "0s", 999 * time.Millisecond: "0s", 45 * time.Second: "45s", 90 * time.Second: "1m", 12 * time.Minute: "12m",
		65 * time.Minute: "1h05m", 26 * time.Hour: "1d02h", 3*24*time.Hour + 30*time.Minute: "3d00h",
	} {
		if got := Duration(in); got != want {
			t.Errorf("Duration(%v) = %q, want %q", in, got, want)
		}
	}
}

func TestExcerptIsOneNeutralLine(t *testing.T) {
	// Given a prompt over several lines, with an escape sequence that would clear the screen
	in := "fix the\n\tbuild \x1b[2J  now"

	// When it is reduced to an excerpt
	// Then it is one line, spaced once, with the escape replaced
	if got, want := Excerpt(in, 80), "fix the build �[2J now"; got != want {
		t.Errorf("Excerpt = %q, want %q", got, want)
	}
	// And a long text is cut with an ellipsis
	if got := Excerpt("one two three four", 9); got != "one two …" {
		t.Errorf("cut = %q", got)
	}
}

func TestPlural(t *testing.T) {
	for n, want := range map[int]string{0: "0 sessions", 1: "1 session", 2: "2 sessions"} {
		if got := Plural(n, "session", "sessions"); got != want {
			t.Errorf("Plural(%d) = %q, want %q", n, got, want)
		}
	}
}
