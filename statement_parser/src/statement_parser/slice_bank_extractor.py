"""
slice_bank_extractor.py
------------------------
Extractor for Slice Small Finance Bank ("slice small finance bank") savings
account e-statement PDFs - the monthly statements like
"slice_bank_savings_statement_-_Sep_2025.pdf". These PDFs are NOT encrypted
by default (unlike ICICI/SBI card statements), but the same
password-protection handling is kept for parity.

Template shape (every page):
  - Page header:        "01 Sep '25 - 30 Sep '25"  /  "1/3"
  - Page 1 only:        account-holder/contact block, then the printed summary
        "Opening balance Total credits Interest earned Total debits Closing balance"
        "+ + - ="
        "₹0.00 ₹1,56,030.00 ₹384.27 ₹52,187.34 ₹1,04,226.93"
    and the column header "DATE DETAILS REF NO. AMOUNT BALANCE" (pages 2+ have
    no column header - the table starts right under the period line).
  - One transaction row per line, e.g.:
        "01 Sep '25 Add Funds 202252447542774 ₹10,000.00 ₹10,000.00"
        "11 Sep '25 UPI Debit-SUYASH MITTAL-7044793121@ybl-52 804252545646524 -₹50,000.00 ₹50,143.24"
    Debit amounts carry a "-" prefix, credits are unsigned (no C/D column).
    2026 template: REF NO. runs up to 17 digits and the UPI detail prefix is
    hyphenated ("UPI-Credit-...", "UPI-Debit-...", "Account Transfer-Credit-...").
    The DETAILS column wraps onto the following line (no date/refno/amount on
    the wrap), e.g. "5463946648-Payment from slice".
  - Page footer:        "Need help? Contact our support team ... slice small finance bank"
                        ("Generated on ..." sits between the last row and the footer).

Notes (hard-won, do not regress):
  - The REF NO. column token is dropped from the output description (it is a
    statement-internal reference; the UPI refs live inside DETAILS).
  - Wrapped DETAILS continuation lines are stitched onto the previous
    transaction with NO space when the continuation starts mid-token
    (the UPI string is split mid-reference by the column wrap, e.g.
    "...@ybl-52" + "5463946648-Payment from slice" ->
    "...@ybl-525463946648-Payment from slice"). A plain space-join would
    leave a bogus gap inside the UPI reference. Mid-token starters, across
    both templates: digit, lowercase, '@', '/' and '-' (2026:
    "...Begusarai" + "-PYTM0123456-..."), plus uppercase letters glued to
    the token (2026: "...MITTAL-ICI" + "C0000570-..." and "...KUMAR SINH" +
    "A-IBKL0001077-..."). A continuation whose first TWO chars are uppercase
    letters is a genuine word wrap ("...using Paytm" + "UPI") and keeps its
    space.
  - The embedded Rubik font has no ToUnicode mapping for the "fl" and "fi"
    ligature glyphs, so pdfplumber renders them as "(cid:53)" and "(cid:65)"
    (verified against the embedded font program's glyph table: glyphID 53 =
    'fl', glyphID 65 = 'fi'). _clean_cid_artifacts() expands exactly those
    two codes so descriptions like "(cid:65)re cashback" come out as the
    readable "fire cashback"; unknown codes are left untouched.
  - "Interest Cr. for DD-Mon-YYYY" rows are ordinary Credit transactions;
    the printed "Interest earned" figure is a separate line item on top of
    "Total credits", so the rebuilt deposit sum must tie out against
    total_credits + interest_earned (the statement's own closing-balance
    equation is closing = opening + credits + interest - debits).
  - There is no opening B/F row: the first row's balance is the opening
    balance + first deposit, so the printed summary's opening_balance (0.00)
    is the authoritative figure, not the first transaction's balance.

Same public contract as the other extractors in this project:

    extract_transactions(path, password=None) -> dict[str, Any]
    to_csv_bytes(transactions) -> bytes
    PdfPasswordRequired  (raised when the PDF is encrypted and no/incorrect
                          password was supplied)

Register it in extractor.py the same way the others are registered:

    from .slice_bank_extractor import (
        PdfPasswordRequired,
        extract_transactions as _slice_extract_transactions,
        to_csv_bytes as _slice_to_csv_bytes,
    )
    register_extractor(
        "slice_bank", "Slice Small Finance Bank",
        _slice_extract_transactions, _slice_to_csv_bytes,
    )
"""

