import { focusManager } from "@tanstack/react-query";
import { act } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { App } from "./App";
import type { Report } from "./features/report";
import { emptyReport, group, meta, metaInProject, report, serveReports, tool, usage } from "./test/fixtures";
import type { SyncStatus } from "./features/sync";
import { choose, click, eventually, render, selectLabelled, testQueryClient, withQueries } from "./test/render";

beforeEach(() => window.history.replaceState(null, "", "?view=usage"));
afterEach(() => {
  vi.unstubAllGlobals();
  window.history.replaceState(null, "", "/");
});

const open = (container: HTMLElement) => ({
  text: () => container.textContent ?? "",
  tables: () => [...container.querySelectorAll("table")].map((t) => t.querySelector("caption")?.textContent),
});

describe("the report page", () => {
  it("shows the tools of a period with tool calls but no model request", async () => {
    // Given a period after the last model request but before a tool call
    serveReports(() => emptyReport({ tools: [tool("Bash", 4)] }));

    // When the page loads it
    const { container } = render(withQueries(testQueryClient(), <App />));

    // Then the totals say there is no request, and the tools are still there
    const page = open(container);
    await eventually(() => expect(page.tables()).toContain("Tools"));
    expect(page.text()).toContain("No requests in this period.");
    expect(page.text()).toContain("Bash");
  });

  it("shows estimated costs beside proportional bars by coding agent", async () => {
    serveReports(() => report({
      byHarness: [
        group("codex", { cost: 9.20 }),
        group("claude", { cost: 5.60 }),
        group("gemini", { requests: 1, cost: 0, unpriced: 1 }),
      ],
      byModel: [],
    }));
    const { container } = render(withQueries(testQueryClient(), <App />));
    const byAgent = () => container.querySelector('[role="list"][aria-label="Estimated cost by coding agent"]');
    await eventually(() => expect(byAgent()?.querySelectorAll("li")).toHaveLength(3));

    const rows = [...byAgent()!.querySelectorAll("li")];
    expect(rows.map((row) => row.textContent)).toEqual(["Codex$9.20", "Claude Code$5.60", "geminiUnavailable"]);
    const width = (row: HTMLLIElement) => parseFloat(row.querySelector<HTMLElement>('[aria-hidden="true"] > span')!.style.width);
    expect(width(rows[0]!)).toBe(100);
    expect(width(rows[1]!)).toBeCloseTo(100 * 5.60 / 9.20);
    expect(width(rows[2]!)).toBe(0);
  });

  it("shows only the empty totals when the period has nothing at all", async () => {
    // Given a period with no request and no tool call
    serveReports(() => emptyReport());

    // When the page loads it
    const { container } = render(withQueries(testQueryClient(), <App />));

    // Then it says so once, without empty tables or charts
    const page = open(container);
    await eventually(() => expect(page.text()).toContain("No requests in this period."));
    expect(page.tables()).toEqual([]);
    expect(container.querySelector("figure")).toBeNull();
  });

  it("shows each zone according to its own data", async () => {
    // Given requests but no tool call
    serveReports(() => report({ tools: [] }));

    // When the page loads it
    const { container } = render(withQueries(testQueryClient(), <App />));

    // Then the usage tables are there and the tools table, with nothing to list, is not
    const page = open(container);
    await eventually(() => expect(page.tables()).toContain("Usage by model"));
    expect(page.tables()).not.toContain("Tools");
  });
});

const periodSelect = (container: HTMLElement) => selectLabelled(container, "Period");
const header = (container: HTMLElement, table: string, name: string) =>
  [...container.querySelectorAll(`[role="region"][aria-label^="${table}"] th`)].find((th) => th.textContent?.startsWith(name));
const models = (n: number) => Array.from({ length: n }, (_, i) => group(`model-${i}`, { requests: i + 1, cost: n - i }));

