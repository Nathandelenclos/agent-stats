package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Nathandelenclos/agent-stats/internal/platform/textfmt"
	reporting "github.com/Nathandelenclos/agent-stats/internal/reporting/application"
)

type filtersShown struct {
	period, harness int
	restricted      bool
}

type relatedSessions struct {
	active  bool
	query   reporting.SessionRelation
	label   string
	back    sessionsState
	shown   filtersShown
	filters filtersShown
	err     error
	daily   bool
	source  mode
}

func (m Model) modeRoot() bool {
	switch m.mode {
	case sessionsMode, promptsMode:
		return m.readingAtModeRoot()
	case catalogMode, quotasMode:
		return !m.localDetail && m.localFilter.value == ""
	default:
		return m.usageFilter.value == ""
	}
}

func (m Model) relationLabel() string {
	if m.related.active {
		return m.related.label
	}
	return ""
}

func (m Model) openRelation(relation reporting.SessionRelation, label string) (Model, tea.Cmd) {
	if m.relations.Queries == nil || !m.sessionsEnabled() {
		return m, nil
	}
	m.related = relatedSessions{
		active: true, query: relation, label: label, back: m.sessions, shown: filtersShown(m.shown),
		filters: filtersShown{m.period, m.harness, m.restricted}, err: m.err, daily: m.dailyOpen, source: m.mode,
	}
	m.sessions = newSessionsState()
	m.sessions.detailSeq, m.sessions.promptSeq = m.related.back.detailSeq, m.related.back.promptSeq
	m.mode, m.dailyOpen = sessionsMode, false
	m.layout()
	return m.reload()
}

func (m *Model) restoreRelationSource() {
	r := m.related
	detailSeq, promptSeq := m.sessions.detailSeq, m.sessions.promptSeq
	m.sessions = r.back
	m.sessions.detailSeq = max(m.sessions.detailSeq, detailSeq)
	m.sessions.promptSeq = max(m.sessions.promptSeq, promptSeq)
	m.shown = r.shown
	m.period, m.harness, m.restricted = r.filters.period, r.filters.harness, r.filters.restricted
	m.mode, m.dailyOpen = r.source, r.daily
	m.err, m.pending = r.err, false
	m.seq++
	m.related = relatedSessions{}
}

func (m Model) openUsageRelation() (Model, tea.Cmd) {
	grid, source := m.filteredUsageGrid()
	order := grid.order(m.sorts[m.tab])
	selected := m.table.Cursor()
	if selected < 0 || selected >= len(order) || m.pending || m.err != nil {
		return m, nil
	}
	i := source[order[selected]]
	var relation reporting.SessionRelation
	kind := tabNames[m.tab]
	switch m.tab {
	case modelTab:
		relation = reporting.SessionRelation{Kind: reporting.RelationModel, Value: m.report.ByModel[i].Group}
	case folderTab:
		relation = reporting.SessionRelation{Kind: reporting.RelationFolder, Value: m.report.ByFolder[i].Group}
	case agentTab:
		relation = reporting.SessionRelation{Kind: reporting.RelationAgent, Value: m.report.ByAgent[i].Group}
	case toolsTab:
		tool := m.report.Tools[i]
		relation = reporting.SessionRelation{Kind: reporting.RelationTool, Value: tool.Name, Server: tool.Server}
		kind = "Tool"
	default:
		relation = reporting.SessionRelation{Kind: reporting.RelationHarness, Value: m.report.ByHarness[i].Group}
	}
	label := kind + " · " + textfmt.Printable(orDash(relation.Value))
	if relation.Server != "" {
		label += " · " + textfmt.Printable(relation.Server)
	}
	return m.openRelation(relation, label)
}

func (m *Model) layoutDailyContent() {
	if m.relations.Queries == nil || !m.loaded || len(m.daily) == 0 {
		m.dailyPager.lines = m.dailyContent()
		m.dailyPager.scrollBy(0)
		return
	}
	lines := m.chartLines(m.width)
	lines = append(lines, "", titleStyle.Render("Daily values"), "Only dated requests are included.", "")
	m.dailyPositions = make([]int, len(m.daily))
	m.dailySelection = max(0, min(m.dailySelection, len(m.daily)-1))
	for i, d := range m.daily {
		marker := "  "
		if i == m.dailySelection {
			marker = "> "
		}
		m.dailyPositions[i] = len(lines)
		line := fmt.Sprintf("%s%s  %s req  %s", marker, d.Group, textfmt.Number(d.Requests), displayCost(d.Usage))
		tokens := textfmt.Number(d.InputWithCache()+d.Output) + " tokens"
		if lipgloss.Width(line+"  "+tokens) > m.width {
			lines = append(lines, line, "    "+tokens)
		} else {
			lines = append(lines, line+"  "+tokens)
		}
	}
	m.dailyPager.lines = lines
	m.dailyPager.scrollBy(0)
}

func (m Model) dailyRelationKey(msg tea.KeyMsg) (Model, tea.Cmd, bool) {
	if m.relations.Queries == nil || !m.loaded || len(m.daily) == 0 {
		return m, nil, false
	}
	switch msg.String() {
	case "down", "j", "up", "k":
		step := 1
		if k := msg.String(); k == "up" || k == "k" {
			step = -1
		}
		first := m.dailyPager.scroll
		line := m.dailyPositions[m.dailySelection]
		if line >= first && line < first+m.dailyPager.display {
			m.dailySelection += step
		}
		m.layoutDailyContent()
		m.dailyPager.reveal(m.dailyPositions[m.dailySelection])
	case "enter":
		line := m.dailyPositions[m.dailySelection]
		if line < m.dailyPager.scroll || line >= m.dailyPager.scroll+m.dailyPager.display || m.pending || m.err != nil {
			m.dailyPager.reveal(line)
			return m, nil, true
		}
		key := m.daily[m.dailySelection].Group
		day, err := time.Parse(time.DateOnly, key)
		if err == nil {
			next, cmd := m.openRelation(reporting.SessionRelation{Kind: reporting.RelationDay, Day: day}, "Day · "+key)
			return next, cmd, true
		}
	default:
		return m, nil, false
	}
	return m, nil, true
}

func (m Model) filteredUsageGrid() (grid, []int) {
	g := m.rawUsageGrid()
	var matches [][]cell
	source := make([]int, 0, len(g.rows))
	for i, row := range g.rows {
		name := row[0].text
		if m.tab == toolsTab {
			name += " " + row[1].text
		}
		if m.usageFilter.matches(strings.TrimSpace(name)) {
			matches = append(matches, row)
			source = append(source, i)
		}
	}
	g.rows = matches
	return g, source
}
