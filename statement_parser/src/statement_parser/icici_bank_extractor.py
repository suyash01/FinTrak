"""
icici_sb_extractor.py
----------------------
Extractor for ICICI Bank Savings / Current account statement PDFs — the
"Statement of Transactions in ... Account XXXXXXXXnnnn" table format ICICI
uses for its retail savings/current account e-statements.

Implements the same interface pattern used by the other statement
extractors in this project (icici_cc_extractor, sbi_cc_extractor):

    extract_transactions(path, password=None) -> dict[str, Any]
    to_csv_bytes(transactions) -> bytes
    PdfPasswordRequired   (raised when the PDF is encrypted and no /
                            an incorrect password was supplied)

Register it in extractor.py the same way the others are registered:

    from .icici_sb_extractor import (
        PdfPasswordRequired,
        extract_transactions as _icici_sb_extract_transactions,
        to_csv_bytes as _icici_sb_to_csv_bytes,
    )
    register_extractor(
        "icici_sb", "ICICI Savings/Current Account",
        _icici_sb_extract_transactions, _icici_sb_to_csv_bytes,
    )

Why this needs its own parsing strategy
----------------------------------------
Unlike a bordered table, ICICI renders each row of this statement as a
paragraph: the PARTICULARS text can wrap onto 1-3 lines, while the DATE,
MODE, DEPOSITS/WITHDRAWALS and BALANCE values sit on a single line that is
vertically *centered* against that paragraph (not aligned to its first
line). `page.extract_table()` cannot reconstruct this reliably, so this
module works at the word/coordinate level instead:

1. Every word is classified into a column (date/mode/particulars/deposit/
   withdrawal/balance) purely by its horizontal (x) position - these
   boundaries are stable across the whole statement.
2. "Anchor rows" are the lines that carry a DATE token; each anchor row
   also carries that transaction's MODE/DEPOSIT/WITHDRAWAL/BALANCE (they
   are always co-located on the exact same line as the date).
3. Any PARTICULARS text that lands on a *different* line than its anchor
   (i.e. a wrapped continuation line) is stitched onto the nearest anchor
   by vertical distance, which correctly reassembles multi-line entries
   even when a continuation line appears visually *above* its own date.

Every page's rebuilt deposit/withdrawal subtotals are cross-checked
against that page's printed "Total:" row as a correctness guardrail (see
`validation_errors` in the returned dict).
"""

from __future__ import annotations

import csv
import io
import re
from collections import defaultdict
from dataclasses import dataclass, field
from typing import Any, Dict, Iterable, List, Literal, Optional, TypedDict, cast

import pdfplumber
from pdfplumber.page import Page

#: The masked/right-aligned word blocks pdfplumber.Page.extract_words()
#: returns. total=False because extract_words() emits several more keys
#: (e.g. "bottom", "upright", "direction") that this module never reads.
class Word(TypedDict, total=False):
    text: str
    x0: float
    x1: float
    top: float
    bottom: float

#: Which statement column a Word belongs to.
Column = Literal["date", "mode", "particulars", "deposit", "withdrawal", "balance"]


class Transaction(TypedDict):
    date: str
    mode: Optional[str]
    particulars: str
    deposit: Optional[float]
    withdrawal: Optional[float]
    balance: Optional[float]
    account_number: Optional[str]
    # Canonical fields consumed by the app's import pipeline (the same contract
    # the credit-card extractors emit): description is the full particulars
    # text (with any MODE prefix, e.g. "DEBIT CARD MPS/PUNJAB ..."), amount is
    # the deposit (Credit) or withdrawal (Debit).
    description: str
    amount: float
    type: str  # "Credit" | "Debit"


class AccountInfo(TypedDict):
    type: str
    number: str
    balance: Optional[float]


class Metadata(TypedDict):
    account_holder: Optional[str]
    customer_id: Optional[str]
    statement_period_from: Optional[str]
    statement_period_to: Optional[str]
    accounts: List[AccountInfo]


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


class _RawRecord(TypedDict):
    """One transaction's raw (still-string) values, keyed by column, before
    amount/date parsing. `particulars_lines` maps each wrapped line's
    vertical position ('top') to that line's text so lines can later be
    re-joined in reading order."""

    date_raw: str
    mode: str
    deposit_raw: Optional[str]
    withdrawal_raw: Optional[str]
    balance_raw: Optional[str]
    particulars_lines: Dict[float, str]


