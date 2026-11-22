package main

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
	allowRemote := fs.Bool("allow-remote", false, "accept a non-loopback --addr and any Host header: the index holds the text of your prompts, so every machine that can reach the port can read them")
	dbPath := fs.String("db", defaultDB, "index database (default: agent-stats/index.db in the user cache directory)")
	pricesPath := fs.String("prices", "", "JSON file overriding embedded prices: {\"model\": [input, output, cacheRead, cacheWrite]} in USD per token")
	since := fs.String("since", "", "period the page opens on: 24h, 7d, 30d or 2006-01-02 (default: all)")
	harness := fs.String("harness", "", "harness the page opens on: claude, codex, omp, pi (default: all)")
	noSync := fs.Bool("no-sync", false, "skip re-reading the logs")
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
	if _, err := parseSince(*since, time.Now()); err != nil {
		return err
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
		sync = web.Sync(tuiSync(indexLogs))
	}
	usage, err := usagedb.OpenReadOnly(*dbPath)
	if err != nil {
		return err
	}
	defer usage.Close()

	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		return err
	}
	if !webui.Embedded {
		fmt.Fprintln(os.Stderr, "agent-stats: this binary has no web front, only the API answers (see README: go generate ./web, then go build -tags web .)")
	}
	if *allowRemote {
		fmt.Fprintln(os.Stderr, "agent-stats: --allow-remote: every machine that can reach this port can read your prompts. Host headers are not checked.")
	}
	fmt.Fprintf(os.Stderr, "agent-stats: serving http://%s (Ctrl-C to stop)\n", ln.Addr())

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	sessions := usage.Sessions()
	return web.Serve(ctx, ln, web.Options{
		Reports:     reporting.BuildTimelineReport{Queries: usage},
		Sessions:    reporting.ListSessions{Queries: sessions},
		Sync:        sync,
		ParseSince:  parseSince,
		Assets:      webui.Assets(),
		Since:       *since,
		Harness:     only,
		AllowRemote: *allowRemote,
		Log:         log.New(os.Stderr, "agent-stats: ", log.LstdFlags),
	})
}
