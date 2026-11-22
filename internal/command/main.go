package command

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	indexing "github.com/Nathandelenclos/agent-stats/internal/indexing/application"
	"github.com/Nathandelenclos/agent-stats/internal/indexing/infra/logfiles"
	"github.com/Nathandelenclos/agent-stats/internal/indexing/infra/sqlite"
	"github.com/Nathandelenclos/agent-stats/internal/kernel"
	"github.com/Nathandelenclos/agent-stats/internal/platform/termquiet"
	"github.com/Nathandelenclos/agent-stats/internal/pricing/infra/pricetable"
	"github.com/Nathandelenclos/agent-stats/internal/quota"
	reporting "github.com/Nathandelenclos/agent-stats/internal/reporting/application"
	reportcli "github.com/Nathandelenclos/agent-stats/internal/reporting/infra/cli"
	usagedb "github.com/Nathandelenclos/agent-stats/internal/reporting/infra/sqlite"
	"github.com/Nathandelenclos/agent-stats/internal/reporting/infra/tui"
)

// indexRun brings the index up to date with the session logs, calling progress as files are indexed,
// until ctx is done.
type indexRun func(ctx context.Context, progress func(indexing.IndexReport)) (indexing.IndexReport, error)

func newIndexRun(index *sqlite.SQLiteIndex, prices indexing.PriceList) (indexRun, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	uc := indexing.IndexSessions{Logs: logfiles.NewFileSessionLogs(logfiles.DefaultSources(home)), Store: index, Prices: prices, Workers: indexWorkers()}
	return uc.RunContext, nil
}

// indexWorkers is how many logs are read at once: three quarters of the CPUs the program may use (at least one),
// which leaves the writer, the garbage collector and an interface that is drawing a core to run on.
func indexWorkers() int {
	cpus := runtime.GOMAXPROCS(0)
	return max(1, cpus-cpus/4)
}

// indexBeforeReport indexes while the plain-text report waits, narrating on stderr so stdout stays the report.
func indexBeforeReport(run indexRun) error {
	return indexForReport(run, func(r indexing.IndexReport) {
		fmt.Fprintf(os.Stderr, "\rindexing… %d files", r.Indexed)
	})
}

func indexForReport(run indexRun, progress func(indexing.IndexReport)) error {
	rep, err := run(context.Background(), progress)
	if progress != nil && rep.Indexed >= 50 {
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
	return nil
}

// Run executes agent-stats using the process arguments and standard streams.
func Run() error {
	if len(os.Args) > 1 && os.Args[1] == "serve" {
		return runServe(os.Args[2:])
	}
	termquiet.Restore()
	if len(os.Args) == 3 && os.Args[1] == "quota" && os.Args[2] == "record-claude" {
		path := quotaFile()
		if path == "" {
			return fmt.Errorf("no user cache directory for Claude quota")
		}
		return quota.RecordClaudeStatus(os.Stdin, path, time.Now())
	}
	if len(os.Args) > 1 && os.Args[1] == "catalog" {
		return runCatalog(os.Args[2:], os.Stdout, os.Stderr)
	}
	defaultDB := ""
	if cache, err := os.UserCacheDir(); err == nil {
		defaultDB = filepath.Join(cache, "agent-stats", "index.db")
	}
	dbPath := flag.String("db", defaultDB, "index database (default: agent-stats/index.db in the user cache directory)")
	pricesPath := flag.String("prices", "", "JSON file overriding embedded prices: {\"model\": [input, output, cacheRead, cacheWrite]} in USD per token")
	since := flag.String("since", "", "only count activity after this: 24h, 7d, 30d or 2006-01-02")
	harness := flag.String("harness", "", "only one harness (default: all)")
	asJSON := flag.Bool("json", false, "print the report as JSON")
	plain := flag.Bool("plain", false, "print a static text report with daily values, even on a terminal")
	noSync := flag.Bool("no-sync", false, "skip re-reading the logs")
	all := flag.Bool("all", false, "count every project, not only the one of the current folder (it wins over --project)")
	projectDir := flag.String("project", "", "only count the sessions that worked in this folder or below it (default: the project of the current folder, if sessions worked in it)")
	flag.Parse()
	sub, err := subcommand(flag.CommandLine)
	if err != nil {
		return err
	}

	if *dbPath == "" {
		return fmt.Errorf("no user cache directory: pass --db")
	}
	from, err := sinceOf(sub, *since, time.Now())
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

	var indexLogs indexRun
	if !*noSync {
		if indexLogs, err = newIndexRun(index, prices); err != nil {
			return err
		}
	}
	ui := sub == "" && !*plain && interactive(*asJSON, os.Stdout)
	if indexLogs != nil && !ui { // the interface indexes in the background instead
		if *plain {
			err = indexForReport(indexLogs, nil)
		} else {
			err = indexBeforeReport(indexLogs)
		}
		if err != nil {
			return err
		}
	}

	usage, err := usagedb.OpenReadOnly(*dbPath)
	if err != nil {
		return err
	}
	defer usage.Close()
	detect := reporting.DetectProject{Queries: usage, Home: homeProject()}.Run
	cov, err := coverageOf(*all, *projectDir, detect)
	if err != nil {
		return err
	}
	if sub == promptsCommand {
		return runPrompts(os.Stdout, usage.Prompts(), reporting.Query{Since: from, Harness: only}, cov, *asJSON)
	}
	if ui {
		sessions := usage.Sessions()
		o := tui.Options{Reports: reporting.BuildTimelineReport{Queries: usage}, Harness: only, SinceLabel: *since, Since: from,
			Sessions: reporting.ListSessions{Queries: sessions}, Session: reporting.ShowSession{Queries: sessions},
			PromptText: reporting.ShowSessionPrompt{Queries: sessions}, RelatedSessions: reporting.ListRelatedSessions{Queries: sessions},
			Prompts: reporting.ReviewPrompts{Queries: usage.Prompts()},
			Catalog: interactiveCatalog(*dbPath, *projectDir), Quotas: readQuotas,
			Project: cov.project, Restricted: cov.restricted, Detect: redetection(*all, *projectDir, detect)}
		if indexLogs != nil {
			o.Sync = tuiSync(indexLogs)
		}
		return tui.Run(o)
	}
	q := reporting.Query{Since: from, Harness: only, Project: cov.restriction()}
	if *plain && !*asJSON {
		rep, err := (reporting.BuildTimelineReport{Queries: usage}).Run(context.Background(), q)
		if err != nil {
			return err
		}
		reportcli.WriteText(os.Stdout, rep.Report, cov.label())
		reportcli.WriteDaily(os.Stdout, rep.Daily)
		return nil
	}
	rep, err := reporting.BuildReport{Queries: usage}.Run(context.Background(), q)
	if err != nil {
		return err
	}
	if *asJSON {
		return reportcli.WriteJSON(os.Stdout, rep)
	}
	reportcli.WriteText(os.Stdout, rep, cov.label())
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