class PdfPasswordRequired(Exception):
    """Raised when the PDF is encrypted and needs a (correct) password."""


# ---------------------------------------------------------------------
# Column layout constants
# ---------------------------------------------------------------------
# A word is assigned to a column based on its left edge (x0), except the
# DATE column which is matched on its right edge (x1) since it's a short
# left-aligned fixed-width field starting at the same x0 as some other
# incidental text. Boundaries were derived empirically from the fixed
# ICICI statement template and validated against every page's printed
# subtotal row (see module docstring / project tests).
_COL_DATE_MAX_X1 = 70
_COL_MODE_MAX_X0 = 140
_COL_PARTICULARS_MAX_X0 = 355
_COL_DEPOSIT_MAX_X0 = 420
_COL_WITHDRAWAL_MAX_X0 = 505

# Words within this many PDF points of an anchor row's "top" are treated
# as being on that same visual line.
_ROW_TOLERANCE = 1.0

_DATE_RE = re.compile(r"^\d{2}-\d{2}-\d{4}$")
_HEADER_WORD = "PARTICULARS"
_TOTAL_WORD = "Total:"


def _classify_column(x0: float, x1: float) -> Column:
    if x1 <= _COL_DATE_MAX_X1:
        return "date"
    if x0 < _COL_MODE_MAX_X0:
        return "mode"
    if x0 < _COL_PARTICULARS_MAX_X0:
        return "particulars"
    if x0 < _COL_DEPOSIT_MAX_X0:
        return "deposit"
    if x0 < _COL_WITHDRAWAL_MAX_X0:
        return "withdrawal"
    return "balance"


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
    """Convert dd-mm-yyyy -> yyyy-mm-dd. Falls back to the raw text if it
    doesn't match the expected pattern."""
    m = re.match(r"^(\d{2})-(\d{2})-(\d{4})$", text)
    if not m:
        return text
    dd, mm, yyyy = m.groups()
    return f"{yyyy}-{mm}-{dd}"


def _open_pdf(path: str, password: Optional[str]) -> pdfplumber.PDF:
    try:
        pdf = pdfplumber.open(path, password=password or "")
    except Exception as exc:  # pdfplumber surfaces pypdf's decrypt errors
        message = str(exc).lower()
        if "password" in message or "encrypt" in message or "decrypt" in message:
            raise PdfPasswordRequired(
                "This PDF is password protected. Provide the correct password."
            ) from exc
        raise

    doc: Any = getattr(pdf, "doc", None)
    is_encrypted: bool = bool(getattr(doc, "is_encrypted", False))
    if is_encrypted:
        try:
            unlocked: bool = bool(doc.decrypt(password or ""))
        except Exception:
            unlocked = False
        if not unlocked:
            pdf.close()
            raise PdfPasswordRequired(
                "This PDF is password protected. Provide the correct password."
            )
    return pdf


# ---------------------------------------------------------------------
# Per-page parsing
# ---------------------------------------------------------------------

_ACCOUNT_TITLE_RE = re.compile(r"Account\s+([X\d]{6,})\s+in\s+INR")


def _find_section_account(words: List[Word]) -> Optional[str]:
    """Best-effort extraction of the masked account number from the
    'Statement of Transactions in Savings Account XXXXXXXX7034 in INR ...'
    title line that precedes the table header on each page."""
    title_words = [w for w in words if w["top"] < 200]
    text = " ".join(w["text"] for w in sorted(title_words, key=lambda w: (w["top"], w["x0"])))
    m = _ACCOUNT_TITLE_RE.search(text)
    return m.group(1) if m else None


@dataclass
class _PageParseResult:
    transactions: List[Transaction] = field(default_factory=list)
    printed_deposit_total: Optional[float] = None
    printed_withdrawal_total: Optional[float] = None
    printed_closing_balance: Optional[float] = None


