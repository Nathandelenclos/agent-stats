package tui

import (
	"fmt"
	"slices"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Nathandelenclos/agent-stats/internal/platform/textfmt"
	reporting "github.com/Nathandelenclos/agent-stats/internal/reporting/application"
)

type readingSearch struct{ listFilter }

func (s readingSearch) view(width int, label string) string {
	if s.editing {
		draft := s.draft
		for len(draft) > 0 && lipgloss.Width(label+": "+string(draft)+"▌") > width {
			draft = draft[1:]
		}
		return label + ": " + string(draft) + "▌"
	}
	if s.value != "" {
		return label + ": " + s.value + " · Esc clear"
	}
	return ""
}

func (s *readingSearch) cancel() bool {
	if s.value == "" {
		return false
	}
	s.value = ""
	return true
}

func matchesReading(text, query string) bool {
	return strings.Contains(strings.ToLower(text), strings.ToLower(query))
}

type readingRow struct {
	title string
	note  string
}

// readingList keeps the selected result visible without losing its position when a detail is opened.
type readingList struct {
	cursor, top int
	shown       []int
	search      readingSearch
	pager
}

func (l *readingList) layout(rows []readingRow, width, height int, empty string) {
	l.shown = nil
	for i, r := range rows {
		if l.search.value == "" || matchesReading(r.title+" "+r.note, l.search.value) {
			l.shown = append(l.shown, i)
		}
	}
	l.display = max(1, height)
	l.lines = nil
	if len(l.shown) == 0 {
		if l.search.value != "" {
			empty = "No matches. Esc clears the filter."
		}
		l.cursor, l.top = 0, 0
		l.lines = []string{empty}
		return
	}
	visible := max(1, height/2)
	l.cursor = max(0, min(l.cursor, len(l.shown)-1))
	l.top = max(0, min(l.top, l.cursor, len(l.shown)-visible))
	l.top = max(l.top, l.cursor-visible+1)
	cut := lipgloss.NewStyle().MaxWidth(max(1, width))
	for i := l.top; i < min(l.top+visible, len(l.shown)); i++ {
		row := rows[l.shown[i]]
		preview := textfmt.Excerpt(row.title, max(1, width-2))
		title := "  " + preview
		if i == l.cursor {
			title = titleStyle.Render("> " + preview)
		}
		l.lines = append(l.lines, cut.Render(title))
		if height > 1 {
			l.lines = append(l.lines, cut.Render("    "+textfmt.Printable(row.note)))
		}
	}
}

func (l readingList) selected() int {
	if len(l.shown) == 0 {
		return -1
	}
	return l.shown[l.cursor]
}

func (l *readingList) move(k string) {
	switch k {
	case "left", "right":
		return
	case "up", "k":
		l.cursor--
	case "down", "j":
		l.cursor++
	case "pgup", "b":
		l.cursor -= max(1, l.display/2)
	case "pgdown", "f", " ":
		l.cursor += max(1, l.display/2)
	case "home", "g":
		l.cursor = 0
	case "end", "G":
		l.cursor = len(l.shown) - 1
	}
}

type readerState struct {
	open      bool
	title     string
	label     string
	text      string
	notice    string
	search    readingSearch
	matches   []int
	highlight int
	source    reporting.SessionID
	pending   bool
	pager
}

func (r *readerState) layout(width, height int) {
	r.display = max(1, height-r.search.rows())
	r.lines = nil
	if r.label != "" {
		r.lines = append(r.lines, wrappedText(r.label, width)...)
		r.lines = append(r.lines, "")
	}
	if r.pending {
		r.lines = append(r.lines, "Loading full prompt…", "")
	}
	if r.notice != "" {
		r.lines = append(r.lines, wrappedText(r.notice, width)...)
		r.lines = append(r.lines, "")
	}
	r.matches = nil
	for _, line := range strings.Split(readableText(r.text), "\n") {
		parts := wrapLine(line, width)
		for _, offset := range wrappedMatches(line, parts, r.search.value) {
			r.matches = append(r.matches, len(r.lines)+offset)
		}
		r.lines = append(r.lines, parts...)
	}
	if len(r.matches) > 0 {
		r.highlight = min(r.highlight, len(r.matches)-1)
		at := r.matches[r.highlight]
		r.lines[at] = titleStyle.Render(r.lines[at])
	} else if r.search.value != "" {
		r.lines = append([]string{"No matches.", ""}, r.lines...)
	}
	r.scrollBy(0)
}