from __future__ import annotations

import argparse
import csv
import io
import json
import re
import sys
from typing import Any, Dict, Iterable, List, Optional, TypedDict

import pdfplumber
from pypdf import PdfReader


class Transaction(TypedDict):
    date: str
    description: str
    amount: float
    type: str  # "Credit" | "Debit"
    deposit: Optional[float]
    withdrawal: Optional[float]
    balance: Optional[float]


class StatementResult(TypedDict):
    bank: str
    statement_type: str
    account_holder: Optional[str]
    customer_id: Optional[str]
    statement_period_from: Optional[str]
    statement_period_to: Optional[str]
    accounts: List[Dict[str, Any]]
    opening_balance: Optional[float]
    closing_balance: Optional[float]
    total_deposits: float
    total_withdrawals: float
    transaction_count: int
    transactions: List[Transaction]
    validation_errors: List[str]
    summary: Dict[str, str]
    page_count: int


class PdfPasswordRequired(Exception):
    """Raised when the PDF is encrypted and no/incorrect password was supplied."""


def _decrypt_if_needed(path: str, password: Optional[str]) -> None:
    """Verify the PDF can be opened, raising PdfPasswordRequired with a clear
    message if a password is needed and missing/wrong. A fast pypdf pre-check;
    pdfplumber itself is given the password too."""
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


def _is_password_exception(exc: BaseException) -> bool:
    """True if any exception in the cause/context chain looks like a PDF
    decryption failure. pdfminer bare-raises PDFPasswordIncorrect (empty
    str()), and pdfplumber wraps it in a PdfminerException whose message is
    also empty, so message string-matching alone misses both."""
    seen: set[int] = set()
    cur: Optional[BaseException] = exc
    while cur is not None and id(cur) not in seen:
        seen.add(id(cur))
        name: str = cur.__class__.__name__
        if "Password" in name or "Encrypt" in name or "Decrypt" in name:
            return True
        cur = cur.__cause__ or cur.__context__
    return False


def _open_pdf(path: str, password: Optional[str]):
    # Callers run _decrypt_if_needed() first, which validates the password
    # with pypdf; this fallback only fires if pdfminer disagrees with pypdf.
    try:
        return pdfplumber.open(path, password=password or "")
    except Exception as exc:
        if _is_password_exception(exc):
            raise PdfPasswordRequired(
                "This PDF is password protected. Provide the correct password."
            ) from exc
        raise


# ---------------------------------------------------------------------
# Row-level parsing
# ---------------------------------------------------------------------

# "01 Sep '25" (day, month abbrev, 2-digit year with apostrophe — the
# statement's native date format, on every row).
_DATE_LED = r"\d{2} [A-Za-z]{3} '\d{2}"

# One transaction row:
#   date | DETAILS (greedy, may contain 10-digit phone/UPI tokens) |
#   REF NO. (the last 12-17 consecutive digits on the line) |
#   AMOUNT (₹ or -₹) | BALANCE (₹)
# The greedy DETAILS backtracking pins REF NO. to the LAST 12-17-digit run,
# which is always the statement's ref column (phone numbers in UPI ids are
# 10 digits and sit inside DETAILS).  The pre-2026 template tops out at 16
# digits; the 2026 template added 17-digit refs (e.g. "20260805380562101"),
# so the range is 12-17 across both templates.  Amounts/balances carry
# 0-2 DECIMAL PLACES: the 2026 PDF generator trims trailing zeros
# ("₹19,500", "₹26.7", "₹1,86,018.5" - the decimals are optional).
_TXN_RE = re.compile(
    r"^(?P<date>" + _DATE_LED + r")\s+"
    r"(?P<details>.+)\s+"
    r"(?P<refno>\d{12,17})\s+"
    r"(?P<amount>-?₹[\d,]+(?:\.\d{1,2})?)\s+"
    r"(?P<balance>₹[\d,]+(?:\.\d{1,2})?)$"
)

