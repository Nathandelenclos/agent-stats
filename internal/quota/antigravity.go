package quota

import (
	"encoding/json"
	"errors"
	"io"
	"maps"
	"os"
	"regexp"
	"slices"
	"time"
)

const (
	antigravityProvider    = "Google Antigravity"
	antigravityMaxAge      = 15 * time.Minute
	maxAntigravitySnapshot = 3 * maxStatusInput
)

var antigravityBucketID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,79}$`)

// RecordAntigravityStatus keeps only usable quota windows from an Antigravity CLI status line.
func RecordAntigravityStatus(r io.Reader, path string, now time.Time) error {
	raw, err := io.ReadAll(io.LimitReader(r, maxStatusInput+1))
	if err != nil {
		return err
	}
	if len(raw) > maxStatusInput {
		return errors.New("Antigravity status-line payload is too large")
	}
	var input struct {
		Product string                     `json:"product"`
		Quota   map[string]json.RawMessage `json:"quota"`
	}
	if json.Unmarshal(raw, &input) != nil {
		return errors.New("invalid Antigravity status-line payload")
	}
	if input.Product != "antigravity" {
		return nil
	}
	var windows []Window
	for _, label := range slices.Sorted(maps.Keys(input.Quota)) {
		var bucket struct {
			Fraction *float64  `json:"remaining_fraction"`
			Reset    time.Time `json:"reset_time"`
			Disabled bool      `json:"disabled"`
		}
		if !antigravityBucketID.MatchString(label) || json.Unmarshal(input.Quota[label], &bucket) != nil ||
			bucket.Fraction == nil || bucket.Disabled || bucket.Reset.IsZero() {
			continue
		}
		fraction := *bucket.Fraction
		if fraction < 0 || fraction > 1 {
			continue
		}
		remaining := 100 * fraction
		if window, ok := validWindow(antigravityProvider, label, 100-remaining, bucket.Reset, now); ok {
			window.Remaining = remaining
			windows = append(windows, window)
		}
	}
	if len(windows) == 0 {
		return nil
	}
	return writeQuotaSnapshot(path, windows)
}

// ReadAntigravity returns current windows recorded by the Antigravity CLI status line.
func ReadAntigravity(path string, now time.Time) []Window {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxAntigravitySnapshot {
		return nil
	}
	file, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, maxAntigravitySnapshot+1))
	if err != nil || len(raw) > maxAntigravitySnapshot {
		return nil
	}
	var cached []struct {
		Window
		Remaining *float64 `json:"remaining"`
	}
	if json.Unmarshal(raw, &cached) != nil {
		return nil
	}
	var windows []Window
	for _, row := range cached {
		if row.Remaining == nil || *row.Remaining < 0 || *row.Remaining > 100 {
			continue
		}
		window := row.Window
		window.Remaining = *row.Remaining
		if window.Provider != antigravityProvider || !antigravityBucketID.MatchString(window.Label) ||
			window.Observed.After(now) || now.Sub(window.Observed) > antigravityMaxAge || window.ResetAt.IsZero() {
			continue
		}
		if _, ok := validWindow(window.Provider, window.Label, 100-window.Remaining, window.ResetAt, now); ok {
			windows = append(windows, window)
		}
	}
	return windows
}