def _parse_page(page: Page) -> Optional[_PageParseResult]:
    # extract_words() returns List[Dict[str, Any]]; cast to our narrower
    # Word shape since List is invariant and mypy won't accept the plain
    # dict list where List[Word] is expected.
    words: List[Word] = cast(
        List[Word], page.extract_words(use_text_flow=False, keep_blank_chars=False)
    )
    if not words:
        return None

    header_tops: List[float] = [w["top"] for w in words if w["text"] == _HEADER_WORD and w["x0"] < 200]
    if not header_tops:
        return None  # this page has no transaction table (e.g. cover/footer page)
    header_top: float = header_tops[0]

    total_tops: List[float] = [w["top"] for w in words if w["text"] == _TOTAL_WORD and w["top"] > header_top]
    total_top: float = min(total_tops, default=float("inf"))

    body: List[Word] = [w for w in words if header_top < w["top"] < total_top]
    if not body:
        return None

    account_number: Optional[str] = _find_section_account(words)

    # Anchor rows: every line carrying a date token. Deposit/withdrawal/
    # balance/mode always sit on the exact same line as their date.
    anchor_tops: List[float] = sorted(
        {
            round(w["top"], 1)
            for w in body
            if _classify_column(w["x0"], w["x1"]) == "date" and _DATE_RE.match(w["text"])
        }
    )
    if not anchor_tops:
        return None

    records: Dict[float, _RawRecord] = {}
    for top in anchor_tops:
        row_words: List[Word] = [w for w in body if abs(w["top"] - top) < _ROW_TOLERANCE]
        by_col: Dict[Column, List[Word]] = defaultdict(list)
        for w in row_words:
            by_col[_classify_column(w["x0"], w["x1"])].append(w)

        date_text: str = by_col["date"][0]["text"]
        mode_text: str = " ".join(w["text"] for w in sorted(by_col["mode"], key=lambda w: w["x0"]))
        deposit_text: Optional[str] = by_col["deposit"][0]["text"] if by_col["deposit"] else None
        withdrawal_text: Optional[str] = by_col["withdrawal"][0]["text"] if by_col["withdrawal"] else None
        balance_text: Optional[str] = by_col["balance"][0]["text"] if by_col["balance"] else None

        particulars_lines: Dict[float, str] = {}
        if by_col["particulars"]:
            first_top = round(by_col["particulars"][0]["top"], 1)
            particulars_lines[first_top] = " ".join(
                w["text"] for w in sorted(by_col["particulars"], key=lambda w: w["x0"])
            )

        records[top] = _RawRecord(
            date_raw=date_text,
            mode=mode_text,
            deposit_raw=deposit_text,
            withdrawal_raw=withdrawal_text,
            balance_raw=balance_text,
            particulars_lines=particulars_lines,
        )

    # Stitch wrapped PARTICULARS continuation lines (no date on that line)
    # onto the nearest anchor row by vertical distance. This correctly
    # handles continuation lines that render *above* their own anchor.
    particulars_words: List[Word] = [w for w in body if _classify_column(w["x0"], w["x1"]) == "particulars"]
    lines_by_top: Dict[float, List[Word]] = defaultdict(list)
    for w in particulars_words:
        lines_by_top[round(w["top"], 1)].append(w)

    for line_top, line_words in lines_by_top.items():
        if any(abs(line_top - a) < _ROW_TOLERANCE for a in anchor_tops):
            continue  # already captured directly on its own anchor row
        text = " ".join(w["text"] for w in sorted(line_words, key=lambda w: w["x0"]))
        nearest_anchor: float = min(anchor_tops, key=lambda a: abs(a - line_top))
        records[nearest_anchor]["particulars_lines"][line_top] = text

    transactions: List[Transaction] = []
    for top in anchor_tops:
        rec: _RawRecord = records[top]
        ordered_lines: List[str] = [
            rec["particulars_lines"][k] for k in sorted(rec["particulars_lines"].keys())
        ]
        particulars: str = re.sub(r"\s+", " ", " ".join(ordered_lines)).strip()
        mode_text: Optional[str] = rec["mode"] or None

        deposit: Optional[float] = _parse_amount(rec["deposit_raw"])
        withdrawal: Optional[float] = _parse_amount(rec["withdrawal_raw"])

        # The app's import contract expects a single amount + sign; derive it
        # from the deposit/withdrawal split (exactly one of the two is set).
        amount: float = 0.0
        txn_type: str = "Credit"
        if deposit:
            amount = deposit
        elif withdrawal:
            amount = withdrawal
            txn_type = "Debit"

        transactions.append(
            Transaction(
                date=_parse_date(rec["date_raw"]),
                mode=mode_text,
                particulars=particulars,
                deposit=deposit,
                withdrawal=withdrawal,
                balance=_parse_amount(rec["balance_raw"]),
                account_number=account_number,
                description=f"{mode_text} {particulars}".strip() if mode_text else particulars,
                amount=amount,
                type=txn_type,
            )
        )

    # Printed subtotal row for this page, used only as a validation check.
    total_words: List[Word] = sorted(
        (w for w in words if abs(w["top"] - total_top) < _ROW_TOLERANCE),
        key=lambda w: w["x0"],
    )
    amount_texts: List[str] = [w["text"] for w in total_words if w["text"] != _TOTAL_WORD]
    printed_deposit_total: Optional[float] = None
    printed_withdrawal_total: Optional[float] = None
    printed_closing_balance: Optional[float] = None
    if len(amount_texts) >= 3:
        printed_deposit_total = _parse_amount(amount_texts[-3])
        printed_withdrawal_total = _parse_amount(amount_texts[-2])
        printed_closing_balance = _parse_amount(amount_texts[-1])

    return _PageParseResult(
        transactions=transactions,
        printed_deposit_total=printed_deposit_total,
        printed_withdrawal_total=printed_withdrawal_total,
        printed_closing_balance=printed_closing_balance,
    )