# Any non-transaction line that still begins with a date (the per-page
# period header like "01 Sep '25 - 30 Sep '25") is neither a transaction
# nor a continuation of one.
_DATE_LED_RE = re.compile(r"^" + _DATE_LED)

# Page footer / post-table fragments. "Generated on 02 Oct '25" appears on
# the LAST page directly after the final row — it MUST never be stitched
# onto that row's description.
_FOOTER_PREFIX = "need help?"
_SKIP_PREFIXES = (
    "generated on",
    "slice small",
    "date details",
    "opening balance",
    "+ +",
)

_MONTH_NUM = {
    "jan": 1, "feb": 2, "mar": 3, "apr": 4, "may": 5, "jun": 6,
    "jul": 7, "aug": 8, "sep": 9, "oct": 10, "nov": 11, "dec": 12,
}

# The embedded Rubik subset renders the "fl" and "fi" LIGATURES as glyphs
# with no ToUnicode mapping; pdfplumber exposes them as "(cid:53)"/"(cid:65)".
# Verified against the font program's glyph table. Unknown codes stay as-is.
_LIGATURE_BY_CID = {"53": "fl", "65": "fi"}
_CID_ARTIFACT_RE = re.compile(r"\(cid:(\d+)\)")


def _clean_cid_artifacts(text: str) -> str:
    return _CID_ARTIFACT_RE.sub(
        lambda m: _LIGATURE_BY_CID.get(m.group(1), m.group(0)), text
    )


def _parse_amount(text: Optional[str]) -> Optional[float]:
    if not text:
        return None
    cleaned = text.strip().replace("₹", "").replace(",", "")
    if not cleaned:
        return None
    try:
        return round(float(cleaned), 2)
    except ValueError:
        return None


def _parse_date(text: str) -> str:
    """Convert "01 Sep '25" -> "2025-09-01" (ISO, matching the icici_bank
    extractor and the frontend's expected import format). Falls back to the
    raw text if it doesn't match the expected pattern."""
    m = re.match(r"^(\d{2}) ([A-Za-z]{3}) '(\d{2})$", text.strip())
    if not m:
        return text
    dd, mon, yy = m.groups()
    mon_num = _MONTH_NUM.get(mon.lower())
    if mon_num is None:
        return text
    return f"20{yy}-{mon_num:02d}-{dd}"


def _starts_mid_token(line: str) -> bool:
    """True when `line` is a wrapped continuation of the previous line's
    token rather than a new field. The DETAILS column splits UPI strings
    mid-reference at the wrap ("...@ybl-52" + "5463946648-Payment from
    slice"), so continuations join with NO separator to reassemble the
    reference intact. Verified starters, across both templates:
      - lowercase / digit / '@' / '/'   (pre-2026 and 2026 templates)
      - '-'                             (2026: "...Begusarai" + "-PYTM0123456-...")
      - uppercase glued to the token    (2026: "...MITTAL-ICI" + "C0000570-..."
                                         and "...KUMAR SINH" + "A-IBKL0001077-...")
    A continuation whose first TWO chars are uppercase letters is a genuine
    word wrap ("...using Paytm" + "UPI") and keeps its space.
    """
    first: str = line[0]
    if first.islower() or first.isdigit() or first in "@/-":
        return True
    if first.isupper() and len(line) > 1:
        second: str = line[1]
        return second.isdigit() or second in "-/@"
    return False


def _build_transaction(match: "re.Match[str]") -> Transaction:
    amount_text: str = match.group("amount")
    negative: bool = amount_text.startswith("-")
    amount: Optional[float] = _parse_amount(amount_text.lstrip("-"))
    # Every row in this template carries an amount and a balance; the
    # optional-ness is defensive only.
    return Transaction(
        date=_parse_date(match.group("date")),
        description=_clean_cid_artifacts(
            re.sub(r"\s+", " ", match.group("details")).strip()
        ),
        amount=amount or 0.0,
        type="Debit" if negative else "Credit",
        deposit=None if negative else amount,
        withdrawal=amount if negative else None,
        balance=_parse_amount(match.group("balance")),
    )


