package sqlite

import (
	"testing"
	"time"

	"github.com/Nathandelenclos/agent-stats/internal/quota"
)

func TestQuotaHistoryPersistsDistinctObservations(t *testing.T) {
	s := open(t)
	now := time.UnixMilli(1767261600000)
	first := quota.Window{Provider: "Codex", Label: "Weekly", Remaining: 80, Observed: now.Add(-time.Hour)}
	last := quota.Window{Provider: "Codex", Label: "Weekly", Remaining: 65, Observed: now, ResetAt: now.Add(time.Hour)}
	if err := s.RecordQuotaWindows(t.Context(), []quota.Window{first, last, last}); err != nil {
		t.Fatal(err)
	}
	path := pathOf(t, s)
	s.Close()
	got, err := reopen(t, path).QuotaHistory(t.Context(), now.Add(-2*time.Hour), 100)
	if err != nil || len(got) != 2 || got[0] != last || got[1] != first {
		t.Fatalf("history = %+v, %v", got, err)
	}
}
