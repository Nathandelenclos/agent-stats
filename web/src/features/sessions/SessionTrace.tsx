import { useState } from "react";
import { describe, Failure } from "../../shared/Failure";
import { formatDateTime, formatMs } from "../../shared/format";
import type { Filters } from "../report";
import { tracePageSize, useSessionTrace, type TraceEvent } from "./api";

const colors: Record<TraceEvent["kind"], string> = {
  prompt: "bg-muted",
  model: "bg-accent",
  tool: "bg-[var(--mark-2)]",
};

function Timeline({ events }: { events: TraceEvent[] }) {
  const timed = events.filter((event) => event.ts > 0);
  if (timed.length === 0) return null;
  const first = timed[0]!.ts;
  const last = timed[timed.length - 1]!.ts;
  const tracks = new Map<string, TraceEvent[]>();
  for (const event of timed) {
    const track = tracks.get(event.agentId);
    if (track) track.push(event);
    else tracks.set(event.agentId, [event]);
  }
  return <figure className="mb-4 space-y-2 rounded-lg border border-rule p-3" aria-hidden="true">
    <div className="flex justify-between gap-2 text-xs text-muted">
      <span>{formatDateTime(first)}</span>
      <span>{formatDateTime(last)}</span>
    </div>
    {[...tracks].map(([id, track]) => <div key={id} className="grid grid-cols-[minmax(0,7rem)_1fr] items-center gap-3 text-xs">
      <span title={id} className="truncate">{track[0]!.agentType}</span>
      <div className="relative h-5 border-y border-rule">
        {track.map((event, i) => <span key={i} title={event.label}
          className={`absolute top-1.5 h-2 w-2 -translate-x-1/2 rounded-full ${event.failed ? "bg-danger" : colors[event.kind]}`}
          style={{ left: `${last === first ? 50 : (event.ts - first) * 100 / (last - first)}%` }} />)}
      </div>
    </div>)}
  </figure>;
}

export function SessionTrace({ id, filters, project }: { id: string; filters: Filters; project: string }) {
  const [offset, setOffset] = useState(0);
  const trace = useSessionTrace(id, filters, project, offset);
  if (trace.isPending) return <p role="status">Loading trace…</p>;
  if (trace.isError) return <Failure title="The trace could not be loaded." message={describe(trace.error)} retry={() => void trace.refetch()} />;
  const { events, hasMore } = trace.data;
  return <>
    <p className="mb-3 text-sm text-muted">Indexed prompts, model usage and tool calls, ordered by their recorded time.</p>
    <Timeline events={events} />
    {events.length === 0 ? <p className="text-sm text-muted">No events to display.</p> : (
      <ol className="divide-y divide-rule rounded-lg border border-rule">
        {events.map((event, i) => <li key={offset + i} className="flex flex-wrap items-baseline gap-x-3 gap-y-1 px-3 py-2 text-sm">
          <span className="w-36 text-xs tabular-nums text-muted">{event.ts ? formatDateTime(event.ts) : "Time unavailable"}</span>
          <span className="text-xs capitalize text-muted">{event.kind}</span>
          <strong className="break-all font-medium">{event.label}</strong>
          <span title={event.agentId} className="text-xs text-muted">{event.agentType}</span>
          {event.durationMs > 0 && <span className="tabular-nums">{formatMs(event.durationMs)}</span>}
          {event.failed && <span className="text-danger">Failed</span>}
        </li>)}
      </ol>
    )}
    <div className="mt-3 flex items-center gap-3 text-sm">
      <button type="button" className="min-h-9 px-2 disabled:opacity-50" disabled={offset === 0} onClick={() => setOffset(offset - tracePageSize)}>Previous events</button>
      <span aria-live="polite">Page {offset / tracePageSize + 1}</span>
      <button type="button" className="min-h-9 px-2 disabled:opacity-50" disabled={!hasMore} onClick={() => setOffset(offset + tracePageSize)}>Next events</button>
    </div>
  </>;
}
