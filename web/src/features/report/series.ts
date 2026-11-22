import type { UsageGroup } from "./types";

/** One day of the cost-over-time chart. */
export interface DayPoint {
  day: string; // 2006-01-02
  cost: number;
  requests: number;
}

function key(d: Date): string {
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`;
}

/**
 * The days of the report as a continuous run from the first to the last one. A day with no request in the index
 * really had none, so it reads as zero requests and zero cost; the gap is shown rather than closed up.
 */
export function continuousDays(daily: UsageGroup[]): DayPoint[] {
  const first = daily[0];
  const last = daily[daily.length - 1];
  if (!first || !last) return [];
  const byDay = new Map(daily.map((g) => [g.key, g]));
  const [y = 1970, m = 1, d = 1] = first.key.split("-").map(Number);
  const out: DayPoint[] = [];
  for (let at = new Date(y, m - 1, d); key(at) <= last.key; at = new Date(at.getFullYear(), at.getMonth(), at.getDate() + 1)) {
    const g = byDay.get(key(at));
    out.push({ day: key(at), cost: g?.cost ?? 0, requests: g?.requests ?? 0 });
  }
  return out;
}

/** The groups that carry the most cost, the rest folded into one "other" row, so a chart stays readable. */
export function topGroups(groups: UsageGroup[], limit: number): { name: string; cost: number; requests: number }[] {
  const rows = groups.map((g) => ({ name: g.key || "(none)", cost: g.cost, requests: g.requests }));
  if (rows.length <= limit) return rows;
  const rest = rows.slice(limit - 1);
  return [
    ...rows.slice(0, limit - 1),
    {
      name: `${rest.length} others`,
      cost: rest.reduce((sum, r) => sum + r.cost, 0),
      requests: rest.reduce((sum, r) => sum + r.requests, 0),
    },
  ];
}

/** Requests whose model has no known price count as $0 in the figures; charts say so. */
export function unpricedRequests(groups: UsageGroup[]): number {
  return groups.reduce((sum, g) => sum + g.unpriced, 0);
}

/** What the cost-per-day chart shows: the continuous days and the figures its summary quotes. */
export interface CostOverTimeModel {
  days: DayPoint[];
  total: number; // cost of the days shown, which leaves out requests with no known time
  busiest: DayPoint;
  unpriced: number;
}

export function buildCostOverTime(daily: UsageGroup[]): CostOverTimeModel | null {
  const days = continuousDays(daily);
  const first = days[0];
  if (!first) return null;
  return {
    days,
    total: days.reduce((sum, d) => sum + d.cost, 0),
    busiest: days.reduce((a, b) => (b.cost > a.cost ? b : a), first),
    unpriced: unpricedRequests(daily),
  };
}

/** What a cost-by-group chart shows. */
export interface CostByModel {
  rows: { name: string; cost: number; requests: number }[];
  groupCount: number;
  unpriced: number;
}

export function buildCostBy(groups: UsageGroup[], limit: number): CostByModel {
  return { rows: topGroups(groups, limit), groupCount: groups.length, unpriced: unpricedRequests(groups) };
}

/** The cost axis charts that sit side by side share, so equal bars mean equal costs: the biggest bar drawn in any of them. */
export function sharedDomainMax(...models: CostByModel[]): number {
  return Math.max(0, ...models.flatMap((m) => m.rows.map((r) => r.cost)));
}
