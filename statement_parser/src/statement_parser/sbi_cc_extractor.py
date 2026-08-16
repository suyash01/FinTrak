"""
extractor.py
------------
Core logic to pull the transaction table out of an SBI Card (PhonePe SBI Card
SELECT BLACK style) monthly statement PDF. Works with password-protected PDFs.

Usage as a library:

    from extractor import extract_transactions

    result = extract_transactions("statement.pdf", password="1234")
    result["transactions"]  # list of dicts
    result["summary"]       # dict of account summary fields (best effort)

Usage from the command line:

    python extractor.py statement.pdf --password 1234 --out transactions.csv
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
#   18 May 26 UPI-SUYASH MITTAL 310.00 C
#   04 May 26 TATA AIG GENERAL INSUR IN 31,939.99 D
# date | description (can contain spaces/punctuation) | amount | C or D
TXN_LINE_RE = re.compile(
    r"^(?P<date>\d{2}\s+[A-Za-z]{3}\s+\d{2,4})\s+"
    r"(?P<description>.+?)\s+"
    r"(?P<amount>[\d,]+\.\d{2})\s+"
    r"(?P<drcr>[CD])$"
)

# Lines that look like transactions but are actually headers/labels to skip.
SKIP_PREFIXES = (
    "TRANSACTIONS FOR",
    "Date",
    "for Statement Period",
)


class PdfPasswordRequired(Exception):
    """Raised when the PDF is encrypted and no/incorrect password was supplied."""


@dataclass
class Transaction:
    date: str
    description: str
    amount: float
    type: str  # "Credit" or "Debit"

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


def _parse_line(line: str) -> Optional[Transaction]:
    line = line.strip()
    if not line or line.startswith(SKIP_PREFIXES):
        return None
    match = TXN_LINE_RE.match(line)
    if not match:
        return None
    amount = float(match.group("amount").replace(",", ""))
    drcr = match.group("drcr")
    return Transaction(
        date=match.group("date"),
        description=" ".join(match.group("description").split()),
        amount=amount,
        type="Credit" if drcr == "C" else "Debit",
    )


def _extract_summary(full_text: str) -> dict[str, str]:
    """Best-effort extraction of a few key headline figures from the statement."""
    summary: dict[str, str] = {}

    patterns = {
        "total_amount_due": r"\*Total Amount Due\s*\(\s*`\s*\)\s*\n?\s*([\d,]+\.\d{2})",
        "minimum_amount_due": r"\*\*Minimum Amount Due\s*\(\s*`\s*\)\s*\n?\s*([\d,]+\.\d{2})",
        "statement_date": r"Statement Date\s*\n?\s*(\d{2}\s+[A-Za-z]{3}\s+\d{4})",
        "payment_due_date": r"Payment Due Date\s*\n?\s*(\d{2}\s+[A-Za-z]{3}\s+\d{4})",
        "credit_limit": r"Credit Limit.*?\n?\s*([\d,]+\.\d{2})",
        "available_credit_limit": r"Available Credit Limit.*?\n?\s*([\d,]+\.\d{2})",
    }

    for key, pattern in patterns.items():
        m = re.search(pattern, full_text, re.IGNORECASE | re.DOTALL)
        if m:
            summary[key] = m.group(1).strip()

    return summary


def extract_transactions(path: str, password: Optional[str] = None) -> dict[str, Any]:
    """
    Extract the transaction table (and a best-effort summary block) from an
    SBI Card statement PDF.

    Returns a dict: {"transactions": [...], "summary": {...}, "page_count": N}
    Raises PdfPasswordRequired if the file is encrypted and the password is
    missing or wrong.

    """
    _decrypt_if_needed(path, password)

    transactions: List[Transaction] = []
    full_text_parts: List[str] = []

    with pdfplumber.open(path, password=password) as pdf:
        page_count = len(pdf.pages)
        for page in pdf.pages:
            text = page.extract_text() or ""
            full_text_parts.append(text)
            for line in text.split("\n"):
                txn = _parse_line(line)
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
    writer = csv.DictWriter(buf, fieldnames=["date", "description", "amount", "type"])
    writer.writeheader()
    for t in transactions:
        writer.writerow(t)
    return buf.getvalue().encode("utf-8")


def main():
    parser = argparse.ArgumentParser(
        description="Extract the transaction table from an SBI Card statement PDF."
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
