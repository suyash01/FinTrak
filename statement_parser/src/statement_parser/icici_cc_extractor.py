"""
icici_extractor.py
-------------------
Core logic to pull the transaction table out of an ICICI Bank Credit Card
monthly statement PDF (Amazon Pay, MakeMyTrip, HPCL Coral and similar retail
card templates). Works with password-protected PDFs.

This mirrors the structure of `sbi_cc_extractor.py` (same public surface:
`extract_transactions`, `to_csv_bytes`, `PdfPasswordRequired`, a CLI `main`)
so it can be dropped in as a sibling/"subclass-style" extractor for a
different issuer.

Usage as a library:

    from icici_extractor import extract_transactions

    result = extract_transactions("statement.pdf", password="1234")
    result["transactions"]  # list of dicts
    result["summary"]       # dict of account summary fields (best effort)

Usage from the command line:

    python icici_extractor.py statement.pdf --password 1234 --out transactions.csv

Notes on the ICICI template quirks handled here:
  * Some ICICI statements render bold header labels (STATEMENT DATE /
    PAYMENT DUE DATE) with every character doubled by the PDF's font
    ("SSTTAATTEEMMEENNTT DDAATTEE") depending on which card template was
    used. A "duplicate-letter tolerant" regex is used for those two labels
    so both the normal and doubled forms match.
  * A statement can contain more than one card/account section, each
    introduced by a standalone masked card-number line (e.g.
    "4315XXXXXXXX9005"). Every transaction under that heading is tagged
    with that card number until the next one is seen.
  * Credits (refunds, payments received, reversals) are suffixed "CR" in
    the amount column; everything else is a debit/purchase.
"""

import argparse
import csv
import io
import json
import re
import sys
from dataclasses import dataclass, asdict
from typing import Any, List, Optional

import pdfplumber
from pypdf import PdfReader


# A transaction line looks like:
#   19/10/2024 10110206781 TATA AIG GENERAL INSUR MUMBAI IN 16 1,673.99
#   22/10/2024 10120266708 Reversal of Fuel Surcharge 0 10.00 CR
#   02/11/2024 10182675820 BBPS Payment received 0 1,40,439.00 CR
# date | ser no. | description (can contain spaces/punctuation) |
# reward points | amount | optional CR marker
#
# NOTE: not anchored at the start of the line (only at the end). ICICI's
# statement layout places a sidebar promo/offer column next to the
# transaction table, and pdfplumber's reading order sometimes merges that
# sidebar text onto the front of a transaction line, e.g.:
#   "offers, visit 10/11/2024 10225537371 Interest Amount Amortization ... 58.08"
# Searching (rather than matching) for the date onward, while still
# requiring the line to end in the amount, reliably skips that leading noise.
TXN_LINE_RE = re.compile(
    r"(?P<date>\d{2}/\d{2}/\d{4})\s+"
    r"(?P<ser_no>\d+)\s+"
    r"(?P<description>.+?)\s+"
    r"(?P<reward_points>\d+)\s+"
    r"(?P<amount>[\d,]+\.\d{2})"
    r"(?:\s+(?P<crdr>CR))?$"
)

# A standalone line holding the masked card number that "heads" the
# transaction block beneath it, e.g. "4315XXXXXXXX9005".
CARD_NUM_RE = re.compile(r"^\d{4}X{8}\d{4}$")

# Lines that look like transactions but are actually headers/labels to skip.
SKIP_PREFIXES = (
    "Date",
    "#",
    "International Spends",
)


class PdfPasswordRequired(Exception):
    """Raised when the PDF is encrypted and no/incorrect password was supplied."""


@dataclass
class Transaction:
    date: str
    ser_no: str
    description: str
    reward_points: int
    amount: float
    type: str  # "Credit" or "Debit"
    card_number: Optional[str] = None

    def to_dict(self):
        return asdict(self)


def _decrypt_if_needed(path: str, password: Optional[str]) -> None:
    """
    Verify the PDF can be opened, raising PdfPasswordRequired with a clear
    message if a password is needed and missing/wrong. This is just a fast
    pre-check; pdfplumber itself is given the password too.
    """
    reader = PdfReader(path)
    if reader.is_encrypted:
        if not password:
            raise PdfPasswordRequired(
                "This PDF is password-protected. Please supply the password."
            )
        result = reader.decrypt(password)
        # pypdf returns 0 for failure, 1/2 for success (user/owner password)
        if result == 0:
            raise PdfPasswordRequired("Incorrect password for this PDF.")


def _parse_line(line: str, current_card: Optional[str]) -> Optional[Transaction]:
    line = line.strip()
    if not line or line.startswith(SKIP_PREFIXES):
        return None
    match = TXN_LINE_RE.search(line)
    if not match:
        return None
    amount = float(match.group("amount").replace(",", ""))
    is_credit = match.group("crdr") == "CR"
    return Transaction(
        date=match.group("date"),
        ser_no=match.group("ser_no"),
        description=" ".join(match.group("description").split()),
        reward_points=int(match.group("reward_points")),
        amount=amount,
        type="Credit" if is_credit else "Debit",
        card_number=current_card,
    )


def _dup_tolerant_pattern(label: str) -> str:
    """
    Build a regex fragment for `label` that matches it whether each letter
    appears once (normal) or is doubled by a font-rendering quirk seen on
    some ICICI statement templates, e.g. "STATEMENT DATE" or
    "SSTTAATTEEMMEENNTT DDAATTEE" both match.
    """
    parts: List[str] = []
    for ch in label:
        if ch.isalpha():
            parts.append(re.escape(ch) + "+")
        elif ch == " ":
            parts.append(r"\s+")
        else:
            parts.append(re.escape(ch))
    return "".join(parts)


