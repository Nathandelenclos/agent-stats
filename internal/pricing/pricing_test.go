package pricing

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/Nathandelenclos/agent-stats/internal/usage"
)

func TestEmbeddedListLoads(t *testing.T) {
	l, err := Load("")
	if err != nil || len(l) < 100 {
		t.Fatalf("embedded list: %d entries, err %v", len(l), err)
	}
}

func TestOverrideReplacesAndAddsModels(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prices.json")
	if err := os.WriteFile(path, []byte(`{"my-model": [1, 2, 0, 0]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	l, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if c, ok := l.Price(usage.Request{Model: "my-model", Tokens: usage.Tokens{Input: 1, Output: 1}}); !ok || c != 3 {
		t.Errorf("override cost = %v %v", c, ok)
	}
}

func TestOverrideRefusesNegativeRates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prices.json")
	if err := os.WriteFile(path, []byte(`{"my-model": [-1, 2, 0, 0]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); !errors.Is(err, usage.ErrInvalidRates) {
		t.Errorf("negative rate accepted: %v", err)
	}
}
