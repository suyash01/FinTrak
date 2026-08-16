import sys
import unittest
from pathlib import Path
from unittest import mock

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "src"))

from statement_parser.icici_cc_extractor import (
    PdfPasswordRequired,
    _amounts_after,
    _decrypt_if_needed,
    _dup_tolerant_pattern,
    _extract_summary,
    _parse_line,
    extract_transactions,
    to_csv_bytes,
)


class IciciParseLineTests(unittest.TestCase):
    def test_parses_debit_line(self):
        txn = _parse_line(
            "19/10/2024 10110206781 TATA AIG GENERAL INSUR MUMBAI IN 16 1,673.99",
            None,
        )
        self.assertIsNotNone(txn)
        self.assertEqual(txn.date, "19/10/2024")
        self.assertEqual(txn.ser_no, "10110206781")
        self.assertEqual(txn.description, "TATA AIG GENERAL INSUR MUMBAI IN")
        self.assertEqual(txn.reward_points, 16)
        self.assertEqual(txn.amount, 1673.99)
        self.assertEqual(txn.type, "Debit")
        self.assertIsNone(txn.card_number)

    def test_parses_credit_line_with_card_number(self):
        txn = _parse_line(
            "02/11/2024 10182675820 BBPS Payment received 0 1,40,439.00 CR",
            "4315XXXXXXXX9005",
        )
        self.assertIsNotNone(txn)
        self.assertEqual(txn.type, "Credit")
        self.assertEqual(txn.amount, 140439.00)
        self.assertEqual(txn.card_number, "4315XXXXXXXX9005")

    def test_parses_line_with_leading_sidebar_noise(self):
        txn = _parse_line(
            "offers, visit 10/11/2024 10225537371 Interest Amount Amortization 0 58.08",
            None,
        )
        self.assertIsNotNone(txn)
        self.assertEqual(txn.date, "10/11/2024")
        self.assertEqual(txn.amount, 58.08)

    def test_skips_header_lines(self):
        self.assertIsNone(_parse_line("Date", None))
        self.assertIsNone(_parse_line("#", None))
        self.assertIsNone(_parse_line("International Spends", None))

    def test_returns_none_for_unmatched_line(self):
        self.assertIsNone(_parse_line("random text", None))


class IciciDupTolerantPatternTests(unittest.TestCase):
    def test_matches_normal_label(self):
        pattern = _dup_tolerant_pattern("STATEMENT DATE")
        self.assertRegex("STATEMENT DATE", pattern)

    def test_matches_doubled_label(self):
        pattern = _dup_tolerant_pattern("STATEMENT DATE")
        self.assertRegex("SSTTAATTEEMMEENNTT DDAATTEE", pattern)


class IciciAmountsAfterTests(unittest.TestCase):
    def test_returns_amounts_after_label(self):
        text = "Total Amount due `1,000.00 `2,000.00"
        self.assertEqual(
            _amounts_after("Total Amount due", text, 2),
            ["1,000.00", "2,000.00"],
        )

    def test_returns_none_when_label_missing(self):
        self.assertEqual(_amounts_after("Missing", "no label", 2), [None, None])

    def test_pads_with_none_when_fewer_amounts(self):
        text = "Total Amount due `1,000.00"
        self.assertEqual(
            _amounts_after("Total Amount due", text, 3),
            ["1,000.00", None, None],
        )


class IciciSummaryTests(unittest.TestCase):
    def test_extracts_total_and_minimum_due(self):
        text = "Total Amount due `40,991.00 Minimum Amount due `2,049.55"
        summary = _extract_summary(text)
        self.assertEqual(summary["total_amount_due"], "40,991.00")
        self.assertEqual(summary["minimum_amount_due"], "2,049.55")

    def test_extracts_credit_limits(self):
        text = "Available Cash `2,29,000.00 `1,88,009.02 `50,000.00 `12,000.00"
        summary = _extract_summary(text)
        self.assertEqual(summary["credit_limit"], "2,29,000.00")
        self.assertEqual(summary["available_credit_limit"], "1,88,009.02")
        self.assertEqual(summary["cash_limit"], "50,000.00")
        self.assertEqual(summary["available_cash"], "12,000.00")

    def test_extracts_previous_balance_block(self):
        text = "Previous Balance `10,000.00 `5,000.00 `1,000.00 `4,000.00"
        summary = _extract_summary(text)
        self.assertEqual(summary["previous_balance"], "10,000.00")
        self.assertEqual(summary["purchases_charges"], "5,000.00")
        self.assertEqual(summary["cash_advances"], "1,000.00")
        self.assertEqual(summary["payments_credits"], "4,000.00")

    def test_extracts_statement_date_normal(self):
        summary = _extract_summary("STATEMENT DATE\nOctober 19, 2024")
        self.assertEqual(summary["statement_date"], "October 19, 2024")

    def test_extracts_statement_date_doubled(self):
        summary = _extract_summary("SSTTAATTEEMMEENNTT DDAATTEE\nOctober 19, 2024")
        self.assertEqual(summary["statement_date"], "October 19, 2024")

    def test_extracts_payment_due_date(self):
        summary = _extract_summary("PAYMENT DUE DATE\nNovember 8, 2024")
        self.assertEqual(summary["payment_due_date"], "November 8, 2024")

    def test_extracts_statement_period(self):
        text = "Statement period : September 20, 2024 to October 19, 2024"
        summary = _extract_summary(text)
        self.assertEqual(summary["statement_period_start"], "September 20, 2024")
        self.assertEqual(summary["statement_period_end"], "October 19, 2024")

    def test_extracts_invoice_no(self):
        summary = _extract_summary("Invoice No : 1234567890")
        self.assertEqual(summary["invoice_no"], "1234567890")

    def test_returns_empty_dict_when_nothing_matches(self):
        self.assertEqual(_extract_summary("nothing here"), {})


