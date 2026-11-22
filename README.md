# agent-stats

Usage statistics for AI coding agents, computed from the session logs they already write on your machine.

`agent-stats` reads the local session logs of **Claude Code**, **Codex**, **oh-my-pi** and **pi**, indexes them in a
local SQLite file and reports requests, tokens, cache hit rate, cost, latency and tool usage, per harness, model,
folder and sub-agent type. There is no proxy, no wrapper, no account and no network call: **nothing leaves your
machine** (see [Privacy](#privacy)).

## Install

With Go 1.26.11 or later:

```sh
go install github.com/Nathandelenclos/agent-stats@latest
```

Or download a binary from the [Releases](https://github.com/Nathandelenclos/agent-stats/releases) page
(Linux, macOS and Windows, on amd64 and arm64), check it against `checksums.txt`, and put it on your `PATH`.
The binaries are static (no cgo): the SQLite driver is pure Go. Each release also carries an SBOM per archive and a
signed build provenance attestation (built by the release workflow), which you can check with the GitHub CLI.
`--source-digest` pins the commit of the release tag (run it in a clone of the repository), and `--source-ref` the ref
the workflow ran on: `refs/heads/main` for a release published by the pipeline.

```sh
gh attestation verify agent-stats_<version>_<os>_<arch>.tar.gz \
  --repo Nathandelenclos/agent-stats \
  --signer-workflow Nathandelenclos/agent-stats/.github/workflows/release.yml \
  --source-ref refs/heads/main \
  --source-digest "$(git rev-parse '<tag>^{commit}')"
```

A release the maintainers finished by hand runs the same workflow on its tag, so its attestation names
`refs/tags/<tag>` instead. Accept that only together with a commit that is on `main`, since anyone who can push a tag
can name a ref like that:

```sh
gh attestation verify ... --source-ref refs/tags/<tag> ...   # same command as above
git merge-base --is-ancestor "$(git rev-parse '<tag>^{commit}')" origin/main && echo "on main"
```

To build from a checkout:

```sh
go build .
```

## Usage

```sh
agent-stats                          # index new logs, print the summary
agent-stats --since 7d               # last 7 days (also 24h, 30d, or a date: 2026-11-01)
agent-stats --harness codex          # one harness: claude, codex, omp or pi
agent-stats --harness claude --json  # machine-readable report
agent-stats --no-sync                # report from the existing index, skip re-reading the logs
```

| Flag | Meaning |
|------|---------|
| `--since` | Only count activity after this point: `24h`, `7d` (days), any Go duration, or a `2006-01-02` date. |
| `--harness` | Restrict the report to `claude`, `codex`, `omp` or `pi`. |
| `--json` | Print the report as JSON instead of tables. |
| `--no-sync` | Skip the indexing step and read the index as it is. |
| `--prices file.json` | Override or add model prices, see [Pricing](#pricing). |
| `--db path` | Index database to use (default: `agent-stats/index.db` in the user cache directory). |

The report has a one-line total (requests, failures, cost, tokens in and out, cache rate, date range), then one table
per harness, model, folder and agent type, then the most used tools with their MCP server, call count, errors and
result size.

### JSON output

`--json` prints one object:

```text
{
  "total":       { requests, failed, input, output, cacheRead, cacheWrite, cost, unpriced,
                   avgDurationMs, avgTtftMs, cacheRate, firstTs, lastTs },
  "byHarness":   [ { "key": "claude", ...same fields as total } ],
  "byModel":     [ ... ],
  "byFolder":    [ ... ],
  "byAgentType": [ ... ],
  "tools":       [ { name, server, calls, errors, argsChars, resultChars, avgMs, lastTs } ]
}
```

Costs are in USD, timestamps are Unix milliseconds (`0` when unknown). When there is nothing to report (an empty
index or a range without activity), a group or tool list with no entry is printed as `null`, not as `[]`: read an
absent or `null` list as empty.

### Interfaces

- **Plain-text report and JSON**: available today.
- **Interactive terminal UI**: in development, not part of the current release.
- **Web UI**: planned.

## Supported harnesses

| Harness | Logs read |
|---------|-----------|
| Claude Code | `~/.claude/projects` (or `$CLAUDE_CONFIG_DIR/projects`), including sub-agent logs |
| Codex | `~/.codex/sessions` and `~/.codex/archived_sessions` (or under `$CODEX_HOME`), `.jsonl` and `.jsonl.zst` rollouts |
| oh-my-pi | `~/.omp/agent/sessions` |
| pi | `~/.pi/agent/sessions` |

A harness that is not installed is simply skipped. The index lives in the user cache directory
(`agent-stats/index.db`). Only changed log files are re-read, and indexed rows survive when a harness deletes its
old logs.

## Pricing

Costs come from an embedded copy of the [LiteLLM](https://github.com/BerriAI/litellm) price table
(`internal/pricing/infra/pricetable/prices.json`), unless the harness logged a positive cost itself (oh-my-pi and pi
do). Requests on a model with no known price are counted as *unpriced* and flagged in the report.

Override or add models with `--prices file.json`, a JSON object of USD **per token**:

```json
{ "my-model": [0.000003, 0.000015, 0.0000003, 0.00000375] }
```

The four values are input, output, cache read and cache write.

## Privacy

- Everything runs locally. The program makes **no network request**; the price table is embedded in the binary. (Only the
  maintainers' `go generate` step downloads it, at development time.)
- Logs are opened read-only and are never modified.
- The index (`index.db`) is more than counters: it stores the **full text of every prompt** you sent, so it can hold
  anything you typed. It also holds token counts, costs and durations per request; tool names, MCP server names and
  sizes per call; per-prompt counts (characters, words and a few frustration signals); and, per session, its title
  (an untitled session is named after the first 80 characters of its first prompt), its working folder and
  `user@hostname` of the machine that read it. The file is created with mode `0600`, and the program creates its
  directory with mode `0700` when it is missing (`agent-stats` in the user cache directory by default). Treat it as
  personal data: do not share it or put it in a backup you do not control. On Windows the file modes are not
  enforced: the access rules of your profile folder apply.
- Reports (text and `--json`) and the `usage_requests` and `usage_tool_calls` views that other parts of the program
  read do not contain prompt texts or session titles. Nothing is sent anywhere.
- Text read from logs is stripped of control characters before it reaches your terminal.
- Reports can show folder paths and session-derived names. Review them before pasting a report in a public place.

## Architecture

The code is split in vertical slices, each in clean-architecture layers where dependencies point inward
(`domain` ← `application` ← `infra`):

| Slice | Role |
|-------|------|
| `internal/indexing` | Reads session logs (`infra/logfiles`: Claude, Codex, pi/oh-my-pi parsers) and keeps them in the SQLite index (`infra/sqlite`). |
| `internal/reporting` | Builds the usage report. Its `UsageQueries` port is implemented by a read-only adapter over the index (`infra/sqlite`). |
| `internal/pricing` | Rates and price list; `infra/pricetable` loads the embedded LiteLLM table and the `--prices` override. |
| `internal/kernel` | What every slice shares: harnesses, token counts, request cost. No I/O. |
| `internal/platform` | Small technical helpers (safe file opening). |

`main.go` wires the slices together; `present.go` renders the report as text or JSON.

## Development

```sh
go vet ./...
go test ./...            # add -race for the race detector
go build .
```

The report is locked by an approved snapshot: `testdata/report.approved.json` is produced from the fixtures in
`testdata/home`, which hold short hand-written logs, not real sessions.

### Refreshing the price table

The table is read from a fixed LiteLLM commit, never from a moving branch. To update it, set `commit` in
`internal/pricing/infra/pricetable/gen/main.go` to a reviewed LiteLLM commit, then run:

```sh
go generate ./internal/pricing/infra/pricetable
```

### Releases

Versions, tags and release notes are derived from [Conventional Commits](https://www.conventionalcommits.org/) by
[git-cliff](https://git-cliff.org) in CI; binaries are built by [GoReleaser](https://goreleaser.com). Nothing is
versioned by hand. See [CONTRIBUTING.md](CONTRIBUTING.md).

## License

[MIT](LICENSE)
