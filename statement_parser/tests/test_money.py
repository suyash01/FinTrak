"""The money comparison the extractors' validation checks use."""

import unittest

from statement_parser.money import cents, same_money


class SameMoneyTests(unittest.TestCase):
    def test_equal_amounts_agree(self):
        self.assertTrue(same_money(5892.32, 5892.32))
        self.assertTrue(same_money(0.0, 0.0))

    def test_a_one_cent_break_is_a_difference(self):
        # The float tolerance these replaced accepted the small-magnitude pair
        # (abs(10.00 - 10.01) is 0.009999999999999787) while flagging the very
        # same break at a larger magnitude, so whether a one-cent ledger
        # inconsistency was reported depended on the figures involved.
        self.assertFalse(same_money(10.00, 10.01))
        self.assertFalse(same_money(100.00, 100.01))
        self.assertFalse(same_money(5748.32, 5748.33))

    def test_cents_collapses_float_noise(self):
        self.assertEqual(cents(0.1 + 0.2), 30)
        self.assertEqual(cents(1234.56), 123456)


if __name__ == "__main__":
    unittest.main()