describe("changing the filters", () => {
  beforeEach(() => vi.spyOn(console, "error").mockImplementation(() => {}));

  it("lets a zone that failed on the old data recover when the new data arrives, without touching the others", async () => {
    // Given a report whose folder table cannot be drawn, because of one broken row
    serveReports((since) =>
      since === "7d" ? report({ byFolder: [null as unknown as Report["byFolder"][number]] }) : report({ total: usage({ requests: 4242 }) }),
    );
    const { container } = render(withQueries(testQueryClient(), <App />));
    const page = () => container.textContent ?? "";
    await eventually(() => expect(page()).toContain("The folder table could not be drawn."));

    // Then only that zone is in error: the totals and the other tables are drawn
    expect(page()).toContain("Usage by model");
    expect(container.querySelector("dl")).not.toBeNull();

    // When another period is chosen and its report, which is valid, arrives
    choose(periodSelect(container), "30d");
    await eventually(() => expect(page()).toContain("4,242"));

    // Then the zone is drawn again
    expect(page()).not.toContain("could not be drawn");
    expect(container.querySelector('[role="region"][aria-label^="Usage by folder"]')).not.toBeNull();
  });

  it("keeps the sort and the expansion of a table across the change", async () => {
    // Given a model table with 20 rows, sorted by requests and shown in full
    serveReports((since) => report({ byModel: models(20), total: usage({ requests: since === "7d" ? 10 : 4242 }) }));
    const { container } = render(withQueries(testQueryClient(), <App />));
    await eventually(() => expect(header(container, "Usage by model", "Requests")).toBeDefined());
    click(header(container, "Usage by model", "Requests")!.querySelector("button")!);
    click([...container.querySelectorAll("button")].find((b) => b.textContent === "Show all 20")!);
    expect(header(container, "Usage by model", "Requests")!.getAttribute("aria-sort")).toBe("descending");

    // When another period is chosen and its report arrives
    choose(periodSelect(container), "30d");
    await eventually(() => expect(container.textContent).toContain("4,242"));

    // Then the table is as the person left it
    expect(header(container, "Usage by model", "Requests")!.getAttribute("aria-sort")).toBe("descending");
    expect(header(container, "Usage by model", "Cost")!.getAttribute("aria-sort")).toBe("none");
    expect([...container.querySelectorAll("button")].some((b) => b.textContent === "Show the first 15")).toBe(true);
  });
});

const scopeButton = (container: HTMLElement, name: string) =>
  [...container.querySelectorAll('[role="group"][aria-label="Projects"] button')].find((b) => b.textContent === name) as HTMLElement;
const headerText = (container: HTMLElement) => container.querySelector("header")?.textContent ?? "";

