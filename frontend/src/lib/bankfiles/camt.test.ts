import { describe, it, expect } from "vitest";
import { readCamt } from "./camt";
import {
  CAMT_052_CARD,
  CAMT_053_BANK,
  CAMT_BROKEN_XML,
  CAMT_MALFORMED,
  CAMT_NO_ENTRIES,
  CAMT_PREFIXED,
  CAMT_TWO_ACCOUNTS,
  CAMT_WRONG_MESSAGE,
} from "./camtFixtures";

describe("readCamt", () => {
  it("reads a camt.053 statement's entries", () => {
    const doc = readCamt(CAMT_053_BANK);
    expect(doc.format).toBe("camt.053");
    expect(doc.groups).toHaveLength(1);
    expect(doc.groups[0].rows).toEqual([
      {
        date: "2026-05-18",
        description: "SWIGGY ORDER",
        amount: 1250.5,
        type: "debit",
      },
      {
        date: "2026-05-20",
        description: "ACME CORP",
        amount: 75000,
        type: "credit",
      },
      {
        date: "2026-05-22",
        description: "BANK CHARGES",
        amount: 99.99,
        type: "debit",
      },
    ]);
  });

  it("reads the account and currency of the statement", () => {
    const doc = readCamt(CAMT_053_BANK);
    expect(doc.groups[0]).toMatchObject({
      accountNumber: "IN40100234567890",
      accountHolder: "Suyash Mittal",
      currency: "INR",
    });
  });

  it("derives the period from the entries", () => {
    const doc = readCamt(CAMT_053_BANK);
    expect(doc.groups[0].periodFrom).toBe("2026-05-18");
    expect(doc.groups[0].periodTo).toBe("2026-05-22");
  });

  it("reads the counterpart party rather than the account holder", () => {
    const doc = readCamt(CAMT_053_BANK);
    // A money-out entry names its creditor; a money-in entry names its debtor.
    // Either way it is the party on the other side of the payment.
    const descriptions = doc.groups[0].rows.map((r) => r.description);
    expect(descriptions).not.toContain("Suyash Mittal");
  });

  it("falls back to the remittance information when there is no party", () => {
    const src = `<?xml version="1.0" encoding="UTF-8"?>
<Document xmlns="urn:iso:std:iso:20022:tech:xsd:camt.053.001.02">
  <BkToCstmrStmt><Stmt><Id>S</Id>
    <Acct><Id><Othr><Id>A1</Id></Othr></Id><Ccy>INR</Ccy></Acct>
    <Ntry><Amt Ccy="INR">250.00</Amt><CdtDbtInd>DBIT</CdtDbtInd><Sts>BOOK</Sts>
      <BookgDt><Dt>2026-05-11</Dt></BookgDt>
      <NtryDtls><TxDtls>
        <RmtInf><Ustrd>UPI/4471/SWIGGY</Ustrd></RmtInf>
      </TxDtls></NtryDtls>
    </Ntry>
  </Stmt></BkToCstmrStmt>
</Document>`;
    expect(readCamt(src).groups[0].rows[0].description).toBe("UPI/4471/SWIGGY");
  });

  it("falls back to the entry's additional information when neither is present", () => {
    const src = `<?xml version="1.0" encoding="UTF-8"?>
<Document xmlns="urn:iso:std:iso:20022:tech:xsd:camt.053.001.02">
  <BkToCstmrStmt><Stmt><Id>S</Id>
    <Acct><Id><Othr><Id>A1</Id></Othr></Id><Ccy>INR</Ccy></Acct>
    <Ntry><Amt Ccy="INR">15.00</Amt><CdtDbtInd>DBIT</CdtDbtInd><Sts>BOOK</Sts>
      <BookgDt><Dt>2026-05-11</Dt></BookgDt>
      <AddtlNtryInf>ATM CASH WITHDRAWAL</AddtlNtryInf>
    </Ntry>
  </Stmt></BkToCstmrStmt>
</Document>`;
    expect(readCamt(src).groups[0].rows[0].description).toBe(
      "ATM CASH WITHDRAWAL",
    );
  });

  it("keeps the booking date when the value date differs", () => {
    // The ledger records when a movement posted, not when it took effect.
    const src = CAMT_053_BANK.replace(
      "<ValDt><Dt>2026-05-16</Dt></ValDt>",
      "<ValDt><Dt>2026-05-16</Dt></ValDt>",
    );
    expect(readCamt(src).groups[0].rows[0].date).toBe("2026-05-18");
  });

  it("reads a namespaced document that uses a prefix", () => {
    const doc = readCamt(CAMT_PREFIXED);
    expect(doc.groups[0].rows).toEqual([
      {
        date: "2026-05-05",
        description: "PREFIXED PAYEE",
        amount: 5,
        type: "debit",
      },
    ]);
    expect(doc.groups[0].accountNumber).toBe("PFX1");
  });

  it("reads a camt.052 statement's balances", () => {
    const doc = readCamt(CAMT_052_CARD);
    expect(doc.format).toBe("camt.052");
    expect(doc.groups[0].balances).toEqual({
      opening: 40649.25,
      closing: 38150.25,
    });
  });

  it("keeps each account in a multi-account file apart", () => {
    const doc = readCamt(CAMT_TWO_ACCOUNTS);
    expect(doc.groups).toHaveLength(2);
    expect(doc.groups[0].accountNumber).toBe("AAA111");
    expect(doc.groups[1].accountNumber).toBe("BBB222");
    expect(doc.groups[0].rows).toHaveLength(1);
    expect(doc.groups[1].rows).toHaveLength(1);
  });

  it("reports the entries it had to drop, and keeps the rest", () => {
    const doc = readCamt(CAMT_MALFORMED);
    expect(doc.groups[0].rows.map((r) => r.description)).toEqual(["GOOD ROW"]);
    const codes = doc.diagnostics.map((d) => d.code);
    expect(codes).toContain("row_no_amount");
    expect(codes).toContain("row_bad_amount");
    expect(codes).toContain("row_no_date");
    expect(codes).toContain("row_no_direction");
    expect(codes).toContain("entry_no_details");
  });

  it("reports a pending entry instead of booking it", () => {
    const doc = readCamt(CAMT_MALFORMED);
    const pending = doc.diagnostics.find((d) => d.code === "interim_excluded");
    expect(pending?.message).toContain("pending entry");
    // One occurrence carries no count, so a single dropped row is not
    // rendered as "1 rows". The aggregated case is pinned in ofx.test.ts.
    expect(pending?.count).toBeUndefined();
    expect(doc.groups[0].rows.map((r) => r.description)).not.toContain(
      "PENDING HOTEL",
    );
  });

  it("never reports a diagnostic for a clean statement", () => {
    expect(readCamt(CAMT_053_BANK).diagnostics).toEqual([]);
    expect(readCamt(CAMT_052_CARD).diagnostics).toEqual([]);
    expect(readCamt(CAMT_TWO_ACCOUNTS).diagnostics).toEqual([]);
  });

  it("distinguishes an empty statement from a failed read", () => {
    const doc = readCamt(CAMT_NO_ENTRIES);
    expect(doc.groups).toHaveLength(1);
    expect(doc.groups[0].rows).toEqual([]);
    expect(doc.diagnostics.map((d) => d.code)).toEqual(["no_transactions"]);
  });

  it("finds no statement in a well-formed document of another family", () => {
    const doc = readCamt(CAMT_WRONG_MESSAGE);
    expect(doc.groups).toEqual([]);
    expect(doc.diagnostics.map((d) => d.code)).toEqual(["no_transactions"]);
  });

  it("reports unparseable XML rather than throwing", () => {
    const doc = readCamt(CAMT_BROKEN_XML);
    expect(doc.groups).toEqual([]);
    expect(doc.diagnostics.map((d) => d.code)).toEqual(["unreadable_file"]);
  });

  it("reports a direction it does not recognise", () => {
    // camt writes CRDT/DBIT; "DEBIT" is not one of them, so the entry is
    // dropped instead of guessed at.
    const doc = readCamt(CAMT_MALFORMED);
    const codes = doc.diagnostics.map((d) => d.code);
    expect(codes).toContain("row_no_direction");
  });
});
