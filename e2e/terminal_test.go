package e2e

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// session is the built program running with its terminal on a pseudo-terminal, through BSD script(1).
type session struct {
	cmd  *exec.Cmd
	in   io.WriteCloser
	mu   sync.Mutex
	out  bytes.Buffer
	done chan error
}

// onATerminal runs the program with args; stdin is redirected from /dev/null when noStdin is set,
// as in `agent-stats </dev/null`, while stdout stays the terminal.
func onATerminal(t *testing.T, noStdin bool, args ...string) *session {
	t.Helper()
	return onATerminalIn(t, "", noStdin, args...)
}

// onATerminalIn is onATerminal with the program started in the folder dir (the test's own folder when empty).
func onATerminalIn(t *testing.T, dir string, noStdin bool, args ...string) *session {
	t.Helper()
	if runtime.GOOS != "darwin" {
		t.Skip("the pseudo-terminal is opened with the BSD script(1)")
	}
	script, err := exec.LookPath("script")
	if err != nil {
		t.Skip("script(1) is not installed")
	}
	work := t.TempDir()
	// --no-sync: the test must not read the logs of whoever runs it
	args = append([]string{"--no-sync", "--db", filepath.Join(work, "index.db")}, args...)
	run := `exec "$0" "$@"`
	if noStdin {
		run += ` </dev/null`
	}
	s := &session{done: make(chan error, 1)}
	s.cmd = exec.Command(script, append([]string{"-q", "/dev/null", "sh", "-c", run, binary}, args...)...)
	s.cmd.Dir = dir
	for _, kv := range os.Environ() { // CI, NO_COLOR and CLICOLOR* would switch the colours off, TERM must allow them
		if !hasAnyPrefix(kv, "CI=", "TERM=", "NO_COLOR=", "CLICOLOR=", "CLICOLOR_FORCE=") {
			s.cmd.Env = append(s.cmd.Env, kv)
		}
	}
	s.cmd.Env = append(s.cmd.Env, "TERM=xterm-256color")
	if s.in, err = s.cmd.StdinPipe(); err != nil {
		t.Fatal(err)
	}
	s.cmd.Stdout = (*lockedWriter)(s)
	if err := s.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() { s.done <- s.cmd.Wait() }()
	t.Cleanup(func() { s.cmd.Process.Kill() })
	return s
}

type lockedWriter session

func (w *lockedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.out.Write(p)
}

func (s *session) output() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.out.String()
}

// waitFor returns once the terminal output holds want, or fails after the timeout.
func (s *session) waitFor(t *testing.T, want string, timeout time.Duration) {
	t.Helper()
	for deadline := time.Now().Add(timeout); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if strings.Contains(s.output(), want) {
			return
		}
	}
	t.Fatalf("no %q on the terminal after %v; output:\n%q", want, timeout, s.output())
}

// wait returns the program's end, or fails after the timeout.
func (s *session) wait(t *testing.T, timeout time.Duration) error {
	t.Helper()
	select {
	case err := <-s.done:
		return err
	case <-time.After(timeout):
		t.Fatalf("the program is still running after %v; output:\n%q", timeout, s.output())
		return nil
	}
}

// What the terminal receives, named so that the scenarios below read as behaviour.
const (
	openInterface       = "\x1b[?1049h" // the interface takes the whole screen
	askBackgroundColour = "\x1b]11;?"   // what the colour libraries send to a terminal when they load
	askCursorPosition   = "\x1b[6n"
)

// asksTheTerminal reports whether the program sent a question the terminal is expected to answer.
func asksTheTerminal(output string) bool {
	return strings.Contains(output, askBackgroundColour) || strings.Contains(output, askCursorPosition)
}

// quit presses q and waits for the interface to end.
func (s *session) quit(t *testing.T) {
	t.Helper()
	s.in.Write([]byte("q"))
	if err := s.wait(t, 10*time.Second); err != nil {
		t.Errorf("the interface did not quit cleanly: %v", err)
	}
}

func TestInterfaceOpensOnATerminalEvenWithStdinRedirected(t *testing.T) {
	// Given stdout on a terminal and stdin redirected from /dev/null
	s := onATerminal(t, true)

	// When agent-stats starts without --json
	// Then the interface opens, and the keyboard of the controlling terminal drives it
	s.waitFor(t, openInterface, 10*time.Second)
	s.waitFor(t, "agent-stats", 10*time.Second)
	s.quit(t)
}

func TestJSONOnATerminalDoesNotAskTheTerminalAnything(t *testing.T) {
	// Given a terminal that never answers a question
	// When the report is asked as JSON
	start := time.Now()
	s := onATerminal(t, false, "--json")
	if err := s.wait(t, 20*time.Second); err != nil {
		t.Fatalf("agent-stats --json: %v\n%q", err, s.output())
	}

	// Then the report is printed, without a question to the terminal and without waiting for an answer
	if out := s.output(); asksTheTerminal(out) || !strings.Contains(out, `"total"`) {
		t.Errorf("the terminal was queried or the report is missing: %q", out)
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Errorf("--json took %v on a terminal that does not answer", d)
	}
}

func TestInterfaceDoesNotWaitForAnAnswerFromTheTerminal(t *testing.T) {
	// Given a terminal that never answers a question
	// When the interface opens
	s := onATerminal(t, false)

	// Then it is on screen within two seconds, without a question to the terminal
	s.waitFor(t, "agent-stats", 2*time.Second)
	if asksTheTerminal(s.output()) {
		t.Errorf("the terminal was queried: %q", s.output())
	}
	s.quit(t)
}

func TestInterfaceHighlightsTheActiveTab(t *testing.T) {
	// Given a terminal that supports colours (CI is cleared and TERM set by the session)
	// When the interface opens
	s := onATerminal(t, false)

	// When the person opens Usage from the dashboard, its mode label is highlighted.
	s.waitFor(t, "[Dashboard]", 2*time.Second)
	n := s.press(t, "\r")
	s.waitForNew(t, n, usageModeOn)
	s.quit(t)
}

func hasAnyPrefix(s string, prefixes ...string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}
