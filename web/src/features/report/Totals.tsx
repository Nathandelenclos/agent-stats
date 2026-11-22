import { formatCompact, formatInteger, formatPercent, formatTimestamp, formatUSD } from "../../shared/format";
import { inputTokens, type Usage } from "./types";

function Stat({ label, value, hint }: { label: string; value: string; hint?: string }) {
  return (
    <div className="rounded-lg border border-rule bg-surface px-4 py-3">
      <dt className="text-sm text-muted">{label}</dt>
      <dd className="text-xl font-semibold tabular-nums">{value}</dd>
      {hint && <dd className="text-xs text-muted">{hint}</dd>}
    </div>
  );
}

/** The totals of the period. */
export function Totals({ total }: { total: Usage }) {
  if (total.requests === 0) {
    return <p className="rounded-lg border border-rule bg-surface p-4">No requests in this period.</p>;
  }
  return (
    <dl className="grid grid-cols-[repeat(auto-fit,minmax(10rem,1fr))] gap-3">
      <Stat label="Cost" value={formatUSD(total.cost)} hint={total.unpriced > 0 ? `${formatInteger(total.unpriced)} requests have no known price` : undefined} />
      <Stat label="Requests" value={formatInteger(total.requests)} hint={`${formatInteger(total.failed)} failed`} />
      <Stat label="Tokens in" value={formatCompact(inputTokens(total))} hint={`${formatCompact(total.cacheRead)} read from cache`} />
      <Stat label="Tokens out" value={formatCompact(total.output)} />
      <Stat label="Cache rate" value={formatPercent(total.cacheRate)} hint="of input tokens" />
      <Stat label="Activity" value={`${formatTimestamp(total.firstTs)} to ${formatTimestamp(total.lastTs)}`} />
    </dl>
  );
}
