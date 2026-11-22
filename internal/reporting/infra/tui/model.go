package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Nathandelenclos/agent-stats/internal/kernel"
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

// period is a span of activity to report, ending now.
type period struct {
	label string
	since func(now time.Time) time.Time // the zero time stands for everything
}

var periods = []period{
	{"24h", func(now time.Time) time.Time { return now.Add(-24 * time.Hour) }},
	{"7d", func(now time.Time) time.Time { return now.AddDate(0, 0, -7) }},
	{"30d", func(now time.Time) time.Time { return now.AddDate(0, 0, -30) }},
	{"all", func(time.Time) time.Time { return time.Time{} }},
}

// harnessChoices are the harness filters in the order h cycles through them: all, then each harness.
var harnessChoices = append([]kernel.Harness{{}}, kernel.Harnesses...)

// reportMsg carries the result of a report request; seq tells which request it answers.
type reportMsg struct {
	seq    int
	report reporting.Report
	err    error
}

// syncProgressMsg reports files indexed so far; events is the stream to keep listening to.
type syncProgressMsg struct {
	indexed int
	events  <-chan tea.Msg
}

// syncDoneMsg ends a sync.
type syncDoneMsg struct {
	note string
	err  error
}

// Model is the Bubble Tea model of the interface.
type Model struct {
	reports Reports
	sync    Sync // nil when indexing is off
	now     func() time.Time

	periods []period // the presets, led by a custom period when the command line asked for one
	period  int
	harness int // index in harnessChoices
	seq     int // the latest report request; older answers are ignored
	pending bool

	syncing  bool
	indexed  int
	syncNote string
	syncErr  error
	spinner  spinner.Model

	tab           tab
	sorts         [tabCount]sortState // each tab keeps its own order
	report        reporting.Report
	loaded        bool
	err           error
	table         table.Model
	width, height int
}

// New returns the model for o. Its Init asks for the first report.
func New(o Options) Model {
	t := table.New(table.WithFocused(true))
	m := Model{reports: o.Reports, sync: o.Sync, syncing: o.Sync != nil, spinner: spinner.New(spinner.WithSpinner(spinner.MiniDot)), now: time.Now, table: t, width: 80, height: 24, periods: periods}
	m.period = len(periods) - 1 // all
	if o.SinceLabel != "" {
		m.period = -1
		for i, p := range periods {
			if p.label == o.SinceLabel {
				m.period = i
			}
		}
		if m.period < 0 { // a custom period leads the presets
			since := o.Since
			m.periods = append([]period{{o.SinceLabel, func(time.Time) time.Time { return since }}}, periods...)
			m.period = 0
		}
	}
	for i, h := range harnessChoices {
		if h == o.Harness {
			m.harness = i
		}
	}
	for i := range m.sorts {
		m.sorts[i] = reportOrder
	}
	return m
}

// Init asks for the first report and, when indexing is on, starts it beside the report.
func (m Model) Init() tea.Cmd {
	if m.sync == nil {
		return m.load()
	}
	return tea.Batch(m.load(), m.startSync(), m.spinner.Tick)
}

// startSync runs the sync off the UI loop and streams its progress back as messages.
func (m Model) startSync() tea.Cmd {
	sync := m.sync
	return func() tea.Msg {
		events := make(chan tea.Msg, 8)
		go func() {
			note, err := sync(func(n int) {
				select {
				case events <- syncProgressMsg{n, events}:
				default: // the screen is behind: the next count will do
				}
			})
			events <- syncDoneMsg{note, err}
		}()
		return <-events
	}
}

// query is what the filters currently ask for.
func (m Model) query() reporting.Query {
	return reporting.Query{Since: m.periods[m.period].since(m.now()), Harness: harnessChoices[m.harness]}
}

// load asks the use case for the report of the current filters, off the UI loop.
func (m Model) load() tea.Cmd {
	reports, q, seq := m.reports, m.query(), m.seq
	return func() tea.Msg {
		r, err := reports.Run(q)
		return reportMsg{seq, r, err}
	}
}

// reload asks for a new report after a filter changed; answers to earlier requests no longer count.
func (m Model) reload() (Model, tea.Cmd) {
	m.seq++
	m.pending = true
	return m, m.load()
}

