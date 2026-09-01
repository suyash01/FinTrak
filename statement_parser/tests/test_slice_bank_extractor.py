import sys
import unittest
from pathlib import Path
from unittest import mock

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "src"))

from statement_parser import extractor as extractor_module
from statement_parser.slice_bank_extractor import (
    PdfPasswordRequired,
    _clean_cid_artifacts,
    _decrypt_if_needed,
    _extract_metadata,
    _extract_summary,
    _parse_date,
    _parse_txn_line,
    extract_transactions,
    to_csv_bytes,
)

# A small but fully self-consistent statement: the balance chain holds and
# the printed summary ties out against the rebuilt totals.
#   opening 0.00 | credits 107.00 | interest 7.53 | debits 70.00 | closing 44.53
_PAGE1 = """\
01 Sep '25 - 30 Sep '25
1/3
SUYASH MITTAL
Customer ID 380002027253 Account SAVINGS
Phone 7044793121 A/C number 033325229949033
Opening balance Total credits Interest earned Total debits Closing balance
+ + - =
₹0.00 ₹107.00 ₹7.53 ₹70.00 ₹44.53
DATE DETAILS REF NO. AMOUNT BALANCE
01 Sep '25 Add Funds 202252447542774 ₹100.00 ₹100.00
02 Sep '25 Interest Cr. for 01-Sep-2025 20225245391304 ₹7.53 ₹107.53
03 Sep '25 UPI Debit-SUYASH MITTAL-7044793121@ybl-52 804252545646524 -₹40.00 ₹67.53
5463946648-Payment from slice
04 Sep '25 (cid:65)re cashback 2022525610130174 ₹7.00 ₹74.53
Need help? Contact our support team at help@sliceit.com or +91-8048329999 slice small finance bank
"""

_PAGE2 = """\
01 Sep '25 - 30 Sep '25
2/3
05 Sep '25 UPI Debit-RANJEET SAH-q28182547@ybl-5256 8042525610080474 -₹30.00 ₹44.53
64498436
Generated on 06 Oct '25
Need help? Contact our support team at help@sliceit.com or +91-8048329999 slice small finance bank
"""


def _make_page(text):
    page = mock.Mock()
    page.extract_text.return_value = text
    return page


class SliceTxnLineTests(unittest.TestCase):
    def test_parses_credit_line(self):
        txn = _parse_txn_line(
            "01 Sep '25 Add Funds 202252447542774 ₹10,000.00 ₹10,000.00"
        )
        self.assertIsNotNone(txn)
        self.assertEqual(txn["date"], "2025-09-01")
        self.assertEqual(txn["description"], "Add Funds")
        self.assertEqual(txn["amount"], 10000.00)
        self.assertEqual(txn["type"], "Credit")
        self.assertEqual(txn["deposit"], 10000.00)
        self.assertIsNone(txn["withdrawal"])
        self.assertEqual(txn["balance"], 10000.00)

    def test_parses_debit_line_and_drops_refno(self):
        txn = _parse_txn_line(
            "11 Sep '25 UPI Debit-SUYASH MITTAL-7044793121@ybl-52 "
            "804252545646524 -₹50,000.00 ₹50,143.24"
        )
        self.assertIsNotNone(txn)
        self.assertEqual(txn["description"], "UPI Debit-SUYASH MITTAL-7044793121@ybl-52")
        self.assertEqual(txn["amount"], 50000.00)
        self.assertEqual(txn["type"], "Debit")
        self.assertIsNone(txn["deposit"])
        self.assertEqual(txn["withdrawal"], 50000.00)
        self.assertEqual(txn["balance"], 50143.24)

    def test_parses_interest_line(self):
        txn = _parse_txn_line(
            "02 Sep '25 Interest Cr. for 01-Sep-2025 20225245391304 ₹7.53 ₹50,007.53"
        )
        self.assertIsNotNone(txn)
        self.assertEqual(txn["date"], "2025-09-02")
        self.assertEqual(txn["description"], "Interest Cr. for 01-Sep-2025")
        self.assertEqual(txn["amount"], 7.53)
        self.assertEqual(txn["type"], "Credit")

    def test_cleans_ligature_artifact_in_details(self):
        txn = _parse_txn_line(
            "13 Sep '25 (cid:65)re cashback 2022525610130174 ₹7.00 ₹50,135.36"
        )
        self.assertIsNotNone(txn)
        self.assertEqual(txn["description"], "fire cashback")

    def test_rejects_summary_amounts_row(self):
        self.assertIsNone(
            _parse_txn_line("₹0.00 ₹1,56,030.00 ₹384.27 ₹52,187.34 ₹1,04,226.93")
        )

    def test_rejects_column_header(self):
        self.assertIsNone(_parse_txn_line("DATE DETAILS REF NO. AMOUNT BALANCE"))

    def test_rejects_period_line(self):
        self.assertIsNone(_parse_txn_line("01 Sep '25 - 30 Sep '25"))

    def test_rejects_continuation_line_without_amounts(self):
        self.assertIsNone(_parse_txn_line("5463946648-Payment from slice"))


