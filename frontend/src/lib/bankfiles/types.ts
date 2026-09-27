import type { ImportTransaction } from "../../types";

/**
 * The bank-supplied file formats FinTrak reads. ISO 20022 `camt.053` is a
 * transaction report and `camt.052` is a statement (the same family, but it
 * also carries the period's opening and closing balances); OFX covers the older
 * `.ofx`/`.qfx` exports, whose 1.x flavour is SGML rather than XML.
 */
export type BankFileFormat = "camt.052" | "camt.053" | "ofx";

/**
 * Something the reader could not read, or had to drop to read at all.
 *
 * The readers never throw: a malformed statement should tell the user which
 * rows it could not use, exactly as the PDF parser's per-page reconciliation
 * warnings do, rather than failing the whole import.
 */
export interface ParseDiagnostic {
  /** Stable identifier, in the spirit of the query language's diagnostic codes. */
  code: ParseDiagnosticCode;
  message: string;
  /** How many rows or groups this concerns, when it is more than one. */
  count?: number;
}

export type ParseDiagnosticCode =
  | "unreadable_file"
  | "unsupported_format"
  | "no_transactions"
  | "row_no_amount"
  | "row_bad_amount"
  | "row_no_date"
  | "row_bad_date"
  | "row_no_direction"
  | "entry_no_details"
  | "interim_excluded"
  | "groups_truncated"
  | "rows_truncated";

/**
 * One account-period's worth of rows. A file holds one or more of these: OFX
 * allows several accounts and several periods in a single download, and a bank
 * that appends every page to one file produces several periods for one account.
 *
 * The identifying fields are shown to the user before the import is committed.
 * They do NOT route rows anywhere: every group is imported into the one account
 * chosen in the wizard, because the import endpoint takes a single account for
 * the whole batch.
 */
export interface StatementGroup {
  format: BankFileFormat;
  accountNumber?: string;
  accountHolder?: string;
  currency?: string;
  periodFrom?: string;
  periodTo?: string;
  /** camt.052 only: the balances the statement itself printed. */
  balances?: {
    opening?: number;
    closing?: number;
  };
  rows: ImportTransaction[];
}

export interface ParsedDocument {
  /**
   * Null when the file was not recognised at all. A recognised file that
   * happens to hold no transactions still has a format -- that distinction is
   * the difference between "quiet period" and "not a statement", and it is the
   * same one the statement parser service draws.
   */
  format: BankFileFormat | null;
  groups: StatementGroup[];
  diagnostics: ParseDiagnostic[];
}