// Update handles messages and key presses.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.layout()
		return m, nil
	case reportMsg:
		if msg.seq != m.seq {
			return m, nil
		}
		m.pending = false
		m.err = msg.err
		if msg.err == nil {
			m.report, m.loaded = msg.report, true
		}
		m.layout()
		return m, nil
	case syncProgressMsg:
		m.indexed = msg.indexed
		return m, func() tea.Msg { return <-msg.events }
	case syncDoneMsg:
		m.syncing, m.syncNote, m.syncErr = false, msg.note, msg.err
		return m.reload() // the index changed: show it
	case spinner.TickMsg:
		if !m.syncing {
			return m, nil // stops the animation
		}
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
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
	case "r":
		if m.sync == nil || m.syncing {
			return m, nil
		}
		m.syncing, m.indexed, m.syncNote, m.syncErr = true, 0, "", nil
		return m, tea.Batch(m.startSync(), m.spinner.Tick)
	case "p":
		m.period = (m.period + 1) % len(m.periods)
		return m.reload()
	case "h":
		m.harness = (m.harness + 1) % len(harnessChoices)
		return m.reload()
	case "s":
		m.sorts[m.tab] = m.sorts[m.tab].next(m.grid())
		m.layout()
		m.table.SetCursor(0)
	case "o":
		if s := m.sorts[m.tab]; s.col >= 0 {
			m.sorts[m.tab].desc = !s.desc
			m.layout()
			m.table.SetCursor(0)
		}
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
	g.rows = g.sorted(m.sorts[m.tab])
	widths := make([]int, len(g.headers))
	for i, h := range g.headers {
		widths[i] = lipgloss.Width(h) + 1 // room for the sort arrow
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
	if s := m.sorts[m.tab]; s.col >= 0 {
		cols[s.col].Title = pad(g.headers[s.col]+arrow(s.desc), widths[s.col], g.numeric[s.col])
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

func arrow(desc bool) string {
	if desc {
		return "▼"
	}
	return "▲"
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
	b.WriteString(m.titleBar() + "\n")
	b.WriteString(m.tabBar() + "\n")
	b.WriteString(m.summary() + "\n")
	if m.loaded && m.report.Total.Requests > 0 {
		b.WriteString(m.table.View() + "\n")
	} else {
		b.WriteString(m.emptyBody() + "\n")
	}
	b.WriteString(m.status() + "\n")
	b.WriteString(faintStyle.Render(m.help()))
	return lipgloss.NewStyle().MaxWidth(m.width).Render(b.String()) // cut what a narrow terminal cannot show
}

// titleBar names the program and the filters that apply to the report.
func (m Model) titleBar() string {
	harness := "all"
	if h := harnessChoices[m.harness]; !h.IsZero() {
		harness = h.String()
	}
	bar := titleStyle.Render("agent-stats") + faintStyle.Render(fmt.Sprintf("  period: %s · harness: %s", m.periods[m.period].label, harness))
	if m.syncing {
		bar += "  " + m.spinner.View() + " indexing…"
		if m.indexed > 0 {
			bar += fmt.Sprintf(" %s files", textfmt.Number(int64(m.indexed)))
		}
	}
	return bar
}

func (m Model) help() string {
	keys := "tab/←→ tabs · ↑↓ scroll · s sort · o reverse · p period · h harness"
	if m.sync != nil {
		keys += " · r re-index"
	}
	return keys + " · q quit"
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

// status lists what the user should know besides the table: failures, work in flight, notes.
func (m Model) status() string {
	var parts []string
	if m.err != nil {
		parts = append(parts, errorStyle.Render("error: "+textfmt.Printable(m.err.Error())))
	}
	if m.syncErr != nil {
		parts = append(parts, errorStyle.Render("indexing failed: "+textfmt.Printable(m.syncErr.Error())))
	}
	if m.pending {
		parts = append(parts, faintStyle.Render("updating…"))
	}
	if m.syncNote != "" {
		parts = append(parts, faintStyle.Render(textfmt.Printable(m.syncNote)))
	}
	if t := m.report.Total; m.loaded && t.Unpriced > 0 {
		parts = append(parts, faintStyle.Render(fmt.Sprintf("%s requests have no known price", textfmt.Number(t.Unpriced))))
	}
	return strings.Join(parts, " · ")
}
