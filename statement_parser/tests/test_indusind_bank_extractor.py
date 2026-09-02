import sys
import unittest
from pathlib import Path
from unittest import mock

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "src"))

from statement_parser import extractor as extractor_module
from statement_parser.indusind_bank_extractor import (
    PdfPasswordRequired,
    _decrypt_if_needed,
    _parse_amount,
    _parse_date,
    _parse_page,
    extract_transactions,
    to_csv_bytes,
)

# ---------------------------------------------------------------------
# Fixtures — transcribed word geometry from the real Jun-2023 statement
# (account 157044793121). Column dividers: 31.4 | 77.7 | 250.4 | 328.9 |
# 407.4 | 485.9 | 564.4; amounts right-aligned to 405.7 / 484.2 / 562.7.
# ---------------------------------------------------------------------


def _w(text, x0, x1, top):
    return {"text": text, "x0": x0, "x1": x1, "top": top, "bottom": top + 7.6}


# Account-details block (must never be parsed or stitched).
_ACCOUNT_BLOCK = [
    _w("Account", 51.0, 80.9, 115.1),
    _w("Number", 80.9, 110.1, 115.1),
    _w("Name", 128.0, 147.0, 115.1),
    _w("157044793121", 55.8, 118.5, 126.0),
    _w("SUYASH", 128.0, 160.0, 126.0),
    _w("MITTAL", 158.7, 190.7, 126.0),
    _w("Primary", 442.0, 474.0, 126.0),
    _w("Holder", 468.0, 500.0, 126.0),
    _w("43800785", 518.3, 550.3, 126.0),
    # Date-shaped token OUTSIDE the date column must not start a row.
    _w("Statement", 34.5, 70.0, 177.9),
    _w("Period", 68.4, 92.0, 177.9),
    _w("01-Jun-2023", 94.5, 126.5, 177.9),
    _w("TO", 135.7, 147.0, 177.9),
    _w("30-Jun-2023", 147.4, 180.4, 177.9),
    _w("Email", 332.0, 352.0, 177.9),
]

_HEADER = [
    _w("Date", 48.1, 63.3, 229.2),
    _w("Particulars", 80.9, 117.0, 229.2),
    _w("Chq", 266.0, 279.6, 229.2),
    _w("No/Ref", 281.6, 304.1, 229.2),
    _w("No", 306.1, 315.4, 229.2),
    _w("Withdrawal", 368.4, 405.7, 229.2),
    _w("Deposit", 458.5, 484.2, 229.2),
    _w("Balance", 535.9, 562.7, 229.2),
]

_BF_ROW = [
    _w("01-Jun-2023", 35.5, 75.9, 241.0),
    _w("Brought", 80.9, 108.1, 241.0),
    _w("Forward", 110.0, 137.6, 241.0),
    _w("15,843.32", 531.6, 562.7, 241.0),
]

# CREDIT OF RD AC ... is a WITHDRAWAL from the savings account (balance
# drops 15,843.32 -> 5,843.32), and the token sits in the Withdrawal column.
_RD_ROW = [
    _w("21-Jun-2023", 36.0, 75.3, 250.3),
    _w("CREDIT", 80.9, 106.9, 250.3),
    _w("OF", 108.9, 118.6, 250.3),
    _w("RD", 120.5, 130.6, 250.3),
    _w("AC", 132.6, 142.3, 250.3),
    _w("300944681618", 144.3, 191.0, 250.3),
    _w("10,000.00", 374.6, 405.7, 250.3),
    _w("5,843.32", 535.5, 562.7, 250.3),
]

_UPI_LINE1 = [
    _w("23-Jun-2023", 36.0, 75.3, 262.1),
    _w("UPI/317478120036/DR/G", 80.9, 160.6, 262.1),
    _w("R", 162.6, 167.6, 262.1),
    _w("/YESB/Q893267845@yb", 169.6, 247.1, 262.1),
    _w("95.00", 388.2, 405.7, 262.1),
    _w("5,748.32", 535.5, 562.7, 262.1),
]