// Search the original line and place each hit on the segment where it begins.
func wrappedMatches(line string, parts []string, query string) []int {
	if query == "" || !matchesReading(line, query) {
		return nil
	}
	line, query = strings.ToLower(line), strings.ToLower(query)
	var hits []int
	start, next := 0, strings.Index(line, query)
	for row, part := range parts {
		if part == "" || next < 0 {
			continue
		}
		part = strings.ToLower(part)
		pos := strings.Index(line[start:], part)
		if pos < 0 {
			continue
		}
		start += pos + len(part)
		for next >= 0 && next < start {
			hits = append(hits, row)
			from := next + len(query)
			next = strings.Index(line[from:], query)
			if next >= 0 {
				next += from
			}
		}
	}
	return hits
}

// Keep paragraphs and code indentation while neutralising terminal controls.
func readableText(raw string) string {
	raw = strings.ReplaceAll(raw, "\r\n", "\n")
	raw = strings.ReplaceAll(raw, "\t", "    ")
	lines := strings.Split(raw, "\n")
	for i := range lines {
		lines[i] = textfmt.Printable(lines[i])
	}
	return strings.Join(lines, "\n")
}

func wrappedText(raw string, width int) []string {
	var lines []string
	for _, line := range strings.Split(readableText(raw), "\n") {
		lines = append(lines, wrapLine(line, width)...)
	}
	return lines
}

func wrapLine(line string, width int) []string {
	// Lip Gloss wraps at display cells, including wide characters and grapheme clusters.
	rendered := strings.Split(lipgloss.NewStyle().Width(max(1, width)).Render(line), "\n")
	for i := range rendered {
		rendered[i] = strings.TrimRight(rendered[i], " ")
	}
	return rendered
}

func (r *readerState) goToMatch(step int) {
	n := len(r.matches)
	if n == 0 {
		return
	}
	r.highlight = (r.highlight + step + n) % n
	r.scrollBy(r.matches[r.highlight] - r.scroll)
}

func (m Model) readingPage(trail, tabs, input, body string) string {
	lines := []string{m.titleBar(), titleStyle.Render(trail)}
	if tabs != "" {
		lines = append(lines, tabs)
	}
	if input != "" {
		lines = append(lines, input)
	}
	lines = append(lines, body, m.status(), m.help())
	return lipgloss.NewStyle().MaxWidth(m.width).Render(strings.Join(lines, "\n"))
}

func (m Model) readerView(r readerState, trail string) string {
	heading := trail + " › " + r.title
	for lipgloss.Width(heading) > m.width {
		_, tail, ok := strings.Cut(heading, " › ")
		if !ok || tail == r.title {
			break
		}
		heading = tail
	}
	return m.readingPage(heading, "", r.search.view(m.width, "Find"), r.visible())
}

func readingTabs(names []string, selected, width int) string {
	parts := slices.Clone(names)
	parts[selected] = activeTab.UnsetPadding().Render("[" + parts[selected] + "]")
	line := strings.Join(parts, "  ")
	if lipgloss.Width(line) <= width {
		return line
	}
	return parts[selected] + "  ·  ← → sections"
}

func (m *Model) activeReader() *readerState {
	switch {
	case m.mode == sessionsMode && m.sessions.detail.reader.open:
		return &m.sessions.detail.reader
	case m.mode == promptsMode && m.prompts.reader.open:
		return &m.prompts.reader
	}
	return nil
}

func (m *Model) activeReadingSearch() *readingSearch {
	if r := m.activeReader(); r != nil {
		return &r.search
	}
	switch m.mode {
	case sessionsMode:
		d := &m.sessions.detail
		if d.open {
			if d.section != sessionSummarySection {
				return &d.list.search
			}
			return nil
		}
		return &m.sessions.search
	case promptsMode:
		return &m.prompts.activeList().search
	}
	return nil
}

func (m Model) readingInputOpen() bool {
	s := m.activeReadingSearch()
	return s != nil && s.editing
}

