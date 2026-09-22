import os
import sys
import unittest
from pathlib import Path
from unittest import mock

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "src"))

from statement_parser import limits  # noqa: E402
from statement_parser.limits import (  # noqa: E402
    DEFAULT_MAX_PAGES,
    PageLimitExceeded,
    ensure_page_limit,
    max_pages,
)


class _FakePdf:
    def __init__(self, pages):
        self.pages = list(range(pages))


class _DocPdf:
    """A pdf whose pages can only be counted through its document handle, the
    way pdfplumber.PDF exposes them."""

    class _Doc:
        catalog = {}  # pdfminer's document handle exposes a mapping catalog

    def __init__(self, doc=None):
        self.doc = doc if doc is not None else self._Doc()
        self.pages_materialized = False

    @property
    def pages(self):
        self.pages_materialized = True
        raise AssertionError("the bounded walk must not touch pdf.pages")


class _DeclaredPdf:
    """A pdf whose page tree declares a count in its catalog."""

    class _Doc:
        def __init__(self, count):
            self.catalog = {"Pages": {"Type": "Pages", "Count": count, "Kids": []}}

    def __init__(self, declared):
        self.doc = self._Doc(declared)

    @property
    def pages(self):
        raise AssertionError("the declared count must be checked before any page is touched")


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

    def test_bounded_walk_stops_at_the_cap(self):
        """The walk must be bounded, not materialized: a PDF declaring a huge
        page count used to cost seconds and ~140 MB before the cap fired."""
        walked = 0

        def fake_create_pages(doc):
            nonlocal walked
            for i in range(10_000):  # a bomb: the walk must not consume it
                walked += 1
                yield i

        with mock.patch.dict(os.environ, {"MAX_PAGES": "5"}), mock.patch.object(
            limits.PDFPage, "create_pages", staticmethod(fake_create_pages)
        ):
            with self.assertRaises(PageLimitExceeded):
                ensure_page_limit(_DocPdf())

        # Six pages are pulled (the cap plus the one that proves it was passed).
        self.assertEqual(walked, 6)

    def test_bounded_walk_returns_the_real_count_within_the_cap(self):
        def fake_create_pages(doc):
            for i in range(3):
                yield i

        with mock.patch.dict(os.environ, {"MAX_PAGES": "5"}), mock.patch.object(
            limits.PDFPage, "create_pages", staticmethod(fake_create_pages)
        ):
            self.assertEqual(ensure_page_limit(_DocPdf()), 3)

    def test_a_declared_count_above_the_cap_is_rejected_without_walking(self):
        walked = False

        def fake_create_pages(doc):
            nonlocal walked
            walked = True
            yield 1

        with mock.patch.dict(os.environ, {"MAX_PAGES": "5"}), mock.patch.object(
            limits.PDFPage, "create_pages", staticmethod(fake_create_pages)
        ):
            with self.assertRaises(PageLimitExceeded):
                ensure_page_limit(_DeclaredPdf(50_000))

        self.assertFalse(walked, "the tree must not be walked when /Count already exceeds the cap")

    def test_a_declared_count_within_the_cap_still_walks(self):
        """A /Count that understates the tree must not slip past the cap."""

        def fake_create_pages(doc):
            for i in range(10):
                yield i

        with mock.patch.dict(os.environ, {"MAX_PAGES": "5"}), mock.patch.object(
            limits.PDFPage, "create_pages", staticmethod(fake_create_pages)
        ):
            with self.assertRaises(PageLimitExceeded):
                ensure_page_limit(_DeclaredPdf(2))

    def test_a_declared_count_within_the_cap_is_returned(self):
        def fake_create_pages(doc):
            for i in range(3):
                yield i

        with mock.patch.dict(os.environ, {"MAX_PAGES": "5"}), mock.patch.object(
            limits.PDFPage, "create_pages", staticmethod(fake_create_pages)
        ):
            self.assertEqual(ensure_page_limit(_DeclaredPdf(3)), 3)

    def test_an_unreadable_document_handle_falls_back(self):
        """A handle that cannot be walked (a wrapper, a test double) still counts
        through whatever the object exposes."""

        class _Unreadable:
            def __init__(self, pages):
                self.doc = object()
                self.pages = list(range(pages))

        with mock.patch.dict(os.environ, {"MAX_PAGES": "5"}):
            self.assertEqual(ensure_page_limit(_Unreadable(3)), 3)

    def test_an_object_without_a_document_handle_still_works(self):
        # The fallback keeps other wrappers (and these tests) working.
        with mock.patch.dict(os.environ, {"MAX_PAGES": "5"}):
            self.assertEqual(ensure_page_limit(_FakePdf(2)), 2)


if __name__ == "__main__":
    unittest.main()