class IciciToCsvTests(unittest.TestCase):
    def test_to_csv_bytes_writes_header_and_rows(self):
        txns = [
            {
                "date": "19/10/2024",
                "ser_no": "10110206781",
                "description": "TATA AIG",
                "reward_points": 16,
                "amount": 1673.99,
                "type": "Debit",
                "card_number": None,
            },
        ]
        csv_bytes = to_csv_bytes(txns)
        text = csv_bytes.decode("utf-8")
        lines = text.strip().split("\r\n") if "\r\n" in text else text.strip().split("\n")
        self.assertEqual(
            lines[0],
            "date,ser_no,description,reward_points,amount,type,card_number",
        )
        self.assertEqual(
            lines[1],
            "19/10/2024,10110206781,TATA AIG,16,1673.99,Debit,",
        )


class IciciDecryptTests(unittest.TestCase):
    @mock.patch("statement_parser.icici_cc_extractor.PdfReader")
    def test_raises_when_password_missing(self, mock_reader_cls):
        mock_reader = mock.Mock()
        mock_reader.is_encrypted = True
        mock_reader_cls.return_value = mock_reader
        with self.assertRaises(PdfPasswordRequired):
            _decrypt_if_needed("/tmp/x.pdf", None)

    @mock.patch("statement_parser.icici_cc_extractor.PdfReader")
    def test_raises_when_password_incorrect(self, mock_reader_cls):
        mock_reader = mock.Mock()
        mock_reader.is_encrypted = True
        mock_reader.decrypt.return_value = 0
        mock_reader_cls.return_value = mock_reader
        with self.assertRaises(PdfPasswordRequired):
            _decrypt_if_needed("/tmp/x.pdf", "wrong")

    @mock.patch("statement_parser.icici_cc_extractor.PdfReader")
    def test_accepts_correct_password(self, mock_reader_cls):
        mock_reader = mock.Mock()
        mock_reader.is_encrypted = True
        mock_reader.decrypt.return_value = 1
        mock_reader_cls.return_value = mock_reader
        _decrypt_if_needed("/tmp/x.pdf", "right")  # should not raise

    @mock.patch("statement_parser.icici_cc_extractor.PdfReader")
    def test_noop_for_unencrypted_pdf(self, mock_reader_cls):
        mock_reader = mock.Mock()
        mock_reader.is_encrypted = False
        mock_reader_cls.return_value = mock_reader
        _decrypt_if_needed("/tmp/x.pdf", None)  # should not raise


class IciciExtractTransactionsTests(unittest.TestCase):
    def _make_page(self, text):
        page = mock.Mock()
        page.extract_text.return_value = text
        return page

    @mock.patch("statement_parser.icici_cc_extractor.pdfplumber.open")
    @mock.patch("statement_parser.icici_cc_extractor.PdfReader")
    def test_extracts_transactions_with_card_sections(self, mock_reader_cls, mock_open):
        mock_reader = mock.Mock()
        mock_reader.is_encrypted = False
        mock_reader_cls.return_value = mock_reader

        pdf = mock.Mock()
        pdf.pages = [
            self._make_page(
                "4315XXXXXXXX9005\n"
                "19/10/2024 10110206781 TATA AIG GENERAL INSUR MUMBAI IN 16 1,673.99\n"
                "22/10/2024 10120266708 Reversal of Fuel Surcharge 0 10.00 CR\n"
            ),
        ]
        mock_open.return_value.__enter__.return_value = pdf

        result = extract_transactions("/tmp/x.pdf")

        self.assertEqual(result["transaction_count"], 2)
        self.assertEqual(result["transactions"][0]["card_number"], "4315XXXXXXXX9005")
        self.assertEqual(result["transactions"][0]["type"], "Debit")
        self.assertEqual(result["transactions"][1]["type"], "Credit")

    @mock.patch("statement_parser.icici_cc_extractor.pdfplumber.open")
    @mock.patch("statement_parser.icici_cc_extractor.PdfReader")
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