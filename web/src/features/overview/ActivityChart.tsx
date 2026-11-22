import { Bar, BarChart, CartesianGrid, Cell, ResponsiveContainer, Tooltip, XAxis, YAxis } from "recharts";
import { formatDateTime, formatDay, formatInteger, formatUSD } from "../../shared/format";
import { inputTokens, type UsageGroup } from "../report/types";
import { continuousDays } from "../report/series";
import { PlotSwatch } from "../report/ActivityBreakdown";
import { tokenColors, tokenLabels } from "./OverviewDetails";
import type { Metric } from "./navigation";

const hourLabel = new Intl.DateTimeFormat("en-US", { hour: "numeric" });
const metrics: Record<Metric, string> = { cost: "Cost", requests: "Requests", tokens: "Tokens" };

const tokenKeys = ["input", "cacheRead", "cacheWrite", "output"] as const;
const stacks = {
  cost: [{ key: "cost", label: "Estimated cost", color: "var(--plot-magenta)" }],
  requests: [
    { key: "completed", label: "Non-failed", color: "var(--plot-cyan)" },
    { key: "failed", label: "Failed", color: "var(--plot-coral)" },
  ],
  tokens: tokenKeys.map((key) => ({ key, label: tokenLabels[key], color: tokenColors[key] })),
} as const;

function dayLabel(key: string) {
  return formatDay(key).replace(/,?\s*\d{4}$/, "");
}

function compactNumber(value: number) {
  return new Intl.NumberFormat(undefined, { notation: "compact", maximumFractionDigits: 1 }).format(value);
}

