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
func (a Amount) String() string {
	n := int64(a)
	sign := ""
	if n < 0 {
		sign, n = "-", -n
	}
	return fmt.Sprintf("%s%d.%02d", sign, n/100, n%100)
}

// Parse parses a decimal string with at most two fractional digits.
func Parse(s string) (Amount, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("money: empty amount")
	}

	neg := false
	switch s[0] {
	case '-':
		neg, s = true, s[1:]
	case '+':
		s = s[1:]
	}

	intPart, fracPart, hasFrac := strings.Cut(s, ".")
	if intPart == "" {
		intPart = "0"
	}
	whole, err := strconv.ParseInt(intPart, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("money: invalid amount %q", s)
	}

	var frac int64
	if hasFrac {
		if len(fracPart) > 2 {
			return 0, fmt.Errorf("money: more than two decimal places in %q", s)
		}
		for len(fracPart) < 2 {
			fracPart += "0"
		}
		if fracPart != "" {
			frac, err = strconv.ParseInt(fracPart, 10, 64)
			if err != nil {
				return 0, fmt.Errorf("money: invalid amount %q", s)
			}
		}
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
// precision than a currency uses.
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
	*a = FromFloat(f)
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
		*a = FromFloat(v)
	default:
		return fmt.Errorf("money: unsupported Scan type %T", src)
	}
	return nil
}

// Value implements driver.Valuer for BIGINT columns holding minor units.
func (a Amount) Value() (driver.Value, error) { return int64(a), nil }
