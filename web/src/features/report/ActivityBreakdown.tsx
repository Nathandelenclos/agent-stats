import { useId, useState } from "react";
import { Bar, BarChart, CartesianGrid, Line, LineChart, ResponsiveContainer, Tooltip, XAxis, YAxis } from "recharts";
import { formatInteger, formatPercent } from "../../shared/format";
import type { ActivitySeries, buildSeriesBreakdown } from "./seriesBreakdown";

type Mode = "calls" | "errors" | "share";

interface Bucket {
  key: string;
  label: string;
}

interface Props {
  title: string;
  buckets: Bucket[];
  breakdown: ReturnType<typeof buildSeriesBreakdown>;
  kind: "models" | "tools";
  onSeries: (series: ActivitySeries) => void;
}

export function PlotSwatch({ color }: { color: string }) {
  return <span aria-hidden="true" className="inline-block size-2.5 shrink-0 rounded-sm" style={{ backgroundColor: color }} />;
}

export function Sparkline({ values, color }: { values: Array<number | null>; color: string }) {
  if (values.filter((value) => value !== null).length < 2) return null;
  return (
    <div aria-hidden="true">
      <LineChart width={104} height={26} data={values.map((value) => ({ value }))} accessibilityLayer={false}>
        <Line dataKey="value" stroke={color} strokeWidth={2} dot={false} isAnimationActive={false} />
      </LineChart>
    </div>
  );
}

export function ActivityBreakdown({ title, buckets, breakdown, kind, onSeries }: Props) {
  const [mode, setMode] = useState<Mode>("calls");
  const id = useId();
  const modes: { value: Mode; label: string }[] = kind === "models"
    ? [{ value: "calls", label: "Requests" }, { value: "share", label: "Share" }]
    : [{ value: "calls", label: "Calls" }, { value: "errors", label: "Errors" }];
  const label = modes.find((option) => option.value === mode)?.label ?? modes[0]!.label;
  const count = mode === "errors" ? "errors" : "calls";
  const series = [...breakdown.shown, ...(breakdown.other ? [breakdown.other] : [])];
  const totals = series.map((row) => row[count]);
  const total = totals.reduce((sum, value) => sum + value, 0);
  const points = buckets.map((bucket, index) => {
    const values = series.map((row) => row.points[index]?.[count] ?? 0);
    const sum = values.reduce((accumulator, value) => accumulator + value, 0);
    return { ...bucket, values: mode === "share" && sum > 0 ? values.map((value) => value / sum) : values };
  });
  const format = mode === "share" ? formatPercent : formatInteger;
  const time = new Map(buckets.map((bucket) => [bucket.key, bucket.label]));

  return (
    <section className="dashboard-panel" aria-labelledby={id}>
      <div className="dashboard-panel-heading">
        <h2 id={id}>{title}</h2>
        <div className="dashboard-segment" role="group" aria-label={`${title} metric`}>
          {modes.map((option) => (
            <button key={option.value} type="button" aria-pressed={mode === option.value} onClick={() => setMode(option.value)}>
              {option.label}
            </button>
          ))}
        </div>
      </div>
      {total === 0 ? <p className="text-sm text-muted">No {label.toLowerCase()} in this period.</p> : (
        <>
          <div className="dashboard-plot" aria-hidden="true">
            <ResponsiveContainer width="100%" height="100%">
              <BarChart data={points} accessibilityLayer={false}>
                <CartesianGrid vertical={false} stroke="var(--rule)" />
                <XAxis dataKey="key" tickFormatter={(key: string) => time.get(key) ?? key} minTickGap={22}
                  tick={{ fill: "var(--muted)", fontSize: 11 }} stroke="var(--rule)" />
                <YAxis tickFormatter={format} domain={mode === "share" ? [0, 1] : [0, "auto"]}
                  tick={{ fill: "var(--muted)", fontSize: 11 }} stroke="var(--rule)" width={65} />
                <Tooltip labelFormatter={(key) => time.get(String(key)) ?? key}
                  formatter={(value) => format(Number(value))}
                  contentStyle={{ background: "var(--surface)", color: "var(--ink)", borderColor: "var(--border)" }} />
                {series.map((entry, index) => (
                  <Bar key={index} name={entry.name} dataKey={(point: (typeof points)[number]) => point.values[index]}
                    stackId="activity" fill={entry.color} isAnimationActive={false}
                    cursor={entry.key !== null ? "pointer" : undefined}
                    onClick={() => { if (entry.key !== null) onSeries(entry); }} />
                ))}
              </BarChart>
            </ResponsiveContainer>
          </div>
          <ul aria-label={`${title} series`} className="mt-3 flex flex-wrap gap-x-5 gap-y-1 text-sm">
            {series.map((entry, index) => {
              const amount = mode === "share" ? formatPercent(totals[index]! / total) : formatInteger(totals[index]!);
              const legend = <><PlotSwatch color={entry.color} /><span>{entry.name}</span><span className="tabular-nums text-muted">{amount}</span></>;
              return <li key={index}>
                {entry.key !== null
                  ? <button type="button" className="flex min-h-7 items-center gap-2 text-left hover:underline" onClick={() => onSeries(entry)}>{legend}</button>
                  : <span className="flex min-h-7 items-center gap-2">{legend}</span>}
              </li>;
            })}
          </ul>
          <details className="dashboard-daily">
            <summary>Values by period</summary>
            <div className="overflow-auto" tabIndex={0} role="region" aria-label={`${title} values`}>
              <table className="dashboard-daily-table">
                <caption className="sr-only">{title} · {label}</caption>
                <thead><tr><th scope="col">Period</th>{series.map((entry, index) => <th key={index} scope="col">{entry.name}</th>)}</tr></thead>
                <tbody>
                  {points.map((point) => (
                    <tr key={point.key}>
                      <th scope="row">{point.label}</th>
                      {point.values.map((value, index) => <td key={index}>{format(value)}</td>)}
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </details>
        </>
      )}
    </section>
  );
}
