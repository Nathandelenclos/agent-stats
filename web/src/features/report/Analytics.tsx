import { useState } from "react";
import { DataTable, type Column } from "../../shared/DataTable";
import { describe, Failure } from "../../shared/Failure";
import { formatInteger, formatMs, formatPercent, formatUSD } from "../../shared/format";
import { ActivityBreakdown, PlotSwatch, Sparkline } from "./ActivityBreakdown";
import { activityBuckets, activityKey, useActivitySeries, type ActivityKind, type MeasuredActivity } from "./activitySeries";
import { buildSeriesBreakdown, otherSeriesColor, type ActivitySeries } from "./seriesBreakdown";
import type { Filters, Tool } from "./types";

const decimal = new Intl.NumberFormat("en-US", { maximumFractionDigits: 1 });
const measuredMs = (value: number) => value === 0 ? "0 ms" : formatMs(value);
const timings = [
  { label: "Latency", value: "avgDurationMs", count: "durationSamples", format: measuredMs, color: "var(--plot-violet)" },
  { label: "First token", value: "avgTtftMs", count: "ttftSamples", format: measuredMs, color: "var(--plot-amber)" },
  { label: "Output rate", value: "avgTokensPerSecond", count: "throughputSamples", format: (value: number) => `${decimal.format(value)} tok/s`, color: "var(--plot-green)" },
] as const;

interface ModelRow extends ActivitySeries {
  model: string;
  provider: string;
  samples: MeasuredActivity[];
  measures: Array<{ value: number; count: number }>;
  tokens: number;
  cacheRate: number | null;
  cost: number;
  unpriced: number;
  premiumUnits: number;
}

function average(samples: MeasuredActivity[], metric: (typeof timings)[number]) {
  const count = samples.reduce((sum, row) => sum + row[metric.count], 0);
  const weighted = samples.reduce((sum, row) => sum + row[metric.value] * row[metric.count], 0);
  return { value: count > 0 ? weighted / count : 0, count };
}

function label(kind: ActivityKind, name: string, source: string) {
  if (kind === "models") return `${name || "Unknown model"} · ${source || "Unknown provider"}`;
  return source ? `${source} / ${name || "Unknown tool"}` : name || "Unknown tool";
}

function errorRate(errors: number, calls: number) {
  return calls > 0 ? formatPercent(errors / calls) : "—";
}

interface Props {
  kind: ActivityKind;
  filters: Filters;
  project: string;
  tools?: Tool[];
  openTool: (name: string, server: string) => void;
  openRequests: (model: string, provider: string) => void;
}

export function Analytics({ kind, filters, project, tools, openTool, openRequests }: Props) {
  const query = useActivitySeries(filters, project, kind);
  const [selected, choose] = useState<string>();
  if (query.isPending) return <p role="status">Loading activity series…</p>;
  if (query.isError) return <Failure title="Activity could not be compared." message={describe(query.error)} retry={() => void query.refetch()} />;

  const { data } = query;
  const buckets = activityBuckets(data);
  const samples = new Map<string, MeasuredActivity[]>();
  const inputs = data.samples.map((entry) => {
    const key = activityKey(entry.name, entry.source);
    const records = samples.get(key);
    if (records) records.push(entry);
    else samples.set(key, [entry]);
    return { ...entry, key, name: label(kind, entry.name, entry.source) };
  });
  const breakdown = buildSeriesBreakdown(inputs, buckets.map((bucket) => bucket.key));
  function onSeries(series: ActivitySeries) {
    const entry = samples.get(series.key)?.[0];
    if (kind === "models") choose(series.key);
    else if (entry) openTool(entry.name, entry.source);
  }

  const rows: ModelRow[] = kind === "models" ? breakdown.series.flatMap((row) => {
    const records = samples.get(row.key);
    const first = records?.[0];
    if (!records || !first) return [];
    const totals = records.reduce((sum, record) => {
      sum.input += record.input + record.cacheRead + record.cacheWrite;
      sum.output += record.output;
      sum.cacheRead += record.cacheRead;
      sum.cost += record.cost;
      sum.unpriced += record.unpriced;
      sum.premiumUnits += record.premiumUnits;
      return sum;
    }, { input: 0, output: 0, cacheRead: 0, cost: 0, unpriced: 0, premiumUnits: 0 });
    return [{ ...row, model: first.name, provider: first.source, samples: records, measures: timings.map((metric) => average(records, metric)),
      tokens: totals.input + totals.output, cacheRate: totals.input > 0 ? totals.cacheRead / totals.input : null,
      cost: totals.cost, unpriced: totals.unpriced, premiumUnits: totals.premiumUnits }];
  }) : [];
  const detail = rows.find((row) => row.key === selected);

  return (
    <div className="grid min-w-0 gap-4">
      <ActivityBreakdown title={kind === "models" ? "Requests by model over time" : "Tool calls over time"}
        buckets={buckets} breakdown={breakdown} kind={kind} onSeries={onSeries} />
      {detail && <ModelPerformance model={detail} buckets={buckets} openRequests={openRequests} />}
      {kind === "models"
        ? <ModelTable rows={rows} select={choose} />
        : tools && <ToolTable tools={tools} series={breakdown.series} open={openTool} />}
    </div>
  );
}

