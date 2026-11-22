import { useQuery } from "@tanstack/react-query";
import { getJSON } from "../../shared/api";
import { describe, Failure } from "../../shared/Failure";
import { formatDateTime, formatPercent } from "../../shared/format";

const defaultProviders = ["Codex", "Claude Code"];

export interface QuotaWindow {
  provider: string;
  label: string;
  remaining: number;
  resetAt: string;
  observedAt: string;
}

function knownTime(value: string): number {
  const time = Date.parse(value);
  return time > 0 ? time : 0;
}

function quotaPercent(remaining: number): string {
  if (remaining > 0 && remaining < 0.5) return "<1%";
  if (remaining < 100 && remaining >= 99.5) return ">99%";
  return formatPercent(remaining / 100);
}

function QuotaRow({ window }: { window: QuotaWindow }) {
  return (
    <li className="flex min-w-0 flex-col gap-2 rounded-lg border border-rule bg-surface p-3">
      <div className="flex flex-wrap items-baseline justify-between gap-x-3">
        <span className="break-words font-medium">{window.label}</span>
        <strong className="tabular-nums">{quotaPercent(window.remaining)} remaining</strong>
      </div>
      <div aria-hidden="true" className="h-2 overflow-hidden rounded-full bg-rule">
        <span className="block h-full rounded-full bg-[var(--mark-2)]" style={{ width: window.remaining + "%" }} />
      </div>
      <dl className="flex flex-wrap gap-x-5 gap-y-1 text-sm text-muted">
        <div className="flex flex-wrap gap-x-1">
          <dt>Resets</dt>
          <dd>{formatDateTime(knownTime(window.resetAt))}</dd>
        </div>
        <div className="flex flex-wrap gap-x-1">
          <dt>Checked</dt>
          <dd>{formatDateTime(knownTime(window.observedAt))}</dd>
        </div>
      </dl>
    </li>
  );
}

export function useQuotas(enabled: boolean) {
  return useQuery({
    queryKey: ["quotas"],
    enabled,
    queryFn: ({ signal }) => getJSON<QuotaWindow[]>("/api/quotas", {}, signal),
    gcTime: 0,
    staleTime: Infinity,
    refetchOnWindowFocus: false,
    refetchOnReconnect: false,
    retry: false,
  });
}

function liveWindows(windows: QuotaWindow[]) {
  const now = Date.now();
  return windows.filter((window) => {
    const reset = knownTime(window.resetAt);
    return window.remaining >= 0 && window.remaining <= 100 && (reset === 0 || reset > now);
  });
}

function providers(windows: QuotaWindow[]) {
  return [...new Set([...defaultProviders, ...windows.map((window) => window.provider)])];
}

export function QuotaOverview({ query }: { query: ReturnType<typeof useQuotas> }) {
  if (query.isPending) return <p role="status">Checking account quotas…</p>;
  if (query.isError) return <p className="text-sm text-muted">Quota readings unavailable.</p>;
  const windows = liveWindows(query.data ?? []);
  return <ul className="flex flex-col gap-3">
    {providers(windows).map((provider) => {
      const current = windows.filter((window) => window.provider === provider).sort((a, b) => a.remaining - b.remaining)[0];
      return <li key={provider}>
        <div className="flex flex-wrap justify-between gap-x-3 text-sm">
          <strong>{provider}</strong>
          <span className={current && current.remaining <= 10 ? "dashboard-low" : "text-muted"}>
            {current ? `${quotaPercent(current.remaining)} remaining${current.remaining <= 10 ? " · Low" : ""}` : "Unavailable"}
          </span>
        </div>
        {current && <>
          <div className="dashboard-meter" aria-hidden="true">
            <span style={{ width: current.remaining + "%", background: current.remaining <= 10 ? "var(--mark-2)" : "var(--mark)" }} />
          </div>
          <p className="text-xs text-muted">{current.label} · Resets {formatDateTime(knownTime(current.resetAt))}</p>
        </>}
      </li>;
    })}
  </ul>;
}

export function Quotas() {
  const query = useQuotas(true);
  const windows = liveWindows(query.data ?? []);
  return (
    <section aria-labelledby="quotas-title" className="flex min-w-0 flex-col gap-4">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <h2 id="quotas-title" className="text-lg font-semibold">Account quotas</h2>
        <button
          type="button"
          className="min-h-9 rounded-md border border-border px-3 text-sm hover:bg-surface aria-disabled:opacity-60"
          aria-disabled={query.isFetching}
          onClick={() => {
            if (!query.isFetching) void query.refetch();
          }}
        >
          Refresh quotas
        </button>
      </div>
      {query.isFetching && <p role="status">Checking account quotas…</p>}
      {query.isError && <Failure title="Quotas could not be loaded." message={describe(query.error)} retry={() => void query.refetch()} />}
      {query.isSuccess && providers(windows).map((provider) => {
        const current = windows.filter((window) => window.provider === provider);
        return (
          <div key={provider} className="flex min-w-0 flex-col gap-2">
            <h3 className="font-semibold">{provider}</h3>
            {current.length === 0 ? (
              <p className="text-sm text-muted">Quota unavailable.</p>
            ) : (
              <ul role="list" className="flex min-w-0 flex-col gap-2">
                {current.map((window, position) => <QuotaRow key={position} window={window} />)}
              </ul>
            )}
          </div>
        );
      })}
    </section>
  );
}
