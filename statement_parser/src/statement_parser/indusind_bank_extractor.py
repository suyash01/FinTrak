"""
indusind_bank_extractor.py
--------------------------
Extractor for IndusInd Bank savings-account e-statement PDFs — the
"STATEMENT OF CUSTOMER" format used for retail savings / 3-in-1 (UPSTOX)
accounts, e.g. "2023-07-04 Indusind Bank Jun2023_....pdf".

Template fingerprints (verified against the Jun-2023 statement, account
157044793121, 2 pages):
  - Page 1 is a cover page: the customer block ("SUYASH MITTAL Date :
    01-Jul-2023", period line), the "Relationship Summary for Customer ID
    - 43800785" section, and the Current / Savings Account summary table
        "<acct> <type> INR <lien> <balance>"   e.g. "157044793121 UPSTOX 3 IN 1 INR 0.00 5,892.32"
    It carries NO transaction rows.
  - Page 2 (and any further transaction pages) starts with the
    account-details block ("Transaction History for Savings Account,
    Current Account and Overdraft Account." table + "Statement Period /
    Email Id / ..." lines) followed by the transaction table with the
    column header
        Date | Particulars | Chq No/Ref No | Withdrawal | Deposit | Balance
    Column dividers sit at x = 31.4 / 77.7 / 250.4 / 328.9 / 407.4 /
    485.9 / 564.4; transaction rows are Helvetica 7pt, the header and the
    Brought/Carried Forward rows are Arial-Bold 7pt.
  - Every table row starts with a date "DD-Mon-YYYY" in the DATE column.
    The FIRST row is "Brought Forward" (date + balance only) and the LAST
    row is "Carried Forward" (date + balance only) — both zero-amount
    rows: they are dropped from the output `transactions` (the app
    rejects zero-amount rows) but their balances are preserved as
    opening_balance / closing_balance and they participate in the
    balance-chain validation.
  - Withdrawal, Deposit and Balance are separate RIGHT-aligned columns
    with no +/- sign, so a money token's column is decided by its right
    edge (x1), never by its text. Right edges equal the column headers'
    own right edges (405.7 / 484.2 / 562.7 on this template), which are
    re-derived per page from the header words when present.

Parsing strategy: WORD/GEOMETRY based (like the icici_bank fallback), NOT
ruled-grid — the table's row rules are drawn irregularly (the UPI row's
cell band has no rules at all), so rule-band segmentation cannot work
here. Each visual line is grouped by word top; a line whose DATE column
carries a "DD-Mon-YYYY" token starts a new transaction; date-less lines
below it with particulars-column words are wrapped continuation lines
stitched onto that transaction. The column wrap splits UPI references
mid-token, so mid-token continuations join with NO space
(".../YESB/Q893267845@yb" + "l/Payme002261100000025/..." ->
".../YESB/Q893267845@ybl/Payme002261100000025/..."), while word-boundary
wraps keep the space ("... STO" + "RESOthPSP/Payment ..." -> "... STO
RESOthPSP/Payment ..."). The same mid-token join also occurs WITHIN a
printed line ("Consolidated Interest PaymentInterest run" — the PDF
generator glues "Payment"+"Interest" into one token; it is preserved
verbatim, not re-spaced).

Guardrails (surfaced as `validation_errors`):
  - balance chain over EVERY parsed row incl. Brought/Carried Forward:
        balance[i] == balance[i-1] + deposit[i] - withdrawal[i]
  - dates non-decreasing across rows
  - the page-1 relationship-summary balance (when found) must equal the
    closing balance (the last row's / Carried Forward's balance)

Same public contract as the other extractors in this project:

    extract_transactions(path, password=None) -> dict[str, Any]
    to_csv_bytes(transactions) -> bytes
    PdfPasswordRequired  (raised when the PDF is encrypted and no/incorrect
                          password was supplied)

Register it in extractor.py the same way the others are registered:

    from .indusind_bank_extractor import (
        PdfPasswordRequired,
        extract_transactions as _indusind_bank_extract_transactions,
        to_csv_bytes as _indusind_bank_to_csv_bytes,
    )
    register_extractor(
        "indusind_bank", "IndusInd Bank Statement",
        _indusind_bank_extract_transactions, _indusind_bank_to_csv_bytes,
    )
"""

