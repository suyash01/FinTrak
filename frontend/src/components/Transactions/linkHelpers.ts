import type { LinkType, Transaction } from "../../types";

// Badge colors for the four link types, shared by the existing-links list.
export function linkTypeBadgeClass(type: LinkType | string): string {
  switch (type) {
    case "transfer":
      return "bg-primary/10 text-primary";
    case "cashback":
      return "bg-chart-3/10 text-chart-3";
    case "refund":
      return "bg-amber-500/10 text-amber-700 dark:text-amber-300";
    default:
      return "bg-chart-2/10 text-chart-2";
  }
}

// Debits are always stored as a link's source so transfer direction stays
// consistent regardless of which side the user opened the modal from.
export function orderLinkEndpoints(
  txn: Transaction,
  target: Transaction,
): { fromId: string; toId: string } {
  if (txn.type === "credit" && target.type === "debit") {
    return { fromId: target.id, toId: txn.id };
  }
  return { fromId: txn.id, toId: target.id };
}
