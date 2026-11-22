import Markdown, { type Components } from "react-markdown";
import { useEffect, useId, useRef, useState, type ReactNode } from "react";
import { DataTable, type Column } from "../../shared/DataTable";
import { describe, Failure } from "../../shared/Failure";
import { formatDateTime, formatInteger, formatMs, formatPercent, formatUSD } from "../../shared/format";
import { describeSignals } from "../report/signals";
import { ToolsTable, type Filters } from "../report";
import { useSessionDetail, type Session, type SessionDetail, type useSessions } from "./api";

function sessionTitle(session: Session) {
  return session.title || "Untitled session";
}

function duration(ms: number) {
  if (ms >= 3_600_000) return `${(ms / 3_600_000).toFixed(1)} h`;
  if (ms >= 60_000) return `${(ms / 60_000).toFixed(1)} min`;
  return formatMs(ms);
}

const sessionMetrics: Column<Session>[] = [
  { id: "cost", header: "Cost", value: (row) => row.usage.cost, render: (row) => formatUSD(row.usage.cost), numeric: true },
  { id: "prompts", header: "Prompts", value: (row) => row.prompts, render: (row) => formatInteger(row.prompts), numeric: true },
  { id: "avgPrompt", header: "Avg prompt chars", value: (row) => row.avgPromptChars, render: (row) => row.prompts === 0 ? "n/a" : formatInteger(row.avgPromptChars), numeric: true },
  { id: "requests", header: "Requests", value: (row) => row.usage.requests, render: (row) => formatInteger(row.usage.requests), numeric: true },
  { id: "duration", header: "Duration", value: (row) => row.durationMs, render: (row) => duration(row.durationMs), numeric: true },
  { id: "started", header: "Started", value: (row) => row.startTs, render: (row) => formatDateTime(row.startTs), numeric: true },
  { id: "activity", header: "Last activity", value: (row) => row.endTs, render: (row) => formatDateTime(row.endTs), numeric: true },
  { id: "harness", header: "Harness", value: (row) => row.harness },
  { id: "model", header: "Main model", value: (row) => row.models[0]?.model ?? "", truncate: true },
  { id: "subagents", header: "Subagents", value: (row) => row.subagents, render: (row) => formatInteger(row.subagents), numeric: true },
  { id: "agent", header: "Agent type", value: (row) => row.agentType, truncate: true },
  { id: "folder", header: "Folder", value: (row) => row.folder, truncate: true },
];

interface Props {
  filters: Filters;
  project: string;
  list: ReturnType<typeof useSessions>;
}

export function Sessions(props: Props) {
  const { since, harness, scope } = props.filters;
  // Re-enter at the list whenever the scope of the detail changes.
  return <SessionBrowser key={JSON.stringify([since, harness, scope, props.project])} {...props} />;
}

function SessionBrowser({ filters, project, list }: Props) {
  const [selected, setSelected] = useState<string>();
  const sessionButton = useRef<HTMLButtonElement>(null);

  if (list.isPending) return <p role="status">Loading sessions…</p>;
  if (list.isError) {
    return <Failure title="Sessions could not be loaded." message={describe(list.error)} retry={() => void list.refetch()} />;
  }

  const { count, total, sessions } = list.data;
  if (count === 0) return <p className="rounded-lg border border-rule bg-surface p-4">No sessions match these filters.</p>;

  const columns: Column<Session>[] = [{
    id: "title",
    header: "Session",
    value: sessionTitle,
    render: (row) => (
      <button
        type="button"
        onClick={(event) => {
          sessionButton.current = event.currentTarget;
          setSelected(row.id);
        }}
        className="block max-w-full truncate py-1 text-left font-medium text-accent hover:underline"
      >
        {sessionTitle(row)}
      </button>
    ),
    truncate: true,
  }, ...sessionMetrics];

  return (
    <>
      <div hidden={selected !== undefined}>
        <div className="mb-3 flex flex-wrap items-baseline justify-between gap-x-4 gap-y-1">
          <h2 className="text-lg font-semibold">Session list</h2>
          <p className="text-sm text-muted">
            {sessions.length < count && `${formatInteger(sessions.length)} of `}
            {formatInteger(count)} sessions, {formatInteger(total.requests)} requests, {formatUSD(total.cost)} total
          </p>
        </div>
        <DataTable caption="Session list" columns={columns} rows={sessions} rowKey={(row) => row.id} initialSort="activity" />
      </div>
      {selected !== undefined && (
        <SessionPage id={selected} filters={filters} project={project} close={() => {
          setSelected(undefined);
          // The list stays mounted to keep its sort and expansion when re-entered.
          requestAnimationFrame(() => sessionButton.current?.focus());
        }} />
      )}
    </>
  );
}

