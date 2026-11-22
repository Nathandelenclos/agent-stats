package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Nathandelenclos/agent-stats/internal/platform/textfmt"
	reporting "github.com/Nathandelenclos/agent-stats/internal/reporting/application"
)

// tab is one view of the report.
type tab int

const (
	harnessTab tab = iota
	modelTab
	folderTab
	agentTab
	toolsTab
	tabCount
)

var tabNames = [tabCount]string{"Harness", "Model", "Folder", "Agent", "Tools"}

var (
	titleStyle    = lipgloss.NewStyle().Bold(true)
	activeTab     = lipgloss.NewStyle().Bold(true).Reverse(true).Padding(0, 1)
	inactiveTab   = lipgloss.NewStyle().Padding(0, 1)
	faintStyle    = lipgloss.NewStyle().Faint(true)
	errorStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	headerStyle   = lipgloss.NewStyle().Bold(true).Padding(0, 1)
	selectedStyle = lipgloss.NewStyle().Reverse(true)
)

// chromeLines is what the view draws besides the table: title, tabs, two summary lines, status and help.
const chromeLines = 7

// reportMsg carries the result of a report request.
type reportMsg struct {
	report reporting.Report
	err    error
}

// Model is the Bubble Tea model of the interface.
type Model struct {
	reports Reports
	query   reporting.Query

	tab           tab
	report        reporting.Report
	loaded        bool
	err           error
	table         table.Model
	width, height int
}

// New returns the model for o. Its Init asks for the first report.
func New(o Options) Model {
	t := table.New(table.WithFocused(true))
	return Model{reports: o.Reports, query: o.Query, table: t, width: 80, height: 24}
}

// Init asks for the first report.
func (m Model) Init() tea.Cmd { return m.load() }

// load asks the use case for the report of the current query, off the UI loop.
func (m Model) load() tea.Cmd {
	reports, q := m.reports, m.query
	return func() tea.Msg {
		r, err := reports.Run(q)
		return reportMsg{r, err}
	}
}

// Update handles messages and key presses.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.layout()
		return m, nil
	case reportMsg:
		m.err = msg.err
		if msg.err == nil {
			m.report, m.loaded = msg.report, true
		}
		m.layout()
		return m, nil
	case tea.KeyMsg:
		return m.key(msg)
	}
	return m, nil
}

func (m Model) key(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k := msg.String(); k {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "tab", "right":
		m.selectTab((m.tab + 1) % tabCount)
	case "shift+tab", "left":
		m.selectTab((m.tab + tabCount - 1) % tabCount)
	case "1", "2", "3", "4", "5":
		m.selectTab(tab(k[0] - '1'))
	default:
		var cmd tea.Cmd
		m.table, cmd = m.table.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m *Model) selectTab(t tab) {
	m.tab = t
	m.layout()
	m.table.SetCursor(0)
}

// grid is the table of the current tab.
func (m Model) grid() grid {
	r := m.report
	switch m.tab {
	case modelTab:
		return usageGrid("MODEL", r.ByModel)
	case folderTab:
		return usageGrid("FOLDER", r.ByFolder)
	case agentTab:
		return usageGrid("AGENT", r.ByAgent)
	case toolsTab:
		return toolsGrid(r.Tools)
	}
	return usageGrid("HARNESS", r.ByHarness)
}

// layout fills the table with the current tab and fits it to the terminal.
func (m *Model) layout() {
	g := m.grid()
	widths := make([]int, len(g.headers))
	for i, h := range g.headers {
		widths[i] = lipgloss.Width(h)
		for _, r := range g.rows {
			widths[i] = max(widths[i], lipgloss.Width(r[i].text))
		}
	}
	// The first column takes what the others leave; each column also pays two cells of padding.
	rest := m.width
	for i := 1; i < len(widths); i++ {
		rest -= widths[i] + 2
	}
	widths[0] = max(10, min(widths[0], rest-2))

	cols := make([]table.Column, len(g.headers))
	for i, h := range g.headers {
		cols[i] = table.Column{Title: pad(h, widths[i], g.numeric[i]), Width: widths[i]}
	}
	rows := make([]table.Row, len(g.rows))
	for i, r := range g.rows {
		row := make(table.Row, len(r))
		for j, c := range r {
			row[j] = pad(c.text, widths[j], g.numeric[j])
		}
		rows[i] = row
	}
	m.table.SetRows(nil) // bubbles/table indexes old rows by the new columns, so never leave them mismatched
	m.table.SetColumns(cols)
	m.table.SetRows(rows)
	m.table.SetWidth(m.width)
	m.table.SetHeight(max(4, m.height-chromeLines))
	s := table.DefaultStyles()
	s.Header, s.Selected, s.Cell = headerStyle, selectedStyle, lipgloss.NewStyle().Padding(0, 1)
	m.table.SetStyles(s)
}

// pad right-aligns numbers and left-aligns text in a column of width w.
func pad(s string, w int, right bool) string {
	gap := w - lipgloss.Width(s)
	if gap <= 0 {
		return s
	}
	if right {
		return strings.Repeat(" ", gap) + s
	}
	return s
}

// View draws the interface.
func (m Model) View() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("agent-stats") + "\n")
	b.WriteString(m.tabBar() + "\n")
	b.WriteString(m.summary() + "\n")
	if m.loaded && m.report.Total.Requests > 0 {
		b.WriteString(m.table.View() + "\n")
	} else {
		b.WriteString(m.emptyBody() + "\n")
	}
	b.WriteString(m.status() + "\n")
	b.WriteString(faintStyle.Render("tab/←→ tabs · ↑↓ scroll · q quit"))
	return lipgloss.NewStyle().MaxWidth(m.width).Render(b.String()) // cut what a narrow terminal cannot show
}

func (m Model) tabBar() string {
	parts := make([]string, tabCount)
	for i, name := range tabNames {
		label := fmt.Sprintf("%d %s", i+1, name)
		if tab(i) == m.tab {
			parts[i] = activeTab.Render(label)
		} else {
			parts[i] = inactiveTab.Render(label)
		}
	}
	return strings.Join(parts, "")
}

// summary is the report's totals on two lines, in the words of the plain-text report.
func (m Model) summary() string {
	if !m.loaded {
		return faintStyle.Render("loading…") + "\n"
	}
	t := m.report.Total
	if t.Requests == 0 {
		return "No requests in this range.\n"
	}
	first, last := "—", "—"
	if !t.First.IsZero() {
		first = t.First.Format("2006-01-02")
	}
	if !t.Last.IsZero() {
		last = t.Last.Format("2006-01-02")
	}
	return fmt.Sprintf("%s requests (%s failed) · $%.2f\n%s in / %s out · cache %.0f%% · %s → %s",
		textfmt.Number(t.Requests), textfmt.Number(t.Failed), float64(t.Cost), textfmt.Number(t.InputWithCache()),
		textfmt.Number(t.Output), t.CacheRate()*100, first, last)
}

// emptyBody fills the table's place while there is nothing to tabulate.
func (m Model) emptyBody() string {
	return strings.Repeat("\n", max(0, m.height-chromeLines-1))
}

func (m Model) status() string {
	if m.err != nil {
		return errorStyle.Render("error: " + m.err.Error())
	}
	if t := m.report.Total; m.loaded && t.Unpriced > 0 {
		return faintStyle.Render(fmt.Sprintf("%s requests have no known price", textfmt.Number(t.Unpriced)))
	}
	return ""
}
