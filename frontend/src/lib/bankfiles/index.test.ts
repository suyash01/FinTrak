import { describe, it, expect } from "vitest";
import { parseBankFile, allRows, MAX_BANK_FILE_BYTES } from "./index";
import { CAMT_053_BANK, CAMT_PREFIXED } from "./camtFixtures";
import { OFX_TWO_ACCOUNTS } from "./fixtures";

function bytesOf(text: string): Uint8Array {
  return new TextEncoder().encode(text);
}

describe("parseBankFile", () => {
  it("dispatches a camt file to the camt reader", () => {
    const doc = parseBankFile(bytesOf(CAMT_053_BANK));
    expect(doc.format).toBe("camt.053");
    expect(doc.groups[0].accountNumber).toBe("IN40100234567890");
  });

  it("dispatches a namespaced camt file to the camt reader", () => {
    expect(parseBankFile(bytesOf(CAMT_PREFIXED)).format).toBe("camt.053");
  });

  it("dispatches an OFX file to the OFX reader", () => {
    const doc = parseBankFile(bytesOf(OFX_TWO_ACCOUNTS));
    expect(doc.format).toBe("ofx");
    expect(doc.groups).toHaveLength(2);
  });

  it("identifies the file by its contents, not its name", () => {
    // A bank handing out OFX as .txt or with no extension is routine, and the
    // CSV importer already made this call with autoDetectMapping.
    expect(parseBankFile(bytesOf(OFX_TWO_ACCOUNTS)).format).toBe("ofx");
  });

  it("reports a file it does not recognise", () => {
    const doc = parseBankFile(bytesOf("date,description\n2026-01-01,coffee"));
    expect(doc.format).toBeNull();
    expect(doc.groups).toEqual([]);
    expect(doc.diagnostics.map((d) => d.code)).toEqual(["unsupported_format"]);
  });

  it("reports a file that is too large instead of parsing it", () => {
    const doc = parseBankFile(new Uint8Array(MAX_BANK_FILE_BYTES + 1));
    expect(doc.diagnostics.map((d) => d.code)).toEqual(["unreadable_file"]);
    expect(doc.diagnostics[0].message).toContain("20 MB");
  });

  it("re-decodes an OFX file that declares a legacy charset", () => {
    // The tags are ASCII, so sniffing works on the first pass; the bytes behind
    // them are not, which is where a payee name gets corrupted.
    const header = "OFXHEADER:100\nDATA:OFXSGML\nCHARSET:1252\n\n<OFX>";
    const tail = `<BANKMSGSRSV1><STMTTRNRS><STMTRS><CURDEF>INR
      <BANKACCTFROM><ACCTID>1</ACCTID></BANKACCTFROM><BANKTRANLIST>
      <STMTTRN><TRNTYPE>DEBIT<DTPOSTED>20260518<TRNAMT>-5.00<NAME>`;
    const closing = `</NAME></STMTTRN></BANKTRANLIST></STMTRS></STMTTRNRS></BANKMSGSRSV1></OFX>`;
    const bytes = new Uint8Array([
      ...new TextEncoder().encode(header + tail),
      0x93, // left curly quote
      ...new TextEncoder().encode("CAFE"),
      0x94, // right curly quote
      ...new TextEncoder().encode(closing),
    ]);

    const doc = parseBankFile(bytes);
    expect(doc.format).toBe("ofx");
    expect(doc.groups[0].rows[0].description).toBe("\u201cCAFE\u201d");
  });

  it("does not throw on empty or binary input", () => {
    for (const input of [
      new Uint8Array(0),
      new Uint8Array([0, 1, 2, 3, 0xff, 0xfe]),
    ]) {
      const doc = parseBankFile(input);
      expect(doc.groups).toEqual([]);
      expect(doc.diagnostics.length).toBeGreaterThan(0);
    }
  });
});

describe("allRows", () => {
  it("returns every group's rows, in file order", () => {
    // The wizard imports one account for the whole batch, so every group's rows
    // are flattened into a single list. This is where that decision lives.
    const doc = parseBankFile(bytesOf(OFX_TWO_ACCOUNTS));
    expect(allRows(doc)).toHaveLength(4);
    expect(allRows(doc).map((r) => r.description)).toEqual([
      "SWIGGY ORDER",
      "SALARY",
      "CHARGES",
      "RENT",
    ]);
  });

  it("returns nothing for a document with no rows", () => {
    expect(allRows(parseBankFile(bytesOf("not a statement")))).toEqual([]);
  });
});
