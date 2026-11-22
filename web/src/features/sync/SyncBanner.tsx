import { formatInteger } from "../../shared/format";
import { useSyncStatus } from "./api";

/** Says where the background indexing stands. A polite live region: the figures below change when it ends. */
export function SyncBanner() {
  const { data, error } = useSyncStatus();
  let text = "";
  let tone = "text-muted";
  if (error) {
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
    <p role="status" aria-live="polite" className={`min-h-6 text-sm ${tone}`}>
      {text}
    </p>
  );
}
