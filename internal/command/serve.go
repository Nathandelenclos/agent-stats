package command

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/Nathandelenclos/agent-stats/internal/indexing/infra/sqlite"
	"github.com/Nathandelenclos/agent-stats/internal/kernel"
	"github.com/Nathandelenclos/agent-stats/internal/pricing/infra/pricetable"
	reporting "github.com/Nathandelenclos/agent-stats/internal/reporting/application"
	usagedb "github.com/Nathandelenclos/agent-stats/internal/reporting/infra/sqlite"
	"github.com/Nathandelenclos/agent-stats/internal/reporting/infra/web"
	webui "github.com/Nathandelenclos/agent-stats/web"
)

// runServe is `agent-stats serve`: the usage report in a browser, served from this machine.
func runServe(args []string) error {
	defaultDB := ""
	if cache, err := os.UserCacheDir(); err == nil {
		defaultDB = filepath.Join(cache, "agent-stats", "index.db")
	}
	fs := flag.NewFlagSet("agent-stats serve", flag.ContinueOnError)
	addr := fs.String("addr", "127.0.0.1:7373", "address to listen on; loopback only unless --allow-remote")
	allowRemote := fs.Bool("allow-remote", false, "accept a non-loopback --addr and any Host header: every machine that reaches the port can read the report and session metadata")
	dbPath := fs.String("db", defaultDB, "index database (default: agent-stats/index.db in the user cache directory)")
	pricesPath := fs.String("prices", "", "JSON file overriding embedded prices: {\"model\": [input, output, cacheRead, cacheWrite]} in USD per token")
	since := fs.String("since", "", "period the page opens on: 24h, 7d, 30d or 2006-01-02 (default: all)")
	harness := fs.String("harness", "", "harness the page opens on (default: all)")
	noSync := fs.Bool("no-sync", false, "skip re-reading the logs")
	all := fs.Bool("all", false, "open the page on every project, not only the one of the current folder (it wins over --project)")
	projectDir := fs.String("project", "", "the project the page offers: this folder or below it (default: the project of the current folder, if sessions worked in it)")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("serve takes no argument, got %q", fs.Arg(0))
	}
	if *dbPath == "" {
		return fmt.Errorf("no user cache directory: pass --db")
	}
	if err := web.ValidateAddr(*addr, *allowRemote); err != nil {
		return err
	}
	if _, err := web.ValidateSince(*since, time.Now(), parseSince); err != nil {
		return fmt.Errorf("invalid --since %q: %w", *since, err)
	}
	var only kernel.Harness
	if *harness != "" {
		var err error
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
	var sync web.Sync
	if !*noSync {
		indexLogs, err := newIndexRun(index, prices)
		if err != nil {
			return err
		}
		sync = web.Sync(tuiSync(indexLogs)) // the same adapter as the interface's: counts as progress, skipped files as the note
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

	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		return err
	}
	if !webui.Embedded {
		fmt.Fprintln(os.Stderr, "agent-stats: this binary has no web front, only the API answers (see README: go generate ./web, then go build -tags web .)")
	}
	if *allowRemote {
		fmt.Fprintln(os.Stderr, "agent-stats: --allow-remote: every machine that reaches this port can read the report and session metadata. Host headers are not checked.")
	}
	fmt.Fprintf(os.Stderr, "agent-stats: serving http://%s, covering: %s (Ctrl-C to stop)\n", ln.Addr(), cov.label())

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	sessions := usage.Sessions()
	return web.Serve(ctx, ln, web.Options{
		Reports: reporting.BuildTimelineReport{Queries: usage},
		TokenRates: func(model string) (web.TokenRates, bool) {
			rates, ok := prices.Lookup(model)
			return web.TokenRates(rates.PerToken()), ok
		},
		Sessions:        reporting.ListSessions{Queries: sessions},
		RelatedSessions: reporting.ListRelatedSessions{Queries: sessions},
		SessionRequests: reporting.ListSessionRequests{Queries: sessions},
		ErrorSignatures: reporting.ListErrorSignatures{Queries: sessions},
		Trace:           reporting.ShowSessionTrace{Queries: sessions},
		SessionPrompt:   reporting.ShowSessionPrompt{Queries: sessions},
		Prompts:         reporting.ReviewPrompts{Queries: usage.Prompts()},
		Catalog:         interactiveCatalog(*dbPath, *projectDir),
		Quotas:          readQuotas,
		Sync:            sync,
		SyncInterval:    30 * time.Second,
		ParseSince:      parseSince,
		Assets:          webui.Assets(),
		Since:           *since,
		Harness:         only,
		Project:         cov.project,
		Restricted:      cov.restricted,
		Detect:          redetection(*all, *projectDir, detect),
		AllowRemote:     *allowRemote,
		Log:             log.New(os.Stderr, "agent-stats: ", log.LstdFlags),
	})
}