_UPI_LINE2 = [
    _w("l/Payme002261100000025/YESB0YBLUPI/G", 80.9, 221.3, 270.1),
    _w("R", 223.3, 228.3, 270.1),
    _w("STO", 230.3, 244.7, 270.1),
]

_UPI_LINE3 = [
    _w("RESOthPSP/Payment", 80.9, 150.1, 278.1),
    _w("from", 152.1, 166.1, 278.1),
    _w("PhonePe", 168.0, 196.8, 278.1),
]

_INTEREST_ROW = [
    _w("30-Jun-2023", 36.0, 75.3, 288.9),
    _w("Consolidated", 80.9, 121.7, 288.9),
    _w("Interest", 123.7, 147.0, 288.9),
    _w("PaymentInterest", 148.9, 199.9, 288.9),
    _w("run", 201.9, 212.0, 288.9),
    _w("144.00", 462.8, 484.2, 288.9),
    _w("5,892.32", 535.5, 562.7, 288.9),
]

_CF_ROW = [
    _w("30-Jun-2023", 35.5, 75.9, 303.2),
    _w("Carried", 80.9, 105.4, 303.2),
    _w("Forward", 107.3, 134.9, 303.2),
    _w("5,892.32", 535.5, 562.7, 303.2),
]

# Post-table footer line (must end the table region, never stitch).
_FOOTER = [
    _w("This", 34.5, 47.8, 321.2),
    _w("is", 50.6, 55.7, 321.2),
    _w("a", 58.6, 62.5, 321.2),
    _w("computer", 65.3, 94.5, 321.2),
    _w("generated", 97.4, 128.9, 321.2),
    _w("statement", 131.8, 162.5, 321.2),
    _w("require", 210.6, 232.4, 321.2),
    _w("signature.", 235.2, 266.0, 321.2),
]

PAGE2_WORDS = (
    _ACCOUNT_BLOCK + _HEADER + _BF_ROW + _RD_ROW
    + _UPI_LINE1 + _UPI_LINE2 + _UPI_LINE3
    + _INTEREST_ROW + _CF_ROW + _FOOTER
)

PAGE1_TEXT = """\
STATEMENT OF CUSTOMER
43800785
SUYASH MITTAL Date : 01-Jul-2023
WARD NO 03 ULAO ULAO Period : 01-Jun-2023 To 30-Jun-2023
l Important Update : Savings Account Interest Rates Revision w.e.f 18th October 2022.
Relationship Summary for Customer ID - 43800785
Current / Savings Account - Summary
Account No Account Type Currency Lien Amount Balance
157044793121 UPSTOX 3 IN 1 INR 0.00 5,892.32
Total 5,892.32
Registered office: INDUSIND BANK LTD, 2401, General Thimmayya Road (Cantonment), Maharashtra Pune-411 001 Page 1 of 2
"""

PAGE2_TEXT = """\
STATEMENT OF CUSTOMER
43800785
Transaction History for Savings Account, Current Account and Overdraft Account.
Account Number Name Holding Status Customer ID
157044793121 SUYASH MITTAL Primary Holder 43800785
Product Description: UPSTOX 3 IN 1 Branch Address : SHOW ROOMNO B2/2
Statement Period : 01-Jun-2023 TO 30-Jun-2023 Email Id For E Statement : suyash8514@gmail.com
Nomination Registered : YES
Date Particulars Chq No/Ref No Withdrawal Deposit Balance
01-Jun-2023 Brought Forward 15,843.32
21-Jun-2023 CREDIT OF RD AC 300944681618 10,000.00 5,843.32
23-Jun-2023 UPI/317478120036/DR/G R /YESB/Q893267845@yb 95.00 5,748.32
l/Payme002261100000025/YESB0YBLUPI/G R STO
RESOthPSP/Payment from PhonePe
30-Jun-2023 Consolidated Interest PaymentInterest run 144.00 5,892.32
30-Jun-2023 Carried Forward 5,892.32
This is a computer generated statement and does not require signature.
"""


