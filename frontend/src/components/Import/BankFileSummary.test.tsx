import { describe, it, expect } from "vitest";
import { render, screen } from "@testing-library/react";
import BankFileSummary from "./BankFileSummary";
import { parseBankFile } from "../../lib/bankfiles";
import { CAMT_052_CARD } from "../../lib/bankfiles/camtFixtures";
import { OFX_TWO_ACCOUNTS } from "../../lib/bankfiles/fixtures";

function docOf(text: string) {
  return parseBankFile(new TextEncoder().encode(text));
}

describe("BankFileSummary", () => {
  it("names the format it read", () => {
    render(
      <BankFileSummary document={docOf(CAMT_052_CARD)} targetAccount="Savings" />,
    );
    expect(screen.getByText(/camt\.052/)).toBeInTheDocument();
  });

  it("counts the statements and the transactions in them", () => {
    render(
      <BankFileSummary document={docOf(OFX_TWO_ACCOUNTS)} targetAccount="Savings" />,
    );
    expect(screen.getByText(/2 statements/)).toBeInTheDocument();
    expect(screen.getByText(/4 transactions/)).toBeInTheDocument();
  });

  it("lists each statement the file described", () => {
    // The whole point: a file can carry more than one account, and the user has
    // to be able to see that before committing.
    render(
      <BankFileSummary document={docOf(OFX_TWO_ACCOUNTS)} targetAccount="Savings" />,
    );
    expect(screen.getByText("50100234567890")).toBeInTheDocument();
    expect(screen.getByText("50100999888777")).toBeInTheDocument();
  });

  it("shows the period and transaction count of each statement", () => {
    render(
      <BankFileSummary document={docOf(OFX_TWO_ACCOUNTS)} targetAccount="Savings" />,
    );
    expect(screen.getByText(/1 May 2026 .* 3 transactions/)).toBeInTheDocument();
    expect(screen.getByText(/1 Apr 2026 .* 1 transaction$/)).toBeInTheDocument();
  });

  it("shows the balances a camt.052 statement printed", () => {
    render(
      <BankFileSummary document={docOf(CAMT_052_CARD)} targetAccount="Card" />,
    );
    // These are the figures a later reconciliation would check the ledger
    // against, so they are worth seeing at import time.
    expect(screen.getByText(/38,150\.25/)).toBeInTheDocument();
    expect(screen.getByText(/40,649\.25/)).toBeInTheDocument();
  });

  it("says which account every row will land in", () => {
    // All statements are imported into the one account the user chose, so that
    // has to be stated rather than left to be discovered afterwards.
    render(
      <BankFileSummary
        document={docOf(OFX_TWO_ACCOUNTS)}
        targetAccount="HDFC Savings"
      />,
    );
    expect(
      screen.getByText(/will be imported into/i),
    ).toBeInTheDocument();
    expect(screen.getByText(/HDFC Savings/)).toBeInTheDocument();
  });

  it("says nothing about accounts it has no name for", () => {
    render(
      <BankFileSummary
        document={docOf(
          '<OFX><BANKMSGSRSV1><STMTTRNRS><STMTRS><CURDEF>INR<BANKTRANLIST><STMTTRN><TRNTYPE>DEBIT<DTPOSTED>20260518<TRNAMT>-1.00<NAME>X</NAME></STMTTRN></BANKTRANLIST></STMTRS></STMTTRNRS></BANKMSGSRSV1></OFX>',
        )}
        targetAccount="Savings"
      />,
    );
    expect(screen.queryByText("—")).not.toBeInTheDocument();
  });
});