function ModelPerformance({ model, buckets, openRequests }: {
  model: ModelRow;
  buckets: ReturnType<typeof activityBuckets>;
  openRequests: Props["openRequests"];
}) {
  const byBucket = new Map(model.samples.map((sample) => [sample.bucket, sample]));
  return (
    <section className="dashboard-panel">
      <div className="dashboard-panel-heading">
        <h2>{model.name}</h2>
        {model.model && <button type="button" className="dashboard-link" onClick={() => openRequests(model.model, model.provider)}>View model requests</button>}
      </div>
      <dl className="grid gap-3 sm:grid-cols-3">
        {timings.map((metric, index) => {
          const measure = model.measures[index]!;
          const values = buckets.map(({ key }) => {
            const sample = byBucket.get(key);
            return sample && sample[metric.count] > 0 ? sample[metric.value] : null;
          });
          return <div key={metric.label} className="rounded-lg border border-rule px-3 py-2">
            <dt className="text-sm text-muted">{metric.label}</dt>
            <dd className="tabular-nums font-semibold">{measure.count ? metric.format(measure.value) : "Unavailable"}</dd>
            <dd className="text-xs text-muted">{formatInteger(measure.count)} of {formatInteger(model.calls)} measured</dd>
            <dd><Sparkline values={values} color={metric.color} /></dd>
          </div>;
        })}
      </dl>
      <details className="dashboard-daily">
        <summary>Performance by period</summary>
        <div className="overflow-x-auto" tabIndex={0} role="region" aria-label="Model performance by period">
          <table className="dashboard-daily-table">
            <caption className="sr-only">{model.name} performance</caption>
            <thead><tr><th scope="col">Period</th><th scope="col">Requests</th>{timings.map((metric) => <th scope="col" key={metric.label}>{metric.label}</th>)}</tr></thead>
            <tbody>{buckets.map(({ key, label }) => {
              const sample = byBucket.get(key);
              return <tr key={key}><th scope="row">{label}</th><td>{formatInteger(sample?.calls ?? 0)}</td>
                {timings.map((metric) => <td key={metric.label}>{sample && sample[metric.count] > 0
                  ? `${metric.format(sample[metric.value])} (${formatInteger(sample[metric.count])})` : "—"}</td>)}
              </tr>;
            })}</tbody>
          </table>
        </div>
      </details>
    </section>
  );
}

