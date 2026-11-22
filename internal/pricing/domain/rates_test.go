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

func TestLookupFindsAClaudeModelWrittenWithADottedVersion(t *testing.T) {
	// Given a list that knows claude-sonnet-4-5, and a model that is named claude-sonnet-4.5 by a gateway
	dashed, dotted := rates(t, RatesPerToken{Input: 0.1}), rates(t, RatesPerToken{Input: 0.2})
	l := PriceList{"claude-sonnet-4-5": dashed}

	// When its price is looked up, with or without a provider and a date
	for _, m := range []string{"claude-sonnet-4.5", "anthropic/claude-sonnet-4.5", "Claude-Sonnet-4.5-20250929"} {
		// Then the price of claude-sonnet-4-5 is found
		if r, ok := l.Lookup(m); !ok || r != dashed {
			t.Errorf("%s: %v %v", m, r, ok)
		}
	}

	// And a name the list holds as written wins over the dashed one
	l["claude-sonnet-4.5"] = dotted
	if r, ok := l.Lookup("claude-sonnet-4.5"); !ok || r != dotted {
		t.Errorf("exact name: %v %v", r, ok)
	}

	// And the versions of the models of other makers are not rewritten
	if _, ok := (PriceList{"gemini-2-5-pro": dashed}).Lookup("gemini-2.5-pro"); ok {
		t.Error("a dotted version of another maker was found as dashed")
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

func TestTheSameTokensCostTheSameOnEveryCPU(t *testing.T) {
	// Given a model's rates and the tokens of one request
	r := rates(t, RatesPerToken{Input: 3e-6, Output: 15e-6})
	tok := tokens(t, kernel.TokenCounts{Input: 1, Output: 5})

	// When the request is priced
	c, err := r.Cost(tok)

	// Then the cost is the sum of each priced part, rounded alike on arm64 and amd64
	// (a fused multiply-add on arm64 would give 7.8e-05)
	if err != nil || c.USD() != 7.800000000000001e-05 {
		t.Errorf("cost = %v %v, want 7.800000000000001e-05", c.USD(), err)
	}
}
