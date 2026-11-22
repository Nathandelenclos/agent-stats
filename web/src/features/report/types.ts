// Shapes of GET /api/report and GET /api/meta. Costs are USD, timestamps Unix milliseconds (0 when unknown).

export interface Usage {
  requests: number;
  failed: number;
  input: number;
  output: number;
  cacheRead: number;
  cacheWrite: number;
  cost: number;
  unpriced: number;
  avgDurationMs: number;
  avgTtftMs: number;
  cacheRate: number;
  firstTs: number;
  lastTs: number;
}

export interface UsageGroup extends Usage {
  key: string;
}

export interface Tool {
  name: string;
  server: string;
  calls: number;
  errors: number;
  argsChars: number;
  resultChars: number;
  avgMs: number;
  lastTs: number;
}

export interface Report {
  total: Usage;
  byHarness: UsageGroup[];
  byModel: UsageGroup[];
  byFolder: UsageGroup[];
  byAgentType: UsageGroup[];
  tools: Tool[];
  /** One group per local day with activity, oldest first; key is 2006-01-02. */
  daily: UsageGroup[];
}

export interface Meta {
  harnesses: string[];
  defaults: { since: string; harness: string };
}

export interface Filters {
  since: string; // "" for all time
  harness: string; // "" for all harnesses
}

/** Tokens the model read in: uncached, read from cache and written to cache. */
export const inputTokens = (u: Usage): number => u.input + u.cacheRead + u.cacheWrite;