// readingInputKey gives the search editor priority over mode and quit shortcuts.
func (m Model) readingInputKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	s := m.activeReadingSearch()
	if !s.handle(msg) {
		return m, nil
	}
	r := m.activeReader()
	if r != nil && msg.String() == "enter" {
		r.highlight = 0
	}
	m.layout()
	if r != nil && msg.String() == "enter" {
		r.goToMatch(0)
	}
	return m, nil
}

func (m Model) readingAtModeRoot() bool {
	switch m.mode {
	case sessionsMode:
		return !m.sessions.detail.open && m.sessions.search.value == "" && !m.sessions.search.editing
	case promptsMode:
		return !m.prompts.reader.open && !m.prompts.group.open && m.prompts.list.search.value == "" && !m.prompts.list.search.editing
	}
	return true
}

func (m Model) readingShortcuts() []string {
	if r := m.activeReader(); r != nil {
		keys := []string{"↑↓ scroll", "/ find"}
		if r.source != (reporting.SessionID{}) && m.sessionsEnabled() {
			keys = append([]string{"s source session"}, keys...)
		}
		if r.search.value != "" {
			keys = append(keys, "n/N matches")
		}
		return append(keys, "esc back")
	}
	if m.mode == sessionsMode {
		d := m.sessions.detail
		if d.section == sessionSummarySection {
			return []string{"←→ sections", "enter prompts", "esc back to sessions"}
		}
		return []string{"←→ sections", "enter open", "/ filter", "esc back"}
	}
	return []string{"←→ sections", "enter open", "/ filter", "↑↓ select", "esc back"}
}

func (m Model) readerKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	r := m.activeReader()
	switch k := msg.String(); k {
	case "esc", "backspace":
		if !r.search.cancel() {
			*r = readerState{}
		}
	case "/":
		r.search.begin()
	case "n":
		r.goToMatch(1)
	case "N":
		r.goToMatch(-1)
	case "s":
		if r.source != (reporting.SessionID{}) && m.sessionsEnabled() {
			return m.openPromptSession(r.source)
		}
	case "m":
		return m.switchMode()
	case "r":
		return m.reindex()
	default:
		r.scrollKey(k)
		return m, nil
	}
	m.layout()
	return m, nil
}

type promptTextMsg struct {
	seq    int
	id     reporting.SessionID
	prompt reporting.Prompt
	err    error
}

func (m Model) openSessionPrompt(p reporting.Prompt, index int) (tea.Model, tea.Cmd) {
	d := &m.sessions.detail
	d.reader = readerState{open: true, title: fmt.Sprintf("Prompt #%d", index+1),
		label: promptLabel(p.At, p.Chars, p.Signals), text: p.Text}
	var cmd tea.Cmd
	if p.Position > 0 && m.promptText.Queries != nil {
		m.sessions.promptSeq++
		seq, id, query, reader, ctx := m.sessions.promptSeq, d.id, m.query(), m.promptText, m.syncs.ctx
		d.reader.pending = true
		d.promptPreview = p
		cmd = func() tea.Msg {
			full, err := reader.Run(ctx, id, p.Position, query)
			return promptTextMsg{seq, id, full, err}
		}
	} else if p.Chars > len([]rune(p.Text)) {
		d.reader.notice = "Only the indexed preview is available."
	}
	m.layout()
	return m, cmd
}

func (m Model) updatePromptText(msg promptTextMsg) (tea.Model, tea.Cmd) {
	d := &m.sessions.detail
	r := &d.reader
	if m.mode != sessionsMode || !d.open || !r.pending || d.id != msg.id || m.sessions.promptSeq != msg.seq {
		return m, nil
	}
	r.pending = false
	p := d.promptPreview
	switch {
	case msg.err != nil:
		r.notice = "Full prompt unavailable: " + textfmt.Printable(msg.err.Error())
	case msg.prompt.Position != p.Position || !msg.prompt.At.Equal(p.At) || msg.prompt.Chars != p.Chars || !strings.HasPrefix(msg.prompt.Text, p.Text):
		r.notice = "Prompt changed in the index. Reopen its session to read the full text."
	default:
		r.text = msg.prompt.Text
	}
	m.layout()
	return m, nil
}
