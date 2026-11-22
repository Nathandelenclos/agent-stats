import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { App } from "./App";
import type { Report } from "./features/report";
import { emptyReport, group, report, serveReports, tool, usage } from "./test/fixtures";
import { choose, click, eventually, render, selectLabelled, testQueryClient, withQueries } from "./test/render";

afterEach(() => vi.unstubAllGlobals());

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
