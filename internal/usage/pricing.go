package usage

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"strings"
)

// Rates are a model's prices in USD per token. Build them with NewRates.
type Rates struct {
	input, output, cacheRead, cacheWrite float64
}

// ErrInvalidRates reports a negative or non-finite price.
var ErrInvalidRates = errors.New("invalid rates")

// NewRates returns the rates of one model, in USD per token, refusing negative or non-finite values.
func NewRates(input, output, cacheRead, cacheWrite float64) (Rates, error) {
	for _, v := range []float64{input, output, cacheRead, cacheWrite} {
		if v < 0 || math.IsNaN(v) || math.IsInf(v, 0) {
			return Rates{}, fmt.Errorf("%w: %v per token", ErrInvalidRates, v)
		}
	}
	return Rates{input, output, cacheRead, cacheWrite}, nil
}

// Cost prices the tokens of one request. Writes with Anthropic's 1-hour TTL are billed at twice
// the input rate instead of the list's 5-minute cache-write rate.
func (r Rates) Cost(t Tokens) USD {
	return USD(float64(t.Input)*r.input + float64(t.Output)*r.output + float64(t.CacheRead)*r.cacheRead +
		float64(t.CacheWrite-t.CacheWrite1h)*r.cacheWrite + float64(t.CacheWrite1h)*2*r.input)
}

// PriceList maps a model name to its rates.
type PriceList map[string]Rates

var dateSuffix = regexp.MustCompile(`-\d{8}$`)

// Lookup finds rates for a model, tolerating provider prefixes ("openai-codex/gpt-6") and date suffixes.
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
	for _, c := range candidates {
		if r, ok := l[c]; ok {
			return r, true
		}
	}
	return Rates{}, false
}

// Price returns what a request cost: the harness's own figure when it logged a positive one,
// otherwise the price list's. priced is false when neither is known.
func (l PriceList) Price(r Request) (cost USD, priced bool) {
	if r.LoggedCost > 0 {
		return r.LoggedCost, true
	}
	rates, ok := l.Lookup(r.Model)
	if !ok {
		return 0, false
	}
	return rates.Cost(r.Tokens), true
}