class SliceCidAndDateTests(unittest.TestCase):
    def test_clean_cid_artifacts_expands_ligatures(self):
        self.assertEqual(_clean_cid_artifacts("(cid:65)re cashback"), "fire cashback")
        self.assertEqual(_clean_cid_artifacts("3rd (cid:53)oor"), "3rd floor")
        self.assertEqual(
            _clean_cid_artifacts("slice small (cid:65)nance bank"),
            "slice small finance bank",
        )

    def test_clean_cid_artifacts_keeps_unknown_codes(self):
        self.assertEqual(_clean_cid_artifacts("(cid:99)xyz"), "(cid:99)xyz")

    def test_clean_cid_artifacts_handles_plain_text(self):
        self.assertEqual(_clean_cid_artifacts("Add Funds"), "Add Funds")

    def test_parse_date_normalizes_to_iso(self):
        self.assertEqual(_parse_date("01 Sep '25"), "2025-09-01")
        self.assertEqual(_parse_date("31 Dec '99"), "2099-12-31")
        self.assertEqual(_parse_date("30 Sep '25"), "2025-09-30")

    def test_parse_date_falls_back_on_garbage(self):
        self.assertEqual(_parse_date("nonsense"), "nonsense")


class SliceSummaryAndMetadataTests(unittest.TestCase):
    def test_extracts_five_printed_figures(self):
        summary = _extract_summary(_PAGE1)
        self.assertEqual(
            summary,
            {
                "opening_balance": 0.00,
                "total_credits": 107.00,
                "interest_earned": 7.53,
                "total_debits": 70.00,
                "closing_balance": 44.53,
            },
        )

    def test_extract_summary_empty_when_absent(self):
        self.assertEqual(_extract_summary("no figures here"), {})

    def test_extracts_metadata(self):
        meta = _extract_metadata(_PAGE1)
        self.assertEqual(meta["account_holder"], "SUYASH MITTAL")
        self.assertEqual(meta["customer_id"], "380002027253")
        self.assertEqual(meta["account_number"], "033325229949033")
        self.assertEqual(meta["statement_period_from"], "2025-09-01")
        self.assertEqual(meta["statement_period_to"], "2025-09-30")


