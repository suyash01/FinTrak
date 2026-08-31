import type { Account } from "@/types";

export interface AccountGroup {
  typeName: string;
  accounts: Account[];
}

// groupAccountsByType buckets accounts by their account type name (falling
// back to the type id), sorts the accounts by name within each group, and
// orders the groups alphabetically by type name. Used by AccountSelect so
// every account dropdown in the app groups by account type consistently.
export function groupAccountsByType(accounts: Account[]): AccountGroup[] {
  const byType = new Map<string, Account[]>();
  for (const a of accounts) {
    const key = a.accountTypeName || a.accountTypeId;
    const list = byType.get(key);
    if (list) list.push(a);
    else byType.set(key, [a]);
  }
  return [...byType.entries()]
    .map(([typeName, list]) => ({
      typeName,
      accounts: list.sort((x, y) => x.name.localeCompare(y.name)),
    }))
    .sort((a, b) => a.typeName.localeCompare(b.typeName));
}