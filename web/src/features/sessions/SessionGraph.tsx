import { useMemo, useState } from "react";
import { RelationDiagram } from "./RelationDiagram";
import { sessionRelations } from "./sessionRelations";
import type { SessionDetail } from "./api";
import { formatInteger, formatMs, formatUSD } from "../../shared/format";

type Agent = SessionDetail["subagents"][number];

const timestamp = new Intl.DateTimeFormat("en-US", { dateStyle: "medium", timeStyle: "medium" });

const agentName = (agent: Agent) => agent.title || agent.agentType || "Subagent";

/** Walk the recorded tree without assuming that every agent has a visible parent. */
function graphRows(root: string, agents: Agent[]) {
  const byStart = [...agents].sort((a, b) => (a.startTs || Infinity) - (b.startTs || Infinity));
  const children = new Map<string, Agent[]>();
  for (const agent of byStart) {
    if (!agent.parentId) continue;
    const siblings = children.get(agent.parentId);
    if (siblings) siblings.push(agent);
    else children.set(agent.parentId, [agent]);
  }

  const pending = (children.get(root) ?? []).map((agent) => ({ agent, depth: 1 })).reverse();
  const seen = new Set<Agent>();
  const rows: { agent: Agent; depth: number }[] = [];
  while (pending.length > 0) {
    const row = pending.pop()!;
    if (seen.has(row.agent)) continue;
    seen.add(row.agent);
    rows.push(row);
    const descendants = row.agent.id ? (children.get(row.agent.id) ?? []) : [];
    for (let index = descendants.length - 1; index >= 0; index--) {
      pending.push({ agent: descendants[index]!, depth: row.depth + 1 });
    }
  }
  return { rows, unlinked: byStart.filter((agent) => !seen.has(agent)) };
}

interface TimeRange {
  start: number;
  end: number;
}

function ActivityBar({ range, start, end }: { range: TimeRange; start?: number; end?: number }) {
  if (!start || !end || end < start || start < range.start || end > range.end) {
    return <span className="self-center text-xs text-muted">Interval unavailable</span>;
  }
  const total = Math.max(1, range.end - range.start);
  return (
    <span className="relative block h-5 self-center rounded-sm bg-bg" aria-hidden="true">
      <span className="absolute top-1 h-3 rounded-sm bg-accent"
        style={{ insetInlineStart: `${((start - range.start) / total) * 100}%`, width: `${((end - start) / total) * 100}%`, minWidth: "2px" }} />
    </span>
  );
}

function AgentDetails({ agent, parent }: { agent: Agent; parent?: string }) {
  const { usage, startTs, endTs } = agent;
  const span = startTs && endTs && endTs >= startTs ? endTs - startTs : undefined;
  const cost = usage.unpriced === usage.requests
    ? "Unavailable"
    : formatUSD(usage.cost) + (usage.unpriced ? " known" : "");
  return (
    <>
      <h3 className="font-semibold">{agentName(agent)}</h3>
      <p className="mt-1 text-sm text-muted">{parent ? `Parent: ${parent}` : "Parent unavailable in this view"}</p>
      <dl className="mt-3 grid grid-cols-[auto_minmax(0,1fr)] gap-x-3 gap-y-1 text-sm">
        <dt>Agent</dt><dd>{agent.agentType || "Unavailable"}</dd>
        <dt>Requests</dt><dd>{formatInteger(usage.requests)} ({formatInteger(usage.failed)} failed)</dd>
        <dt>Cost</dt><dd>{cost}</dd>
        <dt>Models</dt><dd>{agent.models?.join(", ") || "Unavailable"}</dd>
        <dt>First activity</dt><dd>{startTs ? timestamp.format(startTs) : "Unavailable"}</dd>
        <dt>Last activity</dt><dd>{endTs ? timestamp.format(endTs) : "Unavailable"}</dd>
        <dt>Recorded span</dt><dd>{span === undefined ? "Unavailable" : span === 0 ? "0 ms" : formatMs(span)}</dd>
      </dl>
    </>
  );
}

