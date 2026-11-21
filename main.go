// Command agent-stats indexes the session logs of coding agents (Claude Code, Codex, OMP, pi)
// and reports usage across all of them.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/Nathandelenclos/agent-stats/internal/app"
	"github.com/Nathandelenclos/agent-stats/internal/ingest"
	"github.com/Nathandelenclos/agent-stats/internal/pricing"
	"github.com/Nathandelenclos/agent-stats/internal/store"
	"github.com/Nathandelenclos/agent-stats/internal/usage"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "agent-stats:", err)
		os.Exit(1)
	}
}

func run() error {
	defaultDB := ""
	if cache, err := os.UserCacheDir(); err == nil {
		defaultDB = filepath.Join(cache, "agent-stats", "index.db")
	}
	dbPath := flag.String("db", defaultDB, "index database (default: agent-stats/index.db in the user cache directory)")
	pricesPath := flag.String("prices", "", "JSON file overriding embedded prices: {\"model\": [input, output, cacheRead, cacheWrite]} in USD per token")
	since := flag.String("since", "", "only count activity after this: 24h, 7d, 30d or 2006-01-02")
	harness := flag.String("harness", "", "only one harness: claude, codex, omp, pi")
	asJSON := flag.Bool("json", false, "print the report as JSON")
	noSync := flag.Bool("no-sync", false, "skip re-reading the logs")
	flag.Parse()

	if *dbPath == "" {
		return fmt.Errorf("no user cache directory: pass --db")
	}
	from, err := parseSince(*since, time.Now())
	if err != nil {
		return err
	}
	var only usage.Harness // empty: every harness
	if *harness != "" {
		if only, err = usage.ParseHarness(*harness); err != nil {
			return err
		}
	}
	prices, err := pricing.Load(*pricesPath)
	if err != nil {
		return err
	}
	index, err := store.Open(*dbPath, currentUser())
	if err != nil {
		return err
	}
	defer index.Close()

	if !*noSync {
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		logs := ingest.NewFileSessionLogs(ingest.DefaultSources(home))
		rep, err := app.IndexSessions{Logs: logs, Index: index, Prices: prices}.Run(func(r app.IndexReport) {
			fmt.Fprintf(os.Stderr, "\rindexing… %d files", r.Indexed)
		})
		if rep.Indexed >= 50 {
			fmt.Fprintln(os.Stderr)
		}
		if err != nil {
			return err
		}
		for _, e := range rep.Errors {
			fmt.Fprintln(os.Stderr, "skipped", e)
		}
		if rep.SkippedLines > 0 {
			fmt.Fprintf(os.Stderr, "skipped %d unreadable log lines (malformed or over 64 MiB)\n", rep.SkippedLines)
		}
	}

	rep, err := app.BuildReport{Index: index}.Run(app.Filter{Since: from, Harness: only})
	if err != nil {
		return err
	}
	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(rep)
	}
	printReport(rep)
	return nil
}

func printReport(r app.Report) {
	t := r.Total
	if t.Requests == 0 {
		fmt.Println("No requests in this range.")
		return
	}
	fmt.Printf("%s requests (%s failed) · %s · %s in / %s out · cache %.0f%% · %s → %s\n",
		num(t.Requests), num(t.Failed), usd(t.Cost), num(t.Input+t.CacheRead+t.CacheWrite), num(t.Output), t.CacheRate*100,
		day(t.FirstTS), day(t.LastTS))
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

func usageTable(title string, rows []app.UsageGroup, limit int) {
	fmt.Println()
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', tabwriter.AlignRight)
	fmt.Fprintln(w, title+"\tREQUESTS\tFAILED\tTOKENS IN\tTOKENS OUT\tCACHE\tCOST\t")
	for i, x := range rows {
		if limit > 0 && i == limit {
			fmt.Fprintf(w, "… %d more\t\t\t\t\t\t\t\n", len(rows)-limit)
			break
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%.0f%%\t%s\t\n", shorten(x.Group, 50), num(x.Requests), num(x.Failed),
			num(x.Input+x.CacheRead+x.CacheWrite), num(x.Output), x.CacheRate*100, usd(x.Cost))
	}
	w.Flush()
}

// parseSince accepts a duration with a day unit (7d), a Go duration (12h) or a date; zero when empty.
func parseSince(s string, now time.Time) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	if n, ok := strings.CutSuffix(s, "d"); ok {
		if d, err := strconv.Atoi(n); err == nil {
			return now.AddDate(0, 0, -d), nil
		}
	}
	if d, err := time.ParseDuration(s); err == nil {
		return now.Add(-d), nil
	}
	if t, err := time.ParseInLocation("2006-01-02", s, time.Local); err == nil {
		return t, nil
	}
	return time.Time{}, fmt.Errorf("invalid --since %q: use 24h, 7d or 2006-01-02", s)
}

func currentUser() string {
	name := os.Getenv("USER")
	if u, err := user.Current(); err == nil {
		name = u.Username
	}
	host, _ := os.Hostname()
	return name + "@" + host
}

func num(n int64) string {
	s := strconv.FormatInt(n, 10)
	for i := len(s) - 3; i > 0 && s[i-1] != '-'; i -= 3 {
		s = s[:i] + " " + s[i:]
	}
	return s
}

func usd(f float64) string { return fmt.Sprintf("$%.2f", f) }

func day(ms int64) string { return time.UnixMilli(ms).Format("2006-01-02") }

func shorten(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return "…" + string(r[len(r)-n+1:])
	}
	return s
}
