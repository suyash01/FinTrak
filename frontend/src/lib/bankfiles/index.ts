/**
 * Reading a bank-supplied file: the single entry point the import wizard uses.
 *
 * This is the only module in `lib/bankfiles` a caller needs. It decodes the
 * bytes, works out which format they are, and hands them to the right reader --
 * and it never throws. A file FinTrak cannot read comes back as a document with
 * no groups and a diagnostic explaining why, because a failed import with a
 * reason is more useful than an exception the wizard would have to catch and
 * turn into a message anyway.
 */

import { readCamt } from "./camt";
import { declaredCharset, decodeBytes } from "./decode";
import { detectFormat } from "./detect";
import { MAX_BANK_FILE_BYTES } from "./limits";
import { readOfx } from "./ofx";
import type { ParseDiagnostic, ParsedDocument } from "./types";
import type { ImportTransaction } from "../../types";

export * from "./types";
export { MAX_BANK_FILE_BYTES, MAX_BANK_FILE_ROWS } from "./limits";
export { SUPPORTED_CHARSETS } from "./decode";

function failure(
  format: ParsedDocument["format"],
  code: ParseDiagnostic["code"],
  message: string,
): ParsedDocument {
  return { format, groups: [], diagnostics: [{ code, message }] };
}

/**
 * Read an uploaded bank file.
 *
 * `format` is null when the file was not recognised, which is the one case that
 * is not a statement FinTrak cannot read but a file that is not a statement.
 */
export function parseBankFile(bytes: Uint8Array): ParsedDocument {
  if (bytes.byteLength > MAX_BANK_FILE_BYTES) {
    return failure(
      null,
      "unreadable_file",
      `This file is larger than the ${Math.round(MAX_BANK_FILE_BYTES / (1024 * 1024))} MB import limit. Split it, or use a different download from your bank.`,
    );
  }

  // The first pass is UTF-8 because the structural markers of all three formats
  // are ASCII, so sniffing works whatever the payload is encoded in.
  const probe = decodeBytes(bytes);
  const format = detectFormat(probe.text);
  if (!format) {
    return failure(
      null,
      "unsupported_format",
      "This file is not a format FinTrak reads. It supports ISO 20022 (camt.052 and camt.053) and OFX/QFX bank exports.",
    );
  }

  // Then read it properly. A camt file declares its encoding in an XML prolog
  // and a legacy OFX file in its header block; either can name an encoding that
  // is not UTF-8, and reading those bytes as UTF-8 corrupts every payee name in
  // the file while leaving the tags -- and so the rows -- looking fine.
  const declared = declaredCharset(probe.text);
  const decoded =
    declared && declared !== probe.usedCharset
      ? decodeBytes(bytes, declared)
      : probe;

  const document =
    format === "ofx" ? readOfx(decoded.text) : readCamt(decoded.text);

  if (decoded.fallbackTo) {
    document.diagnostics.unshift({
      code: "unreadable_file",
      message: `This file declares the ${decoded.fallbackTo} encoding, which this browser cannot read, so it was read as UTF-8. Non-ASCII payee names may be wrong.`,
    });
  }

  return document;
}

/**
 * Every group's rows, in file order.
 *
 * The import endpoint takes a single account for the whole batch, so a file
 * describing several accounts has all of them imported into the one account
 * chosen in the wizard. The per-group identity is shown to the user before they
 * commit; it does not route rows.
 */
export function allRows(document: ParsedDocument): ImportTransaction[] {
  return document.groups.flatMap((group) => group.rows);
}
