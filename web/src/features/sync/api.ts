import { useQuery } from "@tanstack/react-query";
import { getJSON, shouldRetry } from "../../shared/api";

export interface SyncStatus {
  state: "disabled" | "running" | "done" | "failed";
  indexed: number;
  note?: string;
  error?: string;
  startedAt: number;
  finishedAt: number;
}

/** Where the background indexing stands; polled each second while it runs. */
export function useSyncStatus() {
  return useQuery({
    queryKey: ["sync"],
    queryFn: ({ signal }) => getJSON<SyncStatus>("/api/sync", {}, signal),
    refetchInterval: (query) => (query.state.data?.state === "running" ? 1000 : false),
    retry: shouldRetry,
  });
}
