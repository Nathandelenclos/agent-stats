import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { getJSON, postJSON, shouldRetry } from "../../shared/api";

export interface SyncStatus {
  state: "disabled" | "running" | "done" | "failed";
  indexed: number;
  note?: string;
  error?: string;
  startedAt: number;
  finishedAt: number;
  dataVersion?: number;
}

/** Poll faster while indexing so changes appear as soon as a run finishes. */
export function useSyncStatus() {
  return useQuery({
    queryKey: ["sync"],
    queryFn: ({ signal }) => getJSON<SyncStatus>("/api/sync", {}, signal),
    refetchInterval: (query) => {
      const state = query.state.data?.state;
      if (state === "disabled") return false;
      return state === "running" ? 1000 : 5000;
    },
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
