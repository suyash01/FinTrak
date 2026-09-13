import os
import sys
import unittest
from pathlib import Path
from unittest import mock

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "src"))

from statement_parser.limits import (  # noqa: E402
    DEFAULT_MAX_PAGES,
    PageLimitExceeded,
    ensure_page_limit,
    max_pages,
)


class _FakePdf:
    def __init__(self, pages):
        self.pages = list(range(pages))


class MaxPagesTests(unittest.TestCase):
    def test_default_when_unset(self):
        with mock.patch.dict(os.environ, {"MAX_PAGES": ""}):
            self.assertEqual(max_pages(), DEFAULT_MAX_PAGES)

    def test_invalid_falls_back_to_default(self):
        with mock.patch.dict(os.environ, {"MAX_PAGES": "abc"}):
            self.assertEqual(max_pages(), DEFAULT_MAX_PAGES)

    def test_non_positive_falls_back_to_default(self):
        with mock.patch.dict(os.environ, {"MAX_PAGES": "0"}):
            self.assertEqual(max_pages(), DEFAULT_MAX_PAGES)

    def test_override(self):
        with mock.patch.dict(os.environ, {"MAX_PAGES": "3"}):
            self.assertEqual(max_pages(), 3)


class EnsurePageLimitTests(unittest.TestCase):
    def test_within_limit_returns_count(self):
        with mock.patch.dict(os.environ, {"MAX_PAGES": "5"}):
            self.assertEqual(ensure_page_limit(_FakePdf(5)), 5)

    def test_exceeding_limit_raises(self):
        with mock.patch.dict(os.environ, {"MAX_PAGES": "5"}):
            with self.assertRaises(PageLimitExceeded):
                ensure_page_limit(_FakePdf(6))


if __name__ == "__main__":
    unittest.main()
