import { Bar, BarChart, CartesianGrid, ResponsiveContainer, Tooltip, XAxis, YAxis } from "recharts";
import { formatDay, formatInteger, formatUSD } from "../../shared/format";
import type { CostByModel, CostOverTimeModel, DayPoint } from "./series";

const axisText = { fill: "var(--muted)", fontSize: 12 };

/** Requests whose model has no known price count as $0 in the figures; the charts say so. */
function unpricedNote(n: number): string {
  return n > 0 ? ` ${formatInteger(n)} requests have no known price and count as $0.` : "";
}

function Figure({ title, summary, children }: { title: string; summary: string; children: React.ReactNode }) {
  return (
    <figure className="rounded-lg border border-rule bg-surface p-4">
      <figcaption className="mb-3">
        <h3 className="text-base font-semibold">{title}</h3>
        <p className="text-sm text-muted">{summary}</p>
      </figcaption>
      {children}
    </figure>
  );
}

interface TipProps<T> {
  active?: boolean;
  payload?: ReadonlyArray<{ payload?: T }>;
}

/** Date, series, value and unit, in this order, in every chart. */
function Tip({ title, cost, requests }: { title: string; cost: number; requests: number }) {
  return (
    <div className="rounded-md border border-border bg-bg px-3 py-2 text-sm text-ink shadow">
      <p className="font-semibold">{title}</p>
      <p>Cost: {formatUSD(cost)}</p>
      <p>Requests: {formatInteger(requests)}</p>
    </div>
  );
}

/** Cost per local day. The values are also in a table behind the chart, for those who read numbers and not bars. */
export function CostOverTime({ model: { days, total, busiest, unpriced } }: { model: CostOverTimeModel }) {
  const summary =
    `${formatUSD(total)} over ${days.length} ${days.length === 1 ? "day" : "days"}, ${formatDay(days[0]!.day)} to ${formatDay(days[days.length - 1]!.day)}. ` +
    `Busiest day: ${formatDay(busiest.day)}, ${formatUSD(busiest.cost)}. Days follow this machine's clock; the latest day may be incomplete.${unpricedNote(unpriced)}`;
  return (
    <Figure title="Cost per day" summary={summary}>
      <div className="h-64">
        <ResponsiveContainer width="100%" height="100%">
          <BarChart data={days} margin={{ top: 4, right: 8, bottom: 0, left: 0 }}>
            <CartesianGrid stroke="var(--rule)" vertical={false} />
            <XAxis dataKey="day" tick={axisText} tickFormatter={shortDay} minTickGap={24} stroke="var(--border)" />
            <YAxis tick={axisText} tickFormatter={(v: number) => formatUSD(v)} width={64} stroke="var(--border)" />
            <Tooltip
              cursor={{ fill: "var(--rule)" }}
              content={({ active, payload }: TipProps<DayPoint>) => {
                const p = active ? payload?.[0]?.payload : undefined;
                return p ? <Tip title={formatDay(p.day)} cost={p.cost} requests={p.requests} /> : null;
              }}
            />
            <Bar dataKey="cost" fill="var(--mark)" isAnimationActive={false} />
          </BarChart>
        </ResponsiveContainer>
      </div>
      <details className="mt-3 text-sm">
        <summary className="min-h-8 cursor-pointer py-1">Show the values per day</summary>
        <div role="region" aria-label="Cost per day, scrollable" tabIndex={0} className="mt-2 max-h-72 overflow-auto rounded-md border border-rule bg-bg">
          <table className="w-full border-collapse">
            <caption className="sr-only">Cost and requests per day</caption>
            <thead className="sticky top-0 bg-surface">
              <tr>
                <th scope="col" className="px-3 py-1.5 text-left">Day</th>
                <th scope="col" className="px-3 py-1.5 text-right">Requests</th>
                <th scope="col" className="px-3 py-1.5 text-right">Cost</th>
              </tr>
            </thead>
            <tbody>
              {days.map((d) => (
                <tr key={d.day} className="border-t border-rule">
                  <th scope="row" className="px-3 py-1 text-left font-normal">{formatDay(d.day)}</th>
                  <td className="px-3 py-1 text-right tabular-nums">{formatInteger(d.requests)}</td>
                  <td className="px-3 py-1 text-right tabular-nums">{formatUSD(d.cost)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </details>
    </Figure>
  );
}

function shortDay(day: string): string {
  return formatDay(day).replace(/,?\s*\d{4}$/, "");
}

/** Cost by group, biggest first; the same figures are in the table of that group below the charts. */
export function CostBy({ title, tableName, model: { rows, groupCount, unpriced, domainMax } }: { title: string; tableName: string; model: CostByModel }) {
  if (rows.length === 0) return null;
  const lead = rows[0]!;
  const summary = `${formatUSD(lead.cost)} for ${lead.name}, the most expensive of ${groupCount}. Exact figures in the ${tableName} table below. Charts side by side share one cost scale.${unpricedNote(unpriced)}`;
  return (
    <Figure title={title} summary={summary}>
      <div style={{ height: Math.max(120, rows.length * 32 + 32) }}>
        <ResponsiveContainer width="100%" height="100%">
          <BarChart data={rows} layout="vertical" margin={{ top: 0, right: 16, bottom: 0, left: 0 }}>
            <CartesianGrid stroke="var(--rule)" horizontal={false} />
            <XAxis type="number" domain={[0, domainMax]} tick={axisText} tickFormatter={(v: number) => formatUSD(v)} stroke="var(--border)" />
            <YAxis type="category" dataKey="name" tick={axisText} width={132} tickFormatter={clip} interval={0} stroke="var(--border)" />
            <Tooltip
              cursor={{ fill: "var(--rule)" }}
              content={({ active, payload }: TipProps<{ name: string; cost: number; requests: number }>) => {
                const p = active ? payload?.[0]?.payload : undefined;
                return p ? <Tip title={p.name} cost={p.cost} requests={p.requests} /> : null;
              }}
            />
            <Bar dataKey="cost" fill="var(--mark)" isAnimationActive={false} />
          </BarChart>
        </ResponsiveContainer>
      </div>
    </Figure>
  );
}

function clip(name: string): string {
  return name.length > 20 ? `${name.slice(0, 19)}…` : name;
}
