package api

import (
	"fmt"
	"sort"
	"strings"
)

// The arithmetic behind CurrencyAmounts (types.go holds the type itself, its
// scoped-account and scope siblings, and the doc comment that says why a
// reporting response has no bare amount in it). Every method here is the
// client's copy of one rule the server already enforces in
// models.CurrencyAmounts: nothing adds across currencies, a difference is
// computed one currency at a time, and a caller that cannot render every
// currency is told so rather than handed one of them.
//
// The arithmetic is exact decimal, in integer hundredths. It never routes
// through a float64: Amount carries the server's decimal text verbatim, and a
// float64 cannot hold it — 2^53+1 is not representable, so a value at that
// magnitude silently loses its cents. Amount.Float64 exists for bar widths
// only, and this is not that.

// Add folds one contribution in and returns the map, so it works on a nil map
// as well as one from the decoder: assigning into a nil map panics, and a
// decoded-but-absent field arrives as one.
func (m CurrencyAmounts) Add(currency string, amount Amount) CurrencyAmounts {
	if m == nil {
		m = CurrencyAmounts{}
	}
	m[currency] = addDecimal(m[currency], amount)
	return m
}

// Single returns the map's only entry, and ok=false whenever it does not hold
// exactly one currency — including when it is empty or nil. It is the answer to
// "may this be treated as a single number", and the only sanctioned way to reach
// a bare amount out of this type. A response that needs no narrowing needs no
// reasoning about currencies at all, which is why the server sends a one-key map
// rather than a scalar it would have to take back.
func (m CurrencyAmounts) Single() (string, Amount, bool) {
	if len(m) != 1 {
		return "", "", false
	}
	for code, amount := range m {
		return code, amount, true
	}
	return "", "", false
}

// Currencies returns the codes in sorted order, so a picker and a rendering do
// not depend on map iteration order.
func (m CurrencyAmounts) Currencies() []string {
	out := make([]string, 0, len(m))
	for code := range m {
		out = append(out, code)
	}
	sort.Strings(out)
	return out
}

// Sub returns the per-currency difference m - other, mirroring
// models.CurrencyAmounts.Sub exactly: a currency missing from either operand
// counts as zero and the result carries the union of the key sets. A currency
// that only ever spends therefore gets a negative net rather than vanishing,
// which is the case the union exists for, and no key is ever invented.
//
// A difference is only meaningful inside one currency, so prefer the server's
// own net (TotalNet, and Net on a timeline period or a calendar) over calling
// this. It is here for a client-side comparison between two figures the server
// did not difference for us.
func (m CurrencyAmounts) Sub(other CurrencyAmounts) CurrencyAmounts {
	out := make(CurrencyAmounts, len(m)+len(other))
	for code, amount := range m {
		out[code] = amount
	}
	for code, amount := range other {
		// Negating and adding keeps the sign arithmetic in one place, rather than
		// a second decimal subtraction that could disagree with this one on a
		// borrow or a carry.
		out[code] = addDecimal(out[code], negate(amount))
	}
	return out
}

// Display renders the value for a terminal, which is the tightest space any
// consumer of this type has. One currency formats as "INR 5,000.00". More than
// one renders as an explicit refusal — "2 currencies: EUR 1.00, INR 5,000.00" —
// because there is no room for two currencies and there is no figure that
// stands for both. Picking one silently is the exact bug the type was
// introduced to stop, and adding them is worse: it would report a number that
// never existed.
func (m CurrencyAmounts) Display() string {
	if code, amount, ok := m.Single(); ok {
		return code + " " + amount.Display()
	}
	if len(m) == 0 {
		return "no currency"
	}
	codes := m.Currencies()
	parts := make([]string, 0, len(codes))
	for _, code := range codes {
		parts = append(parts, code+" "+m[code].Display())
	}
	return fmt.Sprintf("%d currencies: %s", len(codes), strings.Join(parts, ", "))
}

// IsNegative reports whether the value is below zero in every currency it
// covers. A map that is negative in one currency and positive in another is
// neither, which is the honest answer: a sign is one bit, and a figure with no
// single sign has none to report. Callers that colour by sign therefore leave
// such a figure uncoloured rather than picking a currency to decide with.
func (m CurrencyAmounts) IsNegative() bool {
	if len(m) == 0 {
		return false
	}
	for _, amount := range m {
		if !amount.IsNegative() {
			return false
		}
	}
	return true
}

