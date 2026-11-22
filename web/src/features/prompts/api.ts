import { useQuery } from "@tanstack/react-query";
import { getJSON, shouldRetry } from "../../shared/api";
import type { Filters, Scope } from "../report";
import type { PromptSignals } from "../report/signals";

export interface PromptReviewData {
  scope: Scope;
  count: number;
  sessions: number;
  promptsVisible: boolean;
  prompts: { session: string; harness: string; title: string; folder: string; ts: number; excerpt?: string; signals: PromptSignals }[];
  recurring: { excerpt?: string; count: number; sessions: number; firstTs: number; lastTs: number }[];
  byFolder: { folder: string; prompts: number; sessions: number; signals: number }[];
}

export function usePromptReview(filters: Filters | undefined, project: string) {
  const params = filters && { since: filters.since, harness: filters.harness, project: filters.scope, limit: "1000" };
  return useQuery({
    queryKey: ["prompts", params, project],
    queryFn: ({ signal }) => getJSON<PromptReviewData>("/api/prompts", params, signal),
    enabled: params !== undefined,
    gcTime: 0,
    retry: shouldRetry,
  });
}
