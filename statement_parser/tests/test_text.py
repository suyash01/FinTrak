import sys
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "src"))

from statement_parser.text import csv_cell


class TestCsvCell(unittest.TestCase):
    def test_formula_prefixes_are_quoted(self):
        for prefix in ("=", "+", "-", "@", "\t", "\r"):
            with self.subTest(prefix=repr(prefix)):
                self.assertEqual(csv_cell(prefix + "cmd"), "'" + prefix + "cmd")

    def test_plain_text_is_unchanged(self):
        self.assertEqual(csv_cell("UPI-SUYASH MITTAL"), "UPI-SUYASH MITTAL")
        self.assertEqual(csv_cell("100.00"), "100.00")

    def test_empty_string_is_unchanged(self):
        self.assertEqual(csv_cell(""), "")


if __name__ == "__main__":
    unittest.main()
