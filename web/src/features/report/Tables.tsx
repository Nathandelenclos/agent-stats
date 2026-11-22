import { DataTable, type Column } from "../../shared/DataTable";
import { formatInteger, formatMs, formatPercent, formatUSD } from "../../shared/format";
import { inputTokens, type Tool, type UsageGroup } from "./types";

const usageColumns = (label: string): Column<UsageGroup>[] => [
  { id: "key", header: label, value: (r) => r.key, truncate: true },
  { id: "requests", header: "Requests", value: (r) => r.requests, render: (r) => formatInteger(r.requests), numeric: true },
  { id: "failed", header: "Failed", value: (r) => r.failed, render: (r) => formatInteger(r.failed), numeric: true },
  { id: "input", header: "Tokens in", value: inputTokens, render: (r) => formatInteger(inputTokens(r)), numeric: true },
  { id: "output", header: "Tokens out", value: (r) => r.output, render: (r) => formatInteger(r.output), numeric: true },
  { id: "cache", header: "Cache", value: (r) => r.cacheRate, render: (r) => formatPercent(r.cacheRate), numeric: true },
  { id: "cost", header: "Cost", value: (r) => r.cost, render: (r) => formatUSD(r.cost), numeric: true },
];

/** Usage grouped by harness, model, folder or agent type. */
export function UsageTable({ caption, label, groups, onSelect }: {
  caption: string; label: string; groups: UsageGroup[]; onSelect?: (key: string) => void;
}) {
  const columns = usageColumns(label);
  if (onSelect && columns[0]) {
    columns[0].render = (r) => <button type="button" className="dashboard-link" onClick={() => onSelect(r.key)}>{r.key || "(none)"}</button>;
  }
  return <DataTable caption={caption} columns={columns} rows={groups} rowKey={(r) => r.key} initialSort="cost" />;
}

const toolColumns: Column<Tool>[] = [
  { id: "name", header: "Tool", value: (r) => r.name, truncate: true },
  { id: "server", header: "MCP server", value: (r) => r.server, truncate: true },
  { id: "calls", header: "Calls", value: (r) => r.calls, render: (r) => formatInteger(r.calls), numeric: true },
  { id: "errors", header: "Errors", value: (r) => r.errors, render: (r) => formatInteger(r.errors), numeric: true },
  { id: "result", header: "Result chars", value: (r) => r.resultChars, render: (r) => formatInteger(r.resultChars), numeric: true },
  { id: "avg", header: "Avg time", value: (r) => r.avgMs, render: (r) => formatMs(r.avgMs), numeric: true },
];

/** The tools the agents called, with their MCP server. */
export function ToolsTable({ tools, onSelect }: { tools: Tool[]; onSelect?: (tool: Tool) => void }) {
  const columns = onSelect ? toolColumns.map((column) => column.id === "name"
    ? { ...column, render: (row: Tool) => <button type="button" className="dashboard-link" onClick={() => onSelect(row)}>{row.name}</button> }
    : column) : toolColumns;
  return <DataTable caption="Tools" columns={columns} rows={tools} rowKey={(r) => `${r.server}\u0000${r.name}`} initialSort="calls" />;
}
