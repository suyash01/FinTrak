import { Search } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Checkbox } from "@/components/ui/checkbox";
import { SelectItem } from "@/components/ui/select";
import AccountSelect from "@/components/AccountSelect/AccountSelect";
import type { Account } from "../../types";

interface LinkSearchPanelProps {
  search: string;
  onSearchChange: (value: string) => void;
  accountId: string;
  accounts: Account[];
  dateFrom: string;
  dateTo: string;
  onDateFromChange: (value: string) => void;
  onDateToChange: (value: string) => void;
  matchAmount: boolean;
  excludeSameAccount: boolean;
  onMatchAmountChange: (checked: boolean) => void;
  onExcludeSameAccountChange: (checked: boolean) => void;
  onAccountChange: (accountId: string) => void;
  onSubmit: () => void;
}

export default function LinkSearchPanel({
  search,
  onSearchChange,
  accountId,
  accounts,
  dateFrom,
  dateTo,
  onDateFromChange,
  onDateToChange,
  matchAmount,
  excludeSameAccount,
  onMatchAmountChange,
  onExcludeSameAccountChange,
  onAccountChange,
  onSubmit,
}: LinkSearchPanelProps) {
  return (
    <div className="p-4 border-b border-border bg-card">
      <div className="flex flex-col gap-4">
        <div className="grid grid-cols-1 md:grid-cols-3 gap-3">
          <div className="relative md:col-span-3">
            <Search className="absolute left-3 top-1/2 -translate-y-1/2 w-4 h-4 text-muted-foreground" />
            <Input
              className="pl-9"
              placeholder="Search description..."
              aria-label="Search transactions by description"
              value={search}
              onChange={(e) => onSearchChange(e.target.value)}
              onKeyDown={(e) => e.key === "Enter" && onSubmit()}
            />
          </div>
          <div className="flex flex-row flex-wrap md:col-span-3 gap-3">
            <div className="flex items-center gap-2 bg-background border border-border rounded-lg px-3.5 py-2.5">
              <Checkbox
                id="matchAmount"
                checked={matchAmount}
                onCheckedChange={(checked) =>
                  onMatchAmountChange(checked === true)
                }
              />
              <Label
                htmlFor="matchAmount"
                className="text-sm text-muted-foreground cursor-pointer select-none"
              >
                Match Amount
              </Label>
            </div>

            <div className="flex items-center gap-2 bg-background border border-border rounded-lg px-3.5 py-2.5">
              <Checkbox
                id="excludeAccount"
                checked={excludeSameAccount}
                onCheckedChange={(checked) =>
                  onExcludeSameAccountChange(checked === true)
                }
              />
              <Label
                htmlFor="excludeAccount"
                className="text-sm text-muted-foreground cursor-pointer select-none"
              >
                Different Account Only
              </Label>
            </div>
            <AccountSelect
              accounts={accounts}
              value={accountId || "all"}
              onValueChange={onAccountChange}
              placeholder="All Accounts"
              ariaLabel="Filter by account"
              triggerClassName="w-full"
              extraItems={<SelectItem value="all">All Accounts</SelectItem>}
            />
          </div>
        </div>

        <div className="flex flex-col md:flex-row items-center gap-3">
          <div className="flex-1 flex items-center gap-2 p-1.5 bg-background border border-border rounded-xl w-full">
            <div className="pl-2.5 text-[10px] font-bold text-muted-foreground uppercase tracking-wider shrink-0">
              Date Range
            </div>
            <Input
              type="date"
              className="flex-1"
              aria-label="From date"
              value={dateFrom}
              onChange={(e) => onDateFromChange(e.target.value)}
            />
            <div className="text-muted-foreground text-xs px-0.5">to</div>
            <Input
              type="date"
              className="flex-1"
              aria-label="To date"
              value={dateTo}
              onChange={(e) => onDateToChange(e.target.value)}
            />
          </div>
          <Button
            onClick={onSubmit}
            className="w-full md:w-auto px-8 py-3 h-auto text-sm font-bold rounded-xl"
          >
            <Search size={16} />
            Find Match
          </Button>
        </div>
      </div>
    </div>
  );
}
