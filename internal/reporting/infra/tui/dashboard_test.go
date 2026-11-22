package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	catalog "github.com/Nathandelenclos/agent-stats/internal/catalog/application"
	catalogDomain "github.com/Nathandelenclos/agent-stats/internal/catalog/domain"
	"github.com/Nathandelenclos/agent-stats/internal/kernel"
	"github.com/Nathandelenclos/agent-stats/internal/quota"
	reporting "github.com/Nathandelenclos/agent-stats/internal/reporting/application"
	"github.com/Nathandelenclos/agent-stats/internal/reporting/domain"
)

func dashboardFixture(t *testing.T) Model {
	t.Helper()
	sessions, prompts := sampleSessions(), samplePrompts()
	name, err := catalogDomain.NewName("github")
	if err != nil {
		t.Fatal(err)
	}
	opts := Options{
		Reports:  build(&fakeReports{report: sampleReport(), daily: sampleDaily()}),
		Sessions: reporting.ListSessions{Queries: sessions},
		Session:  reporting.ShowSession{Queries: sessions},
		Prompts:  reporting.ReviewPrompts{Queries: prompts},
		Catalog: func(catalog.Query) (catalog.Catalog, error) {
			return catalog.Catalog{Entries: []catalog.Entry{{
				Item:    catalog.Item{Name: name, Kind: catalogDomain.MCPServer, Harness: kernel.Codex, Scope: catalogDomain.ScopeUser},
				Tracked: true, Uses: 4,
			}}}, nil
		},
		Quotas: func(context.Context) []quota.Window {
			return []quota.Window{
				{Provider: "Codex", Label: "7-day limit", Remaining: 68},
				{Provider: "Claude Code", Label: "7-day limit", Remaining: 3},
			}
		},
	}
	m := New(opts)
	m.dashboard.enabled, m.dashboard.open = true, true
	next, cmd := m.Update(m.Init()())
	return answer(t, next.(Model), cmd)
}

func TestDashboardShowsItsSectionsAndOpensSelectedView(t *testing.T) {
	m := dashboardFixture(t)
	for _, size := range []tea.WindowSizeMsg{{Width: 80, Height: 24}, {Width: 120, Height: 32}} {
		m = send(t, m, size)
		view := m.View()
		assertTrendSize(t, view, size.Width, size.Height)
		for _, value := range []string{"[Dashboard]", "> Usage", "Requests per day", "Estimated cost by coding agent",
			"3% low", "3 sessions", "4 flagged prompts", "1 recurring ask", "1 MCP"} {
			if !strings.Contains(view, value) {
				t.Errorf("%dx%d dashboard lacks %q:\n%s", size.Width, size.Height, value, view)
			}
		}
	}

	m = send(t, m, tea.WindowSizeMsg{Width: 40, Height: 15})
	m = send(t, send(t, m, tea.KeyMsg{Type: tea.KeyDown}), tea.KeyMsg{Type: tea.KeyDown})
	assertTrendSize(t, m.View(), 40, 15)
	if !strings.Contains(m.View(), "> Sessions") {
		t.Fatalf("narrow dashboard scrolled past the selection:\n%s", m.View())
	}
	m, cmd := pressType(t, m, tea.KeyEnter)
	m = answer(t, m, cmd)
	if m.dashboard.open || m.mode != sessionsMode {
		t.Fatalf("dashboard did not open sessions:\n%s", m.View())
	}
	m, cmd = pressType(t, m, tea.KeyEsc)
	next, cmd := m.Update(cmd())
	m = answer(t, next.(Model), cmd)
	if !m.dashboard.open || m.dashboard.selection != 2 || !strings.Contains(m.View(), "> Sessions") {
		t.Fatalf("return lost the dashboard selection:\n%s", m.View())
	}
}

type relationQueries struct {
	got   domain.SessionRelation
	scope domain.Filter
}

func (f *relationQueries) RelatedSessions(_ context.Context, q domain.Filter, rel domain.SessionRelation) ([]domain.SessionSummary, error) {
	f.got, f.scope = rel, q
	row := sampleSessions().list[0]
	row.Usage = domain.Usage{Requests: 2, Cost: 0.75}
	return []domain.SessionSummary{row}, nil
}

