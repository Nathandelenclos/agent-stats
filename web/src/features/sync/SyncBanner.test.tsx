import { afterEach, expect, it, vi } from "vitest";
import { click, eventually, render, testQueryClient, withQueries } from "../../test/render";
import { SyncBanner } from "./SyncBanner";
import type { SyncStatus } from "./api";

afterEach(() => vi.unstubAllGlobals());

const finished: SyncStatus = { state: "done", indexed: 2, startedAt: 1, finishedAt: 2 };
const running: SyncStatus = { state: "running", indexed: 0, startedAt: 3, finishedAt: 0 };

it("starts an indexing run from the banner", async () => {
  const fetch = vi.fn(async () => Response.json(running));
  vi.stubGlobal("fetch", fetch);
  const client = testQueryClient();
  const { container } = render(withQueries(client, <SyncBanner status={finished} unreachable={false} />));

  click(container.querySelector("button")!);

  await eventually(() => expect(client.getQueryData(["sync"])).toEqual(running));
  expect(fetch).toHaveBeenCalledOnce();
  expect(fetch).toHaveBeenCalledWith("/api/sync", expect.objectContaining({ method: "POST", credentials: "same-origin" }));
});

it("disables the reindex button while indexing runs", () => {
  const { container } = render(withQueries(testQueryClient(), <SyncBanner status={running} unreachable={false} />));
  expect(container.querySelector("button")?.disabled).toBe(true);
});

it("leaves out the reindex button when indexing is disabled", () => {
  const disabled: SyncStatus = { state: "disabled", indexed: 0, startedAt: 0, finishedAt: 0 };
  const { container } = render(withQueries(testQueryClient(), <SyncBanner status={disabled} unreachable={false} />));
  expect(container.querySelector("button")).toBeNull();
});