function SessionPage({ id, filters, project, close }: { id: string; filters: Filters; project: string; close: () => void }) {
  const back = useRef<HTMLButtonElement>(null);
  useEffect(() => {
    back.current?.focus();
  }, []);
  const detail = useSessionDetail(id, filters, project);

  return (
    <section aria-label="Session detail" className="flex min-w-0 flex-col gap-6">
      <button type="button" ref={back} onClick={close} className="min-h-9 self-start rounded-md border border-border px-3 text-sm hover:bg-surface">
        Back to sessions
      </button>
      {detail.isPending && <p role="status">Loading session…</p>}
      {detail.isError && <Failure title="The session could not be loaded." message={describe(detail.error)} retry={() => void detail.refetch()} />}
      {detail.isSuccess && <SessionContents detail={detail.data} />}
    </section>
  );
}

function DetailSection({ heading, children }: { heading: string; children: ReactNode }) {
  const id = useId();
  return (
    <section aria-labelledby={id} className="flex flex-col gap-3">
      <h3 id={id} className="text-base font-semibold">{heading}</h3>
      {children}
    </section>
  );
}

const modelColumns: Column<Session["models"][number]>[] = [
  { id: "model", header: "Model", value: (row) => row.model, truncate: true },
  { id: "requests", header: "Requests", value: (row) => row.requests, render: (row) => formatInteger(row.requests), numeric: true },
  { id: "cost", header: "Cost", value: (row) => row.cost, render: (row) => formatUSD(row.cost), numeric: true },
  { id: "share", header: "Share", value: (row) => row.share, render: (row) => formatPercent(row.share), numeric: true },
];

type SubagentRow = SessionDetail["subagents"][number] & { position: number };

const subagentColumns: Column<SubagentRow>[] = [
  { id: "title", header: "Title", value: (row) => row.title, truncate: true },
  { id: "agent", header: "Agent type", value: (row) => row.agentType, truncate: true },
  { id: "requests", header: "Requests", value: (row) => row.usage.requests, render: (row) => formatInteger(row.usage.requests), numeric: true },
  { id: "cost", header: "Cost", value: (row) => row.usage.cost, render: (row) => formatUSD(row.usage.cost), numeric: true },
];


const promptMarkdownComponents: Components = {
  a: ({ children }) => <span>{children}</span>,
  img: ({ alt }) => <span>{alt}</span>,
};

function SessionContents({ detail }: { detail: SessionDetail }) {
  const { session, tools, subagents, prompts, promptsVisible } = detail;
  return (
    <>
      <div className="space-y-2 border-b border-rule pb-4">
        <h2 className="break-words text-xl font-semibold">{sessionTitle(session)}</h2>
        <p className="break-words text-sm text-muted">
          {session.harness}{session.folder && ` · ${session.folder}`}
        </p>
        <p className="text-sm text-muted">
          {formatDateTime(session.startTs)} to {formatDateTime(session.endTs)} · {duration(session.durationMs)}
        </p>
        <p className="text-sm font-medium tabular-nums">
          {formatUSD(session.usage.cost)} · {formatInteger(session.usage.requests)} requests ({formatInteger(session.usage.failed)} failed) ·{" "}
          {formatInteger(session.prompts)} prompts · {formatInteger(session.subagents)} subagents
        </p>
      </div>
      <DetailSection heading="Models">
        <DataTable caption="Session models" columns={modelColumns} rows={session.models} rowKey={(row) => row.model} initialSort="share" />
      </DetailSection>
      <DetailSection heading="Tools">
        <ToolsTable tools={tools} />
      </DetailSection>
      <DetailSection heading="Subagents">
        <DataTable caption="Session subagents" columns={subagentColumns} rows={subagents.map((row, position) => ({ ...row, position }))} rowKey={(row) => String(row.position)} initialSort="cost" />
      </DetailSection>
      <DetailSection heading="Prompts">
        {!promptsVisible && <p className="text-sm text-muted">Prompt excerpts are available on the local server.</p>}
        {prompts.length === 0 ? <p className="text-sm text-muted">No prompts to display.</p> : (
          <ol className="divide-y divide-rule rounded-lg border border-rule">
            {prompts.map((prompt, position) => {
              const signals = describeSignals(prompt.signals);
              return (
                <li key={position} className="space-y-2 px-4 py-3">
                  <p className="text-sm text-muted">
                    {formatDateTime(prompt.ts)} · {formatInteger(prompt.chars)} characters
                    {signals && <span className="ml-2 font-medium text-ink">{signals}</span>}
                  </p>
                  {promptsVisible && prompt.excerpt && (
                    <div className="max-w-prose space-y-2 text-sm [overflow-wrap:anywhere] [&_blockquote]:border-l-2 [&_blockquote]:border-rule [&_blockquote]:pl-3 [&_ol]:list-decimal [&_ol]:pl-5 [&_pre]:overflow-x-auto [&_pre]:rounded-md [&_pre]:bg-surface [&_pre]:p-3 [&_ul]:list-disc [&_ul]:pl-5">
                      <Markdown components={promptMarkdownComponents}>{prompt.excerpt}</Markdown>
                    </div>
                  )}
                </li>
              );
            })}
          </ol>
        )}
      </DetailSection>
    </>
  );
}
