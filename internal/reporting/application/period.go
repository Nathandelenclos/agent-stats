package application

import (
	"errors"
	"time"
)

// earliestSince is the oldest period start a report covers: no agent log is older, and it keeps date arithmetic far from overflow.
var earliestSince = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)

// ErrSinceOutOfRange reports a period start that no report can cover: older than any agent log, or in the future.
var ErrSinceOutOfRange = errors.New("period start out of range")

// CheckSince reports whether a report can start at since (zero: all time) when the clock reads now.
func CheckSince(since, now time.Time) error {
	if !since.IsZero() && (since.Before(earliestSince) || since.After(now)) {
		return ErrSinceOutOfRange
	}
	return nil
}
