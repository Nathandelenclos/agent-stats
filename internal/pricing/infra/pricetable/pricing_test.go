package pricetable

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/Nathandelenclos/agent-stats/internal/kernel"
	"github.com/Nathandelenclos/agent-stats/internal/pricing/domain"
)

func TestEmbeddedListLoads(t *testing.T) {
	l, err := load("", "")
	if err != nil || len(l) < 100 {
		t.Fatalf("embedded list: %d entries, err %v", len(l), err)
	}
}

func TestOverrideReplacesAndAddsModels(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prices.json")
	if err := os.WriteFile(path, []byte(`{"my-model": [0.25, 0.5, 0, 0]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	l, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	tokens, _ := kernel.NewTokens(kernel.TokenCounts{Input: 1, Output: 1})
	if c, ok := l.Cost("my-model", tokens); !ok || c.USD() != 0.75 {
		t.Errorf("override cost = %v %v", c, ok)
	}
}

func TestOverrideRefusesNegativeRates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prices.json")
	if err := os.WriteFile(path, []byte(`{"my-model": [-1, 2, 0, 0]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); !errors.Is(err, domain.ErrInvalidRates) {
		t.Errorf("negative rate accepted: %v", err)
	}
}

func TestOverrideRefusesMalformedEntries(t *testing.T) {
	for _, body := range []string{`{"m": null}`, `{"m": [1, 2]}`, `{"m": [1, 2, 3, 4, 5]}`} {
		path := filepath.Join(t.TempDir(), "prices.json")
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(path); !errors.Is(err, domain.ErrInvalidRates) {
			t.Errorf("%s accepted: %v", body, err)
		}
	}
}

func TestOverrideMustBeASmallRegularFile(t *testing.T) {
	if _, err := Load(t.TempDir()); err == nil {
		t.Error("directory accepted as price file")
	}
	path := filepath.Join(t.TempDir(), "big.json")
	if err := os.WriteFile(path, make([]byte, maxOverrideBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); !errors.Is(err, ErrOverrideTooLarge) {
		t.Errorf("oversized file: %v", err)
	}
}
