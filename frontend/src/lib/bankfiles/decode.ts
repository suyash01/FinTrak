/**
 * Turning the bytes of an uploaded file into text.
 *
 * The hazard this module exists for: camt is UTF-8 by XML prolog, but OFX 1.x
 * is SGML with a hand-written header block, and banks really do ship it as
 * CP1252, Shift-JIS or ISO-8859-1. Decoded as UTF-8 those files come out full
 * of U+FFFD, which silently corrupts every payee name in the file. So the
 * declared charset is honoured, and anything the browser cannot decode falls
 * back to UTF-8 rather than throwing.
 */

/**
 * The charset labels these bank exports write, normalised to a label
 * `TextDecoder` accepts.
 *
 * Pinned rather than assumed: which legacy encodings a browser can decode
 * depends on its ICU build, so the mapping is part of this module's contract and
 * is covered by a test. `1252` is CP1252 (banks write it as a bare number,
 * never as "CP1252"), which is the case that catches people.
 */
export const SUPPORTED_CHARSETS: ReadonlySet<string> = new Set([
  "utf-8",
  "utf8",
  "us-ascii",
  "ascii",
  "cp1252",
  "1252",
  "windows-1252",
  "iso-8859-1",
  "iso8859-1",
  "latin1",
  "shift_jis",
  "shift-jis",
  "sjis",
  "cp932",
  "gbk",
  "gb2312",
  "gb18030",
  "big5",
  "euc-kr",
]);

const CHARSET_ALIASES: Readonly<Record<string, string>> = {
  utf8: "utf-8",
  ascii: "windows-1252",
  "us-ascii": "windows-1252",
  "1252": "windows-1252",
  cp1252: "windows-1252",
  "windows-1252": "windows-1252",
  latin1: "windows-1252",
  "iso8859-1": "windows-1252",
  "iso-8859-1": "windows-1252",
  "shift-jis": "shift_jis",
  sjis: "shift_jis",
  cp932: "shift_jis",
  gb2312: "gbk",
  gb18030: "gb18030",
};

/** Map a declared label onto a decoder label, or null when it is not one we map. */
function normalizeCharset(label: string | null | undefined): string | null {
  if (!label) return null;
  const key = label.trim().toLowerCase().replace(/^["']|["']$/g, "");
  if (!key) return null;
  return CHARSET_ALIASES[key] ?? (SUPPORTED_CHARSETS.has(key) ? key : null);
}

/**
 * The charset the document declares for itself, or null when it declares none.
 *
 * The OFX header block is preferred over an XML prolog: an OFX 2.x file can
 * carry both, and the header is the one banks actually get right.
 */
export function declaredCharset(text: string): string | null {
  const ofx = /^CHARSET\s*:\s*(.+)$/im.exec(text);
  if (ofx) return normalizeCharset(ofx[1]);

  const prolog = /<\?xml[^>]*\bencoding\s*=\s*["']([^"']+)["']/i.exec(text);
  if (prolog) return normalizeCharset(prolog[1]);

  return null;
}

export interface DecodedBytes {
  text: string;
  /** The decoder label actually used. */
  usedCharset: string;
  /**
   * Set when the declared charset could not be used and UTF-8 was read
   * instead, so the caller can say so rather than pretend the file was clean.
   */
  fallbackTo?: string;
}

/**
 * Decode the file, honouring `charset` when the browser can and falling back to
 * UTF-8 when it cannot.
 */
export function decodeBytes(
  bytes: Uint8Array,
  charset?: string | null,
): DecodedBytes {
  const wanted = normalizeCharset(charset);
  if (wanted) {
    try {
      return {
        text: stripBom(new TextDecoder(wanted).decode(bytes)),
        usedCharset: wanted,
      };
    } catch {
      // An ICU build without this encoding throws a RangeError. Fall through
      // to UTF-8 and report it, so a Shift-JIS file on a bare browser degrades
      // to a flagged import rather than a crash.
      return {
        text: stripBom(new TextDecoder("utf-8").decode(bytes)),
        usedCharset: "utf-8",
        fallbackTo: wanted,
      };
    }
  }
  return { text: stripBom(new TextDecoder("utf-8").decode(bytes)), usedCharset: "utf-8" };
}

/** Remove a leading UTF-8 byte order mark, which is not part of any of these formats. */
export function stripBom(text: string): string {
  return text.charCodeAt(0) === 0xfeff ? text.slice(1) : text;
}
