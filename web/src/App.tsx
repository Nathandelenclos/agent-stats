import { useQueryClient } from "@tanstack/react-query";
import { useEffect, useState, type ReactNode } from "react";
import { ErrorBoundary } from "./shared/ErrorBoundary";
import { useTheme, type ThemeChoice } from "./shared/theme";
import { ApiError } from "./shared/api";
import {
  buildCostBy,
  buildCostOverTime,
  sharedDomainMax,
  CostBy,
  CostOverTime,
  FilterBar,
  ToolsTable,
  Totals,
  UsageTable,
  useMeta,
  useReport,
  type Filters,
  type Meta,
  type Report as ReportData,
} from "./features/report";
import { SyncBanner, useSyncStatus, type SyncStatus } from "./features/sync";

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
  // The server words its own refusals for people; anything else (network, unreadable answer) gets a plain sentence.
  return error instanceof ApiError ? error.message : "The page could not reach the agent-stats server. Check that it is still running, then try again.";
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

/**
 * One zone of the report. A failure while drawing it stays in it, and the arrival of other data tries it again. The zone
 * is not remounted when the filters change, so a table keeps its sort and its expansion while the previous report stays on screen.
 */
function Zone({ name, data, children }: { name: string; data: ReportData; children: ReactNode }) {
  return (
    <ErrorBoundary
      resetKey={data}
      fallback={(retry) => <Failure title={`${name} could not be drawn.`} message="The rest of the page is not affected." retry={retry} />}
    >
      {children}
    </ErrorBoundary>
  );
}

// The series are built inside the zone that draws them, so that a failure while building one stays in that zone.
function DailyCostChart({ daily }: { daily: ReportData["daily"] }) {
  const model = buildCostOverTime(daily);
  return model && <CostOverTime model={model} />;
}

/** A cost-by chart, on the cost scale it shares with the other one. */
function CostByChart({ title, tableName, groups, peers }: { title: string; tableName: string; groups: ReportData["byHarness"]; peers: ReportData["byHarness"][] }) {
  const model = buildCostBy(groups, 10);
  const domainMax = sharedDomainMax(model, ...peers.map((g) => buildCostBy(g, 10)));
  return <CostBy title={title} tableName={tableName} model={model} domainMax={domainMax} />;
}

function Report({ filters, syncState }: { filters: Filters; syncState: SyncStatus["state"] | undefined }) {
  const report = useReport(filters);
  const queryClient = useQueryClient();

  // The figures were read from a partial index while it ran: read them again when it ends.
  useEffect(() => {
    if (syncState === "done") void queryClient.invalidateQueries({ queryKey: ["report"] });
  }, [syncState, queryClient]);

  if (report.isPending) return <p role="status">Loading the report…</p>;
  if (report.isError) {
    return <Failure title="The report could not be loaded." message={describe(report.error)} retry={() => void report.refetch()} />;
  }
  const r = report.data;
  const zone = (name: string, children: ReactNode) => <Zone name={name} data={r}>{children}</Zone>;
  // Each zone is shown according to its own data: a period can hold tool calls and no model request (the last request was
  // before it, a tool call after). The totals tell when there is no request; a zone with nothing to list is left out.
  const hasCharts = r.daily.length > 0 || r.byHarness.length > 0 || r.byModel.length > 0;
  return (
    <div aria-busy={report.isFetching} className="flex flex-col gap-8">
      {zone("The totals", <Totals total={r.total} />)}
      {hasCharts && (
        <Section id="charts" title="Cost">
          <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
            <div className="lg:col-span-2">{zone("The cost over time chart", <DailyCostChart daily={r.daily} />)}</div>
            {zone("The cost by harness chart", <CostByChart title="Cost by harness" tableName="Harness" groups={r.byHarness} peers={[r.byModel]} />)}
            {zone("The cost by model chart", <CostByChart title="Cost by model" tableName="Model" groups={r.byModel} peers={[r.byHarness]} />)}
          </div>
        </Section>
      )}
      {r.byHarness.length > 0 && (
        <Section id="by-harness" title="Harness">
          {zone("The harness table", <UsageTable caption="Usage by harness" label="Harness" groups={r.byHarness} />)}
        </Section>
      )}
      {r.byModel.length > 0 && (
        <Section id="by-model" title="Model">
          {zone("The model table", <UsageTable caption="Usage by model" label="Model" groups={r.byModel} />)}
        </Section>
      )}
      {r.byFolder.length > 0 && (
        <Section id="by-folder" title="Folder">
          {zone("The folder table", <UsageTable caption="Usage by folder" label="Folder" groups={r.byFolder} />)}
        </Section>
      )}
      {r.byAgentType.length > 0 && (
        <Section id="by-agent" title="Agent">
          {zone("The agent table", <UsageTable caption="Usage by agent type" label="Agent type" groups={r.byAgentType} />)}
        </Section>
      )}
      {r.tools.length > 0 && (
        <Section id="tools" title="Tools">
          {zone("The tools table", <ToolsTable tools={r.tools} />)}
        </Section>
      )}
    </div>
  );
}

function Filtered({ meta, syncState }: { meta: Meta; syncState: SyncStatus["state"] | undefined }) {
  const [filters, setFilters] = useState<Filters>(() => readFilters(meta));
  useEffect(() => writeFilters(filters), [filters]);
  return (
    <>
      <FilterBar meta={meta} value={filters} onChange={setFilters} />
      <Report filters={filters} syncState={syncState} />
    </>
  );
}

export function App() {
  const meta = useMeta();
  const sync = useSyncStatus();
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
        <SyncBanner status={sync.data} unreachable={sync.isError} />
        <main id="main" tabIndex={-1} className="flex flex-col gap-6">
          {meta.isPending && <p role="status">Loading…</p>}
          {meta.isError && <Failure title="The server did not answer." message={describe(meta.error)} retry={() => void meta.refetch()} />}
          {meta.data && <Filtered meta={meta.data} syncState={sync.data?.state} />}
        </main>
      </div>
    </>
  );
}
