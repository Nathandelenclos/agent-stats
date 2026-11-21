package pricing

import (
	"math"
	"testing"
)

func TestLookupStripsProviderAndDate(t *testing.T) {
	tb := Table{"claude-x": {1, 2, 3, 4}}
	for _, m := range []string{"claude-x", "anthropic/claude-x", "Claude-X-20260101", "openai-codex/claude-x-20260101"} {
		if _, ok := tb.Lookup(m); !ok {
			t.Errorf("%s not found", m)
		}
	}
	if _, ok := tb.Lookup("other"); ok {
		t.Error("unknown model found")
	}
}

func TestCostBills1hCacheWritesAtTwiceInput(t *testing.T) {
	tb := Table{"m": {1, 10, 0.1, 1.25}}
	got, ok := tb.Cost("m", 100, 10, 1000, 30, 20)
	want := 100.0 + 100 + 100 + 10*1.25 + 20*2
	if !ok || math.Abs(got-want) > 1e-9 {
		t.Fatalf("cost = %v, want %v", got, want)
	}
}

func TestEmbeddedTableLoads(t *testing.T) {
	tb, err := Load("")
	if err != nil || len(tb) < 100 {
		t.Fatalf("embedded table: %d entries, err %v", len(tb), err)
	}
}
