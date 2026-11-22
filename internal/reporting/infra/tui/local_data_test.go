package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	catalog "github.com/Nathandelenclos/agent-stats/internal/catalog/application"
	"github.com/Nathandelenclos/agent-stats/internal/catalog/domain"
	"github.com/Nathandelenclos/agent-stats/internal/kernel"
	"github.com/Nathandelenclos/agent-stats/internal/quota"
)

func TestCatalogUsesCurrentFiltersAndKeepsUnknownDistinctFromZero(t *testing.T) {
	var got catalog.Query
	item := func(label string) catalog.Item {
		name, err := domain.NewName(label)
		if err != nil {
			t.Fatal(err)
		}
		return catalog.Item{Harness: kernel.Claude, Kind: domain.Skill, Name: name, Scope: domain.ScopeUser}
	}
	known, unknown := item("Known skill"), item("Unknown skill")
	m := New(Options{
		Harness: kernel.Claude, SinceLabel: "7d",
		Catalog: func(q catalog.Query) (catalog.Catalog, error) {
			got = q
			entries := []catalog.Entry{{Item: known, Tracked: true}}
			if !q.UnusedOnly {
				entries = append(entries, catalog.Entry{Item: unknown})
			}
			return catalog.Catalog{Entries: entries}, nil
		},
	})
	m = send(t, m, tea.WindowSizeMsg{Width: 40, Height: 15})
	m, cmd := pressCmd(t, m, "c")
	m = answer(t, m, cmd)
	for _, want := range []string{"Catalog", "0 loads", "Usage unknown", "u unused", "q quit"} {
		if !strings.Contains(m.View(), want) {
			t.Errorf("catalog lacks %q:\n%s", want, m.View())
		}
	}
	assertTrendSize(t, m.View(), 40, 15)
	if got.Harness != kernel.Claude || got.Since.IsZero() || got.UnusedOnly {
		t.Fatalf("unexpected filters: %+v", got)
	}

	m, cmd = pressCmd(t, m, "u")
	m = answer(t, m, cmd)
	if !got.UnusedOnly || strings.Contains(m.View(), "Unknown skill") || !strings.Contains(m.View(), "Unused only") {
		t.Fatalf("unused filter not applied:\n%s", m.View())
	}
}

func TestQuotaViewRefreshesWithoutProjectFilters(t *testing.T) {
	calls := 0
	m := New(Options{Quotas: func(context.Context) []quota.Window {
		calls++
		return []quota.Window{{Provider: "Codex", Label: "7-day limit", Remaining: 0, ResetAt: time.Date(2026, 12, 1, 12, 0, 0, 0, time.UTC)}}
	}})
	m = send(t, m, tea.WindowSizeMsg{Width: 40, Height: 15})
	m, cmd := pressCmd(t, m, "l")
	m = answer(t, m, cmd)
	for _, want := range []string{"Quotas", "7-day limit", "0.0% remaining", "Resets", "Claude Code", "Quota unavailable", "r refresh", "q quit"} {
		if !strings.Contains(m.View(), want) {
			t.Errorf("quotas lack %q:\n%s", want, m.View())
		}
	}
	if strings.Contains(m.View(), "period:") {
		t.Error("account quotas show project-usage filters")
	}
	m, cmd = pressCmd(t, m, "r")
	m = answer(t, m, cmd)
	if calls != 2 {
		t.Fatalf("quota read %d times", calls)
	}
	assertTrendSize(t, m.View(), 40, 15)
}
