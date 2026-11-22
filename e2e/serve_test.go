package e2e

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestServeRefusesWhatWouldExposeTheIndexOrIsMalformed(t *testing.T) {
	db := filepath.Join(t.TempDir(), "index.db")
	for _, c := range []struct {
		name string
		args []string
		want string
	}{
		{"a non-loopback address", []string{"--db", db, "--addr", "0.0.0.0:7373"}, "--allow-remote"},
		{"an address without a port", []string{"--db", db, "--addr", "127.0.0.1"}, "host:port"},
		{"an unknown harness", []string{"--db", db, "--harness", "vim"}, "unknown harness"},
		{"an invalid period", []string{"--db", db, "--since", "yesterday"}, "invalid --since"},
		{"a period before any agent log", []string{"--db", db, "--since", "1999-01-01"}, "invalid --since"},
		{"a period in the future", []string{"--db", db, "--since", "2999-01-01"}, "invalid --since"},
		{"a stray argument", []string{"--db", db, "7373"}, "takes no argument"},
	} {
		t.Run(c.name, func(t *testing.T) {
			// Given a serve command line that is unsafe or malformed
			// When it runs
			_, stderr, err := runCLI(t, "", append([]string{"serve"}, c.args...)...)

			// Then it stops before listening or touching the index, saying why
			if err == nil || !strings.Contains(stderr, c.want) {
				t.Errorf("serve %v: %v, stderr %q; want an error mentioning %q", c.args, err, stderr, c.want)
			}
		})
	}
	if _, err := os.Stat(db); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the index was created by a refused command: %v", err)
	}
}
