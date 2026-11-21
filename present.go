package main

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/Nathandelenclos/agent-stats/internal/kernel"
	reporting "github.com/Nathandelenclos/agent-stats/internal/reporting/application"
)

// jsonUsage is the --json form of report.Usage: timestamps in Unix milliseconds, 0 when unknown.
type jsonUsage struct {
	Requests      int64   `json:"requests"`
	Failed        int64   `json:"failed"`
	Input         int64   `json:"input"`
	Output        int64   `json:"output"`
	CacheRead     int64   `json:"cacheRead"`
	CacheWrite    int64   `json:"cacheWrite"`
	Cost          float64 `json:"cost"`
	Unpriced      int64   `json:"unpriced"`
	AvgDurationMs float64 `json:"avgDurationMs"`
	AvgTTFTMs     float64 `json:"avgTtftMs"`
	CacheRate     float64 `json:"cacheRate"`
	FirstTS       int64   `json:"firstTs"`
	LastTS        int64   `json:"lastTs"`
}

type jsonGroup struct {
	Key string `json:"key"`
	jsonUsage
}

type jsonTool struct {
	Name        string  `json:"name"`
	Server      string  `json:"server"`
	Calls       int64   `json:"calls"`
	Errors      int64   `json:"errors"`
	ArgsChars   int64   `json:"argsChars"`
	ResultChars int64   `json:"resultChars"`
	AvgMs       float64 `json:"avgMs"`
	LastTS      int64   `json:"lastTs"`
}

type jsonReport struct {
	Total     jsonUsage   `json:"total"`
	ByHarness []jsonGroup `json:"byHarness"`
	ByModel   []jsonGroup `json:"byModel"`
	ByFolder  []jsonGroup `json:"byFolder"`
	ByAgent   []jsonGroup `json:"byAgentType"`
	Tools     []jsonTool  `json:"tools"`
}

func toJSON(r reporting.Report) jsonReport {
	groups := func(in []reporting.UsageGroup) []jsonGroup {
		var out []jsonGroup
		for _, g := range in {
			out = append(out, jsonGroup{Key: g.Group, jsonUsage: usageJSON(g.Usage)})
		}
		return out
	}
	out := jsonReport{Total: usageJSON(r.Total), ByHarness: groups(r.ByHarness), ByModel: groups(r.ByModel),
		ByFolder: groups(r.ByFolder), ByAgent: groups(r.ByAgent)}
	for _, t := range r.Tools {
		out.Tools = append(out.Tools, jsonTool{t.Name, t.Server, t.Calls, t.Errors, t.ArgsChars, t.ResultChars, t.AvgMs,
			kernel.UnixMilli(t.Last)})
	}
	return out
}

func usageJSON(u reporting.Usage) jsonUsage {
	return jsonUsage{u.Requests, u.Failed, u.Input, u.Output, u.CacheRead, u.CacheWrite, u.CostUSD, u.Unpriced,
		u.AvgDurationMs, u.AvgTTFTMs, u.CacheRate(), kernel.UnixMilli(u.First), kernel.UnixMilli(u.Last)}
}

func printReport(r reporting.Report) {
	t := r.Total
	if t.Requests == 0 {
		fmt.Println("No requests in this range.")
		return
	}
	fmt.Printf("%s requests (%s failed) · %s · %s in / %s out · cache %.0f%% · %s → %s\n",
		num(t.Requests), num(t.Failed), usd(t.CostUSD), num(t.Input+t.CacheRead+t.CacheWrite), num(t.Output), t.CacheRate()*100,
		day(t.First), day(t.Last))
	if t.Unpriced > 0 {
		fmt.Printf("%s requests have no known price (pass --prices to add them)\n", num(t.Unpriced))
	}
	usageTable("HARNESS", r.ByHarness, 0)
	usageTable("MODEL", r.ByModel, 15)
	usageTable("FOLDER", r.ByFolder, 10)
	usageTable("AGENT", r.ByAgent, 10)

	fmt.Println()
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', tabwriter.AlignRight)
	fmt.Fprintln(w, "TOOL\tMCP SERVER\tCALLS\tERRORS\tRESULT CHARS\t")
	for i, x := range r.Tools {
		if i == 20 {
			break
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t\n", x.Name, x.Server, num(x.Calls), num(x.Errors), num(x.ResultChars))
	}
	w.Flush()
}

func usageTable(title string, rows []reporting.UsageGroup, limit int) {
	fmt.Println()
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', tabwriter.AlignRight)
	fmt.Fprintln(w, title+"\tREQUESTS\tFAILED\tTOKENS IN\tTOKENS OUT\tCACHE\tCOST\t")
	for i, x := range rows {
		if limit > 0 && i == limit {
			fmt.Fprintf(w, "… %d more\t\t\t\t\t\t\t\n", len(rows)-limit)
			break
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%.0f%%\t%s\t\n", shorten(x.Group, 50), num(x.Requests), num(x.Failed),
			num(x.Input+x.CacheRead+x.CacheWrite), num(x.Output), x.CacheRate()*100, usd(x.CostUSD))
	}
	w.Flush()
}
