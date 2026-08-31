import { useMemo, type ReactNode } from "react";
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectLabel,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import type { Account } from "@/types";
import { groupAccountsByType } from "@/utils/accountGroups";

interface AccountSelectProps {
  accounts: Account[];
  value: string;
  onValueChange: (value: string) => void;
  placeholder?: string;
  triggerClassName?: string;
  // Optional leading items rendered before the grouped sections (e.g. an
  // "All Accounts" / "No linked account" sentinel SelectItem).
  extraItems?: ReactNode;
}

// AccountSelect renders an account picker grouped by account type, keeping the
// grouping consistent across every account dropdown in the app (Transactions
// filter, Dashboard, Import, Payees, link/edit modals, Paperless import).
export default function AccountSelect({
  accounts,
  value,
  onValueChange,
  placeholder,
  triggerClassName,
  extraItems,
}: AccountSelectProps) {
  const groups = useMemo(() => groupAccountsByType(accounts), [accounts]);

  return (
    <Select value={value} onValueChange={onValueChange}>
      <SelectTrigger className={triggerClassName}>
        <SelectValue placeholder={placeholder} />
      </SelectTrigger>
      <SelectContent>
        {extraItems}
        {groups.map((g) => (
          <SelectGroup key={g.typeName}>
            <SelectLabel>{g.typeName}</SelectLabel>
            {g.accounts.map((a) => (
              <SelectItem key={a.id} value={a.id}>
                <span
                  className={a.closed ? "text-muted-foreground" : undefined}
                >
                  {a.name}
                </span>
              </SelectItem>
            ))}
          </SelectGroup>
        ))}
      </SelectContent>
    </Select>
  );
}