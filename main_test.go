package main

import (
	"os"
	"runtime"
	"testing"
	"time"

	indexing "github.com/Nathandelenclos/agent-stats/internal/indexing/application"
)

func TestIndexWorkersKeepACoreForTheWriterAndTheInterface(t *testing.T) {
	defer func(n int) { runtime.GOMAXPROCS(n) }(runtime.GOMAXPROCS(0))
	for cpus, want := range map[int]int{1: 1, 2: 2, 4: 3, 8: 6, 10: 8} {
		runtime.GOMAXPROCS(cpus)
		if got := indexWorkers(); got != want {
			t.Errorf("%d CPUs: %d workers, want %d", cpus, got, want)
		}
	}
}

func TestParseSince(t *testing.T) {
	now := time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)
	for in, want := range map[string]time.Time{
		"":    {},
		"7d":  now.AddDate(0, 0, -7),
		"12h": now.Add(-12 * time.Hour),
	} {
		if got, err := parseSince(in, now); err != nil || !got.Equal(want) {
			t.Errorf("parseSince(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	if got, err := parseSince("2026-01-02", now); err != nil || !got.Equal(time.Date(2026, 1, 2, 0, 0, 0, 0, time.Local)) {
		t.Errorf("date: %v %v", got, err)
	}
	for _, bad := range []string{"yesterday", "7x", "-"} {
		if _, err := parseSince(bad, now); err == nil {
			t.Errorf("parseSince(%q) accepted", bad)
		}
	}
}

func TestInteractiveOnlyOnATerminalWithoutJSON(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()
	// Given stdout on a pipe (not a terminal), when no flag is set, then the plain report is kept.
	if interactive(false, w) {
		t.Error("a pipe was taken for a terminal")
	}
	if interactive(true, w) {
		t.Error("--json opened the interface")
	}
}

func TestPlainReportWhenNoKeyboardCanBeRead(t *testing.T) {
	// Given stdout on a terminal but no keyboard to read (no controlling terminal)
	noKeyboard := func() bool { return false }

	// When agent-stats starts without --json
	// Then the plain report is shown instead of an interface that could not start
	if showInterface(false, true, noKeyboard) {
		t.Error("the interface was chosen without a keyboard")
	}
}

func TestInterfaceWhenAKeyboardCanBeRead(t *testing.T) {
	// Given stdout on a terminal and a keyboard to read
	keyboard := func() bool { return true }

	// When agent-stats starts without --json
	// Then the interface opens
	if !showInterface(false, true, keyboard) {
		t.Error("the plain report was chosen on a terminal with a keyboard")
	}
}

func TestSyncNoteSaysWhatWasSkipped(t *testing.T) {
	for _, c := range []struct {
		rep  indexing.IndexReport
		want string
	}{
		{indexing.IndexReport{Indexed: 3}, ""},
		{indexing.IndexReport{Errors: []string{"a", "b"}}, "skipped files: 2"},
		{indexing.IndexReport{Errors: []string{"a"}, SkippedLines: 4}, "skipped files: 1 · skipped log lines: 4"},
	} {
		if got := syncNote(c.rep); got != c.want {
			t.Errorf("syncNote(%+v) = %q, want %q", c.rep, got, c.want)
		}
	}
}
