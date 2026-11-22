package application

import (
	"errors"
	"testing"
	"time"
)

func TestAReportCanStartWhereAnAgentLogCanBeAndNotInTheFuture(t *testing.T) {
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		name  string
		since time.Time
		ok    bool
	}{
		{"all time", time.Time{}, true},
		{"last week", now.AddDate(0, 0, -7), true},
		{"just now", now, true},
		{"the first day an agent log can hold", time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC), true},
		{"before any agent log", time.Date(1999, 12, 31, 0, 0, 0, 0, time.UTC), false},
		{"in the future", now.Add(time.Second), false},
	} {
		t.Run(c.name, func(t *testing.T) {
			// Given a period start
			// When it is checked against the clock
			err := CheckSince(c.since, now)

			// Then it is accepted only when a report can cover it
			if (err == nil) != c.ok {
				t.Errorf("CheckSince(%v) = %v, want ok=%v", c.since, err, c.ok)
			}
			if err != nil && !errors.Is(err, ErrSinceOutOfRange) {
				t.Errorf("error = %v, want ErrSinceOutOfRange", err)
			}
		})
	}
}
