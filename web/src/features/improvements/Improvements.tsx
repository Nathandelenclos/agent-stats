import { formatInteger } from "../../shared/format";
import { describe, Failure } from "../../shared/Failure";
import type { useCatalog } from "../catalog/Catalog";
import type { usePromptReview } from "../prompts/api";

interface Props {
  prompts: ReturnType<typeof usePromptReview>;
  catalog?: ReturnType<typeof useCatalog>;
  openPrompts: () => void;
  openCatalog: () => void;
}

const previewLimit = 5;

const evidenceButton = "min-h-11 self-start rounded-md border border-border px-3 text-sm hover:bg-surface";

export function Improvements({ prompts, catalog, openPrompts, openCatalog }: Props) {
  return (
    <section aria-labelledby="improvements-title" className="space-y-5">
      <header>
        <h2 id="improvements-title" className="text-xl font-semibold">Improvements</h2>
        <p className="mt-1 text-sm text-muted">Review patterns in the selected period, harness and project.</p>
      </header>

      <section aria-labelledby="workflow-title" className="space-y-3 rounded-lg border border-rule p-4">
        <h3 id="workflow-title" className="font-semibold">Recurring requests</h3>
        {prompts.isPending && <p role="status">Loading prompt patterns…</p>}
        {prompts.isError && <Failure title="Prompt patterns could not be loaded." message={describe(prompts.error)} retry={() => void prompts.refetch()} />}
        {prompts.data && (prompts.data.recurring.length === 0
          ? <p className="text-sm text-muted">No recurring requests were detected in this selection.</p>
          : <>
            <p className="text-sm text-muted">Check whether a recurring request belongs in your project instructions.</p>
            <ul className="divide-y divide-rule border-y border-rule">
              {prompts.data.recurring.slice(0, previewLimit).map(({ excerpt, count, sessions }, index) => (
                <li key={index} className="py-3 text-sm [overflow-wrap:anywhere]">
                  <p className="font-medium">{excerpt || "Prompt text unavailable"}</p>
                  <p className="text-muted">{formatInteger(count)} occurrences across {formatInteger(sessions)} sessions</p>
                </li>
              ))}
            </ul>
            <button type="button" className={evidenceButton} onClick={openPrompts}>Review prompts</button>
          </>)}
      </section>

      {catalog && (
        <section aria-labelledby="setup-title" className="space-y-3 rounded-lg border border-rule p-4">
          <h3 id="setup-title" className="font-semibold">Setup with no observed use</h3>
          {catalog.isPending && <p role="status">Loading setup signals…</p>}
          {catalog.isError && <Failure title="Setup signals could not be loaded." message={describe(catalog.error)} retry={() => void catalog.refetch()} />}
          {catalog.data && (
            <>
              {catalog.data.skipped > 0 && <p className="text-sm text-muted">{formatInteger(catalog.data.skipped)} configuration sources could not be read.</p>}
              {catalog.data.entries.length === 0
                ? <p className="text-sm text-muted">No enabled entries with zero observed uses in this selection.</p>
                : <>
                  <p className="text-sm text-muted">Review whether these entries still belong in your setup.</p>
                  <ul className="divide-y divide-rule border-y border-rule">
                    {catalog.data.entries.slice(0, previewLimit).map(({ name, harness, kind, scope, origin }) => (
                      <li key={JSON.stringify([harness, kind, name, scope, origin])} className="py-3 text-sm [overflow-wrap:anywhere]">
                        <p className="font-medium">{name}</p>
                        <p className="text-muted">{kind} · {harness} · {scope}{origin && ` · ${origin}`}</p>
                      </li>
                    ))}
                  </ul>
                  <button type="button" className={evidenceButton} onClick={openCatalog}>Review unused entries in Catalog</button>
                </>}
            </>
          )}
        </section>
      )}
    </section>
  );
}
