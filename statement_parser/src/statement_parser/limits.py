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
from typing import Any

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
    """Return the PDF's page count, or raise PageLimitExceeded if it is too big."""
    count = len(pdf.pages)
    limit = max_pages()
    if count > limit:
        raise PageLimitExceeded(
            f"statement has {count} pages, exceeding the {limit}-page limit"
        )
    return count


__all__ = ["DEFAULT_MAX_PAGES", "PageLimitExceeded", "ensure_page_limit", "max_pages"]
