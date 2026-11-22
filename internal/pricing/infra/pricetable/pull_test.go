package pricetable

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/Nathandelenclos/agent-stats/internal/kernel"
)

func TestPullLoadsPricesAndPreservesExplicitOverrides(t *testing.T) {
	cache := filepath.Join(t.TempDir(), "agent-stats", "prices.json")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"cached-model":[0.1,0,0,0],"my-model":[0.2,0,0,0]}`)
	}))
	defer server.Close()

	if count, err := pull(server.Client(), server.URL, cache); err != nil || count != 2 {
		t.Fatalf("pulled %d models: %v", count, err)
	}
	override := filepath.Join(t.TempDir(), "override.json")
	if err := os.WriteFile(override, []byte(`{"my-model":[0.3,0,0,0]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	prices, err := load(cache, override)
	if err != nil {
		t.Fatal(err)
	}
	tokens, _ := kernel.NewTokens(kernel.TokenCounts{Input: 1})
	for model, want := range map[string]float64{"my-model": 0.3, "cached-model": 0.1} {
		if cost, ok := prices.Cost(model, tokens); !ok || cost.USD() != want {
			t.Errorf("%s: cost %v, want %v", model, cost, want)
		}
	}
}

func TestFailedPullKeepsLastValidPrices(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
	}{
		{"missing", http.StatusNotFound, `{"new-model":[0.2,0,0,0]}`},
		{"empty", http.StatusOK, `{}`},
		{"invalid rate", http.StatusOK, `{"new-model":[null,0,0,0]}`},
		{"trailing data", http.StatusOK, `{"new-model":[0.2,0,0,0]}{}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const previous = `{"my-model":[0.1,0,0,0]}`
			cache := filepath.Join(t.TempDir(), "prices.json")
			if err := os.WriteFile(cache, []byte(previous), 0o600); err != nil {
				t.Fatal(err)
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				fmt.Fprint(w, tc.body)
			}))
			defer server.Close()

			if _, err := pull(server.Client(), server.URL, cache); err == nil {
				t.Fatal("invalid download accepted")
			}
			if raw, err := os.ReadFile(cache); err != nil || string(raw) != previous {
				t.Fatalf("last valid prices lost: %q, %v", raw, err)
			}
		})
	}
}

func TestInvalidCacheFallsBackToEmbeddedPrices(t *testing.T) {
	cache := filepath.Join(t.TempDir(), "prices.json")
	if err := os.WriteFile(cache, []byte("broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	if prices, err := load(cache, ""); err != nil || len(prices) < 100 {
		t.Fatalf("embedded prices unavailable: %d models, %v", len(prices), err)
	}
}
