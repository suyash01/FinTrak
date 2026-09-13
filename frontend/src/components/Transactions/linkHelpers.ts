import type { LinkType, Transaction } from "../../types";

// Badge colors for the four link types, shared by the existing-links list.
export function linkTypeBadgeClass(type: LinkType | string): string {
  switch (type) {
    case "transfer":
      return "bg-primary/10 text-primary";
    case "cashback":
      return "bg-emerald-500/10 text-emerald-400";
    case "refund":
      return "bg-amber-500/10 text-amber-400";
    default:
      return "bg-sky-500/10 text-sky-400";
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
