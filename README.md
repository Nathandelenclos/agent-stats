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

`m` switches between the **Usage** report and the **Sessions** view; the title shows which one is open (`[Usage] Sessions` or `Usage [Sessions]`).
Sessions lists one row per session (a subagent whose parent log was not indexed, or a conversation forked from another, has its own row): title, cost,
number of prompts you typed and their average length in characters, requests, duration, start, harness, main model with its share of the cost,
number of subagents, agent type and folder. A narrow terminal leaves out the last columns whole: at 80 columns the title, cost, prompts, average prompt,
requests and duration stay. Requests, cost and models cover the session and its subagents (nested ones too), so a costly session shows even when it delegated its work.
Prompts count what you typed in the session itself, not the briefs it sent to its subagents.
The period and harness filters apply as in Usage: a session is listed when its last activity, or its subagents', falls in the period, and its figures then cover the whole session.
`s`/`o` sort by any column (cost, average prompt length…); the order starts with the most recent session.
`Enter` opens the selected session: its requests by model, the tools it called, its subagents, and its prompts in order, each with the frustration signals
found in it (yelling, profanity, anguish, negation, blame, repeated). Prompt text is shown cut to a line, with control characters replaced.
`↑↓`, `PgUp`/`PgDn`, `Home`/`End` scroll, `Esc` goes back to the list.
The sessions are not part of `--json` or the plain-text report.

The index lives in the user cache directory (`agent-stats/index.db`). Only changed log files are re-read,
and rows survive when a harness deletes old logs.

Costs come from the embedded LiteLLM price table (`internal/pricing/infra/pricetable/prices.json`) unless the harness
logged a positive cost itself (oh-my-pi, pi). To refresh it, set `commit` in `internal/pricing/infra/pricetable/gen/main.go`
to a reviewed LiteLLM commit, then run `go generate ./internal/pricing/infra/pricetable`.
Override or add models with `--prices file.json` (`{"model": [input, output, cacheRead, cacheWrite]}`, USD per token).

## Layout

Vertical slices, each in clean-architecture layers (`domain` ← `application` ← `infra`):

- `internal/indexing`: read session logs (`infra/logfiles`) and keep them in the SQLite index (`infra/sqlite`).
- `internal/reporting`: summarise indexed usage and describe sessions (`ListSessions`, `ShowSession`); its `UsageQueries` and `SessionQueries` ports are implemented by the index.
  It reads only the views `indexing` publishes: `usage_requests` and `usage_tool_calls`, and `session_overview`, `session_requests`, `session_tool_calls` and `session_prompts`.
  `session_prompts` is the only one that carries prompt text.
- `internal/pricing`: rates and price list; `infra/pricetable` loads the embedded LiteLLM table.
- `internal/kernel`: what every slice shares (harnesses, token counts, request cost).
- `internal/reporting/infra/tui`: the interactive view (Bubble Tea), an input adapter over the reporting use cases.
- `main.go` wires the slices, then opens the view on a terminal or prints the report.
