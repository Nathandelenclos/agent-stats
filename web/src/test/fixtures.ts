import { vi } from "vitest";
import type { Meta, Report, Tool, Usage, UsageGroup } from "../features/report/types";

export const usage = (over: Partial<Usage> = {}): Usage => ({
  requests: 10, failed: 1, input: 1000, output: 500, cacheRead: 3000, cacheWrite: 0, cost: 1.5, unpriced: 0,
  avgDurationMs: 0, avgTtftMs: 0, cacheRate: 0.75, firstTs: 1_700_000_000_000, lastTs: 1_700_086_400_000, ...over,
});

export const group = (key: string, over: Partial<Usage> = {}): UsageGroup => ({ key, ...usage(over) });

export const tool = (name: string, calls = 4): Tool => ({
  name, server: "", calls, errors: 0, argsChars: 0, resultChars: 120, avgMs: 15, lastTs: 1_700_000_000_000,
});

const nothing: Report = { total: usage({ requests: 0, cost: 0, failed: 0 }), byHarness: [], byModel: [], byFolder: [], byAgentType: [], tools: [], daily: [] };

/** A report with activity; `over` replaces any part of it. */
export const report = (over: Partial<Report> = {}): Report => ({
  total: usage(),
  byHarness: [group("claude")],
  byModel: [group("opus"), group("sonnet", { requests: 3, cost: 0.4 })],
  byFolder: [group("/work/app")],
  byAgentType: [group("main")],
  tools: [tool("Bash")],
  daily: [group("2026-11-19"), group("2026-11-20")],
  ...over,
});

/** A report of a period with no model request at all. */
export const emptyReport = (over: Partial<Report> = {}): Report => ({ ...nothing, ...over });

export const meta: Meta = { harnesses: ["claude", "codex"], defaults: { since: "7d", harness: "" } };

const json = (body: unknown) => new Response(JSON.stringify(body), { status: 200, headers: { "Content-Type": "application/json" } });

/** Stands in for the server: the report is whatever `reportFor` makes of the requested period and harness. */
export function serveReports(reportFor: (since: string, harness: string) => Report | Promise<Report>) {
  const fetch = vi.fn(async (input: RequestInfo | URL) => {
    const url = new URL(String(input), "http://localhost");
    switch (url.pathname) {
      case "/api/meta":
        return json(meta);
      case "/api/sync":
        return json({ state: "disabled", indexed: 0, startedAt: 0, finishedAt: 0 });
      case "/api/report":
        return json(await reportFor(url.searchParams.get("since") ?? "", url.searchParams.get("harness") ?? ""));
    }
    return new Response("{}", { status: 404 });
  });
  vi.stubGlobal("fetch", fetch);
  return fetch;
}
