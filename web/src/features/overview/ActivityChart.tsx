import { Bar, BarChart, CartesianGrid, Cell, ResponsiveContainer, Tooltip, XAxis, YAxis } from "recharts";
import { formatDateTime, formatDay, formatInteger, formatUSD } from "../../shared/format";
import { inputTokens, type UsageGroup } from "../report/types";
import { continuousDays } from "../report/series";
import type { Metric } from "./navigation";

const hourLabel = new Intl.DateTimeFormat("en-US", { hour: "numeric" });
const metrics: Record<Metric, string> = { cost: "Cost", requests: "Requests", tokens: "Tokens" };

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
    return { day, cost: unavailable ? null : cost, requests, tokens: data ? inputTokens(data) + data.output : 0, unpriced: data?.unpriced ?? 0 };
  });
  const value = (n: number | null) => n === null ? "Unavailable" : metric === "cost" ? formatUSD(n) : formatInteger(n);
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
                  {point.unpriced > 0 && metric === "cost" && <p>{point.unpriced} requests without a known price</p>}
                </div>;
              }}
            />
            <Bar
              dataKey={metric}
              fill="var(--mark)"
              maxBarSize={52}
              radius={[5, 5, 0, 0]}
              isAnimationActive={false}
              cursor={selectDay ? "pointer" : "default"}
              onClick={(_, index) => { if (points[index]) selectDay?.(points[index].day); }}
            >
              {points.map((point) => <Cell key={point.day}
                fill={selectedDay && point.day !== selectedDay ? "color-mix(in srgb, var(--mark) 60%, var(--surface))" : "var(--mark)"}
                stroke={point.day === selectedDay ? "var(--ink)" : undefined}
                strokeWidth={point.day === selectedDay ? 2 : undefined} />)}
            </Bar>
          </BarChart>
        </ResponsiveContainer>
      </div>
      <p className="dashboard-caption">
        {!byHour && (selectedDay ? `Selected day: ${formatDay(selectedDay)}.` : "Select a day to see the sessions that contributed.")}
        {" "}Only dated requests are included.
      </p>
      <details className="dashboard-daily">
        <summary>{byHour ? "Hourly values" : "Daily values"}</summary>
        <div className="overflow-auto">
          <table className="dashboard-daily-table">
            <thead><tr><th scope="col">{byHour ? "Hour" : "Day"}</th><th scope="col">Cost</th><th scope="col">Requests</th><th scope="col">Tokens</th></tr></thead>
            <tbody>
              {points.map((point) => <tr key={point.day}>
                <th scope="row">
                  {selectDay ? <button type="button" className="dashboard-link" id={`day-${point.day}`} aria-pressed={point.day === selectedDay}
                    onClick={() => selectDay(point.day)}>{timeLabel(point.day)}</button> : timeLabel(point.day)}
                </th>
                <td>{point.cost === null ? "Unavailable" : formatUSD(point.cost)}{point.cost !== null && point.unpriced > 0 ? " known" : ""}</td>
                <td>{formatInteger(point.requests)}</td><td>{formatInteger(point.tokens)}</td>
              </tr>)}
            </tbody>
          </table>
        </div>
      </details>
    </> : <p className="text-sm text-muted">No dated requests in this period.</p>}
  </section>;
}
