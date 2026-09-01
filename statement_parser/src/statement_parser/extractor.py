"""
extractor.py
------------
Registry-based entry points for statement extractors.

This module keeps a stable public API while allowing future parsers to be
registered for different statement formats.
"""

from dataclasses import dataclass
from typing import Any, Callable, Dict, List, Optional

from .icici_cc_extractor import (
    PdfPasswordRequired,
    extract_transactions as _icici_extract_transactions,
    to_csv_bytes as _icici_to_csv_bytes,
)

from .sbi_cc_extractor import (
    PdfPasswordRequired,
    extract_transactions as _sbi_extract_transactions,
    to_csv_bytes as _sbi_to_csv_bytes,
)

from .icici_bank_extractor import (
    PdfPasswordRequired,
    extract_transactions as _icici_bank_extract_transactions,
    to_csv_bytes as _icici_bank_to_csv_bytes,
)

from .slice_bank_extractor import (
    PdfPasswordRequired,
    extract_transactions as _slice_bank_extract_transactions,
    to_csv_bytes as _slice_bank_to_csv_bytes,
)


@dataclass(frozen=True)
class ExtractorSpec:
    name: str
    display_name: str
    # Extractors return different dict shapes (some TypedDicts like
    # StatementResult, others plain dict[str, Any]) and accept different
    # transaction container types, so the registry types these loosely.
    extract: Callable[..., Any]
    to_csv: Callable[..., bytes]


_EXTRACTORS: Dict[str, ExtractorSpec] = {}


def register_extractor(
    name: str,
    display_name: str,
    extractor: Callable[..., Any],
    to_csv: Callable[..., bytes],
) -> None:
    """Register a new statement extractor implementation."""
    _EXTRACTORS[name.lower()] = ExtractorSpec(
        name=name.lower(),
        display_name=display_name,
        extract=extractor,
        to_csv=to_csv,
    )


def get_extractor(name: str) -> ExtractorSpec:
    """Return the registered extractor for the requested name."""
    extractor_name = name.strip().lower()
    try:
        return _EXTRACTORS[extractor_name]
    except KeyError as exc:
        available = ", ".join(sorted(_EXTRACTORS)) or "<none>"
        raise KeyError(f"Unsupported extractor '{name}'. Available extractors: {available}") from exc


def list_extractors() -> List[dict[str, str]]:
    """Return metadata for registered extractors."""
    specs = sorted(_EXTRACTORS.values(), key=lambda spec: spec.name)
    return [{"name": spec.name, "display_name": spec.display_name} for spec in specs]


def extract_transactions(
    path: str,
    extractor_name: str,
    password: Optional[str] = None,
) -> dict[str, Any]:
    """Extract transactions using the selected registered extractor."""
    spec = get_extractor(extractor_name)
    return spec.extract(path, password)


def to_csv_bytes(transactions: List[dict[str, Any]], extractor_name: str) -> bytes:
    """Serialize transactions to CSV using the selected registered extractor."""
    spec = get_extractor(extractor_name)
    return spec.to_csv(transactions)


register_extractor("icici_cc", "ICICI Credit Card", _icici_extract_transactions, _icici_to_csv_bytes)
register_extractor("sbi_cc", "SBI Credit Card", _sbi_extract_transactions, _sbi_to_csv_bytes)
register_extractor(
    "icici_bank", "ICICI Bank Statement", _icici_bank_extract_transactions, _icici_bank_to_csv_bytes
)
register_extractor(
    "slice_bank", "Slice Small Finance Bank", _slice_bank_extract_transactions, _slice_bank_to_csv_bytes
)

__all__ = [
    "ExtractorSpec",
    "PdfPasswordRequired",
    "extract_transactions",
    "get_extractor",
    "list_extractors",
    "register_extractor",
    "to_csv_bytes",
]