// negate flips an amount's sign. A subtrahend that is already negative has to
// come out positive, or a difference like 1.00 - (-4.00) would add: a reporting
// response's net is negative whenever the window spent more than it took in, so
// comparing two nets is exactly the case that reaches here. Prepending a minus
// without stripping an existing one would not flip anything, and would report
// 1.00 - (-4.00) as -3.00.
func negate(a Amount) Amount {
	s := strings.TrimSpace(a.String())
	switch {
	case strings.HasPrefix(s, "-"):
		return Amount(s[1:])
	case strings.HasPrefix(s, "+"):
		return Amount("-" + s[1:])
	default:
		return Amount("-" + s)
	}
}

// addDecimal adds two decimal amounts exactly, in minor units, and renders the
// result with two decimals. An absent operand is zero, which is what makes a key
// missing from a map read as zero rather than as an error.
func addDecimal(a, b Amount) Amount {
	if a.IsZero() {
		return normaliseDecimal(b)
	}
	if b.IsZero() {
		return normaliseDecimal(a)
	}
	negA, negB := a.IsNegative(), b.IsNegative()
	wholeA, fracA := splitDecimal(a)
	wholeB, fracB := splitDecimal(b)
	// Same sign: magnitudes add, and the hundredths carry into the whole part.
	// Two hundredths sum to at most 198, so one carry always settles it.
	if negA == negB {
		w, f := wholeA+wholeB, fracA+fracB
		if f >= 100 {
			w, f = w+1, f-100
		}
		out := fmt.Sprintf("%d.%02d", w, f)
		if negA {
			out = "-" + out
		}
		return Amount(out)
	}
	// Opposite signs: the smaller magnitude comes off the larger, so the sign of
	// the result is the sign of whichever operand had the larger magnitude — not
	// the sign of the first one, which is what makes 1.00 - 10.00 come out
	// -9.00.
	if wholeA*100+fracA >= wholeB*100+fracB {
		return renderDifference(wholeA-wholeB, fracA-fracB, negA)
	}
	return renderDifference(wholeB-wholeA, fracB-fracA, negB)
}

// renderDifference finishes an opposite-sign subtraction from two magnitudes
// already known to be in the order first-minus-second, borrowing out of the
// whole part when the fraction goes short. An exact zero renders as "0.00" and
// never as "-0.00": a sign on zero would make IsNegative disagree with the
// value's own magnitude, and a map carrying "-0.00" would colour a figure that
// is not in deficit.
func renderDifference(whole, frac int64, neg bool) Amount {
	if frac < 0 {
		whole, frac = whole-1, frac+100
	}
	if whole == 0 && frac == 0 {
		return "0.00"
	}
	if neg {
		return Amount(fmt.Sprintf("-%d.%02d", whole, frac))
	}
	return Amount(fmt.Sprintf("%d.%02d", whole, frac))
}

// splitDecimal separates a decimal amount into its whole and hundredths parts,
// discarding the sign. It reports zero for an absent amount. A leading "+" is
// treated as a sign, not as a digit, exactly as Amount.Display does with the
// same text: read as a non-digit it would yield 0 and silently discard the
// amount, and a quoted wire value carrying one is decodable (Amount's decoder
// accepts it).
func splitDecimal(a Amount) (whole, frac int64) {
	s := strings.TrimSpace(a.String())
	if len(s) > 0 && (s[0] == '-' || s[0] == '+') {
		s = s[1:]
	}
	if s == "" {
		return 0, 0
	}
	w, f, hasFrac := strings.Cut(s, ".")
	whole = atoi64(w)
	if hasFrac {
		switch {
		case len(f) == 0:
			frac = 0
		case len(f) == 1:
			frac = atoi64(f) * 10
		default:
			// Beyond the second decimal is not read: the API never sends more than
			// two, and rounding a third digit here would invent a value.
			frac = atoi64(f[:2])
		}
	}
	return whole, frac
}

// normaliseDecimal renders an amount with exactly two decimals, so a sum is
// never the ragged "5.5" a raw operand happened to carry, and the text a
// comparison sees is the same shape the server sends.
func normaliseDecimal(a Amount) Amount {
	if a.IsZero() {
		return "0.00"
	}
	w, f := splitDecimal(a)
	if a.IsNegative() && (w != 0 || f != 0) {
		return Amount(fmt.Sprintf("-%d.%02d", w, f))
	}
	return Amount(fmt.Sprintf("%d.%02d", w, f))
}

// atoi64 parses a run of ASCII digits, reporting zero for anything else rather
// than failing: every call site here is on a display path, and a malformed
// amount must not take a render down with it. The API's MaxMinorUnits bound
// keeps a value the server can send inside int64, so the accumulation does not
// wrap; a longer run than that would wrap rather than panic, which on a display
// path is the same shape of wrong answer as reading zero.
func atoi64(s string) int64 {
	var n int64
	for i := range len(s) {
		if s[i] < '0' || s[i] > '9' {
			return n
		}
		n = n*10 + int64(s[i]-'0')
	}
	return n
}
