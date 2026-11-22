package tui

import (
	"fmt"
	"math"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	catalog "github.com/Nathandelenclos/agent-stats/internal/catalog/application"
	"github.com/Nathandelenclos/agent-stats/internal/platform/textfmt"
	"github.com/Nathandelenclos/agent-stats/internal/quota"
	reporting "github.com/Nathandelenclos/agent-stats/internal/reporting/application"
)

type catalogMsg struct {
	seq, period, harness int
	restricted           bool
	result               catalog.Catalog
	err                  error
}

type quotasMsg struct {
	seq     int
	windows []quota.Window
}

func (m Model) loadCatalog() tea.Cmd {
	read, q, seq, period, harness, restricted := m.catalog, m.query(), m.seq, m.period, m.harness, m.restricted
	unused := m.catalogUnused
	return func() tea.Msg {
		result, err := read(catalog.Query{Since: q.Since, Harness: q.Harness, Project: q.Project, UnusedOnly: unused})
		return catalogMsg{seq, period, harness, restricted, result, err}
	}
}

func (m Model) loadQuotas() tea.Cmd {
	read, seq, ctx := m.quotas, m.seq, m.syncs.ctx
	return func() tea.Msg { return quotasMsg{seq, read(ctx)} }
}

var catalogKinds = []string{"", "skill", "agent", "mcp", "plugin", "tool"}
var catalogTabs = []string{"All", "Skills", "Agents", "MCP", "Plugins", "Tools"}

func (m Model) localDataKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k := msg.String(); k {
	case "m":
		return m.switchMode()
	case "r":
		if m.mode == quotasMode {
			return m.reload()
		}
		return m.reindex()
	case "enter":
		if len(m.localItems()) > 0 && !m.localDetail {
			m.localDetail = true
			m.localListScroll = m.localPager.scroll
			m.localPager.scroll = 0
			m.layoutLocalData()
		}
	case "esc":
		if m.localDetail {
			m.localDetail = false
			m.localPager.scroll = m.localListScroll
		} else {
			m.localFilter.value = ""
		}
		m.layoutLocalData()
	case "s":
		return m.catalogSessions()
	case "/":
		if !m.localDetail {
			return m.startListFilter(&m.localFilter)
		}
	case "left", "shift+tab", "right", "tab":
		if m.mode == catalogMode && !m.localDetail {
			step := 1
			if k == "left" || k == "shift+tab" {
				step = -1
			}
			m.localCategory = (m.localCategory + step + len(catalogKinds)) % len(catalogKinds)
			m.localSelection, m.localPager.scroll = 0, 0
			m.layoutLocalData()
		}
	case "up", "k", "down", "j":
		if !m.localDetail {
			step := 1
			if k == "up" || k == "k" {
				step = -1
			}
			m.moveLocalSelection(step)
			break
		}
		m.localPager.scrollKey(k)
	case "p", "h", "a", "u":
		if m.mode == catalogMode && !m.localDetail {
			m.localPager.scroll, m.localSelection = 0, 0
			switch k {
			case "p":
				m.period = (m.period + 1) % len(m.periods)
			case "h":
				m.harness = (m.harness + 1) % len(harnessChoices)
			case "a":
				return m.toggleRestriction()
			case "u":
				m.catalogUnused = !m.catalogUnused
				m.localLoaded = false
			}
			return m.reload()
		}
	default:
		m.localPager.scrollKey(k)
	}
	return m, nil
}

type localItem struct {
	title, section string
	entry          *catalog.Entry
	window         *quota.Window
}

func (m Model) localItems() []localItem {
	if !m.localLoaded {
		return nil
	}
	var out []localItem
	if m.mode == catalogMode {
		kind := catalogKinds[m.localCategory]
		for i := range m.catalogResult.Entries {
			entry := &m.catalogResult.Entries[i]
			name, category := entry.Name.String(), entry.Kind.String()
			if (kind != "" && kind != category) || !m.localFilter.matches(name+" "+category+" "+entry.Harness.String()+" "+entry.Scope.String()) {
				continue
			}
			if entry.Disabled {
				name += " (disabled)"
			}
			out = append(out, localItem{title: name, entry: entry})
		}
		return out
	}
	for _, provider := range []string{"Codex", "Claude Code"} {
		found := false
		for i := range m.quotaResult {
			window := &m.quotaResult[i]
			if window.Provider == provider {
				found = true
				if m.localFilter.matches(provider + " " + window.Label) {
					out = append(out, localItem{title: window.Label, section: provider, window: window})
				}
			}
		}
		if !found && m.localFilter.matches(provider) {
			out = append(out, localItem{title: "Quota unavailable", section: provider})
		}
	}
	return out
}

func (m Model) catalogSessions() (Model, tea.Cmd) {
	if m.mode == catalogMode && m.localDetail {
		if entry := m.selectedCatalogMCP(); entry != nil {
			return m.openRelation(reporting.SessionRelation{
				Kind: reporting.RelationMCP, Value: entry.Name.String(), Servers: entry.ObservedServers(), Harness: entry.Harness,
			}, "MCP · "+entry.Name.String()+" · "+entry.Harness.String())
		}
	}
	return m, nil
}

func (m Model) selectedCatalogMCP() *catalog.Entry {
	rows := m.localItems()
	if m.localSelection >= 0 && m.localSelection < len(rows) {
		entry := rows[m.localSelection].entry
		if entry != nil && entry.Kind.String() == "mcp" && entry.Tracked && entry.Uses > 0 {
			return entry
		}
	}
	return nil
}