def _parse_txn_line(line: str) -> Optional[Transaction]:
    """Parse one extract_text line as a transaction row, or None if it is
    not one (header, footer, summary, period line, wrapped continuation...)."""
    m = _TXN_RE.match(line.strip())
    if not m:
        return None
    return _build_transaction(m)


def _parse_page_text(text: str) -> List[Transaction]:
    """Parse one page's extract_text into transactions, stitching wrapped
    DETAILS continuation lines onto their transaction.

    Line loop:
      - A TXN match starts a new transaction (and becomes the stitch target).
      - The footer line ("Need help? ...") ends the table region: nothing
        after it is a continuation (it also catches "Generated on ...").
      - Anything else before the first row (period line, page number,
        account-holder block, summary, column header) is skipped because no
        previous transaction exists to stitch onto.
      - A date-led non-transaction line (period header) is never a
        continuation.
      - Any remaining line is a wrapped DETAILS continuation of the previous
        row and is joined onto it (no space when it starts mid-token).
    """
    transactions: List[Transaction] = []
    pending: Optional[Transaction] = None

    for raw_line in text.splitlines():
        line = raw_line.strip()
        if not line:
            continue

        txn = _parse_txn_line(line)
        if txn is not None:
            transactions.append(txn)
            pending = txn
            continue

        if line.lower().startswith(_FOOTER_PREFIX):
            pending = None  # table region ends here
            continue

        if pending is None:
            continue  # pre-table material (info block, summary, header)

        if _DATE_LED_RE.match(line):
            continue  # date-led non-transaction line (period header)

        if line.lower().startswith(_SKIP_PREFIXES):
            continue

        # Wrapped DETAILS continuation of the previous row.
        continuation: str = _clean_cid_artifacts(
            re.sub(r"\s+", " ", line)
        ).strip()
        if continuation:
            if _starts_mid_token(continuation):
                pending["description"] += continuation
            else:
                pending["description"] += " " + continuation

    return transactions


# ---------------------------------------------------------------------
# Statement-level metadata (page 1)
# ---------------------------------------------------------------------

# Account holder: the first pure uppercase name line (name block sits right
# above "Customer ID ..."; every other uppercase line carries digits).
_HOLDER_RE = re.compile(r"(?m)^([A-Z][A-Za-z .'’\-]+)$")
_CUSTOMER_ID_RE = re.compile(r"Customer ID\s+(\d+)")
_ACCOUNT_NO_RE = re.compile(r"A/C number\s+(\d+)")
_PERIOD_RE = re.compile(
    r"(\d{2} [A-Za-z]{3} '\d{2})\s*-\s*(\d{2} [A-Za-z]{3} '\d{2})"
)

# The printed five-figure summary row (page 1, under the "+ + - =" sign row):
#   ₹0.00 ₹1,56,030.00 ₹384.27 ₹52,187.34 ₹1,04,226.93
# = opening | total credits | interest earned | total debits | closing.
# Decimals are optional (0-2 places) - the 2026 generator trims trailing
# zeros, e.g. "₹80,786.54 ₹1,04,500 ₹731.96 ₹0 ₹1,86,018.5".
_SUMMARY_AMOUNTS_RE = re.compile(
    r"₹([\d,]+(?:\.\d{1,2})?)\s+₹([\d,]+(?:\.\d{1,2})?)\s+₹([\d,]+(?:\.\d{1,2})?)\s+"
    r"₹([\d,]+(?:\.\d{1,2})?)\s+₹([\d,]+(?:\.\d{1,2})?)"
)
_SUMMARY_KEYS = (
    "opening_balance",
    "total_credits",
    "interest_earned",
    "total_debits",
    "closing_balance",
)


