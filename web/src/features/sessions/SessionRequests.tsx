import { useState } from "react";
import { DataTable, type Column } from "../../shared/DataTable";
import { describe, Failure } from "../../shared/Failure";
import { formatDateTime, formatInteger, formatMs, formatUSD } from "../../shared/format";
import type { Filters } from "../report";
import { requestPageSize, useSessionRequests, type SessionRequest } from "./api";

type Row = SessionRequest & { position: number };

const columns: Column<Row>[] = [
  { id: "time", header: "Time", value: (r) => r.ts, render: (r) => formatDateTime(r.ts) },
  { id: "model", header: "Model", value: (r) => r.model, truncate: true },
  { id: "provider", header: "Provider", value: (r) => r.provider || "Unknown" },
  { id: "agent", header: "Agent", value: (r) => r.agentType },
  { id: "input", header: "Tokens in", value: (r) => r.input + r.cacheRead + r.cacheWrite, render: (r) => formatInteger(r.input + r.cacheRead + r.cacheWrite), numeric: true },
  { id: "output", header: "Tokens out", value: (r) => r.output, render: (r) => formatInteger(r.output), numeric: true },
  { id: "cost", header: "Estimated cost", value: (r) => r.priced ? r.cost : -1, render: (r) => r.priced ? formatUSD(r.cost) : "Unavailable", numeric: true },
  { id: "duration", header: "Duration", value: (r) => r.durationMs, render: (r) => formatMs(r.durationMs), numeric: true },
  { id: "ttft", header: "First token", value: (r) => r.ttftMs, render: (r) => formatMs(r.ttftMs), numeric: true },
  {
    id: "result", header: "Result", value: (r) => r.failed ? "Failed" : r.stopReason || "Unknown",
    render: (r) => r.error ? <details className="max-w-xs whitespace-normal break-words">
      <summary className="cursor-pointer text-accent">Failed</summary>
      <p className="whitespace-pre-wrap">{r.error}</p>
    </details> : r.failed ? "Failed" : r.stopReason || "Unknown",
  },
];

export function SessionRequests({ id, filters, project }: { id: string; filters: Filters; project: string }) {
  const [failed, setFailed] = useState(false);
  const [offset, setOffset] = useState(0);
  const page = useSessionRequests(id, filters, project, failed, offset);
  return <>
    <div className="mb-3 flex flex-wrap items-center justify-between gap-2">
      <label className="flex min-h-9 items-center gap-2 text-sm">
        <input type="checkbox" checked={failed} onChange={(event) => { setFailed(event.target.checked); setOffset(0); }} />
        Failed only
      </label>
      <div className="flex items-center gap-3 text-sm">
        <button type="button" className="min-h-9 px-2 disabled:opacity-50" disabled={offset === 0} onClick={() => setOffset(offset - requestPageSize)}>Newer</button>
        <span aria-live="polite">Page {offset / requestPageSize + 1}</span>
        <button type="button" className="min-h-9 px-2 disabled:opacity-50" disabled={!page.data?.hasMore} onClick={() => setOffset(offset + requestPageSize)}>Older</button>
      </div>
    </div>
    {page.isPending && <p role="status">Loading requests…</p>}
    {page.isError && <Failure title="The requests could not be loaded." message={describe(page.error)} retry={() => void page.refetch()} />}
    {page.isSuccess && <DataTable
      caption="Session requests"
      columns={columns}
      rows={page.data.requests.map((row, i) => ({ ...row, position: offset + i }))}
      rowKey={(row) => String(row.position)}
      initialSort="time"
      initialLimit={requestPageSize}
    />}
  </>;
}
