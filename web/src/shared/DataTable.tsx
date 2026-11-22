import {
  createColumnHelper,
  createSortedRowModel,
  rowSortingFeature,
  sortFns,
  tableFeatures,
  useTable,
  type ColumnDef,
  type RowData,
} from "@tanstack/react-table";
import { useState, type ReactNode } from "react";

const features = tableFeatures({
  rowSortingFeature,
  sortedRowModel: createSortedRowModel(),
  sortFns,
});

export type Column<Row extends RowData> = {
  /** Stable id, also the sort key. */
  id: string;
  header: string;
  /** The value rows are sorted by. */
  value: (row: Row) => string | number;
  /** How the value reads in the cell; defaults to the value. */
  render?: (row: Row) => ReactNode;
  /** Numbers read right-aligned. */
  numeric?: boolean;
  /** Long text is cut to one line, and its full text stays in the tooltip. */
  truncate?: boolean;
};

interface Props<Row extends RowData> {
  /** Names the table for assistive technology; also shown as the heading above it. */
  caption: string;
  columns: Column<Row>[];
  rows: Row[];
  rowKey: (row: Row) => string;
  /** The column sorted first, highest first. */
  initialSort?: string;
  /** Rows shown before the "Show all" button. */
  initialLimit?: number;
}

/** A table whose columns sort on click or Enter, with one scrollable keyboard-reachable region around it. */
export function DataTable<Row extends RowData>({ caption, columns, rows, rowKey, initialSort, initialLimit = 15 }: Props<Row>) {
  const [showAll, setShowAll] = useState(false);
  const helper = createColumnHelper<typeof features, Row>();
  const defs = columns.map((c) =>
    helper.accessor((row) => c.value(row), { id: c.id, header: c.header, sortDescFirst: c.numeric ?? false }),
  ) as ColumnDef<typeof features, Row>[];
  const table = useTable({
    features,
    columns: defs,
    data: rows,
    getRowId: rowKey,
    initialState: { sorting: initialSort ? [{ id: initialSort, desc: true }] : [] },
  });

  const sorted = table.getRowModel().rows;
  const shown = showAll ? sorted : sorted.slice(0, initialLimit);
  const byId = new Map(columns.map((c) => [c.id, c]));

  return (
    <div>
      <div
        role="region"
        aria-label={`${caption}, scrollable`}
        // eslint-disable-next-line jsx-a11y/no-noninteractive-tabindex -- a scrollable region must be reachable by keyboard
        tabIndex={0}
        className="overflow-x-auto rounded-lg border border-rule"
      >
        <table className="w-full border-collapse text-sm">
          <caption className="sr-only">{caption}</caption>
          <thead className="bg-surface">
            {table.getHeaderGroups().map((group) => (
              <tr key={group.id}>
                {group.headers.map((header) => {
                  const col = byId.get(header.column.id);
                  const state = header.column.getIsSorted();
                  return (
                    <th
                      key={header.id}
                      scope="col"
                      aria-sort={state === "asc" ? "ascending" : state === "desc" ? "descending" : "none"}
                      className={`whitespace-nowrap border-b border-rule p-0 font-semibold ${col?.numeric ? "text-right" : "text-left"}`}
                    >
                      <button
                        type="button"
                        onClick={header.column.getToggleSortingHandler()}
                        className={`inline-flex min-h-9 w-full items-center gap-1 px-3 py-1.5 font-semibold hover:bg-rule ${col?.numeric ? "justify-end" : "justify-start"}`}
                      >
                        {col?.header}
                        <span aria-hidden="true" className="w-3 text-xs text-muted">
                          {state === "asc" ? "▲" : state === "desc" ? "▼" : ""}
                        </span>
                      </button>
                    </th>
                  );
                })}
              </tr>
            ))}
          </thead>
          <tbody>
            {shown.map((row) => (
              <tr key={row.id} className="border-b border-rule last:border-b-0">
                {columns.map((c) => {
                  const content = c.render ? c.render(row.original) : String(c.value(row.original));
                  const title = typeof content === "string" ? content : String(c.value(row.original));
                  return (
                    <td
                      key={c.id}
                      title={c.truncate ? title : undefined}
                      className={`px-3 py-1.5 ${c.numeric ? "text-right tabular-nums" : "text-left"} ${c.truncate ? "max-w-[28rem] truncate" : "whitespace-nowrap"}`}
                    >
                      {content === "" ? <span className="text-muted">(none)</span> : content}
                    </td>
                  );
                })}
              </tr>
            ))}
            {shown.length === 0 && (
              <tr>
                <td colSpan={columns.length} className="px-3 py-4 text-center text-muted">
                  Nothing in this period.
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>
      {sorted.length > initialLimit && (
        <button
          type="button"
          onClick={() => setShowAll((v) => !v)}
          aria-expanded={showAll}
          className="mt-2 min-h-9 rounded-md border border-border px-3 text-sm hover:bg-surface"
        >
          {showAll ? `Show the first ${initialLimit}` : `Show all ${sorted.length}`}
        </button>
      )}
    </div>
  );
}
