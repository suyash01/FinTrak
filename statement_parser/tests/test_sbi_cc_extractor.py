import sys
import unittest
from pathlib import Path
from unittest import mock

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "src"))

from statement_parser.sbi_cc_extractor import (
    PdfPasswordRequired,
    _decrypt_if_needed,
    _extract_summary,
    _parse_line,
    extract_transactions,
    to_csv_bytes,
)


class SbiParseLineTests(unittest.TestCase):
    def test_parses_credit_line(self):
        txn = _parse_line("18 May 26 UPI-SUYASH MITTAL 310.00 C")
        self.assertIsNotNone(txn)
        self.assertEqual(txn.date, "18 May 26")
        self.assertEqual(txn.description, "UPI-SUYASH MITTAL")
        self.assertEqual(txn.amount, 310.00)
        self.assertEqual(txn.type, "Credit")

    def test_parses_debit_line_with_comma_amount(self):
        txn = _parse_line("04 May 26 TATA AIG GENERAL INSUR IN 31,939.99 D")
        self.assertIsNotNone(txn)
        self.assertEqual(txn.amount, 31939.99)
        self.assertEqual(txn.type, "Debit")

    def test_parses_four_digit_year(self):
        txn = _parse_line("18 May 2026 UPI-SUYASH MITTAL 310.00 C")
        self.assertIsNotNone(txn)
        self.assertEqual(txn.date, "18 May 2026")

    def test_normalizes_whitespace_in_description(self):
        txn = _parse_line("18 May 26  UPI   SUYASH   MITTAL  310.00 C")
        self.assertIsNotNone(txn)
        self.assertEqual(txn.description, "UPI SUYASH MITTAL")

    def test_skips_empty_line(self):
        self.assertIsNone(_parse_line(""))

    def test_skips_header_lines(self):
        self.assertIsNone(_parse_line("Date"))
        self.assertIsNone(_parse_line("TRANSACTIONS FOR"))
        self.assertIsNone(_parse_line("for Statement Period"))

    def test_returns_none_for_unmatched_line(self):
        self.assertIsNone(_parse_line("some random text"))


class SbiSummaryTests(unittest.TestCase):
    def test_extracts_total_amount_due(self):
        summary = _extract_summary("*Total Amount Due (`) 40,991.00")
        self.assertEqual(summary["total_amount_due"], "40,991.00")

    def test_extracts_minimum_amount_due(self):
        summary = _extract_summary("**Minimum Amount Due (`) 2,049.55")
        self.assertEqual(summary["minimum_amount_due"], "2,049.55")

    def test_extracts_statement_date(self):
        summary = _extract_summary("Statement Date\n18 May 2026")
        self.assertEqual(summary["statement_date"], "18 May 2026")

    def test_extracts_payment_due_date(self):
        summary = _extract_summary("Payment Due Date\n02 Jun 2026")
        self.assertEqual(summary["payment_due_date"], "02 Jun 2026")

    def test_extracts_credit_limit(self):
        summary = _extract_summary("Credit Limit 2,29,000.00")
        self.assertEqual(summary["credit_limit"], "2,29,000.00")

    def test_extracts_available_credit_limit(self):
        summary = _extract_summary("Available Credit Limit 1,88,009.02")
        self.assertEqual(summary["available_credit_limit"], "1,88,009.02")

    def test_returns_empty_dict_when_nothing_matches(self):
        self.assertEqual(_extract_summary("nothing here"), {})


class SbiToCsvTests(unittest.TestCase):
    def test_to_csv_bytes_writes_header_and_rows(self):
        txns = [
            {"date": "18 May 26", "description": "UPI-SUYASH MITTAL", "amount": 310.0, "type": "Credit"},
            {"date": "04 May 26", "description": "TATA AIG", "amount": 31939.99, "type": "Debit"},
        ]
        csv_bytes = to_csv_bytes(txns)
        text = csv_bytes.decode("utf-8")
        lines = text.strip().split("\r\n") if "\r\n" in text else text.strip().split("\n")
        self.assertEqual(lines[0], "date,description,amount,type")
        self.assertEqual(lines[1], "18 May 26,UPI-SUYASH MITTAL,310.0,Credit")
        self.assertEqual(lines[2], "04 May 26,TATA AIG,31939.99,Debit")


