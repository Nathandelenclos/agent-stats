import { ApiError } from "./api";

/** What to tell the person about a failed request. */
export function describe(error: unknown): string {
  // The server words its own refusals for people; anything else (network, unreadable answer) gets a plain sentence.
  return error instanceof ApiError ? error.message : "The page could not reach the agent-stats server. Check that it is still running, then try again.";
}

export function Failure({ title, message, retry }: { title: string; message: string; retry: () => void }) {
  return (
    <div role="alert" className="rounded-lg border border-danger p-4">
      <p className="font-semibold text-danger">{title}</p>
      <p className="mt-1 text-sm">{message}</p>
      <button type="button" onClick={retry} className="mt-3 min-h-9 rounded-md border border-border px-3 text-sm hover:bg-surface">
        Try again
      </button>
    </div>
  );
}