def _extract_summary(full_text: str) -> Dict[str, float]:
    """Best-effort extraction of the five printed summary figures. The
    five-₹-amount line is unique to the summary row (transaction rows carry
    exactly two amounts)."""
    m = _SUMMARY_AMOUNTS_RE.search(full_text)
    if not m:
        return {}
    return {
        key: value
        for key, value in zip(_SUMMARY_KEYS, (_parse_amount(g) for g in m.groups()))
    }


def _extract_metadata(full_text: str) -> Dict[str, Any]:
    metadata: Dict[str, Any] = {
        "account_holder": None,
        "customer_id": None,
        "statement_period_from": None,
        "statement_period_to": None,
        "account_number": None,
    }

    holder = _HOLDER_RE.search(full_text)
    if holder:
        metadata["account_holder"] = holder.group(1).strip()

    cust = _CUSTOMER_ID_RE.search(full_text)
    if cust:
        metadata["customer_id"] = cust.group(1)

    acct = _ACCOUNT_NO_RE.search(full_text)
    if acct:
        metadata["account_number"] = acct.group(1)

    period = _PERIOD_RE.search(full_text)
    if period:
        metadata["statement_period_from"] = _parse_date(period.group(1))
        metadata["statement_period_to"] = _parse_date(period.group(2))

    return metadata


# ---------------------------------------------------------------------
# Public API
# ---------------------------------------------------------------------