describe("the scope of the report", () => {
  afterEach(() => window.history.replaceState(null, "", "/"));

  it("says in the header that the report covers every project when the server found none", async () => {
    // Given a server started outside any project
    serveReports(() => report());

    // When the page loads
    const { container } = render(withQueries(testQueryClient(), <App />));

    // Then the header says all projects are covered, and there is no toggle
    await eventually(() => expect(headerText(container)).toContain("All projects"));
    expect(container.querySelector('[role="group"][aria-label="Projects"]')).toBeNull();
  });

  it("opens on the project the server runs in, named in the header, and asks for it", async () => {
    // Given a server started in the app project
    const fetch = serveReports(() => report(), metaInProject("app"));

    // When the page loads
    const { container } = render(withQueries(testQueryClient(), <App />));

    // Then the header names the project and the report asked for it
    await eventually(() => expect(headerText(container)).toContain("Project: app"));
    const asked = fetch.mock.calls.map(([url]) => new URL(String(url), "http://localhost")).filter((u) => u.pathname === "/api/report");
    expect(asked.map((u) => u.searchParams.get("project"))).toContain("current");
    expect(asked.map((u) => u.searchParams.get("project"))).not.toContain("all");
  });

  it("switches to every project with the toggle, which the header and the report follow", async () => {
    // Given a page opened on the app project, whose figures differ from the other projects'
    serveReports((_since, _harness, project) => report({ total: usage({ requests: project === "all" ? 4242 : 7 }) }), metaInProject("app"));
    const { container } = render(withQueries(testQueryClient(), <App />));
    await eventually(() => expect(container.textContent).toContain("Project: app"));

    // When the person chooses every project
    click(scopeButton(container, "All projects"));

    // Then the header and the figures are those of every project, and the address keeps the choice
    await eventually(() => expect(container.textContent).toContain("4,242"));
    expect(headerText(container)).toContain("All projects");
    expect(scopeButton(container, "All projects").getAttribute("aria-pressed")).toBe("true");
    expect(new URLSearchParams(window.location.search).get("project")).toBe("all");
  });

  it("keeps naming the scope of the figures on screen until the report of the new scope arrives", async () => {
    // Given a page opened on the app project, and a report of every project that is slow to come
    let release: () => void = () => {};
    const slow = new Promise<void>((resolve) => (release = resolve));
    serveReports(async (_since, _harness, project) => {
      if (project === "all") await slow;
      return report({ total: usage({ requests: project === "all" ? 4242 : 7 }) });
    }, metaInProject("app"));
    const { container } = render(withQueries(testQueryClient(), <App />));
    await eventually(() => expect(container.textContent).toContain("Project: app"));

    // When the person chooses every project and its figures have not arrived
    click(scopeButton(container, "All projects"));
    await eventually(() => expect(container.querySelector('[aria-busy="true"]')).not.toBeNull());

    // Then the page still names the project, whose figures are the ones on screen, and shows them as outdated
    expect(headerText(container)).toContain("Project: app");
    expect(container.textContent).not.toContain("4,242");
    expect(container.querySelector('[aria-busy="true"]')!.className).toContain("opacity-60");

    // And once they arrive, the name and the figures are those of every project
    await act(async () => release());
    await eventually(() => expect(container.textContent).toContain("4,242"));
    expect(headerText(container)).toContain("All projects");
    expect(container.querySelector('[aria-busy="true"]')).toBeNull();
  });

  it("learns the project of the server when its indexing ends, which an empty index could not tell at first", async () => {
    // Given a server started in a folder whose sessions are not indexed yet, so that it knows no project
    let indexing: SyncStatus["state"] = "running";
    const fetch = serveReports(() => report(), () => (indexing === "done" ? metaInProject("app") : meta()), () => indexing);
    const { container } = render(withQueries(testQueryClient(), <App />));
    await eventually(() => expect(headerText(container)).toContain("All projects"));
    expect(scopeButton(container, "All projects")).toBeUndefined();

    // When the indexing ends and the server has found its project
    indexing = "done";

    // Then the page names it, opens on it and offers to switch
    await eventually(() => expect(headerText(container)).toContain("Project: app"), 300);
    expect(scopeButton(container, "This project").getAttribute("aria-pressed")).toBe("true");
    const asked = fetch.mock.calls.map(([url]) => new URL(String(url), "http://localhost")).filter((u) => u.pathname === "/api/report");
    expect(asked.at(-1)!.searchParams.get("project")).toBe("current");
  });

  it("opens on every project when the address says so, even in a project", async () => {
    // Given an address that keeps the choice of every project
    window.history.replaceState(null, "", "?since=7d&harness=&project=all");
    const fetch = serveReports(() => report(), metaInProject("app"));

    // When the page loads
    const { container } = render(withQueries(testQueryClient(), <App />));

    // Then it covers every project, and never asked for the project
    await eventually(() => expect(headerText(container)).toContain("All projects"));
    const asked = fetch.mock.calls.map(([url]) => new URL(String(url), "http://localhost")).filter((u) => u.pathname === "/api/report");
    expect(asked.map((u) => u.searchParams.get("project"))).toEqual(["all"]);
  });

  it("ignores the project in the address when the server found none", async () => {
    // Given an address kept from a server that was in a project
    window.history.replaceState(null, "", "?since=7d&harness=&project=current");
    const fetch = serveReports(() => report());

    // When the page loads, on a server with no project
    const { container } = render(withQueries(testQueryClient(), <App />));

    // Then it asks for every project: the server would refuse the other
    await eventually(() => expect(headerText(container)).toContain("All projects"));
    const asked = fetch.mock.calls.map(([url]) => new URL(String(url), "http://localhost")).filter((u) => u.pathname === "/api/report");
    expect(asked.map((u) => u.searchParams.get("project"))).toEqual(["all"]);
  });
});

