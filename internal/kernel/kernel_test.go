package kernel

import (
	"errors"
	"math"
	"testing"
)

func TestParseHarness(t *testing.T) {
	if h, err := ParseHarness("omp"); err != nil || h != OMP {
		t.Errorf("omp: %v %v", h, err)
	}
	if h, _ := ParseHarness("claude"); h != Claude || h.String() != "claude" || h.IsZero() {
		t.Errorf("claude = %#v", h)
	}
	for _, bad := range []string{"", "Claude", "codex ", "x' OR 1=1"} {
		if _, err := ParseHarness(bad); !errors.Is(err, ErrUnknownHarness) {
			t.Errorf("ParseHarness(%q) = %v", bad, err)
		}
	}
}

func TestNewTokens(t *testing.T) {
	ok, err := NewTokens(TokenCounts{Input: 5, Output: 7, CacheRead: 100, CacheWrite: 20, CacheWrite1h: 20, Reasoning: 3})
	if err != nil || ok.Counts() != (TokenCounts{Input: 5, Output: 7, CacheRead: 100, CacheWrite: 20, CacheWrite1h: 20, Reasoning: 3}) {
		t.Fatalf("tokens = %+v %v", ok, err)
	}
	for name, bad := range map[string]TokenCounts{
		"negative":              {Input: -1},
		"over the bound":        {Output: MaxTokenCount + 1},
		"reasoning":             {Reasoning: -2},
		"1h over total":         {CacheWrite: 1, CacheWrite1h: 2},
		"reasoning over output": {Output: 1, Reasoning: 2},
	} {
		if _, err := NewTokens(bad); !errors.Is(err, ErrInvalidTokens) {
			t.Errorf("%s accepted: %v", name, err)
		}
	}
}

func TestNewCost(t *testing.T) {
	if c, err := NewCost(0.5); err != nil || c.USD() != 0.5 || c.IsZero() {
		t.Errorf("cost = %v %v", c, err)
	}
	for _, bad := range []float64{-0.01, math.NaN(), math.Inf(1), MaxRequestCost * 2} {
		if _, err := NewCost(bad); !errors.Is(err, ErrInvalidCost) {
			t.Errorf("NewCost(%v) = %v", bad, err)
		}
	}
}
