import { useQuery } from "@tanstack/react-query";
import { getJSON, shouldRetry } from "../../shared/api";
import { DataTable, type Column } from "../../shared/DataTable";
import { describe, Failure } from "../../shared/Failure";
import { formatDateTime, formatInteger } from "../../shared/format";
import type { Filters, Scope } from "../report";

interface Entry {
  harness: string;
  kind: string;
  name: string;
  scope: string;
  origin?: string;
  originId?: string;
  enabled: boolean;
  uses: number | null;
  unit: string;
  lastTs: number;
}

interface CatalogData {
  scope: Scope;
  entries: Entry[];
  skipped: number;
}

export function useCatalog(filters: Filters | undefined, project: string, unused: boolean) {
  const params = filters && { since: filters.since, harness: filters.harness, project: filters.scope, unused: unused ? "true" : "" };
  return useQuery({
    queryKey: ["catalog", params, project],
    queryFn: ({ signal }) => getJSON<CatalogData>("/api/catalog", params, signal),
    enabled: params !== undefined,
    retry: shouldRetry,
  });
}

const columns: Column<Entry>[] = [
  { id: "name", header: "Name", value: (row) => row.name, truncate: true },
  { id: "kind", header: "Kind", value: (row) => row.kind },
  { id: "harness", header: "Harness", value: (row) => row.harness },
  { id: "scope", header: "Scope", value: (row) => row.origin ? row.scope + ": " + (row.originId ?? row.origin) : row.scope, truncate: true },
  { id: "enabled", header: "State", value: (row) => row.scope === "logs" ? "Seen in logs" : row.enabled ? "Enabled" : "Disabled" },
  {
    id: "uses", header: "Uses", value: (row) => row.uses ?? -1, numeric: true,
    render: (row) => row.uses === null ? "Unknown" : formatInteger(row.uses) + " " + row.unit,
  },
  {
    id: "last", header: "Last used", value: (row) => row.lastTs, numeric: true,
    render: (row) => row.lastTs > 0 ? formatDateTime(row.lastTs) : row.uses === 0 ? "Never" : "Unknown",
  },
];

export function Catalog({
  query, unused, onUnusedChange, onServer,
}: {
  query: ReturnType<typeof useCatalog>;
  unused: boolean;
  onUnusedChange: (unused: boolean) => void;
  onServer?: (entry: Entry) => void;
}) {
  return (
    <section aria-labelledby="catalog-title" className="flex min-w-0 flex-col gap-3">
      <div className="flex flex-wrap items-center justify-between gap-x-4 gap-y-1">
        <h2 id="catalog-title" className="text-lg font-semibold">Catalog</h2>
        <label className="inline-flex min-h-11 cursor-pointer items-center gap-2 text-sm">
          <input type="checkbox" checked={unused} onChange={(event) => onUnusedChange(event.target.checked)} />
          Unused only
        </label>
      </div>
      {query.isPending && <p role="status">Loading the catalog…</p>}
      {query.isError && <Failure title="The catalog could not be loaded." message={describe(query.error)} retry={() => void query.refetch()} />}
      {query.isSuccess && (
        <>
          {query.data.skipped > 0 && (
            <p role="status" className="text-sm text-muted">
              {formatInteger(query.data.skipped)} {query.data.skipped === 1 ? "source was skipped." : "sources were skipped."}
            </p>
          )}
          {query.data.entries.length === 0 ? (
            <p role="status">{unused ? "No unused entries match these filters." : "No catalog entries match these filters."}</p>
          ) : (
            <DataTable
              caption="Catalog entries"
              columns={onServer ? columns.map((column) => column.id === "name"
                ? { ...column, render: (row: Entry) => row.kind === "mcp" && (row.uses ?? 0) > 0
                  ? <button type="button" className="dashboard-link" onClick={() => onServer(row)}>{row.name}</button>
                  : row.name }
                : column) : columns}
              rows={query.data.entries}
              rowKey={(row) => JSON.stringify([row.harness, row.kind, row.name, row.scope, row.originId])}
            />
          )}
        </>
      )}
    </section>
  );
}