func (m *Model) moveLocalSelection(step int) {
	rows := m.localItems()
	if len(rows) == 0 {
		return
	}
	m.localSelection = max(0, min(m.localSelection+step, len(rows)-1))
	m.layoutLocalData()
	if m.localSelection < len(m.localPositions) {
		m.localPager.reveal(m.localPositions[m.localSelection])
	}
}

func (m *Model) layoutLocalData() {
	if m.mode != catalogMode && m.mode != quotasMode {
		return
	}
	p := &m.localPager
	height := m.height - m.extraTitleLines() - 4
	if m.mode == catalogMode && !m.localDetail {
		height--
	}
	if !m.localDetail {
		height -= m.localFilter.rows()
	}
	p.display = max(1, height)
	p.lines = nil
	m.localPositions = nil
	rows := m.localItems()
	switch {
	case !m.localLoaded:
		p.lines = []string{"Loading…"}
		if !m.pending && m.err != nil {
			p.lines = []string{"Catalog unavailable."}
		}
	case len(rows) == 0:
		p.lines = []string{"No entries match these filters."}
		if m.mode == catalogMode && m.catalogUnused && m.localFilter.value == "" {
			p.lines = []string{"No unused items in these filters."}
		}
	case m.localDetail:
		m.localSelection = min(m.localSelection, len(rows)-1)
		item := rows[m.localSelection]
		p.lines = append([]string{titleStyle.Render(textfmt.Printable(item.title)), ""}, localItemDetails(item, m.width)...)
		if m.relations.Queries != nil && m.selectedCatalogMCP() != nil {
			p.lines = append(p.lines, "", "s  Sessions with observed calls")
		}
	default:
		m.localSelection = min(m.localSelection, len(rows)-1)
		if m.mode == catalogMode && len(m.catalogResult.Skipped) > 0 {
			p.lines = append(p.lines, textfmt.Plural(len(m.catalogResult.Skipped), "configuration skipped", "configurations skipped"), "")
		}
		section := ""
		for i, item := range rows {
			if item.section != section {
				if i > 0 {
					p.lines = append(p.lines, "")
				}
				section = item.section
				p.lines = append(p.lines, titleStyle.Render(section))
			} else if i > 0 && m.mode == catalogMode {
				p.lines = append(p.lines, "")
			}
			m.localPositions = append(m.localPositions, len(p.lines))
			marker := "  "
			if i == m.localSelection {
				marker = "> "
			}
			p.lines = append(p.lines, marker+textfmt.Printable(item.title))
			if item.entry != nil {
				p.lines = append(p.lines, "  "+catalogActivity(*item.entry)+" · "+item.entry.Harness.String()+" "+item.entry.Kind.String())
			}
			if item.window != nil {
				p.lines = append(p.lines, quotaSummary(*item.window, m.width))
				if !item.window.ResetAt.IsZero() {
					p.lines = append(p.lines, "  Resets "+item.window.ResetAt.Local().Format("Jan 2, 15:04"))
				}
			}
		}
	}
	p.scrollBy(0)
}

func (m Model) localDataView() string {
	title := modeNames[m.mode]
	if m.mode == catalogMode && m.catalogUnused {
		title += " · Unused only"
	}
	if m.localDetail {
		title += " › Details"
	}
	body := []string{m.titleBar(), titleStyle.Render(title)}
	if !m.localDetail {
		if m.mode == catalogMode {
			body = append(body, readingTabs(catalogTabs, m.localCategory, m.width))
		}
		if m.localFilter.rows() > 0 {
			body = append(body, m.localFilter.view())
		}
	}
	status := m.status()
	if m.mode == quotasMode {
		status = ""
	}
	body = append(body, m.localPager.visible(), status, m.help())
	return lipgloss.NewStyle().MaxWidth(m.width).Render(strings.Join(body, "\n"))
}

func catalogActivity(entry catalog.Entry) string {
	if !entry.Tracked {
		return "Usage unknown"
	}
	return textfmt.Number(entry.Uses) + " " + entry.Unit().String()
}

func localItemDetails(item localItem, width int) []string {
	if entry := item.entry; entry != nil {
		lines := []string{
			"Harness     " + entry.Harness.String(),
			"Kind        " + entry.Kind.String(),
			"Scope       " + entry.Scope.String(),
			"Activity    " + catalogActivity(*entry),
		}
		if plugin := entry.Plugin(); plugin != "" {
			lines = append(lines, "Plugin      "+textfmt.Printable(plugin))
		}
		if !entry.Last.IsZero() {
			lines = append(lines, "Last used   "+entry.Last.Local().Format("2006-01-02 15:04"))
		}
		return lines
	}
	if w := item.window; w != nil {
		lines := []string{w.Provider + " · account", "", quotaSummary(*w, width)}
		if !w.ResetAt.IsZero() {
			lines = append(lines, "Resets    "+w.ResetAt.Local().Format("Jan 2, 15:04 MST"))
		} else {
			lines = append(lines, "Reset time unavailable")
		}
		if !w.Observed.IsZero() {
			lines = append(lines, "Checked   "+w.Observed.Local().Format("Jan 2, 15:04 MST"))
		}
		return lines
	}
	return []string{item.section + " · account"}
}

func quotaSummary(window quota.Window, width int) string {
	value := fmt.Sprintf("  %.1f%% remaining", window.Remaining)
	if window.Remaining <= 10 {
		value += " · low"
	}
	if n := min(20, width-lipgloss.Width(value)-2); n >= 5 {
		value += "  " + quotaBar(window, n)
	}
	return value
}

func quotaBar(window quota.Window, width int) string {
	filled := max(0, min(width, int(math.Round(window.Remaining/100*float64(width)))))
	if window.Remaining > 0 && filled == 0 {
		filled = 1
	}
	return chartStyle.Render(strings.Repeat("━", filled)) + strings.Repeat("·", width-filled)
}
