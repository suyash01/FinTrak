import { describe, it, expect } from "vitest";
import { readOfx } from "./ofx";
import {
  OFX_CREDIT_CARD,
  OFX_MALFORMED_ROWS,
  OFX_NO_TRANSACTIONS,
  OFX_SERVICES_NOT_STMTRS,
  OFX_SIGN_DISAGREES,
  OFX_TWO_ACCOUNTS,
  OFX_WITH_PENDING,
  OFX_XML_2,
} from "./fixtures";

describe("readOfx", () => {
  it("reads a bank statement's transactions", () => {
    const doc = readOfx(OFX_TWO_ACCOUNTS);
    const rows = doc.groups[0].rows;
    expect(rows).toEqual([
      {
        date: "2026-05-18",
        description: "SWIGGY ORDER",
        amount: 1250.5,
        type: "debit",
      },
      {
        date: "2026-05-20",
        description: "SALARY",
        amount: 75000,
        type: "credit",
      },
      {
        date: "2026-05-22",
        description: "CHARGES",
        amount: 99.99,
        type: "debit",
      },
    ]);
  });

  it("reads the account, currency and period of each statement", () => {
    const doc = readOfx(OFX_TWO_ACCOUNTS);
    expect(doc.groups).toHaveLength(2);
    expect(doc.groups[0]).toMatchObject({
      format: "ofx",
      accountNumber: "50100234567890",
      currency: "INR",
      periodFrom: "2026-05-01",
      periodTo: "2026-05-31",
      balances: { closing: 73749.51 },
    });
    expect(doc.groups[1]).toMatchObject({
      accountNumber: "50100999888777",
      periodFrom: "2026-04-01",
      periodTo: "2026-04-30",
      balances: { closing: 1200 },
    });
  });

  it("keeps the two accounts in one file apart", () => {
    const doc = readOfx(OFX_TWO_ACCOUNTS);
    // The point of grouping: rows from the second account must not leak into
    // the first group's balance or period.
    expect(doc.groups[0].rows).toHaveLength(3);
    expect(doc.groups[1].rows).toHaveLength(1);
    expect(doc.groups[1].rows[0].description).toBe("RENT");
  });

  it("reads the closing balance from LEDGERBAL, not AVAILBAL", () => {
    // The fixture sets AVAILBAL 100 lower; the ledger balance is the one that
    // must agree with the statement.
    const doc = readOfx(OFX_TWO_ACCOUNTS);
    expect(doc.groups[0].balances?.closing).toBe(73749.51);
  });

  it("reads a credit card statement", () => {
    const doc = readOfx(OFX_CREDIT_CARD);
    expect(doc.groups).toHaveLength(1);
    expect(doc.groups[0].accountNumber).toBe("XXXX5678");
    expect(doc.groups[0].rows).toEqual([
      { date: "2026-05-03", description: "AMAZON", amount: 2499, type: "debit" },
      { date: "2026-05-09", description: "REFUND AMAZON", amount: 150, type: "credit" },
    ]);
  });

  it("prefers an explicit TRNTYPE over the sign of the amount", () => {
    const doc = readOfx(OFX_SIGN_DISAGREES);
    expect(doc.groups[0].rows[0]).toMatchObject({
      description: "POSITIVE DEBIT",
      amount: 500,
      type: "debit",
    });
  });

  it("falls back to the sign of the amount when TRNTYPE is not a direction", () => {
    // XFER is a movement, not a direction, so the sign decides: -300.00 is
    // money out of the account.
    const doc = readOfx(OFX_SIGN_DISAGREES);
    expect(doc.groups[0].rows[1]).toMatchObject({ type: "debit", amount: 300 });
  });

  it("reports that some rows' stated type disagreed with their sign", () => {
    const doc = readOfx(OFX_SIGN_DISAGREES);
    expect(doc.diagnostics.map((d) => d.code)).toContain("row_no_direction");
  });

  it("reports the rows it had to drop, and keeps the rest", () => {
    const doc = readOfx(OFX_MALFORMED_ROWS);
    // Only the two rows the reader can trust survive: the fully-described one
    // and the one identified only by the bank's reference.
    expect(doc.groups[0].rows.map((r) => r.description)).toEqual([
      "M7",
      "GOOD ROW",
    ]);

    const codes = doc.diagnostics.map((d) => d.code);
    expect(codes).toContain("row_no_amount");
    expect(codes).toContain("row_bad_amount");
    expect(codes).toContain("row_no_date");
    expect(codes).toContain("row_bad_date");
    expect(codes).toContain("entry_no_details");
  });

  it("imports a row whose only identifier is the bank's own reference", () => {
    // A reference is enough to file and to de-duplicate against, so a row that
    // carries one is imported rather than thrown away.
    const doc = readOfx(OFX_MALFORMED_ROWS);
    const byDescription = doc.groups[0].rows.map((r) => r.description);
    expect(byDescription).toContain("M7");
  });

  it("reports pending movements instead of importing them", () => {
    // Booking an unbooked movement would overstate the balance, and dropping
    // it silently would hide that the file had it.
    const doc = readOfx(OFX_WITH_PENDING);
    expect(doc.groups[0].rows.map((r) => r.description)).toEqual(["BOOKED"]);
    const pending = doc.diagnostics.find((d) => d.code === "interim_excluded");
    expect(pending?.count).toBe(2);
    expect(pending?.message).toContain("2 pending transactions");
  });

  it("never reports a diagnostic for a clean statement", () => {
    expect(readOfx(OFX_TWO_ACCOUNTS).diagnostics).toEqual([]);
    expect(readOfx(OFX_CREDIT_CARD).diagnostics).toEqual([]);
    expect(readOfx(OFX_XML_2).diagnostics).toEqual([]);
  });

  it("reads OFX 2.x as well as 1.x", () => {
    const doc = readOfx(OFX_XML_2);
    expect(doc.groups[0].rows).toEqual([
      { date: "2026-05-07", description: "COFFEE", amount: 15, type: "debit" },
    ]);
    expect(doc.groups[0].balances?.closing).toBe(985);
  });

  it("distinguishes an empty statement from a failed read", () => {
    // The parser service makes the same distinction, and for the same reason: a
    // quiet period is a legitimate 0-row import, an unreadable file is not.
    const doc = readOfx(OFX_NO_TRANSACTIONS);
    expect(doc.groups).toHaveLength(1);
    expect(doc.groups[0].rows).toEqual([]);
    expect(doc.diagnostics.map((d) => d.code)).toEqual(["no_transactions"]);
  });

  it("finds no statement when the file carries none", () => {
    // A sign-on-only or services response has no STMTRS: that is "nothing to
    // import", not a crash and not a group with no rows.
    const doc = readOfx(OFX_SERVICES_NOT_STMTRS);
    expect(doc.groups).toEqual([]);
    expect(doc.diagnostics.map((d) => d.code)).toEqual(["no_transactions"]);
  });

  it("does not throw on a file that is not OFX at all", () => {
    const doc = readOfx("this is not a statement");
    expect(doc.groups).toEqual([]);
    expect(doc.diagnostics.map((d) => d.code)).toEqual(["no_transactions"]);
  });

  it("uses the memo when a row carries no payee name", () => {
    const src = `<OFX><BANKMSGSRSV1><STMTTRNRS><STMTRS><CURDEF>INR
      <BANKACCTFROM><ACCTID>1</ACCTID></BANKACCTFROM><BANKTRANLIST>
      <STMTTRN><TRNTYPE>DEBIT<DTPOSTED>20260518<TRNAMT>-5.00<MEMO>Card top-up</MEMO></STMTTRN>
      </BANKTRANLIST></STMTRS></STMTTRNRS></BANKMSGSRSV1></OFX>`;
    expect(readOfx(src).groups[0].rows[0].description).toBe("Card top-up");
  });

  it("falls back through the transaction's own dates when DTPOSTED is absent", () => {
    const src = `<OFX><BANKMSGSRSV1><STMTTRNRS><STMTRS><CURDEF>INR
      <BANKACCTFROM><ACCTID>1</ACCTID></BANKACCTFROM><BANKTRANLIST>
      <STMTTRN><TRNTYPE>DEBIT<DTUSER>20260511<TRNAMT>-5.00<NAME>USER DATE</NAME></STMTTRN>
      <STMTTRN><TRNTYPE>DEBIT<DTAVAIL>20260512<TRNAMT>-5.00<NAME>AVAIL DATE</NAME></STMTTRN>
      </BANKTRANLIST></STMTRS></STMTTRNRS></BANKMSGSRSV1></OFX>`;
    const rows = readOfx(src).groups[0].rows;
    expect(rows[0].date).toBe("2026-05-11");
    expect(rows[1].date).toBe("2026-05-12");
  });
});
