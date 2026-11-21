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

	"github.com/Nathandelenclos/agent-stats/internal/ingest"
	"github.com/Nathandelenclos/agent-stats/internal/pricing"
	"github.com/Nathandelenclos/agent-stats/internal/store"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "agent-stats:", err)
		os.Exit(1)
	}
}

func run() error {
	cache, _ := os.UserCacheDir()
	dbPath := flag.String("db", filepath.Join(cache, "agent-stats", "index.db"), "index database")
	pricesPath := flag.String("prices", "", "JSON file overriding embedded prices: {\"model\": [input, output, cacheRead, cacheWrite]} in USD per token")
	since := flag.String("since", "", "only count activity after this: 24h, 7d, 30d or 2006-01-02")
	harness := flag.String("harness", "", "only one harness: claude, codex, omp, pi")
	asJSON := flag.Bool("json", false, "print the report as JSON")
	noSync := flag.Bool("no-sync", false, "skip re-reading the logs")
	flag.Parse()

	from, err := parseSince(*since, time.Now())
	if err != nil {
		return err
	}
	prices, err := pricing.Load(*pricesPath)
	if err != nil {
		return err
	}
	st, err := store.Open(*dbPath, prices, currentUser())
	if err != nil {
		return err
	}
	defer st.Close()

	if !*noSync {
		home, _ := os.UserHomeDir()
		rep, err := st.Sync(ingest.DefaultSources(home), func(r store.SyncReport) {
			fmt.Fprintf(os.Stderr, "\rindexing… %d files", r.Parsed)
		})
		if rep.Parsed >= 50 {
			fmt.Fprintln(os.Stderr)
		}
		if err != nil {
			return err
		}
		for _, e := range rep.Errors {
			fmt.Fprintln(os.Stderr, "skipped", e)
		}
	}

	rep, err := buildReport(st, store.Filter{Since: from, Harness: *harness})
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

type report struct {
	Total     store.Usage     `json:"total"`
	ByHarness []store.Row     `json:"byHarness"`
	ByModel   []store.Row     `json:"byModel"`
	ByFolder  []store.Row     `json:"byFolder"`
	ByAgent   []store.Row     `json:"byAgentType"`
	Tools     []store.ToolRow `json:"tools"`
}

func buildReport(st *store.Store, f store.Filter) (report, error) {
	var r report
	var err error
	if r.Total, err = st.Total(f); err != nil {
		return r, err
	}
	groups := []struct {
		dst  *[]store.Row
		expr string
	}{
		{&r.ByHarness, "s.harness"},
		{&r.ByModel, "r.model"},
		{&r.ByFolder, "COALESCE(s.folder, '')"},
		{&r.ByAgent, "s.agent_type"},
	}
	for _, g := range groups {
		if *g.dst, err = st.UsageBy(f, g.expr); err != nil {
			return r, err
		}
	}
	r.Tools, err = st.Tools(f)
	return r, err
}

func printReport(r report) {
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

func usageTable(title string, rows []store.Row, limit int) {
	fmt.Println()
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', tabwriter.AlignRight)
	fmt.Fprintln(w, title+"\tREQUESTS\tFAILED\tTOKENS IN\tTOKENS OUT\tCACHE\tCOST\t")
	for i, x := range rows {
		if limit > 0 && i == limit {
			fmt.Fprintf(w, "… %d more\t\t\t\t\t\t\t\n", len(rows)-limit)
			break
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%.0f%%\t%s\t\n", shorten(x.Key, 50), num(x.Requests), num(x.Failed),
			num(x.Input+x.CacheRead+x.CacheWrite), num(x.Output), x.CacheRate*100, usd(x.Cost))
	}
	w.Flush()
}

// parseSince accepts a duration with a day unit (7d), a Go duration (12h) or a date.
func parseSince(s string, now time.Time) (int64, error) {
	if s == "" {
		return 0, nil
	}
	if n, ok := strings.CutSuffix(s, "d"); ok {
		if d, err := strconv.Atoi(n); err == nil {
			return now.AddDate(0, 0, -d).UnixMilli(), nil
		}
	}
	if d, err := time.ParseDuration(s); err == nil {
		return now.Add(-d).UnixMilli(), nil
	}
	if t, err := time.ParseInLocation("2006-01-02", s, time.Local); err == nil {
		return t.UnixMilli(), nil
	}
	return 0, fmt.Errorf("invalid --since %q: use 24h, 7d or 2006-01-02", s)
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
