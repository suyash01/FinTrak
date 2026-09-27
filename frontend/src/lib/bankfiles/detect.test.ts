import { describe, it, expect } from "vitest";
import { detectFormat } from "./detect";
import {
  decodeBytes,
  declaredCharset,
  stripBom,
  SUPPORTED_CHARSETS,
} from "./decode";

const CAMT_053 = `<?xml version="1.0" encoding="UTF-8"?>
<Document xmlns="urn:iso:std:iso:20022:tech:xsd:camt.053.001.02">
  <BkToCstmrStmt><Stmt><Ntry><Amt Ccy="INR">1.00</Amt></Ntry></Stmt></BkToCstmrStmt>
</Document>`;

const CAMT_052 = `<?xml version="1.0" encoding="UTF-8"?>
<Document xmlns="urn:iso:std:iso:20022:tech:xsd:camt.052.001.08">
  <BkToCstmrStmt><Stmt><Bal><Amt>1.00</Amt></Bal></Stmt></BkToCstmrStmt>
</Document>`;

// OFX 1.x is SGML, not XML: unclosed leaf tags, and a plain-text header block.
const OFX_SGML = `OFXHEADER:100
DATA:OFXSGML
VERSION:102
SECURITY:NONE
ENCODING:USASCII
CHARSET:1252
COMPRESSION:NONE
OLDFILEUID:NONE
NEWFILEUID:NONE

<OFX>
<BANKMSGSRSV1><STMTTRNRS><STMTRS><CURDEF>INR
<BANKTRANLIST><STMTTRN><TRNTYPE>DEBIT
</BANKTRANLIST></STMTRS></STMTTRNRS></BANKMSGSRSV1>
</OFX>`;

// OFX 2.x is well-formed XML with a processing instruction.
const OFX_XML = `<?xml version="1.0" encoding="UTF-8" standalone="no"?>
<?OFX OFXHEADER="200" VERSION="211" SECURITY="NONE" OLDFILEUID="NONE" NEWFILEUID="NONE"?>
<OFX><BANKMSGSRSV1><STMTTRNRS><CURDEF>INR</CURDEF></STMTTRNRS></BANKMSGSRSV1></OFX>`;

describe("detectFormat", () => {
  it("recognises a camt.053 report", () => {
    expect(detectFormat(CAMT_053)).toBe("camt.053");
  });

  it("recognises a camt.052 statement", () => {
    expect(detectFormat(CAMT_052)).toBe("camt.052");
  });

  it("recognises a camt namespace version it has never seen", () => {
    // ISO 20022 has published several camt versions; the namespace carries the
    // message family, which is the part that decides the reader.
    expect(
      detectFormat(
        `<Document xmlns="urn:iso:std:iso:20022:tech:xsd:camt.053.001.99"/>`,
      ),
    ).toBe("camt.053");
  });

  it("recognises an OFX 1.x SGML document", () => {
    expect(detectFormat(OFX_SGML)).toBe("ofx");
  });

  it("recognises an OFX 2.x document", () => {
    expect(detectFormat(OFX_XML)).toBe("ofx");
  });

  it("recognises OFX from a bare <OFX> root with no header at all", () => {
    expect(detectFormat("<OFX><BANKMSGSRSV1></BANKMSGSRSV1></OFX>")).toBe("ofx");
  });

  it("ignores a leading byte order mark when sniffing", () => {
    expect(detectFormat(`﻿${CAMT_053}`)).toBe("camt.053");
  });

  it("returns null for a file it does not recognise", () => {
    expect(detectFormat("")).toBeNull();
    expect(detectFormat("date,description\n2026-01-01,coffee")).toBeNull();
    expect(detectFormat("<html><body>Not a statement</body></html>")).toBeNull();
    expect(
      detectFormat('<Document xmlns="urn:iso:std:iso:20022:tech:xsd:pain.001.001.03"/>'),
    ).toBeNull();
  });
});

describe("stripBom", () => {
  it("removes a UTF-8 byte order mark", () => {
    expect(stripBom("﻿<OFX>")).toBe("<OFX>");
  });

  it("leaves text without one untouched", () => {
    expect(stripBom("<OFX>")).toBe("<OFX>");
  });
});

