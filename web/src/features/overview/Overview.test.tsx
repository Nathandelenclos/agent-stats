import { act } from "react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { App } from "../../App";
import { group, metaInProject, report, serveReports, usage } from "../../test/fixtures";
import { choose, click, eventually, render, selectLabelled, testQueryClient, withQueries } from "../../test/render";

const fixtureSession = {
  id: "codex:matching", harness: "codex", title: "Trace the cost of retries", folder: "/work/agent-stats",
  agentType: "main", startTs: 1795300000000, endTs: 1795300001000, durationMs: 1000,
  usage: usage({ requests: 4, failed: 0, cost: 4.2 }),
  models: [{ model: "test-model", requests: 4, cost: 4.2, share: 1 }], subagents: 0, prompts: 1, avgPromptChars: 2100,
};
const list = {
  scope: "current", count: 1, total: { requests: 4, failed: 0, cost: 4.2, unpriced: 0 }, sessions: [fixtureSession],
};

const json = (value: unknown) => new Response(JSON.stringify(value), { status: 200 });
const button = (container: HTMLElement, label: string) =>
  [...container.querySelectorAll<HTMLButtonElement>("button")].find((el) => el.textContent?.trim() === label);

beforeEach(() => window.history.replaceState(null, "", "/"));
afterEach(() => {
  vi.unstubAllGlobals();
  window.history.replaceState(null, "", "/");
});

it("opens a date's contributions and restores its selection on browser back", async () => {
  const date = "2026-11-21";
  const normal = serveReports(() => report({ daily: [group(date)] }), metaInProject("agent-stats"));
  const fetch = vi.fn((input: RequestInfo | URL) => {
    const requested = new URL(String(input), "http://localhost");
    if (requested.pathname === "/api/sessions") return Promise.resolve(json(list));
    if (requested.pathname === "/api/prompts") return Promise.resolve(json({
      scope: "current", count: 0, sessions: 0, promptsVisible: true, prompts: [], recurring: [], byFolder: [],
    }));
    if (requested.pathname === "/api/related-sessions") return Promise.resolve(json(list));
    if (requested.pathname.endsWith("codex%3Amatching")) return Promise.resolve(json({
      scope: "current", session: fixtureSession, tools: [], subagents: [], prompts: [], promptsVisible: true,
    }));
    return normal(input);
  });
  vi.stubGlobal("fetch", fetch);
  const { container } = render(withQueries(testQueryClient(), <App />));
  await eventually(() => expect(container.querySelector("h1")?.textContent).toBe("Overview"));
  await eventually(() => expect(container.querySelector("#activity-title")).not.toBeNull());
  choose(selectLabelled(container, "Period"), "30d");
  await eventually(() => expect(container.querySelector(`#day-${date}`)).not.toBeNull());
  const details = container.querySelector<HTMLDetailsElement>(".dashboard-daily")!;
  act(() => { details.open = true; });
  const day = container.querySelector<HTMLButtonElement>(`#day-${date}`)!;
  day.focus();
  click(day);

  const panel = () => container.querySelector<HTMLElement>(".dashboard-inspector");
  await eventually(() => expect(panel()?.textContent).toContain("Trace the cost of retries"));
  expect(panel()?.textContent).toContain("$4.20");
  expect(day.getAttribute("aria-pressed")).toBe("true");
  expect(fetch).toHaveBeenCalledWith(`/api/related-sessions?kind=day&value=${date}&since=30d&project=current&limit=50`, expect.any(Object));

  const recentSession = () => container.querySelector<HTMLButtonElement>(".dashboard-card-grid .dashboard-row");
  const relatedSession = panel()!.querySelector<HTMLButtonElement>(".dashboard-row")!;
  await eventually(() => expect(recentSession()).not.toBeNull());
  expect(relatedSession.id).not.toBe(recentSession()!.id);
  relatedSession.focus();
  click(relatedSession);
  await eventually(() => expect(container.querySelector("h1")?.textContent).toBe("Sessions"));
  expect(new URLSearchParams(location.search).get("session")).toBe("codex:matching");

  const graph = () => button(container, "Activity graph");
  await eventually(() => expect(graph()).toBeDefined());
  click(graph()!);
  expect(new URLSearchParams(location.search).get("section")).toBe("activity graph");
  expect(container.querySelector('[aria-label="Session activity graph"]')).not.toBeNull();

  await act(async () => { window.history.back(); });
  await eventually(() => expect(panel()?.textContent).toContain("Trace the cost of retries"));
  await eventually(() => expect(document.activeElement).toBe(panel()!.querySelector(".dashboard-row")));
  const restored = new URLSearchParams(location.search);
  expect(restored.get("value")).toBe(date);
  expect(restored.get("since")).toBe("30d");
  expect(restored.get("project")).toBe("current");
});

it.each(["banana", "2026-02-30"])("opens the dashboard when a link contains invalid day %s", async (value) => {
  window.history.replaceState(null, "", `?kind=day&value=${value}`);
  const fetch = serveReports(() => report());
  const { container } = render(withQueries(testQueryClient(), <App />));
  await eventually(() => expect(container.querySelector("#activity-title")).not.toBeNull());
  expect(container.querySelector(".dashboard-inspector")).toBeNull();
  expect(fetch.mock.calls.some(([input]) => String(input).startsWith("/api/related-sessions"))).toBe(false);
});

it("opens the exact MCP catalog entry and shows tool calls without a dollar total", async () => {
  window.history.replaceState(null, "", "?view=catalog&since=7d");
  const normal = serveReports(() => report(), { ...metaInProject("agent-stats"), catalogAvailable: true });
  const fetch = vi.fn((input: RequestInfo | URL) => {
    const requested = new URL(String(input), "http://localhost");
    if (requested.pathname === "/api/catalog") return Promise.resolve(json({
      scope: "current", skipped: 0, entries: [{
        name: "GitHub Tools", harness: "codex", kind: "mcp", scope: "plugin", origin: "tools", originId: "tools@official", enabled: true,
        lastTs: 0, uses: 3, unit: "calls",
      }],
    }));
    if (requested.pathname === "/api/related-sessions") return Promise.resolve(json({
      ...list, toolTotals: { calls: 3, errors: 1 },
      sessions: [{ ...fixtureSession, relatedTool: { calls: 3, errors: 1 } }],
    }));
    return normal(input);
  });
  vi.stubGlobal("fetch", fetch);
  const { container } = render(withQueries(testQueryClient(), <App />));
  await eventually(() => expect(button(container, "GitHub Tools")).toBeDefined());
  click(button(container, "GitHub Tools")!);
  const panel = () => container.querySelector(".dashboard-inspector")?.textContent ?? "";
  await eventually(() => expect(panel()).toContain("3 calls"));
  expect(panel()).not.toContain("$4.20");
  expect(fetch).toHaveBeenCalledWith(
    "/api/related-sessions?kind=mcp&value=GitHub+Tools&itemHarness=codex&itemScope=plugin&itemOrigin=tools%40official&since=7d&project=current&limit=50",
    expect.any(Object),
  );
});

it("shows hourly values and fills empty hours in a short period", async () => {
  const first = 1_797_600_600_000;
  serveReports(() => report({ hourly: [group(String(first)), group(String(first + 7_200_000))] }));
  const { container } = render(withQueries(testQueryClient(), <App />));
  const details = () => container.querySelector(".dashboard-daily");
  await eventually(() => expect(details()?.textContent).toContain("Hourly values"));
  const rows = details()?.querySelectorAll("tbody tr");
  expect(rows).toHaveLength(3);
  expect(rows?.[1]?.textContent).toContain("$0.00");
});
