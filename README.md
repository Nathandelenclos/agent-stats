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
go build .                          # without the web page: `serve` answers with a notice and the JSON API
go generate ./web && go build -tags web .   # with the web page (needs Node 22.12 or later)
```

## Usage

```sh
agent-stats                          # on a terminal: open the interactive view, indexing new logs in the background
agent-stats --since 7d               # last 7 days (also 24h, 30d, or a date: 2026-11-01)
agent-stats --harness codex          # one harness: claude, codex, omp, pi, opencode, kilo or gemini
agent-stats --harness claude --json  # machine-readable report
agent-stats --no-sync                # report from the existing index, skip re-reading the logs
agent-stats --all                    # every project, even from inside one
agent-stats --project ~/work/app     # one project, from anywhere
agent-stats serve                    # the same report in a browser, at http://127.0.0.1:7373 (see Web interface)
```

### Per-project view

Run from a project, `agent-stats` reports on that project only: the report, the interactive view (usage and sessions)
and `agent-stats catalog`. The project is the top-level folder of the Git repository the current folder is in (found by
looking for `.git` upwards, no `git` process is run), or the current folder itself when it is in no repository. Links are
resolved, and on a volume that ignores case (the default on macOS) a folder is read as it is named on disk, so a project reached through a link,
or written in another case, is the same project. A linked Git worktree is a project of its own.

- The project is used when an indexed session worked in its folder or below it. Otherwise the report covers every
  project, as before. The home folder is never a project, since every other project lies below it: `--project` naming it
  counts every project, and the first line says so. On an empty index the project is looked for again when each indexing ends.
- `--all` forces every project. `--project DIR` forces the project at `DIR`, whether or not sessions worked there.
  With both, `--all` wins and the interface can switch to `DIR` with `a`.
- A session belongs to a project when its working folder is the project's folder or lies below it. Folders are compared
  element by element, so `/work/app-2` is not in `/work/app`. A session with no folder, or a folder that is not an
  absolute path, belongs to no project and shows only in the view of every project. Inside a project, a session's figures
  count only it and the subagents that worked in the project, so the sessions add up to the report; a subagent that
  worked there whose session started in another project is listed on its own.
- The plain-text report says what it covers on its first line (`Project: app (/work/app)` or `All projects`). The
  interactive view puts it in the title (`project: app` or `all projects`), and `a` switches between the project and
  every project.
- `--json` keeps its format and has no field for the project: its content is the project's when one applies. Pass `--all`
  in scripts that expect every project.

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
The sessions are not part of `--json` or the plain-text report. Both modes follow the per-project view.

| Flag | Meaning |
|------|---------|
| `--since` | Only count activity after this point: `24h`, `7d` (days), any Go duration, or a `2006-01-02` date. |
| `--harness` | Restrict the report to `claude`, `codex`, `omp`, `pi`, `opencode`, `kilo` or `gemini`. |
| `--all` | Count every project, not only the one of the current folder, see [Per-project view](#per-project-view). |
| `--project DIR` | Count only the project at `DIR` (folder or below), see [Per-project view](#per-project-view). |
| `--json` | Print the report as JSON instead of tables. |
| `--no-sync` | Skip the indexing step and read the index as it is. |
| `--prices file.json` | Override or add model prices, see [Pricing](#pricing). |
| `--db path` | Index database to use (default: `agent-stats/index.db` in the user cache directory). |

On a terminal, `agent-stats` opens an interactive view: `tab`/`←→` or `1`–`5` switch between harness, model,
folder, agent and tools tables, `↑↓` scroll, `s` sorts by the next column and `o` reverses it, `p` cycles the period
(24h, 7d, 30d, all), `h` cycles the harness, `a` switches between the project and every project, `r` indexes the logs again and `q` quits. With `--json`, or when the
output is redirected, it prints the plain report described below instead.

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
- **Web UI**: `agent-stats serve`, see [Web interface](#web-interface).

## Web interface

```sh
agent-stats serve                     # http://127.0.0.1:7373, indexing the logs in the background
agent-stats serve --since 7d --harness claude
agent-stats serve --all               # open on every project, even from inside one
agent-stats serve --no-sync --addr 127.0.0.1:8080
```

`serve` opens the usage report in a browser, from this machine: totals, period and harness filters, cost per day and by
harness and model, and sortable tables per harness, model, folder, agent type and tool. The page follows the system
light or dark theme (or a choice made on the page), works on a narrow screen and with the keyboard, and keeps the filters
in its address. Each chart has its values behind it (a table, or the table of the same name below).
Like the terminal view, it indexes the logs in the background and shows the progress; the figures refresh when it ends.
Switch to **Sessions** to sort the most recent sessions within the same filters. Open one to see its models, tools,
subagents and the signals of its prompts. The detail resets when the filters change. Press Ctrl-C to stop the server.

Started from a project (see [Per-project view](#per-project-view)), `serve` opens on that project: the header says
`Project: app` or `All projects`, and a toggle switches between the two. The page offers no other folder: the server
decides which folder the project is, and a request cannot name one. Started elsewhere, it covers every project and has
no toggle.

| Flag | Meaning |
|------|---------|
| `--addr` | Address to listen on (default `127.0.0.1:7373`). Only loopback addresses are accepted, see below. |
| `--allow-remote` | Accept a non-loopback `--addr` and any `Host` header. Read the security notes first. |
| `--since`, `--harness` | The period and harness the page opens on, as for the report. |
| `--all`, `--project DIR` | The project the page offers, as for the report: `--all` opens it on every project, `--project DIR` offers the project at `DIR`. |
| `--no-sync` | Skip re-reading the logs. |
| `--db`, `--prices` | As for the report. |

### API

The page reads a small JSON API, which is also usable with `curl`. Every route is a `GET`; errors are
[RFC 9457](https://www.rfc-editor.org/rfc/rfc9457) problem documents (`application/problem+json`) with a stable `type`.
`since` and `harness` take what the command line takes (`7d`, `24h`, `2006-01-02`; `claude`, `codex`, `omp`, `pi`, `opencode`, `kilo`, `gemini`) and
may be omitted. `project` is `current` (the project the server runs in) or `all`, and when omitted is what the page opens
on; `current` is refused with `400` when the server found no project, as is any other value, a folder included.
Unknown or repeated parameters are refused with `400`.

| Route | Answer |
|-------|--------|
| `/api/report?since=&harness=&project=` | The `--json` report (same fields), plus `daily`: one entry per local day with activity, oldest first, `key` being `2006-01-02`. Lists are `[]` when empty, never `null`. |
| `/api/sessions?since=&harness=&project=&limit=` | The most recent sessions (`limit` 1 to 1000, default 100) with their usage, models and subagent count, without the prompts. `scope` is the project filter applied. `count` and `total` cover every matching session, and add up to the report of the same scope. `title` is the session title, which for an untitled session is the start of its first prompt. |
| `/api/sessions/{id}?since=&harness=&project=` | One session from the same scope and filters, with its tools, subagents and prompt signals. Encode the session ID as a single path segment. An unknown or out-of-scope session returns `404`. Excerpts are at most 2,000 characters each and are omitted with `--allow-remote`. |
| `/api/meta` | The harness names, the name of the project the server runs in (`project`, `""` when it found none), and the period, harness and scope (`defaults.scope`: `current` or `all`) the page opens on. |
| `/api/sync` | The background indexing: `state` (`running`, `done`, `failed`, or `disabled` with `--no-sync`), files `indexed`, and what was skipped. |

### Security

The index holds the full text of your prompts (see [Privacy](#privacy)), and a local web server is reachable by every
page open in your browser, so `serve` is restrictive by default:

- It listens on `127.0.0.1` only. `--addr` refuses anything that is not a loopback address (`127.0.0.1`, `::1`,
  `localhost`), including `0.0.0.0` and an empty host, unless you pass `--allow-remote`. With `--allow-remote`, every
  machine that can reach the port can read your report and session metadata and nothing authenticates them: use it only on a network you
  trust, or behind something that does the authentication. The server has no login, no TLS and no rate limit.
- A request is answered only if its `Host` header is `localhost`, `127.0.0.1` or `[::1]` with the listening port. A web
  page that makes your browser reach the server under another name (DNS rebinding) gets `403`. `--allow-remote` lifts
  this check, since the server is then meant to be reached by other names.
- Only `GET` and `HEAD` are answered; nothing the API does changes the index. Requests that a browser marks
  `Sec-Fetch-Site` other than `same-origin` or `none` (so `cross-site` and `same-site`, which includes another local
  application on another port) are refused on `/api`. Clients that send no such header, like curl, are answered.
- No CORS header is ever sent, so other origins cannot read the API from a browser.
- Every response carries `Content-Security-Policy` (own scripts and styles only, no inline script, no framing,
  `base-uri` and `form-action` none), `X-Content-Type-Options: nosniff`, `Referrer-Policy: no-referrer` and
  `X-Frame-Options: DENY`. API responses are `Cache-Control: no-store`.
- Query strings are bounded on every API route (256 bytes, 32 characters for a period, `limit` at most 1000); a malformed
  one, a parameter the route does not take (`/api/meta?typo=1`) or a repeated one is a `400`, never ignored. The server has read and
  write timeouts. A failure answers `500` without its cause, which goes to the server's log on stderr.
- Prompt excerpts appear only in session details on a loopback listener without `--allow-remote`. Session titles
  (which can be based on the first prompt) and folder paths are visible in both modes. Treat screenshots like reports
  (see [Privacy](#privacy)).

### Building the web page

The front (`web/`: React, Vite, Tailwind, Recharts, TanStack Table and Query) is not versioned in its built form:
`web/dist` is ignored by Git and built on demand.

```sh
go generate ./web        # npm ci, then npm run build (type-checks with tsc, bundles with Vite)
go build -tags web .     # embeds web/dist in the binary
```

Without the `web` tag, `go build` and `go install` still work: the binary embeds a one-page notice that says how to build
the front, and the API answers. That is what `go install github.com/Nathandelenclos/agent-stats@latest` gives you; the
binaries of a release include the page, and each archive carries `web/package-lock.json` so that its SBOM lists the npm packages bundled in the binary. Install scripts are off for npm (`web/.npmrc`) and the lockfile is committed.
To work on the page, run `agent-stats serve` and, in `web/`, `npm run dev` (Vite proxies `/api` to `127.0.0.1:7373`).

## Catalog

`agent-stats catalog` lists what each harness has installed and how much the logs show it was used:

```sh
./agent-stats catalog                           # one table per harness: kind, item, scope, uses, last use
./agent-stats catalog --unused                  # only installed items the logs never show in use
./agent-stats catalog --harness claude --since 30d --json
```

It takes `--harness`, `--since`, `--db`, `--no-sync`, `--prices`, `--all` and `--json` as the report does (the `--json`
output keeps its format), plus `--project DIR` for the project. Like the report, it indexes new logs first unless you pass
`--no-sync`.

The project is that of the current folder (see [Per-project view](#per-project-view)) unless `--project DIR` names another. It
does two things: its project-level configuration is read, and usage is counted from its sessions only, when sessions
worked in it. With `--all` the usage of every project is counted, and the project-level configuration is still read.
`--project ""` reads no project-level configuration and counts every project. The first line of the text output says
what the usage covers.

| Harness     | Installed items read                                                                                              |
|-------------|--------------------------------------------------------------------------------------------------------------------|
| Claude Code | MCP servers (`~/.claude.json` incl. per-project, `.mcp.json`), skills and agents (`~/.claude`, `.claude`), plugins from `~/.claude/plugins` with their skills, agents and MCP servers |
| Codex       | `[mcp_servers.*]` and `[plugins.*]` of `~/.codex/config.toml`, skills (`~/.codex/skills`, `~/.agents/skills`), agents (`~/.codex/agents/*.toml`) |
| oh-my-pi    | `~/.omp/agent/mcp.json`, skills (`~/.omp/agent/skills`, `~/.agents/skills`), agents (`~/.omp/agent/agents`), plugins (`~/.omp/plugins`) |
| pi          | skills (`~/.pi/agent/skills`, `~/.agents/skills`), packages of `~/.pi/agent/settings.json`                           |

OpenCode, Kilo Code and Gemini CLI have no configuration reader yet: their tools show up with the `logs` scope, from the
usage their sessions record, and nothing of them is listed as installed (so none is reported unused).

The per-project MCP servers of `~/.claude.json` are listed for every project it names, not only for `--project`.
Project-level files (`.claude/`, `.agents/skills`, `.codex/`, `.omp/`, `.pi/`, `.mcp.json`) are read in the project's folder.
Instruction files such as `AGENTS.md` are not catalogued: they are not skills, agents, MCP servers or plugins.
`CLAUDE_CONFIG_DIR` and `CODEX_HOME` are honoured.

Scope is `user`, `project`, `plugin:<name>` (carried by an enabled plugin) or `logs` (seen in the logs, defined in no
configuration: built-in tools such as `Read` or `bash`, claude.ai connectors, agents like `Explore`). The harnesses expose
no list of their built-in tools, so those come from the logs only.

Uses are counted in the unit of the UNIT column, which depends on the kind:

- MCP servers and built-in tools count `calls`. A server's calls are summed over its tools.
- Skills count `loads` through Claude Code's `Skill` tool, the only harness that logs the skill by name. An index written
  before that was recorded is migrated on the next run: only the log files holding a `Skill` call, or a Codex call of an MCP
  tool recorded under its full `mcp__<server>__<tool>` name, are read again. Until a `Skill` call's log is read again, the
  usage of Claude skills is `?`. A `Skill` call that named no valid skill is listed as `(unnamed Skill calls)` and is
  never credited to an installed skill; it does not make the usage of skills unknown.
- Agents count `requests`: the model requests made by sessions of that agent type, which only Claude Code logs by name
  (the index keeps no count of sessions).
- Plugins count `invocations`: the tool calls and skill loads of the MCP servers and skills they carry. The requests of
  their agents are not added, as they count something else. Codex and pi plugin contents are not read, so their usage is unknown.

`?` means the logs of that harness do not tell the item apart, so the usage is unknown, not zero. In `--json`, `uses` is then
`null`. An item defined in several scopes shows the usage of its name on each line. Disabled items are marked `(off)` and not
counted as unused.

The catalog reads names only. MCP environment variables, headers, URLs, tokens and credentials in package sources are never
read into the output. Files must be regular files and are size-bounded; anything unusable (a FIFO, an oversized or malformed
file, a name with control characters) is left out and noted on stderr (`skipped` in `--json`).

## Supported harnesses

| Harness | Logs read |
|---------|-----------|
| Claude Code | `~/.claude/projects` (or `$CLAUDE_CONFIG_DIR/projects`), including sub-agent logs |
| Codex | `~/.codex/sessions` and `~/.codex/archived_sessions` (or under `$CODEX_HOME`), `.jsonl` and `.jsonl.zst` rollouts |
| oh-my-pi | `~/.omp/agent/sessions` |
| pi | `~/.pi/agent/sessions` |
| OpenCode | the SQLite databases `opencode.db` and `opencode-<channel>.db` of `$XDG_DATA_HOME/opencode` (`~/.local/share/opencode`), or the file named by `$OPENCODE_DB` |
| Kilo Code | `kilo.db` and `kilo-<channel>.db` of `$XDG_DATA_HOME/kilo` (`~/.local/share/kilo`), or `$KILO_DB`; an `opencode-<channel>.db` there (the old name) only when the channel has no `kilo-<channel>.db` |
| Gemini CLI | `~/.gemini/tmp/<project>/chats/*.json` and `*.jsonl` (under `$GEMINI_CLI_HOME` when set), including subagent journals |

A harness that is not installed is simply skipped. The index lives in the user cache directory
(`agent-stats/index.db`). Only changed log files are re-read, and indexed rows survive when a harness deletes its
old logs. When two logs name the same session (a copy of a conversation, or one saved in two formats), the session is
that of the log that goes on last, the later path when both stop together, whichever was indexed first.

What each of the three newer readers counts, and what it does not:

- **OpenCode and Kilo Code** share one reader. Both schema generations are read (`session`/`message`/`part`, and
  `session_v2` or `session` with `session_message`/`session_part`); a database with none of those tables is skipped and
  reported. A request is an assistant message with its tokens (input, output with the reasoning, cache reads and writes),
  its model and provider, the cost the harness logged, its stop reason and its error name. Where the database records
  them (the events generation), the time to first token and the duration of the stream are read too; the messages
  generation has neither. Prompts are the text of the user's messages, minus synthetic and ignored parts. Tool calls
  count their argument and result sizes, errors and durations; an MCP tool is listed under the name OpenCode gives it
  (`<server>_<tool>`), with no server, as that name cannot be split reliably. A child session (with a parent) is a
  subagent. A forked session (one that names the session it was forked from, or, in the older generation, whose title ends with
  `(fork #N)`) counts only what was said since the fork: the messages it copied from the original were made before it,
  by the time in their data, and are dropped. A session that is not a fork keeps all its messages. Compactions of the events generation count as requests, those that failed too (with what they used and their error). Not read: the session totals and
  `step-finish` parts (they repeat the usage of the messages), Kilo's former extension tasks, and shared or archived state.
- **Gemini CLI** conversations are JSON documents or JSONL journals; a journal is replayed (`$patch`, `$rewindTo`,
  `$set`) before it is counted. A request is a `gemini` message that states its tokens, counted once whatever the
  number of its tool calls; the cached tokens are part of its input and are counted as cache reads, not added to it, and
  the thoughts are counted as output. Prompts are the `user` messages and tool calls come with their sizes and status.
  The folder is read from the `.project_root` file Gemini CLI keeps beside the chats, when it is a regular file (a link
  is not followed) that holds an absolute path; the conversations of the project are read again when that file appears or changes.
  Gemini CLI logs no cost, duration or time to first token, so costs are priced from the table (the model ids it logs,
  such as `gemini-2.5-pro` or `gemini-3.1-pro-preview`, are in it) and latencies stay unknown. A subagent journal in the
  directory of its parent session is linked to it; a subagent file elsewhere is a subagent with no known parent.

A SQLite database holds many sessions, while the index keeps one record per log: each session of a database is a log of its
own, named `<database>#<session id>`, and it is read again when what its tables hold of it changes: for its messages and
for its parts, the number of rows, the sum of their update times and the bytes of their data, with the time the session
was updated (one grouped query per table, which reads the data of every row). The sum of the times, not the latest,
because a row stamped in the future would hide every later update; the bytes, because a tool call that goes from running to
error keeps its row. A session with no message is listed too, as an empty session: one that lost its last message is read
again and loses its rows. The size and modification time of the database file would
not do, as they change whenever any session is written and every session would be read again. Databases are opened
read-only (`mode=ro`) and the database file is never written. A database in write-ahead mode (how OpenCode keeps its
own) needs its `-shm` file, and SQLite creates it, and a `-wal` file, beside the database when they are missing: the
directory must be writable, or the database is reported as unreadable. A database is never opened as immutable, as a
harness may start writing to it at any time. A session deleted from a database keeps its indexed rows, as for any
other log.

## Pricing

Costs come from an embedded copy of the [LiteLLM](https://github.com/BerriAI/litellm) price table
(`internal/pricing/infra/pricetable/prices.json`), unless the harness logged a positive cost itself (oh-my-pi, pi, OpenCode and Kilo Code do; Gemini CLI does not). Requests on a model with no known price are counted as *unpriced* and flagged in the report.

Override or add models with `--prices file.json`, a JSON object of USD **per token**:

```json
{ "my-model": [0.000003, 0.000015, 0.0000003, 0.00000375] }
```

The four values are input, output, cache read and cache write.

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

## Privacy

- Everything runs locally. The program makes **no network request**; the price table is embedded in the binary.
  `agent-stats serve` only listens, on a loopback address unless you pass `--allow-remote` (see [Security](#security)). (Only the
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
| `internal/reporting` | Builds the usage report, with its per-day usage for charts (`BuildReport`, `BuildTimelineReport`), and describes sessions (`ListSessions`, `ShowSession`). Its `UsageQueries` and `SessionQueries` ports read only the views `indexing` publishes (`usage_*`, `session_*`; only `session_prompts` carries prompt text). Both are implemented by a read-only adapter over the index (`infra/sqlite`); `infra/tui` (Bubble Tea) and `infra/web` (local HTTP server and JSON API) are input adapters over these use cases. |
| `internal/catalog` | What harnesses have installed (`infra/configfiles`) linked to the usage read from the index's published `usage_*` views (`infra/sqlite`); `agent-stats catalog`. |
| `internal/pricing` | Rates and price list; `infra/pricetable` loads the embedded LiteLLM table and the `--prices` override. |
| `internal/kernel` | What every slice shares: harnesses, token counts, request cost. No I/O. |
| `internal/platform` | Small technical helpers: safe file opening, and `projectfolders` (the project of a folder, and narrowing the index's views to a project). |

Both `usage_requests` and `usage_tool_calls` carry the working `folder` of their session, and `usage_folders` lists each
folder a session worked in. A project is a folder: the adapters read that list, resolve each folder (links, `..`), keep the
ones inside the project, and pass them to the query as one bound parameter, so a folder written by a log never reaches SQL
as text.

The skill column of `usage_tool_calls` is part of that contract. On a `Skill` call it holds the skill loaded, `''` when the
call named no valid skill, and `NULL` while the index has not read the log again since it records skills. Every other call
holds `''`. The skills are kept in `tool_call_skills`, a table of their own: a build from before them writes `tool_calls`
with positional statements that a column more would refuse, so it keeps writing an index this build has migrated. Its
`Skill` calls then stay `NULL` until their log is read again (delete the index to read all logs again).

`web/` holds the front of the web interface and embeds its build. `main.go` wires the slices together and opens the interactive view on a terminal; `serve.go` wires `agent-stats serve`; `present.go` renders the plain report as text or JSON.

## Development

```sh
go vet ./...
go test ./...            # add -race for the race detector
go build .
```

The web front has its own checks: `cd web && npm ci && npm run typecheck` (`npm run build` type-checks too).

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
