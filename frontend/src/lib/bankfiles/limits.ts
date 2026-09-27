/**
 * Bounds on what a bank file is allowed to make the browser do.
 *
 * These follow the query language's precedent rather than inventing a new
 * policy: a bounded input that degrades to a diagnostic, instead of a hard
 * failure or an unbounded parse. `MAX_CSV_BYTES` is the same 20 MB the CSV
 * importer already accepts, so a file that is a reasonable size in one import
 * path is not rejected in the other.
 */

/** Largest bank file accepted, matching the CSV importer's own cap. */
export const MAX_BANK_FILE_BYTES = 20 * 1024 * 1024;

/**
 * Largest number of rows a reader will collect from one file.
 *
 * A multi-year camt statement from a busy account can hold tens of thousands of
 * entries, and the preview step renders every one of them. The cap is set well
 * above any real statement so it is never the thing that stops an import, and
 * its real job is to stop a malformed or hostile file from becoming an
 * unbounded loop or an unresponsive table.
 */
export const MAX_BANK_FILE_ROWS = 50_000;

/** Largest number of account-period groups a reader will collect. */
export const MAX_BANK_FILE_GROUPS = 200;