# ---------------------------------------------------------------------
# Statement-level metadata (first page)
# ---------------------------------------------------------------------

_PERIOD_RE = re.compile(
    r"for the period\s+([A-Za-z]+ \d{1,2},\s*\d{4})\s*-\s*([A-Za-z]+ \d{1,2},\s*\d{4})"
)
_CUSTOMER_ID_RE = re.compile(r"Customer ID:\s*([A-Z0-9]+)")
_ACCOUNT_ROW_RE = re.compile(
    r"^(Current|Savings)\s+A/c\s+([X\d]{6,})\s+([\d,]+\.\d{2})", re.MULTILINE
)
_MONTHS = [
    "january", "february", "march", "april", "may", "june",
    "july", "august", "september", "october", "november", "december",
]


def _parse_long_date(text: str) -> Optional[str]:
    m = re.match(r"([A-Za-z]+)\s+(\d{1,2}),\s*(\d{4})", text.strip())
    if not m:
        return None
    month, day, year = m.groups()
    try:
        month_num = _MONTHS.index(month.lower()) + 1
    except ValueError:
        return None
    return f"{year}-{month_num:02d}-{int(day):02d}"


def _extract_metadata(statement_text: str) -> Metadata:
    metadata: Metadata = Metadata(
        account_holder=None,
        customer_id=None,
        statement_period_from=None,
        statement_period_to=None,
        accounts=[],
    )

    for line in (l.strip() for l in statement_text.splitlines() if l.strip()):
        if re.match(r"^(MR|MS|MRS|M/S)\.", line, re.IGNORECASE):
            metadata["account_holder"] = line.title().strip()
            break

    id_match = _CUSTOMER_ID_RE.search(statement_text)
    if id_match:
        metadata["customer_id"] = id_match.group(1)

    period_match = _PERIOD_RE.search(statement_text)
    if period_match:
        metadata["statement_period_from"] = _parse_long_date(period_match.group(1))
        metadata["statement_period_to"] = _parse_long_date(period_match.group(2))

    for acc_type, acc_number, balance in _ACCOUNT_ROW_RE.findall(statement_text):
        metadata["accounts"].append(
            AccountInfo(type=acc_type, number=acc_number, balance=_parse_amount(balance))
        )

    return metadata


# ---------------------------------------------------------------------
# Public API
# ---------------------------------------------------------------------


