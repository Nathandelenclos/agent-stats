import { expect, it } from "vitest";
import mermaid from "mermaid";
import { usage } from "../../test/fixtures";
import type { SessionDetail } from "./api";
import { sessionRelations } from "./sessionRelations";

it("generates a class diagram with typed links and literal labels from session logs", async () => {
  const detail: SessionDetail = {
    scope: "all",
    session: {
      id: "codex:main", harness: "codex", title: 'Main"]\nclick agent0 href "https://example.com"\n%%{init: {}}%%',
      folder: "", agentType: "main", startTs: 0, endTs: 0, durationMs: 0, usage: usage(), models: [],
      subagents: 3, prompts: 0, avgPromptChars: 0,
    },
    forkedFrom: "codex:source",
    tools: [], prompts: [], promptsVisible: false,
    subagents: [
      { id: "codex:review", parentId: "codex:main", agentType: "reviewer", title: "Review", usage: usage() },
      { id: "codex:test", parentId: "codex:review", agentType: "worker", title: "Check", usage: usage() },
      { id: "codex:orphan", parentId: "codex:hidden", agentType: "worker", title: "Unlinked", usage: usage() },
    ],
  };

  const { source } = sessionRelations(detail);

  await expect(mermaid.parse(source)).resolves.toMatchObject({ diagramType: expect.stringContaining("class") });
  expect(source.match(/^agent.*(?:-->|\.\.>).*/gm)).toEqual([
    "agent0 --> agent1 : recorded parent",
    "agent1 --> agent2 : recorded parent",
    "agent4 ..> agent0 : fork",
  ]);
  expect(source).not.toContain("\nclick");
  expect(source).not.toContain("%%{");
  expect(source).not.toContain("codex:hidden");
});
