import { useQueryClient, type UseQueryResult } from "@tanstack/react-query";
import { useEffect, useMemo, useRef, useState, type ReactNode } from "react";
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
import { PromptReader, Sessions, useSessions } from "./features/sessions";
import { PromptReview } from "./features/prompts/PromptReview";
import { usePromptReview } from "./features/prompts/api";
import { SyncBanner, useSyncStatus } from "./features/sync";
import { Catalog, useCatalog } from "./features/catalog/Catalog";
import { Improvements } from "./features/improvements/Improvements";
import { Quotas, useQuotas } from "./features/quotas/Quotas";
import { Overview } from "./features/overview/Overview";
import { CostBreakdown } from "./features/report/CostBreakdown";
import { SessionRequests } from "./features/sessions/SessionRequests";
import { RelatedSessions } from "./features/overview/RelatedSessions";
import { readRelation, readView, relationKeys, useLocation, views, type Metric, type Relation, type View } from "./features/overview/navigation";

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
function DailyCostChart({ daily, select }: { daily: ReportData["daily"]; select?: (day: string) => void }) {
  const model = buildCostOverTime(daily);
  return model && <CostOverTime model={model} onDay={select} />;
}

/** A cost-by chart, on the cost scale it shares with the other one. */
function CostByChart({ title, tableName, groups, peers, select }: { title: string; tableName: string; groups: ReportData["byHarness"]; peers: ReportData["byHarness"][]; select?: (key: string) => void }) {
  const model = buildCostBy(groups, 10);
  const domainMax = sharedDomainMax(model, ...peers.map((g) => buildCostBy(g, 10)));
  return <CostBy title={title} tableName={tableName} model={model} domainMax={domainMax} onSelect={select} />;
}

function Report({ report, explore }: {
  report: UseQueryResult<ReportData, unknown>;
  explore: (relation: Relation) => void;
}) {
  if (report.isPending) return <p role="status">Loading the report…</p>;
  if (report.isError) {
    return <Failure title="The report could not be loaded." message={describe(report.error)} retry={() => void report.refetch()} />;
  }
  const r = report.data;
  const select = (kind: Relation["kind"]) => report.isPlaceholderData ? undefined : (value: string) => explore({ kind, value });
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
            {r.costBreakdown && <div className="lg:col-span-2"><CostBreakdown cost={r.costBreakdown} requests={r.total.requests} /></div>}
            <div className="lg:col-span-2">{zone("The cost over time chart", <DailyCostChart daily={r.daily} select={select("day")} />)}</div>
            {zone("The cost by harness chart", <CostByChart title="Estimated cost by coding agent" tableName="Harness" groups={r.byHarness} peers={[r.byModel]} select={select("harness")} />)}
            {zone("The cost by model chart", <CostByChart title="Cost by model" tableName="Model" groups={r.byModel} peers={[r.byHarness]} select={select("model")} />)}
          </div>
        </Section>
      )}
      {r.byHarness.length > 0 && (
        <Section id="by-harness" title="Harness">
          {zone("The harness table", <UsageTable caption="Usage by harness" label="Harness" groups={r.byHarness} onSelect={select("harness")} />)}
        </Section>
      )}
      {r.byModel.length > 0 && (
        <Section id="by-model" title="Model">
          {zone("The model table", <UsageTable caption="Usage by model" label="Model" groups={r.byModel} onSelect={select("model")} performance />)}
        </Section>
      )}
      {r.byProvider.length > 0 && (
        <Section id="by-provider" title="Provider">
          {zone("The provider table", <UsageTable caption="Usage by provider" label="Provider" groups={r.byProvider} performance />)}
        </Section>
      )}
      {r.byFolder.length > 0 && (
        <Section id="by-folder" title="Folder">
          {zone("The folder table", <UsageTable caption="Usage by folder" label="Folder" groups={r.byFolder} onSelect={select("folder")} />)}
        </Section>
      )}
      {r.byAgentType.length > 0 && (
        <Section id="by-agent" title="Agent">
          {zone("The agent table", <UsageTable caption="Usage by agent type" label="Agent type" groups={r.byAgentType} onSelect={select("agent")} />)}
        </Section>
      )}
      {r.tools.length > 0 && (
        <Section id="tools" title="Tools">
          {zone("The tools table", <ToolsTable tools={r.tools} onSelect={report.isPlaceholderData ? undefined : (tool) => explore({ kind: "tool", value: tool.name, server: tool.server })} />)}
        </Section>
      )}
    </div>
  );
}

