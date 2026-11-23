package quota

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAntigravityRecordsOnlyQuotaWindows(t *testing.T) {
	const payload = `{
		"product": "antigravity",
		"email": "private_email_marker",
		"transcript_path": "private_transcript_marker",
		"quota": {
			"gemini-weekly": {"remaining_fraction": 0.9378, "reset_time": "2026-07-06T07:50:32Z", "reset_in_seconds": 560580},
			"gemini-5-hour": {"remaining_fraction": 0, "reset_time": "2026-07-06T07:51:32Z"},
			"amount-only": {"remaining_amount": 100, "reset_time": "2026-07-06T07:50:32Z"},
			"disabled": {"remaining_fraction": 1, "reset_time": "2026-07-06T07:50:32Z", "disabled": true},
			"no-reset": {"remaining_fraction": 0.5, "reset_in_seconds": 60},
			"past-reset": {"remaining_fraction": 0.5, "reset_time": "2026-07-06T07:49:00Z"},
			"bad-reset": {"remaining_fraction": 1, "reset_time": "yesterday"},
			"bad-type": {"remaining_fraction": "1", "reset_time": "2026-07-06T07:50:32Z"},
			"above-range": {"remaining_fraction": 1.01, "reset_time": "2026-07-06T07:50:32Z"},
			"below-range": {"remaining_fraction": -1e-100, "reset_time": "2026-07-06T07:50:32Z"},
			"name with spaces": {"remaining_fraction": 1, "reset_time": "2026-07-06T07:50:32Z"}
		}
	}`
	path := filepath.Join(t.TempDir(), "agent-stats", "antigravity-quota.json")
	now := time.Date(2026, time.July, 6, 7, 49, 32, 0, time.UTC)
	if err := RecordAntigravityStatus(strings.NewReader(payload), path, now); err != nil {
		t.Fatal(err)
	}
	want := []Window{
		{Provider: "Google Antigravity", Label: "gemini-5-hour", Remaining: 0, ResetAt: now.Add(2 * time.Minute), Observed: now},
		{Provider: "Google Antigravity", Label: "gemini-weekly", Remaining: 93.78, ResetAt: now.Add(time.Minute), Observed: now},
	}
	assertWindows(t, ReadAntigravity(path, now), want)
	assertWindows(t, ReadAntigravity(path, now.Add(time.Minute)), want[:1])
	assertWindows(t, ReadAntigravity(path, now.Add(-time.Second)), nil)
	assertWindows(t, ReadAntigravity(path, now.Add(2*time.Minute)), nil)

	snapshot, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, unrelated := range []string{"private_email_marker", "private_transcript_marker", "reset_in_seconds", "remaining_amount", "disabled"} {
		if strings.Contains(string(snapshot), unrelated) {
			t.Errorf("snapshot contains %q", unrelated)
		}
	}
}

func TestAntigravityKeepsItsSnapshotUntilItExpires(t *testing.T) {
	path := filepath.Join(t.TempDir(), "antigravity-quota.json")
	now := time.Date(2026, time.July, 6, 7, 30, 0, 0, time.UTC)
	payload := `{"product":"antigravity","quota":{"gemini-weekly":{"remaining_fraction":1,"reset_time":"2026-07-06T09:00:00Z"}}}`
	if err := RecordAntigravityStatus(strings.NewReader(payload), path, now); err != nil {
		t.Fatal(err)
	}
	for _, payload := range []string{`{"product":"antigravity"}`, `{"product":"antigravity","quota":{}}`, `{"product":"another"}`} {
		if err := RecordAntigravityStatus(strings.NewReader(payload), path, now.Add(time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	for _, payload := range []string{`{"quota":`, strings.Repeat(" ", maxStatusInput+1)} {
		if err := RecordAntigravityStatus(strings.NewReader(payload), path, now.Add(time.Minute)); err == nil {
			t.Fatal("invalid status-line input should fail")
		}
	}
	want := []Window{{Provider: "Google Antigravity", Label: "gemini-weekly", Remaining: 100, ResetAt: now.Add(90 * time.Minute), Observed: now}}
	assertWindows(t, ReadAntigravity(path, now.Add(time.Minute)), want)
	assertWindows(t, ReadAntigravity(path, now.Add(16*time.Minute)), nil)
}

func TestAntigravitySkipsInvalidCachedPercentages(t *testing.T) {
	path := filepath.Join(t.TempDir(), "antigravity-quota.json")
	now := time.Date(2026, time.July, 6, 7, 30, 0, 0, time.UTC)
	const payload = `[
		{"provider":"Google Antigravity","label":"missing","observedAt":"2026-07-06T07:30:00Z","resetAt":"2026-07-06T09:00:00Z"},
		{"provider":"Google Antigravity","label":"null","remaining":null,"observedAt":"2026-07-06T07:30:00Z","resetAt":"2026-07-06T09:00:00Z"},
		{"provider":"Google Antigravity","label":"negative","remaining":-1e-100,"observedAt":"2026-07-06T07:30:00Z","resetAt":"2026-07-06T09:00:00Z"},
		{"provider":"Google Antigravity","label":"exhausted","remaining":0,"observedAt":"2026-07-06T07:30:00Z","resetAt":"2026-07-06T09:00:00Z"}
	]`
	if err := os.WriteFile(path, []byte(payload), 0600); err != nil {
		t.Fatal(err)
	}
	want := []Window{{Provider: "Google Antigravity", Label: "exhausted", ResetAt: now.Add(90 * time.Minute), Observed: now}}
	assertWindows(t, ReadAntigravity(path, now), want)
}
