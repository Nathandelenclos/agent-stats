package command

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	indexing "github.com/Nathandelenclos/agent-stats/internal/indexing/application"
	"github.com/Nathandelenclos/agent-stats/internal/indexing/infra/logfiles"
	"github.com/Nathandelenclos/agent-stats/internal/indexing/infra/sqlite"
	"github.com/Nathandelenclos/agent-stats/internal/pricing/infra/pricetable"
	reporting "github.com/Nathandelenclos/agent-stats/internal/reporting/application"
	usagedb "github.com/Nathandelenclos/agent-stats/internal/reporting/infra/sqlite"
)

// BenchmarkRealLogs indexes the logs of this machine into a temporary index, never the default one.
// It is skipped unless AGENT_STATS_BENCH_REAL=1:
//
//	AGENT_STATS_BENCH_REAL=1 go test -run '^$' -bench RealLogs -benchtime 1x -cpuprofile cpu.out -memprofile mem.out .
//
// "cold" starts from an empty index, "warm" re-runs on an index where nothing changed, "report" builds
// the report from a full index.
func BenchmarkRealLogs(b *testing.B) {
	if os.Getenv("AGENT_STATS_BENCH_REAL") != "1" {
		b.Skip("set AGENT_STATS_BENCH_REAL=1 to index the real session logs")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		b.Fatal(err)
	}
	prices, err := pricetable.Load("")
	if err != nil {
		b.Fatal(err)
	}
	open := func(path string) *sqlite.SQLiteIndex {
		index, err := sqlite.Open(sqlite.Options{Path: path, User: "bench@host"})
		if err != nil {
			b.Fatal(err)
		}
		b.Cleanup(func() { index.Close() })
		return index
	}
	index := func(db *sqlite.SQLiteIndex) indexing.IndexReport {
		logs := logfiles.NewFileSessionLogs(logfiles.DefaultSources(home))
		rep, err := indexing.IndexSessions{Logs: logs, Store: db, Prices: prices, Workers: indexWorkers()}.Run(nil)
		if err != nil {
			b.Fatal(err)
		}
		return rep
	}

	b.Run("cold", func(b *testing.B) {
		for range b.N {
			db := open(filepath.Join(b.TempDir(), "index.db"))
			rep := index(db)
			b.ReportMetric(float64(rep.Indexed), "files")
			db.Close()
		}
	})

	// The warm and report runs need a full index; build it only when one of them is selected.
	path := filepath.Join(b.TempDir(), "index.db")
	var db *sqlite.SQLiteIndex
	full := func() {
		if db == nil {
			db = open(path)
			index(db)
		}
	}
	b.Run("warm", func(b *testing.B) {
		full()
		b.ResetTimer()
		for range b.N {
			// A log an agent is writing right now (this session's, for one) changes between the runs.
			b.ReportMetric(float64(index(db).Indexed), "reindexed")
		}
	})
	b.Run("report", func(b *testing.B) {
		full()
		b.ResetTimer()
		usage, err := usagedb.OpenReadOnly(path)
		if err != nil {
			b.Fatal(err)
		}
		defer usage.Close()
		for range b.N {
			if _, err := (reporting.BuildReport{Queries: usage}).Run(context.Background(), reporting.Query{}); err != nil {
				b.Fatal(err)
			}
		}
	})
}
