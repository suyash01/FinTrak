import sys
import unittest
from pathlib import Path
from unittest import mock

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "src"))

from statement_parser import extractor as extractor_module
from statement_parser.indusind_bank_extractor import (
    PdfPasswordRequired,
    _decrypt_if_needed,
    _extract_metadata,
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


# A crore-scale withdrawal row. The template's column header IS present (the
# old bound derived from it was 405.7 - 368.4 ≈ 43pt, so only the divider-bound
# 405.7 - 322.9 ≈ 82pt admits this token), and the amounts are lakh-grouped
# exactly as an Indian statement prints them. Character width matches the
# transcribed fixtures (3.46pt/char): 14 chars ≈ 48.4pt wide.
_WIDE_AMOUNT_PAGE_TEXT = """\
STATEMENT OF CUSTOMER
43800785
SUYASH MITTAL Date : 01-Jul-2023
Transaction History for Savings Account, Current Account and Overdraft Account.
Account Number Name Holding Status Customer ID
157044793121 SUYASH MITTAL Primary Holder 43800785
Statement Period : 01-Jun-2023 TO 30-Jun-2023
"""

_WIDE_AMOUNT_PAGE_WORDS = _HEADER + [
    _w("01-Jun-2023", 35.5, 75.9, 241.0),
    _w("Brought", 80.9, 108.1, 241.0),
    _w("Forward", 110.0, 137.6, 241.0),
    _w("1,50,00,000.00", 514.3, 562.7, 241.0),
    _w("21-Jun-2023", 36.0, 75.3, 250.3),
    _w("CREDIT", 80.9, 106.9, 250.3),
    _w("1,00,00,000.00", 357.3, 405.7, 250.3),  # the wide withdrawal
    _w("50,00,000.00", 521.2, 562.7, 250.3),
    _w("30-Jun-2023", 35.5, 75.9, 303.2),
    _w("Carried", 80.9, 105.4, 303.2),
    _w("Forward", 107.3, 134.9, 303.2),
    _w("50,00,000.00", 521.2, 562.7, 303.2),
]


class IndusindNarrationAmountTests(unittest.TestCase):
    def test_an_amount_shaped_ref_token_is_not_booked_as_a_withdrawal(self):
        """A reference or narration token that looks like an amount sits left of
        the money columns: classifying by right edge alone booked it as a
        withdrawal, inventing a debit and losing the row's real deposit."""
        words = [
            _w("30-Jun-2023", 31.4, 75.3, 100.0),
            _w("NEFT", 80.9, 100.0, 100.0),
            _w("1,000.00", 250.4, 285.0, 100.0),   # Chq No/Ref No column
            _w("144.00", 460.0, 484.2, 100.0),     # deposit, right-aligned
            _w("5,892.32", 530.0, 562.7, 100.0),   # balance
        ]
        rows, _ = _parse_page(words)

        self.assertEqual(len(rows), 1)
        row = rows[0]
        self.assertEqual(row["amount"], 144.00)
        self.assertEqual(row["type"], "Credit")
        self.assertEqual(row["deposit"], 144.00)
        self.assertIsNone(row["withdrawal"])
        self.assertEqual(row["balance"], 5892.32)

    def test_a_wide_amount_inside_its_column_is_still_read(self):
        """The left bound must come from the column divider (328.9), not from
        the right-aligned "Withdrawal" header word (x0 368.4): the header's own
        left edge sits ~40pt (≈11 characters) inside the column, so using it
        silently dropped every withdrawal wider than ~43pt. The header line is
        part of this fixture precisely because that is the path that broke."""
        words = _HEADER + [
            _w("30-Jun-2023", 31.4, 75.3, 100.0),
            _w("UPI payment", 80.9, 160.0, 100.0),
            _w("1,00,000.00", 325.0, 405.7, 100.0),  # wide withdrawal
            _w("5,892.32", 530.0, 562.7, 100.0),
        ]
        rows, _ = _parse_page(words)

        self.assertEqual(len(rows), 1)
        self.assertEqual(rows[0]["withdrawal"], 100000.00)
        self.assertEqual(rows[0]["type"], "Debit")
        self.assertIsNone(rows[0]["deposit"])


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
    def test_a_crore_withdrawal_survives_into_the_import(self, mock_reader_cls, mock_open):
        """A ~₹1 crore debit on a page carrying the column header used to be
        dropped by the header-derived left bound: the row then became a 0.00
        "Credit" placeholder, the importable filter deleted it, and the balance
        chain reported a break naming "dep wd None" instead of the real cause."""
        mock_reader = mock.Mock()
        mock_reader.is_encrypted = False
        mock_reader_cls.return_value = mock_reader

        pdf = mock.Mock()
        pdf.pages = [_make_page(_WIDE_AMOUNT_PAGE_TEXT, _WIDE_AMOUNT_PAGE_WORDS)]
        mock_open.return_value = pdf

        result = extract_transactions("/tmp/indusind.pdf")

        self.assertEqual(result["validation_errors"], [])
        self.assertEqual(result["page_count"], 1)
        self.assertEqual(result["opening_balance"], 15000000.00)
        self.assertEqual(result["closing_balance"], 5000000.00)
        self.assertEqual(result["total_withdrawals"], 10000000.00)
        self.assertEqual(result["transaction_count"], 1)
        self.assertEqual(len(result["transactions"]), 1)
        txn = result["transactions"][0]
        self.assertEqual(txn["date"], "2023-06-21")
        self.assertEqual(txn["type"], "Debit")
        self.assertEqual(txn["amount"], 10000000.00)
        self.assertEqual(txn["withdrawal"], 10000000.00)
        self.assertEqual(txn["balance"], 5000000.00)

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

# ---------------------------------------------------------------------
# Fixtures — FY2019-20 (Mar-2020) vintage, transcribed word geometry.
# Differences from the Jun-2023 fixtures above:
#   * the 12-char dates are WIDER than their cell and overflow the 77.7
#     divider (x1 = 81.5-83.0 vs 75.3-75.9), so the date cell must be
#     identified by its LEFT edge (x0 = 37.3-38.8);
#   * the FIRST particulars line of a record can be printed 1.4-1.5pt
#     ABOVE its own date line (12-Mar and 20-Mar here);
#   * the relationship-summary table has no Lien Amount column, i.e.
#     "<acct> <type> INR <balance>";
#   * page 3 is an Interest Certificate (date-shaped tokens far right of
#     the page, an interest row with an amount whose x1 lands in the
#     withdrawal column) and must contribute no rows.
# ---------------------------------------------------------------------

_MAR2020_HEADER = [
    _w("Date", 51.7, 68.5, 215.6),
    _w("Particulars", 87.6, 125.2, 215.6),
    _w("Chq", 271.0, 284.7, 215.6),
    _w("No/Ref", 286.6, 312.2, 215.6),
    _w("No", 314.1, 324.2, 215.6),
    _w("Withdrawal", 361.5, 403.2, 215.6),
    _w("Deposit", 454.3, 481.5, 215.6),
    _w("Balance", 532.2, 559.8, 215.6),
]

_MAR2020_BF = [
    _w("01-Mar-2020", 38.6, 81.6, 228.4),
    _w("Brought", 87.6, 114.4, 228.4),
    _w("Forward", 116.2, 144.1, 228.4),
    _w("0.00", 545.5, 559.8, 228.4),
]

# "Transfer/Customer Induced IMPS / P2A / <ref>" — the ref splits mid-token
# across the wrap ("...00700169688" + "8/...").
_MAR2020_CREDIT_0903 = [
    _w("09-Mar-2020", 38.8, 81.5, 244.1),
    _w("Transfer/Customer", 87.6, 149.4, 244.1),
    _w("Induced", 151.3, 177.4, 244.1),
    _w("IMPS", 179.2, 195.9, 244.1),
    _w("/", 197.7, 200.8, 244.1),
    _w("P2A", 202.6, 215.4, 244.1),
    _w("/", 217.2, 220.3, 244.1),
    _w("00700169688", 222.1, 266.8, 244.1),
    _w("1,000.00", 453.2, 481.5, 244.1),
    _w("1,000.00", 531.5, 559.8, 244.1),
]
_MAR2020_CREDIT_0903_WRAP1 = [
    _w("8", 87.6, 91.7, 253.9),
    _w("/", 93.5, 96.6, 253.9),
    _w("9028", 98.4, 114.6, 253.9),
    _w("/", 116.4, 119.5, 253.9),
    _w("27260110037263", 121.3, 178.1, 253.9),
    _w("157044793121", 179.9, 228.6, 253.9),
    _w("/INWD48/", 230.4, 263.9, 253.9),
]
_MAR2020_CREDIT_0903_WRAP2 = [
    _w("00/MOB/SUYASH", 87.6, 144.4, 263.7),
    _w("MITTAL", 146.2, 170.9, 263.7),
]

# 12-Mar: the particulars line (top 278.7) is 1.5pt ABOVE its date line
# (top 280.2). With a 1.0pt line tolerance this record split in two and the
# particulars were stitched onto the 09-Mar record.
_MAR2020_CREDIT_1203 = [
    _w("Transfer/Customer", 87.6, 149.4, 278.7),
    _w("Induced", 151.3, 177.4, 278.7),
    _w("IMPS", 179.2, 195.9, 278.7),
    _w("/", 197.7, 200.8, 278.7),
    _w("P2A", 202.6, 215.4, 278.7),
    _w("/", 217.2, 220.3, 278.7),
    _w("00721446314", 222.1, 266.8, 278.7),
    _w("12-Mar-2020", 38.8, 81.5, 280.2),
    _w("6,000.00", 453.2, 481.5, 280.2),
    _w("7,000.00", 531.5, 559.8, 280.2),
]
_MAR2020_CREDIT_1203_WRAP1 = [
    _w("1", 87.6, 91.7, 288.5),
    _w("/", 93.5, 96.6, 288.5),
    _w("9028", 98.4, 114.6, 288.5),
    _w("/", 116.4, 119.5, 288.5),
    _w("27260110037263", 121.3, 178.1, 288.5),
    _w("157044793121", 179.9, 228.6, 288.5),
    _w("/INWD48/", 230.4, 263.9, 288.5),
]
_MAR2020_CREDIT_1203_WRAP2 = [
    _w("00/MOB/SUYASH", 87.6, 144.4, 298.3),
    _w("MITTAL", 146.2, 170.9, 298.3),
]

_MAR2020_DEBIT_1303 = [
    _w("13-Mar-2020", 38.8, 81.5, 313.4),
    _w("Transfer/Bank", 87.6, 133.8, 313.4),
    _w("Induced", 135.6, 161.8, 313.4),
    _w("UPS", 163.6, 176.6, 313.4),
    _w("3IN1", 178.4, 193.7, 313.4),
    _w("SETTLEMENT", 195.5, 237.9, 313.4),
    _w("UPS", 239.7, 252.7, 313.4),
    _w("3IN", 254.5, 265.7, 313.4),
    _w("2,069.01", 374.9, 403.2, 313.4),
    _w("4,930.99", 531.5, 559.8, 313.4),
]
_MAR2020_DEBIT_1303_WRAP = [
    _w("1", 87.6, 91.7, 323.1),
    _w("SETTLEMENT", 93.5, 135.9, 323.1),
]

# 20-Mar: same 1.4pt offset as 12-Mar.
_MAR2020_DEBIT_2003 = [
    _w("Transfer/Bank", 87.6, 133.8, 338.2),
    _w("Induced", 135.6, 161.8, 338.2),
    _w("UPS", 163.6, 176.6, 338.2),
    _w("3IN1", 178.4, 193.7, 338.2),
    _w("SETTLEMENT", 195.5, 237.9, 338.2),
    _w("UPS", 239.7, 252.7, 338.2),
    _w("3IN", 254.5, 265.7, 338.2),
    _w("20-Mar-2020", 38.8, 81.5, 339.6),
    _w("974.13", 380.9, 403.2, 339.6),
    _w("3,956.86", 531.5, 559.8, 339.6),
]
_MAR2020_DEBIT_2003_WRAP = [
    _w("1", 87.6, 91.7, 348.0),
    _w("SETTLEMENT", 93.5, 135.9, 348.0),
]

_MAR2020_INTEREST_3103 = [
    _w("31-Mar-2020", 38.8, 81.5, 363.1),
    _w("Transfer/Interest", 87.6, 143.3, 363.1),
    _w("Paid", 145.1, 159.1, 363.1),
    _w("Consolidated", 160.9, 203.5, 363.1),
    _w("Interest", 205.3, 230.8, 363.1),
    _w("Payment", 232.6, 261.4, 363.1),
    _w("I", 263.2, 265.2, 363.1),
    _w("10.00", 463.3, 481.5, 363.1),
    _w("3,966.86", 531.5, 559.8, 363.1),
]
_MAR2020_INTEREST_3103_WRAP = [
    _w("nterest", 87.6, 111.1, 372.8),
    _w("run", 112.9, 124.1, 372.8),
]

# Last row: Arial-Bold 8.5pt, so the date token is the WIDEST one (x1=83.0).
_MAR2020_CF = [
    _w("31-Mar-2020", 37.3, 83.0, 387.8),
    _w("Carried", 87.6, 113.3, 387.8),
    _w("Forward", 115.2, 144.8, 387.8),
    _w("3,966.86", 529.5, 559.8, 387.8),
]

MAR2020_PAGE2_WORDS = (
    _MAR2020_HEADER + _MAR2020_BF
    + _MAR2020_CREDIT_0903 + _MAR2020_CREDIT_0903_WRAP1 + _MAR2020_CREDIT_0903_WRAP2
    + _MAR2020_CREDIT_1203 + _MAR2020_CREDIT_1203_WRAP1 + _MAR2020_CREDIT_1203_WRAP2
    + _MAR2020_DEBIT_1303 + _MAR2020_DEBIT_1303_WRAP
    + _MAR2020_DEBIT_2003 + _MAR2020_DEBIT_2003_WRAP
    + _MAR2020_INTEREST_3103 + _MAR2020_INTEREST_3103_WRAP
    + _MAR2020_CF
)

# Page 3 — Interest Certificate: date-shaped tokens far right of the page
# and an interest row whose "10.00" has x1 = 387.6 (inside the withdrawal
# column). Neither may start or feed a transaction row.
MAR2020_PAGE3_WORDS = [
    _w("IndusInd", 46.0, 85.8, 45.4),
    _w("Bank", 88.3, 111.1, 45.4),
    _w("Date", 42.5, 66.2, 108.9),
    _w(":", 68.1, 70.3, 108.9),
    _w("31-Mar-2020", 388.9, 442.2, 108.9),
    _w("01-Apr-2019", 488.2, 539.3, 263.4),
    _w("157044793121", 58.0, 106.7, 340.0),
    _w("SAVING-DOMESTIC", 127.9, 190.2, 340.0),
    _w("INR", 283.9, 295.4, 340.0),
    _w("10.00", 369.4, 387.6, 340.0),
    _w("0.00", 453.2, 467.4, 340.0),
    _w("10.00", 528.8, 547.1, 340.0),
]

MAR2020_PAGE1_TEXT = """\
STATEMENT OF ACCOUNT
157044793121
SUYASH MITTAL Date : 31-Mar-2020
WARD NO 03 ULAO ULAO Period : 01-Mar-2020 To 31-Mar-2020
Relationship Summary for Customer ID - 43800785
Customer Details
Name Holding Status Customer ID
SUYASH MITTAL Primary Holder 43800785
Current / Savings Account - Summary
Account No Account Type Currency Balance
157044793121 UPSTOX 3 IN 1 INR 3,966.86
Total 3,966.86
This is a computer generated statement and so valid without signature. Page : 1 of 3
"""

MAR2020_PAGE2_TEXT = """\
STATEMENT OF ACCOUNT
157044793121
Transaction History for Savings Account, Current Account and Over Draft Account.
Account Number Name Holding Status Customer ID
157044793121 SUYASH MITTAL Primary Holder 43800785
Statement Period : 01-Mar-2020 To 31-Mar-2020 Email Id For E Statement : suyash8514@gmail.com
Date Particulars Chq No/Ref No Withdrawal Deposit Balance
01-Mar-2020 Brought Forward 0.00
09-Mar-2020 Transfer/Customer Induced IMPS / P2A / 00700169688 1,000.00 1,000.00
8 / 9028 / 27260110037263 157044793121 /INWD48/
00/MOB/SUYASH MITTAL
12-Mar-2020 Transfer/Customer Induced IMPS / P2A / 00721446314 6,000.00 7,000.00
1 / 9028 / 27260110037263 157044793121 /INWD48/
00/MOB/SUYASH MITTAL
13-Mar-2020 Transfer/Bank Induced UPS 3IN1 SETTLEMENT UPS 3IN 2,069.01 4,930.99
1 SETTLEMENT
20-Mar-2020 Transfer/Bank Induced UPS 3IN1 SETTLEMENT UPS 3IN 974.13 3,956.86
1 SETTLEMENT
31-Mar-2020 Transfer/Interest Paid Consolidated Interest Payment I 10.00 3,966.86
nterest run
31-Mar-2020 Carried Forward 3,966.86
This is a computer generated statement and does not require signature.
"""

MAR2020_PAGE3_TEXT = """\
IndusInd Bank
Date : 31-Mar-2020
Customer ID : 43800785
INTEREST CERTIFICATE
Dear Customer,
Please find below the statement of Interest Paid/Accrued on your account(s) with us for the period 01-Apr-2019 to
31-Mar-2020.
Interest Accrued But
Account / Fixed
Account Type Currency Interest Paid Unpaid Total Interest
Deposit Number
(31-Mar-2020)
157044793121 SAVING-DOMESTIC INR 10.00 0.00 10.00
Grand Total : INR 10.00 0.00 10.00
Generated on : 01-04-2020 10:10:12
Page : 3 of 3
"""


class IndusindMar2020VintageTests(unittest.TestCase):
    """FY2019-20 vintage: wide date tokens (x1 > 78), particulars lines
    printed above their date line, a lien-less relationship summary, and an
    Interest Certificate page."""

    def test_wide_date_tokens_still_start_rows(self):
        rows, _ = _parse_page(MAR2020_PAGE2_WORDS)
        self.assertEqual(
            [r["date"] for r in rows],
            ["2020-03-01", "2020-03-09", "2020-03-12", "2020-03-13",
             "2020-03-20", "2020-03-31", "2020-03-31"],
        )

    def test_date_cell_is_identified_by_left_edge(self):
        # Every date token on this vintage overflows the 77.7 divider; an
        # x1-based date-cell test drops the whole page (silently, with an
        # empty validation_errors list) — that is the regression under test.
        dates = [w for w in MAR2020_PAGE2_WORDS if w["text"].endswith("-2020")]
        self.assertEqual(len(dates), 7)
        self.assertTrue(all(w["x1"] > 78.0 for w in dates))
        self.assertTrue(all(w["x0"] <= 60.0 for w in dates))

    def test_offset_particulars_line_belongs_to_its_own_row(self):
        rows, _ = _parse_page(MAR2020_PAGE2_WORDS)
        self.assertEqual(
            rows[2]["description"],
            "Transfer/Customer Induced IMPS / P2A / 007214463141 / 9028 / "
            "27260110037263 157044793121 /INWD48/00/MOB/SUYASH MITTAL",
        )
        self.assertEqual(rows[2]["date"], "2020-03-12")
        self.assertEqual(rows[2]["deposit"], 6000.00)
        self.assertEqual(rows[2]["balance"], 7000.00)
        # ... and it must NOT have been stitched onto the 09-Mar record.
        self.assertNotIn("00721446314", rows[1]["description"])
        self.assertTrue(rows[1]["description"].endswith("00/MOB/SUYASH MITTAL"))
        self.assertEqual(rows[4]["date"], "2020-03-20")
        self.assertEqual(rows[4]["withdrawal"], 974.13)

    def test_wide_bold_carried_forward_row(self):
        rows, _ = _parse_page(MAR2020_PAGE2_WORDS)
        cf = rows[-1]
        self.assertEqual(cf["date"], "2020-03-31")
        self.assertEqual(cf["description"], "Carried Forward")
        self.assertEqual(cf["amount"], 0.0)
        self.assertEqual(cf["balance"], 3966.86)

    def test_interest_certificate_page_yields_no_rows(self):
        rows, _ = _parse_page(MAR2020_PAGE3_WORDS)
        self.assertEqual(rows, [])

    def test_lien_less_relationship_summary_row(self):
        meta = _extract_metadata(MAR2020_PAGE1_TEXT)
        self.assertEqual(meta["account_number"], "157044793121")
        self.assertEqual(meta["account_type"], "UPSTOX 3 IN 1")
        self.assertEqual(meta["summary_balance"], 3966.86)
        self.assertEqual(meta["account_holder"], "SUYASH MITTAL")
        self.assertEqual(meta["customer_id"], "43800785")

    def test_interest_certificate_text_is_not_a_summary_row(self):
        meta = _extract_metadata(MAR2020_PAGE3_TEXT)
        self.assertIsNone(meta["account_type"])
        self.assertIsNone(meta["summary_balance"])

    @mock.patch("statement_parser.indusind_bank_extractor.pdfplumber.open")
    @mock.patch("statement_parser.indusind_bank_extractor.PdfReader")
    def test_extracts_mar2020_statement(self, mock_reader_cls, mock_open):
        mock_reader = mock.Mock()
        mock_reader.is_encrypted = False
        mock_reader_cls.return_value = mock_reader

        pdf = mock.Mock()
        pdf.pages = [
            _make_page(MAR2020_PAGE1_TEXT, []),
            _make_page(MAR2020_PAGE2_TEXT, MAR2020_PAGE2_WORDS),
            _make_page(MAR2020_PAGE3_TEXT, MAR2020_PAGE3_WORDS),
        ]
        mock_open.return_value = pdf

        result = extract_transactions("/tmp/indusind-mar2020.pdf")

        self.assertEqual(result["account_holder"], "SUYASH MITTAL")
        self.assertEqual(result["customer_id"], "43800785")
        self.assertEqual(result["statement_period_from"], "2020-03-01")
        self.assertEqual(result["statement_period_to"], "2020-03-31")
        self.assertEqual(result["accounts"][0]["number"], "157044793121")
        self.assertEqual(result["accounts"][0]["type"], "UPSTOX 3 IN 1")
        self.assertEqual(result["accounts"][0]["balance"], 3966.86)
        self.assertEqual(result["opening_balance"], 0.0)
        self.assertEqual(result["closing_balance"], 3966.86)
        self.assertEqual(result["total_deposits"], 7010.00)
        self.assertEqual(result["total_withdrawals"], 3043.14)
        self.assertEqual(result["transaction_count"], 5)
        self.assertEqual(result["page_count"], 3)
        self.assertEqual(result["validation_errors"], [])

        txns = result["transactions"]
        self.assertEqual([t["date"] for t in txns],
                         ["2020-03-09", "2020-03-12", "2020-03-13",
                          "2020-03-20", "2020-03-31"])
        self.assertEqual([t["type"] for t in txns],
                         ["Credit", "Credit", "Debit", "Debit", "Credit"])
        self.assertEqual([t["amount"] for t in txns],
                         [1000.00, 6000.00, 2069.01, 974.13, 10.00])
        # UPS 3IN1 SETTLEMENT sits in the WITHDRAWAL column (x1 = 403.2).
        self.assertEqual(txns[2]["withdrawal"], 2069.01)
        self.assertIsNone(txns[2]["deposit"])
        self.assertEqual(txns[4]["description"],
                         "Transfer/Interest Paid Consolidated Interest Payment Interest run")


if __name__ == "__main__":
    unittest.main()