import { Search, Folder, Tag } from "lucide-react";
import { Input } from "@/components/ui/input";
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import AccountSelect from "@/components/AccountSelect/AccountSelect";
import type { CategorySection } from "../../lib/categories";
import type { Account, Payee, TagCount } from "../../types";
import { PAGE_SIZE_OPTIONS } from "./transactionConstants";

interface TransactionFiltersProps {
  compactLayout: boolean;
  filters: Record<string, string | number>;
  onFilterChange: (key: string, value: string) => void;
  accounts: Account[];
  payees: Payee[];
  tags: TagCount[];
  categorySections: CategorySection[];
  groupIds: Set<string>;
  preset: string;
  customInput: string;
  onPresetChange: (value: string) => void;
  onCustomInputChange: (value: string) => void;
  onCommitCustom: () => void;
}

// Search + filter controls above the transactions table, plus the rows-per-page
// picker floated to the right.
export default function TransactionFilters({
  compactLayout,
  filters,
  onFilterChange,
  accounts,
  payees,
  tags,
  categorySections,
  groupIds,
  preset,
  customInput,
  onPresetChange,
  onCustomInputChange,
  onCommitCustom,
}: TransactionFiltersProps) {
  const triggerHeight = compactLayout ? "h-8" : "h-10";

  return (
    <>
      <div className={`relative w-full ${compactLayout ? "mb-3" : "mb-5"}`}>
        <Search className="absolute left-3 top-1/2 -translate-y-1/2 w-4 h-4 text-muted-foreground" />
        <Input
          className={`pl-9 ${compactLayout ? "h-8" : "h-10"} bg-background`}
          placeholder="Search descriptions..."
          aria-label="Search transactions by description"
          value={filters.search}
          onChange={(e) => onFilterChange("search", e.target.value)}
        />
      </div>
      <div
        className={`flex flex-wrap items-center ${compactLayout ? "gap-2 mb-3" : "gap-3 mb-5"}`}
      >
        <AccountSelect
          accounts={accounts}
          value={String(filters.accountId || "all")}
          onValueChange={(v) => onFilterChange("accountId", v === "all" ? "" : v)}
          placeholder="All Accounts"
          ariaLabel="Filter by account"
          triggerClassName={`${triggerHeight} bg-background`}
          extraItems={<SelectItem value="all">All Accounts</SelectItem>}
        />
        <Select
          value={String(filters.groupId || filters.categoryId || "all")}
          onValueChange={(v) => {
            if (v === "all") {
              onFilterChange("categoryId", "");
              onFilterChange("groupId", "");
            } else if (groupIds.has(v)) {
              onFilterChange("categoryId", "");
              onFilterChange("groupId", v);
            } else {
              onFilterChange("groupId", "");
              onFilterChange("categoryId", v);
            }
          }}
        >
          <SelectTrigger
            aria-label="Filter by category"
            className={`${triggerHeight} bg-background`}
          >
            <SelectValue placeholder="All Categories" />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="all">All Categories</SelectItem>
            <SelectItem value="uncategorized" className="font-semibold">
              Uncategorized
            </SelectItem>
            {categorySections.map((s) => (
              <SelectGroup key={s.group.id}>
                <SelectItem value={s.group.id} className="font-semibold">
                  <span className="flex items-center gap-2">
                    <Folder size={12} className="text-muted-foreground" />
                    {s.group.name}
                  </span>
                </SelectItem>
                {s.items.map((c) => (
                  <SelectItem key={c.id} value={c.id}>
                    {c.name}
                  </SelectItem>
                ))}
              </SelectGroup>
            ))}
          </SelectContent>
        </Select>
        <Select
          value={String(filters.payeeId || "all")}
          onValueChange={(v) => onFilterChange("payeeId", v === "all" ? "" : v)}
        >
          <SelectTrigger
            aria-label="Filter by payee"
            className={`${triggerHeight} bg-background`}
          >
            <SelectValue placeholder="All Payees" />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="all">All Payees</SelectItem>
            {payees.map((p) => (
              <SelectItem key={p.id} value={p.id}>
                {p.name}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        <Select
          value={String(filters.tags || "all")}
          onValueChange={(v) => onFilterChange("tags", v === "all" ? "" : v)}
        >
          <SelectTrigger
            aria-label="Filter by tag"
            className={`${triggerHeight} bg-background`}
          >
            <SelectValue placeholder="All Tags" />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="all">All Tags</SelectItem>
            {tags.map((t) => (
              <SelectItem key={t.name} value={t.name}>
                <span className="flex items-center gap-2">
                  <Tag size={12} className="text-muted-foreground" />
                  {t.name}
                  <span className="text-muted-foreground">({t.count})</span>
                </span>
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        <Select
          value={String(filters.type || "all")}
          onValueChange={(v) => onFilterChange("type", v === "all" ? "" : v)}
        >
          <SelectTrigger
            aria-label="Filter by type"
            className={`${triggerHeight} bg-background`}
          >
            <SelectValue placeholder="All Types" />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="all">All Types</SelectItem>
            <SelectItem value="debit">Debit</SelectItem>
            <SelectItem value="credit">Credit</SelectItem>
          </SelectContent>
        </Select>
        <Select
          value={String(filters.linked || "all")}
          onValueChange={(v) => onFilterChange("linked", v === "all" ? "" : v)}
        >
          <SelectTrigger
            aria-label="Filter by link status"
            className={`${triggerHeight} bg-background`}
          >
            <SelectValue placeholder="All Link Status" />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="all">All Link Status</SelectItem>
            <SelectItem value="true">Linked Only</SelectItem>
            <SelectItem value="false">Not Linked Only</SelectItem>
          </SelectContent>
        </Select>
        <Input
          type="date"
          className={`w-auto ${compactLayout ? "h-9" : "h-10"} bg-background`}
          value={filters.dateFrom}
          onChange={(e) => onFilterChange("dateFrom", e.target.value)}
          title="From date"
          aria-label="From date"
        />
        <Input
          type="date"
          className={`w-auto ${compactLayout ? "h-9" : "h-10"} bg-background`}
          value={filters.dateTo}
          onChange={(e) => onFilterChange("dateTo", e.target.value)}
          title="To date"
          aria-label="To date"
        />

        {/* Page size control, floated right to stay visually separate */}
        <div className="ml-auto flex items-center gap-2">
          <label
            htmlFor="rows-per-page"
            className="text-sm text-muted-foreground"
          >
            Rows per page
          </label>
          <Select value={preset} onValueChange={onPresetChange}>
            <SelectTrigger
              id="rows-per-page"
              className={`${compactLayout ? "h-8" : "h-9"} bg-background cursor-pointer`}
            >
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {PAGE_SIZE_OPTIONS.map((o) => (
                <SelectItem key={o} value={String(o)}>
                  {o}
                </SelectItem>
              ))}
              <SelectItem value="custom">Custom...</SelectItem>
            </SelectContent>
          </Select>
          {preset === "custom" && (
            <Input
              type="number"
              min="1"
              max="1000"
              value={customInput}
              onChange={(e) => onCustomInputChange(e.target.value)}
              onBlur={onCommitCustom}
              onKeyDown={(e) => e.key === "Enter" && onCommitCustom()}
              placeholder="Custom"
              aria-label="Custom rows per page"
              className={`w-24 ${compactLayout ? "h-8" : "h-9"} bg-background`}
            />
          )}
        </div>
      </div>
    </>
  );
}
