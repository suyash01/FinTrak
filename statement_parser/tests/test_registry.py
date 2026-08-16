import sys
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "src"))

from statement_parser.extractor import get_extractor, list_extractors


class ExtractorRegistryTests(unittest.TestCase):
    def test_default_sbi_extractor_is_registered(self):
        names = [spec["name"] for spec in list_extractors()]
        self.assertIn("sbi_cc", names)

    def test_extractor_has_display_name(self):
        by_name = {spec["name"]: spec for spec in list_extractors()}
        self.assertEqual(by_name["sbi_cc"]["display_name"], "SBI Credit Card")

    def test_get_extractor_returns_default_spec(self):
        spec = get_extractor("sbi_cc")
        self.assertEqual(spec.name, "sbi_cc")
        self.assertEqual(spec.display_name, "SBI Credit Card")
        self.assertTrue(callable(spec.extract))
        self.assertTrue(callable(spec.to_csv))


if __name__ == "__main__":
    unittest.main()
