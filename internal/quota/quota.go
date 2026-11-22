package quota

import (
	"context"
	"fmt"
	"math"
	"time"
)

// Window is an observed account limit for a single provider and period.
type Window struct {
	Provider  string    `json:"provider"`
	Label     string    `json:"label"`
	Remaining float64   `json:"remaining"`
	ResetAt   time.Time `json:"resetAt"`
	Observed  time.Time `json:"observedAt"`
}

type Reader func(context.Context) []Window

func validWindow(provider, label string, used float64, reset, observed time.Time) (Window, bool) {
	if math.IsNaN(used) || math.IsInf(used, 0) || used < 0 || used > 100 ||
		(!reset.IsZero() && !reset.After(observed)) {
		return Window{}, false
	}
	if _, err := reset.MarshalJSON(); err != nil {
		return Window{}, false
	}
	return Window{Provider: provider, Label: label, Remaining: 100 - used, ResetAt: reset, Observed: observed}, true
}

func resetTime(epoch int64) time.Time {
	if epoch == 0 {
		return time.Time{}
	}
	return time.Unix(epoch, 0)
}

func periodLabel(minutes int64) string {
	switch {
	case minutes < 1:
		return "Limit"
	case minutes%(60*24) == 0:
		return fmt.Sprintf("%d-day limit", minutes/(60*24))
	case minutes%60 == 0:
		return fmt.Sprintf("%d-hour limit", minutes/60)
	default:
		return fmt.Sprintf("%d-minute limit", minutes)
	}
}
