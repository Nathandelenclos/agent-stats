package tui

import (
	"fmt"

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
