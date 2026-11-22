package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	catalog "github.com/Nathandelenclos/agent-stats/internal/catalog/application"
	"github.com/Nathandelenclos/agent-stats/internal/catalog/infra/configfiles"
	catalogdb "github.com/Nathandelenclos/agent-stats/internal/catalog/infra/sqlite"
	"github.com/Nathandelenclos/agent-stats/internal/indexing/infra/sqlite"
	"github.com/Nathandelenclos/agent-stats/internal/kernel"
	"github.com/Nathandelenclos/agent-stats/internal/pricing/infra/pricetable"
)

// runCatalog is the `agent-stats catalog` subcommand: what each harness has installed, linked to how much it was used.
func runCatalog(args []string, stdout, stderr io.Writer) error {
	defaultDB := ""
	if cache, err := os.UserCacheDir(); err == nil {
		defaultDB = filepath.Join(cache, "agent-stats", "index.db")
	}
	cwd, _ := os.Getwd()
	fl := flag.NewFlagSet("agent-stats catalog", flag.ContinueOnError)
	fl.SetOutput(stderr)
	dbPath := fl.String("db", defaultDB, "index database (default: agent-stats/index.db in the user cache directory)")
	pricesPath := fl.String("prices", "", "JSON file overriding embedded prices, used when indexing")
	since := fl.String("since", "", "only count usage after this: 24h, 7d, 30d or 2006-01-02")
	harness := fl.String("harness", "", "only one harness: claude, codex, omp, pi")
	asJSON := fl.Bool("json", false, "print the catalog as JSON")
	noSync := fl.Bool("no-sync", false, "skip re-reading the logs")
	unused := fl.Bool("unused", false, "only installed items the logs never show in use")
	project := fl.String("project", cwd, "folder whose project-level configuration is read (empty: none)")
	if err := fl.Parse(args); err != nil {
		return err
	}
	if fl.NArg() > 0 {
		return fmt.Errorf("unexpected argument %q", fl.Arg(0))
	}
	if *dbPath == "" {
		return errors.New("no user cache directory: pass --db")
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
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	if *project != "" {
		if *project, err = filepath.Abs(*project); err != nil {
			return err
		}
	}

	index, err := sqlite.Open(sqlite.Options{Path: *dbPath, User: currentUser()})
	if err != nil {
		return err
	}
	defer index.Close()
	if !*noSync {
		prices, err := pricetable.Load(*pricesPath)
		if err != nil {
			return err
		}
		indexLogs, err := newIndexRun(index, prices)
		if err != nil {
			return err
		}
		if err := indexBeforeReport(indexLogs); err != nil {
			return err
		}
	}
	activity, err := catalogdb.OpenReadOnly(*dbPath)
	if err != nil {
		return err
	}
	defer activity.Close()

	c, err := catalog.BuildCatalog{
		Inventory: configfiles.Inventory{Roots: configfiles.DefaultRoots(home, *project, os.Getenv)},
		Activity:  activity,
	}.Run(catalog.Query{Since: from, Harness: only, UnusedOnly: *unused})
	if err != nil {
		return err
	}
	if *asJSON {
		return writeCatalogJSON(stdout, c)
	}
	for _, note := range c.Skipped {
		fmt.Fprintln(stderr, "skipped", printable(note))
	}
	printCatalog(stdout, c, *unused)
	return nil
}
