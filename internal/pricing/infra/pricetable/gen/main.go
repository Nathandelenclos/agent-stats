// Command gen refreshes prices.json from LiteLLM's public price table (MIT).
// Run with `go generate ./internal/pricing`. To take newer prices, set commit to a reviewed
// BerriAI/litellm commit first: the table is read at that commit, never from a moving branch.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"sort"
	"time"

	"github.com/Nathandelenclos/agent-stats/internal/pricing/domain"
)

const (
	commit   = "39c0f0cc164af0f7b3514b0dd2b2935dc70c4c42" // BerriAI/litellm main on 2026-11-21
	source   = "https://raw.githubusercontent.com/BerriAI/litellm/" + commit + "/model_prices_and_context_window.json"
	maxBytes = 64 << 20
)

type entry struct {
	InputCostPerToken      float64 `json:"input_cost_per_token"`
	OutputCostPerToken     float64 `json:"output_cost_per_token"`
	CacheReadCostPerToken  float64 `json:"cache_read_input_token_cost"`
	CacheWriteCostPerToken float64 `json:"cache_creation_input_token_cost"`
	Mode                   string  `json:"mode"`
}

func main() {
	raw, err := fetch()
	if err != nil {
		log.Fatal(err)
	}
	out := map[string][4]float64{}
	for name, msg := range raw {
		var e entry
		if json.Unmarshal(msg, &e) != nil || e.InputCostPerToken == 0 || (e.Mode != "chat" && e.Mode != "responses") {
			continue
		}
		if _, err := domain.NewRates(domain.RatesPerToken{Input: e.InputCostPerToken, Output: e.OutputCostPerToken,
			CacheRead: e.CacheReadCostPerToken, CacheWrite: e.CacheWriteCostPerToken}); err != nil {
			log.Printf("skipping %s: %v", name, err)
			continue
		}
		out[name] = [4]float64{e.InputCostPerToken, e.OutputCostPerToken, e.CacheReadCostPerToken, e.CacheWriteCostPerToken}
	}
	names := make([]string, 0, len(out))
	for n := range out {
		names = append(names, n)
	}
	sort.Strings(names)
	f, err := os.Create("prices.json")
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()
	// One model per line keeps diffs readable when prices change.
	f.WriteString("{\n")
	for i, n := range names {
		k, _ := json.Marshal(n)
		v, _ := json.Marshal(out[n])
		sep := ",\n"
		if i == len(names)-1 {
			sep = "\n"
		}
		f.WriteString(" " + string(k) + ":" + string(v) + sep)
	}
	f.WriteString("}\n")
	log.Printf("%d models written", len(names))
}

func fetch() (map[string]json.RawMessage, error) {
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Get(source)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s", source, resp.Status)
	}
	var raw map[string]json.RawMessage
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBytes)).Decode(&raw); err != nil {
		return nil, fmt.Errorf("%s: %w", source, err)
	}
	return raw, nil
}