from __future__ import annotations

import argparse
import csv
import io
import json
import re
import sys
from typing import Any, Dict, Iterable, List, Optional, Tuple, TypedDict

import pdfplumber
from pypdf import PdfReader


class Word(TypedDict):
    text: str
    x0: float
    x1: float
    top: float
    bottom: float


class Transaction(TypedDict):
    date: str
    description: str
    amount: float
    type: str  # "Credit" | "Debit"
    deposit: Optional[float]
    withdrawal: Optional[float]
    balance: Optional[float]


class AccountInfo(TypedDict):
    type: str
    number: str
    balance: Optional[float]


class StatementResult(TypedDict):
    bank: str
    statement_type: str
    account_holder: Optional[str]
    customer_id: Optional[str]
    statement_period_from: Optional[str]
    statement_period_to: Optional[str]
    accounts: List[AccountInfo]
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
    """Fast pypdf pre-check: raise PdfPasswordRequired with a clear message
    when the PDF is encrypted and the password is missing/wrong."""
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
# Column layout constants (derived from the real statement geometry)
# ---------------------------------------------------------------------
# Column dividers on the transaction table: x = 31.4 | 77.7 | 250.4 |
# 328.9 | 407.4 | 485.9 | 564.4. Dates are short left-aligned tokens whose
# right edge stays below 77.7; particulars start at ~80.9.
_DATE_MAX_X1 = 78.0
_PARTICULARS_MIN_X0 = 77.0

# Amount right edges when the column header line is not present on a page
# (header-derived edges are preferred; these are the observed fallbacks).
_FALLBACK_WD_RIGHT = 405.7
_FALLBACK_DEP_RIGHT = 484.2
_FALLBACK_BAL_RIGHT = 562.7

# x1 matching tolerance around a column's right edge.
_COL_TOL = 2.0

# Words within this many PDF points of each other share a visual line
# (rows are 8+pt apart at 7pt font, so 1.0pt never merges rows).
_ROW_TOLERANCE = 1.0

# Post-table footer lines (end the table region; never stitched).
_TABLE_END_PREFIXES = (
    "this is a computer generated",
    "kindly check your statement",
    "acronyms :",
    "for any queries",
    "registered office:",
    "corporate identity number",
)

_MONTH_NUM = {
    "jan": 1, "feb": 2, "mar": 3, "apr": 4, "may": 5, "jun": 6,
    "jul": 7, "aug": 8, "sep": 9, "oct": 10, "nov": 11, "dec": 12,
}

_DATE_RE = re.compile(r"^\d{2}-[A-Za-z]{3}-\d{4}$")
_AMOUNT_RE = re.compile(r"^\d{1,3}(,\d{3})*\.\d{2}$")


def _parse_amount(text: Optional[str]) -> Optional[float]:
    if not text:
        return None
    cleaned = text.strip().replace(",", "")
    if not cleaned:
        return None
    try:
        return round(float(cleaned), 2)
    except ValueError:
        return None


def _parse_date(text: str) -> str:
    """Convert "01-Jun-2023" -> "2023-06-01" (ISO, matching the other
    extractors and the frontend's expected import format). Returns the raw
    text if it doesn't match."""
    m = re.match(r"^(\d{2})-([A-Za-z]{3})-(\d{4})$", text.strip())
    if not m:
        return text
    dd, mon, yyyy = m.groups()
    mon_num = _MONTH_NUM.get(mon.lower())
    if mon_num is None:
        return text
    return f"{yyyy}-{mon_num:02d}-{dd}"


def _starts_mid_token(line: str) -> bool:
    """True when `line` is a wrapped continuation of the previous line's
    token rather than a new field. The particulars column splits UPI
    strings mid-reference at the wrap ("...@yb" + "l/Payme..."), so
    continuations starting with a digit, lowercase letter, '@' or '/' are
    joined with NO separator to reassemble the reference intact."""
    first: str = line[0]
    return first.islower() or first.isdigit() or first in "@/"


# ---------------------------------------------------------------------
# Line / row parsing (word geometry)
# ---------------------------------------------------------------------


