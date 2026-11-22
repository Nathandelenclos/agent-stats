import { useQuery } from "@tanstack/react-query";
import type { Filters, Scope, Tool, Usage } from "../report";
import { getJSON, shouldRetry } from "../../shared/api";

interface SessionModel {
  model: string;
  requests: number;
  cost: number;
  share: number;
}

export interface Session {
  id: string;
  harness: string;
  title: string;
  folder: string;
  agentType: string;
  startTs: number;
  endTs: number;
  durationMs: number;
  usage: Usage;
  models: SessionModel[];
  subagents: number;
  prompts: number;
  avgPromptChars: number;
}

interface SessionList {
  scope: Scope;
  count: number;
  total: Pick<Usage, "requests" | "failed" | "cost" | "unpriced">;
  sessions: Session[];
}

interface SessionPrompt {
  ts: number;
  excerpt?: string;
  chars: number;
  signals: {
    yelling: number;
    profanity: number;
    anguish: number;
    negation: number;
    blame: number;
    repeated: boolean;
  };
}

export interface SessionDetail {
  scope: Scope;
  session: Session;
  tools: Tool[];
  subagents: { agentType: string; title: string; usage: Usage }[];
  promptsVisible: boolean;
  prompts: SessionPrompt[];
}

function filterParams({ since, harness, scope }: Filters) {
  return { since, harness, project: scope };
}

export function useSessions(filters: Filters | undefined, project: string) {
  const params = filters && filterParams(filters);
  return useQuery({
    queryKey: ["sessions", "list", params, project],
    queryFn: ({ signal }) => getJSON<SessionList>("/api/sessions", { ...params, limit: "1000" }, signal),
    enabled: params !== undefined,
    retry: shouldRetry,
  });
}

export function useSessionDetail(id: string, filters: Filters, project: string) {
  const params = filterParams(filters);
  return useQuery({
    queryKey: ["sessions", "detail", id, params, project],
    queryFn: ({ signal }) => getJSON<SessionDetail>(`/api/sessions/${encodeURIComponent(id)}`, params, signal),
    gcTime: 0,
    retry: shouldRetry,
  });
}
