import { formatInteger, formatPercent, formatUSD } from "../../shared/format";
import { Sparkline } from "../report/ActivityBreakdown";
import { inputTokens, type Usage, type UsageGroup } from "../report/types";

const cost = (row: Usage) => row.requests > 0 && row.unpriced === row.requests ? null : row.cost;
const totalTokens = (row: Usage) => inputTokens(row) + row.output;

export function OverviewMetrics({ total, periods }: { total: Usage; periods: UsageGroup[] }) {
  const knownCost = cost(total);
  const metrics = [
    { label: "Estimated cost", value: knownCost === null ? "Unavailable" : formatUSD(knownCost),
      note: `Price known for ${formatInteger(total.requests - total.unpriced)} of ${formatInteger(total.requests)} requests`,
      trend: periods.map(cost), color: "var(--plot-magenta)" },
    { label: "Model requests", value: formatInteger(total.requests),
      note: `${formatInteger(total.failed)} failed`,
      trend: periods.map((point) => point.requests), color: "var(--plot-cyan)" },
    { label: "Tokens", value: formatInteger(totalTokens(total)),
      note: `${formatInteger(total.output)} output`,
      trend: periods.map(totalTokens), color: "var(--plot-blue)" },
    { label: "Input from cache", value: inputTokens(total) > 0 ? formatPercent(total.cacheRate) : "Unavailable",
      note: "Share of input tokens",
      trend: periods.map((point) => inputTokens(point) > 0 ? point.cacheRate : null), color: "var(--plot-green)" },
    { label: "Error rate", value: total.requests > 0 ? formatPercent(total.failed / total.requests) : "Unavailable",
      note: `${formatInteger(total.requests - total.failed)} non-failed requests`,
      trend: periods.map((point) => point.requests > 0 ? point.failed / point.requests : null), color: "var(--plot-coral)" },
  ];
  return (
    <dl className="dashboard-metrics">
      {metrics.map(({ label, value, note, trend, color }) => (
        <div key={label} className="dashboard-panel dashboard-metric">
          <dt>{label}</dt>
          <dd className="dashboard-metric-value tabular-nums">{value}</dd>
          <dd className="dashboard-metric-note">{note}</dd>
          <dd className="mt-1"><Sparkline values={trend} color={color} /></dd>
        </div>
      ))}
    </dl>
  );
}
