import { useId } from "react";
import type { Filters, Meta, Scope } from "./types";

const presets = [
  { value: "24h", label: "Last 24 hours" },
  { value: "7d", label: "Last 7 days" },
  { value: "30d", label: "Last 30 days" },
  { value: "", label: "All time" },
];

const selectClass = "min-h-9 rounded-md border border-border bg-bg px-2 text-ink";

/** What a report covers, in words: the project the server runs in, or every project. `scope` is the one the report says it covers. */
export function scopeLabel(meta: Meta, scope: Scope): string {
  return scope === "current" && meta.project !== "" ? `Project: ${meta.project}` : "All projects";
}

const scopes: { value: Scope; label: string }[] = [
  { value: "current", label: "This project" },
  { value: "all", label: "All projects" },
];

/** Switches between the project the server runs in and every project. */
function ScopeToggle({ value, onChange }: { value: Scope; onChange: (next: Scope) => void }) {
  return (
    <div role="group" aria-label="Projects" className="flex overflow-hidden rounded-md border border-border">
      {scopes.map((s) => (
        <button
          key={s.value}
          type="button"
          aria-pressed={value === s.value}
          onClick={() => onChange(s.value)}
          className={`min-h-9 px-3 text-sm ${value === s.value ? "bg-accent text-accent-ink" : "bg-bg text-ink hover:bg-surface"}`}
        >
          {s.label}
        </button>
      ))}
    </div>
  );
}

/** The period, harness and, when the server runs in a project, scope the report is limited to. */
export function FilterBar({ meta, value, onChange }: { meta: Meta; value: Filters; onChange: (next: Filters) => void }) {
  const periodId = useId();
  const harnessId = useId();
  // The period given on the command line, when it is not a preset (a date, 12h…), stays selectable whichever period is chosen.
  const extra = [...new Set([meta.defaults.since, value.since])].filter((since) => !presets.some((p) => p.value === since));
  const periods = [...extra.map((since) => ({ value: since, label: `Since ${since}` })), ...presets];
  return (
    <form className="flex flex-wrap items-end gap-4" onSubmit={(e) => e.preventDefault()}>
      <div className="flex flex-col gap-1">
        <label htmlFor={periodId} className="text-sm text-muted">Period</label>
        <select id={periodId} className={selectClass} value={value.since} onChange={(e) => onChange({ ...value, since: e.target.value })}>
          {periods.map((p) => (
            <option key={p.value} value={p.value}>{p.label}</option>
          ))}
        </select>
      </div>
      <div className="flex flex-col gap-1">
        <label htmlFor={harnessId} className="text-sm text-muted">Harness</label>
        <select id={harnessId} className={selectClass} value={value.harness} onChange={(e) => onChange({ ...value, harness: e.target.value })}>
          <option value="">All harnesses</option>
          {meta.harnesses.map((h) => (
            <option key={h} value={h}>{h}</option>
          ))}
        </select>
      </div>
      {meta.project !== "" && <ScopeToggle value={value.scope} onChange={(scope) => onChange({ ...value, scope })} />}
    </form>
  );
}
