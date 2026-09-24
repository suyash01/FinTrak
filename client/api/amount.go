package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// Amount is a currency amount in decimal major units, carried verbatim between
// the API and the UI.
//
// The backend stores money as integer minor units and marshals it as a JSON
// number with exactly two decimals (money.Amount.String). The TUI therefore
// treats an amount as opaque text: it never does arithmetic on one and never
// reformats a value it received, because every total it displays was already
// computed server-side. ParseAmount is the single entry point for user input
// and mirrors money.Parse's grammar (one optional leading sign, a digit on both
// sides of the point when one is present, at most two fractional digits, and a
// magnitude no larger than money.MaxMinorUnits), so a form cannot submit an
// amount the API would refuse — including the shapes that are typos rather than
// numbers ("--1", ".5", "5.").
type Amount string

// String returns the amount as carried on the wire (e.g. "1250.50").
func (a Amount) String() string { return string(a) }

// IsZero reports whether the amount is absent or zero.
func (a Amount) IsZero() bool { return a == "" || a == "0.00" || a == "0" }

// IsNegative reports whether the amount is below zero. It reads the sign off
// the text rather than parsing a number.
func (a Amount) IsNegative() bool { return strings.HasPrefix(strings.TrimSpace(a.String()), "-") }

// Abs returns the amount with any leading sign removed.
func (a Amount) Abs() Amount { return Amount(strings.TrimPrefix(a.String(), "-")) }

// Display renders the amount with thousands separators for table output, e.g.
// "1,250.50" or "-0.05". It is pure text manipulation: no value is recomputed.
func (a Amount) Display() string {
	s := strings.TrimSpace(a.String())
	if s == "" {
		return "0.00"
	}
	sign := ""
	if strings.HasPrefix(s, "-") || strings.HasPrefix(s, "+") {
		if s[0] == '-' {
			sign = "-"
		}
		s = s[1:]
	}
	whole, frac, hasFrac := strings.Cut(s, ".")
	if !hasFrac {
		frac = "00"
	}
	for len(frac) < 2 {
		frac += "0"
	}
	return sign + groupThousands(whole) + "." + frac
}

// groupThousands inserts separators into an integer digit string.
func groupThousands(digits string) string {
	if len(digits) <= 3 {
		return digits
	}
	var b strings.Builder
	lead := len(digits) % 3
	if lead > 0 {
		b.WriteString(digits[:lead])
	}
	for i := lead; i < len(digits); i += 3 {
		if b.Len() > 0 {
			b.WriteByte(',')
		}
		b.WriteString(digits[i : i+3])
	}
	return b.String()
}

// Float64 parses the amount as a float for PRESENTATION ONLY — bar widths,
// heatmap intensity, sparkline scaling. It must never feed a value that is
// displayed as currency, compared, or sent back to the API: every stored,
// shown, or transmitted amount stays the exact decimal text the server sent.
// This mirrors money.Amount.Float64, which the backend likewise restricts to
// display and serialization.
func (a Amount) Float64() float64 {
	f, _ := strconv.ParseFloat(strings.TrimSpace(a.String()), 64)
	return f
}

// maxAmountMinorUnits mirrors money.MaxMinorUnits (1<<62 minor units): the
// largest amount the API accepts, because whole*100 must stay inside int64 on
// the backend and the float64 JSON boundary must still round-trip the value.
// ParseAmount rejects anything above it here rather than sending a value the
// API answers with a 400.
const maxAmountMinorUnits = 1 << 62

