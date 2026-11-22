import { useQuery } from "@tanstack/react-query";
import { useEffect, useRef } from "react";
import { getJSON, shouldRetry } from "../../shared/api";
import { describe, Failure } from "../../shared/Failure";
import { formatInteger, formatUSD } from "../../shared/format";
import type { Filters, Tool } from "../report";
import type { Session, SessionList } from "../sessions/api";
import { relationLabel, type Relation } from "./navigation";

type RelatedSession = Session & { relatedTool?: Tool };
type RelatedList = Omit<SessionList, "sessions"> & {
  sessions: RelatedSession[];
  toolTotals?: { calls: number; errors: number };
};

export function SessionRows({
  sessions, open, idPrefix, calls = false,
}: { sessions: RelatedSession[]; open: (id: string) => void; idPrefix: string; calls?: boolean }) {
  return (
    <ul className="dashboard-rows">
      {sessions.map((row) => (
        <li key={row.id}>
          <button type="button" className="dashboard-row" id={`${idPrefix}-${row.id}`} onClick={() => open(row.id)}>
            <span className="min-w-0">
              <strong>{row.title || "Untitled session"}</strong>
              <small>{row.harness}{row.folder && ` · ${row.folder}`}</small>
            </span>
            <span className="tabular-nums text-right">
              {calls
                ? `${formatInteger(row.relatedTool?.calls ?? 0)} calls`
                : row.usage.requests > 0 && row.usage.unpriced === row.usage.requests
                  ? "Cost unavailable"
                  : formatUSD(row.usage.cost)}
              <span className="ml-2" aria-hidden="true">›</span>
            </span>
          </button>
        </li>
      ))}
    </ul>
  );
}

export function RelatedSessions({
  selection, filters, project, close, open,
}: { selection: Relation; filters: Filters; project: string; close: () => void; open: (id: string) => void }) {
  const params = {
    kind: selection.kind, value: selection.value, server: selection.server ?? "",
    itemHarness: selection.itemHarness ?? "", itemScope: selection.itemScope ?? "", itemOrigin: selection.itemOrigin ?? "",
    since: filters.since, harness: filters.harness, project: filters.scope, limit: "50",
  };
  const query = useQuery({
    queryKey: ["sessions", "related", params, project],
    queryFn: ({ signal }) => getJSON<RelatedList>("/api/related-sessions", params, signal),
    retry: shouldRetry,
  });
  const heading = useRef<HTMLHeadingElement>(null);
  const label = relationLabel(selection);
  useEffect(() => heading.current?.focus({ preventScroll: false }), [selection.kind, label]);
  const calls = selection.kind === "tool" || selection.kind === "mcp";
  const data = query.data;
  const cost = data && data.total.requests > 0 && data.total.unpriced === data.total.requests ? "Cost unavailable" : formatUSD(data?.total.cost ?? 0);
  return (
    <section
      className="dashboard-panel dashboard-inspector"
      aria-labelledby="related-title"
      onKeyDown={(event) => {
        if (event.key === "Escape" && !event.defaultPrevented) {
          event.preventDefault();
          close();
        }
      }}
    >
      <div className="dashboard-panel-heading">
        <h2 id="related-title" ref={heading} tabIndex={-1}>{label}</h2>
        <button type="button" className="dashboard-link" onClick={close}>Clear selection</button>
      </div>
      {query.isPending && <p role="status">Loading contributing sessions…</p>}
      {query.isError && <Failure title="Contributing sessions could not be loaded." message={describe(query.error)} retry={() => void query.refetch()} />}
      {data && (
        <>
          <p className="dashboard-inspector-total tabular-nums">
            {calls ? `${formatInteger(data.toolTotals?.calls ?? 0)} calls` : cost}
          </p>
          <p className="text-sm text-muted">
            {calls ? `${formatInteger(data.toolTotals?.errors ?? 0)} errors` : `${formatInteger(data.total.requests)} requests`}
            {" · "}{formatInteger(data.count)} {data.count === 1 ? "session" : "sessions"}
          </p>
          {data.total.unpriced > 0 && <p className="text-sm text-muted">Price unavailable for {formatInteger(data.total.unpriced)} requests.</p>}
          {data.sessions.length > 0 ? <SessionRows sessions={data.sessions} idPrefix="related-session" calls={calls} open={open} /> : (
            <p className="text-sm text-muted">No sessions match this selection.</p>
          )}
          {data.count > data.sessions.length && <p className="text-sm text-muted">Showing the first {data.sessions.length} sessions.</p>}
          <p className="text-xs text-muted">
            {calls ? "These calls were observed in the selected activity. Session details include model usage." : "These totals cover the selected activity. Session details show the full run."}
          </p>
        </>
      )}
    </section>
  );
}
