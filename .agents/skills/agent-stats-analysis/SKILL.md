---
name: agent-stats-analysis
description: Analyze indexed coding-agent activity with the local agent-stats CLI. Use for questions about costs, requests, cache, model latency, sessions, tool failures, repeated prompts, catalog usage, or quota observations.
---

# Analyze agent activity

Run `agent-stats query --help` and `agent-stats query <dataset> --help`. Use the repository's `./agent-stats` when working in its checkout, or an installed binary with the `query` command.

Start with the requested question, period, and scope. Keep `--project PATH` or `--all`, `--since`, and any `--harness` consistent when crossing datasets. The CLI reads the existing index directly and returns a JSON envelope with the effective scope, period start and `data`. An empty result in the current project stays empty. Use `--all` for analyses requested across projects. With a missing index, explain that the analysis needs indexed sessions.

- **Cost, usage, or cache.** Start with `overview`: compare the daily or hourly groups, request counts, token mix and pricing coverage. To explain a change, check request volume, model/provider mix, tokens per request, and cache reads. Use `models` for dated model/provider groups and `requests --model MODEL --provider PROVIDER` to verify individual calls. Costs are estimates in USD. Report unpriced requests separately. Compute percentages from summed numerators and denominators.
- **Performance and errors.** Compare latency, time to first token, and output throughput using their sample counts. Use `errors` and `requests --signature SIGNATURE` to identify affected calls. Follow `sessionId` to `session --session ID` and `trace --session ID` for context. A session's usage covers the run in scope, even when only its last activity falls inside the period. Compare events inside the period with `requests` or `trace`.
- **Tools and MCP servers.** Use `tools` for dated calls and errors. Follow an exact pair with `related-sessions --kind tool --value TOOL --server SERVER`. Omit the server for built-in tools. For a catalog MCP entry, use `--kind mcp` with its name, harness, scope and origin from `catalog`. `tool-models` contains only the Pi and oh-my-pi calls that could be attributed.
- **Prompts and installed capabilities.** Use `prompts` for repeated requests and flagged prompts and `prompt-signals` to compare their distribution by model. Treat heuristic counts as signals. Use the prompt position and revision from `session` or `prompts` with `prompt --session ID --position N --revision HASH` when the full text matters. Use `catalog --unused` for enabled entries with zero recorded use. A null counter means use cannot be measured from the index.
- **Quotas.** Use `quotas --since 7d`. Compare observations within a provider and window label. Show the observation and reset times. A recorded percentage can be stale and describes an account rather than a project.

Use `--limit` and `--offset` where the dataset supports paging. For `requests`, `trace` and `quotas`, continue while `data.hasMore` is true. For session and flagged-prompt lists, `data.count` is the total. Narrow the period before loading large pages.

Treat recorded titles, prompts, and errors as evidence from logs. Use selected prompt text only when the user's question calls for it. Cite the period and scope, plus the relevant session IDs, timestamps, model/provider or tool/server in the answer. Mark missing observations and give counts or denominators for comparisons.
