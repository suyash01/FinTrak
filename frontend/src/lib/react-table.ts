// App-wide TanStack Table v9 configuration.
//
// Every table in the app declares its features here once, so components import
// the pre-bound `createColumnHelper` and column/row/table types below instead
// of repeating the feature set. Only the features the app actually uses are
// registered, which keeps the unused feature code out of the bundle.
import {
  columnVisibilityFeature,
  createColumnHelper as createColumnHelperBase,
  createPaginatedRowModel,
  createSortedRowModel,
  rowPaginationFeature,
  rowSelectionFeature,
  rowSortingFeature,
  sortFn_alphanumeric,
  sortFn_datetime,
  sortFn_text,
  tableFeatures,
} from "@tanstack/react-table";
import type {
  Cell as CellBase,
  CellData,
  Column as ColumnBase,
  ColumnDef as ColumnDefBase,
  Header as HeaderBase,
  HeaderGroup as HeaderGroupBase,
  ReactTable as ReactTableBase,
  Row as RowBase,
  RowData,
  Table as TableBase,
  TableState,
} from "@tanstack/react-table";

export { useTable, flexRender } from "@tanstack/react-table";
export type {
  OnChangeFn,
  PaginationState,
  RowSelectionState,
  SortingState,
  CellData,
  RowData,
  TableState,
  TableFeatures,
} from "@tanstack/react-table";

export const features = tableFeatures({
  rowSortingFeature,
  rowPaginationFeature,
  rowSelectionFeature,
  // In v8 column visibility was always on; in v9 the `getVisibleLeafColumns`
  // and `row.getVisibleCells` APIs the renderer uses live behind this feature.
  columnVisibilityFeature,
  sortedRowModel: createSortedRowModel(),
  paginatedRowModel: createPaginatedRowModel(),
  sortFns: {
    text: sortFn_text,
    alphanumeric: sortFn_alphanumeric,
    datetime: sortFn_datetime,
  },
});

export type AppFeatures = typeof features;

export function createColumnHelper<TData extends RowData>() {
  return createColumnHelperBase<AppFeatures, TData>();
}

export type ColumnDef<
  TData extends RowData,
  TValue extends CellData = CellData,
> = ColumnDefBase<AppFeatures, TData, TValue>;
export type Column<
  TData extends RowData,
  TValue extends CellData = CellData,
> = ColumnBase<AppFeatures, TData, TValue>;
export type Row<TData extends RowData> = RowBase<AppFeatures, TData>;
export type Cell<
  TData extends RowData,
  TValue extends CellData = CellData,
> = CellBase<AppFeatures, TData, TValue>;
export type Header<
  TData extends RowData,
  TValue extends CellData = CellData,
> = HeaderBase<AppFeatures, TData, TValue>;
export type HeaderGroup<TData extends RowData> = HeaderGroupBase<
  AppFeatures,
  TData
>;
export type Table<TData extends RowData> = TableBase<AppFeatures, TData>;
export type ReactTable<
  TData extends RowData,
  TSelected = TableState<AppFeatures>,
> = ReactTableBase<AppFeatures, TData, TSelected>;
