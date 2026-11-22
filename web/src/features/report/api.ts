import { keepPreviousData, useQuery } from "@tanstack/react-query";
import { getJSON, shouldRetry } from "../../shared/api";
import type { Filters, Meta, Report } from "./types";

/** What the filters can be: loaded once, and again when the server has indexed more, which may teach it its project. */
export function useMeta() {
  return useQuery({
    queryKey: ["meta"],
    queryFn: ({ signal }) => getJSON<Meta>("/api/meta", {}, signal),
    staleTime: Infinity,
    retry: shouldRetry,
  });
}

/** The report for the filters, once there are some; the previous one stays on screen while a new one loads, and says which scope it covers. */
export function useReport(filters: Filters | undefined) {
  return useQuery({
    queryKey: ["report", filters?.since, filters?.harness, filters?.scope],
    queryFn: ({ signal }) => getJSON<Report>("/api/report", { since: filters!.since, harness: filters!.harness, project: filters!.scope }, signal),
    enabled: filters !== undefined,
    placeholderData: keepPreviousData,
    retry: shouldRetry,
  });
}
