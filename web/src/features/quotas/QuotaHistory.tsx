import { useId, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Line, LineChart, ResponsiveContainer, Tooltip, XAxis, YAxis } from "recharts";
import { getJSON } from "../../shared/api";
import { describe, Failure } from "../../shared/Failure";
import { formatDateTime, formatPercent } from "../../shared/format";
import type { QuotaWindow } from "./Quotas";

export function QuotaHistory({ version, enabled }: { version: number; enabled: boolean }) {
  const history = useQuery({
    queryKey: ["quota-history", version],
    queryFn: ({ signal }) => getJSON<QuotaWindow[]>("/api/quotas/history", {}, signal),
    enabled,
    gcTime: 0,
    retry: false,
  });
  const id = useId();
  const [selected, setSelected] = useState("");
  if (!enabled) return null;
  if (history.isPending) return <p role="status">Loading quota history…</p>;
  if (history.isError) return <Failure title="Quota history could not be loaded." message={describe(history.error)} retry={() => void history.refetch()} />;
  const key = (w: QuotaWindow) => JSON.stringify([w.provider, w.label]);
  const groups = new Map(history.data.map((w) => [key(w), `${w.provider} · ${w.label}`]));
  const current = groups.has(selected) ? selected : (groups.keys().next().value ?? "");
  const points = history.data.filter((w) => key(w) === current).map((w) => ({
    ts: Date.parse(w.observedAt), remaining: w.remaining, resets: Date.parse(w.resetAt),
  }));
  return <div className="dashboard-panel">
    <h3 className="font-semibold">Quota history</h3>
    <p className="text-sm text-muted">Observations taken when Agent Stats checks your quotas in the last 30 days.</p>
    {points.length === 0 ? <p className="mt-2 text-sm">No recorded observations yet.</p> : <>
      <label htmlFor={id} className="mt-3 block text-sm">Quota window</label>
      <select id={id} className="mb-3 min-h-9 rounded border border-rule bg-bg px-2 text-sm" value={current}
        onChange={(event) => setSelected(event.target.value)}>
        {[...groups].map(([value, label]) => <option key={value} value={value}>{label}</option>)}
      </select>
      <div className="h-44" aria-hidden="true">
        <ResponsiveContainer>
          <LineChart data={points.toReversed()} accessibilityLayer={false}>
            <XAxis dataKey="ts" type="number" domain={["dataMin", "dataMax"]} tickFormatter={formatDateTime} tick={{ fontSize: 10 }} minTickGap={55} />
            <YAxis domain={[0, 100]} tickFormatter={(v) => v + "%"} tick={{ fontSize: 10 }} width={38} />
            <Tooltip labelFormatter={(v) => formatDateTime(Number(v))} formatter={(v) => `${v}%`} />
            <Line name="Remaining" dataKey="remaining" stroke="var(--mark)" isAnimationActive={false} />
          </LineChart>
        </ResponsiveContainer>
      </div>
      <details className="dashboard-daily">
        <summary>Recorded observations</summary>
        <table className="dashboard-daily-table">
          <thead><tr><th scope="col">Checked</th><th scope="col">Remaining</th><th scope="col">Resets</th></tr></thead>
          <tbody>{points.map((p, index) => <tr key={index}>
            <th scope="row">{formatDateTime(p.ts)}</th><td>{formatPercent(p.remaining / 100)}</td>
            <td>{formatDateTime(p.resets > 0 ? p.resets : 0)}</td>
          </tr>)}</tbody>
        </table>
      </details>
    </>}
  </div>;
}