def _group_lines(words: List[Word]) -> List[List[Word]]:
    """Cluster words into visual lines by their top coordinate."""
    ordered = sorted(words, key=lambda w: (w["top"], w["x0"]))
    lines: List[List[Word]] = []
    for word in ordered:
        if lines and abs(word["top"] - lines[-1][0]["top"]) <= _ROW_TOLERANCE:
            lines[-1].append(word)
        else:
            lines.append([word])
    return lines


def _find_header_edges(lines: List[List[Word]]) -> Tuple[float, float, float]:
    """Return (withdrawal_right, deposit_right, balance_right) for the page.
    Money tokens are right-aligned to their column header's own right edge,
    so the header line (when present) defines the exact column edges;
    otherwise the fixed template constants are used."""
    wd = _FALLBACK_WD_RIGHT
    dep = _FALLBACK_DEP_RIGHT
    bal = _FALLBACK_BAL_RIGHT
    for line in lines:
        texts = [w["text"] for w in line]
        if "Particulars" in texts and "Balance" in texts:
            by_text = {w["text"]: w for w in line}
            for label, _ in (("Withdrawal", wd), ("Deposit", dep), ("Balance", bal)):
                header_word = by_text.get(label)
                if header_word is not None:
                    if label == "Withdrawal":
                        wd = header_word["x1"]
                    elif label == "Deposit":
                        dep = header_word["x1"]
                    else:
                        bal = header_word["x1"]
            break
    return wd, dep, bal


def _is_amount(word: Word) -> bool:
    return bool(_AMOUNT_RE.match(word["text"]))


def _line_text(line: List[Word]) -> str:
    return " ".join(w["text"] for w in line).strip()


def _parse_page(
    page_words: List[Word],
) -> Tuple[List[Transaction], Optional[Transaction]]:
    """Parse one page's words into transactions (rows start with a date in
    the DATE column; date-less particulars lines are stitched onto the most
    recent row). Returns (rows, last_row) — `last_row` is kept only so callers
    could extend across pages; this template never splits a row across pages,
    so extract_transactions() ignores it.

    Returns the rows INCLUDING Brought Forward / Carried Forward; the caller
    decides what is importable.
    """
    lines: List[List[Word]] = _group_lines(page_words)
    wd_right, dep_right, bal_right = _find_header_edges(lines)

    rows: List[Transaction] = []
    cur: Optional[Transaction] = None

    for line in lines:
        joined: str = _line_text(line)
        if joined.lower().startswith(_TABLE_END_PREFIXES):
            cur = None  # table region ended; stop stitching
            continue

        date_word: Optional[Word] = None
        for word in line:
            if _DATE_RE.match(word["text"]) and word["x1"] <= _DATE_MAX_X1:
                date_word = word
                break

        if date_word is not None:
            # ---- a new transaction row ----
            deposit: Optional[float] = None
            withdrawal: Optional[float] = None
            balance: Optional[float] = None
            for word in line:
                if not _is_amount(word):
                    continue
                value = _parse_amount(word["text"])
                if value is None:
                    continue
                if word["x1"] <= wd_right + _COL_TOL:
                    withdrawal = value
                elif word["x1"] <= dep_right + _COL_TOL:
                    deposit = value
                elif word["x1"] <= bal_right + _COL_TOL:
                    balance = value
            # Description = every non-amount word at/right of the
            # particulars column (includes any Chq No/Ref No token).
            text = " ".join(
                w["text"]
                for w in sorted(line, key=lambda w: w["x0"])
                if w["x0"] >= _PARTICULARS_MIN_X0 and not _is_amount(w)
            ).strip()
            text = re.sub(r"\s+", " ", text)
            if withdrawal is not None:
                amount, txn_type = withdrawal, "Debit"
            elif deposit is not None:
                amount, txn_type = deposit, "Credit"
            else:
                amount, txn_type = 0.0, "Credit"  # Brought/Carried Forward
            txn: Transaction = Transaction(
                date=_parse_date(date_word["text"]),
                description=text,
                amount=amount,
                type=txn_type,
                deposit=deposit,
                withdrawal=withdrawal,
                balance=balance,
            )
            rows.append(txn)
            cur = txn
            continue

        # ---- possible wrapped continuation line ----
        if cur is None:
            continue
        part_words = [
            w for w in line
            if w["x0"] >= _PARTICULARS_MIN_X0 and not _is_amount(w)
        ]
        if not part_words:
            continue  # account-block / misc text outside the particulars column
        if any(w["x1"] <= _DATE_MAX_X1 for w in line):
            continue  # date-column token without a date shape — not ours
        fragment: str = re.sub(r"\s+", " ", _line_text(part_words)).strip()
        if not fragment:
            continue
        if _starts_mid_token(fragment):
            cur["description"] += fragment
        else:
            cur["description"] += " " + fragment

    return rows, cur


