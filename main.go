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
	"time"

	indexing "github.com/Nathandelenclos/agent-stats/internal/indexing/application"
	"github.com/Nathandelenclos/agent-stats/internal/indexing/infra/logfiles"
	"github.com/Nathandelenclos/agent-stats/internal/indexing/infra/sqlite"
	"github.com/Nathandelenclos/agent-stats/internal/kernel"
	"github.com/Nathandelenclos/agent-stats/internal/pricing/infra/pricetable"
	reporting "github.com/Nathandelenclos/agent-stats/internal/reporting/application"
	usagedb "github.com/Nathandelenclos/agent-stats/internal/reporting/infra/sqlite"
	"github.com/Nathandelenclos/agent-stats/internal/reporting/infra/tui"
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
	var only kernel.Harness // empty: every harness
	if *harness != "" {
		if only, err = kernel.ParseHarness(*harness); err != nil {
			return err
		}
	}
	prices, err := pricetable.Load(*pricesPath)
	if err != nil {
		return err
	}
	index, err := sqlite.Open(sqlite.Options{Path: *dbPath, User: currentUser()})
	if err != nil {
		return err
	}
	defer index.Close()

	if !*noSync {
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		logs := logfiles.NewFileSessionLogs(logfiles.DefaultSources(home))
		rep, err := indexing.IndexSessions{Logs: logs, Store: index, Prices: prices}.Run(func(r indexing.IndexReport) {
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

	usage, err := usagedb.OpenReadOnly(*dbPath)
	if err != nil {
		return err
	}
	defer usage.Close()
	if interactive(*asJSON, os.Stdin, os.Stdout) {
		return tui.Run(tui.Options{Reports: reporting.BuildReport{Queries: usage}, Harness: only, SinceLabel: *since, Since: from})
	}
	rep, err := reporting.BuildReport{Queries: usage}.Run(reporting.Query{Since: from, Harness: only})
	if err != nil {
		return err
	}
	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(toJSON(rep))
	}
	printReport(rep)
	return nil
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

func usd(c reporting.USD) string { return fmt.Sprintf("$%.2f", float64(c)) }

// day formats t as a date, an unknown time reading as the Unix epoch.
func day(t time.Time) string {
	if t.IsZero() {
		t = time.UnixMilli(0)
	}
	return t.Format("2006-01-02")
}
