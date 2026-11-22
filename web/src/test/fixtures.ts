import { vi } from "vitest";
import type { Meta, Report, Tool, Usage, UsageGroup } from "../features/report";
import type { SyncStatus } from "../features/sync";

export const usage = (over: Partial<Usage> = {}): Usage => ({
  requests: 10, failed: 1, input: 1000, output: 500, cacheRead: 3000, cacheWrite: 0, cost: 1.5, unpriced: 0,
  avgDurationMs: 0, avgTtftMs: 0, durationSamples: 0, ttftSamples: 0, avgTokensPerSecond: 0, throughputSamples: 0, premiumUnits: 0, cacheRate: 0.75, firstTs: 1_700_000_000_000, lastTs: 1_700_086_400_000, ...over,
});

export const group = (key: string, over: Partial<Usage> = {}): UsageGroup => ({ key, ...usage(over) });

export const tool = (name: string, calls = 4): Tool => ({
  name, server: "", calls, errors: 0, argsChars: 0, resultChars: 120, avgMs: 15, lastTs: 1_700_000_000_000,
});

const nothing: Report = { scope: "all", total: usage({ requests: 0, cost: 0, failed: 0 }), byHarness: [], byModel: [], byProvider: [], byFolder: [], byAgentType: [], tools: [], daily: [] };

/** A report with activity; `over` replaces any part of it. */
export const report = (over: Partial<Report> = {}): Report => ({
  scope: "all",
  total: usage(),
  byHarness: [group("claude")],
  byModel: [group("opus"), group("sonnet", { requests: 3, cost: 0.4 })],
  byProvider: [],
  byFolder: [group("/work/app")],
  byAgentType: [group("main")],
  tools: [tool("Bash")],
  daily: [group("2026-11-19"), group("2026-11-20")],
  ...over,
});

/** A report of a period with no model request at all. */
export const emptyReport = (over: Partial<Report> = {}): Report => ({ ...nothing, ...over });

/** A server started outside any project; `over` replaces any part of it. */
export const meta = (over: Partial<Meta> = {}): Meta => ({ harnesses: ["claude", "codex"], project: "", defaults: { since: "7d", harness: "", scope: "all" }, ...over });

/** A server started in the project `name`, whose page opens on it. */
export const metaInProject = (name: string): Meta => meta({ project: name, defaults: { since: "7d", harness: "", scope: "current" } });

const json = (body: unknown) => new Response(JSON.stringify(body), { status: 200, headers: { "Content-Type": "application/json" } });

/**
 * Stands in for the server, which describes itself as `about` (read at each request, for a server that learns more as it indexes)
 * and says its indexing is `indexing`: the report is whatever `reportFor` makes of the requested period, harness and scope,
 * and it says which scope it covers, as the server does.
 */
export function serveReports(
  reportFor: (since: string, harness: string, project: string) => Report | Promise<Report>,
  about: Meta | (() => Meta) = meta(),
  indexing: () => SyncStatus["state"] = () => "disabled",
) {
  const fetch = vi.fn(async (input: RequestInfo | URL) => {
    const url = new URL(String(input), "http://localhost");
    switch (url.pathname) {
      case "/api/meta":
        return json(typeof about === "function" ? about() : about);
      case "/api/sync":
        return json({ state: indexing(), indexed: 0, startedAt: 0, finishedAt: 0 });
      case "/api/report": {
        const scope = url.searchParams.get("project") ?? "";
        return json({ ...(await reportFor(url.searchParams.get("since") ?? "", url.searchParams.get("harness") ?? "", scope)), scope });
      }
    }
    return new Response("{}", { status: 404 });
  });
  vi.stubGlobal("fetch", fetch);
  return fetch;
}
