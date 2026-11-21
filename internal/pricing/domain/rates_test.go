package domain

import (
	"errors"
	"math"
	"testing"

	"github.com/Nathandelenclos/agent-stats/internal/kernel"
)

func rates(t *testing.T, r RatesPerToken) Rates {
	t.Helper()
	out, err := NewRates(r)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func tokens(t *testing.T, c kernel.TokenCounts) kernel.Tokens {
	t.Helper()
	out, err := kernel.NewTokens(c)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestLookupStripsProviderAndDate(t *testing.T) {
	l := PriceList{"claude-x": rates(t, RatesPerToken{Input: 1})}
	for _, m := range []string{"claude-x", "anthropic/claude-x", "Claude-X-20260101", "openai-codex/claude-x-20260101"} {
		if _, ok := l.Lookup(m); !ok {
			t.Errorf("%s not found", m)
		}
	}
	if _, ok := l.Lookup("other"); ok {
		t.Error("unknown model found")
	}
}

func TestCostBills1hCacheWritesAtTwiceInput(t *testing.T) {
	r := rates(t, RatesPerToken{Input: 0.1, Output: 1, CacheRead: 0.01, CacheWrite: 0.125})
	got, err := r.Cost(tokens(t, kernel.TokenCounts{Input: 100, Output: 10, CacheRead: 1000, CacheWrite: 30, CacheWrite1h: 20}))
	want := 10.0 + 10 + 10 + 10*0.125 + 20*2*0.1
	if err != nil || math.Abs(got.USD()-want) > 1e-9 {
		t.Fatalf("cost = %v %v, want %v", got, err, want)
	}
}

func TestNewRatesRefusesOutOfBoundPrices(t *testing.T) {
	for _, bad := range []RatesPerToken{{Input: -1}, {Output: math.NaN()}, {CacheRead: math.Inf(1)}, {CacheWrite: 2}} {
		if _, err := NewRates(bad); !errors.Is(err, ErrInvalidRates) {
			t.Errorf("NewRates(%+v) = %v", bad, err)
		}
	}
}

func TestPriceListCost(t *testing.T) {
	l := PriceList{"m": rates(t, RatesPerToken{Input: 1})}
	if c, ok := l.Cost("m", tokens(t, kernel.TokenCounts{Input: 10})); !ok || c.USD() != 10 {
		t.Errorf("cost = %v %v", c, ok)
	}
	if _, ok := l.Cost("unknown", tokens(t, kernel.TokenCounts{Input: 10})); ok {
		t.Error("unknown model priced")
	}
	// Over a million dollars for one request is out of bounds: the request stays unpriced.
	if _, ok := l.Cost("m", tokens(t, kernel.TokenCounts{Input: kernel.MaxTokenCount})); ok {
		t.Error("out-of-bound cost accepted")
	}
}
