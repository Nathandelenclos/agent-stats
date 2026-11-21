// Package pricing loads the price list: an embedded copy of LiteLLM's table, optionally overridden by a local file.
package pricing

//go:generate go run ./gen

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"

	"github.com/Nathandelenclos/agent-stats/internal/usage"
)

//go:embed prices.json
var embedded []byte

// Load returns the embedded price list, overridden model by model by the JSON file at path when given.
// Both use the format {"model": [input, output, cacheRead, cacheWrite]}, in USD per token.
func Load(path string) (usage.PriceList, error) {
	list, err := decode(embedded)
	if err != nil {
		return nil, fmt.Errorf("embedded prices: %w", err)
	}
	if path == "" {
		return list, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	override, err := decode(raw)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	for model, rates := range override {
		list[model] = rates
	}
	return list, nil
}

func decode(raw []byte) (usage.PriceList, error) {
	var table map[string][]float64
	if err := json.Unmarshal(raw, &table); err != nil {
		return nil, err
	}
	list := make(usage.PriceList, len(table))
	for model, r := range table {
		if len(r) != 4 {
			return nil, fmt.Errorf("model %q: %w: want [input, output, cacheRead, cacheWrite]", model, usage.ErrInvalidRates)
		}
		rates, err := usage.NewRates(r[0], r[1], r[2], r[3])
		if err != nil {
			return nil, fmt.Errorf("model %q: %w", model, err)
		}
		list[model] = rates
	}
	return list, nil
}
