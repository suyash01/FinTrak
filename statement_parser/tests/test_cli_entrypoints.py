import contextlib
import io
import json
import sys
import tempfile
import unittest
from pathlib import Path
from unittest import mock

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "src"))

from statement_parser import (
    icici_cc_extractor,
    indusind_bank_extractor,
    sbi_cc_extractor,
    slice_bank_extractor,
)

MODULES = [
    sbi_cc_extractor,
    slice_bank_extractor,
    icici_cc_extractor,
    indusind_bank_extractor,
]

RESULT = {
    "transactions": [
        {"date": "2024-01-01", "description": "X", "amount": 1.0, "type": "Debit"}
    ],
    "summary": {},
    "page_count": 1,
    "transaction_count": 1,
}


class CliEntrypointTests(unittest.TestCase):
    def _run(self, module, argv, side_effect=None):
        with mock.patch.object(module, "extract_transactions") as extractor:
            if side_effect is not None:
                extractor.side_effect = side_effect
            else:
                extractor.return_value = RESULT
            with mock.patch.object(sys, "argv", [module.__name__] + argv):
                out = io.StringIO()
                err = io.StringIO()
                with contextlib.redirect_stdout(out), contextlib.redirect_stderr(err):
                    module.main()
                return out.getvalue(), err.getvalue()

    def test_prints_json_to_stdout_without_out(self):
        for module in MODULES:
            with self.subTest(module=module.__name__):
                out, _ = self._run(module, ["x.pdf"])
                self.assertIn('"description": "X"', out)

    def test_writes_csv_when_out_ends_with_csv(self):
        for module in MODULES:
            with self.subTest(module=module.__name__):
                with tempfile.TemporaryDirectory() as tmp:
                    path = Path(tmp) / "out.csv"
                    out, _ = self._run(module, ["x.pdf", "--out", str(path)])
                    self.assertTrue(path.exists())
                    text = path.read_text()
                    self.assertIn("description", text)
                    self.assertIn("X", text)
                    self.assertIn("Wrote 1 transactions", out)

    def test_writes_json_when_out_is_not_csv(self):
        for module in MODULES:
            with self.subTest(module=module.__name__):
                with tempfile.TemporaryDirectory() as tmp:
                    path = Path(tmp) / "out.json"
                    self._run(module, ["x.pdf", "--out", str(path)])
                    self.assertEqual(json.loads(path.read_text())["transaction_count"], 1)

    def test_password_required_exits_two(self):
        for module in MODULES:
            with self.subTest(module=module.__name__):
                with self.assertRaises(SystemExit) as ctx:
                    self._run(module, ["x.pdf"], side_effect=module.PdfPasswordRequired("pw"))
                self.assertEqual(ctx.exception.code, 2)

    def test_generic_error_exits_one(self):
        for module in MODULES:
            with self.subTest(module=module.__name__):
                with self.assertRaises(SystemExit) as ctx:
                    self._run(module, ["x.pdf"], side_effect=RuntimeError("boom"))
                self.assertEqual(ctx.exception.code, 1)


