import { formatInteger, formatPercent, formatUSD } from "../../shared/format";
import type { CostBreakdown as Breakdown } from "./types";

export function CostBreakdown({ cost, requests }: { cost: Breakdown; requests: number }) {
  const parts = [
    ["Uncached input", cost.input],
    ["Cache reads", cost.cacheRead],
    ["Cache writes", cost.cacheWrite],
    ["Output", cost.output],
  ] as const;
  const total = parts.reduce((sum, [, amount]) => sum + amount, 0);
  return <div className="dashboard-panel">
    <h3 className="font-semibold">API cost at current rates</h3>
    {cost.coveredRequests === 0 ? <p className="text-sm text-muted">No applicable rates for this period.</p> : <>
      <dl className="mt-2 grid grid-cols-2 gap-x-6 gap-y-2 sm:grid-cols-3 lg:grid-cols-6">
        {parts.map(([label, amount]) => <div key={label}>
          <dt className="text-sm text-muted">{label}</dt>
          <dd className="tabular-nums">{formatUSD(amount)}</dd>
        </div>)}
        <div><dt className="text-sm text-muted">Total</dt><dd className="font-semibold tabular-nums">{formatUSD(total)}</dd></div>
        <div><dt className="text-sm text-muted">Input cost saved by caching</dt>
          <dd className="tabular-nums">{cost.cacheSavings === null ? "n/a" : formatPercent(cost.cacheSavings)}</dd></div>
      </dl>
      <p className="mt-2 text-xs text-muted">Rates available for {formatInteger(cost.coveredRequests)} of {formatInteger(requests)} requests. Cache writes use the rate table’s standard price.</p>
    </>}
  </div>;
}
