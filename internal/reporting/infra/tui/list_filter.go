package tui

import (
	"strings"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
)

type listFilter struct {
	value   string
	editing bool
	draft   []rune
}

func (f listFilter) rows() int {
	if f.editing || f.value != "" {
		return 1
	}
	return 0
}

func (f listFilter) view() string {
	if f.editing {
		return "Filter: " + string(f.draft) + "▌"
	}
	return "Filter: " + f.value + " · Esc clears"
}

func (f *listFilter) begin() {
	f.draft, f.editing = []rune(f.value), true
}

// handle reports whether Enter applied the filter or Escape closed the editor.
func (f *listFilter) handle(msg tea.KeyMsg) bool {
	switch k := msg.String(); k {
	case "enter":
		f.value = strings.TrimSpace(string(f.draft))
		fallthrough
	case "esc":
		f.draft, f.editing = nil, false
		return true
	case "backspace", "ctrl+h":
		if len(f.draft) > 0 {
			f.draft = append([]rune(nil), f.draft[:len(f.draft)-1]...)
		}
	case "ctrl+u":
		f.draft = nil
	default:
		if msg.Type == tea.KeyRunes || msg.Type == tea.KeySpace {
			draft := append([]rune(nil), f.draft...)
			for _, ch := range k {
				if !unicode.IsControl(ch) && len(draft) < 160 {
					draft = append(draft, ch)
				}
			}
			f.draft = draft
		}
	}
	return false
}

func (m *Model) startListFilter(f *listFilter) (Model, tea.Cmd) {
	f.begin()
	m.layout()
	return *m, nil
}

func (m Model) updateListFilter(msg tea.KeyMsg) (Model, tea.Cmd) {
	f := &m.usageFilter
	if m.localFilter.editing {
		f = &m.localFilter
	}
	if f.handle(msg) {
		if f == &m.localFilter {
			m.localSelection, m.localPager.scroll = 0, 0
		}
		m.layout()
	}
	return m, nil
}

func (f listFilter) matches(text string) bool {
	return f.value == "" || strings.Contains(strings.ToLower(text), strings.ToLower(f.value))
}
