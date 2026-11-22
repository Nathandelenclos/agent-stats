// Package pricetable loads the price list: an embedded copy of LiteLLM's table with cached updates and local overrides.
package pricetable

//go:generate go run ./gen

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"

	"github.com/Nathandelenclos/agent-stats/internal/platform/files"
	"github.com/Nathandelenclos/agent-stats/internal/pricing/domain"
)

//go:embed prices.json
var embedded []byte

// maxOverrideBytes bounds the --prices file (the full LiteLLM table is about 300 KB).
const maxOverrideBytes = 16 << 20

// ErrOverrideTooLarge reports a --prices file over maxOverrideBytes.
var ErrOverrideTooLarge = errors.New("price file too large")

// Load overlays cached prices on the embedded list, then applies explicit overrides from path.
// All three use the format {"model": [input, output, cacheRead, cacheWrite]}, in USD per token.
func Load(path string) (domain.PriceList, error) {
	cache, _ := cachePath()
	return load(cache, path)
}

func load(cache, path string) (domain.PriceList, error) {
	list, err := decode(embedded)
	if err != nil {
		return nil, fmt.Errorf("embedded prices: %w", err)
	}
	if cache != "" {
		if raw, err := readOverride(cache); err == nil {
			if cached, err := decode(raw); err == nil {
				maps.Copy(list, cached)
			}
		}
	}
	if path == "" {
		return list, nil
	}
	raw, err := readOverride(path)
	if err != nil {
		return nil, err
	}
	override, err := decode(raw)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	maps.Copy(list, override)
	return list, nil
}

// readOverride reads a regular file of at most maxOverrideBytes.
func readOverride(path string) ([]byte, error) {
	f, err := files.OpenRegular(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	raw, err := readPrices(f)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return raw, nil
}

func readPrices(reader io.Reader) ([]byte, error) {
	raw, err := io.ReadAll(io.LimitReader(reader, maxOverrideBytes+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > maxOverrideBytes {
		return nil, fmt.Errorf("%w (over %d bytes)", ErrOverrideTooLarge, maxOverrideBytes)
	}
	return raw, nil
}

func decode(raw []byte) (domain.PriceList, error) {
	var table map[string][]*float64
	if err := json.Unmarshal(raw, &table); err != nil {
		return nil, err
	}
	list := make(domain.PriceList, len(table))
	for model, r := range table {
		if len(r) != 4 || r[0] == nil || r[1] == nil || r[2] == nil || r[3] == nil {
			return nil, fmt.Errorf("model %q: %w: want [input, output, cacheRead, cacheWrite]", model, domain.ErrInvalidRates)
		}
		rates, err := domain.NewRates(domain.RatesPerToken{Input: *r[0], Output: *r[1], CacheRead: *r[2], CacheWrite: *r[3]})
		if err != nil {
			return nil, fmt.Errorf("model %q: %w", model, err)
		}
		list[model] = rates
	}
	return list, nil
}