def _make_page(text, words):
    page = mock.Mock()
    page.extract_text.return_value = text
    page.extract_words.return_value = list(words)
    return page


class IndusindParsePageTests(unittest.TestCase):
    def test_parses_all_rows_including_bf_cf(self):
        rows, _ = _parse_page(PAGE2_WORDS)
        self.assertEqual(len(rows), 5)

        bf = rows[0]
        self.assertEqual(bf["date"], "2023-06-01")
        self.assertEqual(bf["description"], "Brought Forward")
        self.assertEqual(bf["amount"], 0.0)
        self.assertIsNone(bf["deposit"])
        self.assertIsNone(bf["withdrawal"])
        self.assertEqual(bf["balance"], 15843.32)

    def test_ruler_column_classification(self):
        rows, _ = _parse_page(PAGE2_WORDS)
        # "CREDIT OF RD AC 300944681618" sits in the WITHDRAWAL column
        # (balance drops): column is decided by x1, not by the text.
        rd = rows[1]
        self.assertEqual(rd["description"], "CREDIT OF RD AC 300944681618")
        self.assertEqual(rd["type"], "Debit")
        self.assertEqual(rd["amount"], 10000.00)
        self.assertEqual(rd["withdrawal"], 10000.00)
        self.assertIsNone(rd["deposit"])
        self.assertEqual(rd["balance"], 5843.32)

        interest = rows[3]
        self.assertEqual(interest["type"], "Credit")
        self.assertEqual(interest["deposit"], 144.00)
        self.assertIsNone(interest["withdrawal"])
        self.assertEqual(interest["balance"], 5892.32)

    def test_mid_token_wrap_stitches_without_space(self):
        rows, _ = _parse_page(PAGE2_WORDS)
        upi = rows[2]
        self.assertEqual(
            upi["description"],
            "UPI/317478120036/DR/G R /YESB/Q893267845@ybl/"
            "Payme002261100000025/YESB0YBLUPI/G R STO "
            "RESOthPSP/Payment from PhonePe",
        )
        self.assertEqual(upi["type"], "Debit")
        self.assertEqual(upi["withdrawal"], 95.00)
        self.assertEqual(upi["balance"], 5748.32)

    def test_preserves_generator_glued_token(self):
        rows, _ = _parse_page(PAGE2_WORDS)
        self.assertEqual(rows[3]["description"], "Consolidated Interest PaymentInterest run")

    def test_carried_forward_row_and_footer_not_stitched(self):
        rows, _ = _parse_page(PAGE2_WORDS)
        cf = rows[4]
        self.assertEqual(cf["description"], "Carried Forward")
        self.assertEqual(cf["amount"], 0.0)
        self.assertEqual(cf["balance"], 5892.32)

    def test_account_block_ignored(self):
        # The date-shaped "01-Jun-2023" in the period line (x0=94.5, outside
        # the date column) must not create a phantom row.
        rows, _ = _parse_page(PAGE2_WORDS)
        self.assertEqual([r["date"] for r in rows],
                         ["2023-06-01", "2023-06-21", "2023-06-23",
                          "2023-06-30", "2023-06-30"])


class IndusindDateAmountTests(unittest.TestCase):
    def test_parse_date_normalizes_to_iso(self):
        self.assertEqual(_parse_date("01-Jun-2023"), "2023-06-01")
        self.assertEqual(_parse_date("31-Dec-2022"), "2022-12-31")
        self.assertEqual(_parse_date("garbage"), "garbage")

    def test_parse_amount(self):
        self.assertEqual(_parse_amount("15,843.32"), 15843.32)
        self.assertEqual(_parse_amount("95.00"), 95.00)
        self.assertIsNone(_parse_amount("UPSTOX"))


