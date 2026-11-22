package e2e

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"testing"
)

var binary string

func TestMain(m *testing.M) {
	os.Exit(runTests(m))
}

func runTests(m *testing.M) int {
	dir, err := os.MkdirTemp("", "agent-stats-e2e-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer os.RemoveAll(dir)

	binary = filepath.Join(dir, "agent-stats")
	args := []string{"build", "-p", "1", "-o", binary}
	// CI tests the web build with the race detector, so the CLI must use those flags too.
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range info.Settings {
			switch setting.Key {
			case "-race", "-tags":
				args = append(args, setting.Key+"="+setting.Value)
			}
		}
	}
	if out, err := exec.Command("go", append(args, "..")...).CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "build agent-stats: %v\n%s", err, out)
		return 1
	}
	return m.Run()
}

func runCLI(t *testing.T, dir string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), binary, args...)
	cmd.Dir = dir
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	err = cmd.Run()
	return out.String(), errOut.String(), err
}

func fixtureEnv(t *testing.T, home string) {
	t.Helper()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	for _, name := range []string{"CLAUDE_CONFIG_DIR", "CODEX_HOME", "XDG_DATA_HOME", "OPENCODE_DB", "KILO_DB", "GEMINI_CLI_HOME", "COPILOT_HOME", "GROK_HOME",
		"XDG_CONFIG_HOME", "APPDATA", "LOCALAPPDATA", "FLATPAK_XDG_DATA_HOME",
		"CLINE_DIR", "CLINE_DATA_DIR", "CLINE_SESSION_DATA_DIR"} {
		t.Setenv(name, "")
	}
}

// approve compares got with testdata/name; on a difference it writes the received file for review.
func approve(t *testing.T, name, received string, got []byte) {
	t.Helper()
	approved, err := os.ReadFile(filepath.Join("..", "testdata", name))
	if err != nil || !bytes.Equal(approved, got) {
		if werr := os.WriteFile(filepath.Join("..", "testdata", received), got, 0o644); werr != nil {
			t.Fatal(werr)
		}
		t.Fatalf("output differs from testdata/%s (%v); see testdata/%s", name, err, received)
	}
	os.Remove(filepath.Join("..", "testdata", received))
}
