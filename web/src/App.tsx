import { useQueryClient } from "@tanstack/react-query";
import { useEffect, useState, type ReactNode } from "react";
import { ErrorBoundary } from "./shared/ErrorBoundary";
import { useTheme, type ThemeChoice } from "./shared/theme";
import { useMeta, useReport } from "./features/report/api";
import { CostBy, CostOverTime } from "./features/report/CostCharts";
import { FilterBar } from "./features/report/Filters";
import { ToolsTable, UsageTable } from "./features/report/Tables";
import { Totals } from "./features/report/Totals";
import type { Filters, Meta } from "./features/report/types";
import { useSyncStatus } from "./features/sync/api";
import { SyncBanner } from "./features/sync/SyncBanner";

/** Filters live in the address (?since=7d&harness=claude), so a reload or a bookmark keeps them. */
function readFilters(meta: Meta): Filters {
  const params = new URLSearchParams(window.location.search);
  const harness = params.get("harness") ?? meta.defaults.harness;
  return {
    since: params.get("since") ?? meta.defaults.since,
    harness: harness === "" || meta.harnesses.includes(harness) ? harness : meta.defaults.harness,
  };
}

function writeFilters(f: Filters) {
  const params = new URLSearchParams();
  params.set("since", f.since);
  params.set("harness", f.harness);
  window.history.replaceState(null, "", `?${params}`);
}

/** What to tell the person about a failed request. */
function describe(error: unknown): string {
  return error instanceof Error ? error.message : "Unknown error.";
}

function Failure({ title, message, retry }: { title: string; message: string; retry: () => void }) {
  return (
    <div role="alert" className="rounded-lg border border-danger p-4">
      <p className="font-semibold text-danger">{title}</p>
      <p className="mt-1 text-sm">{message}</p>
      <button type="button" onClick={retry} className="mt-3 min-h-9 rounded-md border border-border px-3 text-sm hover:bg-surface">
        Try again
      </button>
    </div>
  );
}

function ThemeSelect() {
  const [theme, setTheme] = useTheme();
  return (
    <div className="flex items-center gap-2 text-sm">
      <label htmlFor="theme" className="text-muted">Theme</label>
      <select
        id="theme"
        value={theme}
        onChange={(e) => setTheme(e.target.value as ThemeChoice)}
        className="min-h-9 rounded-md border border-border bg-bg px-2 text-ink"
      >
        <option value="system">System</option>
        <option value="light">Light</option>
        <option value="dark">Dark</option>
      </select>
    </div>
  );
}

function Section({ id, title, children }: { id: string; title: string; children: ReactNode }) {
  return (
    <section aria-labelledby={id} className="flex flex-col gap-3">
      <h2 id={id} className="text-lg font-semibold">{title}</h2>
      {children}
    </section>
  );
}

function Report({ filters }: { filters: Filters }) {
  const report = useReport(filters);
  const queryClient = useQueryClient();
  const sync = useSyncStatus();

  // The figures were read from a partial index while it ran: read them again when it ends.
  const syncState = sync.data?.state;
  useEffect(() => {
    if (syncState === "done") void queryClient.invalidateQueries({ queryKey: ["report"] });
  }, [syncState, queryClient]);

  if (report.isPending) return <p role="status">Loading the report…</p>;
  if (report.isError) {
    return <Failure title="The report could not be loaded." message={describe(report.error)} retry={() => void report.refetch()} />;
  }
  const r = report.data;
  if (r.total.requests === 0) return <Totals total={r.total} />; // nothing to chart or tabulate
  return (
    <div aria-busy={report.isFetching} className="flex flex-col gap-8">
      <Totals total={r.total} />
      <Section id="charts" title="Cost">
        <ErrorBoundary fallback={(retry) => <Failure title="The charts could not be drawn." message="The tables below still hold the figures." retry={retry} />}>
          <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
            <div className="lg:col-span-2">
              <CostOverTime daily={r.daily} />
            </div>
            <CostBy title="Cost by harness" tableName="Harness" groups={r.byHarness} limit={10} />
            <CostBy title="Cost by model" tableName="Model" groups={r.byModel} limit={10} />
          </div>
        </ErrorBoundary>
      </Section>
      <Section id="by-harness" title="Harness">
        <UsageTable caption="Usage by harness" label="Harness" groups={r.byHarness} />
      </Section>
      <Section id="by-model" title="Model">
        <UsageTable caption="Usage by model" label="Model" groups={r.byModel} />
      </Section>
      <Section id="by-folder" title="Folder">
        <UsageTable caption="Usage by folder" label="Folder" groups={r.byFolder} />
      </Section>
      <Section id="by-agent" title="Agent">
        <UsageTable caption="Usage by agent type" label="Agent type" groups={r.byAgentType} />
      </Section>
      <Section id="tools" title="Tools">
        <ToolsTable tools={r.tools} />
      </Section>
    </div>
  );
}

function Filtered({ meta }: { meta: Meta }) {
  const [filters, setFilters] = useState<Filters>(() => readFilters(meta));
  useEffect(() => writeFilters(filters), [filters]);
  return (
    <>
      <FilterBar meta={meta} value={filters} onChange={setFilters} />
      <Report filters={filters} />
    </>
  );
}

export function App() {
  const meta = useMeta();
  return (
    <>
      <a
        href="#main"
        className="sr-only focus:not-sr-only focus:absolute focus:left-2 focus:top-2 focus:z-10 focus:rounded-md focus:bg-accent focus:px-3 focus:py-2 focus:text-accent-ink"
      >
        Skip to the report
      </a>
      <div className="mx-auto flex max-w-7xl flex-col gap-6 px-4 py-6">
        <header className="flex flex-wrap items-center justify-between gap-3">
          <div>
            <h1 className="text-2xl font-bold">agent-stats</h1>
            <p className="text-sm text-muted">Usage of your coding agents, read from the session logs on this machine.</p>
          </div>
          <ThemeSelect />
        </header>
        <SyncBanner />
        <main id="main" tabIndex={-1} className="flex flex-col gap-6 outline-none">
          {meta.isPending && <p role="status">Loading…</p>}
          {meta.isError && <Failure title="The server did not answer." message={describe(meta.error)} retry={() => void meta.refetch()} />}
          {meta.data && <Filtered meta={meta.data} />}
        </main>
      </div>
    </>
  );
}
