import { useQuery } from "@tanstack/react-query";
import { getJSON, shouldRetry } from "../../shared/api";
import { describe, Failure } from "../../shared/Failure";
import { formatDateTime, formatInteger, formatMs, formatPercent, formatUSD } from "../../shared/format";
import { PlotSwatch } from "../report/ActivityBreakdown";
import type { Filters, Usage } from "../report/types";
import type { SessionRequest } from "../sessions/api";

export const tokenColors = {
  input: "var(--plot-blue)", cacheRead: "var(--plot-cyan)", cacheWrite: "var(--plot-amber)", output: "var(--plot-magenta)",
} as const;

export const tokenLabels = { input: "Uncached input", cacheRead: "Cache read", cacheWrite: "Cache write", output: "Output" };

export function TokenMix({ usage }: { usage: Usage }) {
  const parts = (Object.keys(tokenColors) as Array<keyof typeof tokenColors>).map((key) => ({
    key, label: tokenLabels[key], value: usage[key], color: tokenColors[key],
  }));
  const total = parts.reduce((sum, part) => sum + part.value, 0);
  return (
    <section className="dashboard-panel" aria-labelledby="token-mix">
      <div className="dashboard-panel-heading"><h2 id="token-mix">Token mix</h2></div>
      {total === 0 ? <p className="text-sm text-muted">No tokens in this period.</p> : (
        <>
          <div aria-hidden="true" className="flex h-3 overflow-hidden rounded-full bg-rule">
            {parts.filter((part) => part.value > 0).map((part) => <span key={part.key}
              style={{ width: `${part.value / total * 100}%`, backgroundColor: part.color }} />)}
          </div>
          <ul className="mt-3 grid gap-x-4 gap-y-1.5 text-sm">
            {parts.map((part) => <li key={part.key} className="flex justify-between gap-2">
              <span className="inline-flex min-w-0 items-center gap-2"><PlotSwatch color={part.color} />{part.label}</span>
              <span className="text-right tabular-nums">{formatInteger(part.value)} <small className="text-muted">{formatPercent(part.value / total)}</small></span>
            </li>)}
          </ul>
        </>
      )}
    </section>
  );
}

export function RecentRequests({ filters, project, open, browse }: {
  filters: Filters;
  project: string;
  open: (id: string) => void;
  browse: () => void;
}) {
  const params = { since: filters.since, harness: filters.harness, project: filters.scope, limit: "5" };
  const query = useQuery({
    queryKey: ["sessions", "requests", "overview", params, project],
    queryFn: ({ signal }) => getJSON<{ requests: SessionRequest[] }>("/api/requests", params, signal),
    retry: shouldRetry,
  });
  return (
    <section className="dashboard-panel dashboard-latest-requests" aria-labelledby="overview-requests">
      <div className="dashboard-panel-heading">
        <h2 id="overview-requests">Latest requests</h2>
        <button type="button" className="dashboard-link" onClick={browse}>All requests</button>
      </div>
      {query.isPending && <p role="status" className="text-sm text-muted">Loading requests…</p>}
      {query.isError && <Failure title="Recent requests could not be loaded." message={describe(query.error)} retry={() => void query.refetch()} />}
      {query.data && (query.data.requests.length === 0 ? <p className="text-sm text-muted">No requests in this period.</p> : (
        <div className="overflow-x-auto" tabIndex={0} role="region" aria-labelledby="overview-requests">
          <table className="dashboard-daily-table">
            <caption className="sr-only">Latest model requests</caption>
            <thead><tr>
              <th scope="col">Model</th><th scope="col">Time</th><th scope="col">Tokens</th>
              <th scope="col">Estimated cost</th><th scope="col">Duration</th><th scope="col">Status</th>
            </tr></thead>
            <tbody>{query.data.requests.map((request, index) => (
              <tr key={`${request.sessionId}-${request.ts}-${index}`}>
                <th scope="row">
                  <button type="button" className="dashboard-link" onClick={() => open(request.sessionId)}>{request.model || "Unknown model"}</button>
                  {request.provider && <small className="block text-muted">{request.provider}</small>}
                </th>
                <td>{formatDateTime(request.ts)}</td>
                <td>{formatInteger(request.input + request.cacheRead + request.cacheWrite + request.output)}</td>
                <td>{request.priced ? formatUSD(request.cost) : "Unavailable"}</td>
                <td>{formatMs(request.durationMs)}</td>
                <td><strong className={request.failed ? "text-danger" : "text-muted"}>{request.failed ? "Failed" : "Completed"}</strong></td>
              </tr>
            ))}</tbody>
          </table>
        </div>
      ))}
    </section>
  );
}