export function SessionGraph({ detail }: { detail: SessionDetail }) {
  const { session, subagents, tools, forkedFrom, prompts } = detail;
  const title = session.title || "Untitled session";
  const names = new Map(subagents.flatMap((agent) => agent.id ? [[agent.id, agentName(agent)] as const] : []));
  names.set(session.id, title);
  const parentOf = (agent: Agent) => agent.parentId ? names.get(agent.parentId) : undefined;
  const { rows, unlinked } = graphRows(session.id, subagents);
  const [view, setView] = useState<"Relations" | "Timeline">("Relations");
  const [selected, select] = useState<Agent | "fork">();
  const forkSelected = selected === "fork" && !!forkedFrom;
  const current = selected && selected !== "fork" && subagents.includes(selected) ? selected : undefined;
  const layout = useMemo(() => sessionRelations(detail), [detail]);
  const selectedIndex = forkSelected ? subagents.length + 1 : current ? subagents.indexOf(current) + 1 : 0;
  const range = session.startTs && session.endTs >= session.startTs
    ? { start: session.startTs, end: session.endTs }
    : undefined;
  const elapsed = range ? range.end - range.start : 0;
  const timeline = view === "Timeline";
  const lane = range && timeline ? "grid-cols-[minmax(0,3fr)_minmax(0,2fr)]" : "grid-cols-1";
  const rowClass = `grid min-h-11 w-full items-center gap-2 rounded-md border border-transparent px-2 py-1 text-left text-sm hover:bg-bg aria-pressed:border-accent aria-pressed:bg-bg ${lane}`;
  const renderAgent = (agent: Agent, depth: number, index: number) => {
    const parent = parentOf(agent);
    return (
      <li key={agent.id ?? index}>
        <button type="button" className={rowClass} aria-pressed={current === agent} onClick={() => select(agent)}>
          <span className="min-w-0 [overflow-wrap:anywhere]" style={{ paddingInlineStart: `${Math.min(Math.max(0, depth - 1), 3) * 0.55}rem` }}>
            <span className="block font-medium">{agentName(agent)}</span>
            <span className="block text-xs text-muted">{parent ? `Parent: ${parent}` : "Parent unavailable in this view"}</span>
          </span>
          {range && timeline && <ActivityBar range={range} start={agent.startTs} end={agent.endTs} />}
        </button>
      </li>
    );
  };
  return (
    <section aria-label="Session activity graph" className="grid gap-4 rounded-lg border border-rule bg-surface p-3 xl:grid-cols-[minmax(0,1.6fr)_minmax(0,1fr)] xl:p-4">
      <div className="flex flex-wrap gap-2 xl:col-span-2" role="group" aria-label="Graph views">
        {(["Relations", "Timeline"] as const).map((name) => (
          <button key={name} type="button" aria-pressed={view === name} onClick={() => setView(name)}
            className="min-h-11 rounded-md border border-border px-3 text-sm aria-pressed:border-accent aria-pressed:bg-bg">
            {name}
          </button>
        ))}
      </div>
      <div className="min-w-0 space-y-2">
        {!timeline && <RelationDiagram source={layout.source} names={layout.names} selectedIndex={selectedIndex}
          onSelect={(index) => select(index > subagents.length && forkedFrom ? "fork" : subagents[index - 1])} />}
        <div className={`grid items-baseline gap-2 border border-transparent px-2 ${lane}`}>
          <h3 className="font-semibold">{timeline ? "Activity timeline" : "Recorded agents"}</h3>
          {range && timeline && (
            <p className="flex justify-between gap-1 text-xs tabular-nums text-muted">
              <span>0 s</span>{elapsed > 0 && <span>{formatMs(elapsed)}</span>}
            </p>
          )}
        </div>
        {timeline && !range && <p className="px-2 text-sm text-muted">Session time range unavailable.</p>}
        <button type="button" className={rowClass} aria-pressed={!current && !forkSelected} onClick={() => select(undefined)}>
          <span className="min-w-0 [overflow-wrap:anywhere]">
            <span className="block font-semibold">{title}</span>
            <span className="block text-xs text-muted">{timeline ? "Session range" : "Selected session"} · {session.harness}</span>
          </span>
          {range && timeline && <ActivityBar range={range} start={range.start} end={range.end} />}
        </button>
        {forkedFrom && !timeline && (
          <button type="button" className={rowClass} aria-pressed={forkSelected} onClick={() => select("fork")}>
            Fork source
          </button>
        )}
        {rows.length > 0 && (
          <ul aria-label="Recorded parent links" className="space-y-1">
            {rows.map(({ agent, depth }, index) => renderAgent(agent, depth, index))}
          </ul>
        )}
        {unlinked.length > 0 && (
          <section className="space-y-1 border-t border-rule pt-2">
            <h4 className="px-2 text-sm font-semibold">Agents outside the recorded tree</h4>
            <ul>{unlinked.map((agent, index) => renderAgent(agent, 0, index))}</ul>
          </section>
        )}
        {subagents.length === 0 && <p className="px-2 text-sm text-muted">No subagent links were recorded for this session.</p>}
      </div>

      <section aria-label="Selected activity" className="min-w-0 rounded-md border border-rule bg-bg p-3 [overflow-wrap:anywhere]">
        {forkSelected ? <>
          <h3 className="font-semibold">Fork source</h3>
          <p className="mt-1 text-sm"><code>{forkedFrom}</code></p>
        </> : current ? <AgentDetails agent={current} parent={parentOf(current)} /> : (
          <>
            <h3 className="font-semibold">Session overview</h3>
            <p className="mt-1 text-sm text-muted">{formatInteger(session.usage.requests)} requests across the session and its subagents</p>
            {forkedFrom && <p className="mt-2 text-sm text-muted">Forked from <code>{forkedFrom}</code></p>}
            <dl className="mt-3 grid grid-cols-[auto_minmax(0,1fr)] gap-x-3 gap-y-1 text-sm">
              <dt>Models</dt><dd>{session.models.map(({ model }) => model).join(", ") || "Unavailable"}</dd>
              <dt>First activity</dt><dd>{session.startTs ? timestamp.format(session.startTs) : "Unavailable"}</dd>
              <dt>Last activity</dt><dd>{session.endTs ? timestamp.format(session.endTs) : "Unavailable"}</dd>
            </dl>
            {tools.length > 0 && (
              <details className="mt-2 text-sm">
                <summary className="min-h-11 cursor-pointer py-3">Tools ({formatInteger(tools.length)})</summary>
                <ul className="space-y-1 pl-4">
                  {tools.map(({ name, server, calls }) => <li key={JSON.stringify([server, name])}>{server ? `${server} / ${name}` : name} · {formatInteger(calls)} calls</li>)}
                </ul>
              </details>
            )}
            {prompts.length > 0 && (
              <details className="text-sm">
                <summary className="min-h-11 cursor-pointer py-3">Prompt activity</summary>
                <ol className="space-y-1 pl-4">
                  {prompts.map((prompt, index) => <li key={index}>{prompt.ts ? timestamp.format(prompt.ts) : "Time unavailable"} · {formatInteger(prompt.chars)} characters</li>)}
                </ol>
              </details>
            )}
          </>
        )}
      </section>
    </section>
  );
}
