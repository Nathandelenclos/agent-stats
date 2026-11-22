import type { SessionDetail } from "./api";

function label(text: string) {
  // Titles come from logs. Keep them on a single literal label; Mermaid directives and markup are never input.
  return Array.from(text.replace(/[^\p{L}\p{N}\p{M} .,:+_()/-]/gu, " ").replace(/\s+/g, " ").trim()).slice(0, 72).join("");
}

export function sessionRelations({ session, subagents, forkedFrom }: SessionDetail) {
  const names = [
    session.title || "Untitled session",
    ...subagents.map((agent) => agent.title || agent.agentType || "Subagent"),
  ];
  const ids = new Map([[session.id, 0]]);
  const rows = ["classDiagram", "direction LR"];
  for (const [index, agent] of subagents.entries()) {
    if (agent.id) ids.set(agent.id, index + 1);
  }
  if (forkedFrom) names.push("Fork source");

  for (const [index, name] of names.entries()) {
    rows.push(`class agent${index}["${label(name) || "Session"}"]`);
    const role = index === 0 ? session.harness : subagents[index - 1]?.agentType;
    if (role && label(role)) rows.push(`agent${index} : ${label(role)}`);
  }
  for (const [index, agent] of subagents.entries()) {
    const parent = agent.parentId ? ids.get(agent.parentId) : undefined;
    if (parent !== undefined && parent !== index + 1) rows.push(`agent${parent} --> agent${index + 1} : recorded parent`);
  }
  if (forkedFrom) rows.push(`agent${names.length - 1} ..> agent0 : fork`);
  return { source: rows.join("\n"), names };
}
