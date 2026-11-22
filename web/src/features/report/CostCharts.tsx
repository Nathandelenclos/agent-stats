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

/** Cost per group as a labelled bar and a value that is always visible. */
export function CostBy({
  title,
  tableName,
  model: { rows, groupCount, unpriced },
  domainMax,
}: {
  title: string;
  tableName: string;
  model: CostByModel;
  /** The cost scale shared with the chart shown beside this one. */
  domainMax: number;
}) {
  if (rows.length === 0) return null;
  const summary = `${formatInteger(groupCount)} ${tableName.toLowerCase()} groups. Both charts use the same scale.` +
    (unpriced > 0 ? ` ${formatInteger(unpriced)} requests have no known price.` : "");
  return (
    <Figure title={title} summary={summary}>
      <ul role="list" aria-label={title} className="grid grid-cols-[minmax(0,2fr)_minmax(0,5fr)_auto] gap-x-3 gap-y-2 text-sm">
        {rows.map((row, index) => {
          const name = tableName === "Harness"
            ? ({ claude: "Claude Code", codex: "Codex" } as Record<string, string>)[row.name] ?? row.name
            : row.name;
          const value = row.requests > 0 && row.requests === row.unpriced ? "Unavailable" : formatUSD(row.cost);
          return (
            <li key={index} className="col-span-3 grid grid-cols-subgrid items-center">
              <span className="min-w-0 wrap-anywhere">{name}</span>
              <span aria-hidden="true" className="min-w-0">
                <span className="block h-2 bg-[var(--mark-2)]" style={{ width: `${domainMax > 0 ? 100 * row.cost / domainMax : 0}%` }} />
              </span>
              <strong className="text-right tabular-nums">{value}</strong>
            </li>
          );
        })}
      </ul>
    </Figure>
  );
}
