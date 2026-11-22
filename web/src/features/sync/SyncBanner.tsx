import { formatInteger } from "../../shared/format";
import { ApiError } from "../../shared/api";
import { useStartSync, type SyncStatus } from "./api";

/** Says where the background indexing stands. A polite live region: the figures below change when it ends. */
export function SyncBanner({ status: data, unreachable }: { status: SyncStatus | undefined; unreachable: boolean }) {
  const start = useStartSync();
  let text = "";
  let tone = "text-muted";
  if (unreachable) {
    text = "Cannot reach the agent-stats server; the figures may be out of date.";
    tone = "text-danger";
  } else if (data?.state === "running") {
    text = `Indexing session logs: ${formatInteger(data.indexed)} files so far. Figures update when it ends.`;
  } else if (data?.state === "failed") {
    text = data.error ?? "Indexing failed.";
    tone = "text-danger";
  } else if (data?.state === "done") {
    text = `Indexing done, ${formatInteger(data.indexed)} files read.${data.note ? ` ${data.note}.` : ""}`;
  } else if (data?.state === "disabled") {
    text = "Indexing is off (--no-sync): the figures are those of the last index.";
  }
  return (
    <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
      <p role="status" aria-live="polite" className={`min-h-6 text-sm ${tone}`}>
        {text}
      </p>
      {data && data.state !== "disabled" && (
        <button
          type="button"
          disabled={unreachable || data.state === "running" || start.isPending}
          onClick={() => start.mutate()}
          className="min-h-9 rounded-md border border-border px-3 text-sm hover:bg-surface disabled:opacity-60"
        >
          Re-index
        </button>
      )}
      {start.isError && (
        <p role="alert" className="text-sm text-danger">
          {start.error instanceof ApiError ? start.error.message : "Could not start indexing. Try again."}
        </p>
      )}
    </div>
  );
}
