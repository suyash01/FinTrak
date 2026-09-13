import { describe, it, expect, vi } from "vitest";
import { useState } from "react";
import { render, screen, fireEvent, within } from "@testing-library/react";
import {
  DataTable,
  DataTableColumnHeader,
  DataTablePagination,
} from "./data-table";
import {
  createColumnHelper,
  type ColumnDef,
  type PaginationState,
  type RowSelectionState,
  type SortingState,
} from "@/lib/react-table";

interface Person {
  id: string;
  name: string;
  amount: number;
}

const people: Person[] = [
  { id: "1", name: "Charlie", amount: 300 },
  { id: "2", name: "Alice", amount: 100 },
  { id: "3", name: "Bob", amount: 200 },
];

const columnHelper = createColumnHelper<Person>();

function peopleColumns(): ColumnDef<Person, any>[] {
  return [
    columnHelper.accessor("name", {
      header: ({ column }) => (
        <DataTableColumnHeader column={column} title="Name" />
      ),
      cell: (info) => info.getValue(),
    }),
    columnHelper.accessor("amount", {
      header: ({ column }) => (
        <DataTableColumnHeader column={column} title="Amount" />
      ),
      cell: (info) => <span>{info.getValue()}</span>,
    }),
  ];
}

// First body cell of each rendered row, in visual order.
function renderedNames(): string[] {
  return screen
    .getAllByRole("row")
    .slice(1)
    .map((row) => within(row).getAllByRole("cell")[0]?.textContent ?? "");
}