// ParseAmount validates and canonicalizes user input into a wire amount. It
// accepts an optional sign, requires a digit on both sides of the point when a
// fraction is present, allows at most two fractional digits and bounds the
// magnitude at maxAmountMinorUnits — the same rules money.Parse enforces in
// backend/internal/money/money.go — so the API cannot be handed an amount it
// would reject. The result is always rendered with two decimals so what the
// user typed and what the API stores agree.
func ParseAmount(s string) (Amount, error) {
	raw := strings.TrimSpace(s)
	if raw == "" {
		return "", fmt.Errorf("amount is required")
	}
	s = raw

	neg := false
	switch s[0] {
	case '-':
		neg, s = true, s[1:]
	case '+':
		s = s[1:]
	}

	wholeText, fracText, hasFrac := strings.Cut(s, ".")
	// An absent part is not a zero: "." and "-" carry no digits at all, and
	// ".5"/"5." are typos, not amounts. money.Parse rejects all four, so
	// accepting them here would let the form submit a value the API refuses.
	if wholeText == "" {
		return "", fmt.Errorf("invalid amount")
	}
	// The integer part is digits only: the sign was already consumed above, so
	// anything else here is a second sign ("--1", "+-1"), which the API also
	// rejects (money.Parse allows a sign only as the first character). Naming
	// the typo here keeps the form from sending a request that can only fail.
	if !digitsOnly(wholeText) {
		return "", fmt.Errorf("invalid amount")
	}
	whole, err := strconv.ParseInt(wholeText, 10, 64)
	if err != nil {
		// Only an overflow reaches here; the digits are digits.
		return "", fmt.Errorf("invalid amount")
	}

	var frac int64
	if hasFrac {
		if fracText == "" {
			return "", fmt.Errorf("invalid amount")
		}
		if len(fracText) > 2 {
			return "", fmt.Errorf("at most two decimal places")
		}
		for len(fracText) < 2 {
			fracText += "0"
		}
		frac, err = strconv.ParseInt(fracText, 10, 64)
		if err != nil {
			return "", fmt.Errorf("invalid amount")
		}
	} else {
		fracText = "00"
	}

	// Checked before it is rendered: above the bound whole*100 would overflow
	// int64 on the backend, which refuses the amount, and the float64 JSON
	// boundary could not carry it either.
	if whole > (maxAmountMinorUnits-frac)/100 {
		return "", fmt.Errorf("amount is out of range")
	}

	// Rendered from the parsed digits rather than from the input: a leading
	// zero run ("007") is not a valid JSON number, so it would make the request
	// body unparseable even though the API accepts the value itself.
	out := strconv.FormatInt(whole, 10) + "." + fracText
	if neg && out != "0.00" {
		out = "-" + out
	}
	return Amount(out), nil
}

// digitsOnly reports whether s is a non-empty run of ASCII digits, i.e. an
// integer part that carries no sign of its own.
func digitsOnly(s string) bool {
	if s == "" {
		return false
	}
	for i := range s {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// UnmarshalJSON keeps the raw decimal text. The wire value is a JSON number
// (money.Amount marshals with MarshalJSON), but a quoted number is accepted too
// so the TUI can decode bundles and fixtures that quote amounts. This decoder
// is intentionally more permissive than ParseAmount: it accepts the structural
// decimal forms used by stored fixtures and does not apply the API's strict
// user-input grammar or MaxMinorUnits bound.
func (a *Amount) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if len(b) == 0 || string(b) == "null" {
		*a = ""
		return nil
	}
	if b[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		s = strings.TrimSpace(s)
		if s == "" {
			*a = ""
			return nil
		}
		if !isDecimal([]byte(s)) {
			return fmt.Errorf("invalid amount %q", s)
		}
		*a = Amount(s)
		return nil
	}
	if !isDecimal(b) {
		return fmt.Errorf("invalid amount %s", b)
	}
	*a = Amount(string(b))
	return nil
}

// MarshalJSON emits the amount as an unquoted JSON number so request bodies
// match what the backend's money.Amount expects on the way in. An unset amount
// is sent as 0. This method checks decimal structure, not the MaxMinorUnits
// bound; call sites accepting user input should validate through ParseAmount
// first.
func (a Amount) MarshalJSON() ([]byte, error) {
	s := strings.TrimSpace(a.String())
	if s == "" {
		return []byte("0"), nil
	}
	if !isDecimal([]byte(s)) {
		return nil, fmt.Errorf("invalid amount %q", s)
	}
	return []byte(s), nil
}

// isDecimal reports whether b is a decimal-shaped token with at most one dot.
// It is a structural guard for JSON amounts, not a strict JSON-number parser;
// values such as `.5` and `1.` pass here, while ParseAmount rejects them.
func isDecimal(b []byte) bool {
	if len(b) == 0 {
		return false
	}
	seenDigit, seenDot := false, false
	for i, c := range b {
		switch {
		case c >= '0' && c <= '9':
			seenDigit = true
		case c == '-' || c == '+':
			if i != 0 {
				return false
			}
		case c == '.':
			if seenDot {
				return false
			}
			seenDot = true
		default:
			return false
		}
	}
	return seenDigit
}
