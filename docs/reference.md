# agent-stats reference

See the [README](../README.md) for installation and a first report.

## Verify a release

Compare the SHA-256 of the package or archive against the matching entry in `checksums.txt`.
Each release includes an SBOM per archive and signed build provenance.

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

## CLI flags

| Flag | Meaning |
|------|---------|
| `--since` | Only count activity after this point: `24h`, `7d` (days), any Go duration, or a `2006-01-02` date. |
| `--harness` | Restrict the report to one of the [supported harnesses](#supported-harnesses). |
| `--all` | Count every project, not only the one of the current folder, see [Per-project view](#per-project-view). |
| `--project DIR` | Count only the project at `DIR` (folder or below), see [Per-project view](#per-project-view). |
| `--json` | Print the report as JSON instead of tables. |
| `--plain` | Print a static report with daily values, without terminal updates or styling. |
| `--no-sync` | Skip the indexing step and read the index as it is. |
| `--prices file.json` | Override or add model prices, see [Pricing](#pricing). |
| `--db path` | Index database to use (default: `agent-stats/index.db` in the user cache directory). |

## Per-project view

Run from a project, `agent-stats` reports on that project only: the report, the interactive view (usage, sessions and prompts)
and the `catalog` and `prompts` subcommands. The project is the top-level folder of the Git repository the current folder is in (found by
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
- JSON follows the selected project scope without adding a project field. Pass `--all` in scripts that expect every project.

## Interactive terminal

On a terminal, `agent-stats` opens the dashboard. Use the arrow keys to choose Usage, Quotas, Sessions,
Prompts or Catalog, and press Enter to open it. Press `0` to go back to the dashboard, `?` for shortcuts in
the current view, or `e` to expand a detail. Esc closes the current detail and restores the previous selection.

In Usage, `tab` or `1`–`5` switches the breakdown and `g` opens the charts and the daily values. Select a
row or day and press Enter to see the sessions that contributed activity within the current filters. Session
details show the full session. Related tool and MCP rows count calls and errors without attributing model cost.

Open a session to browse its summary, models, tools, subagents, prompts and recorded parent links in Graph (`6`). Open a prompt to read it with its
line breaks and use `/` to find text. In lists, `/` filters the results. A flagged prompt can open its source
session with `s`. In Catalog, `s` on an MCP detail opens the sessions with observed calls. Quota windows
belong to the account and have their own reset times.

Sessions are ordered by most recent activity by default. Use `s` and `o` in the session list to change the sort.
The period matches the last activity of the session or its subagents. The figures then cover the entire session
within the selected project scope. Prompt counts refer to prompts you typed in the session itself.

Press `p` to change period, `h` to change harness, `a` to switch the project scope, or `r` to re-index the logs.
Set `NO_COLOR=1` for a monochrome interface, or run `agent-stats --plain` for a static report with the daily
values. `--json` and redirected output use the report formats below.

The report has a one-line total (requests, failures, cost, tokens in and out, cache rate, date range), then one table
per harness, model, folder and agent type, then the most used tools with their MCP server, call count, errors and
result size.

## Prompts review

The **Prompts** mode and the `agent-stats prompts` subcommand help you spot what you keep correcting or repeating, so you can
write it down once in `AGENTS.md` or `CLAUDE.md`. They look at the prompts you typed (not the briefs your agents sent to their
subagents or the messages relayed by T3 from other agents) that show at least one frustration signal, within the report's period, harness and project:

- **Recurring**: flagged prompts that say the same thing in at least two sessions, give or take case and punctuation (at least
  two words), with a hint line each: `Repeated 3 times in 2 sessions: consider writing this down.` A correction that shows no signal is not
  looked at, and a rephrasing is a different ask: the grouping is an exact match on the normalised text. Nothing is ever written
  to `AGENTS.md` for you.
- **Folders**: how many flagged prompts, sessions and signals each working folder gathered, so you see where frustration concentrates.
- **Prompts**: the flagged prompts, the most distinct signal categories first (then the most recent), each with its date, harness, session title, folder,
  signals and a one-line excerpt with control characters replaced.

In the interface, `↑↓`, `PgUp`/`PgDn`, `Home`/`End` scroll, `p` and `h` change the period and the harness, `a` switches between the project and all projects, and `r` indexes again.
On the command line:

```sh
agent-stats prompts                           # last 7 days, every harness in the project, as text
agent-stats prompts --since 30d --harness claude
agent-stats prompts --since all --all --json  # all flagged prompts across projects, as JSON
```

`--since` takes the values of the report plus `all`, and defaults to `7d` for this command; `--harness`, `--json`, `--no-sync`,
`--db`, `--prices`, `--all` and `--project` work as in the report. The text lists the 20 first prompts, 10 asks and 10 folders; `--json` lists everything:

```text
{
  "prompts":   [ { ts, harness, session, title, folder, excerpt,
                   signals: { yelling, profanity, anguish, negation, blame, repeated } } ],
  "sessions":  3,
  "recurring": [ { excerpt, count, sessions, firstTs, lastTs } ],
  "byFolder":  [ { folder, prompts, sessions, signals } ]
}
```

Timestamps are Unix milliseconds (`0` when unknown); empty lists print as `[]`. The signals are keyword counts (French and English): a long pasted
text can raise them, so read the counts as a hint, not a score. Excerpts of your prompts appear in the output: review it
before pasting it anywhere.

## JSON output

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
Like the terminal view, it indexes the logs in the background and shows the progress. Use **Re-index** to refresh the index again. The figures refresh when indexing ends.
Switch to **Sessions** to sort the most recent sessions within the same filters. Open one to see its models, tools,
subagents and the signals of its prompts. Activity graph opens a Mermaid diagram of recorded relationships and a separate timeline of recorded activity intervals. Improvements shows recurring requests and setup entries with no observed use for the active filters. The detail renders prompt excerpts as Markdown with literal HTML and plain
link labels and image descriptions. The detail resets when the filters change. **Prompts** shows repeated requests, flagged prompts and totals by folder. Press Ctrl-C to stop the server.

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

The page uses a small JSON API. Its read endpoints are also usable with `curl`. Errors are
[RFC 9457](https://www.rfc-editor.org/rfc/rfc9457) problem documents (`application/problem+json`) with a stable `type`.
`since` and `harness` take the same values as the command-line flags and
may be omitted. `project` is `current` (the project the server runs in) or `all`, and when omitted is what the page opens
on; `current` is refused with `400` when the server found no project, as is any other value, a folder included.
Unknown or repeated parameters are refused with `400`.

| Route | Answer |
|-------|--------|
| `/api/report?since=&harness=&project=` | The `--json` report (same fields), plus `daily`: one entry per local day with activity, oldest first, `key` being `2006-01-02`. Lists are `[]` when empty, never `null`. |
| `/api/sessions?since=&harness=&project=&limit=` | The most recent sessions (`limit` 1 to 1000, default 100) with their usage, models and subagent count, without the prompts. `scope` is the project filter applied. `count` and `total` cover every matching session, and add up to the report of the same scope. `title` is the session title, which for an untitled session is the start of its first prompt. |
| `/api/sessions/{id}?since=&harness=&project=` | One session from the same scope and filters, with its tools, subagents and prompt signals. Subagents can include `id`, `parentId` within the selected run, `startTs`, `endTs` and `models`. `forkedFrom` identifies the recorded source of a Codex fork when it is visible in the selected project. Encode the session ID as a single path segment. An unknown or out-of-scope session returns `404`. Excerpts are at most 2,000 characters each and are omitted with `--allow-remote`. |
| `/api/prompts?since=&harness=&project=&limit=` | Flagged prompts in review order, recurrent requests and totals by folder. `limit` affects the flagged list only. Excerpts are at most 240 characters each and are omitted with `--allow-remote`. |
| `/api/meta` | The harness names, the name of the project the server runs in (`project`, `""` when it found none), and the period, harness and scope (`defaults.scope`: `current` or `all`) the page opens on. |
| `GET /api/sync` | The background indexing: `state` (`running`, `done`, `failed`, or `disabled` with `--no-sync`), files `indexed`, and what was skipped. |
| `POST /api/sync` | Starts another indexing run or returns the run already in progress. Requires a same-origin browser request and no body or query parameters. |

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
- The API accepts reads and exposes one write, `POST /api/sync`. The write requires `Sec-Fetch-Site: same-origin`. Reads that a browser marks
  `Sec-Fetch-Site` other than `same-origin` or `none` (so `cross-site` and `same-site`, which includes another local
  application on another port) are refused on `/api`. Clients that send no such header, like curl, are answered.
- No CORS header is ever sent, so other origins cannot read the API from a browser.
- Every response carries `Content-Security-Policy` (own scripts and styles only, no inline script, no framing,
  `base-uri` and `form-action` none), `X-Content-Type-Options: nosniff`, `Referrer-Policy: no-referrer` and
  `X-Frame-Options: DENY`. API responses are `Cache-Control: no-store`.
- Query strings are bounded on every API route (256 bytes, 32 characters for a period, `limit` at most 1000); a malformed
  one, a parameter the route does not take (`/api/meta?typo=1`) or a repeated one is a `400`, never ignored. The server has read and
  write timeouts. A failure answers `500` without its cause, which goes to the server's log on stderr.
- Prompt excerpts appear in session details and prompt reviews on a loopback listener without `--allow-remote`. Session titles
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

OpenCode, Kilo Code, Gemini CLI, GitHub Copilot CLI, Zed Agent, Grok Build CLI, Cursor IDE, Goose and Continue CLI have no configuration reader yet: their tools show up with the `logs` scope, from the
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


In the terminal, press `c` for Catalog and `u` to toggle unused items. The local web dashboard offers the
same catalog in its sidebar. Both apply the active period, harness and project filters to usage.

## Account quotas

Open Quotas from the web sidebar, or press `l` in the terminal and `r` to refresh. Quotas apply to the
provider account, independently of the report filters. The views show the remaining percentage and the
reset time for each available window. Catalog and Quotas are available on a loopback server without
`--allow-remote`.

Codex is read through the installed app-server with its current login when the view opens or refreshes.
Claude Code can send the standard status-line JSON to `agent-stats quota record-claude` on stdin. If you
already have a status line, forward its input to this command from that script and keep its current
display. The command stores the observed 5-hour and 7-day figures locally for up to 15 minutes, and
discards windows once they reset. The rest of the status-line payload stays out of the cache.

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
| GitHub Copilot CLI | `~/.copilot/session-state/<session-id>/events.jsonl` (under `$COPILOT_HOME` when set) |
| Zed Agent | `threads/threads.db` in `~/Library/Application Support/Zed` on macOS, `${FLATPAK_XDG_DATA_HOME:-$XDG_DATA_HOME}/zed` on Linux (`~/.local/share/zed` by default), or `%LOCALAPPDATA%/Zed` on Windows |
| Grok Build CLI | `~/.grok/sessions/<encoded-cwd>/<id>/updates.jsonl` (under `$GROK_HOME` when set), with session metadata and usage from neighbouring files |
| Cursor IDE | `~/.cursor/projects/<project>/agent-transcripts/<id>/<id>.jsonl` and `User/globalStorage/state.vscdb` under Cursor's application data directory |
| Goose | `~/.local/share/goose/sessions/sessions.db` |
| VS Code Chat | `workspaceStorage/<workspace>/chatSessions` and `globalStorage/emptyWindowChatSessions` under the VS Code user directory, plus supported debug logs |
| Cline | Session manifests and messages under `~/.cline/data/sessions`, or legacy tasks under the VS Code user directory |
| Continue CLI (`cn`) | `~/.continue/sessions/<id>.json` |

A harness that is not installed is simply skipped. The index lives in the user cache directory
(`agent-stats/index.db`). Only changed log files are re-read, and indexed rows survive when a harness deletes its
old logs. When two logs name the same session (a copy of a conversation, or one saved in two formats), the session is
that of the log that goes on last, the later path when both stop together, whichever was indexed first.

What the newer readers count:

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
- **GitHub Copilot CLI** reads prompts and working folders from its event journals. Model responses with a service
  request ID are counted once per call, including when the response has several messages. Older events can use the
  GitHub request tracing ID when service IDs are absent. The reader keeps the effective model and any recorded output
  tokens. It pairs tool starts and completions for sizes, errors and elapsed time. Subagent events contribute to the
  same session. Input and cache counts, model call duration, time to first token and USD cost are unavailable in the
  on-disk per-call data. Missing token counts appear as zero in the current report, and calls remain unpriced. The
  session store, shutdown totals and VS Code chat history are outside this reader.
- **Zed Agent** reads [native threads version `0.3.0`](https://github.com/zed-industries/zed/blob/2f808b3c2d/crates/agent/src/db.rs)
  in JSON or Zstandard, with a 64 MiB limit on both stored and decoded data per thread. The index keeps the text of
  user prompts, tool calls and their recorded argument and result sizes and errors, and links subagents to their
  parents. Project filtering applies to single-folder workspaces. Prompts and tools have no timestamps, so tool calls
  appear in session details and all-time reports. Zed stores aggregate token usage per turn and per thread, which the
  index leaves out. Agent 1 archives, shared exports, ACP sessions and custom data locations are outside this reader.
- **Grok Build CLI** reads the local ACP update log, joins user prompt fragments, and groups tool updates by call ID.
  It uses the session summary, project directory and subagent metadata to place and link each session. The usage report
  includes complete turns that identify exactly one model call, dated at the recorded end of the turn. Session totals,
  multi-call turns and parent ledgers that can include child usage are excluded, so reported Grok usage is partial.
  Tool durations, request durations and time to first token are unknown. Copied fork history, chat history, the search
  index and checkpoints are excluded. The ACP log records historical activity, including activity later rewound.
  The on-disk schema was checked against [Grok Build's storage and usage source](https://github.com/xai-org/grok-build/tree/2bdd1d6a63/crates/codegen/xai-grok-shell/src/session).
- **Continue CLI** reads prompts and tool calls from the saved history. Each assistant message with usage contributes one
  request. The reader uses the model, token counts and cent-rounded cost estimate saved on that message, and skips the
  session-wide usage total and the extension's separate token log. Sessions without response usage still contribute their
  prompts and tools. The CLI and extension share a history directory, and snapshots do not identify the writer. The CLI
  saves no message timestamps. Its requests and tools appear in totals without a date filter and are omitted by `--since` and daily reports.
- **Goose** reads the working directory, session dates, parent links, prompts, and tool requests and results from
  `sessions` and `messages`. Older SQLite schemas provide activity without per-message usage. When present, message
  metadata provides the model, tokens, logged cost, request duration and time to first token. Cache reads and writes
  are part of Goose's input count and reported separately. Compaction, aggregate usage and pre-migration JSONL are
  excluded. Tool durations are unknown.
- **Cursor IDE** combines local Composer headers and JSONL transcripts by conversation ID. Composer headers supply session
  dates and project folders when `User/workspaceStorage/<workspaceId>/workspace.json` names a local folder.
  The editor data directory is `~/Library/Application Support/Cursor` on macOS, `${XDG_CONFIG_HOME:-~/.config}/Cursor`
  on Linux, and `%APPDATA%/Cursor` on Windows. The JSONL reader counts text prompts from user messages and explicit
  `tool_use` blocks from assistant messages. It prefers a nested subagent transcript over its top-level copy so the
  child is counted once under its parent. Tool timestamps, results, errors and durations are unavailable, so undated
  calls appear in session details and all-time tool totals. Model requests, tokens, costs and time to first token are
  unavailable. Legacy text transcripts, legacy conversation blobs, CLI databases and cloud-only conversations are
  outside the reader's scope.

Zed revisions hash each thread's stored payload and metadata. The index keeps one log per SQLite session, named `<database>#<session id>`. For OpenCode and Kilo Code, a session
is read again when its update time or its message and part statistics change. The statistics include row count, the
sum of update times and total data bytes (one grouped query per table, which reads the data of every row). The sum of the times, not the latest,
because a row stamped in the future would hide every later update; the bytes, because a tool call that goes from running to
error keeps its row. A session with no message is listed too, as an empty session: one that lost its last message is read
again and loses its rows. Goose checks the size and modification time of the database and its WAL. A change
refreshes all Goose sessions because a message can be edited without changing the session timestamp. Databases are opened
read-only (`mode=ro`) and the database file is never written. A database in write-ahead mode (how OpenCode keeps its
own) needs its `-shm` file, and SQLite creates it, and a `-wal` file, beside the database when they are missing: the
directory must be writable, or the database is reported as unreadable. A database is never opened as immutable, as a
harness may start writing to it at any time. A session deleted from a database keeps its indexed rows, as for any
other log.

## Pricing

Costs come from an embedded copy of the [LiteLLM](https://github.com/BerriAI/litellm) price table
(`internal/pricing/infra/pricetable/prices.json`), unless the harness logged a positive cost itself (for example, Continue CLI can save an estimate rounded to cents). Requests on a model with no known price are counted as *unpriced* and flagged in the report.

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
AGENT_STATS_BENCH_REAL=1 go test -run '^$' -bench RealLogs -benchtime 1x -cpuprofile cpu.out ./internal/command
```

To check that the scanner and `encoding/json` read your logs alike, and as the parsers of commit 80bf4b1 did before
the scanner (about 1 min):

```sh
AGENT_STATS_CORPUS=$HOME go test ./internal/indexing/infra/logfiles -run RealLogs -v
```

## Privacy

- Indexing and reporting run locally and use an embedded price table. The optional Codex quota view asks the
  installed Codex app-server for account limits. `agent-stats serve` binds to a loopback address by default
  (see [Security](#security)).
- Logs are opened read-only and are never modified.
- The index (`index.db`) is more than counters: it stores the **full text of every prompt** you sent, so it can hold
  anything you typed. It also holds token counts, costs and durations per request; tool names, MCP server names and
  sizes per call; per-prompt counts (characters, words and a few frustration signals); and, per session, its title
  (an untitled session is named after the first 80 characters of its first prompt), its working folder and
  `user@hostname` of the machine that read it. The file is created with mode `0600`, and the program creates its
  directory with mode `0700` when it is missing (`agent-stats` in the user cache directory by default). Treat it as
  personal data: do not share it or put it in a backup you do not control. On Windows the file modes are not
  enforced: the access rules of your profile folder apply.
- Usage reports (text and `--json`) and the `usage_requests` and `usage_tool_calls` views that other parts of the program
  read omit prompt text and session titles.
- Text read from logs is stripped of control characters before it reaches your terminal.
- Reports can show folder paths and session-derived names. Review them before pasting a report in a public place.

## Architecture

The code is split in vertical slices, each in clean-architecture layers where dependencies point inward
(`domain` ← `application` ← `infra`):

| Slice | Role |
|-------|------|
| `internal/indexing` | Reads session logs (`infra/logfiles`: Claude, Codex, pi/oh-my-pi parsers) and keeps them in the SQLite index (`infra/sqlite`). |
| `internal/reporting` | Builds the usage report, with its per-day usage for charts (`BuildReport`, `BuildTimelineReport`), and describes sessions (`ListSessions`, `ShowSession`) and reviews flagged prompts (`ReviewPrompts`). Its `UsageQueries`, `SessionQueries` and `PromptQueries` ports read only the views `indexing` publishes (`usage_*`, `session_*`; only `session_prompts` carries prompt text). These ports are implemented by a read-only adapter over the index (`infra/sqlite`); `infra/tui` (Bubble Tea) and `infra/web` (local HTTP server and JSON API) are input adapters over these use cases. |
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

`main.go` is the executable entry point. `internal/command` wires the slices together, opens the interactive view on a
terminal, and runs `agent-stats serve`. The `infra/cli` adapters in reporting and catalog format the command-line output.
`web/` holds the web front and embeds its build.

## Development

```sh
go vet ./...
go test ./...            # add -race for the race detector
go build .
```

The web front has its own checks: `cd web && npm ci && npm run typecheck` (`npm run build` type-checks too).

The report is locked by an approved snapshot: `testdata/report.approved.json` is produced from the fixtures in
`testdata/home`, which hold short hand-written logs, not real sessions. The prompts review is locked the same way by
`testdata/prompts.approved.txt` and `testdata/prompts.approved.json`, built from neutral prompts written by the test. On a
difference a test writes `<file>.received` next to the approved file: read the diff, and only rename it if the change is intended.

### Refreshing the price table

The table is read from a fixed LiteLLM commit, never from a moving branch. To update it, set `commit` in
`internal/pricing/infra/pricetable/gen/main.go` to a reviewed LiteLLM commit, then run:

```sh
go generate ./internal/pricing/infra/pricetable
```
