import { afterEach, expect, it, vi } from "vitest";
import { click, eventually, render, testQueryClient, withQueries } from "../../test/render";
import { tool } from "../../test/fixtures";
import { Analytics } from "./Analytics";
import type { MeasuredActivity } from "./activitySeries";

const sample = (overrides: Partial<MeasuredActivity>): MeasuredActivity => ({
  bucket: "2026-11-22", name: "model", source: "a", calls: 2, errors: 1,
  avgDurationMs: 1200, avgTtftMs: 300, avgTokensPerSecond: 42,
  durationSamples: 1, ttftSamples: 1, throughputSamples: 1,
  input: 100, output: 50, cacheRead: 100, cacheWrite: 0, cost: 0.5, premiumUnits: 0, unpriced: 0,
  ...overrides,
});
const filters = { since: "7d", harness: "", scope: "current" as const };
const button = (container: HTMLElement, name: string) => [...container.querySelectorAll<HTMLButtonElement>("button")]
  .find((element) => element.textContent?.trim() === name);

afterEach(() => vi.unstubAllGlobals());

it("compares providers separately and opens requests for the exact model and provider", async () => {
  const fetch = vi.fn(async () => new Response(JSON.stringify({
    bucketSize: "day", samples: [
      sample({ source: "endpoints-a" }),
      sample({ source: "endpoints-b", durationSamples: 0, ttftSamples: 0, throughputSamples: 0 }),
    ],
  })));
  vi.stubGlobal("fetch", fetch);
  const openRequests = vi.fn();
  const { container } = render(withQueries(testQueryClient(),
    <Analytics kind="models" filters={filters} project="stats" openRequests={openRequests} openTool={vi.fn()} />,
  ));
  await eventually(() => expect(button(container, "model · endpoints-b")).toBeDefined());
  expect(fetch).toHaveBeenCalledWith("/api/activity-series?kind=models&since=7d&project=current", expect.anything());
  click(button(container, "Share")!);
  expect(container.textContent).toContain("50%");
  click(button(container, "model · endpoints-b")!);
  expect(container.textContent).toContain("0 of 2 measured");
  expect(container.textContent).toContain("Unavailable");
  click(button(container, "View model requests")!);
  expect(openRequests).toHaveBeenCalledWith("model", "endpoints-b");
});

it("uses the exact MCP server when opening a tool and keeps zero-error periods explicit", async () => {
  vi.stubGlobal("fetch", vi.fn(async () => new Response(JSON.stringify({
    bucketSize: "day", samples: [sample({ name: "read", source: "memory", errors: 0 })],
  }))));
  const openTool = vi.fn();
  const { container } = render(withQueries(testQueryClient(),
    <Analytics kind="tools" filters={filters} project="stats" openRequests={vi.fn()} openTool={openTool}
      tools={[{ ...tool("read"), server: "memory" }]} />,
  ));
  await eventually(() => expect(button(container, "memory / read")).toBeDefined());
  const errorsSwitch = container.querySelector('[role="group"]') as HTMLElement;
  click(button(errorsSwitch, "Errors")!);
  expect(container.textContent).toContain("No errors in this period.");
  click(button(container, "memory / read")!);
  expect(openTool).toHaveBeenCalledWith("read", "memory");
});
