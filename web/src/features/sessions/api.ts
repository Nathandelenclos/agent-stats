import { useQuery } from "@tanstack/react-query";
import type { Filters, Scope, Tool, Usage } from "../report";
import type { PromptSignals } from "../report/signals";
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
  relatedTool?: Tool;
}

export interface SessionList {
  scope: Scope;
  count: number;
  total: Pick<Usage, "requests" | "failed" | "cost" | "unpriced">;
  sessions: Session[];
}

export interface SessionPrompt {
  position?: number;
  revision?: string;
  ts: number;
  excerpt?: string;
  chars: number;
  signals: PromptSignals;
}

interface Subagent {
  id?: string;
  parentId?: string;
  agentType: string;
  title: string;
  startTs?: number;
  endTs?: number;
  models?: string[];
  usage: Usage;
}

export interface SessionDetail {
  scope: Scope;
  session: Session;
  tools: Tool[];
  forkedFrom?: string;
  subagents: Subagent[];
  promptsVisible: boolean;
  prompts: SessionPrompt[];
}

function filterParams({ since, harness, scope }: Filters) {
  return { since, harness, project: scope };
}

export function useSessions(filters: Filters | undefined, project: string, limit = 1000) {
  const params = filters && { ...filterParams(filters), limit: String(limit) };
  return useQuery({
    queryKey: ["sessions", "list", params, project],
    queryFn: ({ signal }) => getJSON<SessionList>("/api/sessions", params, signal),
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

export interface FullPrompt {
  position: number;
  ts: number;
  text: string;
  chars: number;
  signals: PromptSignals;
}

export function useSessionPrompt(id: string, position: number, revision: string | undefined, filters: Filters, project: string) {
  const params = { ...filterParams(filters), ...(revision ? { revision } : {}) };
  return useQuery({
    queryKey: ["sessions", "prompt", id, position, params, project],
    queryFn: ({ signal }) => getJSON<FullPrompt>(`/api/sessions/${encodeURIComponent(id)}/prompts/${position}`, params, signal),
    gcTime: 0,
    retry: shouldRetry,
  });
}

export interface SessionRequest {
  sessionId: string;
  folder: string;
  ts: number;
  model: string;
  provider: string;
  stopReason: string;
  purpose: string;
  serviceTier: string;
  premiumUnits: number;
  agentType: string;
  input: number;
  output: number;
  cacheRead: number;
  cacheWrite: number;
  cost: number;
  priced: boolean;
  durationMs: number;
  ttftMs: number;
  failed: boolean;
  error?: string;
}

export const requestPageSize = 25;

export function useSessionRequests(id: string | undefined, filters: Filters, project: string, failed: boolean, offset: number, signature: string) {
  const params = {
    ...filterParams(filters),
    limit: String(requestPageSize),
    offset: String(offset),
    ...(failed ? { failed: "1" } : {}),
    ...(signature ? { signature } : {}),
  };
  return useQuery({
    queryKey: ["sessions", "requests", id, params, project],
    queryFn: ({ signal }) => getJSON<{ requests: SessionRequest[]; hasMore: boolean }>(
      id ? `/api/sessions/${encodeURIComponent(id)}/requests` : "/api/requests", params, signal,
    ),
    gcTime: 0,
    retry: shouldRetry,
  });
}

interface ErrorSignature {
  key: string;
  message: string;
  count: number;
  lastTs: number;
}

export function useErrorSignatures(filters: Filters, project: string, enabled: boolean) {
  const params = { ...filterParams(filters), limit: "50" };
  return useQuery({
    queryKey: ["sessions", "errors", params, project],
    queryFn: ({ signal }) => getJSON<ErrorSignature[]>("/api/errors", params, signal),
    enabled,
    gcTime: 0,
    retry: shouldRetry,
  });
}

export interface TraceEvent {
  ts: number;
  kind: "prompt" | "model" | "tool";
  label: string;
  agentId: string;
  agentType: string;
  durationMs: number;
  failed: boolean;
}

export const tracePageSize = 50;

export function useSessionTrace(id: string, filters: Filters, project: string, offset: number) {
  const params = { ...filterParams(filters), limit: String(tracePageSize), offset: String(offset) };
  return useQuery({
    queryKey: ["sessions", "trace", id, params, project],
    queryFn: ({ signal }) => getJSON<{ events: TraceEvent[]; hasMore: boolean }>(
      `/api/sessions/${encodeURIComponent(id)}/trace`, params, signal,
    ),
    gcTime: 0,
    retry: shouldRetry,
  });
}