class SliceDecryptTests(unittest.TestCase):
    @mock.patch("statement_parser.slice_bank_extractor.PdfReader")
    def test_raises_when_password_missing(self, mock_reader_cls):
        mock_reader = mock.Mock()
        mock_reader.is_encrypted = True
        mock_reader_cls.return_value = mock_reader
        with self.assertRaises(PdfPasswordRequired):
            _decrypt_if_needed("/tmp/x.pdf", None)

    @mock.patch("statement_parser.slice_bank_extractor.PdfReader")
    def test_raises_when_password_incorrect(self, mock_reader_cls):
        mock_reader = mock.Mock()
        mock_reader.is_encrypted = True
        mock_reader.decrypt.return_value = 0
        mock_reader_cls.return_value = mock_reader
        with self.assertRaises(PdfPasswordRequired):
            _decrypt_if_needed("/tmp/x.pdf", "wrong")

    @mock.patch("statement_parser.slice_bank_extractor.PdfReader")
    def test_accepts_correct_password(self, mock_reader_cls):
        mock_reader = mock.Mock()
        mock_reader.is_encrypted = True
        mock_reader.decrypt.return_value = 1
        mock_reader_cls.return_value = mock_reader
        _decrypt_if_needed("/tmp/x.pdf", "right")  # should not raise

    @mock.patch("statement_parser.slice_bank_extractor.PdfReader")
    def test_noop_for_unencrypted_pdf(self, mock_reader_cls):
        mock_reader = mock.Mock()
        mock_reader.is_encrypted = False
        mock_reader_cls.return_value = mock_reader
        _decrypt_if_needed("/tmp/x.pdf", None)  # should not raise


class SliceExtractTransactionsTests(unittest.TestCase):
    @mock.patch("statement_parser.slice_bank_extractor.pdfplumber.open")
    @mock.patch("statement_parser.slice_bank_extractor.PdfReader")
    def test_extracts_full_statement(self, mock_reader_cls, mock_open):
        mock_reader = mock.Mock()
        mock_reader.is_encrypted = False
        mock_reader_cls.return_value = mock_reader

        pdf = mock.Mock()
        pdf.pages = [_make_page(_PAGE1), _make_page(_PAGE2)]
        mock_open.return_value = pdf

        result = extract_transactions("/tmp/slice.pdf")

        self.assertEqual(result["bank"], "Slice Small Finance Bank")
        self.assertEqual(result["statement_type"], "savings_account")
        self.assertEqual(result["account_holder"], "SUYASH MITTAL")
        self.assertEqual(result["customer_id"], "380002027253")
        self.assertEqual(result["accounts"][0]["number"], "033325229949033")
        self.assertEqual(result["statement_period_from"], "2025-09-01")
        self.assertEqual(result["statement_period_to"], "2025-09-30")
        self.assertEqual(result["opening_balance"], 0.00)
        self.assertEqual(result["closing_balance"], 44.53)
        self.assertEqual(result["total_deposits"], 114.53)
        self.assertEqual(result["total_withdrawals"], 70.00)
        self.assertEqual(result["transaction_count"], 5)
        self.assertEqual(result["page_count"], 2)
        self.assertEqual(result["validation_errors"], [])
        self.assertEqual(result["summary"]["total_credits"], "107.00")
        self.assertEqual(result["summary"]["interest_earned"], "7.53")

        txns = result["transactions"]
        self.assertEqual(txns[0]["description"], "Add Funds")
        self.assertEqual(txns[1]["type"], "Credit")
        # Wrapped DETAILS continuation joined mid-token (no space), refno dropped.
        self.assertEqual(
            txns[2]["description"],
            "UPI Debit-SUYASH MITTAL-7044793121@ybl-525463946648-Payment from slice",
        )
        self.assertEqual(txns[2]["type"], "Debit")
        self.assertEqual(txns[2]["amount"], 40.00)
        self.assertEqual(txns[2]["balance"], 67.53)
        # Ligature artifact expanded on the cashback row.
        self.assertEqual(txns[3]["description"], "fire cashback")
        self.assertEqual(txns[3]["type"], "Credit")
        # Second page: "Generated on ..." must NOT be stitched onto the last row.
        self.assertEqual(
            txns[4]["description"],
            "UPI Debit-RANJEET SAH-q28182547@ybl-525664498436",
        )
        self.assertEqual(txns[4]["type"], "Debit")
        self.assertEqual(txns[4]["balance"], 44.53)

    @mock.patch("statement_parser.slice_bank_extractor.pdfplumber.open")
    @mock.patch("statement_parser.slice_bank_extractor.PdfReader")
    def test_prints_validation_errors_on_summary_mismatch(self, mock_reader_cls, mock_open):
        mock_reader = mock.Mock()
        mock_reader.is_encrypted = False
        mock_reader_cls.return_value = mock_reader

        pdf = mock.Mock()
        # Printed closing says 45.00 but the chain closes at 44.53.
        page1 = _PAGE1.replace("₹70.00 ₹44.53", "₹70.00 ₹45.00")
        pdf.pages = [_make_page(page1), _make_page(_PAGE2)]
        mock_open.return_value = pdf

        result = extract_transactions("/tmp/slice.pdf")

        self.assertTrue(result["validation_errors"])
        self.assertTrue(
            any("closing balance mismatch" in e for e in result["validation_errors"])
        )
        self.assertTrue(
            any("does not balance" in e for e in result["validation_errors"])
        )

    @mock.patch("statement_parser.slice_bank_extractor.pdfplumber.open")
    @mock.patch("statement_parser.slice_bank_extractor.PdfReader")
    def test_prints_validation_errors_on_broken_balance_chain(self, mock_reader_cls, mock_open):
        mock_reader = mock.Mock()
        mock_reader.is_encrypted = False
        mock_reader_cls.return_value = mock_reader

        pdf = mock.Mock()
        # Break the chain: interest row claims balance 200.00 instead of 107.53.
        page1 = _PAGE1.replace("₹7.53 ₹107.53", "₹7.53 ₹200.00")
        pdf.pages = [_make_page(page1)]
        mock_open.return_value = pdf

        result = extract_transactions("/tmp/slice.pdf")

        self.assertTrue(
            any("balance chain broken" in e for e in result["validation_errors"])
        )

    @mock.patch("statement_parser.slice_bank_extractor.pdfplumber.open")
    @mock.patch("statement_parser.slice_bank_extractor.PdfReader")
    def test_passes_password_to_pdfplumber(self, mock_reader_cls, mock_open):
        mock_reader = mock.Mock()
        mock_reader.is_encrypted = False
        mock_reader_cls.return_value = mock_reader
        pdf = mock.Mock()
        pdf.pages = []
        mock_open.return_value = pdf

        extract_transactions("/tmp/slice.pdf", password="1234")

        mock_open.assert_called_once_with("/tmp/slice.pdf", password="1234")


