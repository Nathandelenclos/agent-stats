// Command gen refreshes prices.json from LiteLLM's public price table (MIT).
// Run with `go generate ./internal/pricing`.
package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"sort"
)

const source = "https://raw.githubusercontent.com/BerriAI/litellm/main/model_prices_and_context_window.json"

type entry struct {
	InputCostPerToken      float64 `json:"input_cost_per_token"`
	OutputCostPerToken     float64 `json:"output_cost_per_token"`
	CacheReadCostPerToken  float64 `json:"cache_read_input_token_cost"`
	CacheWriteCostPerToken float64 `json:"cache_creation_input_token_cost"`
	Mode                   string  `json:"mode"`
}

func main() {
	resp, err := http.Get(source)
	if err != nil {
		log.Fatal(err)
	}
	defer resp.Body.Close()
	var raw map[string]json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		log.Fatal(err)
	}
	out := map[string][4]float64{}
	for name, msg := range raw {
		var e entry
		if json.Unmarshal(msg, &e) != nil || e.InputCostPerToken == 0 || (e.Mode != "chat" && e.Mode != "responses") {
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