class IndusindDecryptTests(unittest.TestCase):
    @mock.patch("statement_parser.indusind_bank_extractor.PdfReader")
    def test_raises_when_password_missing(self, mock_reader_cls):
        mock_reader = mock.Mock()
        mock_reader.is_encrypted = True
        mock_reader_cls.return_value = mock_reader
        with self.assertRaises(PdfPasswordRequired):
            _decrypt_if_needed("/tmp/x.pdf", None)

    @mock.patch("statement_parser.indusind_bank_extractor.PdfReader")
    def test_raises_when_password_incorrect(self, mock_reader_cls):
        mock_reader = mock.Mock()
        mock_reader.is_encrypted = True
        mock_reader.decrypt.return_value = 0
        mock_reader_cls.return_value = mock_reader
        with self.assertRaises(PdfPasswordRequired):
            _decrypt_if_needed("/tmp/x.pdf", "wrong")

    @mock.patch("statement_parser.indusind_bank_extractor.PdfReader")
    def test_accepts_correct_password(self, mock_reader_cls):
        mock_reader = mock.Mock()
        mock_reader.is_encrypted = True
        mock_reader.decrypt.return_value = 1
        mock_reader_cls.return_value = mock_reader
        _decrypt_if_needed("/tmp/x.pdf", "right")  # should not raise

    @mock.patch("statement_parser.indusind_bank_extractor.PdfReader")
    def test_noop_for_unencrypted_pdf(self, mock_reader_cls):
        mock_reader = mock.Mock()
        mock_reader.is_encrypted = False
        mock_reader_cls.return_value = mock_reader
        _decrypt_if_needed("/tmp/x.pdf", None)  # should not raise


class IndusindExtractTransactionsTests(unittest.TestCase):
    @mock.patch("statement_parser.indusind_bank_extractor.pdfplumber.open")
    @mock.patch("statement_parser.indusind_bank_extractor.PdfReader")
    def test_extracts_full_statement(self, mock_reader_cls, mock_open):
        mock_reader = mock.Mock()
        mock_reader.is_encrypted = False
        mock_reader_cls.return_value = mock_reader

        pdf = mock.Mock()
        pdf.pages = [
            _make_page(PAGE1_TEXT, []),
            _make_page(PAGE2_TEXT, PAGE2_WORDS),
        ]
        mock_open.return_value = pdf

        result = extract_transactions("/tmp/indusind.pdf")

        self.assertEqual(result["bank"], "IndusInd Bank")
        self.assertEqual(result["statement_type"], "savings_account")
        self.assertEqual(result["account_holder"], "SUYASH MITTAL")
        self.assertEqual(result["customer_id"], "43800785")
        self.assertEqual(result["statement_period_from"], "2023-06-01")
        self.assertEqual(result["statement_period_to"], "2023-06-30")
        self.assertEqual(result["accounts"][0]["number"], "157044793121")
        self.assertEqual(result["accounts"][0]["type"], "UPSTOX 3 IN 1")
        self.assertEqual(result["opening_balance"], 15843.32)
        self.assertEqual(result["closing_balance"], 5892.32)
        self.assertEqual(result["total_deposits"], 144.00)
        self.assertEqual(result["total_withdrawals"], 10095.00)
        self.assertEqual(result["transaction_count"], 3)
        self.assertEqual(result["page_count"], 2)
        self.assertEqual(result["validation_errors"], [])
        self.assertEqual(result["summary"]["opening_balance"], "15843.32")
        self.assertEqual(result["summary"]["closing_balance"], "5892.32")

        txns = result["transactions"]
        # Brought/Carried Forward (zero-amount rows) are NOT importable.
        self.assertEqual([t["description"] for t in txns],
                         ["CREDIT OF RD AC 300944681618",
                          "UPI/317478120036/DR/G R /YESB/Q893267845@ybl/"
                          "Payme002261100000025/YESB0YBLUPI/G R STO "
                          "RESOthPSP/Payment from PhonePe",
                          "Consolidated Interest PaymentInterest run"])
        self.assertEqual(txns[0]["type"], "Debit")
        self.assertEqual(txns[0]["amount"], 10000.00)
        self.assertEqual(txns[1]["type"], "Debit")
        self.assertEqual(txns[1]["amount"], 95.00)
        self.assertEqual(txns[1]["balance"], 5748.32)
        self.assertEqual(txns[2]["type"], "Credit")
        self.assertEqual(txns[2]["amount"], 144.00)
        self.assertEqual(txns[2]["balance"], 5892.32)

    @mock.patch("statement_parser.indusind_bank_extractor.pdfplumber.open")
    @mock.patch("statement_parser.indusind_bank_extractor.PdfReader")
    def test_validation_error_on_broken_balance_chain(self, mock_reader_cls, mock_open):
        mock_reader = mock.Mock()
        mock_reader.is_encrypted = False
        mock_reader_cls.return_value = mock_reader

        pdf = mock.Mock()
        pdf.pages = [
            _make_page(PAGE1_TEXT, []),
            _make_page(PAGE2_TEXT, [w for w in PAGE2_WORDS if w["text"] != "5,748.32"]
                      + [_w("5,749.00", 535.5, 562.7, 262.1)]),
        ]
        mock_open.return_value = pdf

        result = extract_transactions("/tmp/indusind.pdf")
        self.assertTrue(
            any("balance chain broken" in e for e in result["validation_errors"])
        )

    @mock.patch("statement_parser.indusind_bank_extractor.pdfplumber.open")
    @mock.patch("statement_parser.indusind_bank_extractor.PdfReader")
    def test_validation_error_on_closing_mismatch(self, mock_reader_cls, mock_open):
        mock_reader = mock.Mock()
        mock_reader.is_encrypted = False
        mock_reader_cls.return_value = mock_reader

        pdf = mock.Mock()
        page1 = PAGE1_TEXT.replace("5,892.32", "5,900.00")
        pdf.pages = [_make_page(page1, []), _make_page(PAGE2_TEXT, PAGE2_WORDS)]
        mock_open.return_value = pdf

        result = extract_transactions("/tmp/indusind.pdf")
        self.assertTrue(
            any("closing balance mismatch" in e for e in result["validation_errors"])
        )

    @mock.patch("statement_parser.indusind_bank_extractor.pdfplumber.open")
    @mock.patch("statement_parser.indusind_bank_extractor.PdfReader")
    def test_passes_password_to_pdfplumber(self, mock_reader_cls, mock_open):
        mock_reader = mock.Mock()
        mock_reader.is_encrypted = False
        mock_reader_cls.return_value = mock_reader
        pdf = mock.Mock()
        pdf.pages = []
        mock_open.return_value = pdf

        extract_transactions("/tmp/indusind.pdf", password="1234")

        mock_open.assert_called_once_with("/tmp/indusind.pdf", password="1234")


