/**
 * Identifying a bank-supplied file from its own contents.
 *
 * Sniffing rather than trusting the extension or asking the user: banks hand
 * these out as `.xml`, `.txt`, `.qfx`, `.OFX` and sometimes with no extension at
 * all, and the CSV importer already made the same call with
 * `autoDetectMapping`. Getting this wrong is not cosmetic — the statement PDF
 * service's own error message is about exactly this failure.
 */

import type { BankFileFormat } from "./types";

/**
 * The ISO 20022 message family, wherever it appears.
 *
 * Matched on the family rather than on `xmlns="..."` because camt files put
 * their namespace in two different shapes: the common default form
 * (`xmlns="urn:...:camt.053.001.02"`) and a prefixed form some exporters emit
 * (`xmlns:c53="urn:...:camt.053.001.02"`). A lookup anchored on `xmlns=` reads
 * the first and silently misses the second, which is why the anchor is the
 * family itself. The version segment is deliberately not matched: ISO 20022 has
 * published several revisions of both camt messages and only the family decides
 * which reader applies.
 */
const CAMT_FAMILY = /camt[.:](052|053)\b/i;

/** An OFX 2.x processing instruction: `<?OFX OFXHEADER="200" ...?>`. */
const OFX_PROLOG = /<\?ofx\b/i;

/** An OFX 1.x header block, or a bare `<OFX>` root when the header is missing. */
const OFX_ROOT = /^\s*(?:[A-Z]+:\s*[^\r\n]*\r?\n)*\s*<ofx[\s>]/i;

/**
 * Return the format of a decoded bank file, or null when it is not one of ours.
 * Never throws: an unreadable file is a diagnostic, not an exception.
 */
export function detectFormat(text: string): BankFileFormat | null {
  if (!text) return null;

  const head = text.slice(0, 4096);

  // camt first: it is XML, and an XML prolog cannot be confused with OFX's.
  const family = CAMT_FAMILY.exec(head);
  if (family) return `camt.${family[1]}` as BankFileFormat;

  if (OFX_PROLOG.test(head) || OFX_ROOT.test(head)) return "ofx";

  return null;
}