class PasswordExceptionTests(unittest.TestCase):
    """The password-detection helpers shared by the bank extractors."""

    class PDFPasswordIncorrect(Exception):
        pass

    def test_detects_password_exception_by_name(self):
        for module in (slice_bank_extractor, indusind_bank_extractor):
            with self.subTest(module=module.__name__):
                self.assertTrue(module._is_password_exception(self.PDFPasswordIncorrect()))

    def test_plain_exception_is_not_a_password_error(self):
        for module in (slice_bank_extractor, indusind_bank_extractor):
            with self.subTest(module=module.__name__):
                self.assertFalse(module._is_password_exception(ValueError("nope")))

    def test_detects_chained_password_exception(self):
        for module in (slice_bank_extractor, indusind_bank_extractor):
            with self.subTest(module=module.__name__):
                outer = RuntimeError("wrapped")
                outer.__cause__ = self.PDFPasswordIncorrect()
                self.assertTrue(module._is_password_exception(outer))

    def test_cyclic_cause_chain_terminates(self):
        for module in (slice_bank_extractor, indusind_bank_extractor):
            with self.subTest(module=module.__name__):
                cyc = RuntimeError("cycle")
                cyc.__cause__ = cyc
                self.assertFalse(module._is_password_exception(cyc))

    def test_open_pdf_translates_password_error(self):
        for module in (slice_bank_extractor, indusind_bank_extractor):
            with self.subTest(module=module.__name__):
                with mock.patch(
                    f"statement_parser.{module.__name__.split('.')[-1]}.pdfplumber.open",
                    side_effect=self.PDFPasswordIncorrect(),
                ):
                    with self.assertRaises(module.PdfPasswordRequired):
                        module._open_pdf("x.pdf", None)

    def test_open_pdf_reraises_other_errors(self):
        for module in (slice_bank_extractor, indusind_bank_extractor):
            with self.subTest(module=module.__name__):
                with mock.patch(
                    f"statement_parser.{module.__name__.split('.')[-1]}.pdfplumber.open",
                    side_effect=ValueError("corrupt"),
                ):
                    with self.assertRaises(ValueError):
                        module._open_pdf("x.pdf", None)


class AmountAndDateHelperTests(unittest.TestCase):
    def test_slice_parse_amount_edges(self):
        self.assertIsNone(slice_bank_extractor._parse_amount(None))
        self.assertIsNone(slice_bank_extractor._parse_amount(""))
        self.assertIsNone(slice_bank_extractor._parse_amount("   "))
        self.assertIsNone(slice_bank_extractor._parse_amount("not-a-number"))
        self.assertEqual(slice_bank_extractor._parse_amount("₹1,234.50"), 1234.50)

    def test_slice_parse_date_edges(self):
        self.assertEqual(slice_bank_extractor._parse_date("01 Sep '25"), "2025-09-01")
        self.assertEqual(slice_bank_extractor._parse_date("not a date"), "not a date")
        self.assertEqual(slice_bank_extractor._parse_date("01 Xyz '25"), "01 Xyz '25")

    def test_slice_starts_mid_token_edges(self):
        self.assertTrue(slice_bank_extractor._starts_mid_token("lower"))
        self.assertTrue(slice_bank_extractor._starts_mid_token("1st"))
        self.assertTrue(slice_bank_extractor._starts_mid_token("@upi"))
        self.assertTrue(slice_bank_extractor._starts_mid_token("A1"))
        self.assertFalse(slice_bank_extractor._starts_mid_token("Ab"))

    def test_indusind_parse_amount_edges(self):
        self.assertIsNone(indusind_bank_extractor._parse_amount(None))
        self.assertIsNone(indusind_bank_extractor._parse_amount("   "))
        self.assertIsNone(indusind_bank_extractor._parse_amount("junk"))
        self.assertEqual(indusind_bank_extractor._parse_amount("1,234.50"), 1234.50)

    def test_indusind_parse_date_edges(self):
        self.assertEqual(indusind_bank_extractor._parse_date("01-Sep-2025"), "2025-09-01")
        self.assertEqual(indusind_bank_extractor._parse_date("nope"), "nope")
        self.assertEqual(indusind_bank_extractor._parse_date("01-Xyz-2025"), "01-Xyz-2025")

    def test_indusind_starts_mid_token(self):
        self.assertTrue(indusind_bank_extractor._starts_mid_token("5@upi"))
        self.assertFalse(indusind_bank_extractor._starts_mid_token("Upper"))

    def test_icici_cc_dup_tolerant_pattern_non_alpha(self):
        pattern = icici_cc_extractor._dup_tolerant_pattern("A#1")
        self.assertIn(r"\#", pattern)
        self.assertIn("1", pattern)


if __name__ == "__main__":
    unittest.main()
