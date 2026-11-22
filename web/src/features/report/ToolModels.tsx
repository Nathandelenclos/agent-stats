import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { getJSON, shouldRetry } from "../../shared/api";
import { DataTable, type Column } from "../../shared/DataTable";
import { describe, Failure } from "../../shared/Failure";
import { formatInteger, formatUSD } from "../../shared/format";
import type { Filters } from "./types";

interface AttributedCall {
  tool: string;
  server: string;
  model: string;
  provider: string;
  calls: number;
  errors: number;
  tokens: number;
  cost: number;
  pricedCalls: number;
}

const columns: Column<AttributedCall>[] = [
  { id: "tool", header: "Tool", value: (r) => r.tool, truncate: true },
  { id: "server", header: "MCP server", value: (r) => r.server },
  { id: "model", header: "Model", value: (r) => r.model, truncate: true },
  { id: "provider", header: "Provider", value: (r) => r.provider },
  { id: "calls", header: "Matched calls", value: (r) => r.calls, render: (r) => formatInteger(r.calls), numeric: true },
  { id: "errors", header: "Errors", value: (r) => r.errors, render: (r) => formatInteger(r.errors), numeric: true },
  { id: "tokens", header: "Attributed tokens", value: (r) => r.tokens, render: (r) => formatInteger(r.tokens), numeric: true },
  {
    id: "cost", header: "Attributed cost", value: (r) => r.cost, numeric: true,
    render: (r) => r.pricedCalls ? `${formatUSD(r.cost)} (${r.pricedCalls}/${r.calls})` : "Unavailable",
  },
];

export function ToolModels({ filters, project }: { filters: Filters; project: string }) {
  const [open, setOpen] = useState(false);
  const params = { since: filters.since, harness: filters.harness, project: filters.scope };
  const query = useQuery({
    queryKey: ["report", "tool-models", params, project],
    queryFn: ({ signal }) => getJSON<AttributedCall[]>("/api/tool-models", params, signal),
    enabled: open,
    retry: shouldRetry,
  });
  return <details className="dashboard-panel mt-3" onToggle={(event) => setOpen(event.currentTarget.open)}>
    <summary className="cursor-pointer font-semibold">Tool usage by model</summary>
    <p className="my-2 text-sm text-muted">For matched Pi and oh-my-pi calls, the invoking request’s tokens and cost are divided evenly across its tools.</p>
    {open && query.isPending && <p role="status">Attributing tool usage…</p>}
    {query.isError && <Failure title="Could not attribute tool usage." message={describe(query.error)} retry={() => void query.refetch()} />}
    {query.isSuccess && <DataTable caption="Tool usage by model" columns={columns} rows={query.data}
      rowKey={(row) => JSON.stringify([row.server, row.tool, row.provider, row.model])} initialSort="calls" />}
  </details>;
}
