package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	catalog "github.com/Nathandelenclos/agent-stats/internal/catalog/application"
	"github.com/Nathandelenclos/agent-stats/internal/platform/textfmt"
	"github.com/Nathandelenclos/agent-stats/internal/quota"
	reporting "github.com/Nathandelenclos/agent-stats/internal/reporting/application"
)

type dashboardState struct {
	enabled, open bool
	selection     int
	positions     []int
	summary       dashboardSummary
	pager
}

type dashboardSummary struct {
	seq                             int
	sessions, prompts, recurring    int
	skills, agents, servers, unused int
	latestSession                   reporting.SessionSummary
	quotas                          []quota.Window
	sessionOK, promptOK, catalogOK  bool
	loaded                          bool
}

func (m Model) loadDashboardSummary() tea.Cmd {
	q, seq, ctx := m.query(), m.seq, m.syncs.ctx
	lister, reviewer, inventory, quotas := m.lister, m.reviewer, m.catalog, m.quotas
	return func() tea.Msg {
		out := dashboardSummary{seq: seq, loaded: true}
		if lister.Queries != nil {
			if result, err := lister.Run(ctx, q); err == nil {
				out.sessionOK, out.sessions = true, len(result.Sessions)
				if out.sessions > 0 {
					out.latestSession = result.Sessions[0]
				}
			}
		}
		if reviewer.Queries != nil {
			if result, err := reviewer.Run(ctx, q); err == nil {
				out.promptOK, out.prompts, out.recurring = true, len(result.Prompts), len(result.Recurring)
			}
		}
		if inventory != nil {
			if result, err := inventory(catalog.Query{Since: q.Since, Harness: q.Harness, Project: q.Project}); err == nil {
				out.catalogOK = true
				for _, entry := range result.Entries {
					switch entry.Kind.String() {
					case "skill":
						out.skills++
					case "agent":
						out.agents++
					case "mcp":
						out.servers++
					}
					if entry.Unused() {
						out.unused++
					}
				}
			}
		}
		if quotas != nil && ctx.Err() == nil {
			out.quotas = quotas(ctx)
		}
		return out
	}
}

func (m Model) dashboardModes() []mode {
	var modes []mode
	for _, md := range []mode{usageMode, quotasMode, sessionsMode, promptsMode, catalogMode} {
		if m.modeEnabled(md) {
			modes = append(modes, md)
		}
	}
	return modes
}

func (m Model) openDashboard() (Model, tea.Cmd) {
	if m.related.active {
		m.restoreRelationSource()
	}
	m.dashboard.enabled, m.dashboard.open = true, true
	m.dashboard.summary = dashboardSummary{}
	if m.loaded {
		m.shown = m.reported
	}
	m.mode, m.dailyOpen, m.expanded = usageMode, false, false
	m.sessions.detail = sessionDetail{}
	m.layout()
	return m.reload()
}

func (m Model) dashboardKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	d := &m.dashboard
	modes := m.dashboardModes()
	switch k := msg.String(); k {
	case "up", "k", "down", "j", "left", "right", "tab", "shift+tab":
		step := 1
		switch k {
		case "up", "k", "left", "shift+tab":
			step = -1
		}
		d.selection = max(0, min(d.selection+step, len(modes)-1))
		m.layoutDashboard()
	case "enter", "m":
		return m.selectMode(modes[d.selection])
	case "g":
		d.open = false
		m.dailyFromDashboard = true
		return m.usageKey(msg)
	case "p", "h", "a", "r":
		return m.usageKey(msg)
	default:
		d.scrollKey(k)
	}
	return m, nil
}

func (m *Model) layoutDashboard() {
	d := &m.dashboard
	if !d.open {
		return
	}
	d.display = max(1, m.height-m.extraTitleLines()-4)
	modes := m.dashboardModes()
	d.selection = min(d.selection, len(modes)-1)
	d.positions = make([]int, len(modes))
	lines := m.dashboardCard(usageMode, d.selection == 0, m.width)
	for i := 1; i < len(modes); {
		lines = append(lines, "")
		d.positions[i] = len(lines)
		if m.width >= 78 && i+1 < len(modes) {
			width := (m.width - chartGap) / 2
			d.positions[i+1] = len(lines)
			lines = append(lines, pairLines(
				m.dashboardCard(modes[i], d.selection == i, width),
				m.dashboardCard(modes[i+1], d.selection == i+1, width), width)...)
			i += 2
		} else {
			lines = append(lines, m.dashboardCard(modes[i], d.selection == i, m.width)...)
			i++
		}
	}
	d.lines = lines
	d.reveal(d.positions[d.selection])
}

