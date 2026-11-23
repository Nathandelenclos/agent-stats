package quota

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"time"
)

const claudeMaxAge = 15 * time.Minute

type claudeLimit struct {
	Used  *float64 `json:"used_percentage"`
	Reset int64    `json:"resets_at"`
}

// RecordClaudeStatus keeps the two subscription windows from a status-line payload.
func RecordClaudeStatus(r io.Reader, path string, now time.Time) error {
	raw, err := io.ReadAll(io.LimitReader(r, maxStatusInput+1))
	if err != nil {
		return err
	}
	if len(raw) > maxStatusInput {
		return errors.New("Claude status-line payload is too large")
	}
	var input struct {
		Limits struct {
			Session claudeLimit `json:"five_hour"`
			Weekly  claudeLimit `json:"seven_day"`
		} `json:"rate_limits"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return errors.New("invalid Claude status-line payload")
	}
	windows := make([]Window, 0, 2)
	for _, row := range []struct {
		label string
		value claudeLimit
	}{
		{"5-hour limit", input.Limits.Session},
		{"7-day limit", input.Limits.Weekly},
	} {
		if row.value.Used != nil {
			if window, ok := validWindow("Claude Code", row.label, *row.value.Used, resetTime(row.value.Reset), now); ok {
				windows = append(windows, window)
			}
		}
	}
	// Claude can call the status line before a session's first response. Keep
	// any still-valid observation from another session in that case.
	if len(windows) == 0 {
		return nil
	}
	return writeQuotaSnapshot(path, windows)
}

func ReadClaude(path string, now time.Time) []Window {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	var cached []Window
	if json.NewDecoder(io.LimitReader(f, 4096)).Decode(&cached) != nil {
		return nil
	}
	out := make([]Window, 0, 2)
	for _, window := range cached {
		if window.Provider != "Claude Code" || (window.Label != "5-hour limit" && window.Label != "7-day limit") ||
			now.Before(window.Observed) || now.Sub(window.Observed) > claudeMaxAge {
			continue
		}
		if _, ok := validWindow(window.Provider, window.Label, 100-window.Remaining, window.ResetAt, now); ok {
			out = append(out, window)
		}
	}
	return out
}
