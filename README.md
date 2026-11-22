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

## Catalog

`agent-stats catalog` lists what each harness has installed and how much the logs show it was used:

```sh
./agent-stats catalog                           # one table per harness: kind, item, scope, uses, last use
./agent-stats catalog --unused                  # only installed items the logs never show in use
./agent-stats catalog --harness claude --since 30d --json
```

It takes `--harness`, `--since`, `--db`, `--no-sync`, `--prices` and `--json` as the report does (the report and its
`--json` output are unchanged), plus `--project DIR` (default: the current folder) for project-level configuration.
Like the report, it indexes new logs first unless you pass `--no-sync`.

| Harness     | Installed items read                                                                                              |
|-------------|--------------------------------------------------------------------------------------------------------------------|
| Claude Code | MCP servers (`~/.claude.json` incl. per-project, `.mcp.json`), skills and agents (`~/.claude`, `.claude`), plugins from `~/.claude/plugins` with their skills, agents and MCP servers |
| Codex       | `[mcp_servers.*]` and `[plugins.*]` of `~/.codex/config.toml`, skills (`~/.codex/skills`, `~/.agents/skills`), agents (`~/.codex/agents/*.toml`) |
| oh-my-pi    | `~/.omp/agent/mcp.json`, skills (`~/.omp/agent/skills`, `~/.agents/skills`), agents (`~/.omp/agent/agents`), plugins (`~/.omp/plugins`) |
| pi          | skills (`~/.pi/agent/skills`, `~/.agents/skills`), packages of `~/.pi/agent/settings.json`                           |

Project-level files (`.claude/`, `.agents/skills`, `.codex/`, `.omp/`, `.pi/`, `.mcp.json`) are read under `--project`.
`CLAUDE_CONFIG_DIR` and `CODEX_HOME` are honoured.

Scope is `user`, `project`, `plugin:<name>` (carried by an enabled plugin) or `logs` (seen in the logs, defined in no
configuration: built-in tools such as `Read` or `bash`, claude.ai connectors, agents like `Explore`). The harnesses expose
no list of their built-in tools, so those come from the logs only.

What "uses" counts depends on the kind:

- MCP servers and tools: tool calls. A server's calls are summed over its tools.
- Agents: model requests made by sessions of that agent type, which only Claude Code logs by name.
- Skills: loads through Claude Code's `Skill` tool, the only harness that logs the skill by name. An index written before
  that was recorded is migrated on the next run: only the log files holding a `Skill` call are read again.
- Plugins: the sum over what they carry. Codex and pi plugin contents are not read, so their usage is unknown.

`?` means the logs of that harness do not tell the item apart, so the usage is unknown, not zero. In `--json`, `uses` is then
`null`. An item defined in several scopes shows the usage of its name on each line. Disabled items are marked `(off)` and not
counted as unused.

The catalog reads names only. MCP environment variables, headers, URLs, tokens and credentials in package sources are never
read into the output. Files must be regular files and are size-bounded; anything unusable (a FIFO, an oversized or malformed
file, a name with control characters) is left out and noted on stderr (`skipped` in `--json`).

## Layout

Vertical slices, each in clean-architecture layers (`domain` ← `application` ← `infra`):

- `internal/indexing`: read session logs (`infra/logfiles`) and keep them in the SQLite index (`infra/sqlite`).
- `internal/reporting`: summarise indexed usage; its `UsageQueries` port is implemented by the index.
- `internal/catalog`: what harnesses have installed (`infra/configfiles`) linked to usage read from the index's published `usage_*` views (`infra/sqlite`).
- `internal/pricing`: rates and price list; `infra/pricetable` loads the embedded LiteLLM table.
- `internal/kernel`: what every slice shares (harnesses, token counts, request cost).
- `internal/reporting/infra/tui`: the interactive view (Bubble Tea), an input adapter over the reporting use case.
- `main.go` wires the slices, then opens the view on a terminal or prints the report.