export function ActivityChart({ daily, hourly, metric, onMetric, onDay, selectedDay }: {
  daily: UsageGroup[];
  hourly?: UsageGroup[];
  metric: Metric;
  onMetric: (metric: Metric) => void;
  onDay?: (day: string) => void;
  selectedDay?: string;
}) {
  const byHour = !!hourly?.length;
  const dataInRange = hourly?.length ? hourly : daily;
  const byDay = new Map(dataInRange.map((bucket) => [bucket.key, bucket]));
  const hourMs = 3_600_000;
  const hours = (groups: UsageGroup[]) => {
    const first = Number(groups[0]?.key);
    const last = Number(groups.at(-1)?.key);
    return Array.from({ length: Math.max(0, Math.min(75, (last - first) / hourMs + 1)) }, (_, i) => {
      const hour = String(first + i * hourMs);
      const group = byDay.get(hour);
      return { day: hour, cost: group?.cost ?? 0, requests: group?.requests ?? 0 };
    });
  };
  const timeLabel = (key: string) => byHour ? formatDateTime(Number(key)) : formatDay(key);
  const tickLabel = (key: string) => byHour
    ? hourLabel.format(Number(key)) : dayLabel(key);
  const selectDay = byHour ? undefined : onDay;
  const points = (byHour ? hours(dataInRange) : continuousDays(daily)).map(({ day, cost, requests }) => {
    const data = byDay.get(day);
    const unavailable = requests > 0 && data?.unpriced === requests;
    const failed = data?.failed ?? 0;
    return {
      day, cost: unavailable ? null : cost, requests, failed, completed: Math.max(0, requests - failed),
      tokens: data ? inputTokens(data) + data.output : 0, unpriced: data?.unpriced ?? 0,
      input: data?.input ?? 0, cacheRead: data?.cacheRead ?? 0, cacheWrite: data?.cacheWrite ?? 0, output: data?.output ?? 0,
    };
  });
  const value = (n: number | null) => n === null ? "Unavailable" : metric === "cost" ? formatUSD(n) : formatInteger(n);
  const bars = stacks[metric];
  const from = points[0]?.day;
  const until = points.at(-1)?.day;
  return <section className="dashboard-panel" aria-labelledby="activity-title">
    <div className="dashboard-panel-heading">
      <div>
        <h2 id="activity-title">Activity over time</h2>
        {from && until && <p className="dashboard-caption">{byHour ? timeLabel(from) : dayLabel(from)}{from !== until && ` – ${byHour ? timeLabel(until) : dayLabel(until)}`} · Local time</p>}
      </div>
      <div className="dashboard-segment" role="group" aria-label="Graph metric">
        {(Object.keys(metrics) as Metric[]).map((key) => (
          <button type="button" key={key} aria-pressed={metric === key} onClick={() => onMetric(key)}>{metrics[key]}</button>
        ))}
      </div>
    </div>
    {points.length > 0 ? <>
      <div className="dashboard-plot" aria-hidden="true">
        <ResponsiveContainer width="100%" height="100%">
          <BarChart data={points} margin={{ top: 5, right: 6, bottom: 0, left: 0 }} accessibilityLayer={false}>
            <CartesianGrid vertical={false} stroke="var(--rule)" />
            <XAxis dataKey="day" tickFormatter={tickLabel} tick={{ fill: "var(--muted)", fontSize: 11 }} minTickGap={18} stroke="var(--rule)" />
            <YAxis tick={{ fill: "var(--muted)", fontSize: 11 }} tickFormatter={(n: number) => metric === "cost" ? formatUSD(n) : compactNumber(n)} width={65} stroke="var(--rule)" />
            <Tooltip
              cursor={{ fill: "var(--rule)", opacity: 0.4 }}
              content={({ active, payload }) => {
                const point = active ? payload?.[0]?.payload as (typeof points)[number] | undefined : undefined;
                return point && <div className="dashboard-tooltip"><strong>{timeLabel(point.day)}</strong><p>{metrics[metric]} · {value(point[metric])}</p>
                  {bars.length > 1 && bars.map((bar) => <p className="flex items-center gap-2" key={bar.key}>
                    <PlotSwatch color={bar.color} />{bar.label} · {value(point[bar.key])}
                  </p>)}
                  {point.unpriced > 0 && metric === "cost" && <p>{point.unpriced} requests without a known price</p>}
                </div>;
              }}
            />
            {bars.map((bar) => (
              <Bar key={bar.key} dataKey={bar.key} fill={bar.color} stackId="metric" maxBarSize={52}
                isAnimationActive={false} cursor={selectDay ? "pointer" : "default"}
                onClick={(_, index) => { if (points[index]) selectDay?.(points[index].day); }}>
                {selectedDay && points.map((point) => <Cell key={point.day}
                  fill={point.day === selectedDay ? bar.color : `color-mix(in srgb, ${bar.color} 60%, var(--surface))`}
                  stroke={point.day === selectedDay ? "var(--ink)" : undefined}
                  strokeWidth={point.day === selectedDay ? 2 : undefined} />)}
              </Bar>
            ))}
          </BarChart>
        </ResponsiveContainer>
      </div>
      {bars.length > 1 && <ul className="mb-2 flex flex-wrap gap-x-4 gap-y-1 text-sm">
        {bars.map((bar) => <li className="flex items-center gap-2" key={bar.key}><PlotSwatch color={bar.color} />{bar.label}</li>)}
      </ul>}
      <p className="dashboard-caption">
        {!byHour && (selectedDay ? `Selected day: ${formatDay(selectedDay)}.` : "Select a day to see the sessions that contributed.")}
        {" "}Only dated requests are included.
      </p>
      <details className="dashboard-daily">
        <summary>{byHour ? "Hourly values" : "Daily values"}</summary>
        <div className="overflow-auto">
          <table className="dashboard-daily-table">
            <thead><tr><th scope="col">{byHour ? "Hour" : "Day"}</th><th scope="col">Cost</th><th scope="col">Requests</th><th scope="col">Failed</th><th scope="col">Tokens</th>
              {metric === "tokens" && tokenKeys.map((key) => <th scope="col" key={key}>{tokenLabels[key]}</th>)}</tr></thead>
            <tbody>
              {points.map((point) => <tr key={point.day}>
                <th scope="row">
                  {selectDay ? <button type="button" className="dashboard-link" id={`day-${point.day}`} aria-pressed={point.day === selectedDay}
                    onClick={() => selectDay(point.day)}>{timeLabel(point.day)}</button> : timeLabel(point.day)}
                </th>
                <td>{point.cost === null ? "Unavailable" : formatUSD(point.cost)}{point.cost !== null && point.unpriced > 0 ? " known" : ""}</td>
                <td>{formatInteger(point.requests)}</td><td>{formatInteger(point.failed)}</td><td>{formatInteger(point.tokens)}</td>
                {metric === "tokens" && tokenKeys.map((key) => <td key={key}>{formatInteger(point[key])}</td>)}
              </tr>)}
            </tbody>
          </table>
        </div>
      </details>
    </> : <p className="text-sm text-muted">No dated requests in this period.</p>}
  </section>;
}
