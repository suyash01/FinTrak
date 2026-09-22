"""Money comparison for the extractors' validation checks.

The amounts the extractors compare are floats parsed from the printed text and
already rounded to two decimals, so a float tolerance is not a rounding guard —
the operands are exact to the cent — it only hides real one-cent breaks. Worse,
it hid them representation-dependently: ``abs(100.00 - 100.01)`` is
``0.010000000000005116`` (flagged) while ``abs(10.00 - 10.01)`` is
``0.009999999999999787`` (silently accepted), so whether a one-cent ledger
inconsistency was reported depended on the magnitude of the figures involved.

Everything therefore compares integer cents.
"""

from __future__ import annotations

__all__ = ["cents", "same_money"]


def cents(value: float) -> int:
    """Return a two-decimal amount as integer cents."""
    return int(round(value * 100))


def same_money(left: float, right: float) -> bool:
    """True when two amounts agree to the cent."""
    return cents(left) == cents(right)
