import { DataTable, type Column } from "../../shared/DataTable";
import { describe, Failure } from "../../shared/Failure";
import { formatDateTime, formatInteger } from "../../shared/format";
import { describeSignals } from "../report/signals";
import type { PromptReviewData, usePromptReview } from "./api";

type Folder = PromptReviewData["byFolder"][number];
type Flagged = PromptReviewData["prompts"][number] & { position: number };
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

function flaggedColumns(excerpts: boolean): Column<Flagged>[] {
  const columns: Column<Flagged>[] = [
    { id: "signals", header: "Signals", value: (p) => describeSignals(p.signals) },
    { id: "title", header: "Session", value: (p) => p.title || p.session, truncate: true },
    { id: "folder", header: "Folder", value: (p) => p.folder || "Unknown", truncate: true },
    { id: "harness", header: "Harness", value: (p) => p.harness },
    { id: "ts", header: "Time", value: (p) => p.ts, render: (p) => formatDateTime(p.ts) },
  ];
  if (excerpts) columns.unshift({ id: "prompt", header: "Prompt", value: (p) => p.excerpt ?? "" });
  return columns;
}

export function PromptReview({ query }: { query: ReturnType<typeof usePromptReview> }) {
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
      <section aria-labelledby="recurring-requests" className="space-y-2">
        <h3 id="recurring-requests" className="font-semibold">Repeated requests</h3>
        <DataTable caption="Repeated requests" rows={review.recurring.map((ask, position) => ({ ...ask, position }))} columns={recurringColumns(review.promptsVisible)}
          rowKey={(a) => String(a.position)} initialSort="count" />
      </section>
      <section aria-labelledby="prompts-by-folder" className="space-y-2">
        <h3 id="prompts-by-folder" className="font-semibold">By folder</h3>
        <DataTable caption="Flagged prompts by folder" rows={review.byFolder} columns={folderColumns}
          rowKey={(f) => f.folder} initialSort="prompts" />
      </section>
      <section aria-labelledby="flagged-prompts" className="space-y-2">
        <h3 id="flagged-prompts" className="font-semibold">Flagged prompts</h3>
        {review.prompts.length < review.count && (
          <p className="text-sm text-muted">Showing {formatInteger(review.prompts.length)} of {formatInteger(review.count)} prompts.</p>
        )}
        <DataTable caption="Flagged prompts" rows={review.prompts.map((prompt, position) => ({ ...prompt, position }))} columns={flaggedColumns(review.promptsVisible)}
          rowKey={(p) => `${p.session}:${p.position}`} />
      </section>
    </div>
  );
}
