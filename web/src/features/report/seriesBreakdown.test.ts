import { expect, it } from "vitest";
import { buildSeriesBreakdown, otherSeriesColor } from "./seriesBreakdown";

it("keeps the series color when the samples are shuffled and preserves missing buckets", () => {
  const buckets = ["2026-11-20", "2026-11-21", "2026-11-22"];
  const rows = [
    { bucket: buckets[2]!, key: "a", name: "Same label", calls: 4, errors: 1 },
    { bucket: buckets[0]!, key: "b", name: "Same label", calls: 5, errors: 0 },
    { bucket: buckets[0]!, key: "a", name: "Same label", calls: 1, errors: 0 },
  ];
  const breakdown = buildSeriesBreakdown(rows, buckets);
  expect(breakdown).toEqual(buildSeriesBreakdown([...rows].reverse(), buckets));
  expect(breakdown.series.map((row) => row.key)).toEqual(["a", "b"]);
  expect(breakdown.series[0]?.points.map((point) => point.calls)).toEqual([1, 0, 4]);
  expect(breakdown.series[0]?.color).not.toBe(breakdown.series[1]?.color);
});

it("groups the remaining series without losing counts or errors", () => {
  const rows = Array.from({ length: 8 }, (_, i) => ({ bucket: "today", key: String(i), name: String(i), calls: 3, errors: 1 }));
  const result = buildSeriesBreakdown(rows, ["today"]);
  expect(result.shown).toHaveLength(6);
  expect(result.series).toHaveLength(8);
  expect(result.other).toEqual({
    key: null, name: "Other (2)", color: otherSeriesColor, calls: 6, errors: 2, points: [{ calls: 6, errors: 2 }],
  });
});
