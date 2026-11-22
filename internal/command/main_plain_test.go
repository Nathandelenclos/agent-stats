package command

import (
	"context"
	"flag"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	indexing "github.com/Nathandelenclos/agent-stats/internal/indexing/application"
)

func TestPlainAddsDailyUsageWithoutChangingOtherReportFormats(t *testing.T) {
	for _, tt := range []struct {
		name      string
		flags     []string
		wantDaily bool
		wantJSON  bool
	}{
		{name: "plain", flags: []string{"--plain"}, wantDaily: true},
		{name: "redirected report"},
		{name: "JSON also takes precedence over plain", flags: []string{"--plain", "--json"}, wantJSON: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			stdout, err := os.Create(filepath.Join(dir, "stdout.txt"))
			if err != nil {
				t.Fatal(err)
			}
			defer stdout.Close()
			priorStdout, priorArgs, priorFlags := os.Stdout, os.Args, flag.CommandLine
			defer func() {
				os.Stdout, os.Args, flag.CommandLine = priorStdout, priorArgs, priorFlags
			}()
			os.Stdout = stdout
			os.Args = append([]string{"agent-stats", "--no-sync", "--all", "--db", filepath.Join(dir, "index.db")}, tt.flags...)
			flag.CommandLine = flag.NewFlagSet("agent-stats", flag.ContinueOnError)
			if err := Run(); err != nil {
				t.Fatal(err)
			}
			if _, err := stdout.Seek(0, io.SeekStart); err != nil {
				t.Fatal(err)
			}
			out, err := io.ReadAll(stdout)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(out), "Daily usage") != tt.wantDaily ||
				strings.Contains(string(out), `"total"`) != tt.wantJSON ||
				strings.ContainsAny(string(out), "\x1b\r") {
				t.Errorf("unexpected static output:\n%q", out)
			}
		})
	}
}

func TestStaticIndexForReportSkipsProgress(t *testing.T) {
	called := false
	run := func(_ context.Context, progress func(indexing.IndexReport)) (indexing.IndexReport, error) {
		called = true
		if progress != nil {
			t.Error("the static index run has an animated progress callback")
		}
		return indexing.IndexReport{Indexed: 50}, nil
	}
	if err := indexForReport(run, nil); err != nil || !called {
		t.Errorf("static indexing did not finish: called %v, error %v", called, err)
	}
}
