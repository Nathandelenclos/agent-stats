package main

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	indexing "github.com/Nathandelenclos/agent-stats/internal/indexing/application"
	"github.com/Nathandelenclos/agent-stats/internal/indexing/domain"
	"github.com/Nathandelenclos/agent-stats/internal/indexing/infra/sqlite"
	"github.com/Nathandelenclos/agent-stats/internal/kernel"
)

// seedSession indexes one session with a request and two prompts, the second one angry, and returns the index path.
func seedSession(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "index.db")
	w, err := sqlite.Open(sqlite.Options{Path: path, User: "me@host"})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	at := time.Now().Add(-time.Hour)
	id, _ := domain.NewSessionID(kernel.Claude, "s1")
	tokens, _ := kernel.NewTokens(kernel.TokenCounts{Input: 1000, Output: 100})
	cost, _ := kernel.NewCost(2)
	prompts := []domain.Prompt{{Key: "p1", At: at, Text: "fix the build"}, {Key: "p2", At: at, Text: "WHY IS IT STILL BROKEN??"}}
	err = w.Save(indexing.LogFile{Path: "/logs/s1.jsonl", Harness: kernel.Claude}, indexing.IndexedTranscript{
		Transcript: domain.Transcript{
			Session:  domain.Session{ID: id, Harness: kernel.Claude, AgentType: domain.MainAgent, Title: "fix build", Folder: "/repo", Start: at, End: at.Add(time.Minute)},
			Requests: []domain.Request{{Key: "m1", At: at, Model: "big-model", Tokens: tokens}},
			Prompts:  prompts,
		},
		Costs:   []indexing.PricedCost{{Cost: cost, Priced: true}},
		Signals: domain.AnalyzeSession(prompts),
	})
	if err != nil {
		t.Fatal(err)
	}
	return path
}

// waitForNew returns once the terminal output received after offset holds want.
func (s *session) waitForNew(t *testing.T, offset int, want string) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if out := s.output(); len(out) > offset && strings.Contains(out[offset:], want) {
			return
		}
	}
	t.Fatalf("no %q on the terminal after the last key; output since then:\n%q", want, s.output()[offset:])
}

// press sends keys and returns how much the terminal had received before them.
func (s *session) press(t *testing.T, keys string) int {
	t.Helper()
	n := len(s.output())
	if _, err := s.in.Write([]byte(keys)); err != nil {
		t.Fatal(err)
	}
	return n
}

// The mode label draws the active mode in reverse video, between brackets.
const (
	usageModeOn    = "\x1b[1;7m[Usage]"
	sessionsModeOn = "\x1b[1;7m[Sessions]"
	promptsModeOn  = "\x1b[1;7m[Prompts]"
)

func TestSessionsOnATerminal(t *testing.T) {
	// Given an index holding one session, and the interface open on a terminal
	s := onATerminal(t, false, "--db", seedSession(t))
	s.waitFor(t, usageModeOn, 10*time.Second)

	// When the user presses m, the sessions appear under their own label
	n := s.press(t, "m")
	s.waitForNew(t, n, sessionsModeOn)
	s.waitForNew(t, n, "fix build")

	// When the user presses Enter, the session's prompts appear with their signals
	n = s.press(t, "\r")
	s.waitForNew(t, n, "fix the build")
	s.waitForNew(t, n, "WHY IS IT STILL BROKEN??")
	s.waitForNew(t, n, "yelling")

	// When the user presses Esc, the list comes back
	n = s.press(t, "\x1b")
	s.waitForNew(t, n, "enter open")

	// And m leads on to the review of the flagged prompts, then back to the usage report
	n = s.press(t, "m")
	s.waitForNew(t, n, promptsModeOn)
	s.waitForNew(t, n, "1 flagged prompt in 1 session")
	s.waitForNew(t, n, "yelling")
	n = s.press(t, "m")
	s.waitForNew(t, n, usageModeOn)
	s.quit(t)
}
