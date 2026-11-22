import { afterEach, expect, it, vi } from "vitest";
import type { Filters } from "../report";
import { tool, usage } from "../../test/fixtures";
import { click, eventually, render, testQueryClient, withQueries } from "../../test/render";
import { Sessions } from "./Sessions";
import { useSessions, type Session, type SessionDetail } from "./api";

const filters: Filters = { since: "7d", harness: "claude", scope: "current" };
const session: Session = {
  id: "claude:main/helper",
  title: "Update the import flow",
  harness: "claude",
  folder: "/work/imports",
  agentType: "main",
  startTs: 1_700_000_000_000,
  endTs: 1_700_000_060_000,
  durationMs: 60_000,
  usage: usage({ requests: 42 }),
  models: [{ model: "opus", cost: 1.5, requests: 42, share: 1 }],
  subagents: 1,
  prompts: 1,
  avgPromptChars: 46,
};

const excerpt = "<strong>Use the existing import helper.</strong>";
const detail: SessionDetail = {
  scope: "current",
  session,
  tools: [tool("Read")],
  subagents: [{ agentType: "explorer", title: "Find import callers", usage: usage() }],
  promptsVisible: true,
  prompts: [{
    ts: session.startTs,
    excerpt,
    chars: 46,
    signals: { yelling: 0, profanity: 0, anguish: 0, negation: 2, blame: 0, repeated: true },
  }],
};

const json = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status });
const list = (scope: Filters["scope"], sessions = [session]) => ({ scope, count: sessions.length, sessions, total: session.usage });

function View({ value }: { value: Filters }) {
  const sessions = useSessions(value, "imports");
  return <Sessions filters={value} project="imports" list={sessions} />;
}

function showSessions() {
  const client = testQueryClient();
  const withFilters = (value: Filters) => withQueries(client, <View value={value} />);
  const mounted = render(withFilters(filters));
  return { container: mounted.container, change: (value: Filters) => mounted.rerender(withFilters(value)) };
}

const button = (container: HTMLElement, label: string) => [...container.querySelectorAll("button")].find((b) => b.textContent?.trim() === label);

async function openSession(container: HTMLElement) {
  await eventually(() => expect(button(container, session.title)).toBeDefined());
  click(button(container, session.title)!);
}

afterEach(() => vi.unstubAllGlobals());

it("lists the sessions in the requested period, harness and project", async () => {
  // Given a project with one matching session
  const requests = vi.fn(async () => json(list("current")));
  vi.stubGlobal("fetch", requests);

  // When its sessions are shown
  const { container } = showSessions();

  // Then the filters are applied and no session detail has been requested
  await eventually(() => expect(button(container, session.title)).toBeDefined());
  expect(requests.mock.calls).toHaveLength(1);
  expect(requests).toHaveBeenCalledWith("/api/sessions?since=7d&harness=claude&project=current&limit=1000", expect.any(Object));
});

it("opens a session with its models, tools, subagents and prompts", async () => {
  // Given a session with activity and a prompt containing markup
  const requests = vi.fn(async (input: string) => json(input.startsWith("/api/sessions/") ? detail : list("current")));
  vi.stubGlobal("fetch", requests);
  const { container } = showSessions();

  // When the session is opened
  await openSession(container);

  // Then its detail is shown and the excerpt is rendered literally
  const contents = container.querySelector('[aria-label="Session detail"]')!;
  await eventually(() => expect(contents.textContent).toContain(excerpt));
  expect(contents.textContent).toContain("opus");
  expect(contents.textContent).toContain("Read");
  expect(contents.textContent).toContain("Find import callers");
  expect(contents.textContent).toContain("Negation 2, Repeated");
  expect(contents.querySelector("strong")).toBeNull();
  expect(requests).toHaveBeenCalledWith("/api/sessions/claude%3Amain%2Fhelper?since=7d&harness=claude&project=current", expect.any(Object));
});

it("hides prompt excerpts when the server withholds them", async () => {
  // Given a server that does not make prompt excerpts available
  vi.stubGlobal("fetch", vi.fn(async (input: string) => json(input.startsWith("/api/sessions/") ? { ...detail, promptsVisible: false } : list("current"))));
  const { container } = showSessions();

  // When the session is opened
  await openSession(container);

  // Then its prompt signals are visible without the excerpt
  await eventually(() => expect(container.textContent).toContain("Negation 2, Repeated"));
  expect(container.textContent).not.toContain(excerpt);
});

it("removes the open detail as soon as the project filter changes", async () => {
  // Given an open session and a slow list of every project
  vi.stubGlobal("fetch", vi.fn(async (input: string) => {
    if (input.startsWith("/api/sessions/")) return json(detail);
    if (input.includes("project=all")) return new Promise<Response>(() => {});
    return json(list("current"));
  }));
  const { container, change } = showSessions();
  await openSession(container);
  await eventually(() => expect(container.textContent).toContain(excerpt));

  // When every project is requested
  change({ ...filters, scope: "all" });

  // Then the previous project detail is gone while the list loads
  expect(container.textContent).not.toContain(excerpt);
  expect(container.querySelector('[aria-label="Session detail"]')).toBeNull();
  expect(container.querySelector('[role="status"]')?.textContent).toBe("Loading sessions…");
});

it("says when no sessions match the filters", async () => {
  vi.stubGlobal("fetch", vi.fn(async () => json(list("current", []))));
  const { container } = showSessions();

  await eventually(() => expect(container.textContent).toContain("No sessions match these filters."));
});

it("shows the server's refusal when a session is outside the filters", async () => {
  vi.stubGlobal("fetch", vi.fn(async (input: string) => input.startsWith("/api/sessions/")
    ? json({ title: "Session not found in this view" }, 404)
    : json(list("current"))));
  const { container } = showSessions();
  await openSession(container);

  await eventually(() => expect(container.querySelector('[role="alert"]')?.textContent).toContain("Session not found in this view"));
});