describe("declaredCharset", () => {
  it("reads the charset an OFX header declares, as a canonical label", () => {
    // Banks write CP1252 as a bare "1252"; the canonical WHATWG label is
    // windows-1252, so that is what callers see.
    expect(declaredCharset(OFX_SGML)).toBe("windows-1252");
  });

  it("reads the encoding an XML prolog declares", () => {
    expect(declaredCharset(CAMT_053)).toBe("utf-8");
  });

  it("prefers the OFX header over the XML prolog", () => {
    // An OFX 2.x file can carry both; the header is the one banks get right.
    const both = `<?xml version="1.0" encoding="UTF-8"?>\n<?OFX?>\nOFXHEADER:100\nCHARSET:1252\n<OFX></OFX>`;
    expect(declaredCharset(both)).toBe("windows-1252");
  });

  it("returns null when nothing declares an encoding", () => {
    expect(declaredCharset("<OFX></OFX>")).toBeNull();
    expect(declaredCharset("")).toBeNull();
  });

  it("returns null for a label it does not map", () => {
    expect(declaredCharset("<?xml version='1.0' encoding='EBCDIC'?>")).toBeNull();
  });
});

describe("decodeBytes", () => {
  function cp1252Bytes(): Uint8Array {
    // 0x93/0x94 are the Windows-1252 curly quotes, which is exactly the class
    // of character an Indian bank's payee name arrives as.
    return new Uint8Array([
      ...new TextEncoder().encode("NTRY<NAME>"),
      0x93, 0x94, // curly quotes
      ...new TextEncoder().encode("SWIGGY"),
    ]);
  }

  it("decodes UTF-8 and strips the byte order mark", () => {
    const withBom = new Uint8Array([
      0xef, 0xbb, 0xbf,
      ...new TextEncoder().encode("<OFX>"),
    ]);
    expect(decodeBytes(withBom).text).toBe("<OFX>");
  });

  it("honours a declared charset over the UTF-8 default", () => {
    // Read as UTF-8 these bytes are two U+FFFD replacement characters; read as
    // windows-1252 they are the curly quotes the bank actually wrote.
    expect(decodeBytes(cp1252Bytes(), "cp1252").text).toContain("\u201c");
    expect(decodeBytes(cp1252Bytes(), "1252").text).toContain("\u201d");
  });

  it("treats a label it does not map the same as no label at all", () => {
    const out = decodeBytes(cp1252Bytes(), "definitely-not-a-charset");
    expect(out.usedCharset).toBe("utf-8");
    // Nothing was attempted, so there is nothing to report falling back from.
    expect(out.fallbackTo).toBeUndefined();
  });

  it("reports a fallback when a mapped charset this browser cannot decode", () => {
    // Which legacy encodings a browser can decode depends on its ICU build, so
    // a mapped label is not a promise the platform kept. Stubbing the decoder
    // is the only way to reach that branch: a real Shift-JIS file would decode
    // here and never take it.
    const RealDecoder = globalThis.TextDecoder;
    globalThis.TextDecoder = class {
      private inner: TextDecoder;
      constructor(label = "utf-8") {
        if (label === "shift_jis") throw new RangeError("unsupported label");
        this.inner = new RealDecoder(label);
      }
      decode(bytes?: BufferSource) {
        return this.inner.decode(bytes as never);
      }
    } as unknown as typeof TextDecoder;
    try {
      const out = decodeBytes(cp1252Bytes(), "shift_jis");
      expect(out.usedCharset).toBe("utf-8");
      expect(out.fallbackTo).toBe("shift_jis");
      expect(out.text).toContain("NTRY<NAME>");
    } finally {
      globalThis.TextDecoder = RealDecoder;
    }
  });

  it("reports the charset it used", () => {
    expect(decodeBytes(new Uint8Array([60]), "cp1252").usedCharset).toBe(
      "windows-1252",
    );
    expect(decodeBytes(new Uint8Array([60])).usedCharset).toBe("utf-8");
  });
});

describe("SUPPORTED_CHARSETS", () => {
  it("covers the labels these bank exports actually write", () => {
    // Keys are normalised to lower case; declaredCharset is what maps a
    // document's own label onto one of them.
    for (const label of [
      "1252",
      "cp1252",
      "utf-8",
      "shift_jis",
      "iso-8859-1",
    ]) {
      expect(SUPPORTED_CHARSETS.has(label)).toBe(true);
    }
  });

  it("does not claim a charset it will not map", () => {
    expect(SUPPORTED_CHARSETS.has("ebcdic")).toBe(false);
  });
});
