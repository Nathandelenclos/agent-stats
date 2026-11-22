import { afterEach, describe, expect, it, vi } from "vitest";
import { App } from "./App";
import { emptyReport, report, serveReports, tool } from "./test/fixtures";
import { eventually, render, testQueryClient, withQueries } from "./test/render";

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