def extract_transactions(path: str, password: Optional[str] = None) -> StatementResult:
    """Extract every transaction from a Slice Small Finance Bank savings
    account statement PDF, in the order they appear in the statement.

    Returns:
        {
            "bank": "Slice Small Finance Bank",
            "statement_type": "savings_account",
            "account_holder": "SUYASH MITTAL" | None,
            "customer_id": "380002027253" | None,
            "statement_period_from": "2025-09-01" | None,
            "statement_period_to": "2025-09-30" | None,
            "accounts": [{"type": "Savings", "number": "033325229949033",
                          "balance": 104226.93}],
            "opening_balance": 0.0,       # from the printed summary (no B/F row)
            "closing_balance": 104226.93, # last row's balance
            "total_deposits": 156414.27,  # Σ credits incl. interest rows
            "total_withdrawals": 52187.34,
            "transaction_count": 56,
            "transactions": [
                {"date": "2025-09-01", "description": "Add Funds",
                 "amount": 10000.0, "type": "Credit",
                 "deposit": 10000.0, "withdrawal": None, "balance": 10000.0},
                ...
            ],
            "validation_errors": [],   # non-empty => parsing drift
            "summary": {"opening_balance": "0.00", ...},  # printed figures (strings)
            "page_count": 3,
        }

    Raises:
        PdfPasswordRequired: the PDF is encrypted and the supplied password
            (or lack thereof) doesn't open it.
    """
    _decrypt_if_needed(path, password)
    pdf = _open_pdf(path, password)
    try:
        page_count: int = len(pdf.pages)
        # Page 1 (and 2, defensively) carries the name block, summary row and
        # period line; the transaction table spans every page.
        lookahead_text: str = "\n".join(
            (page.extract_text() or "") for page in pdf.pages[:2]
        )
        summary: Dict[str, float] = _extract_summary(lookahead_text)
        metadata: Dict[str, Any] = _extract_metadata(lookahead_text)

        transactions: List[Transaction] = []
        for page in pdf.pages:
            transactions.extend(_parse_page_text(page.extract_text() or ""))

        total_deposits: float = round(
            sum(t["deposit"] or 0.0 for t in transactions), 2
        )
        total_withdrawals: float = round(
            sum(t["withdrawal"] or 0.0 for t in transactions), 2
        )

        opening_balance: Optional[float] = summary.get("opening_balance")
        closing_balance: Optional[float] = (
            transactions[-1]["balance"] if transactions else None
        )

        validation_errors: List[str] = []

        # Balance chain: every consecutive pair must satisfy the running
        # balance equation (the decisive correctness check for this template).
        for i in range(1, len(transactions)):
            prev, cur = transactions[i - 1], transactions[i]
            if cur["balance"] is None:
                continue
            expected = round(
                (prev["balance"] or 0) + (cur["deposit"] or 0) - (cur["withdrawal"] or 0),
                2,
            )
            if abs(expected - cur["balance"]) > 0.01:
                validation_errors.append(
                    f"balance chain broken at txn {i}: {prev['date']} "
                    f"{prev['balance']} -> {cur['date']} dep {cur['deposit']} "
                    f"wd {cur['withdrawal']} bal {cur['balance']} expected {expected}"
                )

        # Printed summary guardrails. The statement's own equation is
        # closing = opening + credits + interest - debits, and the "Interest
        # earned" figure is a separate line item on top of "Total credits",
        # so rebuilt deposits must tie out against credits + interest.
        if summary:
            printed_deposits: float = round(
                summary["total_credits"] + summary["interest_earned"], 2
            )
            if abs(total_deposits - printed_deposits) > 0.01:
                validation_errors.append(
                    f"statement deposit total mismatch (computed "
                    f"{total_deposits}, printed credits+interest {printed_deposits})"
                )
            if abs(total_withdrawals - summary["total_debits"]) > 0.01:
                validation_errors.append(
                    f"statement withdrawal total mismatch (computed "
                    f"{total_withdrawals}, printed {summary['total_debits']})"
                )
            if (
                closing_balance is not None
                and abs(closing_balance - summary["closing_balance"]) > 0.01
            ):
                validation_errors.append(
                    f"closing balance mismatch (last row {closing_balance}, "
                    f"printed {summary['closing_balance']})"
                )
            implied_close: float = round(
                summary["opening_balance"] + total_deposits - total_withdrawals, 2
            )
            if abs(summary["closing_balance"] - implied_close) > 0.01:
                validation_errors.append(
                    f"printed summary does not balance (closing "
                    f"{summary['closing_balance']}, "
                    f"opening + credits + interest - debits {implied_close})"
                )

        account_number: Optional[str] = metadata["account_number"]
        accounts: List[Dict[str, Any]] = []
        if account_number:
            accounts.append(
                {
                    "type": "Savings",
                    "number": account_number,
                    "balance": closing_balance,
                }
            )

        summary_strings: Dict[str, str] = {
            key: f"{value:.2f}" for key, value in summary.items()
        }

        return StatementResult(
            bank="Slice Small Finance Bank",
            statement_type="savings_account",
            account_holder=metadata["account_holder"],
            customer_id=metadata["customer_id"],
            statement_period_from=metadata["statement_period_from"],
            statement_period_to=metadata["statement_period_to"],
            accounts=accounts,
            opening_balance=opening_balance,
            closing_balance=closing_balance,
            total_deposits=total_deposits,
            total_withdrawals=total_withdrawals,
            transaction_count=len(transactions),
            transactions=transactions,
            validation_errors=validation_errors,
            summary=summary_strings,
            page_count=page_count,
        )
    finally:
        pdf.close()


_CSV_FIELDS: List[str] = ["date", "description", "amount", "type"]


def to_csv_bytes(transactions: Iterable[Transaction]) -> bytes:
    """Serialize an iterable of transaction dicts (the 'transactions' key
    from extract_transactions()) to CSV bytes (UTF-8)."""
    buffer: io.StringIO = io.StringIO()
    writer = csv.DictWriter(buffer, fieldnames=_CSV_FIELDS, extrasaction="ignore")
    writer.writeheader()
    for tx in transactions:
        writer.writerow(tx)
    return buffer.getvalue().encode("utf-8")


def main() -> None:
    parser = argparse.ArgumentParser(
        description="Extract the transaction table from a Slice Small Finance "
        "Bank savings account statement PDF."
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
        print(json.dumps(result, indent=2, default=str))
        return

    if args.out.lower().endswith(".csv"):
        with open(args.out, "wb") as f:
            f.write(to_csv_bytes(result["transactions"]))
    else:
        with open(args.out, "w") as f:
            json.dump(result, f, indent=2, default=str)

    print(f"Wrote {result['transaction_count']} transactions to {args.out}")


__all__ = [
    "PdfPasswordRequired",
    "extract_transactions",
    "to_csv_bytes",
]