import * as React from "react";
import {
  type Column,
  type ColumnDef,
  type OnChangeFn,
  type PaginationState,
  type Row,
  type RowData,
  type RowSelectionState,
  type SortingState,
  type Table as TanstackTable,
  flexRender,
  getCoreRowModel,
  getPaginationRowModel,
  getSortedRowModel,
  useReactTable,
} from "@tanstack/react-table";
import { useVirtualizer } from "@tanstack/react-virtual";
import { ChevronDown, ChevronUp } from "lucide-react";
import { cn } from "@/lib/utils";
import { Button } from "@/components/ui/button";
import { Spinner } from "@/components/ui/spinner";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";

declare module "@tanstack/react-table" {
  // eslint-disable-next-line @typescript-eslint/no-unused-vars
  interface ColumnMeta<TData extends RowData, TValue> {
    headerClassName?: string;
    cellClassName?: string;
  }
}

interface DataTableProps<TData, TValue> {
  columns: ColumnDef<TData, TValue>[];
  data: TData[];
  loading?: boolean;
  emptyMessage?: string;
  getRowId?: (originalRow: TData, index: number, parent?: Row<TData>) => string;
  enableRowSelection?: boolean | ((row: Row<TData>) => boolean);
  getRowClassName?: (row: Row<TData>) => string | undefined;
  // Controlled sorting (manual/server-side)
  sorting?: SortingState;
  onSortingChange?: OnChangeFn<SortingState>;
  manualSorting?: boolean;
  // Controlled pagination (manual/server-side)
  pagination?: PaginationState;
  onPaginationChange?: OnChangeFn<PaginationState>;
  manualPagination?: boolean;
  rowCount?: number;
  pageCount?: number;
  // Controlled row selection
  rowSelection?: RowSelectionState;
  onRowSelectionChange?: OnChangeFn<RowSelectionState>;
  // Styling
  containerClassName?: string;
  tableClassName?: string;
  theadClassName?: string;
  headerClassName?: string;
  cellClassName?: string;
  footer?: (table: TanstackTable<TData>) => React.ReactNode;
  // Row virtualization: when enabled, only the rows near the scroll position
  // are rendered inside a vertically scrollable region bounded by `maxHeight`,
  // which keeps large pages (e.g. 1000 transactions) light. `estimateRowSize`
  // seeds each row's height before the real rows are measured.
  virtualize?: boolean;
  maxHeight?: string | number;
  estimateRowSize?: number;
}