class IndusindToCsvTests(unittest.TestCase):
    def test_to_csv_bytes_writes_header_and_rows(self):
        txns = [
            {"date": "2023-06-21", "description": "CREDIT OF RD AC", "amount": 10000.0, "type": "Debit"},
            {"date": "2023-06-30", "description": "Interest", "amount": 144.0, "type": "Credit"},
        ]
        text = to_csv_bytes(txns).decode("utf-8")
        lines = text.strip().split("\r\n") if "\r\n" in text else text.strip().split("\n")
        self.assertEqual(lines[0], "date,description,amount,type")
        self.assertEqual(lines[1], "2023-06-21,CREDIT OF RD AC,10000.0,Debit")
        self.assertEqual(lines[2], "2023-06-30,Interest,144.0,Credit")


class IndusindRegistryTests(unittest.TestCase):
    def test_indusind_bank_is_registered(self):
        names = [spec["name"] for spec in extractor_module.list_extractors()]
        self.assertIn("indusind_bank", names)

    def test_indusind_bank_display_name(self):
        by_name = {spec["name"]: spec for spec in extractor_module.list_extractors()}
        self.assertEqual(by_name["indusind_bank"]["display_name"], "IndusInd Bank Statement")

    def test_get_extractor_dispatches(self):
        spec = extractor_module.get_extractor("indusind_bank")
        self.assertEqual(spec.name, "indusind_bank")


if __name__ == "__main__":
    unittest.main()