class SliceToCsvTests(unittest.TestCase):
    def test_to_csv_bytes_writes_header_and_rows(self):
        txns = [
            {"date": "2025-09-01", "description": "Add Funds", "amount": 100.0, "type": "Credit"},
            {"date": "2025-09-03", "description": "UPI Debit-X", "amount": 40.0, "type": "Debit"},
        ]
        csv_bytes = to_csv_bytes(txns)
        text = csv_bytes.decode("utf-8")
        lines = text.strip().split("\r\n") if "\r\n" in text else text.strip().split("\n")
        self.assertEqual(lines[0], "date,description,amount,type")
        self.assertEqual(lines[1], "2025-09-01,Add Funds,100.0,Credit")
        self.assertEqual(lines[2], "2025-09-03,UPI Debit-X,40.0,Debit")


class SliceRegistryTests(unittest.TestCase):
    def test_slice_bank_is_registered(self):
        names = [spec["name"] for spec in extractor_module.list_extractors()]
        self.assertIn("slice_bank", names)

    def test_slice_bank_display_name(self):
        by_name = {spec["name"]: spec for spec in extractor_module.list_extractors()}
        self.assertEqual(by_name["slice_bank"]["display_name"], "Slice Small Finance Bank")

    def test_get_extractor_dispatches(self):
        spec = extractor_module.get_extractor("slice_bank")
        self.assertEqual(spec.name, "slice_bank")


if __name__ == "__main__":
    unittest.main()