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
./agent-stats                 # index new logs, print the summary
./agent-stats --since 7d      # last 7 days (also 24h, 2026-11-01)
./agent-stats --harness codex --json
```

The index lives in the user cache directory (`agent-stats/index.db`). Only changed log files are re-read,
and rows survive when a harness deletes old logs.

Costs come from the embedded LiteLLM price table (`internal/pricing/infra/pricetable/prices.json`) unless the harness
logged a positive cost itself (oh-my-pi, pi). To refresh it, set `commit` in `internal/pricing/infra/pricetable/gen/main.go`
to a reviewed LiteLLM commit, then run `go generate ./internal/pricing/infra/pricetable`.
Override or add models with `--prices file.json` (`{"model": [input, output, cacheRead, cacheWrite]}`, USD per token).

## Layout

Vertical slices, each in clean-architecture layers (`domain` ← `application` ← `infra`):

- `internal/indexing`: read session logs (`infra/logfiles`) and keep them in the SQLite index (`infra/sqlite`).
- `internal/reporting`: summarise indexed usage; its `UsageQueries` port is implemented by the index.
- `internal/pricing`: rates and price list; `infra/pricetable` loads the embedded LiteLLM table.
- `internal/kernel`: what every slice shares (harnesses, token counts, request cost).
- `main.go` wires the slices and prints the report.
