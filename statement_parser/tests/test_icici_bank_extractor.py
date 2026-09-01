import sys
import unittest
from pathlib import Path
from unittest import mock

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "src"))

from statement_parser.icici_bank_extractor import (
    PdfPasswordRequired,
    _decrypt_if_needed,
    _extract_metadata,
    _join_particulars_lines,
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
    def __init__(self, words, text="", lines=None, height=842.0):
        self._words = [dict(w) for w in words]
        self._text = text
        self.lines = list(lines) if lines else []
        self.height = height

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


class IciciBankDecryptTests(unittest.TestCase):
    @mock.patch("statement_parser.icici_bank_extractor.PdfReader")
    def test_raises_when_password_missing(self, mock_reader_cls):
        mock_reader = mock.Mock()
        mock_reader.is_encrypted = True
        mock_reader_cls.return_value = mock_reader
        with self.assertRaises(PdfPasswordRequired):
            _decrypt_if_needed("/tmp/x.pdf", None)

    @mock.patch("statement_parser.icici_bank_extractor.PdfReader")
    def test_raises_when_password_incorrect(self, mock_reader_cls):
        mock_reader = mock.Mock()
        mock_reader.is_encrypted = True
        mock_reader.decrypt.return_value = 0
        mock_reader_cls.return_value = mock_reader
        with self.assertRaises(PdfPasswordRequired):
            _decrypt_if_needed("/tmp/x.pdf", "wrong")

    @mock.patch("statement_parser.icici_bank_extractor.PdfReader")
    def test_accepts_correct_password(self, mock_reader_cls):
        mock_reader = mock.Mock()
        mock_reader.is_encrypted = True
        mock_reader.decrypt.return_value = 1
        mock_reader_cls.return_value = mock_reader
        _decrypt_if_needed("/tmp/x.pdf", "right")  # should not raise

    @mock.patch("statement_parser.icici_bank_extractor.PdfReader")
    def test_noop_for_unencrypted_pdf(self, mock_reader_cls):
        mock_reader = mock.Mock()
        mock_reader.is_encrypted = False
        mock_reader_cls.return_value = mock_reader
        _decrypt_if_needed("/tmp/x.pdf", None)  # should not raise


class IciciBankExtractTransactionsTests(unittest.TestCase):
    @mock.patch("statement_parser.icici_bank_extractor.pdfplumber.open")
    @mock.patch("statement_parser.icici_bank_extractor.PdfReader")
    def test_emits_canonical_fields_and_drops_bf_row(self, mock_reader_cls, mock_open):
        mock_reader = mock.Mock()
        mock_reader.is_encrypted = False
        mock_reader_cls.return_value = mock_reader
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
    @mock.patch("statement_parser.icici_bank_extractor.PdfReader")
    def test_to_csv_still_writes_rich_columns(self, mock_reader_cls, mock_open):
        mock_reader = mock.Mock()
        mock_reader.is_encrypted = False
        mock_reader_cls.return_value = mock_reader
        mock_open.side_effect = _open_fake_pdf
        result = extract_transactions("/tmp/statement.pdf")
        text = to_csv_bytes(result["transactions"]).decode("utf-8")
        self.assertTrue(text.startswith("date,mode,particulars,deposit,withdrawal,balance,account_number"))
        self.assertIn("ACH/Groww/x", text)

    @mock.patch("statement_parser.icici_bank_extractor.pdfplumber.open")
    @mock.patch("statement_parser.icici_bank_extractor.PdfReader")
    def test_passes_password_to_pdfplumber(self, mock_reader_cls, mock_open):
        mock_reader = mock.Mock()
        mock_reader.is_encrypted = False
        mock_reader_cls.return_value = mock_reader
        mock_open.side_effect = _open_fake_pdf
        extract_transactions("/tmp/statement.pdf", password="1234")
        mock_open.assert_called_once_with("/tmp/statement.pdf", password="1234")

    @mock.patch("statement_parser.icici_bank_extractor.pdfplumber.open")
    @mock.patch("statement_parser.icici_bank_extractor.PdfReader")
    def test_raises_password_required_before_opening_encrypted_pdf(self, mock_reader_cls, mock_open):
        # The pypdf pre-check must reject an encrypted PDF whose password is
        # missing BEFORE pdfplumber even gets a chance to raise its
        # message-less PdfminerException wrapper (the old failure mode).
        mock_reader = mock.Mock()
        mock_reader.is_encrypted = True
        mock_reader_cls.return_value = mock_reader
        with self.assertRaises(PdfPasswordRequired):
            extract_transactions("/tmp/statement.pdf")
        mock_open.assert_not_called()


class IciciBankTemplateRegressionTests(unittest.TestCase):
    @mock.patch("statement_parser.icici_bank_extractor.pdfplumber.open")
    @mock.patch("statement_parser.icici_bank_extractor.PdfReader")
    def test_bf_row_label_and_wrapped_lines_go_to_the_right_txn(self, mock_reader_cls, mock_open):
        # Layout mirrors real page 1: the bare B/F row's label sits ~1pt off
        # its anchor (a rounding boundary), and the first real transaction's
        # wrapped PARTICULARS lines render both ABOVE its date (closer to the
        # B/F row than to their own anchor) and BELOW it. The B/F row must
        # not swallow or leak either, and its label must not leak either.
        mock_reader = mock.Mock()
        mock_reader.is_encrypted = False
        mock_reader_cls.return_value = mock_reader
        words = [
            _HEADER,
            _word("01-04-2025", 29.8, 68.0, 200.58),
            _word("B/F", 140.3, 150.8, 199.62),
            _word("2,19,825.76", 526.8, 564.1, 200.58),
            _word("UPI/FlipkartInterne/UPIIntent/AIRTEL", 140.3, 220.3, 210.65),
            _word("01-04-2025", 29.8, 68.0, 221.22),
            _word("PAYMENTS/509175898500/PPPL230046496060104", 140.3, 340.0, 220.25),
            _word("318.00", 461.6, 489.1, 221.22),
            _word("2,19,507.76", 526.8, 564.1, 221.22),
            _word("2501344567eaf55d/Flipkart Internet Groceries", 140.3, 340.0, 229.85),
            _TOTAL,
        ]
        mock_open.side_effect = lambda path, password="": _FakePdf([_FakePage(words)])
        result = extract_transactions("/tmp/statement.pdf")

        self.assertEqual(result["opening_balance"], 219825.76)
        self.assertEqual(result["transaction_count"], 1)
        self.assertEqual(
            result["transactions"][0]["description"],
            "UPI/FlipkartInterne/UPIIntent/AIRTEL PAYMENTS/509175898500/"
            "PPPL2300464960601042501344567eaf55d/Flipkart Internet Groceries",
        )
        self.assertFalse(any("B/F" in t["description"] for t in result["transactions"]))

    @mock.patch("statement_parser.icici_bank_extractor.pdfplumber.open")
    @mock.patch("statement_parser.icici_bank_extractor.PdfReader")
    def test_final_total_row_excludes_footer_and_validates_statement_totals(self, mock_reader_cls, mock_open):
        # Current template: the subtotal row is all-caps "TOTAL" (once, on the
        # last table page) and is followed by the "Account Related Other
        # Information" block. The info block must not bleed into the last
        # transaction, and the printed whole-statement totals must cross-check
        # cleanly at the statement level.
        mock_reader = mock.Mock()
        mock_reader.is_encrypted = False
        mock_reader_cls.return_value = mock_reader
        words = [
            _HEADER,
            _word("31-03-2026", 29.8, 68.0, 200.0),
            _word("CMS", 73.0, 92.6, 200.0),
            _word("TRANSACTION", 94.4, 113.8, 200.0),
            _word("CMS/ CMS5614232428/NAGARRO ENTERPRISE", 140.3, 340.0, 200.0),
            _word("1,46,139.00", 374.6, 406.1, 200.0),
            _word("1,75,677.70", 526.8, 564.1, 200.0),
            _word("SERVICES", 140.3, 190.0, 210.0),
            _word("PV", 192.0, 205.0, 210.0),
            _word("TOTAL", 27.0, 62.0, 220.0),
            _word("1,46,139.00", 374.6, 406.1, 220.0),
            _word("0.00", 461.6, 489.1, 220.0),
            _word("1,75,677.70", 526.8, 564.1, 220.0),
            _word("ACCOUNT", 27.0, 90.0, 235.0),
            _word("NUMBER", 151.0, 193.0, 235.0),
            _word("MICR", 299.0, 321.0, 235.0),
            _word("CODE", 321.0, 350.0, 235.0),
            _word("Nominee", 27.0, 105.0, 250.0),
            _word("consent", 150.0, 181.0, 250.0),
            _word("customer.", 174.0, 220.0, 250.0),
        ]
        mock_open.side_effect = lambda path, password="": _FakePdf([_FakePage(words)])
        result = extract_transactions("/tmp/statement.pdf")

        self.assertEqual(result["transaction_count"], 1)
        self.assertEqual(
            result["transactions"][0]["description"],
            "CMS TRANSACTION CMS/ CMS5614232428/NAGARRO ENTERPRISE SERVICES PV",
        )
        self.assertEqual(result["total_deposits"], 146139.0)
        self.assertEqual(result["total_withdrawals"], 0.0)
        self.assertEqual(result["closing_balance"], 175677.7)
        self.assertEqual(result["validation_errors"], [])

    @mock.patch("statement_parser.icici_bank_extractor.pdfplumber.open")
    @mock.patch("statement_parser.icici_bank_extractor.PdfReader")
    def test_account_number_from_title_across_pages(self, mock_reader_cls, mock_open):
        # The account title ("Account Number: XXXX in INR") sits above the
        # table header but well below the top of the page, and only on the
        # first table page; the account number must be found there and
        # backfilled onto transactions parsed from later (title-less) pages.
        mock_reader = mock.Mock()
        mock_reader.is_encrypted = False
        mock_reader_cls.return_value = mock_reader
        header_high = {"text": "PARTICULARS", "x0": 140.3, "x1": 193.0, "top": 250.0, "bottom": 259.0}
        title_words = [
            _word("Statement", 27.0, 90.0, 220.0),
            _word("of", 90.0, 100.0, 220.0),
            _word("Transactions", 100.0, 160.0, 220.0),
            _word("in", 160.0, 175.0, 220.0),
            _word("Savings", 175.0, 220.0, 220.0),
            _word("Account", 220.0, 260.0, 220.0),
            _word("Number:", 260.0, 290.0, 220.0),
            _word("057001527034", 290.0, 340.0, 220.0),
            _word("in", 340.0, 355.0, 220.0),
            _word("INR", 355.0, 380.0, 220.0),
        ]
        page1 = [
            header_high,
            *title_words,
            _word("01-04-2025", 29.8, 68.0, 270.0),
            _word("B/F", 140.3, 150.8, 270.0),
            _word("2,19,825.76", 526.8, 564.1, 270.0),
            _word("02-04-2025", 29.8, 68.0, 290.0),
            _word("ACH/Groww/x", 140.3, 310.8, 290.0),
            _word("2,000.00", 461.6, 489.1, 290.0),
            _word("1,92,497.61", 526.8, 564.1, 290.0),
            _TOTAL,
        ]
        page2 = [
            header_high,
            _word("03-04-2025", 29.8, 68.0, 270.0),
            _word("UPI/thanks/StateBank", 140.3, 332.1, 270.0),
            _word("30,000.00", 374.6, 406.1, 270.0),
            _word("1,62,497.61", 526.8, 564.1, 270.0),
            _TOTAL,
        ]
        mock_open.side_effect = lambda path, password="": _FakePdf(
            [_FakePage(page1), _FakePage(page2)]
        )
        result = extract_transactions("/tmp/statement.pdf")

        self.assertEqual(result["transaction_count"], 2)
        self.assertTrue(
            all(t["account_number"] == "057001527034" for t in result["transactions"])
        )

    @mock.patch("statement_parser.icici_bank_extractor.pdfplumber.open")
    @mock.patch("statement_parser.icici_bank_extractor.PdfReader")
    def test_ruled_grid_segments_records_and_joins_multiline_particulars(self, mock_reader_cls, mock_open):
        # New-template geometry: a ruled grid with one date-column rule per
        # record. The first data row has NO top rule of its own - the header
        # line is its upper bound - so band 0 spans header->first rule. Each
        # record's whole multiline PARTICULARS paragraph is extracted "in one
        # go" from its band, in reading order.
        mock_reader = mock.Mock()
        mock_reader.is_encrypted = False
        mock_reader_cls.return_value = mock_reader

        def rule(top):
            return {"x0": 25.0, "x1": 73.7, "top": top, "bottom": top, "y0": top, "y1": top}

        hdr = {"text": "PARTICULARS", "x0": 158.5, "x1": 193.0, "top": 150.92, "bottom": 159.9}
        words = [
            hdr,
            # record 1 (band 150.92-188.3): 3-line particulars, wrap-above + wrap-below
            _word("UPI/qwalle049369003/Payment", 158.5, 220.3, 161.0),
            _word("for", 220.3, 232.0, 161.0),
            _word("GoS/YES", 232.0, 260.0, 161.0),
            _word("BANK", 260.0, 280.0, 161.0),
            _word("LIMITE/987578867872/YBLc944caa4f65c", 158.5, 330.0, 170.6),
            _word("16-04-2025", 27.0, 68.0, 171.56),
            _word("1,000.00", 474.1, 490.0, 171.56),
            _word("18,511.88", 547.6, 565.0, 171.56),
            _word("ef5d971934M2P", 158.5, 220.0, 180.2),
            _word("Solutions", 220.0, 256.0, 180.2),
            _word("Pvt", 256.0, 272.0, 180.2),
            _word("Ltd", 272.0, 286.0, 180.2),
            # record 2 (band 188.3-217.6)
            _word("UPI/gpay-1122362189/Paid", 158.5, 220.3, 190.28),
            _word("via", 220.3, 232.0, 190.28),
            _word("Navi", 232.0, 254.0, 190.28),
            _word("U/AXIS", 254.0, 280.0, 190.28),
            _word("16-04-2025", 27.0, 68.0, 200.8),
            _word("420.00", 474.1, 490.0, 200.8),
            _word("18,091.88", 547.6, 565.0, 200.8),
            # record 3 (band 217.6-237.2)
            _word("ACH/TP ACH BAJAJ", 158.5, 240.0, 225.3),
            _word("20-04-2025", 27.0, 68.0, 225.3),
            _word("3,000.00", 489.1, 510.0, 225.3),
            _word("15,091.88", 547.6, 565.0, 225.3),
            _word("LIFE/ICIC7022709210001121/1755888636", 158.5, 340.0, 230.0),
        ]
        lines = [rule(188.3), rule(217.6), rule(237.2)]
        mock_open.side_effect = lambda path, password="": _FakePdf([_FakePage(words, lines=lines)])
        result = extract_transactions("/tmp/statement.pdf")

        self.assertEqual(result["transaction_count"], 3)
        self.assertEqual(
            result["transactions"][0]["description"],
            "UPI/qwalle049369003/Payment for GoS/YES BANK LIMITE/987578867872/"
            "YBLc944caa4f65cef5d971934M2P Solutions Pvt Ltd",
        )
        self.assertEqual(result["transactions"][0]["withdrawal"], 1000.0)
        self.assertEqual(result["transactions"][0]["balance"], 18511.88)
        self.assertEqual(result["transactions"][1]["withdrawal"], 420.0)
        self.assertEqual(
            result["transactions"][2]["description"],
            "ACH/TP ACH BAJAJ LIFE/ICIC7022709210001121/1755888636",
        )
        self.assertEqual(result["transactions"][2]["withdrawal"], 3000.0)

    @mock.patch("statement_parser.icici_bank_extractor.pdfplumber.open")
    @mock.patch("statement_parser.icici_bank_extractor.PdfReader")
    def test_ruled_grid_merged_band_falls_back_to_anchors(self, mock_reader_cls, mock_open):
        # Two records share one band (the rule between them is missing): the
        # merged-band guard disables ruled mode for the page so the fallback
        # path keeps the two transactions separate.
        mock_reader = mock.Mock()
        mock_reader.is_encrypted = False
        mock_reader_cls.return_value = mock_reader

        def rule(top):
            return {"x0": 25.0, "x1": 73.7, "top": top, "bottom": top, "y0": top, "y1": top}

        words = [
            _HEADER,
            _word("01-04-2025", 29.8, 68.0, 200.0),
            _word("ACH/Groww/x", 140.3, 310.8, 200.0),
            _word("2,000.00", 461.6, 489.1, 200.0),
            _word("1,92,497.61", 526.8, 564.1, 200.0),
            _word("02-04-2025", 29.8, 68.0, 225.0),
            _word("UPI/thanks/StateBank", 140.3, 332.1, 225.0),
            _word("30,000.00", 374.6, 406.1, 225.0),
            _word("1,62,497.61", 526.8, 564.1, 225.0),
        ]
        # a grid exists, but only one rule splits the page: both rows share
        # the band (150, 400) -> guard fires -> anchor path
        lines = [rule(400.0)]
        mock_open.side_effect = lambda path, password="": _FakePdf([_FakePage(words, lines=lines)])
        result = extract_transactions("/tmp/statement.pdf")

        self.assertEqual(result["transaction_count"], 2)
        self.assertEqual(result["transactions"][0]["withdrawal"], 2000.0)
        self.assertEqual(result["transactions"][1]["deposit"], 30000.0)

    def test_join_particulars_lines_smart(self):
        # Mid-token wrap: a UPI/ACH reference split across lines is
        # reassembled WITHOUT a space (a plain space-join would leave
        # "...b343fd ef5d...")...
        self.assertEqual(
            _join_particulars_lines(
                [
                    "UPI/qwalle049369003/Payment for GoS/YES BANK LIMITE/"
                    "987578867872/YBLc944caa4f65c4fc6b343fd",
                    "ef5d971934M2P Solutions Pvt Ltd",
                ]
            ),
            "UPI/qwalle049369003/Payment for GoS/YES BANK LIMITE/"
            "987578867872/YBLc944caa4f65c4fc6b343fdef5d971934M2P Solutions Pvt Ltd",
        )
        # ...digit continuation of a hex ref...
        self.assertEqual(
            _join_particulars_lines([".../YBL6a09b2f2fdff47c192a0f14", "9e19190fa/Cheq"]),
            ".../YBL6a09b2f2fdff47c192a0f149e19190fa/Cheq",
        )
        # ...but a numeric code + company suffix ("9657882 INC", the
        # systematic KISETSU mandate pattern) stays a separate field.
        self.assertEqual(
            _join_particulars_lines(
                ["ACH/KISETSU03042025 CAMS/ICIC7020606244001215/7953000543014", "9657882 INC"]
            ),
            "ACH/KISETSU03042025 CAMS/ICIC7020606244001215/7953000543014 9657882 INC",
        )
        # ...while a wrapped word boundary keeps its space.
        self.assertEqual(
            _join_particulars_lines(["UPI/.../Yes", "Bank Ltd/APY018f4490e/"]),
            "UPI/.../Yes Bank Ltd/APY018f4490e/",
        )
        # Empty/blank lines are dropped silently.
        self.assertEqual(_join_particulars_lines(["A", "", "  ", "B"]), "A B")

    def test_metadata_name_cust_id_accounts_and_period(self):
        text = (
            "MR.SUYASH MITTAL Your Base Branch: ICICI BANK LTD., INFOSYS\n"
            "Summary of Accounts held under Cust ID: 567635687 as on March 31, 2026\n"
            "ACCOUNT TYPE A/c BALANCE(I) FIXED DEPOSITS (LINKED) BAL.(II) TOTAL BALANCE(I+II) NOMINATION\n"
            "Current A/c 057005001064 0.00 0.00 0.00 Not Registered\n"
            "Savings A/c 057001527034 1,75,677.70 0.00 1,75,677.70 Not Registered\n"
            "Statement of Transactions in Savings Account Number: 057001527034 in INR "
            "for the period April 01, 2025 - March 31, 2026\n"
        )
        metadata = _extract_metadata(text)
        self.assertEqual(metadata["account_holder"], "Mr.Suyash Mittal")
        self.assertEqual(metadata["customer_id"], "567635687")
        self.assertEqual(metadata["statement_period_from"], "2025-04-01")
        self.assertEqual(metadata["statement_period_to"], "2026-03-31")
        self.assertEqual(
            metadata["accounts"],
            [
                {"type": "Current", "number": "057005001064", "balance": 0.0},
                {"type": "Savings", "number": "057001527034", "balance": 175677.7},
            ],
        )


if __name__ == "__main__":
    unittest.main()