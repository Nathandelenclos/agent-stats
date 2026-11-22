package tui

import (
	"fmt"
	"slices"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/Nathandelenclos/agent-stats/internal/platform/textfmt"
	reporting "github.com/Nathandelenclos/agent-stats/internal/reporting/application"
)

// cell is one displayed value; n is what a numeric column sorts by.
type cell struct {
	text string
	n    float64
}

// grid is a report table as it is displayed: headers, which columns hold numbers, and the rows.
type grid struct {
	headers []string
	numeric []bool // right-aligned columns
	rows    [][]cell
}

// minFirstColumn is the room the first column keeps at least; every column also pays two cells of padding.
const minFirstColumn = 10

// columnWidths is the room each column needs: its header and a sort arrow, or its widest cell.
func columnWidths(g grid) []int {
	widths := make([]int, len(g.headers))
	for i, h := range g.headers {
		widths[i] = lipgloss.Width(h) + 1
		for _, r := range g.rows {
			widths[i] = max(widths[i], lipgloss.Width(r[i].text))
		}
	}
	return widths
}

// narrowedTo keeps the leading columns that fit a terminal of the given width beside a first column squeezed to its
// minimum; the columns after them are left out whole rather than cut. Columns come in order of importance.
func (g grid) narrowedTo(width int) grid {
	widths := columnWidths(g)
	room, keep := width-minFirstColumn-2, 1
	for keep < len(widths) && room >= widths[keep]+2 {
		room -= widths[keep] + 2
		keep++
	}
	g.headers, g.numeric = g.headers[:keep], g.numeric[:keep]
	rows := make([][]cell, len(g.rows))
	for i, r := range g.rows {
		rows[i] = r[:keep]
	}
	g.rows = rows
	return g
}

func textCell(s string) cell { return cell{text: textfmt.Printable(s)} }

func countCell(n int64) cell { return cell{textfmt.Number(n), float64(n)} }

// usageGrid lays out usage groups; first titles the column holding the group name.
func usageGrid(first string, groups []reporting.UsageGroup) grid {
	g := grid{
		headers: []string{first, "REQUESTS", "FAILED", "TOKENS IN", "TOKENS OUT", "CACHE", "COST"},
		numeric: []bool{false, true, true, true, true, true, true},
	}
	for _, x := range groups {
		g.rows = append(g.rows, []cell{
			textCell(x.Group),
			countCell(x.Requests),
			countCell(x.Failed),
			countCell(x.InputWithCache()),
			countCell(x.Output),
			{fmt.Sprintf("%.0f%%", x.CacheRate()*100), x.CacheRate()},
			{fmt.Sprintf("$%.2f", float64(x.Cost)), float64(x.Cost)},
		})
	}
	return g
}

func toolsGrid(tools []reporting.ToolUsage) grid {
	g := grid{
		headers: []string{"TOOL", "MCP SERVER", "CALLS", "ERRORS", "RESULT CHARS"},
		numeric: []bool{false, false, true, true, true},
	}
	for _, t := range tools {
		g.rows = append(g.rows, []cell{
			textCell(t.Name), textCell(t.Server), countCell(t.Calls), countCell(t.Errors), countCell(t.ResultChars),
		})
	}
	return g
}

// sortState orders a table by one column. col -1 keeps the report's own order.
type sortState struct {
	col  int
	desc bool
}

var reportOrder = sortState{col: -1}

// next moves to the following column, back to the report's order after the last one.
// Numbers start with the largest, text in alphabetical order.
func (s sortState) next(g grid) sortState {
	col := s.col + 1
	if col >= len(g.headers) {
		return reportOrder
	}
	return sortState{col: col, desc: g.numeric[col]}
}

// order lists the indexes of the rows as s orders them; equal rows keep the report's order.
func (g grid) order(s sortState) []int {
	idx := make([]int, len(g.rows))
	for i := range idx {
		idx[i] = i
	}
	if s.col < 0 || s.col >= len(g.headers) {
		return idx
	}
	slices.SortStableFunc(idx, func(i, j int) int {
		a, b := g.rows[i], g.rows[j]
		var c int
		if g.numeric[s.col] {
			c = cmpFloat(a[s.col].n, b[s.col].n)
		} else {
			c = strings.Compare(strings.ToLower(a[s.col].text), strings.ToLower(b[s.col].text))
		}
		if s.desc {
			return -c
		}
		return c
	})
	return idx
}

// sorted returns the rows ordered by s.
func (g grid) sorted(s sortState) [][]cell {
	var rows [][]cell
	for _, i := range g.order(s) {
		rows = append(rows, g.rows[i])
	}
	return rows
}

func cmpFloat(a, b float64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}