# ---------------------------------------------------------------------
# Statement-level metadata
# ---------------------------------------------------------------------

# Page 2 account-details table row:
#   "157044793121 SUYASH MITTAL Primary Holder 43800785"
_ACCOUNT_TABLE_RE = re.compile(
    r"(?m)^(\d{12})\s+([A-Z][A-Z .'’-]+?)\s+(?:Primary|Joint) Holder\s+(\d+)$"
)

# Page 1 relationship summary row:
#   "157044793121 UPSTOX 3 IN 1 INR 0.00 5,892.32"
# (account | type | lien amount | balance) — balance is the closing figure.
_ACCOUNT_SUMMARY_RE = re.compile(
    r"(?m)^(\d{12})\s+([A-Z0-9 .'’-]+?)\s+INR\s+([\d,]+\.\d{2})\s+([\d,]+\.\d{2})$"
)

# Page 1: "Relationship Summary for Customer ID - 43800785" (fallback; the
# account-table regex above already yields the customer id).
_CUSTOMER_ID_RE = re.compile(r"Customer ID[^\S\n]*[-:][^\S\n]*(\d{6,12})")

# Page 1: "SUYASH MITTAL Date : 01-Jul-2023" (fallback holder).
_HOLDER_RE = re.compile(r"(?m)^([A-Z][A-Z .'’-]{3,}?)\s+Date\s*:")

# "Period : 01-Jun-2023 To 30-Jun-2023" (page 1) /
# "Statement Period : 01-Jun-2023 TO 30-Jun-2023" (page 2).
_PERIOD_RE = re.compile(
    r"(?:Statement Period|Period)\s*:\s*(\d{2}-[A-Za-z]{3}-\d{4})\s*"
    r"(?:TO|To|to)\s*(\d{2}-[A-Za-z]{3}-\d{4})"
)


def _extract_metadata(full_text: str) -> Dict[str, Any]:
    metadata: Dict[str, Any] = {
        "account_holder": None,
        "customer_id": None,
        "account_number": None,
        "account_type": None,
        "statement_period_from": None,
        "statement_period_to": None,
        "summary_balance": None,
    }

    table = _ACCOUNT_TABLE_RE.search(full_text)
    if table:
        metadata["account_number"] = table.group(1)
        metadata["account_holder"] = table.group(2).strip()
        metadata["customer_id"] = table.group(3)

    summary = _ACCOUNT_SUMMARY_RE.search(full_text)
    if summary:
        metadata["account_number"] = metadata["account_number"] or summary.group(1)
        metadata["account_type"] = summary.group(2).strip()
        metadata["summary_balance"] = _parse_amount(summary.group(4))

    cust = _CUSTOMER_ID_RE.search(full_text)
    if cust:
        metadata["customer_id"] = metadata["customer_id"] or cust.group(1)

    holder = _HOLDER_RE.search(full_text)
    if holder:
        metadata["account_holder"] = metadata["account_holder"] or holder.group(1).strip()

    period = _PERIOD_RE.search(full_text)
    if period:
        metadata["statement_period_from"] = _parse_date(period.group(1))
        metadata["statement_period_to"] = _parse_date(period.group(2))

    return metadata


# ---------------------------------------------------------------------
# Public API
# ---------------------------------------------------------------------


