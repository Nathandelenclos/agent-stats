import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { getJSON, postJSON, shouldRetry } from "../../shared/api";

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

export function useStartSync() {
  const client = useQueryClient();
  return useMutation({
    mutationFn: () => postJSON<SyncStatus>("/api/sync"),
    onSuccess: async (status) => {
      await client.cancelQueries({ queryKey: ["sync"] });
      client.setQueryData(["sync"], status);
      void client.invalidateQueries({ queryKey: ["sync"] });
    },
  });
}
