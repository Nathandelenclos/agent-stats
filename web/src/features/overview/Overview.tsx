import type { ReactNode } from "react";
import type { UseQueryResult } from "@tanstack/react-query";
import { describe, Failure } from "../../shared/Failure";
import { formatDateTime, formatInteger, formatUSD } from "../../shared/format";
import type { Filters, Report, UsageGroup } from "../report";
import type { useSessions } from "../sessions/api";
import type { usePromptReview } from "../prompts/api";
import type { useCatalog } from "../catalog/Catalog";
import { QuotaOverview, type useQuotas } from "../quotas/Quotas";
import { ActivityChart } from "./ActivityChart";
import { RecentRequests, TokenMix } from "./OverviewDetails";
import { OverviewMetrics } from "./OverviewMetrics";
import { SessionRows } from "./RelatedSessions";
import type { Metric, Relation, View } from "./navigation";

function Card({ title, action, children }: { title: string; action?: ReactNode; children: ReactNode }) {
  return <section className="dashboard-panel">
    <div className="dashboard-panel-heading"><h2>{title}</h2>{action}</div>
    {children}
  </section>;
}

function Browse({ title, open }: { title: string; open: () => void }) {
  return <button type="button" className="dashboard-link" onClick={open}>{title} <span aria-hidden="true">›</span></button>;
}

function Preview({ query, updating, children }: {
  query: { isPending: boolean; isError: boolean };
  updating: boolean;
  children: ReactNode;
}) {
  if (query.isPending || updating) return <p role="status" className="text-sm text-muted">Loading…</p>;
  if (query.isError) return <p className="text-sm text-muted">This preview is unavailable.</p>;
  return children;
}

function Rank({ groups, explore }: { groups: UsageGroup[]; explore?: (relation: Relation) => void }) {
  const peak = Math.max(0, ...groups.map((g) => g.cost));
  if (groups.length === 0) return <p className="text-sm text-muted">No model requests were recorded for these filters. Check Sessions for indexed conversations.</p>;
  return <ul role="list" aria-label="Estimated cost by coding agent" className="dashboard-rank">
    {groups.slice(0, 5).map((group) => {
      const name = ({ claude: "Claude Code", codex: "Codex" } as Record<string, string>)[group.key] ?? group.key;
      const cost = group.requests > 0 && group.requests === group.unpriced ? "Unavailable" : formatUSD(group.cost);
      return <li key={group.key}>
        {explore ? <button type="button" className="dashboard-link" id={`agent-${group.key}`}
          onClick={() => explore({ kind: "harness", value: group.key })}>{name}</button> : <span>{name}</span>}
        <span className="dashboard-meter" aria-hidden="true"><span style={{ width: `${peak > 0 ? 100 * group.cost / peak : 0}%` }} /></span>
        <strong className="tabular-nums">{cost}</strong>
      </li>;
    })}
  </ul>;
}

