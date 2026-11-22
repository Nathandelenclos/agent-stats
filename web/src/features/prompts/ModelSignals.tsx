import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { getJSON, shouldRetry } from "../../shared/api";
import { DataTable, type Column } from "../../shared/DataTable";
import { describe, Failure } from "../../shared/Failure";
import { formatInteger, formatPercent } from "../../shared/format";
import type { Filters } from "../report";

interface ModelSignalsRow {
  provider: string;
  model: string;
  prompts: number;
  flagged: number;
}

const share = (row: ModelSignalsRow) => row.flagged / row.prompts;
const columns: Column<ModelSignalsRow>[] = [
  { id: "model", header: "Previous model", value: (r) => r.model || "Unknown", truncate: true },
  { id: "provider", header: "Provider", value: (r) => r.provider || "Unknown" },
  { id: "prompts", header: "Prompts", value: (r) => r.prompts, render: (r) => formatInteger(r.prompts), numeric: true },
  { id: "flagged", header: "Flagged prompts", value: (r) => r.flagged, render: (r) => formatInteger(r.flagged), numeric: true },
  { id: "share", header: "Flagged share", value: share, render: (r) => formatPercent(share(r)), numeric: true },
];

export function ModelSignals({ filters, project }: { filters: Filters; project: string }) {
  const [open, setOpen] = useState(false);
  const params = { since: filters.since, harness: filters.harness, project: filters.scope };
  const query = useQuery({
    queryKey: ["prompts", "model-signals", params, project],
    queryFn: ({ signal }) => getJSON<ModelSignalsRow[]>("/api/prompts/model-signals", params, signal),
    enabled: open,
    retry: shouldRetry,
  });
  return <details className="dashboard-panel" onToggle={(event) => setOpen(event.currentTarget.open)}>
    <summary className="cursor-pointer font-semibold">Prompt signals by model</summary>
    <p className="my-2 text-sm text-muted">Local text signals grouped by the most recent model request before each prompt. A flagged prompt can have several causes.</p>
    {open && query.isPending && <p role="status">Comparing prompt signals…</p>}
    {query.isError && <Failure title="Could not compare prompt signals." message={describe(query.error)} retry={() => void query.refetch()} />}
    {query.isSuccess && <DataTable caption="Prompt signals by model" columns={columns} rows={query.data}
      rowKey={(row) => JSON.stringify([row.provider, row.model])} initialSort="flagged" />}
  </details>;
}