describe("DataTable", () => {
  it("renders column headers and cell values", () => {
    render(
      <DataTable data={people} columns={peopleColumns()} getRowId={(p) => p.id} />,
    );

    expect(screen.getByRole("columnheader", { name: "Name" })).toBeInTheDocument();
    expect(screen.getByText("Alice")).toBeInTheDocument();
    expect(screen.getByText("300")).toBeInTheDocument();
  });

  it("shows the empty message when there are no rows", () => {
    render(<DataTable data={[]} columns={peopleColumns()} />);
    expect(screen.getByText("No results.")).toBeInTheDocument();
  });

  it("shows a custom empty message", () => {
    render(
      <DataTable
        data={[]}
        columns={peopleColumns()}
        emptyMessage="Nothing to see"
      />,
    );
    expect(screen.getByText("Nothing to see")).toBeInTheDocument();
  });

  it("shows a spinner instead of rows while loading", () => {
    render(<DataTable data={people} columns={peopleColumns()} loading />);
    expect(screen.getByRole("status", { name: "Loading" })).toBeInTheDocument();
    expect(screen.queryByText("Alice")).toBeNull();
  });

  it("applies getRowClassName to the rendered rows", () => {
    render(
      <DataTable
        data={people}
        columns={peopleColumns()}
        getRowId={(p) => p.id}
        getRowClassName={(row) => (row.original.id === "2" ? "row-selected" : "")}
      />,
    );
    expect(screen.getByText("Alice").closest("tr")).toHaveClass("row-selected");
  });

  it("sorts rows when a header is toggled and exposes aria-sort", () => {
    function Harness() {
      const [sorting, setSorting] = useState<SortingState>([]);
      return (
        <DataTable
          data={people}
          columns={peopleColumns()}
          getRowId={(p) => p.id}
          sorting={sorting}
          onSortingChange={setSorting}
        />
      );
    }
    render(<Harness />);

    expect(renderedNames()).toEqual(["Charlie", "Alice", "Bob"]);

    const nameHeader = screen.getByRole("columnheader", { name: "Name" });
    expect(nameHeader).toHaveAttribute("aria-sort", "none");

    // First click sorts descending; second flips to ascending.
    fireEvent.click(screen.getByRole("button", { name: "Name" }));
    expect(nameHeader).toHaveAttribute("aria-sort", "descending");
    expect(renderedNames()).toEqual(["Charlie", "Bob", "Alice"]);

    fireEvent.click(screen.getByRole("button", { name: "Name" }));
    expect(nameHeader).toHaveAttribute("aria-sort", "ascending");
    expect(renderedNames()).toEqual(["Alice", "Bob", "Charlie"]);
  });

  it("calls onSortingChange for controlled sorting", () => {
    const onSortingChange = vi.fn();
    render(
      <DataTable
        data={people}
        columns={peopleColumns()}
        sorting={[]}
        onSortingChange={onSortingChange}
      />,
    );
    fireEvent.click(screen.getByRole("button", { name: "Amount" }));
    expect(onSortingChange).toHaveBeenCalledTimes(1);
  });

  it("paginates through a controlled page state via the footer", () => {
    function Harness() {
      const [pagination, setPagination] = useState<PaginationState>({
        pageIndex: 0,
        pageSize: 2,
      });
      return (
        <DataTable
          data={people}
          columns={peopleColumns()}
          getRowId={(p) => p.id}
          pagination={pagination}
          onPaginationChange={setPagination}
          footer={(table) => <DataTablePagination table={table} />}
        />
      );
    }
    render(<Harness />);

    expect(screen.getByText("Page 1 of 2 (3 total)")).toBeInTheDocument();
    expect(renderedNames()).toEqual(["Charlie", "Alice"]);

    fireEvent.click(screen.getByRole("button", { name: "Next" }));
    expect(screen.getByText("Page 2 of 2 (3 total)")).toBeInTheDocument();
    expect(renderedNames()).toEqual(["Bob"]);

    fireEvent.click(screen.getByRole("button", { name: "Prev" }));
    expect(screen.getByText("Page 1 of 2 (3 total)")).toBeInTheDocument();

    // Direct page-number buttons jump to a page index.
    fireEvent.click(screen.getByRole("button", { name: "2" }));
    expect(screen.getByText("Page 2 of 2 (3 total)")).toBeInTheDocument();
  });

  it("tracks controlled row selection with per-row and select-all checkboxes", () => {
    function Harness() {
      const [rowSelection, setRowSelection] = useState<RowSelectionState>({});
      const columns: ColumnDef<Person, any>[] = [
        columnHelper.display({
          id: "select",
          header: ({ table }) => (
            <input
              type="checkbox"
              aria-label="select all"
              checked={table.getIsAllPageRowsSelected()}
              onChange={(e) => table.toggleAllPageRowsSelected(e.target.checked)}
            />
          ),
          cell: ({ row }) => (
            <input
              type="checkbox"
              aria-label={`select ${row.original.name}`}
              checked={row.getIsSelected()}
              onChange={(e) => row.toggleSelected(e.target.checked)}
            />
          ),
        }),
        ...peopleColumns(),
      ];
      return (
        <DataTable
          data={people}
          columns={columns}
          getRowId={(p) => p.id}
          enableRowSelection
          rowSelection={rowSelection}
          onRowSelectionChange={setRowSelection}
        />
      );
    }
    render(<Harness />);

    fireEvent.click(screen.getByLabelText("select Alice"));
    expect(screen.getByLabelText("select Alice")).toBeChecked();
    expect(screen.getByLabelText("select Bob")).not.toBeChecked();

    fireEvent.click(screen.getByLabelText("select all"));
    expect(screen.getByLabelText("select Bob")).toBeChecked();
    expect(screen.getByLabelText("select all")).toBeChecked();

    fireEvent.click(screen.getByLabelText("select all"));
    expect(screen.getByLabelText("select Alice")).not.toBeChecked();
  });

  it("falls back to rendering every row when virtualization has no measurable viewport", () => {
    render(
      <DataTable
        data={people}
        columns={peopleColumns()}
        getRowId={(p) => p.id}
        virtualize
      />,
    );
    // jsdom reports clientHeight 0, so the table must render all rows rather
    // than virtualizing to zero.
    expect(renderedNames()).toEqual(["Charlie", "Alice", "Bob"]);
  });
});