const navIcons: Record<View, string> = { Overview: "▦", Usage: "▥", Sessions: "▤", Requests: "↔", Errors: "!", Prompts: "≡", Improvements: "↗", Catalog: "⊞", Quotas: "◴" };
const clearedRelation = Object.fromEntries(relationKeys.map((key) => [key, undefined]));
const clearedDetail = { session: undefined, prompt: undefined, revision: undefined, section: undefined };

export function App() {
  const meta = useMeta();
  const sync = useSyncStatus();
  const queryClient = useQueryClient();
  const { address, update, back } = useLocation();
  const [unused, setUnused] = useState(false);
  const trigger = useRef<string | undefined>(undefined);
  const heading = useRef<HTMLHeadingElement>(null);
  const available = views.filter((view) => view === "Catalog" ? !!meta.data?.catalogAvailable : view !== "Quotas" || !!meta.data?.quotasAvailable);
  const requestedView = readView(address);
  const view = available.includes(requestedView) ? requestedView : "Overview";
  const filters = useMemo(() => meta.data ? readFilters(meta.data, address) : undefined, [meta.data, address]);
  const project = filters?.scope === "current" ? (meta.data?.project ?? "") : "";
  const isOverview = view === "Overview";
  const isImprovements = view === "Improvements";
  const hasCatalog = !!meta.data?.catalogAvailable;
  const report = useReport(isOverview || view === "Usage" ? filters : undefined);
  const sessionID = address.get("session") || undefined;
  const position = Number(address.get("prompt"));
  const selectedPrompt = Number.isSafeInteger(position) && position > 0 ? position : undefined;
  const revision = address.get("revision") || undefined;
  const section = address.get("section") || undefined;
  const sessions = useSessions(isOverview || view === "Sessions" ? filters : undefined, project, isOverview ? 5 : 1000);
  const prompts = usePromptReview(isOverview || isImprovements || view === "Prompts" ? filters : undefined, project, isOverview || isImprovements ? 5 : 1000);
  const catalog = useCatalog(
    isOverview || view === "Catalog" || (isImprovements && hasCatalog) ? filters : undefined,
    project,
    isImprovements || (view === "Catalog" && unused),
  );
  const quotas = useQuotas(isOverview && !!meta.data?.quotasAvailable);
  const selection = readRelation(address);
  const rawMetric = address.get("metric");
  const metric: Metric = rawMetric === "requests" || rawMetric === "tokens" ? rawMetric : "cost";
  const shownScope = {
    Overview: report.data?.scope,
    Usage: report.data?.scope,
    Sessions: sessions.data?.scope,
    Requests: filters?.scope,
    Errors: filters?.scope,
    Prompts: prompts.data?.scope,
    Improvements: prompts.data?.scope ?? catalog.data?.scope,
    Catalog: catalog.data?.scope,
    Quotas: undefined,
  }[view];

  function selectView(next: View, focusHeading = true) {
    if (next !== view || sessionID || selection) {
      const previous = document.activeElement;
      update({ ...clearedDetail, ...clearedRelation, view: next === "Overview" ? undefined : next.toLowerCase() });
      if (focusHeading) window.requestAnimationFrame(() => {
        if (document.activeElement === previous || document.activeElement === document.body) {
          heading.current?.focus();
        }
      });
    }
  }

  function explore(relation: Relation) {
    trigger.current = document.activeElement?.id || undefined;
    update({ ...clearedRelation, ...relation, ...clearedDetail });
  }

  function closeSelection() {
    update(clearedRelation, true);
    window.requestAnimationFrame(() => {
      const el = trigger.current ? document.getElementById(trigger.current) : undefined;
      (el ?? heading.current)?.focus();
    });
  }

  function openSession(id: string) {
    update({ ...clearedDetail, view: "sessions", session: id });
  }

  function openPrompt(id: string, prompt: number, version?: string) {
    update({ session: id, prompt: String(prompt), revision: version });
  }

  function changeFilters(next: Filters) {
    update({ ...clearedRelation, ...clearedDetail, since: next.since, harness: next.harness, project: next.scope });
  }

  const canInspect = filters && selection && (view === "Overview" || view === "Usage" || view === "Catalog");
  const inspector = canInspect && <RelatedSessions filters={filters} project={project} selection={selection} close={closeSelection} open={openSession} />;

  // Refresh scoped reads once indexing has finished and project detection has settled.
  const syncVersion = sync.data?.dataVersion ?? sync.data?.finishedAt;
  useEffect(() => {
    if (syncVersion) {
      void queryClient.invalidateQueries({ queryKey: ["meta"] }).then(() => Promise.all([
        queryClient.invalidateQueries({ queryKey: ["report"] }),
        queryClient.invalidateQueries({ queryKey: ["sessions"] }),
        queryClient.invalidateQueries({ queryKey: ["prompts"] }),
        queryClient.invalidateQueries({ queryKey: ["catalog"] }),
      ]));
    }
  }, [syncVersion, queryClient]);
  return (
    <>
      <a href="#main" className="sr-only focus:not-sr-only focus:absolute focus:left-2 focus:top-2 focus:z-10 focus:rounded-md focus:bg-accent focus:px-3 focus:py-2 focus:text-accent-ink">
        Skip to main content
      </a>
      <div className="dashboard-workspace">
        <aside className="dashboard-sidebar">
          <button type="button" className="dashboard-brand" onClick={() => selectView("Overview", false)}>
            <span className="dashboard-mark" aria-hidden="true"><i /><i /><i /></span>agent-stats
          </button>
          <nav aria-label="Views" className="dashboard-nav">
            {available.map((name) => (
              <button key={name} type="button" aria-current={view === name ? "page" : undefined} aria-pressed={view === name}
                onClick={() => selectView(name, false)}>
                <span className="dashboard-nav-icon" aria-hidden="true" data-icon={navIcons[name]} />{name}
              </button>
            ))}
          </nav>
          <div className="dashboard-theme"><ThemeSelect /></div>
        </aside>
        <main id="main" tabIndex={-1} className="dashboard-main">
          <header className="dashboard-header">
            <div>
              <h1 ref={heading} tabIndex={-1}>{view}</h1>
              {meta.data && shownScope && <p>{scopeLabel(meta.data, shownScope)}</p>}
            </div>
            {meta.data && filters && view !== "Quotas" && <FilterBar meta={meta.data} value={filters} onChange={changeFilters} />}
          </header>
          <SyncBanner status={sync.data} unreachable={sync.isError} />
          {meta.isPending && <p role="status">Loading…</p>}
          {meta.isError && <Failure title="The server did not answer." message={describe(meta.error)} retry={() => void meta.refetch()} />}
          {meta.data && filters && (
            <>
              {isOverview && <Overview report={report} sessions={sessions} prompts={prompts} catalog={catalog} quotas={quotas}
                catalogAvailable={!!meta.data.catalogAvailable} quotasAvailable={!!meta.data.quotasAvailable}
                metric={metric} setMetric={(next) => update({ metric: next === "cost" ? undefined : next }, true)}
                explore={explore} selectView={selectView} openSession={openSession} inspector={inspector}
                selectedDay={selection?.kind === "day" ? selection.value : undefined} />}
              {view === "Usage" && <>{inspector}<Report report={report} explore={explore} /></>}
              {view === "Sessions" && <Sessions filters={filters} project={project} list={sessions} selected={sessionID} prompt={selectedPrompt}
                revision={revision} section={section} onSelect={openSession} onPrompt={openPrompt}
                onSection={(next) => update({ section: next }, true)}
                onBack={() => back(selectedPrompt ? { prompt: undefined, revision: undefined } : clearedDetail)} />}
              {(view === "Requests" || view === "Errors") && <SessionRequests key={JSON.stringify([view, project, filters])} filters={filters} project={project} errorsOnly={view === "Errors"}
                openSession={(id) => update({ ...clearedDetail, view: "sessions", session: id, section: "Requests" })} />}
              {view === "Prompts" && (sessionID && selectedPrompt
                ? <PromptReader id={sessionID} position={selectedPrompt} revision={revision} filters={filters} project={project}
                    onBack={() => back(clearedDetail)} onSession={openSession} backLabel="Back to prompt review" />
                : <PromptReview query={prompts} onSession={openSession} onPrompt={openPrompt} />)}
              {view === "Catalog" && <>{inspector}<Catalog query={catalog} unused={unused} onUnusedChange={setUnused}
                onServer={(entry) => explore({ kind: "mcp", value: entry.name, itemHarness: entry.harness, itemScope: entry.scope, itemOrigin: entry.originId })} /></>}
              {view === "Quotas" && <Quotas />}
              {isImprovements && <Improvements prompts={prompts} catalog={hasCatalog ? catalog : undefined}
                openPrompts={() => selectView("Prompts")}
                openCatalog={() => {
                  setUnused(true);
                  selectView("Catalog");
                }} />}
            </>
          )}
        </main>
      </div>
    </>
  );
}