func TestUsageAndDayOpenTheirRelatedSessions(t *testing.T) {
	m := dashboardFixture(t)
	related := &relationQueries{}
	m.relations.Queries = related
	m, cmd := pressType(t, m, tea.KeyEnter)
	m = answer(t, m, cmd)
	m = press(t, m, "2", "/", "claude")
	m = send(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	m, cmd = pressType(t, m, tea.KeyEnter)
	m = answer(t, m, cmd)
	if related.got.Kind != reporting.RelationModel || related.got.Value != "claude-x" {
		t.Fatalf("wrong relation from filtered row: %+v", related.got)
	}
	if view := m.View(); !strings.Contains(view, "Model · claude-x") || !strings.Contains(view, "0.75") {
		t.Errorf("missing contribution context:\n%s", view)
	}
	m = send(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.mode != usageMode || m.usageFilter.value != "claude" || m.tab != modelTab {
		t.Fatalf("return lost the usage table or filter:\n%s", m.View())
	}
	m = press(t, m, "g")
	m, cmd = pressType(t, m, tea.KeyEnter)
	if cmd == nil {
		m, cmd = pressType(t, m, tea.KeyEnter)
	}
	m = answer(t, m, cmd)
	if related.got.Kind != reporting.RelationDay || related.got.Day.Format(time.DateOnly) != "2026-11-01" {
		t.Fatalf("wrong day relation: %+v", related.got)
	}
	m = send(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if !m.dailyOpen || !strings.Contains(m.View(), "> 2026-11-01") {
		t.Fatalf("return lost the selected day:\n%s", m.View())
	}
}

func TestCatalogMCPLinksToObservedCallsAndReturnsToDetails(t *testing.T) {
	m := dashboardFixture(t)
	related := &relationQueries{}
	m.relations.Queries = related
	m, cmd := pressCmd(t, m, "c")
	m = answer(t, m, cmd)
	m = send(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if view := m.View(); !strings.Contains(view, "Scope") || !strings.Contains(view, "4 calls") {
		t.Fatalf("catalog item details unavailable:\n%s", view)
	}
	m, cmd = pressCmd(t, m, "s")
	m = answer(t, m, cmd)
	if related.got.Kind != reporting.RelationMCP || related.got.Value != "github" || related.got.Harness != kernel.Codex {
		t.Fatalf("wrong MCP relation: %+v", related.got)
	}
	m = send(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.mode != catalogMode || !m.localDetail || !strings.Contains(m.View(), "4 calls") {
		t.Fatalf("return lost the MCP detail:\n%s", m.View())
	}
}

func TestDashboardQuotasNameTheirScope(t *testing.T) {
	m := dashboardFixture(t)
	m, cmd := pressCmd(t, m, "l")
	m = answer(t, m, cmd)
	m = send(t, m, tea.KeyMsg{Type: tea.KeyDown})
	m = send(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	view := m.View()
	for _, want := range []string{"Claude Code · account", "3.0% remaining", "Reset time unavailable"} {
		if !strings.Contains(view, want) {
			t.Errorf("quota detail lacks %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "period:") {
		t.Errorf("quota inherited activity scope:\n%s", view)
	}
}

func TestRelationKeepsRequestSequencesAcrossNavigation(t *testing.T) {
	m := dashboardFixture(t)
	m.relations.Queries = &relationQueries{}
	m.dashboard.open = false
	m.sessions.detailSeq, m.sessions.promptSeq = 7, 11
	m, _ = m.openRelation(reporting.SessionRelation{Kind: reporting.RelationModel, Value: "fixture"}, "Model")
	if m.sessions.detailSeq != 7 || m.sessions.promptSeq != 11 {
		t.Fatal("entering a relation reuses earlier request sequences")
	}
	m.sessions.detailSeq, m.sessions.promptSeq = 8, 12
	m.restoreRelationSource()
	if m.sessions.detailSeq != 8 || m.sessions.promptSeq != 12 {
		t.Fatal("leaving a relation reuses earlier request sequences")
	}
}
