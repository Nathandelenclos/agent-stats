package tui

import (
	"context"
	"fmt"
	"strings"
	"sync"
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

// detailChromeLines is what a session's detail draws besides its body: title, heading, status and help, and a spare line.
const detailChromeLines = 5

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
	seq             int
	period, harness int // the filters the report was asked for
	report          reporting.Report
	err             error
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
	reports reporting.BuildReport
	lister  reporting.ListSessions // without queries, the sessions mode is off
	shower  reporting.ShowSession
	sync    Sync // nil when indexing is off
	syncs   *syncRuns
	now     func() time.Time

	periods []period // the presets, led by a custom period when the command line asked for one
	period  int
	harness int                           // index in harnessChoices
	shown   struct{ period, harness int } // the filters of the report on screen: the title names these, not the pending choice
	seq     int                           // the latest report request; older answers are ignored
	pending bool

	syncing  bool
	indexed  int
	syncNote string
	syncErr  error
	spinner  spinner.Model

	mode          mode
	sessions      sessionsState
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
	m := Model{reports: o.Reports, lister: o.Sessions, shower: o.Session, sessions: newSessionsState(), sync: o.Sync, syncs: newSyncRuns(), syncing: o.Sync != nil, spinner: spinner.New(spinner.WithSpinner(spinner.MiniDot)), now: time.Now, table: t, width: 80, height: 24, periods: periods}
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
	m.shown.period, m.shown.harness = m.period, m.harness // nothing on screen yet: the title names the first request
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

// progressEvery is the shortest time between two progress messages: each one redraws the screen, and a
// sync that indexes files by the hundred per second would otherwise keep the interface busy drawing counts.
const progressEvery = 100 * time.Millisecond

// syncRuns tells the syncs to stop when the interface closes, and waits for them: they write to an index
// that is closed right after.
type syncRuns struct {
	ctx    context.Context
	cancel context.CancelFunc
	mu     sync.Mutex // orders begin after stop
	runs   sync.WaitGroup
}

func newSyncRuns() *syncRuns {
	ctx, cancel := context.WithCancel(context.Background())
	return &syncRuns{ctx: ctx, cancel: cancel}
}

// begin registers a sync about to run; it is false once the syncs were stopped.
func (s *syncRuns) begin() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ctx.Err() != nil {
		return false
	}
	s.runs.Add(1)
	return true
}

// stop cancels the syncs and returns once they ended.
func (s *syncRuns) stop() {
	s.mu.Lock()
	s.cancel()
	s.mu.Unlock()
	s.runs.Wait()
}

// stopSync is what closing the interface does about a sync still running.
func (m Model) stopSync() { m.syncs.stop() }

// listen is the command that waits for the next message of a sync, or for the interface to close.
func (m Model) listen(events <-chan tea.Msg) tea.Cmd {
	done := m.syncs.ctx.Done()
	return func() tea.Msg {
		select {
		case msg := <-events:
			return msg
		case <-done:
			return syncDoneMsg{err: context.Canceled}
		}
	}
}

// startSync runs the sync off the UI loop and streams its progress back as messages.
func (m Model) startSync() tea.Cmd {
	index, now, runs := m.sync, m.now, m.syncs
	return func() tea.Msg {
		if !runs.begin() {
			return syncDoneMsg{err: context.Canceled}
		}
		events := make(chan tea.Msg, 8)
		go func() {
			defer runs.runs.Done()
			var mu sync.Mutex
			var last time.Time // when the latest progress message was sent
			note, err := index(runs.ctx, func(n int) {
				mu.Lock()
				t := now()
				skip := !last.IsZero() && t.Sub(last) < progressEvery
				if !skip {
					last = t
				}
				mu.Unlock()
				if skip {
					return
				}
				select {
				case events <- syncProgressMsg{n, events}:
				default: // the screen is behind: the next count will do
				}
			})
			select {
			case events <- syncDoneMsg{note, err}:
			case <-runs.ctx.Done(): // the interface closed: nobody reads any more
			}
		}()
		return m.listen(events)()
	}
}

// query is what the filters currently ask for.
func (m Model) query() reporting.Query {
	return reporting.Query{Since: m.periods[m.period].since(m.now()), Harness: harnessChoices[m.harness]}
}

// load asks the use case for the report of the current filters, off the UI loop.
func (m Model) load() tea.Cmd {
	reports, q, seq, period, harness := m.reports, m.query(), m.seq, m.period, m.harness
	return func() tea.Msg {
		r, err := reports.Run(q)
		return reportMsg{seq, period, harness, r, err}
	}
}

