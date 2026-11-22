// Package domain holds the pricing rules: what a model charges per token and what a request costs.
package domain

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"strings"

	"github.com/Nathandelenclos/agent-stats/internal/kernel"
)

// MaxRatePerToken bounds a price (most expensive model seen in LiteLLM's table: 0.0006 USD per token).
const MaxRatePerToken = 1.0

// RatesPerToken are a model's prices in USD per token, as a price table states them.
type RatesPerToken struct {
	Input, Output, CacheRead, CacheWrite float64
}

// Rates are a model's validated prices. Build them with NewRates.
type Rates struct {
	perToken RatesPerToken
}

// ErrInvalidRates reports a price that is negative, not finite or over MaxRatePerToken.
var ErrInvalidRates = errors.New("invalid rates")

// NewRates validates the prices of one model.
func NewRates(r RatesPerToken) (Rates, error) {
	for _, v := range []float64{r.Input, r.Output, r.CacheRead, r.CacheWrite} {
		if v < 0 || v > MaxRatePerToken || math.IsNaN(v) {
			return Rates{}, fmt.Errorf("%w: %v USD per token", ErrInvalidRates, v)
		}
	}
	return Rates{r}, nil
}

// Cost prices the tokens of one request. Writes with Anthropic's 1-hour TTL are billed at twice
// the input rate instead of the list's 5-minute cache-write rate.
func (r Rates) Cost(t kernel.Tokens) (kernel.Cost, error) {
	c, p := t.Counts(), r.perToken
	// Each product is converted explicitly so the compiler cannot fuse it with the sum (FMA on arm64 but not
	// on amd64): the same tokens must cost the same on every CPU.
	return kernel.NewCost(float64(float64(c.Input)*p.Input) + float64(float64(c.Output)*p.Output) +
		float64(float64(c.CacheRead)*p.CacheRead) + float64(float64(t.CacheWrite5m())*p.CacheWrite) +
		float64(float64(c.CacheWrite1h)*2*p.Input))
}

// PriceList maps a model name to its rates.
type PriceList map[string]Rates

var dateSuffix = regexp.MustCompile(`-\d{8}$`)

// dottedVersion is a version written with a dot, which the price table writes with a dash for Claude models.
var dottedVersion = regexp.MustCompile(`(\d)\.(\d)`)

// Lookup finds rates for a model, tolerating provider prefixes ("openai-codex/gpt-6"), date suffixes and, for Claude
// models, a dotted version ("claude-sonnet-4.5" for "claude-sonnet-4-5"). The name as written is looked up first.
func (l PriceList) Lookup(model string) (Rates, bool) {
	m := strings.ToLower(model)
	candidates := []string{m}
	if i := strings.LastIndex(m, "/"); i >= 0 {
		candidates = append(candidates, m[i+1:])
	}
	for _, c := range candidates[:len(candidates):len(candidates)] {
		if d := dateSuffix.ReplaceAllString(c, ""); d != c {
			candidates = append(candidates, d)
		}
	}
	for _, c := range candidates[:len(candidates):len(candidates)] {
		if d := dottedVersion.ReplaceAllString(c, "$1-$2"); d != c && strings.Contains(c, "claude") {
			candidates = append(candidates, d)
		}
	}
	for _, c := range candidates {
		if r, ok := l[c]; ok {
			return r, true
		}
	}
	return Rates{}, false
}

// Cost prices tokens used with a model; false when the model has no known price or the cost is out of bounds.
func (l PriceList) Cost(model string, t kernel.Tokens) (kernel.Cost, bool) {
	r, ok := l.Lookup(model)
	if !ok {
		return kernel.Cost{}, false
	}
	c, err := r.Cost(t)
	return c, err == nil
}
