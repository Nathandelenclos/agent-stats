package textfmt

import "testing"

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
