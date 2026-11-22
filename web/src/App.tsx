import { useQueryClient, type UseQueryResult } from "@tanstack/react-query";
import { useEffect, useMemo, useState, type ReactNode } from "react";
import { ErrorBoundary } from "./shared/ErrorBoundary";
import { useTheme, type ThemeChoice } from "./shared/theme";
import { describe, Failure } from "./shared/Failure";
import {
  buildCostBy,
  buildCostOverTime,
  sharedDomainMax,
  CostBy,
  CostOverTime,
  FilterBar,
  scopeLabel,
  ToolsTable,
  Totals,
  UsageTable,
  useMeta,
  useReport,
  type Filters,
  type Meta,
  type Report as ReportData,
} from "./features/report";
import { Sessions, useSessions } from "./features/sessions";
import { PromptReview } from "./features/prompts/PromptReview";
import { usePromptReview } from "./features/prompts/api";
import { SyncBanner, useSyncStatus } from "./features/sync";

/** Filters live in the address (?since=7d&harness=claude&project=all), so a reload or a bookmark keeps them. */
function readFilters(meta: Meta, params: URLSearchParams): Filters {
  const harness = params.get("harness") ?? meta.defaults.harness;
  const scope = params.get("project"); // the address and the API call the scope "project"
  return {
    since: params.get("since") ?? meta.defaults.since,
    harness: harness === "" || meta.harnesses.includes(harness) ? harness : meta.defaults.harness,
    // "current" names a project only a server that found one can answer for: with none, the server refuses it.
    scope: scope === "all" || (scope === "current" && meta.project !== "") ? scope : meta.defaults.scope,
  };
}

function writeFilters(f: Filters) {
  const params = new URLSearchParams();
  params.set("since", f.since);
  params.set("harness", f.harness);
  params.set("project", f.scope);
  window.history.replaceState(null, "", `?${params}`);
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

function Report({ report }: { report: UseQueryResult<ReportData, unknown> }) {
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
    // The figures of the previous filters stay while the new ones load: dimmed, they are shown as outdated.
    <div aria-busy={report.isFetching} className={`flex flex-col gap-8 transition-opacity ${report.isPlaceholderData ? "opacity-60" : ""}`}>
      {zone("The totals", <Totals total={r.total} />)}
      {hasCharts && (
        <Section id="charts" title="Cost">
          <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
            <div className="lg:col-span-2">{zone("The cost over time chart", <DailyCostChart daily={r.daily} />)}</div>
            {zone("The cost by harness chart", <CostByChart title="Estimated cost by coding agent" tableName="Harness" groups={r.byHarness} peers={[r.byModel]} />)}
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

type View = "Usage" | "Sessions" | "Prompts";

const views: View[] = ["Usage", "Sessions", "Prompts"];

export function App() {
  const meta = useMeta();
  const sync = useSyncStatus();
  const queryClient = useQueryClient();
  // The filters are the person's once they change them, and what the server and the address say until then. The address is
  // read once, as it was when the page opened: the filters written to it since are not the person's choice.
  const [view, setView] = useState<View>("Usage");
  const [chosen, setChosen] = useState<Filters>();
  const [address] = useState(() => new URLSearchParams(window.location.search));
  const initial = useMemo(() => (meta.data ? readFilters(meta.data, address) : undefined), [meta.data, address]);
  const filters = chosen ?? initial;
  useEffect(() => {
    if (filters) writeFilters(filters);
  }, [filters]);
  const report = useReport(view === "Usage" ? filters : undefined);
  const project = filters?.scope === "current" ? (meta.data?.project ?? "") : "";
  const sessions = useSessions(view === "Sessions" ? filters : undefined, project);
  const prompts = usePromptReview(view === "Prompts" ? filters : undefined, project);
  const shownScope = { Usage: report.data?.scope, Sessions: sessions.data?.scope, Prompts: prompts.data?.scope }[view];

  // The figures were read from a partial index while it ran, and the server may have learned its project from it: ask again when it ends.
  const syncState = sync.data?.state;
  const syncFinishedAt = sync.data?.finishedAt;
  useEffect(() => {
    if (syncState === "done" || syncState === "failed") {
      void queryClient.invalidateQueries({ queryKey: ["meta"] }).then(() => Promise.all([
        queryClient.invalidateQueries({ queryKey: ["report"] }),
        queryClient.invalidateQueries({ queryKey: ["sessions"] }),
        queryClient.invalidateQueries({ queryKey: ["prompts"] }),
      ]));
    }
  }, [syncState, syncFinishedAt, queryClient]);
  return (
    <>
      <a
        href="#main"
        className="sr-only focus:not-sr-only focus:absolute focus:left-2 focus:top-2 focus:z-10 focus:rounded-md focus:bg-accent focus:px-3 focus:py-2 focus:text-accent-ink"
      >
        Skip to main content
      </a>
      <div className="mx-auto flex max-w-7xl flex-col gap-6 px-4 py-6">
        <header className="flex flex-wrap items-center justify-between gap-3">
          <div>
            <h1 className="text-2xl font-bold">agent-stats</h1>
            <p className="text-sm text-muted">Usage of your coding agents, read from the session logs on this machine.</p>
            {meta.data && shownScope && <p className="mt-1 text-sm font-semibold">{scopeLabel(meta.data, shownScope)}</p>}
          </div>
          <ThemeSelect />
        </header>
        <SyncBanner status={sync.data} unreachable={sync.isError} />
        <div className="grid grid-cols-1 gap-6 lg:grid-cols-[10rem_minmax(0,1fr)] lg:items-start">
          <nav
            aria-label="Views"
            className="flex flex-wrap gap-1 border-b border-rule pb-2 lg:sticky lg:top-6 lg:flex-col lg:border-r lg:border-b-0 lg:pr-3 lg:pb-0"
          >
            {views.map((name) => (
              <button
                key={name}
                type="button"
                aria-pressed={view === name}
                className={`min-h-11 rounded-sm border-l-4 px-3 text-left text-sm ${view === name ? "border-accent bg-surface font-semibold text-ink" : "border-transparent text-muted hover:bg-surface hover:text-ink"}`}
                onClick={() => setView(name)}
              >
                {name}
              </button>
            ))}
          </nav>
          <main id="main" tabIndex={-1} className="flex min-w-0 flex-col gap-6">
            {meta.isPending && <p role="status">Loading…</p>}
            {meta.isError && <Failure title="The server did not answer." message={describe(meta.error)} retry={() => void meta.refetch()} />}
            {meta.data && filters && (
              <>
                <FilterBar meta={meta.data} value={filters} onChange={setChosen} />
                {view === "Usage" && <Report report={report} />}
                {view === "Sessions" && <Sessions filters={filters} project={project} list={sessions} />}
                {view === "Prompts" && <PromptReview query={prompts} />}
              </>
            )}
          </main>
        </div>
      </div>
    </>
  );
}
