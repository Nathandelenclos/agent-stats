import type { Element, ElementContent, Root } from "hast";
import { useEffect, useId, useMemo, useRef, useState } from "react";
import Markdown, { type Components } from "react-markdown";
import { describe, Failure } from "../../shared/Failure";
import { formatDateTime, formatInteger } from "../../shared/format";
import type { Filters } from "../report";
import { describeSignals } from "../report/signals";
import { useSessionPrompt } from "./api";

// Prompt contents are local data. Link labels and image alt text never become requests or navigation.
const plainLinks: Components = {
  a: ({ children }) => <span>{children}</span>,
  img: ({ alt }) => <span>{alt}</span>,
};

function markMatches(query: string) {
  return (root: Root) => {
    if (!query) return;
    const pattern = new RegExp(query.replace(/[.*+?^${}()|[\]\\]/g, "\\$&"), "giu");

    function visit(parent: Root | Element) {
      for (let i = 0; i < parent.children.length; i++) {
        const child = parent.children[i];
        if (child?.type === "element") {
          visit(child);
        } else if (child?.type === "text") {
          const parts: ElementContent[] = [];
          let end = 0;
          for (const match of child.value.matchAll(pattern)) {
            if (match.index > end) parts.push({ type: "text", value: child.value.slice(end, match.index) });
            const hit = match[0];
            parts.push({ type: "element", tagName: "mark", properties: {}, children: [{ type: "text", value: hit }] });
            end = match.index + hit.length;
          }
          if (parts.length === 0) continue;
          if (end < child.value.length) parts.push({ type: "text", value: child.value.slice(end) });
          parent.children.splice(i, 1, ...parts);
          i += parts.length - 1;
        }
      }
    }
    visit(root);
  };
}

export function PromptMarkdown({ text, query = "" }: { text: string; query?: string }) {
  return (
    <div className="max-w-prose space-y-3 text-sm leading-relaxed [overflow-wrap:anywhere] [&_blockquote]:border-l-2 [&_blockquote]:border-rule [&_blockquote]:pl-3 [&_h1]:font-semibold [&_h2]:font-semibold [&_h3]:font-semibold [&_li]:my-1 [&_mark]:rounded-sm [&_mark]:bg-accent [&_mark]:px-0.5 [&_mark]:text-accent-ink [&_ol]:list-decimal [&_ol]:pl-5 [&_p]:whitespace-pre-wrap [&_pre]:overflow-x-auto [&_pre]:rounded-md [&_pre]:bg-bg [&_pre]:p-3 [&_ul]:list-disc [&_ul]:pl-5">
      <Markdown components={plainLinks} rehypePlugins={[[markMatches, query]]}>{text}</Markdown>
    </div>
  );
}

interface Props {
  id: string;
  position: number;
  revision?: string;
  filters: Filters;
  project: string;
  onBack: () => void;
  onSession?: (id: string) => void;
  backLabel?: string;
}

export function PromptReader({ id, position, revision, filters, project, onBack, onSession, backLabel = "Back to session" }: Props) {
  const back = useRef<HTMLButtonElement>(null);
  const prompt = useSessionPrompt(id, position, revision, filters, project);
  useEffect(() => {
    back.current?.focus();
  }, [id, position]);

  return (
    <section aria-label="Prompt reader" className="flex min-w-0 flex-col gap-4">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <button type="button" ref={back} onClick={onBack} className="min-h-10 rounded-md border border-border px-3 text-sm hover:bg-surface">
          {backLabel}
        </button>
        {onSession && <button type="button" onClick={() => onSession(id)} className="min-h-10 rounded-md border border-border px-3 text-sm hover:bg-surface">Source session</button>}
      </div>
      {prompt.isPending && <p role="status">Loading full prompt…</p>}
      {prompt.isError && <Failure title="The full prompt could not be loaded." message={describe(prompt.error)} retry={() => void prompt.refetch()} />}
      {prompt.isSuccess && (
        <article className="dashboard-panel min-w-0 rounded-xl border border-rule bg-surface p-4 sm:p-6">
          <h2 className="text-xl font-semibold">Prompt</h2>
          <p className="mt-1 text-sm text-muted">{formatDateTime(prompt.data.ts)} · {formatInteger(prompt.data.chars)} characters</p>
          {describeSignals(prompt.data.signals) && <p className="mt-1 text-sm font-medium">{describeSignals(prompt.data.signals)}</p>}
          <SearchablePrompt key={JSON.stringify([id, position, revision])} text={prompt.data.text} />
        </article>
      )}
    </section>
  );
}

function SearchablePrompt({ text }: { text: string }) {
  const field = useId();
  const [entered, setEntered] = useState("");
  const query = entered.trim();
  const [count, setCount] = useState(0);
  const [active, setActive] = useState(0);
  const contents = useRef<HTMLDivElement>(null);
  const markdown = useMemo(() => <PromptMarkdown text={text} query={query} />, [text, query]);

  useEffect(() => {
    setCount(contents.current?.querySelectorAll("mark").length ?? 0);
    setActive(0);
  }, [markdown]);

  function findNext(offset: number) {
    if (count === 0) return;
    const next = active === 0 ? (offset > 0 ? 1 : count) : (active - 1 + count + offset) % count + 1;
    setActive(next);
    contents.current?.querySelectorAll("mark")[next - 1]?.scrollIntoView?.({ block: "nearest" });
  }

  return (
    <>
      <form className="my-4 flex flex-wrap items-center gap-2 border-b border-rule pb-4" onSubmit={(event) => {
        event.preventDefault();
        findNext(1);
      }}>
        <label className="text-sm font-medium" htmlFor={field}>Find in prompt</label>
        <input id={field} type="search" value={entered} onChange={(event) => setEntered(event.target.value)} className="min-h-10 min-w-0 rounded-md border border-border bg-bg px-2 text-sm" />
        {query !== "" && (
          <>
            <button type="button" disabled={count === 0} onClick={() => findNext(-1)} className="min-h-10 rounded-md border border-border px-2 text-sm disabled:opacity-50">Previous match</button>
            <button type="submit" disabled={count === 0} className="min-h-10 rounded-md border border-border px-2 text-sm disabled:opacity-50">Next match</button>
            <output className="text-sm text-muted" aria-live="polite">
              {count === 0 ? "No matches" : active === 0 ? `${count} ${count === 1 ? "match" : "matches"}` : `Match ${active} of ${count}`}
            </output>
          </>
        )}
      </form>
      <div ref={contents}>{markdown}</div>
    </>
  );
}
