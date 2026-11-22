# agent-stats

Usage statistics across coding agents, read from the session logs they already write.
No proxy, no wrapper, no account: one binary that reads local files.

| Harness     | Logs read                                                        |
|-------------|------------------------------------------------------------------|
| Claude Code | `~/.claude/projects` (or `$CLAUDE_CONFIG_DIR/projects`)          |
| Codex       | `~/.codex/sessions`, `~/.codex/archived_sessions` (`.zst` too)   |
| oh-my-pi    | `~/.omp/agent/sessions`                                          |
| pi          | `~/.pi/agent/sessions`                                           |

```sh
go build .
./agent-stats                 # open the interactive view (on a terminal); it indexes new logs in the background
./agent-stats --since 7d      # start on the last 7 days (also 24h, 2026-11-01)
./agent-stats --no-sync       # do not re-read the logs
./agent-stats --harness codex --json   # plain JSON; any redirected output prints the plain-text report
```

In the interactive view: `tab`/`←→` or `1`–`5` switch between harness, model, folder, agent and tools tables,
`↑↓` scroll, `s` sorts by the next column and `o` reverses it, `p` cycles the period (24h, 7d, 30d, all),
`h` cycles the harness, `r` indexes the logs again and `q` quits.

The index lives in the user cache directory (`agent-stats/index.db`). Only changed log files are re-read,
and rows survive when a harness deletes old logs.

Costs come from the embedded LiteLLM price table (`internal/pricing/infra/pricetable/prices.json`) unless the harness
logged a positive cost itself (oh-my-pi, pi). To refresh it, set `commit` in `internal/pricing/infra/pricetable/gen/main.go`
to a reviewed LiteLLM commit, then run `go generate ./internal/pricing/infra/pricetable`.
Override or add models with `--prices file.json` (`{"model": [input, output, cacheRead, cacheWrite]}`, USD per token).

## Speed

A first run over about 2 000 logs (3.3 GB) takes 3 s on a 10-core machine, a run with nothing new 0.5 s.
Logs are read and parsed by several workers (three quarters of `GOMAXPROCS`) and saved by one writer, 64 logs per
transaction, in the order they were listed. A scanner reads each log line in a fraction of the time `encoding/json`
needs; it hands back to `encoding/json` any line that is not in the shape it knows, so the results are the same.
Logs are read ahead of the writer within 512 MiB of log text: a compressed log counts for what it holds once
decompressed, and a log counts until the batch it belongs to is saved. Quitting the interface stops the indexer.

To measure on your own logs without touching your index:

```sh
AGENT_STATS_BENCH_REAL=1 go test -run '^$' -bench RealLogs -benchtime 1x -cpuprofile cpu.out .
```

To check that the scanner and `encoding/json` read your logs alike, and as the parsers of commit 80bf4b1 did before
the scanner (about 1 min):

```sh
AGENT_STATS_CORPUS=$HOME go test ./internal/indexing/infra/logfiles -run RealLogs -v
```

## Layout

Vertical slices, each in clean-architecture layers (`domain` ← `application` ← `infra`):

- `internal/indexing`: read session logs (`infra/logfiles`) and keep them in the SQLite index (`infra/sqlite`).
- `internal/reporting`: summarise indexed usage; its `UsageQueries` port is implemented by the index.
- `internal/pricing`: rates and price list; `infra/pricetable` loads the embedded LiteLLM table.
- `internal/kernel`: what every slice shares (harnesses, token counts, request cost).
- `internal/reporting/infra/tui`: the interactive view (Bubble Tea), an input adapter over the reporting use case.
- `main.go` wires the slices, then opens the view on a terminal or prints the report.
