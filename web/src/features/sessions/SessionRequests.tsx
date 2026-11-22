import { useId, useState } from "react";
import { DataTable, type Column } from "../../shared/DataTable";
import { describe, Failure } from "../../shared/Failure";
import { formatDateTime, formatInteger, formatMs, formatUSD } from "../../shared/format";
import type { Filters } from "../report";
import { requestPageSize, useErrorSignatures, useSessionRequests, type RequestModel, type SessionRequest } from "./api";

type Row = SessionRequest & { position: number };

const columns: Column<Row>[] = [
  { id: "time", header: "Time", value: (r) => r.ts, render: (r) => formatDateTime(r.ts) },
  { id: "model", header: "Model", value: (r) => r.model, truncate: true },
  { id: "provider", header: "Provider", value: (r) => r.provider || "Unknown" },
  { id: "purpose", header: "Purpose", value: (r) => r.purpose || "Conversation" },
  { id: "agent", header: "Agent", value: (r) => r.agentType },
  { id: "input", header: "Tokens in", value: (r) => r.input + r.cacheRead + r.cacheWrite, render: (r) => formatInteger(r.input + r.cacheRead + r.cacheWrite), numeric: true },
  { id: "output", header: "Tokens out", value: (r) => r.output, render: (r) => formatInteger(r.output), numeric: true },
  { id: "cost", header: "Estimated cost", value: (r) => r.priced ? r.cost : -1, render: (r) => r.priced ? formatUSD(r.cost) : "Unavailable", numeric: true },
  { id: "duration", header: "Duration", value: (r) => r.durationMs, render: (r) => formatMs(r.durationMs), numeric: true },
  { id: "premium", header: "Premium units", value: (r) => r.premiumUnits || 0, render: (r) => r.premiumUnits > 0 ? r.premiumUnits.toLocaleString("en-US", { maximumFractionDigits: 2 }) : "n/a", numeric: true },
  { id: "tier", header: "Service tier", value: (r) => r.serviceTier || "Unknown" },
  { id: "ttft", header: "First token", value: (r) => r.ttftMs, render: (r) => formatMs(r.ttftMs), numeric: true },
  {
    id: "result", header: "Result", value: (r) => r.failed ? "Failed" : r.stopReason || "Unknown",
    render: (r) => r.error ? <details className="max-w-xs whitespace-normal break-words">
      <summary className="cursor-pointer text-accent">Failed</summary>
      <p className="whitespace-pre-wrap">{r.error}</p>
    </details> : r.failed ? "Failed" : r.stopReason || "Unknown",
  },
];

export function SessionRequests({ id, filters, project, openSession, errorsOnly = false, model, clearModel }: {
  id?: string;
  errorsOnly?: boolean;
  model?: RequestModel;
  clearModel?: () => void;
  filters: Filters;
  project: string;
  openSession?: (id: string) => void;
}) {
  const signatureID = useId();
  const [failed, setFailed] = useState(false);
  const [offset, setOffset] = useState(0);
  const [signature, setSignature] = useState("");
  const [input, setInput] = useState("");
  const [search, setSearch] = useState("");
  const signatures = useErrorSignatures(filters, project, errorsOnly);
  const page = useSessionRequests(id, filters, project, errorsOnly || failed, offset, signature, search, model);
  const shownColumns: Column<Row>[] = openSession ? [{
    id: "session", header: "Session", value: (r) => r.folder || r.sessionId, truncate: true,
    render: (r) => <button type="button" className="dashboard-link" onClick={() => openSession(r.sessionId)}>{r.folder || r.sessionId}</button>,
  }, ...columns] : columns;
  return <>
    {model && <p className="mb-3 flex flex-wrap items-center gap-2 text-sm">
      <strong>{model.name}</strong><span>{model.provider || "Unknown provider"}</span>
      {clearModel && <button type="button" className="dashboard-link" onClick={clearModel}>Clear model filter</button>}
    </p>}
    <form className="mb-3 flex flex-wrap items-end gap-2" onSubmit={(event) => { event.preventDefault(); setSearch(input.trim()); setOffset(0); }}>
      <label className="flex flex-col gap-1 text-sm">
        Search requests
        <input type="search" className="min-h-9 rounded border border-rule bg-bg px-2"
          maxLength={200} value={input} onChange={(event) => setInput(event.target.value)} placeholder="Model, provider or folder" />
      </label>
      <button type="submit" className="min-h-9 rounded border border-rule px-3 text-sm hover:bg-surface">Search</button>
    </form>
    <div className="mb-3 flex flex-wrap items-center justify-between gap-2">
      {errorsOnly ? (
        signatures.data && signatures.data.length > 0 && <div className="flex max-w-full flex-col gap-1 text-sm">
          <label htmlFor={signatureID}>Error signature</label>
          <select id={signatureID} className="min-h-9 max-w-full rounded border border-rule bg-surface px-2"
            value={signature}
            onChange={(event) => { setSignature(event.target.value); setOffset(0); }}>
            <option value="">All signatures</option>
            {signatures.data.map((group) => <option key={group.key} value={group.key}>
              {formatInteger(group.count)} · {group.message || "Unknown error"}
            </option>)}
          </select>
        </div>
      ) : <label className="flex min-h-9 items-center gap-2 text-sm">
        <input type="checkbox" checked={failed} onChange={(event) => { setFailed(event.target.checked); setOffset(0); }} />
        Failed only
      </label>}
      <div className="flex items-center gap-3 text-sm">
        <button type="button" className="min-h-9 px-2 disabled:opacity-50" disabled={offset === 0} onClick={() => setOffset(offset - requestPageSize)}>Newer</button>
        <span aria-live="polite">Page {offset / requestPageSize + 1}</span>
        <button type="button" className="min-h-9 px-2 disabled:opacity-50" disabled={!page.data?.hasMore} onClick={() => setOffset(offset + requestPageSize)}>Older</button>
      </div>
    </div>
    {signatures.isError && <Failure title="Error signatures could not be loaded." message={describe(signatures.error)} retry={() => void signatures.refetch()} />}
    {page.isPending && <p role="status">Loading requests…</p>}
    {page.isError && <Failure title="The requests could not be loaded." message={describe(page.error)} retry={() => void page.refetch()} />}
    {page.isSuccess && <DataTable
      caption={id ? "Session requests" : errorsOnly ? "Failed requests" : "Model requests"}
      columns={shownColumns}
      rows={page.data.requests.map((row, i) => ({ ...row, position: offset + i }))}
      rowKey={(row) => String(row.position)}
      initialSort="time"
      initialLimit={requestPageSize}
    />}
  </>;
}
