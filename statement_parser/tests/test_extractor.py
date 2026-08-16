import sys
import unittest
from pathlib import Path
from unittest import mock

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "src"))

from statement_parser import extractor as extractor_module
from statement_parser.extractor import (
    extract_transactions,
    get_extractor,
    list_extractors,
    register_extractor,
    to_csv_bytes,
)


class ExtractorRegistryTests(unittest.TestCase):
    def test_icici_extractor_is_registered(self):
        names = [spec["name"] for spec in list_extractors()]
        self.assertIn("icici_cc", names)

    def test_icici_extractor_has_display_name(self):
        by_name = {spec["name"]: spec for spec in list_extractors()}
        self.assertEqual(by_name["icici_cc"]["display_name"], "ICICI Credit Card")

    def test_list_extractors_is_sorted(self):
        names = [spec["name"] for spec in list_extractors()]
        self.assertEqual(names, sorted(names))

    def test_get_extractor_is_case_insensitive_and_strips_whitespace(self):
        spec = get_extractor("  SBI_CC ")
        self.assertEqual(spec.name, "sbi_cc")

    def test_get_extractor_unknown_raises_keyerror_with_available(self):
        with self.assertRaises(KeyError) as ctx:
            get_extractor("nope")
        self.assertIn("nope", str(ctx.exception))
        self.assertIn("sbi_cc", str(ctx.exception))
        self.assertIn("icici_cc", str(ctx.exception))

    def test_register_extractor_adds_new_one(self):
        def fake_extract(path, password):
            return {"transactions": []}

        def fake_to_csv(transactions):
            return b""

        register_extractor("test_bank", "Test Bank", fake_extract, fake_to_csv)
        try:
            spec = get_extractor("test_bank")
            self.assertEqual(spec.name, "test_bank")
            self.assertEqual(spec.display_name, "Test Bank")
            self.assertIn("test_bank", [s["name"] for s in list_extractors()])
        finally:
            extractor_module._EXTRACTORS.pop("test_bank")

    def test_extract_transactions_dispatches_to_registered_extractor(self):
        with mock.patch("statement_parser.extractor.get_extractor") as m:
            spec = mock.Mock()
            spec.extract.return_value = {"transactions": [{"a": 1}]}
            m.return_value = spec

            result = extract_transactions("/tmp/x.pdf", "sbi_cc", password="pw")

            spec.extract.assert_called_once_with("/tmp/x.pdf", "pw")
            self.assertEqual(result, {"transactions": [{"a": 1}]})

    def test_to_csv_bytes_dispatches_to_registered_extractor(self):
        with mock.patch("statement_parser.extractor.get_extractor") as m:
            spec = mock.Mock()
            spec.to_csv.return_value = b"csv"
            m.return_value = spec

            result = to_csv_bytes([{"a": 1}], "icici_cc")

            spec.to_csv.assert_called_once_with([{"a": 1}])
            self.assertEqual(result, b"csv")


if __name__ == "__main__":
    unittest.main()