def extract_transactions(path: str, password: Optional[str] = None) -> StatementResult:
    """Extract every transaction from an ICICI Bank Savings/Current
    account statement PDF, in the order they appear in the statement.

    Returns:
        {
            "bank": "ICICI Bank",
            "statement_type": "savings_current_account",
            "account_holder": "Mr.Suyash Mittal" | None,
            "customer_id": "XXXXX5687" | None,
            "statement_period_from": "2024-04-01" | None,
            "statement_period_to": "2025-03-31" | None,
            "accounts": [
                {"type": "Savings", "number": "XXXXXXXX7034", "balance": 219825.76},
                {"type": "Current", "number": "XXXXXXXX1064", "balance": 0.0},
            ],
            "opening_balance": 194497.61 | None,
            "closing_balance": 219825.76 | None,
            "total_deposits": 3XXXXXX.XX,
            "total_withdrawals": 3XXXXXX.XX,
            "transaction_count": 578,  # the opening "B/F" row is excluded
            "transactions": [
                {
                    "date": "2024-04-02",
                    "mode": None,
                    "particulars": "ACH/Groww/ICIC7020810210002930/L22ZKJAKY3KY",
                    "deposit": None,
                    "withdrawal": 2000.0,
                    "balance": 192497.61,
                    "account_number": "XXXXXXXX7034",
                    "description": "ACH/Groww/ICIC7020810210002930/L22ZKJAKY3KY",
                    "amount": 2000.0,
                    "type": "Debit",
                },
                ...
            ],
            "validation_errors": [],  # non-empty => a page's rebuilt
                                      # subtotal didn't match the printed one
        }

    Raises:
        PdfPasswordRequired: the PDF is encrypted and the supplied
            password (or lack thereof) doesn't open it.
    """
    pdf = _open_pdf(path, password)
    try:
        # Customer name/ID live on page 1; the account list and statement
        # period line live on the page that starts the transaction table
        # (typically page 2 or 3, right after any promotional page). A
        # few pages is plenty and keeps this cheap on large statements.
        lookahead_text: str = "\n".join(
            (page.extract_text() or "") for page in pdf.pages[:4]
        )
        metadata: Metadata = _extract_metadata(lookahead_text)

        transactions: List[Transaction] = []
        total_deposits: float = 0.0
        total_withdrawals: float = 0.0
        validation_errors: List[str] = []

        page_number: int
        page: Page
        for page_number, page in enumerate(pdf.pages, start=1):
            result: Optional[_PageParseResult] = _parse_page(page)
            if result is None or not result.transactions:
                continue

            transactions.extend(result.transactions)

            page_deposit_sum: float = round(sum(t["deposit"] or 0.0 for t in result.transactions), 2)
            page_withdrawal_sum: float = round(
                sum(t["withdrawal"] or 0.0 for t in result.transactions), 2
            )
            total_deposits += page_deposit_sum
            total_withdrawals += page_withdrawal_sum

            if (
                result.printed_deposit_total is not None
                and abs(page_deposit_sum - result.printed_deposit_total) > 0.01
            ):
                validation_errors.append(
                    f"page {page_number}: deposit subtotal mismatch "
                    f"(computed {page_deposit_sum}, printed {result.printed_deposit_total})"
                )
            if (
                result.printed_withdrawal_total is not None
                and abs(page_withdrawal_sum - result.printed_withdrawal_total) > 0.01
            ):
                validation_errors.append(
                    f"page {page_number}: withdrawal subtotal mismatch "
                    f"(computed {page_withdrawal_sum}, printed {result.printed_withdrawal_total})"
                )

        opening_balance: Optional[float] = transactions[0]["balance"] if transactions else None
        closing_balance: Optional[float] = transactions[-1]["balance"] if transactions else None

        # The opening "B/F" row carries no deposit/withdrawal; its balance is
        # preserved as opening_balance above, but it is not an importable
        # transaction (the app rejects zero-amount rows), so drop it here.
        importable: List[Transaction] = [t for t in transactions if t["amount"] > 0]

        return StatementResult(
            bank="ICICI Bank",
            statement_type="savings_current_account",
            account_holder=metadata["account_holder"],
            customer_id=metadata["customer_id"],
            statement_period_from=metadata["statement_period_from"],
            statement_period_to=metadata["statement_period_to"],
            accounts=metadata["accounts"],
            opening_balance=opening_balance,
            closing_balance=closing_balance,
            total_deposits=round(total_deposits, 2),
            total_withdrawals=round(total_withdrawals, 2),
            transaction_count=len(importable),
            transactions=importable,
            validation_errors=validation_errors,
        )
    finally:
        pdf.close()


_CSV_FIELDS: List[str] = ["date", "mode", "particulars", "deposit", "withdrawal", "balance", "account_number"]


def to_csv_bytes(transactions: Iterable[Transaction]) -> bytes:
    """Serialize an iterable of transaction dicts (the 'transactions' key
    from extract_transactions()) to CSV bytes."""
    buffer: io.StringIO = io.StringIO()
    writer = csv.DictWriter(buffer, fieldnames=_CSV_FIELDS, extrasaction="ignore")
    writer.writeheader()
    for tx in transactions:
        writer.writerow(tx)
    return buffer.getvalue().encode("utf-8")


__all__ = [
    "PdfPasswordRequired",
    "extract_transactions",
    "to_csv_bytes",
]