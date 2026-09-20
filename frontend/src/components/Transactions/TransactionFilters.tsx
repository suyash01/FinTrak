import { useMemo } from "react";
import { Search, Folder, Tag } from "lucide-react";
import { Input } from "@/components/ui/input";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import MultiSelect, {
  type MultiSelectGroup,
} from "@/components/ui/multi-select";
import { UNCATEGORIZED, type CategorySection } from "../../lib/categories";
import { groupAccountsByType } from "../../utils/accountGroups";
import type { Account, Payee, TagCount } from "../../types";
import {
  PAGE_SIZE_OPTIONS,
  parseFilterList,
} from "./transactionConstants";

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
// picker floated to the right. The account, category, payee, and tag filters
// are multi-select: each holds a comma-separated list of ids (or tag names) and
// the API matches a transaction satisfying any entry.
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

  const accountGroups = useMemo<MultiSelectGroup[]>(
    () =>
      groupAccountsByType(accounts).map((group) => ({
        label: group.typeName,
        options: group.accounts.map((a) => ({
          value: a.id,
          triggerLabel: a.name,
          label: (
            <span className={a.closed ? "text-muted-foreground" : undefined}>
              {a.name}
            </span>
          ),
        })),
      })),
    [accounts],
  );

  // One dropdown holds groups and their categories: picking a group filters by
  // the whole group, picking a category filters by that category.
  const categoryGroups = useMemo<MultiSelectGroup[]>(
    () => [
      {
        options: [
          {
            value: UNCATEGORIZED,
            triggerLabel: "Uncategorized",
            label: <span className="font-semibold">Uncategorized</span>,
          },
        ],
      },
      ...categorySections.map((section) => ({
        options: [
          {
            value: section.group.id,
            triggerLabel: section.group.name,
            label: (
              <span className="flex items-center gap-2 font-semibold">
                <Folder size={12} className="text-muted-foreground" />
                {section.group.name}
              </span>
            ),
          },
          ...section.items.map((c) => ({
            value: c.id,
            triggerLabel: c.name,
            label: c.name,
            inset: true,
          })),
        ],
      })),
    ],
    [categorySections],
  );

  const payeeGroups = useMemo<MultiSelectGroup[]>(
    () => [
      {
        options: payees.map((p) => ({
          value: p.id,
          triggerLabel: p.name,
          label: p.name,
        })),
      },
    ],
    [payees],
  );

  const tagGroups = useMemo<MultiSelectGroup[]>(
    () => [
      {
        options: tags.map((t) => ({
          value: t.name,
          triggerLabel: t.name,
          label: (
            <span className="flex items-center gap-2">
              <Tag size={12} className="text-muted-foreground" />
              {t.name}
              <span className="text-muted-foreground">({t.count})</span>
            </span>
          ),
        })),
      },
    ],
    [tags],
  );

  return (
    <>
      <div className={`relative w-full ${compactLayout ? "mb-3" : "mb-5"}`}>
        <Search className="absolute left-3 top-1/2 -translate-y-1/2 w-4 h-4 text-muted-foreground" />
        <Input
          className={`pl-9 ${compactLayout ? "h-8" : "h-10"} bg-background`}
          placeholder="Search descriptions, notes, payees, tags..."
          aria-label="Search transactions by description, notes, payee, or tag"
          value={filters.search}
          onChange={(e) => onFilterChange("search", e.target.value)}
        />
      </div>
      <div
        className={`flex flex-wrap items-center ${compactLayout ? "gap-2 mb-3" : "gap-3 mb-5"}`}
      >
        <MultiSelect
          values={parseFilterList(filters.accountId)}
          onValuesChange={(values) =>
            onFilterChange("accountId", values.join(","))
          }
          groups={accountGroups}
          placeholder="All Accounts"
          ariaLabel="Filter by account"
          triggerClassName={`${triggerHeight} bg-background`}
        />
        <MultiSelect
          values={[
            ...parseFilterList(filters.categoryId),
            ...parseFilterList(filters.groupId),
          ]}
          onValuesChange={(values) => {
            // The API takes groups and categories in two parameters, and
            // matches any of them, so a selection is split by kind.
            const selectedGroups = values.filter((v) => groupIds.has(v));
            const selectedCategories = values.filter((v) => !groupIds.has(v));
            onFilterChange("categoryId", selectedCategories.join(","));
            onFilterChange("groupId", selectedGroups.join(","));
          }}
          groups={categoryGroups}
          placeholder="All Categories"
          ariaLabel="Filter by category"
          triggerClassName={`${triggerHeight} bg-background`}
        />
        <MultiSelect
          values={parseFilterList(filters.payeeId)}
          onValuesChange={(values) =>
            onFilterChange("payeeId", values.join(","))
          }
          groups={payeeGroups}
          placeholder="All Payees"
          ariaLabel="Filter by payee"
          triggerClassName={`${triggerHeight} bg-background`}
        />
        <MultiSelect
          values={parseFilterList(filters.tags)}
          onValuesChange={(values) => onFilterChange("tags", values.join(","))}
          groups={tagGroups}
          placeholder="All Tags"
          ariaLabel="Filter by tag"
          triggerClassName={`${triggerHeight} bg-background`}
        />
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