function ModelTable({ rows, select }: { rows: ModelRow[]; select: (key: string) => void }) {
  const columns: Column<ModelRow>[] = [
    { id: "model", header: "Model", value: (row) => row.name, render: (row) => (
      <button type="button" className="dashboard-link inline-flex items-center gap-2" onClick={() => select(row.key)}><PlotSwatch color={row.color} />{row.name}</button>
    ) },
    { id: "trend", header: "Trend", value: (row) => row.calls, render: (row) => <Sparkline values={row.points.map((point) => point.calls)} color={row.color} /> },
    { id: "requests", header: "Requests", value: (row) => row.calls, render: (row) => formatInteger(row.calls), numeric: true },
    { id: "cost", header: "Estimated cost", value: (row) => row.unpriced === row.calls ? -1 : row.cost,
      render: (row) => row.unpriced === row.calls ? "—" : formatUSD(row.cost) + (row.unpriced > 0 ? " known" : ""), numeric: true },
    { id: "tokens", header: "Tokens", value: (row) => row.tokens, render: (row) => formatInteger(row.tokens), numeric: true },
    { id: "cache", header: "Cache rate", value: (row) => row.cacheRate ?? -1,
      render: (row) => row.cacheRate === null ? "—" : formatPercent(row.cacheRate), numeric: true },
    { id: "premium", header: "Premium units", value: (row) => row.premiumUnits, render: (row) => row.premiumUnits > 0 ? decimal.format(row.premiumUnits) : "—", numeric: true },
    { id: "errors", header: "Errors", value: (row) => row.errors, render: (row) => `${formatInteger(row.errors)} · ${errorRate(row.errors, row.calls)}`, numeric: true },
    ...timings.map((metric, index): Column<ModelRow> => ({
      id: metric.value, header: metric.label, value: (row) => row.measures[index]?.count ? row.measures[index]!.value : -1,
      render: (row) => {
        const measure = row.measures[index]!;
        return measure.count ? `${metric.format(measure.value)} (${formatInteger(measure.count)})` : "—";
      },
      numeric: true,
    })),
  ];
  return (
    <section className="dashboard-panel">
      <h2 className="mb-2 font-semibold">By model and provider</h2>
      <p className="dashboard-caption mb-3">Dated requests. Timing values include the number of measured samples.</p>
      <DataTable caption="Model and provider performance" columns={columns} rows={rows} rowKey={(row) => row.key} initialSort="requests" />
    </section>
  );
}

function ToolTable({ tools, series, open }: { tools: Tool[]; series: ActivitySeries[]; open: Props["openTool"] }) {
  const trends = new Map(series.map((row) => [row.key, row]));
  const columns: Column<Tool>[] = [
    { id: "tool", header: "Tool", value: (row) => label("tools", row.name, row.server), render: (row) => (
      <button type="button" className="dashboard-link inline-flex items-center gap-2" onClick={() => open(row.name, row.server)}>
        <PlotSwatch color={trends.get(activityKey(row.name, row.server))?.color ?? otherSeriesColor} />{label("tools", row.name, row.server)}
      </button>
    ) },
    { id: "trend", header: "Trend", value: (row) => row.calls, render: (row) => {
      const series = trends.get(activityKey(row.name, row.server));
      return series && <Sparkline values={series.points.map((point) => point.calls)} color={series.color} />;
    } },
    { id: "calls", header: "Calls", value: (row) => row.calls, render: (row) => formatInteger(row.calls), numeric: true },
    { id: "errors", header: "Errors", value: (row) => row.errors, render: (row) => `${formatInteger(row.errors)} · ${errorRate(row.errors, row.calls)}`, numeric: true },
    { id: "args", header: "Argument chars", value: (row) => row.argsChars, render: (row) => formatInteger(row.argsChars), numeric: true },
    { id: "result", header: "Result chars", value: (row) => row.resultChars, render: (row) => formatInteger(row.resultChars), numeric: true },
    { id: "result-per-call", header: "Chars per call", value: (row) => row.calls ? row.resultChars / row.calls : 0,
      render: (row) => row.calls ? formatInteger(Math.round(row.resultChars / row.calls)) : "—", numeric: true },
    { id: "duration", header: "Avg duration", value: (row) => row.avgMs, render: (row) => formatMs(row.avgMs), numeric: true },
  ];
  return (
    <section className="dashboard-panel">
      <h2 className="mb-2 font-semibold">By tool</h2>
      <p className="dashboard-caption mb-3">Trends cover dated calls. The table includes every call in scope.</p>
      <DataTable caption="Tool activity" columns={columns} rows={tools} rowKey={(row) => activityKey(row.name, row.server)} initialSort="calls" />
    </section>
  );
}
