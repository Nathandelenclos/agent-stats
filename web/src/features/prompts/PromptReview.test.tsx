import { afterEach, expect, it, vi } from "vitest";
import { App } from "../../App";
import { metaInProject, report } from "../../test/fixtures";
import { click, eventually, render, testQueryClient, withQueries } from "../../test/render";
import type { PromptReviewData } from "./api";

afterEach(() => vi.unstubAllGlobals());

const sample: PromptReviewData = {
  scope: "current", count: 2, sessions: 2, promptsVisible: true,
  recurring: [{ excerpt: "Keep the file", count: 2, sessions: 2, firstTs: 0, lastTs: 0 }],
  byFolder: [{ folder: "/work/app", prompts: 2, sessions: 2, signals: 2 }],
  prompts: [{
    session: "claude:42", harness: "claude", title: "Build", folder: "/work/app", ts: 0,
    excerpt: "<script>Keep the file</script>",
    signals: { yelling: 0, profanity: 0, anguish: 0, negation: 1, blame: 0, repeated: false },
  }],
};

function openReview(data: PromptReviewData, error = false) {
  window.history.replaceState(null, "", "/");
  const fetch = vi.fn(async (input: RequestInfo | URL) => {
    const target = new URL(String(input), window.location.origin);
    switch (target.pathname) {
      case "/api/meta": return Response.json(metaInProject("app"));
      case "/api/sync": return Response.json({ state: "disabled", indexed: 0 });
      case "/api/report": return Response.json(report({ scope: "current" }));
      case "/api/prompts":
        if (target.searchParams.get("project") === "all") return Response.json({ ...data, scope: "all", count: 0 });
        return error ? Response.json({ title: "Read failed" }, { status: 403 }) : Response.json(data);
      default: return new Response(null, { status: 404 });
    }
  });
  vi.stubGlobal("fetch", fetch);
  return { ...render(withQueries(testQueryClient(), <App />)), fetch };
}

async function choosePrompts(container: HTMLElement) {
  const button = () => [...container.querySelectorAll("nav button")].find((b) => b.textContent === "Prompts");
  await eventually(() => expect(button()).toBeDefined());
  click(button()!);
}

it("opens the review with the current project and displays text as text", async () => {
  const { container, fetch } = openReview(sample);
  await choosePrompts(container);
  await eventually(() => expect(container.textContent).toContain("<script>Keep the file</script>"));
  expect(container.textContent).toContain("2 flagged prompts across 2 sessions");
  expect(container.textContent).toContain("Negation 1");
  expect(container.querySelector("script")).toBeNull();
  const asked = fetch.mock.calls.map(([url]) => new URL(String(url), window.location.origin));
  expect(asked.find((url) => url.pathname === "/api/prompts")?.searchParams.get("project")).toBe("current");
});

it("keeps the signals visible when excerpts are unavailable", async () => {
  const { container } = openReview({ ...sample, promptsVisible: false });
  await choosePrompts(container);
  await eventually(() => expect(container.textContent).toContain("Negation 1"));
  expect(container.textContent).not.toContain("Keep the file");
});

it("reports a failure to load the review", async () => {
  const { container } = openReview(sample, true);
  await choosePrompts(container);
  await eventually(() => expect(container.textContent).toContain("Read failed"));
});

it("removes the previous review when the project filter changes", async () => {
  const { container } = openReview(sample);
  await choosePrompts(container);
  await eventually(() => expect(container.textContent).toContain("Keep the file"));
  const global = [...container.querySelectorAll('[aria-label="Projects"] button')].find((b) => b.textContent === "All projects");
  expect(global).toBeDefined();

  click(global!);

  expect(container.textContent).not.toContain("Keep the file");
  await eventually(() => expect(container.textContent).toContain("No flagged prompts match these filters."));
});
