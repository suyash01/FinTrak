// Package money represents currency amounts as integer minor units (cents).
//
// Performing arithmetic in integer cents avoids the rounding drift inherent in
// float64 money math. Conversion to and from the decimal representation used on
// the wire and in configuration happens only at the boundaries: JSON
// marshal/unmarshal (dollars) and SQL scan/value (cents, matching the BIGINT
// columns).
package money

import (
	"database/sql/driver"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Amount is a currency amount in minor units (1/100 of the major unit).
type Amount int64

// MaxMinorUnits bounds a single amount's minor units. Every accepted amount has
// to survive both boundary conversions exactly:
//
//   - the JSON boundary is a float64, and float64 cannot represent every int64
//     value near its maximum: an amount parsed from a float that rounds above
//     math.MaxInt64 converts back to math.MinInt64, and the row then marshals as
//     a malformed number (the whole response fails). 1<<62 is exactly
//     representable as a float64, so a value at the bound still round-trips.
//   - the storage boundary is BIGINT, so whole*100 + fraction must not overflow.
//
// The bound is far above any real ledger amount (≈4.6e16 major units) and it is
// what "out of range" means for both Parse and UnmarshalJSON.
const MaxMinorUnits Amount = 1 << 62

// amountOutOfRange reports the standard error for a value outside the
// supported range. All rejection paths share it so an out-of-range amount is
// never silently wrapped into the int64.
func amountOutOfRange(raw string) error {
	return fmt.Errorf("money: amount %q is out of range", raw)
}

// FromFloat converts a decimal amount to cents, rounding to the nearest cent.
func FromFloat(f float64) Amount {
	return Amount(math.Round(f * 100))
}

// Float64 returns the decimal amount. Intended for display/serialization only.
func (a Amount) Float64() float64 { return float64(a) / 100 }

// Cents returns the raw minor-unit value.
func (a Amount) Cents() int64 { return int64(a) }

// Abs returns the absolute value.
func (a Amount) Abs() Amount {
	if a < 0 {
		return -a
	}
	return a
}

// String renders the amount as a fixed two-decimal string (e.g. "1250.50").
// It renders through uint64 so a stored value whose magnitude overflows int64
// when negated (math.MinInt64) still produces a valid decimal — and therefore a
// valid JSON number — instead of a malformed one.
func (a Amount) String() string {
	n := int64(a)
	sign := ""
	if n < 0 {
		sign = "-"
	}
	units := uint64(n)
	if n < 0 {
		units = -units
	}
	return fmt.Sprintf("%s%d.%02d", sign, units/100, units%100)
}

// Parse parses a decimal string with one optional leading sign, decimal digits,
// and at most two fractional digits. The grammar is strict: a sign is accepted
// only as the first character (so "--1" and "+-1" are errors, not +1.00/-1.00),
// and neither the integer nor the fraction part may be empty.
func Parse(s string) (Amount, error) {
	raw := strings.TrimSpace(s)
	if raw == "" {
		return 0, fmt.Errorf("money: empty amount")
	}
	s = raw

	neg := false
	switch s[0] {
	case '-':
		neg, s = true, s[1:]
	case '+':
		s = s[1:]
	}
	// One sign, at the front, and nothing else: strconv.ParseInt would happily
	// accept a second sign on the part that follows ("-1", "-5"), which the
	// switch above would then apply on top — parsing "--1" as +1.00 and "1.-5"
	// as 0.95.
	if strings.ContainsAny(s, "+-") {
		return 0, fmt.Errorf("money: invalid amount %q", raw)
	}

	intPart, fracPart, hasFrac := strings.Cut(s, ".")
	// An empty part is not a number: "." and "-" have no digits at all, and a
	// missing integer ("5.") or fraction (".5") part is a typo, not a zero.
	if intPart == "" {
		return 0, fmt.Errorf("money: invalid amount %q", raw)
	}
	whole, err := strconv.ParseInt(intPart, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("money: invalid amount %q", raw)
	}

	var frac int64
	if hasFrac {
		if fracPart == "" {
			return 0, fmt.Errorf("money: invalid amount %q", raw)
		}
		if len(fracPart) > 2 {
			return 0, fmt.Errorf("money: more than two decimal places in %q", raw)
		}
		for len(fracPart) < 2 {
			fracPart += "0"
		}
		frac, err = strconv.ParseInt(fracPart, 10, 64)
		if err != nil {
			return 0, fmt.Errorf("money: invalid amount %q", raw)
		}
	}

	// Check before multiplying: whole*100 would overflow int64 silently.
	if whole > (int64(MaxMinorUnits)-frac)/100 {
		return 0, amountOutOfRange(raw)
	}
	total := whole*100 + frac
	if neg {
		total = -total
	}
	return Amount(total), nil
}

// MarshalJSON emits the amount as a JSON number in major units.
func (a Amount) MarshalJSON() ([]byte, error) {
	return []byte(a.String()), nil
}

// UnmarshalJSON parses a JSON number (or quoted number) in major units,
// rounding to the nearest cent. It is deliberately lenient about extra decimal
// places: the wire representation is a float, and bulk imports may carry more
// precision than a currency uses. It rejects a non-finite (NaN/±Inf, which
// strconv.ParseFloat accepts from text) or out-of-range value instead of
// wrapping it into the int64: such a row serializes as a malformed JSON number
// and makes every response containing it fail to marshal.
func (a *Amount) UnmarshalJSON(b []byte) error {
	s := strings.TrimSpace(string(b))
	if s == "null" {
		*a = 0
		return nil
	}
	s = strings.Trim(s, `"`)
	if s == "" {
		return fmt.Errorf("money: empty amount")
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return fmt.Errorf("money: invalid amount %q", s)
	}
	cents := math.Round(f * 100)
	// The comparison is exact: float64(MaxMinorUnits) is a power of two.
	if math.IsNaN(cents) || math.IsInf(cents, 0) || math.Abs(cents) > float64(MaxMinorUnits) {
		return amountOutOfRange(s)
	}
	*a = Amount(cents)
	return nil
}

// Scan implements sql.Scanner for BIGINT columns holding minor units.
func (a *Amount) Scan(src any) error {
	switch v := src.(type) {
	case nil:
		*a = 0
	case int64:
		*a = Amount(v)
	case int32:
		*a = Amount(v)
	case int:
		*a = Amount(v)
	case []byte:
		n, err := strconv.ParseInt(string(v), 10, 64)
		if err != nil {
			return fmt.Errorf("money: scanning bytes: %w", err)
		}
		*a = Amount(n)
	case string:
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return fmt.Errorf("money: scanning string: %w", err)
		}
		*a = Amount(n)
	case float64:
		// Floats are major units (legacy/mock rows). They get the same bound as
		// the JSON boundary so a float64 column cannot reintroduce the
		// out-of-range int64 that UnmarshalJSON now rejects.
		cents := math.Round(v * 100)
		if math.IsNaN(cents) || math.IsInf(cents, 0) || math.Abs(cents) > float64(MaxMinorUnits) {
			return amountOutOfRange(strconv.FormatFloat(v, 'g', -1, 64))
		}
		*a = Amount(cents)
	default:
		return fmt.Errorf("money: unsupported Scan type %T", src)
	}
	return nil
}

// Value implements driver.Valuer for BIGINT columns holding minor units.
func (a Amount) Value() (driver.Value, error) { return int64(a), nil }
