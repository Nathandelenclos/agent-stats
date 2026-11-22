import { useEffect, useState } from "react";
import { formatDay } from "../../shared/format";

export const views = ["Overview", "Usage", "Sessions", "Requests", "Errors", "Prompts", "Improvements", "Catalog", "Quotas"] as const;
export type View = (typeof views)[number];
export type Metric = "cost" | "requests" | "tokens";

const relationKinds = ["day", "harness", "model", "folder", "agent", "tool", "mcp"] as const;
export type Relation = {
  kind: (typeof relationKinds)[number];
  value: string;
  server?: string;
  itemHarness?: string;
  itemScope?: string;
  itemOrigin?: string;
};

export function readView(params: URLSearchParams): View {
  return views.find((view) => view.toLowerCase() === params.get("view")?.toLowerCase()) ?? "Overview";
}

export function readRelation(params: URLSearchParams): Relation | undefined {
  const kind = relationKinds.find((k) => k === params.get("kind"));
  if (!kind) return;
  const value = params.get("value") ?? "";
  if (kind === "day" && !isDayKey(value)) return;
  return {
    kind,
    value,
    server: params.get("server") ?? undefined,
    itemHarness: params.get("itemHarness") ?? undefined,
    itemScope: params.get("itemScope") ?? undefined,
    itemOrigin: params.get("itemOrigin") ?? undefined,
  };
}

function isDayKey(key: string): boolean {
  if (!/^\d{4}-\d{2}-\d{2}$/.test(key)) return false;
  const date = new Date(`${key}T00:00:00Z`);
  return Number.isFinite(date.getTime()) && date.toISOString().slice(0, 10) === key;
}

export const relationKeys = ["kind", "value", "server", "itemHarness", "itemScope", "itemOrigin"] as const;

export function relationLabel(r: Relation): string {
  if (r.kind === "day") return formatDay(r.value);
  return r.server ? `${r.server} / ${r.value}` : r.value || "(none)";
}

type EntryState = { dashboardPrevious?: boolean; returnTop?: number; returnFocus?: string };

export function useLocation() {
  const [address, setAddress] = useState(() => new URLSearchParams(window.location.search));
  useEffect(() => {
    const onBack = () => {
      setAddress(new URLSearchParams(window.location.search));
      const state = window.history.state as EntryState | null;
      window.requestAnimationFrame(() => {
        if (state?.returnTop !== undefined && window.scrollY !== state.returnTop) window.scrollTo(0, state.returnTop);
        if (state?.returnFocus) document.getElementById(state.returnFocus)?.focus({ preventScroll: true });
      });
    };
    window.addEventListener("popstate", onBack);
    return () => window.removeEventListener("popstate", onBack);
  }, []);

  function update(values: Record<string, string | undefined>, replace = false) {
    const next = new URLSearchParams(window.location.search);
    for (const [key, value] of Object.entries(values)) {
      if (value === undefined) next.delete(key);
      else next.set(key, value);
    }
    if (next.toString() === new URLSearchParams(window.location.search).toString()) return;
    const url = window.location.pathname + (next.size > 0 ? `?${next}` : "");
    if (replace) window.history.replaceState(window.history.state, "", url);
    else {
      const previous = window.history.state as EntryState | null;
      window.history.replaceState({
        ...previous, returnTop: window.scrollY, returnFocus: document.activeElement?.id || undefined,
      }, "");
      window.history.pushState({ dashboardPrevious: true }, "", url);
    }
    setAddress(next);
  }

  function back(fallback: Record<string, string | undefined>) {
    if ((window.history.state as EntryState | null)?.dashboardPrevious) window.history.back();
    else update(fallback, true);
  }

  return { address, update, back };
}
