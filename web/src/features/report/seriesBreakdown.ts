const seriesColors = [
  "var(--plot-cyan)",
  "var(--plot-magenta)",
  "var(--plot-violet)",
  "var(--plot-amber)",
  "var(--plot-green)",
  "var(--plot-blue)",
] as const;

export const otherSeriesColor = "var(--plot-neutral)";

export interface ActivitySample {
  bucket: string;
  key: string;
  name: string;
  calls: number;
  errors: number;
}

type Value = Pick<ActivitySample, "calls" | "errors">;

export interface ActivitySeries extends Value {
  key: string;
  name: string;
  color: string;
  points: Value[];
}

export function buildSeriesBreakdown(samples: ActivitySample[], bucketKeys: string[]) {
  const bins = new Map<string, Map<string, Value>>(bucketKeys.map((key) => [key, new Map()]));
  const totals = new Map<string, Omit<ActivitySeries, "color" | "points">>();
  for (const sample of samples) {
    const bin = bins.get(sample.bucket);
    if (!bin) continue;
    const point = bin.get(sample.key) ?? { calls: 0, errors: 0 };
    point.calls += sample.calls;
    point.errors += sample.errors;
    bin.set(sample.key, point);

    const series = totals.get(sample.key) ?? { key: sample.key, name: sample.name, calls: 0, errors: 0 };
    series.calls += sample.calls;
    series.errors += sample.errors;
    totals.set(sample.key, series);
  }

  const ranked = [...totals.values()].sort((a, b) => b.calls - a.calls || a.key.localeCompare(b.key));
  const series = ranked.map((row, index): ActivitySeries => ({
    ...row,
    color: seriesColors[index] ?? otherSeriesColor,
    points: bucketKeys.map((key) => bins.get(key)?.get(row.key) ?? { calls: 0, errors: 0 }),
  }));
  const shown = series.slice(0, seriesColors.length);
  const rest = series.slice(seriesColors.length);
  const other = rest.length > 0 ? {
    key: null,
    name: `Other (${rest.length})`,
    color: otherSeriesColor,
    calls: rest.reduce((sum, row) => sum + row.calls, 0),
    errors: rest.reduce((sum, row) => sum + row.errors, 0),
    points: bucketKeys.map((_, index) => rest.reduce((sum, row) => ({
      calls: sum.calls + (row.points[index]?.calls ?? 0),
      errors: sum.errors + (row.points[index]?.errors ?? 0),
    }), { calls: 0, errors: 0 })),
  } : undefined;
  return { series, shown, other };
}
