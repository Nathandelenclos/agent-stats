# agent-stats

See which AI coding agents, models, and projects account for your token usage and estimated costs. agent-stats reads the session logs you already have, keeps its index on your machine, and gives you a terminal report, a browser dashboard, and JSON for scripts.

[Download a release](https://github.com/Nathandelenclos/agent-stats/releases/latest) · [Documentation](https://agent-stats.delenclosnathan.fr/docs/) · [Reference](docs/reference.md) · [Contributing](CONTRIBUTING.md)

## Install

On macOS with Homebrew

```sh
brew install nathandelenclos/tap/agent-stats
```

The [release downloads](https://github.com/Nathandelenclos/agent-stats/releases/latest) cover macOS, Linux, and Windows on `amd64` and `arm64`.

- **Linux.** Install the `.deb` on Debian or Ubuntu, the `.rpm` on Fedora, or the `.apk` on Alpine.
- **Windows.** Extract the ZIP and add the folder containing `agent-stats.exe` to your `PATH`.

See [how to verify a download](docs/reference.md#verify-a-release) for checksums and build attestations.

For coding agents, [install the analysis kit](docs/reference.md#install-the-analysis-kit) with `agent-stats skill install` after installing a binary that includes the installer.

<details>
<summary>Install with Go</summary>

With Go 1.26.11 or later

```sh
go install github.com/Nathandelenclos/agent-stats@latest
```

This gives you the CLI and local JSON API. Use a release binary or [build with the web page](#building-the-web-page) for the browser dashboard.

</details>

## Run your first report

After a session with a [supported coding agent](#supported-harnesses), run this from any directory.

```sh
agent-stats --all --since 7d --plain
```

The first run indexes local logs. Later runs reuse the index and read changed logs. Here's the start of a report generated from the repository's sample logs.

```text
All projects
10 requests (2 failed) · $0.50 · 2 339 in / 154 out · cache 82% · 2026-01-01 → 2026-01-03
9 requests have no known cost

  HARNESS  REQUESTS  FAILED  TOKENS IN  TOKENS OUT  CACHE   COST
      omp         4       1        115          15     4%  $0.50
   claude         4       1      1 208          81    91%  $0.00
    codex         1       0      1 000          50    80%  $0.00
       pi         1       0         16           8    56%  $0.00
```

The report breaks down usage by coding agent, model, project folder, and agent type. It also counts tool and MCP calls and errors. [Costs are estimates](#pricing), and the metrics available depend on what each agent logs.

## Usage

| I want to… | Run |
| --- | --- |
| Browse usage, sessions, and prompts in the terminal | `agent-stats` |
| Open the local browser dashboard | `agent-stats serve` |
| Check Codex usage over the past 24 hours | `agent-stats --all --harness codex --since 24h --plain` |
| Report on one project | `agent-stats --project ~/work/app --since 7d --plain` |
| Export a report for a script | `agent-stats --all --since 7d --json` |
| Find corrections you repeat across sessions | `agent-stats prompts --since 30d` |
| Check which installed tools show no use in the logs | `agent-stats catalog --unused` |

From a project folder, agent-stats can select that project automatically when it has indexed sessions there. Use `--all` in scripts that need every project. See [project selection](docs/reference.md#per-project-view) and the [CLI flags](docs/reference.md#cli-flags).

### Web interface

`agent-stats serve` starts the dashboard at <http://127.0.0.1:7373>. Open it to compare costs by day, drill into sessions, and review recurring prompts. The terminal and browser dashboards also show [available provider quota windows](docs/reference.md#account-quotas). See the [dashboard guide](docs/reference.md#web-interface) and the [HTTP API](docs/reference.md#api).

### JSON output

`--json` exports the usage report and respects the same period, coding-agent, and project filters as the text report. See the [report fields and empty values](docs/reference.md#json-output).

### Catalog

The catalog joins installed MCP servers, skills, agents, and plugins with the activity recorded in session logs. See [configuration coverage and how usage is counted](docs/reference.md#catalog).

## Supported harnesses

Claude Code, Codex, Cursor IDE, GitHub Copilot CLI, Gemini CLI, OpenCode, Kilo Code, Zed Agent, Grok Build CLI, Goose, VS Code Chat, Cline, Continue CLI, pi, and oh-my-pi.

See [log locations and metrics by agent](docs/reference.md#supported-harnesses).

## Pricing

agent-stats uses a cost recorded in the log when available. Otherwise, it estimates cost from token counts and its price table. Run `agent-stats prices pull` to download the weekly price snapshot. The embedded table is available offline. It flags requests whose cost is unknown. See [how to override model prices](docs/reference.md#pricing).

## Privacy

agent-stats reads session logs without changing their contents. It stores its SQLite index at `agent-stats/index.db` under your user cache directory by default. The index can retain **full prompt text**, session titles, and working folders after the original logs are gone. Keep it private. See [what the index stores](docs/reference.md#privacy).

### Security

The dashboard listens on loopback by default. With `--allow-remote`, anyone who can reach it can read usage reports and session metadata without a login. See the [server security details](docs/reference.md#security).

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for development, tests, and releases. Open an [issue](https://github.com/Nathandelenclos/agent-stats/issues) to report a bug or suggest a change.

### Architecture

The Go CLI reads agent logs into SQLite. Reporting and catalog queries use views published by the index. Domain code stays free of I/O, with file, database, terminal, and web adapters in `infra` packages. See the [code layout](docs/reference.md#architecture).

### Building the web page

From a checkout with Go and Node 22.12 or later, run these commands to build an executable with the dashboard bundled.

```sh
go generate ./web
go build -tags web .
```

[Web build and development details](docs/reference.md#building-the-web-page)

## License

[MIT](LICENSE)
