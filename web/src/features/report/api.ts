import { keepPreviousData, useQuery } from "@tanstack/react-query";
import { getJSON, shouldRetry } from "../../shared/api";
import type { Filters, Meta, Report } from "./types";

/** What the filters can be: loaded once, it does not change while the server runs. */
export function useMeta() {
  return useQuery({
    queryKey: ["meta"],
    queryFn: ({ signal }) => getJSON<Meta>("/api/meta", {}, signal),
    staleTime: Infinity,
    retry: shouldRetry,
  });
}

/** The report for the filters; the previous one stays on screen while a new one loads. */
export function useReport(filters: Filters) {
  return useQuery({
    queryKey: ["report", filters.since, filters.harness, filters.scope],
    queryFn: ({ signal }) => getJSON<Report>("/api/report", { since: filters.since, harness: filters.harness, project: filters.scope }, signal),
    placeholderData: keepPreviousData,
    retry: shouldRetry,
  });
}
