package main

import (
	"bytes"
	"io"
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
	if runtime.GOOS != "darwin" {
		t.Skip("the pseudo-terminal is opened with the BSD script(1)")
	}
	script, err := exec.LookPath("script")
	if err != nil {
		t.Skip("script(1) is not installed")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "agent-stats")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	// --no-sync: the test must not read the logs of whoever runs it
	args = append([]string{"--no-sync", "--db", filepath.Join(dir, "index.db")}, args...)
	run := `exec "$0" "$@"`
	if noStdin {
		run += ` </dev/null`
	}
	s := &session{done: make(chan error, 1)}
	s.cmd = exec.Command(script, append([]string{"-q", "/dev/null", "sh", "-c", run, bin}, args...)...)
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

const enterAltScreen = "\x1b[?1049h"

func TestInterfaceOpensOnATerminalEvenWithStdinRedirected(t *testing.T) {
	// Given stdout on a terminal and stdin redirected from /dev/null
	s := onATerminal(t, true)

	// When agent-stats starts without --json, then the interface opens (on the controlling terminal for its input)
	s.waitFor(t, enterAltScreen, 10*time.Second)
	s.waitFor(t, "agent-stats", 10*time.Second)

	// And it answers the keyboard: q quits
	s.in.Write([]byte("q"))
	if err := s.wait(t, 10*time.Second); err != nil {
		t.Errorf("the interface did not quit cleanly: %v", err)
	}
}