export function DataTable<TData, TValue>({
  columns,
  data,
  loading = false,
  emptyMessage = "No results.",
  getRowId,
  enableRowSelection,
  getRowClassName,
  sorting,
  onSortingChange,
  manualSorting = false,
  pagination,
  onPaginationChange,
  manualPagination = false,
  rowCount,
  pageCount,
  rowSelection,
  onRowSelectionChange,
  containerClassName,
  tableClassName,
  theadClassName,
  headerClassName,
  cellClassName,
  footer,
  virtualize = false,
  maxHeight = 480,
  estimateRowSize = 48,
}: DataTableProps<TData, TValue>) {
  const useSorting = sorting !== undefined || onSortingChange !== undefined;
  const usePagination =
    pagination !== undefined || onPaginationChange !== undefined;

  const table = useReactTable({
    data,
    columns,
    getRowId,
    state: { sorting, pagination, rowSelection },
    onSortingChange,
    onPaginationChange,
    onRowSelectionChange,
    getCoreRowModel: getCoreRowModel(),
    getSortedRowModel:
      !manualSorting && useSorting ? getSortedRowModel() : undefined,
    getPaginationRowModel:
      !manualPagination && usePagination ? getPaginationRowModel() : undefined,
    manualSorting,
    manualPagination,
    rowCount,
    pageCount,
    enableRowSelection,
  });

  const columnCount = table.getVisibleLeafColumns().length;

  const scrollRef = React.useRef<HTMLDivElement>(null);
  const rows = table.getRowModel().rows;
  const virtualizer = useVirtualizer({
    count: rows.length,
    getScrollElement: () => scrollRef.current,
    estimateSize: () => estimateRowSize,
    getItemKey: (index) => {
      const row = rows[index];
      return row ? getRowId?.(row.original, index) ?? index : index;
    },
    overscan: 10,
  });
  const virtualItems = virtualize ? virtualizer.getVirtualItems() : [];
  const totalSize = virtualizer.getTotalSize();
  const measureRow = React.useCallback(
    (el: HTMLTableRowElement | null) => {
      if (el) virtualizer.measureElement(el);
    },
    [virtualizer],
  );

  const renderRow = (row: Row<TData>, dataIndex?: number) => (
    <TableRow
      key={row.id}
      data-index={dataIndex}
      ref={dataIndex !== undefined ? measureRow : undefined}
      className={cn("border-border", getRowClassName?.(row))}
    >
      {row.getVisibleCells().map((cell) => (
        <TableCell
          key={cell.id}
          className={cn(
            cellClassName,
            cell.column.columnDef.meta?.cellClassName,
          )}
        >
          {flexRender(cell.column.columnDef.cell, cell.getContext())}
        </TableCell>
      ))}
    </TableRow>
  );

  const tableElement = (
    <Table
      className={tableClassName}
      wrapperClassName={virtualize ? "relative w-full" : undefined}
    >
      <TableHeader
        className={cn(
          virtualize && "sticky top-0 z-10 bg-card",
          theadClassName,
        )}
      >
        {table.getHeaderGroups().map((headerGroup) => (
          <TableRow key={headerGroup.id} className="hover:bg-transparent">
            {headerGroup.headers.map((header) => (
              <TableHead
                key={header.id}
                className={cn(
                  headerClassName,
                  header.column.columnDef.meta?.headerClassName,
                )}
              >
                {header.isPlaceholder
                  ? null
                  : flexRender(
                      header.column.columnDef.header,
                      header.getContext(),
                    )}
              </TableHead>
            ))}
          </TableRow>
        ))}
      </TableHeader>
      <TableBody>
        {loading ? (
          <TableRow>
            <TableCell
              colSpan={columnCount}
              className="text-center p-10"
            >
              <Spinner className="mx-auto size-8 text-primary" />
            </TableCell>
          </TableRow>
        ) : rows.length === 0 ? (
          <TableRow>
            <TableCell
              colSpan={columnCount}
              className="text-center p-10 text-muted-foreground"
            >
              {emptyMessage}
            </TableCell>
          </TableRow>
        ) : virtualize ? (
          <>
            {virtualItems.length > 0 && virtualItems[0].start > 0 && (
              <TableRow
                aria-hidden
                className="pointer-events-none"
                style={{ height: virtualItems[0].start }}
              >
                <TableCell colSpan={columnCount} className="p-0 border-0" />
              </TableRow>
            )}
            {virtualItems.map((vi) => renderRow(rows[vi.index], vi.index))}
            {virtualItems.length > 0 &&
              totalSize - virtualItems[virtualItems.length - 1].end > 0 && (
                <TableRow
                  aria-hidden
                  className="pointer-events-none"
                  style={{
                    height:
                      totalSize - virtualItems[virtualItems.length - 1].end,
                  }}
                >
                  <TableCell colSpan={columnCount} className="p-0 border-0" />
                </TableRow>
              )}
          </>
        ) : (
          rows.map((row) => renderRow(row))
        )}
      </TableBody>
    </Table>
  );

  return (
    <>
      <div className={containerClassName}>
        {virtualize ? (
          <div
            ref={scrollRef}
            className="relative w-full"
            style={{ maxHeight, overflow: "auto", overflowAnchor: "none" }}
          >
            {tableElement}
          </div>
        ) : (
          tableElement
        )}
      </div>
      {footer?.(table)}
    </>
  );
}

export function DataTableColumnHeader<TData, TValue>({
  column,
  title,
  className,
}: {
  column: Column<TData, TValue>;
  title: string;
  className?: string;
}) {
  const sorted = column.getIsSorted();
  const canSort = column.getCanSort();
  return (
    <button
      type="button"
      disabled={!canSort}
      onClick={() => {
        if (sorted === "desc") {
          column.toggleSorting(false);
        } else {
          column.toggleSorting(true);
        }
      }}
      className={cn(
        "inline-flex items-center gap-1 select-none whitespace-nowrap",
        canSort ? "cursor-pointer hover:text-foreground" : "cursor-default",
        className,
      )}
    >
      {title}
      {sorted === "asc" ? (
        <ChevronUp size={14} />
      ) : sorted === "desc" ? (
        <ChevronDown size={14} />
      ) : null}
    </button>
  );
}

export function DataTablePagination<TData>({
  table,
}: {
  table: TanstackTable<TData>;
}) {
  const pageIndex = table.getState().pagination.pageIndex;
  const pageCount = table.getPageCount();
  const rowCount = table.getRowCount();
  const page = pageIndex + 1;

  return (
    <div className="flex items-center justify-between mt-4 mb-4">
      <div className="text-sm text-muted-foreground">
        Page {page} of {pageCount} ({rowCount} total)
      </div>
      <div className="flex gap-1.5">
        <Button
          size="sm"
          variant="ghost"
          disabled={!table.getCanPreviousPage()}
          onClick={() => table.previousPage()}
        >
          Prev
        </Button>
        {Array.from({ length: Math.min(pageCount, 7) }, (_, i) => {
          let pageNum: number;
          if (pageCount <= 7) {
            pageNum = i + 1;
          } else if (page <= 4) {
            pageNum = i + 1;
          } else if (page >= pageCount - 3) {
            pageNum = pageCount - 6 + i;
          } else {
            pageNum = page - 3 + i;
          }
          return (
            <Button
              key={pageNum}
              size="sm"
              variant={page === pageNum ? "default" : "ghost"}
              onClick={() => table.setPageIndex(pageNum - 1)}
            >
              {pageNum}
            </Button>
          );
        })}
        <Button
          size="sm"
          variant="ghost"
          disabled={!table.getCanNextPage()}
          onClick={() => table.nextPage()}
        >
          Next
        </Button>
      </div>
    </div>
  );
}