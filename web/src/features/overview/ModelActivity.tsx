import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Line, LineChart, ResponsiveContainer, Tooltip, XAxis, YAxis } from "recharts";
import { getJSON, shouldRetry } from "../../shared/api";
import { describe, Failure } from "../../shared/Failure";
import { formatDay, formatMs } from "../../shared/format";
import type { Filters, UsageGroup } from "../report/types";
import { continuousDays } from "../report/series";

export function ModelActivity({ model, filters, project }: { model: string; filters: Filters; project: string }) {
  const [open, setOpen] = useState(false);
  const params = { model, since: filters.since, harness: filters.harness, project: filters.scope };
  const query = useQuery({
    queryKey: ["report", "model-activity", params, project],
    queryFn: ({ signal }) => getJSON<UsageGroup[]>("/api/model-activity", params, signal),
    enabled: open,
    retry: shouldRetry,
  });
  const source = query.data ?? [];
  const byDay = new Map(source.map((row) => [row.key, row]));
  const data = continuousDays(source).map(({ day }) => {
    const row = byDay.get(day);
    return {
      day, duration: row?.durationSamples ? row.avgDurationMs : null,
      ttft: row?.ttftSamples ? row.avgTtftMs : null,
      throughput: row?.throughputSamples ? row.avgTokensPerSecond : null,
    };
  });
  return <details className="dashboard-daily mt-2" onToggle={(event) => setOpen(event.currentTarget.open)}>
    <summary>Model performance over time</summary>
    {open && query.isPending && <p role="status">Loading model performance…</p>}
    {query.isError && <Failure title="Model activity could not be loaded." message={describe(query.error)} retry={() => void query.refetch()} />}
    {query.isSuccess && (data.length === 0 ? <p className="text-sm text-muted">No dated requests for this model.</p> : <>
      <div className="h-44" aria-hidden="true">
        <ResponsiveContainer>
          <LineChart data={data} accessibilityLayer={false}>
            <XAxis dataKey="day" tickFormatter={formatDay} tick={{ fontSize: 10 }} minTickGap={45} />
            <YAxis yAxisId="ms" tickFormatter={formatMs} tick={{ fontSize: 10 }} width={52} />
            <YAxis yAxisId="tps" orientation="right" tickFormatter={(n) => `${n}/s`} tick={{ fontSize: 10 }} width={40} />
            <Tooltip labelFormatter={(day) => formatDay(String(day))} />
            <Line yAxisId="ms" dataKey="duration" name="Duration (ms)" stroke="var(--mark)" isAnimationActive={false} />
            <Line yAxisId="ms" dataKey="ttft" name="First token (ms)" stroke="var(--mark-2)" isAnimationActive={false} />
            <Line yAxisId="tps" dataKey="throughput" name="Tokens/s" stroke="var(--muted)" isAnimationActive={false} />
          </LineChart>
        </ResponsiveContainer>
      </div>
      <div className="overflow-auto"><table className="dashboard-daily-table">
        <thead><tr><th scope="col">Day</th><th scope="col">Duration</th><th scope="col">First token</th><th scope="col">Tokens/s</th></tr></thead>
        <tbody>{data.map(({ day, duration, ttft, throughput }) => <tr key={day}>
          <th scope="row">{formatDay(day)}</th><td>{formatMs(duration ?? 0)}</td><td>{formatMs(ttft ?? 0)}</td>
          <td>{throughput === null ? "n/a" : throughput.toFixed(1)}</td>
        </tr>)}</tbody>
      </table></div>
    </>)}
  </details>;
}