def _amounts_after(label: str, text: str, count: int, window: int = 400) -> List[Optional[str]]:
    """
    Find `label` in `text` and return the first `count` rupee amounts
    (values after a `` ` `` currency glyph) that appear within `window`
    characters after it, in order. Missing values come back as None.
    """
    idx = text.find(label)
    if idx == -1:
        return [None] * count
    segment = text[idx: idx + window]
    amounts = re.findall(r"`([\d,]+\.\d{2})", segment)
    result = amounts[:count]
    result += [None] * (count - len(result))
    return result


def _extract_summary(full_text: str) -> dict[str, Any]:
    """Best-effort extraction of key headline figures from the statement."""
    summary: dict[str, Any] = {}

    total_due, = _amounts_after("Total Amount due", full_text, 1)
    min_due, = _amounts_after("Minimum Amount due", full_text, 1)
    if total_due:
        summary["total_amount_due"] = total_due
    if min_due:
        summary["minimum_amount_due"] = min_due

    credit_limit, avail_credit, cash_limit, avail_cash = _amounts_after(
        "Available Cash", full_text, 4
    )
    if credit_limit:
        summary["credit_limit"] = credit_limit
    if avail_credit:
        summary["available_credit_limit"] = avail_credit
    if cash_limit:
        summary["cash_limit"] = cash_limit
    if avail_cash:
        summary["available_cash"] = avail_cash

    prev_bal, purchases, cash_adv, payments = _amounts_after(
        "Previous Balance", full_text, 4
    )
    if prev_bal:
        summary["previous_balance"] = prev_bal
    if purchases:
        summary["purchases_charges"] = purchases
    if cash_adv:
        summary["cash_advances"] = cash_adv
    if payments:
        summary["payments_credits"] = payments

    stmt_date_re = re.compile(
        _dup_tolerant_pattern("STATEMENT DATE") + r"\s*\n?\s*"
        r"([A-Za-z]+ \d{1,2},? \d{4})"
    )
    m = stmt_date_re.search(full_text)
    if m:
        summary["statement_date"] = m.group(1).strip().rstrip(",")

    due_date_re = re.compile(
        _dup_tolerant_pattern("PAYMENT DUE DATE") + r"\s*\n?\s*"
        r"([A-Za-z]+ \d{1,2},? \d{4})"
    )
    m = due_date_re.search(full_text)
    if m:
        summary["payment_due_date"] = m.group(1).strip().rstrip(",")

    m = re.search(
        r"Statement period\s*:\s*([A-Za-z]+ \d{1,2},? \d{4})\s*to\s*"
        r"([A-Za-z]+ \d{1,2},? \d{4})",
        full_text,
    )
    if m:
        summary["statement_period_start"] = m.group(1).strip()
        summary["statement_period_end"] = m.group(2).strip()

    m = re.search(r"Invoice No\s*:\s*(\S+)", full_text)
    if m:
        summary["invoice_no"] = m.group(1)

    return summary


def extract_transactions(path: str, password: Optional[str] = None) -> dict[str, Any]:
    """
    Extract the transaction table (and a best-effort summary block) from an
    ICICI Bank Credit Card statement PDF.

    Returns a dict: {"transactions": [...], "summary": {...}, "page_count": N}
    Raises PdfPasswordRequired if the file is encrypted and the password is
    missing or wrong.
    """
    _decrypt_if_needed(path, password)

    transactions: List[Transaction] = []
    full_text_parts: List[str] = []
    current_card: Optional[str] = None

    with pdfplumber.open(path, password=password) as pdf:
        page_count = len(pdf.pages)
        for page in pdf.pages:
            text = page.extract_text() or ""
            full_text_parts.append(text)
            for line in text.split("\n"):
                stripped = line.strip()
                if CARD_NUM_RE.match(stripped):
                    current_card = stripped
                    continue
                txn = _parse_line(line, current_card)
                if txn:
                    transactions.append(txn)

    full_text = "\n".join(full_text_parts)
    summary = _extract_summary(full_text)

    return {
        "transactions": [t.to_dict() for t in transactions],
        "summary": summary,
        "page_count": page_count,
        "transaction_count": len(transactions),
    }


def to_csv_bytes(transactions: List[dict[str, Any]]) -> bytes:
    buf = io.StringIO()
    writer = csv.DictWriter(
        buf,
        fieldnames=[
            "date",
            "ser_no",
            "description",
            "reward_points",
            "amount",
            "type",
            "card_number",
        ],
    )
    writer.writeheader()
    for t in transactions:
        writer.writerow(t)
    return buf.getvalue().encode("utf-8")


def main():
    parser = argparse.ArgumentParser(
        description="Extract the transaction table from an ICICI Bank Credit Card statement PDF."
    )
    parser.add_argument("pdf_path", help="Path to the statement PDF")
    parser.add_argument(
        "--password", "-p", default=None, help="PDF password, if it is protected"
    )
    parser.add_argument(
        "--out", "-o", default=None,
        help="Output file path (.csv or .json). If omitted, prints JSON to stdout."
    )
    args = parser.parse_args()

    try:
        result = extract_transactions(args.pdf_path, password=args.password)
    except PdfPasswordRequired as e:
        print(f"Error: {e}", file=sys.stderr)
        sys.exit(2)
    except Exception as e:
        print(f"Error reading PDF: {e}", file=sys.stderr)
        sys.exit(1)

    if args.out is None:
        print(json.dumps(result, indent=2))
        return

    if args.out.lower().endswith(".csv"):
        with open(args.out, "wb") as f:
            f.write(to_csv_bytes(result["transactions"]))
    else:
        with open(args.out, "w") as f:
            json.dump(result, f, indent=2)

    print(f"Wrote {result['transaction_count']} transactions to {args.out}")


if __name__ == "__main__":
    main()