def extract_transactions(path: str, password: Optional[str] = None) -> StatementResult:
    """Extract every transaction from an IndusInd Bank savings account
    statement PDF, in the order they appear in the statement.

    Returns:
        {
            "bank": "IndusInd Bank",
            "statement_type": "savings_account",
            "account_holder": "SUYASH MITTAL" | None,
            "customer_id": "43800785" | None,
            "statement_period_from": "2023-06-01" | None,
            "statement_period_to": "2023-06-30" | None,
            "accounts": [{"type": "UPSTOX 3 IN 1", "number": "157044793121",
                          "balance": 5892.32}],
            "opening_balance": 15843.32,   # Brought Forward row's balance
            "closing_balance": 5892.32,    # Carried Forward row's balance
            "total_deposits": 144.0,
            "total_withdrawals": 10095.0,
            "transaction_count": 3,
            "transactions": [ ... "Credit"/"Debit" rows only — the
                              zero-amount Brought/Carried Forward rows are
                              dropped (the app rejects zero-amount rows) ...],
            "validation_errors": [],   # non-empty => parsing drift
            "summary": {"opening_balance": "15843.32",
                        "closing_balance": "5892.32"},
            "page_count": 2,
        }

    Raises:
        PdfPasswordRequired: the PDF is encrypted and the supplied password
            (or lack thereof) doesn't open it.
    """
    _decrypt_if_needed(path, password)
    pdf = _open_pdf(path, password)
    try:
        page_count: int = len(pdf.pages)

        # Metadata comes from the text layer (cover page + account details);
        # transaction rows come from word geometry.
        full_text: str = "\n".join(
            (page.extract_text() or "") for page in pdf.pages
        )
        metadata: Dict[str, Any] = _extract_metadata(full_text)

        parsed: List[Transaction] = []
        for page in pdf.pages:
            words: List[Word] = [
                w for w in page.extract_words()
                if all(k in w for k in ("text", "x0", "x1", "top", "bottom"))
            ]
            rows, _ = _parse_page(words)
            parsed.extend(rows)

        total_deposits: float = round(
            sum(t["deposit"] or 0.0 for t in parsed), 2
        )
        total_withdrawals: float = round(
            sum(t["withdrawal"] or 0.0 for t in parsed), 2
        )

        opening_balance: Optional[float] = (
            parsed[0]["balance"] if parsed else None
        )
        closing_balance: Optional[float] = (
            parsed[-1]["balance"] if parsed else None
        )

        # The app rejects zero-amount rows (Brought/Carried Forward), so
        # they are dropped from the importable list though they drove the
        # opening/closing balances and the chain validation above.
        importable: List[Transaction] = [t for t in parsed if t["amount"] > 0]

        validation_errors: List[str] = []

        # Balance chain over EVERY parsed row (incl. B/F and C/F).
        for i in range(1, len(parsed)):
            prev, cur = parsed[i - 1], parsed[i]
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

        # Dates must be non-decreasing (row-order drift guard).
        for i in range(1, len(parsed)):
            if parsed[i]["date"] < parsed[i - 1]["date"]:
                validation_errors.append(
                    f"dates not monotonic at txn {i}: "
                    f"{parsed[i - 1]['date']} -> {parsed[i]['date']}"
                )

        # The page-1 relationship-summary balance ties to the closing figure.
        summary_balance: Optional[float] = metadata["summary_balance"]
        if (
            summary_balance is not None
            and closing_balance is not None
            and abs(summary_balance - closing_balance) > 0.01
        ):
            validation_errors.append(
                f"closing balance mismatch (last row {closing_balance}, "
                f"relationship summary {summary_balance})"
            )

        accounts: List[AccountInfo] = []
        if metadata["account_number"]:
            accounts.append(
                {
                    "type": metadata["account_type"] or "Savings",
                    "number": metadata["account_number"],
                    "balance": closing_balance,
                }
            )

        summary_strings: Dict[str, str] = {}
        if opening_balance is not None:
            summary_strings["opening_balance"] = f"{opening_balance:.2f}"
        if closing_balance is not None:
            summary_strings["closing_balance"] = f"{closing_balance:.2f}"

        return StatementResult(
            bank="IndusInd Bank",
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
            transaction_count=len(importable),
            transactions=importable,
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
        description="Extract the transaction table from an IndusInd Bank "
        "savings account statement PDF."
    )
    parser.add_argument("pdf_path", help="Path to the statement PDF")
    parser.add_argument(
        "--password", "-p", default=None, help="PDF password, if it is protected"
    )
    parser.add_argument(
        "--out", "-o", default=None,
        help="Output file path (.csv or .json). If omitted, prints JSON to stdout.",
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