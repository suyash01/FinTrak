"""
limits.py
---------
Resource bounds shared by every statement extractor.

PDF parse cost is driven by page count rather than upload size, so a crafted
"decompression bomb" can exhaust CPU/memory even under the app's 20 MB upload
limit. `ensure_page_limit` caps the number of pages an extractor will touch;
the cap is configurable via the `MAX_PAGES` environment variable.
"""

import os
from typing import Any, Optional

from pdfminer.pdfpage import PDFPage
from pdfminer.pdftypes import resolve1

DEFAULT_MAX_PAGES = 500


class PageLimitExceeded(Exception):
    """Raised when a PDF has more pages than the configured maximum."""


def max_pages() -> int:
    """Return the configured page cap, falling back to the default."""
    raw = os.environ.get("MAX_PAGES", "").strip()
    if not raw:
        return DEFAULT_MAX_PAGES
    try:
        value = int(raw)
    except ValueError:
        return DEFAULT_MAX_PAGES
    return value if value > 0 else DEFAULT_MAX_PAGES


def ensure_page_limit(pdf: Any) -> int:
    """Return the PDF's page count, or raise PageLimitExceeded if it is too big.

    Counting is bounded: the page tree is walked lazily and the walk stops as
    soon as the limit is passed. `len(pdf.pages)` — what this used to do — builds
    a Page object for *every* page, so the cap fired only after paying the full
    cost it exists to prevent (measured: 5.98 s and ~140 MB for a 5.2 MB file
    declaring 50 000 pages, with gunicorn running two workers).
    """
    limit = max_pages()
    count = _count_pages(pdf, limit)
    if count > limit:
        raise PageLimitExceeded(
            f"statement has more than {limit} pages, exceeding the {limit}-page limit"
        )
    return count


def _count_pages(pdf: Any, limit: int) -> int:
    """Count the PDF's pages, materializing at most limit+1 of them.

    Two cheap steps, in order:
      1. the page tree's declared /Count, which costs one object resolution and
         rejects a bomb outright;
      2. a lazy walk of the tree that stops at the cap, which catches a document
         whose /Count understates its real page count.

    The walk works from the already-opened document rather than re-parsing the
    stream: an encrypted statement's stream would need the password again.
    """
    doc = getattr(pdf, "doc", None)
    if not isinstance(getattr(doc, "catalog", None), dict):
        # No readable document handle (a wrapper or a test double): fall back to
        # whatever the object exposes. pdfplumber.PDF always carries a real one.
        return len(pdf.pages)

    if declared := declared_page_count(doc):
        if declared > limit:
            return declared

    count = 0
    for _ in PDFPage.create_pages(doc):
        count += 1
        if count > limit:
            break
    return count


def close_pdf(pdf: Any) -> None:
    """Release a pdfplumber document without building a Page for every page.

    `pdfplumber.PDF.close()` is `self.flush_cache(); for page in self.pages:
    page.close(); ...`, and `pages` is the lazy cached property that constructs
    a `Page` for every page in the document. Closing that way therefore rebuilds
    exactly the whole page tree `ensure_page_limit` exists to avoid building —
    so a rejected bomb (the 5.98 s / ~140 MB file declaring 50 000 pages) paid
    the full cost on the way out, any time `close()` or `with ... as pdf` ran in
    the extractor's cleanup. Flush the caches and close the stream instead;
    every attribute is tolerated as missing so wrappers and test doubles work.
    """
    try:
        pdf.flush_cache()
    except Exception:
        pass
    stream = getattr(pdf, "stream", None)
    if stream is not None and not getattr(pdf, "stream_is_external", True):
        stream.close()


def declared_page_count(doc: Any) -> Optional[int]:
    """Return the page tree's declared /Count, or None when it does not state
    one (a damaged or unusual file still goes through the bounded walk)."""
    try:
        node = resolve1(doc.catalog.get("Pages"))
        if isinstance(node, dict) and "Count" in node:
            return int(resolve1(node["Count"]))
    except Exception:  # a malformed tree must not break the parse
        return None
    return None


__all__ = [
    "DEFAULT_MAX_PAGES",
    "PageLimitExceeded",
    "close_pdf",
    "declared_page_count",
    "ensure_page_limit",
    "max_pages",
]
