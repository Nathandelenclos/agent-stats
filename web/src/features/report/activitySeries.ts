import { useQuery } from "@tanstack/react-query";
import { getJSON, shouldRetry } from "../../shared/api";
import { formatDateTime, formatDay } from "../../shared/format";
import type { Filters } from "./types";

export type ActivityKind = "models" | "tools";
type BucketSize = "hour" | "day" | "month";

export interface MeasuredActivity {
  bucket: string;
  name: string;
  source: string;
  calls: number;
  errors: number;
  avgDurationMs: number;
  avgTtftMs: number;
  avgTokensPerSecond: number;
  durationSamples: number;
  ttftSamples: number;
  throughputSamples: number;
  input: number;
  output: number;
  cacheRead: number;
  cacheWrite: number;
  cost: number;
  premiumUnits: number;
  unpriced: number;
}

interface ActivityData {
  bucketSize: BucketSize;
  samples: MeasuredActivity[];
}

export function useActivitySeries(filters: Filters, project: string, kind: ActivityKind) {
  const params = { kind, since: filters.since, harness: filters.harness, project: filters.scope };
  return useQuery({
    queryKey: ["activity-series", params, project],
    queryFn: ({ signal }) => getJSON<ActivityData>("/api/activity-series", params, signal),
    retry: shouldRetry,
  });
}

export function activityKey(name: string, source: string) {
  return JSON.stringify([name, source]);
}

const monthFormat = new Intl.DateTimeFormat("en-US", { month: "short", year: "numeric", timeZone: "UTC" });

export function activityBuckets(data: ActivityData) {
  const epochKey = data.bucketSize === "hour";
  const months = data.bucketSize === "month";
  const parse = (key: string) => epochKey ? Number(key) : Date.parse(months ? `${key}-01` : key);
  const stamp = (date: Date) => epochKey ? String(date.getTime()) : date.toISOString().slice(0, months ? 7 : 10);
  const label = (date: Date, key: string) => epochKey ? formatDateTime(date.getTime()) : months ? monthFormat.format(date) : formatDay(key);
  const times = [...new Set(data.samples.map((sample) => parse(sample.bucket)))]
    .filter((value) => Number.isFinite(new Date(value).getTime()))
    .sort((a, b) => a - b);
  const start = times[0], end = times.at(-1);
  if (start === undefined || end === undefined) return [];
  const result: { key: string; label: string }[] = [];
  for (const date = new Date(start); date.getTime() <= end;) {
    if (result.length === 1500) {
      return times.map((time) => {
        const observed = new Date(time);
        const key = stamp(observed);
        return { key, label: label(observed, key) };
      });
    }
    const key = stamp(date);
    result.push({ key, label: label(date, key) });
    if (months) date.setUTCMonth(date.getUTCMonth() + 1);
    else date.setTime(date.getTime() + (epochKey ? 3_600_000 : 86_400_000));
  }
  return result;
}
