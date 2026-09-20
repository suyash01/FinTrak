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
// and mirrors money.Parse's grammar exactly (optional sign, at most two
// fractional digits), so the TUI rejects precisely what the API would reject.
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

// ParseAmount validates and canonicalizes user input into a wire amount. It
// accepts an optional sign and at most two fractional digits, matching the
// backend's money.Parse; the result is always rendered with two decimals so
// what the user typed and what the API stores agree.
func ParseAmount(s string) (Amount, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", fmt.Errorf("amount is required")
	}
	neg := false
	switch s[0] {
	case '-':
		neg, s = true, s[1:]
	case '+':
		s = s[1:]
	}
	if s == "" {
		return "", fmt.Errorf("invalid amount")
	}
	whole, frac, hasFrac := strings.Cut(s, ".")
	if whole == "" {
		whole = "0"
	}
	if _, err := strconv.ParseUint(whole, 10, 63); err != nil {
		return "", fmt.Errorf("invalid amount")
	}
	if hasFrac {
		if len(frac) > 2 {
			return "", fmt.Errorf("at most two decimal places")
		}
		for len(frac) < 2 {
			frac += "0"
		}
	} else {
		frac = "00"
	}
	if _, err := strconv.ParseUint(frac, 10, 16); err != nil {
		return "", fmt.Errorf("invalid amount")
	}
	out := whole + "." + frac
	if neg && out != "0.00" {
		out = "-" + out
	}
	return Amount(out), nil
}

// UnmarshalJSON keeps the raw decimal text. The wire value is a JSON number
// (money.Amount marshals with MarshalJSON), but a quoted number is accepted too
// so the TUI can decode bundles and fixtures that quote amounts.
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
// is sent as 0 — call sites that require one validate through ParseAmount
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

// isDecimal reports whether b is a JSON number literal with at most one dot.
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