func pairLines(left, right []string, width int) []string {
	column := lipgloss.NewStyle().Width(width).MaxWidth(width)
	return strings.Split(lipgloss.JoinHorizontal(lipgloss.Top, column.Render(strings.Join(left, "\n")),
		strings.Repeat(" ", chartGap), column.Render(strings.Join(right, "\n"))), "\n")
}

func (m Model) dashboardCard(md mode, selected bool, width int) []string {
	name := modeNames[md]
	if md == quotasMode {
		name += " · account"
	}
	marker := "  "
	if selected {
		marker = "> "
	}
	lines := []string{marker + titleStyle.Render(name)}
	if md == usageMode {
		lines = append(lines, m.overview()...)
		charts := m.dashboardCharts(width)
		if len(charts) > 0 {
			lines = append(lines, "")
			lines = append(lines, charts...)
		}
		return lines
	}
	d := m.dashboard.summary
	if !d.loaded {
		return append(lines, "  Loading…")
	}
	switch md {
	case sessionsMode:
		if d.sessionOK {
			lines = append(lines, "  "+textfmt.Plural(d.sessions, "session", "sessions"))
			if d.sessions > 0 {
				latest := d.latestSession
				lines = append(lines, "  "+textfmt.Excerpt(sessionTitle(latest), max(1, width-2)))
			}
			return lines
		}
	case promptsMode:
		if d.promptOK {
			return append(lines, "  "+textfmt.Plural(d.prompts, "flagged prompt", "flagged prompts"),
				"  "+textfmt.Plural(d.recurring, "recurring ask", "recurring asks"))
		}
	case catalogMode:
		if d.catalogOK {
			return append(lines, fmt.Sprintf("  %d skills · %d agents · %d MCP", d.skills, d.agents, d.servers),
				"  "+textfmt.Plural(d.unused, "tracked item unused", "tracked items unused"))
		}
	case quotasMode:
		return append(lines, dashboardQuotaLines(d.quotas, m.now(), width)...)
	}
	return append(lines, "  Unavailable")
}

func (m Model) dashboardCharts(width int) []string {
	column := width
	if width >= 78 {
		column = (width - chartGap) / 2
	}
	var requests []string
	if len(m.daily) > 0 {
		from, to, days := recentDays(m.daily, min(trendDays, column))
		if len(days) > 0 {
			graph := sparkline(days, func(u reporting.Usage) float64 { return float64(u.Requests) })
			requests = []string{titleStyle.Render("Requests per day"), from + " to " + to, chartStyle.Render(graph)}
		}
	}
	costs := m.costByHarness(column)
	if len(requests) == 0 || len(costs) == 0 {
		return append(requests, costs...)
	}
	if column != width {
		return pairLines(requests, costs, column)
	}
	return append(append(requests, ""), costs...)
}

func dashboardQuotaLines(windows []quota.Window, now time.Time, width int) []string {
	var lines []string
	for _, provider := range []string{"Codex", "Claude Code"} {
		var tightest *quota.Window
		for i := range windows {
			w := &windows[i]
			if w.Provider == provider && (tightest == nil || w.Remaining < tightest.Remaining) {
				tightest = w
			}
		}
		if tightest == nil {
			lines = append(lines, "  "+provider+" · unavailable")
			continue
		}
		left := "  " + provider
		right := fmt.Sprintf("%.0f%%", tightest.Remaining)
		if tightest.Remaining <= 10 {
			right += " low"
		}
		if label := strings.TrimSuffix(tightest.Label, " limit"); label != "" && lipgloss.Width(left+" "+label+"  "+right) <= width {
			left += " " + label
		}
		barWidth := min(12, width-lipgloss.Width(left+right)-4)
		if barWidth >= 3 {
			left += "  " + quotaBar(*tightest, barWidth)
		}
		lines = append(lines, left+"  "+right)
		if left := tightest.ResetAt.Sub(now); tightest.Remaining <= 10 && left > 0 {
			lines = append(lines, "  "+provider+" resets in "+textfmt.Duration(left))
		}
	}
	return lines
}

func (m Model) dashboardView() string {
	return lipgloss.NewStyle().MaxWidth(m.width).Render(strings.Join([]string{
		m.titleBar(), "", m.dashboard.visible(), m.status(), m.help(),
	}, "\n"))
}

// reveal keeps a selected line in a pager's viewport after navigation.
func (p *pager) reveal(line int) {
	if line < p.scroll {
		p.scroll = line
	} else if line >= p.scroll+p.display {
		p.scroll = line - p.display + 1
	}
	p.scrollBy(0)
}
