// Package pricing turns token counts into USD using an embedded price table.
package pricing

//go:generate go run ./gen

import (
	_ "embed"
	"encoding/json"
	"os"
	"regexp"
	"strings"
)

//go:embed prices.json
var embedded []byte

// Rates are USD per token: input, output, cache read, cache write.
type Rates [4]float64

// Table maps a model name to its rates.
type Table map[string]Rates

// Load returns the embedded table, overridden entry by entry by the JSON file at path when given.
func Load(path string) (Table, error) {
	t := Table{}
	if err := json.Unmarshal(embedded, &t); err != nil {
		return nil, err
	}
	if path == "" {
		return t, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	override := Table{}
	if err := json.Unmarshal(raw, &override); err != nil {
		return nil, err
	}
	for k, v := range override {
		t[k] = v
	}
	return t, nil
}

var dateSuffix = regexp.MustCompile(`-\d{8}$`)

// Lookup finds rates for a model, tolerating provider prefixes ("openai-codex/gpt-6") and date suffixes.
func (t Table) Lookup(model string) (Rates, bool) {
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
		if r, ok := t[c]; ok {
			return r, true
		}
	}
	return Rates{}, false
}

// Cost prices one request. cacheWrite1h is the part of cacheWrite written with Anthropic's 1-hour TTL,
// billed at twice the input rate instead of the table's 5-minute rate. ok is false when the model is unknown.
func (t Table) Cost(model string, input, output, cacheRead, cacheWrite, cacheWrite1h int64) (usd float64, ok bool) {
	r, ok := t.Lookup(model)
	if !ok {
		return 0, false
	}
	return float64(input)*r[0] + float64(output)*r[1] + float64(cacheRead)*r[2] +
		float64(cacheWrite-cacheWrite1h)*r[3] + float64(cacheWrite1h)*2*r[0], true
}
