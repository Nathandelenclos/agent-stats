// The HTTP client of the agent-stats API. Every route is a GET that answers JSON or an RFC 9457 problem.

export class ApiError extends Error {
  readonly status: number;
  constructor(status: number, message: string) {
    super(message);
    this.name = "ApiError";
    this.status = status;
  }
}

/** Reads one API route. A problem document becomes an ApiError carrying its detail. */
export async function getJSON<T>(path: string, params: Record<string, string> = {}, signal?: AbortSignal): Promise<T> {
  const query = new URLSearchParams(Object.entries(params).filter(([, value]) => value !== ""));
  const url = query.size > 0 ? `${path}?${query}` : path;
  const res = await fetch(url, { signal, headers: { Accept: "application/json" }, credentials: "same-origin" });
  if (!res.ok) {
    let detail = res.statusText;
    try {
      const body = (await res.json()) as { title?: string; detail?: string };
      detail = body.detail ?? body.title ?? detail;
    } catch {
      /* not a problem document: keep the status text */
    }
    throw new ApiError(res.status, detail);
  }
  return (await res.json()) as T;
}

/** Client errors (4xx) are the page's fault and retrying cannot fix them; the rest may be transient. */
export function shouldRetry(failures: number, error: unknown): boolean {
  if (error instanceof ApiError && error.status < 500) return false;
  return failures < 2;
}
