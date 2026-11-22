import { expect, it } from "vitest";
import mermaid from "mermaid";
import { usage } from "../../test/fixtures";
import type { SessionDetail } from "./api";
import { sessionRelations } from "./sessionRelations";

it("generates a flowchart with a distinct fork link and literal labels from session logs", async () => {
  const detail: SessionDetail = {
    scope: "all",
    session: {
      id: "codex:main", harness: "codex", title: 'Main"]\nclick agent0 href "https://example.com"\n%%{init: {}}%%',
      folder: "", agentType: "main", startTs: 0, endTs: 0, durationMs: 0, usage: usage(), models: [],
      subagents: 5, prompts: 0, avgPromptChars: 0,
    },
    forkedFrom: "codex:source",
    tools: [], prompts: [], promptsVisible: false,
    subagents: [
      { id: "codex:review", parentId: "codex:main", agentType: "reviewer", title: "Review", usage: usage() },
      { id: "codex:test", parentId: "codex:review", agentType: "worker", title: "Check", usage: usage() },
      { id: "codex:orphan", parentId: "codex:hidden", agentType: "worker", title: "Unlinked", usage: usage() },
      { agentType: "reviewer", title: "This is a long review prompt that should stay readable in the detail panel", usage: usage() },
      { agentType: "reviewer", title: "This is another long review prompt that would widen the diagram", usage: usage() },
    ],
  };

  const { source, names } = sessionRelations(detail);

  await expect(mermaid.parse(source)).resolves.toMatchObject({ diagramType: expect.stringContaining("flowchart") });
  expect(source.match(/^agent.*(?:-->|-\.->).*/gm)).toEqual([
    "agent0 --> agent1",
    "agent1 --> agent2",
    "agent6 -.-> agent0",
  ]);
  expect(source).toContain('agent0("Main click agent0 href');
  expect(source).toContain(":::mainSession");
  expect(source).toContain('agent4("reviewer")');
  expect(source).toContain('agent5("reviewer (2)")');
  expect(names[5]).toBe(detail.subagents[4]?.title);
  expect(source).not.toContain("\nclick");
  expect(source).not.toContain("%%{");
  expect(source).not.toContain("codex:hidden");
  expect(names[0]).toBe(detail.session.title);
});
