// Compatibility adapter for TanStack Table v9.
//
// The app was written against the v8 API. v9 ships a `legacy` entrypoint that
// preserves those signatures (useLegacyTable + the Legacy* types); this module
// re-exports it under the v8 names so the components don't need to change.
// State/`flexRender` come from the main package, which re-exports table-core.
export {
  useLegacyTable as useReactTable,
  getCoreRowModel,
  getSortedRowModel,
  getPaginationRowModel,
  legacyCreateColumnHelper as createColumnHelper,
  type LegacyCell as Cell,
  type LegacyColumn as Column,
  type LegacyColumnDef as ColumnDef,
  type LegacyFeatures,
  type LegacyHeader as Header,
  type LegacyHeaderGroup as HeaderGroup,
  type LegacyRow as Row,
  type LegacyTable as Table,
  type LegacyReactTable as ReactTable,
} from "@tanstack/react-table/legacy";

export { flexRender } from "@tanstack/react-table";
export {
  type CellData,
  type OnChangeFn,
  type PaginationState,
  type RowData,
  type RowSelectionState,
  type SortingState,
  type TableFeatures,
} from "@tanstack/react-table";
