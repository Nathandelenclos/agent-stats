/** The interface is in English: numbers and dates follow, whatever the browser's language. */
const LOCALE = "en-US";

const integer = new Intl.NumberFormat(LOCALE, { maximumFractionDigits: 0 });
const compact = new Intl.NumberFormat(LOCALE, { notation: "compact", maximumFractionDigits: 1 });
const usd = new Intl.NumberFormat(LOCALE, { style: "currency", currency: "USD" });
const usdSmall = new Intl.NumberFormat(LOCALE, { style: "currency", currency: "USD", minimumFractionDigits: 2, maximumFractionDigits: 4 });
const percent = new Intl.NumberFormat(LOCALE, { style: "percent", maximumFractionDigits: 0 });
const date = new Intl.DateTimeFormat(LOCALE, { year: "numeric", month: "short", day: "numeric" });

export const formatInteger = (n: number): string => integer.format(n);
export const formatCompact = (n: number): string => compact.format(n);
export const formatPercent = (ratio: number): string => percent.format(ratio);

/** Dollars with cents; under one cent, enough digits to show something. */
export function formatUSD(n: number): string {
  return n > 0 && n < 0.01 ? usdSmall.format(n) : usd.format(n);
}

/** A Unix-millisecond timestamp as a date; "unknown" for 0. */
export function formatTimestamp(ms: number): string {
  return ms === 0 ? "unknown" : date.format(new Date(ms));
}

/** A local day, 2006-01-02, as a date. Parsed as local time: new Date("2006-01-02") would read it as UTC. */
export function formatDay(day: string): string {
  const [y, m, d] = day.split("-").map(Number);
  return date.format(new Date(y ?? 1970, (m ?? 1) - 1, d ?? 1));
}

export function formatMs(ms: number): string {
  return ms === 0 ? "n/a" : ms >= 1000 ? `${(ms / 1000).toFixed(1)} s` : `${integer.format(ms)} ms`;
}