it("opens the sessions view with the report's filters", async () => {
  // Given a usage report for the current project
  const fetchReport = serveReports(() => report(), metaInProject("imports"));
  const fetch = vi.fn(async (input: RequestInfo | URL) => {
    if (String(input).startsWith("/api/sessions?")) {
      return new Response(JSON.stringify({ scope: "current", count: 0, total: {}, sessions: [] }), { status: 200 });
    }
    return fetchReport(input);
  });
  vi.stubGlobal("fetch", fetch);
  const { container } = render(withQueries(testQueryClient(), <App />));
  const sessions = () => [...container.querySelectorAll("nav button")].find((b) => b.textContent === "Sessions");
  await eventually(() => expect(container.textContent).toContain("Usage by model"));

  // When the person changes view
  click(sessions()!);

  // Then Sessions is loaded for the same period and project
  await eventually(() => expect(container.textContent).toContain("No sessions match these filters."));
  expect(sessions()?.getAttribute("aria-pressed")).toBe("true");
  expect(fetch).toHaveBeenCalledWith("/api/sessions?since=7d&project=current&limit=1000", expect.any(Object));
  window.history.replaceState(null, "", "/");
});

it("keeps view navigation before the main content and opens Prompts with the active filters", async () => {
  const fetchReport = serveReports(() => report(), metaInProject("imports"));
  const fetch = vi.fn(async (input: RequestInfo | URL) => {
    if (String(input).startsWith("/api/prompts?")) {
      return new Response(JSON.stringify({
        scope: "current", count: 0, sessions: 0, promptsVisible: true, prompts: [], recurring: [], byFolder: [],
      }));
    }
    return fetchReport(input);
  });
  vi.stubGlobal("fetch", fetch);
  const { container } = render(withQueries(testQueryClient(), <App />));
  await eventually(() => expect(container.textContent).toContain("Usage by model"));
  choose(periodSelect(container), "30d");

  const nav = container.querySelector('nav[aria-label="Views"]')!;
  const main = container.querySelector("main#main")!;
  const buttons = [...nav.querySelectorAll("button")];
  expect(container.querySelector('a[href="#main"]')?.textContent?.trim()).toBe("Skip to main content");
  expect(main.contains(nav)).toBe(false);
  expect(nav.compareDocumentPosition(main) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  expect(buttons.map((button) => button.textContent)).toEqual(["Overview", "Usage", "Sessions", "Prompts"]);
  expect(buttons.map((button) => button.getAttribute("aria-pressed"))).toEqual(["false", "true", "false", "false"]);

  const prompts = buttons[3]!;
  prompts.focus();
  click(prompts);

  await eventually(() => expect(main.textContent).toContain("No flagged prompts match these filters."));
  expect(document.activeElement).toBe(prompts);
  expect(buttons.map((button) => button.getAttribute("aria-pressed"))).toEqual(["false", "false", "false", "true"]);
  expect(fetch).toHaveBeenCalledWith("/api/prompts?since=30d&project=current&limit=1000", expect.any(Object));
  expect(periodSelect(container).value).toBe("30d");
  window.history.replaceState(null, "", "/");
});

it("keeps report filters when opening Catalog and lets the person ask for unused entries", async () => {
  window.history.replaceState(null, "", "?view=usage&since=30d&harness=claude&project=current");
  const fetchReport = serveReports(() => report(), { ...metaInProject("imports"), catalogAvailable: true });
  const unused = { harness: "claude", kind: "skill", scope: "user", unit: "loads", enabled: true, uses: 0, lastTs: 0, name: "testing" };
  const fetch = vi.fn(async (input: RequestInfo | URL) => {
    const requested = new URL(String(input), "http://localhost");
    if (requested.pathname === "/api/catalog") {
      const entries = requested.searchParams.get("unused") === "true"
        ? [unused]
        : [unused, { ...unused, uses: null, name: "untracked" }];
      return new Response(JSON.stringify({ entries, scope: "current", skipped: 1 }));
    }
    return fetchReport(input);
  });
  vi.stubGlobal("fetch", fetch);
  const { container } = render(withQueries(testQueryClient(), <App />));
  const byName = (name: string) => [...container.querySelectorAll("nav button")].find((button) => button.textContent === name);
  await eventually(() => expect(container.textContent).toContain("Usage by model"));
  expect(fetch.mock.calls.some(([url]) => String(url).startsWith("/api/catalog"))).toBe(false);

  click(byName("Catalog")!);

  const entries = () => [...container.querySelectorAll("tbody tr")].map((row) => row.textContent ?? "");
  await eventually(() => expect(entries()).toHaveLength(2));
  expect(fetch).toHaveBeenCalledWith("/api/catalog?since=30d&harness=claude&project=current", expect.any(Object));
  expect(byName("Catalog")?.getAttribute("aria-pressed")).toBe("true");
  expect(byName("Quotas")).toBeUndefined();
  expect(entries().find((row) => row.includes("testing"))).toContain("0 loadsNever");
  expect(entries().find((row) => row.includes("untracked"))).toContain("UnknownUnknown");
  expect(container.textContent).toContain("1 source was skipped.");
  const checkbox = container.querySelector<HTMLInputElement>('input[type="checkbox"]')!;
  expect(checkbox.labels?.[0]?.textContent).toContain("Unused only");
  click(checkbox);

  await eventually(() => expect(entries()).toHaveLength(1));
  expect(fetch).toHaveBeenCalledWith("/api/catalog?since=30d&harness=claude&project=current&unused=true", expect.any(Object));
  expect(entries()[0]).toContain("testing");
  expect(checkbox.checked).toBe(true);
  window.history.replaceState(null, "", "/");
});

it("reads account quotas without usage filters and refreshes them on demand", async () => {
  window.history.replaceState(null, "", "?view=usage&since=30d&harness=claude&project=current");
  const fetchReport = serveReports(() => report(), { ...metaInProject("imports"), quotasAvailable: true });
  let calls = 0;
  const snapshot = {
    provider: "Codex", label: "Weekly quota", remaining: 0, resetAt: "0001-01-01T00:00:00Z", observedAt: new Date().toISOString(),
  };
  const fetch = vi.fn(async (input: RequestInfo | URL) => {
    if (String(input) === "/api/quotas") {
      calls++;
      return new Response(JSON.stringify(calls === 1 ? [
        snapshot,
        { ...snapshot, label: "Expired quota", remaining: 42, resetAt: new Date(Date.now() - 60_000).toISOString() },
      ] : [{ ...snapshot, remaining: 73 }]));
    }
    return fetchReport(input);
  });
  vi.stubGlobal("fetch", fetch);
  const { container } = render(withQueries(testQueryClient(), <App />));
  const quotas = () => [...container.querySelectorAll("nav button")].find((button) => button.textContent === "Quotas");
  await eventually(() => expect(container.textContent).toContain("Usage by model"));
  expect(calls).toBe(0);
  click(quotas()!);

  await eventually(() => expect(container.textContent).toContain("0% remaining"));
  expect(quotas()?.getAttribute("aria-pressed")).toBe("true");
  expect(container.textContent).toContain("Quota unavailable.");
  expect(container.textContent).toContain("Resetsunknown");
  expect(container.textContent).not.toContain("Expired quota");
  expect([...container.querySelectorAll("label")].some((label) => label.textContent === "Period")).toBe(false);
  expect(fetch.mock.calls.filter(([url]) => String(url).startsWith("/api/quotas")).map(([url]) => url)).toEqual(["/api/quotas"]);

  await act(async () => {
    focusManager.setFocused(false);
    focusManager.setFocused(true);
  });
  expect(calls).toBe(1);

  const refresh = [...container.querySelectorAll("button")].find((button) => button.textContent === "Refresh quotas")!;
  refresh.focus();
  click(refresh);
  await eventually(() => expect(container.textContent).toContain("73% remaining"));
  expect(document.activeElement).toBe(refresh);
  expect(calls).toBe(2);

  focusManager.setFocused(undefined);
  window.history.replaceState(null, "", "/");
});

it("shows provider performance with the measured-request counts", async () => {
  serveReports(() => report({ byProvider: [group("anthropic", {
    requests: 4, avgDurationMs: 1250, avgTtftMs: 250, avgTokensPerSecond: 30,
    durationSamples: 2, ttftSamples: 1, throughputSamples: 2,
  })] }));
  const { container } = render(withQueries(testQueryClient(), <App />));
  const provider = () => container.querySelector('[aria-label="Usage by provider, scrollable"]');
  await eventually(() => expect(provider()).not.toBeNull());
  expect(provider()?.textContent).toContain("anthropic");
  expect(provider()?.textContent).toContain("1.3 s (2/4)");
  expect(provider()?.textContent).toContain("250 ms (1/4)");
  expect(provider()?.textContent).toContain("30.0 (2/4)");
});