// reload asks for a new report after a filter changed; answers to earlier requests no longer count.
func (m Model) reload() (Model, tea.Cmd) {
	m.seq++
	m.pending = true
	if m.mode == sessionsMode {
		if m.sessions.detail.open { // the session on screen may have grown too
			detail := m.loadDetail(m.sessions.detail.id) // before the return: loadDetail counts the request in m
			return m, tea.Batch(m.loadSessions(), detail)
		}
		return m, m.loadSessions()
	}
	return m, m.load()
}

// Update handles messages and key presses.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		if msg.Width > 0 && msg.Height > 0 { // a terminal that reports no size (a bare pseudo-terminal) keeps the default
			m.width, m.height = msg.Width, msg.Height
		}
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
			m.shown.period, m.shown.harness = msg.period, msg.harness
		}
		m.layout()
		return m, nil
	case sessionsMsg:
		if msg.seq != m.seq {
			return m, nil
		}
		m.pending = false
		m.err = msg.err
		if msg.err == nil {
			m.sessions.list, m.sessions.loaded = msg.list, true
			m.shown.period, m.shown.harness = msg.period, msg.harness
		}
		m.layout()
		return m, nil
	case detailMsg:
		if d := &m.sessions.detail; d.open && d.id == msg.id && msg.seq == m.sessions.detailSeq {
			d.err = msg.err
			if msg.err == nil {
				d.data, d.loaded = msg.detail, true
			}
			m.layout()
		}
		return m, nil
	case syncProgressMsg:
		m.indexed = msg.indexed
		return m, m.listen(msg.events)
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
	if m.mode == sessionsMode {
		if m.sessions.detail.open {
			return m.detailKey(msg)
		}
		return m.sessionsKey(msg)
	}
	switch k := msg.String(); k {
	case "q", "ctrl+c":
		m.syncs.cancel() // Run waits for the sync to stop once the program has
		return m, tea.Quit
	case "tab", "right":
		m.selectTab((m.tab + 1) % tabCount)
	case "shift+tab", "left":
		m.selectTab((m.tab + tabCount - 1) % tabCount)
	case "1", "2", "3", "4", "5":
		m.selectTab(tab(k[0] - '1'))
	case "m":
		return m.switchMode()
	case "r":
		return m.reindex()
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

// reindex starts a sync when indexing is on and none is running.
func (m Model) reindex() (Model, tea.Cmd) {
	if m.sync == nil || m.syncing {
		return m, nil
	}
	m.syncing, m.indexed, m.syncNote, m.syncErr = true, 0, "", nil
	return m, tea.Batch(m.startSync(), m.spinner.Tick)
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

// layout fills the tables with the current report and fits them to the terminal.
func (m *Model) layout() {
	fit(&m.table, m.grid(), m.sorts[m.tab], m.width, m.height)
	sg := m.sessionsGrid()
	if m.sessions.sort.col >= len(sg.headers) { // sorted by a column this terminal cannot hold
		m.sessions.sort = reportOrder
	}
	selected := m.selectedSession()
	m.sessions.shown = nil
	for _, i := range sg.order(m.sessions.sort) {
		m.sessions.shown = append(m.sessions.shown, m.sessions.list.Sessions[i])
	}
	fit(&m.sessions.table, sg, m.sessions.sort, m.width, m.height)
	for i, s := range m.sessions.shown { // the selection follows its session, not its row: fit may have put the cursor back at the top
		if s.ID == selected {
			m.sessions.table.SetCursor(i)
		}
	}
	if d := &m.sessions.detail; d.open {
		d.display = max(4, m.height-detailChromeLines)
		if d.loaded {
			d.lines = detailLines(d.data, m.width)
		}
		d.scrollBy(0) // a longer terminal or a shorter body leaves no empty page
	}
}

// fit fills t with the rows of g ordered by s and fits it to a terminal of the given size.
func fit(t *table.Model, g grid, s sortState, width, height int) {
	g.rows = g.sorted(s)
	widths := columnWidths(g)
	// The first column takes what the others leave; each column also pays two cells of padding.
	rest := width
	for i := 1; i < len(widths); i++ {
		rest -= widths[i] + 2
	}
	widths[0] = max(minFirstColumn, min(widths[0], rest-2))

	cols := make([]table.Column, len(g.headers))
	for i, h := range g.headers {
		cols[i] = table.Column{Title: pad(h, widths[i], g.numeric[i]), Width: widths[i]}
	}
	if s.col >= 0 {
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
	if len(t.Columns()) != len(cols) {
		// bubbles/table indexes the old rows by the new columns: never let their counts differ. Only a switch
		// to a tab with another column count gets here (selectTab puts the cursor back at the top anyway);
		// clearing on every resize would lose the scroll.
		t.SetRows(nil)
	}
	t.SetColumns(cols)
	t.SetRows(rows)     // keeps the cursor and the scroll, clamped to a shorter report
	if t.Cursor() < 0 { // emptied above, or the report had no row
		t.SetCursor(0)
	}
	t.SetWidth(width)
	t.SetHeight(max(4, height-chromeLines))
	st := table.DefaultStyles()
	st.Header, st.Selected, st.Cell = headerStyle, selectedStyle, lipgloss.NewStyle().Padding(0, 1)
	t.SetStyles(st)
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
	if m.mode == sessionsMode {
		return m.sessionsView()
	}
	var b strings.Builder
	b.WriteString(m.titleBar() + "\n")
	b.WriteString(m.tabBar() + "\n")
	b.WriteString(m.summary() + "\n")
	if m.loaded && !m.empty() {
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
	if h := harnessChoices[m.shown.harness]; !h.IsZero() {
		harness = h.String()
	}
	bar := titleStyle.Render("agent-stats") + m.modeLabel() + faintStyle.Render(fmt.Sprintf("  period: %s · harness: %s", m.periods[m.shown.period].label, harness))
	if m.syncing {
		bar += "  " + m.spinner.View() + " indexing…"
		if m.indexed > 0 {
			bar += fmt.Sprintf(" %s files", textfmt.Number(int64(m.indexed)))
		}
	}
	return bar
}

// modeLabel shows which mode is on screen, the other one in plain text beside it; empty without a sessions mode.
func (m Model) modeLabel() string {
	if !m.sessionsEnabled() {
		return ""
	}
	parts := make([]string, len(modeNames))
	for i, name := range modeNames {
		if mode(i) == m.mode {
			parts[i] = activeTab.UnsetPadding().Render("[" + name + "]")
		} else {
			parts[i] = name
		}
	}
	return "  " + strings.Join(parts, " ")
}

// help lists the keys, most useful first: on a narrow terminal the last ones are left out, never "q quit".
func (m Model) help() string {
	var keys, filters []string
	switch {
	case m.mode == sessionsMode && m.sessions.detail.open:
		keys = []string{"↑↓ pgup/pgdn scroll", "esc back to sessions"}
	case m.mode == sessionsMode:
		keys, filters = []string{"↑↓ scroll", "enter open"}, []string{"s sort", "o reverse", "p period", "h harness"}
	default:
		keys, filters = []string{"tab/←→ tabs", "↑↓ scroll"}, []string{"s sort", "o reverse", "p period", "h harness"}
	}
	if m.sessionsEnabled() {
		keys = append(keys, "m "+strings.ToLower(modeNames[1-m.mode]))
	}
	if m.sync != nil {
		keys = append(keys, "r re-index")
	}
	keys = append(keys, filters...)
	line := func(k []string) string { return strings.Join(append(k[:len(k):len(k)], "q quit"), " · ") }
	for m.width > 0 && len(keys) > 0 && lipgloss.Width(line(keys)) > m.width {
		keys = keys[:len(keys)-1]
	}
	return line(keys)
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
	if m.empty() {
		return emptyMessages[m.tab] + "\n"
	}
	t := m.report.Total
	if t.Requests == 0 { // tool calls and requests are filtered by their own timestamps: only the former may be left
		return noRequests + "\n"
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

// emptyMessages say, per tab, what is missing when its table has no row.
var emptyMessages = [tabCount]string{harnessTab: noRequests, modelTab: noRequests, folderTab: noRequests, agentTab: noRequests, toolsTab: "No tool calls in this range."}

const noRequests = "No requests in this range."

// empty reports whether the current tab has nothing to tabulate.
func (m Model) empty() bool { return len(m.table.Rows()) == 0 }

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
	if n := m.unpriced(); n > 0 {
		parts = append(parts, faintStyle.Render(fmt.Sprintf("%s requests have no known price", textfmt.Number(n))))
	}
	return strings.Join(parts, " · ")
}

// unpriced counts the requests of what is on screen that have no known price.
func (m Model) unpriced() int64 {
	if m.mode == usageMode {
		return m.report.Total.Unpriced
	}
	return m.sessions.list.Total.Unpriced
}
