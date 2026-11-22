import { DataTable, type Column } from "../../shared/DataTable";
import { describe, Failure } from "../../shared/Failure";
import { formatDateTime, formatInteger } from "../../shared/format";
import { describeSignals } from "../report/signals";
import type { PromptReviewData, usePromptReview } from "./api";

type Folder = PromptReviewData["byFolder"][number];
type Flagged = PromptReviewData["prompts"][number] & { tableIndex: number };
type Ask = PromptReviewData["recurring"][number] & { position: number };

const folderColumns: Column<Folder>[] = [
  { id: "folder", header: "Folder", value: (f) => f.folder || "Unknown", truncate: true },
  { id: "prompts", header: "Prompts", value: (f) => f.prompts, render: (f) => formatInteger(f.prompts), numeric: true },
  { id: "sessions", header: "Sessions", value: (f) => f.sessions, render: (f) => formatInteger(f.sessions), numeric: true },
  { id: "signals", header: "Signals", value: (f) => f.signals, render: (f) => formatInteger(f.signals), numeric: true },
];

function recurringColumns(excerpts: boolean): Column<Ask>[] {
  const columns: Column<Ask>[] = [
    { id: "sessions", header: "Sessions", value: (a) => a.sessions, render: (a) => formatInteger(a.sessions), numeric: true },
    { id: "count", header: "Occurrences", value: (a) => a.count, render: (a) => formatInteger(a.count), numeric: true },
    { id: "last", header: "Last seen", value: (a) => a.lastTs, render: (a) => formatDateTime(a.lastTs) },
  ];
  if (excerpts) columns.unshift({ id: "ask", header: "Request", value: (a) => a.excerpt ?? "" });
  return columns;
}

interface Actions {
  onSession?: (id: string) => void;
  onPrompt?: (id: string, position: number, revision?: string) => void;
}

const rowAction = "min-h-9 max-w-full text-left font-medium text-accent [overflow-wrap:anywhere] hover:underline";

function flaggedColumns(excerpts: boolean, { onSession, onPrompt }: Actions): Column<Flagged>[] {
  const columns: Column<Flagged>[] = [
    { id: "signals", header: "Signals", value: (p) => describeSignals(p.signals) },
    {
      id: "title",
      header: "Session",
      value: (p) => p.title || p.session,
      render: onSession ? (p) => (
        <button type="button" className={rowAction} onClick={() => onSession(p.session)}>{p.title || p.session}</button>
      ) : undefined,
      truncate: true,
    },
    { id: "folder", header: "Folder", value: (p) => p.folder || "Unknown", truncate: true },
    { id: "harness", header: "Harness", value: (p) => p.harness },
    { id: "ts", header: "Time", value: (p) => p.ts, render: (p) => formatDateTime(p.ts) },
  ];
  if (excerpts) columns.unshift({
    id: "prompt",
    header: "Prompt",
    value: (p) => p.excerpt ?? "",
    truncate: true,
    render: (p) => {
      const position = p.position;
      if (position === undefined || !onPrompt) return p.excerpt ?? "";
      return (
        <button type="button" className={rowAction} onClick={() => onPrompt(p.session, position, p.revision)}>
          {p.excerpt || "Read prompt"}
        </button>
      );
    },
  });
  return columns;
}

export function PromptReview({ query, onSession, onPrompt }: { query: ReturnType<typeof usePromptReview> } & Actions) {
  if (query.isPending) return <p role="status">Loading the prompt review…</p>;
  if (query.isError) {
    return <Failure title="The prompt review could not be loaded." message={describe(query.error)} retry={() => void query.refetch()} />;
  }
  const review = query.data;
  if (review.count === 0) return <p>No flagged prompts match these filters.</p>;

  return (
    <div className="flex min-w-0 flex-col gap-6">
      <div>
        <h2 className="text-lg font-semibold">Prompt review</h2>
        <p className="text-sm text-muted">
          {formatInteger(review.count)} flagged prompts across {formatInteger(review.sessions)} sessions
        </p>
        {!review.promptsVisible && <p className="text-sm text-muted">Prompt excerpts are available on the local server.</p>}
      </div>
      <section aria-labelledby="recurring-requests" className="dashboard-panel min-w-0 space-y-2 rounded-xl border border-rule bg-surface p-4">
        <h3 id="recurring-requests" className="font-semibold">Repeated requests</h3>
        <DataTable caption="Repeated requests" rows={review.recurring.map((ask, position) => ({ ...ask, position }))} columns={recurringColumns(review.promptsVisible)}
          rowKey={(a) => String(a.position)} initialSort="count" />
      </section>
      <section aria-labelledby="prompts-by-folder" className="dashboard-panel min-w-0 space-y-2 rounded-xl border border-rule bg-surface p-4">
        <h3 id="prompts-by-folder" className="font-semibold">By folder</h3>
        <DataTable caption="Flagged prompts by folder" rows={review.byFolder} columns={folderColumns}
          rowKey={(f) => f.folder} initialSort="prompts" />
      </section>
      <section aria-labelledby="flagged-prompts" className="dashboard-panel min-w-0 space-y-2 rounded-xl border border-rule bg-surface p-4">
        <h3 id="flagged-prompts" className="font-semibold">Flagged prompts</h3>
        {review.prompts.length < review.count && (
          <p className="text-sm text-muted">Showing {formatInteger(review.prompts.length)} of {formatInteger(review.count)} prompts.</p>
        )}
        <DataTable caption="Flagged prompts" rows={review.prompts.map((prompt, tableIndex) => ({ ...prompt, tableIndex }))} columns={flaggedColumns(review.promptsVisible, { onSession, onPrompt })}
          rowKey={(p) => `${p.session}:${p.position ?? p.tableIndex}`} />
      </section>
    </div>
  );
}
