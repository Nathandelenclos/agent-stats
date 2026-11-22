package quota

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestClaudeRecordsOnlySubscriptionWindows(t *testing.T) {
	const payload = `{
		"model": {"description":"private_model_marker"},
		"workspace":{"current_dir":"private_workspace_marker"},
		"rate_limits":{
			"five_hour":{"used_percentage":0,"resets_at":1700000600},
			"seven_day":{"used_percentage":100},
			"other":{"used_percentage":55,"resets_at":1700000700}
		}
	}`
	path := filepath.Join(t.TempDir(), "agent-stats", "claude-quota.json")
	now := time.Unix(1700000000, 0)
	if err := RecordClaudeStatus(strings.NewReader(payload), path, now); err != nil {
		t.Fatal(err)
	}

	want := []Window{
		{Provider: "Claude Code", Label: "5-hour limit", Remaining: 100, ResetAt: time.Unix(1700000600, 0), Observed: now},
		{Provider: "Claude Code", Label: "7-day limit", Remaining: 0, Observed: now},
	}
	assertWindows(t, ReadClaude(path, now), want)

	snapshot, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{"private_model_marker", "private_workspace_marker", "other", "rate_limits"} {
		if bytes.Contains(snapshot, []byte(private)) {
			t.Errorf("snapshot kept an unrelated field or value: %q", private)
		}
	}
	file, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && file.Mode().Perm() != 0600 {
		t.Errorf("snapshot permissions = %o, want 600", file.Mode().Perm())
	}
}

func TestClaudeFiltersExpiredAndStaleObservations(t *testing.T) {
	const payload = `{"rate_limits":{
		"five_hour":{"used_percentage":40,"resets_at":1700000060},
		"seven_day":{"used_percentage":52.5,"resets_at":1700003600}
	}}`
	path := filepath.Join(t.TempDir(), "claude-quota.json")
	now := time.Unix(1700000000, 0)
	if err := RecordClaudeStatus(strings.NewReader(payload), path, now); err != nil {
		t.Fatal(err)
	}
	want := []Window{
		{Provider: "Claude Code", Label: "7-day limit", Remaining: 47.5, ResetAt: time.Unix(1700003600, 0), Observed: now},
	}
	assertWindows(t, ReadClaude(path, now.Add(time.Minute)), want)
	assertWindows(t, ReadClaude(path, now.Add(16*time.Minute)), nil)
}

func TestClaudeKeepsOnlyAnObservedWindowAndIgnoresAnEmptyUpdate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "claude-quota.json")
	now := time.Unix(1700000000, 0)
	const payload = `{"rate_limits":{"seven_day":{"used_percentage":25}}}`
	if err := RecordClaudeStatus(strings.NewReader(payload), path, now); err != nil {
		t.Fatal(err)
	}
	if err := RecordClaudeStatus(strings.NewReader(`{"rate_limits":{}}`), path, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	want := []Window{{Provider: "Claude Code", Label: "7-day limit", Remaining: 75, Observed: now}}
	assertWindows(t, ReadClaude(path, now.Add(time.Minute)), want)
}

func TestClaudeRejectsMalformedUpdatesWithoutReplacingTheSnapshot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "claude-quota.json")
	now := time.Unix(1700000000, 0)
	const payload = `{"rate_limits":{"five_hour":{"used_percentage":20}}}`
	if err := RecordClaudeStatus(strings.NewReader(payload), path, now); err != nil {
		t.Fatal(err)
	}
	if err := RecordClaudeStatus(strings.NewReader(`{"rate_limits":`), path, now.Add(time.Minute)); err == nil {
		t.Fatal("malformed status-line payload should fail")
	}
	want := []Window{{Provider: "Claude Code", Label: "5-hour limit", Remaining: 80, Observed: now}}
	assertWindows(t, ReadClaude(path, now.Add(time.Minute)), want)
}
