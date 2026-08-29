import sys
import unittest
from pathlib import Path
from unittest import mock

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "src"))

from statement_parser.icici_bank_extractor import (
    extract_transactions,
    to_csv_bytes,
)

# Column layout copied from a real ICICI savings statement: the date column is
# matched on its right edge (x1 <= 70), everything else on its left edge.
_HEADER = {"text": "PARTICULARS", "x0": 140.3, "x1": 193.0, "top": 150.0, "bottom": 159.0}
_TOTAL = {"text": "Total:", "x0": 440.0, "x1": 460.0, "top": 400.0, "bottom": 409.0}


def _word(text, x0, x1, top):
    return {"text": text, "x0": x0, "x1": x1, "top": top, "bottom": top + 9.0}


# One statement page: a B/F (opening) row with no amount, a withdrawal row whose
# particulars wrap to a second line, a deposit row with its particulars line
# rendered ABOVE its own date, and a row that has a MODE prefix.
_PAGE_WORDS = [
    _HEADER,
    # B/F opening balance row (no deposit/withdrawal) -> excluded from output.
    _word("01-04-2024", 29.8, 68.0, 200.0),
    _word("B/F", 140.3, 150.8, 200.0),
    _word("1,94,497.61", 526.8, 564.1, 200.0),
    # Withdrawal row, particulars on the same line.
    _word("02-04-2024", 29.8, 68.0, 225.0),
    _word("ACH/Groww/x", 140.3, 310.8, 225.0),
    _word("2,000.00", 461.6, 489.1, 225.0),
    _word("1,92,497.61", 526.8, 564.1, 225.0),
    # Deposit row; its particulars continuation line sits ABOVE the date line.
    _word("UPI/thanks/StateBank", 140.3, 332.1, 253.0),
    _word("02-04-2024", 29.8, 68.0, 257.2),
    _word("30,000.00", 374.6, 406.1, 257.2),
    _word("1,62,497.61", 526.8, 564.1, 257.2),
    # Row with a MODE prefix ("DEBIT CARD") plus particulars.
    _word("05-04-2024", 29.8, 68.0, 290.0),
    _word("DEBIT", 73.0, 92.6, 290.0),
    _word("CARD", 94.4, 113.8, 290.0),
    _word("MPS/PUNJAB", 140.3, 183.2, 290.0),
    _word("1,500.00", 461.6, 489.1, 290.0),
    _word("18,842.61", 526.8, 564.1, 290.0),
    _TOTAL,
]


class _FakePage:
    def __init__(self, words, text=""):
        self._words = [dict(w) for w in words]
        self._text = text

    def extract_words(self, **kwargs):
        return [dict(w) for w in self._words]

    def extract_text(self):
        return self._text


class _FakeDoc:
    is_encrypted = False


class _FakePdf:
    def __init__(self, pages):
        self.pages = pages
        self.doc = _FakeDoc()

    def close(self):
        pass


def _open_fake_pdf(path, password=""):
    return _FakePdf([_FakePage(_PAGE_WORDS)])


class IciciBankExtractTransactionsTests(unittest.TestCase):
    @mock.patch("statement_parser.icici_bank_extractor.pdfplumber.open")
    def test_emits_canonical_fields_and_drops_bf_row(self, mock_open):
        mock_open.side_effect = _open_fake_pdf
        result = extract_transactions("/tmp/statement.pdf")

        self.assertEqual(result["bank"], "ICICI Bank")
        self.assertEqual(result["statement_type"], "savings_current_account")
        # The B/F opening-balance row is excluded from the transactions.
        self.assertEqual(result["transaction_count"], 3)
        self.assertEqual(len(result["transactions"]), 3)
        self.assertEqual(result["opening_balance"], 194497.61)
        self.assertEqual(result["closing_balance"], 18842.61)
        self.assertEqual(result["total_deposits"], 30000.0)
        self.assertEqual(result["total_withdrawals"], 3500.0)
        self.assertEqual(result["validation_errors"], [])

        first = result["transactions"][0]
        self.assertEqual(first["date"], "2024-04-02")
        self.assertEqual(first["description"], "ACH/Groww/x")
        self.assertEqual(first["amount"], 2000.0)
        self.assertEqual(first["type"], "Debit")
        self.assertEqual(first["withdrawal"], 2000.0)
        self.assertIsNone(first["deposit"])

        # Wrapped particulars rendered above the date line are still stitched in.
        second = result["transactions"][1]
        self.assertEqual(second["date"], "2024-04-02")
        self.assertEqual(second["description"], "UPI/thanks/StateBank")
        self.assertEqual(second["amount"], 30000.0)
        self.assertEqual(second["type"], "Credit")
        self.assertEqual(second["deposit"], 30000.0)
        self.assertIsNone(second["withdrawal"])

        # A MODE prefix is folded into the canonical description.
        third = result["transactions"][2]
        self.assertEqual(third["mode"], "DEBIT CARD")
        self.assertEqual(third["description"], "DEBIT CARD MPS/PUNJAB")
        self.assertEqual(third["amount"], 1500.0)
        self.assertEqual(third["type"], "Debit")

        # No row with a zero amount can ever leak through (import rejects those).
        self.assertTrue(all(t["amount"] > 0 for t in result["transactions"]))

    @mock.patch("statement_parser.icici_bank_extractor.pdfplumber.open")
    def test_to_csv_still_writes_rich_columns(self, mock_open):
        mock_open.side_effect = _open_fake_pdf
        result = extract_transactions("/tmp/statement.pdf")
        text = to_csv_bytes(result["transactions"]).decode("utf-8")
        self.assertTrue(text.startswith("date,mode,particulars,deposit,withdrawal,balance,account_number"))
        self.assertIn("ACH/Groww/x", text)


if __name__ == "__main__":
    unittest.main()