class SbiDecryptTests(unittest.TestCase):
    @mock.patch("statement_parser.sbi_cc_extractor.PdfReader")
    def test_raises_when_password_missing(self, mock_reader_cls):
        mock_reader = mock.Mock()
        mock_reader.is_encrypted = True
        mock_reader_cls.return_value = mock_reader
        with self.assertRaises(PdfPasswordRequired):
            _decrypt_if_needed("/tmp/x.pdf", None)

    @mock.patch("statement_parser.sbi_cc_extractor.PdfReader")
    def test_raises_when_password_incorrect(self, mock_reader_cls):
        mock_reader = mock.Mock()
        mock_reader.is_encrypted = True
        mock_reader.decrypt.return_value = 0
        mock_reader_cls.return_value = mock_reader
        with self.assertRaises(PdfPasswordRequired):
            _decrypt_if_needed("/tmp/x.pdf", "wrong")

    @mock.patch("statement_parser.sbi_cc_extractor.PdfReader")
    def test_accepts_correct_password(self, mock_reader_cls):
        mock_reader = mock.Mock()
        mock_reader.is_encrypted = True
        mock_reader.decrypt.return_value = 1
        mock_reader_cls.return_value = mock_reader
        _decrypt_if_needed("/tmp/x.pdf", "right")  # should not raise

    @mock.patch("statement_parser.sbi_cc_extractor.PdfReader")
    def test_noop_for_unencrypted_pdf(self, mock_reader_cls):
        mock_reader = mock.Mock()
        mock_reader.is_encrypted = False
        mock_reader_cls.return_value = mock_reader
        _decrypt_if_needed("/tmp/x.pdf", None)  # should not raise


class SbiExtractTransactionsTests(unittest.TestCase):
    def _make_page(self, text):
        page = mock.Mock()
        page.extract_text.return_value = text
        return page

    @mock.patch("statement_parser.sbi_cc_extractor.pdfplumber.open")
    @mock.patch("statement_parser.sbi_cc_extractor.PdfReader")
    def test_extracts_transactions_and_summary(self, mock_reader_cls, mock_open):
        mock_reader = mock.Mock()
        mock_reader.is_encrypted = False
        mock_reader_cls.return_value = mock_reader

        pdf = mock.Mock()
        pdf.pages = [
            self._make_page(
                "18 May 26 UPI-SUYASH MITTAL 310.00 C\n"
                "04 May 26 TATA AIG GENERAL INSUR IN 31,939.99 D\n"
                "Date\n"
            ),
            self._make_page("*Total Amount Due (`) 40,991.00"),
        ]
        mock_open.return_value.__enter__.return_value = pdf

        result = extract_transactions("/tmp/x.pdf")

        self.assertEqual(result["transaction_count"], 2)
        self.assertEqual(result["page_count"], 2)
        self.assertEqual(result["transactions"][0]["description"], "UPI-SUYASH MITTAL")
        self.assertEqual(result["transactions"][0]["type"], "Credit")
        self.assertEqual(result["transactions"][1]["type"], "Debit")
        self.assertEqual(result["summary"]["total_amount_due"], "40,991.00")

    @mock.patch("statement_parser.sbi_cc_extractor.pdfplumber.open")
    @mock.patch("statement_parser.sbi_cc_extractor.PdfReader")
    def test_passes_password_to_pdfplumber(self, mock_reader_cls, mock_open):
        mock_reader = mock.Mock()
        mock_reader.is_encrypted = False
        mock_reader_cls.return_value = mock_reader
        pdf = mock.Mock()
        pdf.pages = []
        mock_open.return_value.__enter__.return_value = pdf

        extract_transactions("/tmp/x.pdf", password="1234")

        mock_open.assert_called_once_with("/tmp/x.pdf", password="1234")


if __name__ == "__main__":
    unittest.main()