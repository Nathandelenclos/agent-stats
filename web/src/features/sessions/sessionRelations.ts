import type { SessionDetail } from "./api";

function literal(text: string) {
  // Logs supply titles, never Mermaid directives or markup.
  return text.replace(/[^\p{L}\p{N}\p{M} .,:+_()/-]/gu, " ").replace(/\s+/g, " ").trim();
}

function shortLabel(title: string, role?: string) {
  const label = literal(title);
  const characters = Array.from(label);
  if (characters.length <= 44) return label || "Session";
  const agent = role && literal(role);
  return agent && Array.from(agent).length <= 44
    ? agent
    : characters.slice(0, 43).join("").trimEnd() + "…";
}

export function sessionRelations({ session, subagents, forkedFrom }: SessionDetail) {
  const names = [
    session.title || "Untitled session",
    ...subagents.map((agent) => agent.title || agent.agentType || "Subagent"),
  ];
  const ids = new Map([[session.id, 0]]);
  for (const [index, agent] of subagents.entries()) {
    if (agent.id) ids.set(agent.id, index + 1);
  }
  if (forkedFrom) names.push("Fork source");

  const rows = ["flowchart LR"];
  const occurrences = new Map<string, number>();
  for (const [index, name] of names.entries()) {
    const label = shortLabel(name, index > 0 ? subagents[index - 1]?.agentType : undefined);
    const count = (occurrences.get(label) ?? 0) + 1;
    occurrences.set(label, count);
    const visible = count === 1 ? label : `${label} (${count})`;
    const style = index === 0 ? ":::mainSession" : forkedFrom && index === subagents.length + 1 ? ":::forkSource" : "";
    rows.push(`agent${index}("${visible}")${style}`);
  }
  for (const [index, agent] of subagents.entries()) {
    const parent = agent.parentId ? ids.get(agent.parentId) : undefined;
    if (parent !== undefined && parent !== index + 1) rows.push(`agent${parent} --> agent${index + 1}`);
  }
  if (forkedFrom) rows.push(`agent${names.length - 1} -.-> agent0`);
  return { source: rows.join("\n"), names };
}