export function Overview({
  report, sessions, prompts, catalog, quotas, catalogAvailable, quotasAvailable, metric, setMetric,
  explore, selectView, openSession, openRequestSession, filters, project, selectedDay, inspector,
}: {
  report: UseQueryResult<Report, unknown>;
  sessions: ReturnType<typeof useSessions>;
  prompts: ReturnType<typeof usePromptReview>;
  catalog: ReturnType<typeof useCatalog>;
  quotas: ReturnType<typeof useQuotas>;
  catalogAvailable: boolean;
  quotasAvailable: boolean;
  metric: Metric;
  setMetric: (metric: Metric) => void;
  explore: (relation: Relation) => void;
  selectView: (view: View) => void;
  openSession: (id: string) => void;
  openRequestSession: (id: string) => void;
  filters: Filters;
  project: string;
  selectedDay?: string;
  inspector?: ReactNode;
}) {
  if (report.isPending) return <p role="status">Loading the overview…</p>;
  if (report.isError) return <Failure title="The overview could not be loaded." message={describe(report.error)} retry={() => void report.refetch()} />;
  const { total, daily, hourly, byHarness, tools } = report.data;
  const canExplore = report.isPlaceholderData ? undefined : explore;
  const entries = catalog.data?.entries ?? [];
  const recent = sessions.data?.sessions ?? [];
  const recurring = prompts.data?.recurring[0];
  const errors = tools.filter((tool) => tool.errors > 0).sort((a, b) => b.errors - a.errors).slice(0, 3);
  return <div aria-busy={report.isFetching} className={`dashboard-overview${report.isPlaceholderData ? " opacity-60" : ""}`}>
    <OverviewMetrics total={total} periods={hourly?.length ? hourly : daily} />

    <div className={`dashboard-chart-grid${inspector ? " dashboard-chart-grid-active" : ""}`}>
      <ActivityChart daily={daily} hourly={hourly} metric={metric} onMetric={setMetric} selectedDay={selectedDay}
        onDay={canExplore ? (day) => canExplore({ kind: "day", value: day }) : undefined} />
      {inspector}
    </div>

    <div className="dashboard-card-grid">
      <TokenMix usage={total} />
      <Card title="Cost by agent" action={<Browse title="Usage" open={() => selectView("Usage")} />}><Rank groups={byHarness} explore={canExplore} /></Card>
      {quotasAvailable && <Card title="Account quotas" action={<Browse title="Quotas" open={() => selectView("Quotas")} />}><QuotaOverview query={quotas} /></Card>}
      <Card title="Recent sessions" action={<Browse title="Sessions" open={() => selectView("Sessions")} />}>
        <Preview query={sessions} updating={report.isPlaceholderData}>
          {recent.length > 0 ? <SessionRows sessions={recent.slice(0, 3)} idPrefix="recent-session" open={openSession} /> : <p className="text-sm text-muted">No sessions match this period.</p>}
        </Preview>
      </Card>
      <Card title="Prompt review" action={<Browse title="Prompts" open={() => selectView("Prompts")} />}>
        <Preview query={prompts} updating={report.isPlaceholderData}>
          <p className="text-sm"><strong>{formatInteger(prompts.data?.count ?? 0)}</strong> flagged prompts</p>
          {recurring && <div className="mt-2 text-sm">
            {recurring.excerpt && <p className="dashboard-excerpt">{recurring.excerpt}</p>}
            <p className="text-xs text-muted">{formatInteger(recurring.count)} occurrences in {formatInteger(recurring.sessions)} sessions</p>
          </div>}
        </Preview>
      </Card>
      {errors.length > 0 && <Card title="Tool errors">
        <ul className="dashboard-rows">
          {errors.map((tool) => <li key={JSON.stringify([tool.server, tool.name])}>
            <button className="dashboard-row" type="button" disabled={!canExplore}
              onClick={() => canExplore?.({ kind: "tool", value: tool.name, server: tool.server })}>
              <span className="min-w-0"><strong>{tool.name}</strong><small>{tool.server || "Built-in"} · {formatDateTime(tool.lastTs)}</small></span>
              <strong className="tabular-nums">{formatInteger(tool.errors)} errors</strong>
            </button>
          </li>)}
        </ul>
      </Card>}
      {catalogAvailable && <Card title="Catalog" action={<Browse title="Catalog" open={() => selectView("Catalog")} />}>
        <Preview query={catalog} updating={report.isPlaceholderData}>
          <div className="dashboard-counts">
            {([["skill", "skills"], ["agent", "agents"], ["mcp", "MCP servers"]] as const).map(([kind, label]) =>
              <span key={kind}><strong>{entries.filter((entry) => entry.kind === kind).length}</strong> {label}</span>)}
          </div>
          <p className="dashboard-caption">{entries.filter((entry) => entry.uses === 0 && entry.enabled && entry.scope !== "logs").length} tracked entries with zero observed use.</p>
        </Preview>
      </Card>}
    </div>
    {!report.isPlaceholderData && <RecentRequests filters={filters} project={project} open={openRequestSession} browse={() => selectView("Requests")} />}
  </div>;
}
