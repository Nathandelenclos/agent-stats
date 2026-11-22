import { useEffect, useId, useRef, useState, type KeyboardEvent, type ReactNode } from "react";
import { DataTable, type Column } from "../../shared/DataTable";
import { describe, Failure } from "../../shared/Failure";
import { formatDateTime, formatInteger, formatMs, formatPercent, formatUSD } from "../../shared/format";
import { describeSignals } from "../report/signals";
import { ToolsTable, type Filters, type Usage } from "../report";
import { PromptMarkdown, PromptReader } from "./PromptReader";
import { useSessionDetail, type Session, type SessionDetail, type SessionPrompt, type useSessions } from "./api";

function sessionTitle(session: Session) {
  return session.title || "Untitled session";
}

function duration(ms: number) {
  if (ms >= 3_600_000) return `${(ms / 3_600_000).toFixed(1)} h`;
  if (ms >= 60_000) return `${(ms / 60_000).toFixed(1)} min`;
  return formatMs(ms);
}

const sessionMetrics: Column<Session>[] = [
  { id: "cost", header: "Cost", value: (row) => row.usage.cost, render: (row) => estimatedCost(row.usage), numeric: true },
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

const sectionNames = ["Summary", "Models", "Tools", "Subagents", "Prompts"] as const;
type SectionName = typeof sectionNames[number];
type PromptTarget = { position: number; revision?: string };
type OpenPrompt = (id: string, position: number, revision?: string) => void;
type PromptClick = (id: string, position: number, revision: string | undefined, button: HTMLButtonElement) => void;

function namedSection(section: string | undefined): SectionName {
  return sectionNames.find((name) => name.toLowerCase() === section?.toLowerCase()) ?? "Summary";
}

function estimatedCost(usage: Pick<Usage, "requests" | "cost" | "unpriced">) {
  return usage.requests > 0 && usage.requests === usage.unpriced ? "Unavailable" : formatUSD(usage.cost);
}

interface Props {
  filters: Filters;
  project: string;
  list: ReturnType<typeof useSessions>;
  selected?: string;
  prompt?: number;
  revision?: string;
  section?: string;
  onSelect?: (id: string) => void;
  onPrompt?: OpenPrompt;
  onSection?: (section: string) => void;
  onBack?: () => void;
}

export function Sessions(props: Props) {
  const { since, harness, scope } = props.filters;
  return <SessionBrowser key={JSON.stringify([since, harness, scope, props.project])} {...props} />;
}

function SessionBrowser({ filters, project, list, selected, prompt, revision, section, onSelect, onPrompt, onSection, onBack }: Props) {
  const [localSession, setLocalSession] = useState<string>();
  const [localPrompt, setLocalPrompt] = useState<PromptTarget>();
  const [localSection, setLocalSection] = useState<SectionName>("Summary");
  const sessionButton = useRef<HTMLButtonElement>(null);
  const id = onSelect ? selected : localSession;
  const reader = onPrompt ? (prompt === undefined ? undefined : { position: prompt, revision }) : localPrompt;
  const active = onSection ? namedSection(section) : localSection;

  useEffect(() => {
    if (id === undefined) sessionButton.current?.focus();
  }, [id]);

  function open(id: string, button: HTMLButtonElement) {
    sessionButton.current = button;
    setLocalSection("Summary");
    setLocalPrompt(undefined);
    if (onSelect) onSelect(id);
    else setLocalSession(id);
  }

  function openPrompt(id: string, position: number, revision?: string) {
    if (onPrompt) onPrompt(id, position, revision);
    else setLocalPrompt({ position, revision });
  }

  function close() {
    if (onBack) onBack();
    else if (reader) setLocalPrompt(undefined);
    else setLocalSession(undefined);
  }

  return (
    <>
      <div hidden={id !== undefined}>
        {list.isPending && <p role="status">Loading sessions…</p>}
        {list.isError && (
          <Failure title="Sessions could not be loaded." message={describe(list.error)} retry={() => void list.refetch()} />
        )}
        {list.isSuccess && list.data.count === 0 && (
          <p className="dashboard-panel rounded-xl border border-rule bg-surface p-4">No sessions match these filters.</p>
        )}
        {list.isSuccess && list.data.count > 0 && (
          <SessionList list={list.data} open={open} />
        )}
      </div>
      {id !== undefined && (
        <>
          <div hidden={reader !== undefined}>
            <SessionPage
              key={id}
              id={id}
              filters={filters}
              project={project}
              close={close}
              active={active}
              reading={reader !== undefined}
              onSection={(name) => onSection ? onSection(name.toLowerCase()) : setLocalSection(name)}
              openPrompt={openPrompt}
            />
          </div>
          {reader !== undefined && (
            <PromptReader key={JSON.stringify([id, reader.position])} id={id} position={reader.position} revision={reader.revision} filters={filters} project={project} onBack={close} />
          )}
        </>
      )}
    </>
  );
}

function SessionList({ list, open }: { list: NonNullable<ReturnType<typeof useSessions>["data"]>; open: (id: string, button: HTMLButtonElement) => void }) {
  const { count, sessions, total } = list;
  const columns: Column<Session>[] = [{
    id: "title",
    header: "Session",
    value: sessionTitle,
    render: (row) => (
      <button type="button" onClick={(event) => open(row.id, event.currentTarget)} className="block min-h-9 max-w-full truncate text-left font-medium text-accent hover:underline">
        {sessionTitle(row)}
      </button>
    ),
    truncate: true,
  }, ...sessionMetrics];

  return (
    <section aria-labelledby="session-list-title" className="dashboard-panel rounded-xl border border-rule bg-surface p-4">
      <div className="mb-3 flex flex-wrap items-baseline justify-between gap-x-4 gap-y-1">
        <h2 id="session-list-title" className="text-lg font-semibold">Session list</h2>
        <p className="text-sm text-muted">
          {sessions.length < count && `${formatInteger(sessions.length)} of `}
          {formatInteger(count)} sessions, {formatInteger(total.requests)} requests, {estimatedCost(total)} total
        </p>
      </div>
      <DataTable caption="Session list" columns={columns} rows={sessions} rowKey={(row) => row.id} initialSort="activity" />
    </section>
  );
}

function SessionPage({ id, filters, project, close, active, reading, onSection, openPrompt }: {
  id: string;
  filters: Filters;
  project: string;
  close: () => void;
  active: SectionName;
  reading: boolean;
  onSection: (section: SectionName) => void;
  openPrompt: OpenPrompt;
}) {
  const back = useRef<HTMLButtonElement>(null);
  const promptButton = useRef<HTMLButtonElement>(null);
  const detail = useSessionDetail(id, filters, project);
  useEffect(() => {
    if (!reading) (promptButton.current ?? back.current)?.focus();
  }, [id, reading]);

  return (
    <section aria-label="Session detail" className="flex min-w-0 flex-col gap-4">
      <button type="button" ref={back} onClick={close} className="min-h-10 self-start rounded-md border border-border px-3 text-sm hover:bg-surface">
        Back to sessions
      </button>
      {detail.isPending && <p role="status">Loading session…</p>}
      {detail.isError && <Failure title="The session could not be loaded." message={describe(detail.error)} retry={() => void detail.refetch()} />}
      {detail.isSuccess && (
        <SessionContents detail={detail.data} active={active} onSection={onSection} openPrompt={(id, position, revision, button) => {
          promptButton.current = button;
          openPrompt(id, position, revision);
        }} />
      )}
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


function Metric({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="dashboard-panel min-w-0 rounded-xl border border-rule bg-surface p-3">
      <dt className="text-xs text-muted">{label}</dt>
      <dd className="mt-1 break-words text-lg font-semibold tabular-nums">{children}</dd>
    </div>
  );
}

function SessionContents({ detail, active, onSection, openPrompt }: {
  detail: SessionDetail;
  active: SectionName;
  onSection: (section: SectionName) => void;
  openPrompt: PromptClick;
}) {
  const id = useId();
  const buttons = useRef<Array<HTMLButtonElement | null>>([]);
  const { session } = detail;

  function keyboard(event: KeyboardEvent<HTMLButtonElement>, index: number) {
    const last = sectionNames.length - 1;
    let next: number;
    switch (event.key) {
      case "ArrowLeft": next = index === 0 ? last : index - 1; break;
      case "ArrowRight": next = index === last ? 0 : index + 1; break;
      case "Home": next = 0; break;
      case "End": next = last; break;
      default: return;
    }
    event.preventDefault();
    const section = sectionNames[next];
    if (section) onSection(section);
    buttons.current[next]?.focus();
  }

  return (
    <>
      <header>
        <h2 className="break-words text-2xl font-semibold tracking-tight">{sessionTitle(session)}</h2>
        <p className="mt-1 break-words text-sm text-muted">{session.harness}{session.folder && ` · ${session.folder}`}</p>
      </header>
      <dl className="grid gap-2 sm:grid-cols-3">
        <Metric label="Estimated cost">{estimatedCost(session.usage)}</Metric>
        <Metric label="Model requests">{formatInteger(session.usage.requests)}</Metric>
        <Metric label="Input from cache">{session.usage.input + session.usage.cacheRead + session.usage.cacheWrite > 0 ? formatPercent(session.usage.cacheRate) : "Unavailable"}</Metric>
      </dl>
      <div className="dashboard-panel min-w-0 rounded-xl border border-rule bg-surface p-3 sm:p-5">
        <div role="tablist" aria-label="Session sections" className="mb-4 flex flex-wrap gap-x-4 gap-y-1 border-b border-rule">
          {sectionNames.map((section, index) => (
            <button
              key={section}
              type="button"
              role="tab"
              id={`${id}-tab-${index}`}
              aria-controls={`${id}-section-${index}`}
              aria-selected={section === active}
              tabIndex={section === active ? 0 : -1}
              ref={(button) => { buttons.current[index] = button; }}
              onClick={() => onSection(section)}
              onKeyDown={(event) => keyboard(event, index)}
              className={`min-h-10 border-b-2 pb-1 text-sm ${section === active ? "border-accent font-semibold text-ink" : "border-transparent text-muted hover:text-ink"}`}
            >
              {section}
            </button>
          ))}
        </div>
        {sectionNames.map((section, index) => (
          <section
            key={section}
            role="tabpanel"
            id={`${id}-section-${index}`}
            aria-labelledby={`${id}-tab-${index}`}
            hidden={section !== active}
            tabIndex={0}
          >
            {section === active && <SectionContent detail={detail} section={section} openPrompt={openPrompt} />}
          </section>
        ))}
      </div>
    </>
  );
}

function SectionContent({ detail, section, openPrompt }: { detail: SessionDetail; section: SectionName; openPrompt: PromptClick }) {
  const { session, tools, subagents, prompts, promptsVisible } = detail;
  switch (section) {
    case "Summary": {
      const fields = [
        ["Started", formatDateTime(session.startTs)], ["Last activity", formatDateTime(session.endTs)],
        ["Duration", duration(session.durationMs)], ["Failed requests", formatInteger(session.usage.failed)],
        ["Prompts", formatInteger(session.prompts)], ["Subagents", formatInteger(session.subagents)],
      ];
      return (
        <>
          <p className="mb-3 text-sm text-muted">Activity for the full session, including its subagents.</p>
          <dl className="grid grid-cols-2 gap-3 text-sm sm:grid-cols-3">
            {fields.map(([label, value]) => (
              <div key={label} className="min-w-0 border-t border-rule pt-2"><dt className="text-muted">{label}</dt><dd className="tabular-nums">{value}</dd></div>
            ))}
          </dl>
          {session.usage.unpriced > 0 && <p className="mt-3 text-sm text-muted">Price known for {formatInteger(session.usage.requests - session.usage.unpriced)} of {formatInteger(session.usage.requests)} requests.</p>}
        </>
      );
    }
    case "Models":
      return <DataTable caption="Session models" columns={modelColumns} rows={session.models} rowKey={(row) => row.model} initialSort="share" />;
    case "Tools":
      return <>
        <p className="mb-3 text-sm text-muted">Tool calls observed in the session.</p>
        <ToolsTable tools={tools} />
      </>;
    case "Subagents":
      return <DataTable caption="Session subagents" columns={subagentColumns} rows={subagents.map((row, position) => ({ ...row, position }))} rowKey={(row) => String(row.position)} initialSort="cost" />;
    case "Prompts":
      return <SessionPrompts prompts={prompts} visible={promptsVisible} open={(position, revision, button) => openPrompt(session.id, position, revision, button)} />;
  }
}

function SessionPrompts({ prompts, visible, open }: { prompts: SessionPrompt[]; visible: boolean; open: (position: number, revision: string | undefined, button: HTMLButtonElement) => void }) {
  const pageSize = 12;
  const [limit, setLimit] = useState(pageSize);
  return (
    <>
      {!visible && <p className="mb-3 text-sm text-muted">Prompt text is available on the local server.</p>}
      {prompts.length === 0 ? <p className="text-sm text-muted">No prompts to display.</p> : (
        <ol className="divide-y divide-rule">
          {prompts.slice(0, limit).map((prompt, i) => {
            const signals = describeSignals(prompt.signals);
            const position = prompt.position;
            const canRead = visible && position !== undefined;
            return (
              <li key={position ?? i} className="space-y-2 py-3">
                <div className="flex flex-wrap items-center justify-between gap-2">
                  <p className="text-sm text-muted">
                    {formatDateTime(prompt.ts)} · {formatInteger(prompt.chars)} characters
                    {signals && <span className="ml-2 font-medium text-ink">{signals}</span>}
                  </p>
                  {canRead && (
                    <button type="button" onClick={(event) => open(position, prompt.revision, event.currentTarget)} className="min-h-9 rounded-md border border-border px-3 text-sm font-medium hover:bg-bg">Read prompt {i + 1}</button>
                  )}
                </div>
                {visible && prompt.excerpt && (
                  canRead ? <p className="line-clamp-2 max-w-prose break-words text-sm">{prompt.excerpt}</p> : <PromptMarkdown text={prompt.excerpt} />
                )}
              </li>
            );
          })}
        </ol>
      )}
      {prompts.length > limit && (
        <button type="button" onClick={() => setLimit(limit + pageSize)} className="mt-2 min-h-10 rounded-md border border-border px-3 text-sm hover:bg-bg">
          Show more prompts ({formatInteger(limit)} of {formatInteger(prompts.length)})
        </button>
      )}
    </>
  );
}
