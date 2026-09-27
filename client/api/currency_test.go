package api

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCurrencyAmountsDisplayRefusesToChoose(t *testing.T) {
	// One currency: the formatted value, named.
	got := CurrencyAmounts{"INR": "5000.00"}.Display()
	if got != "INR 5,000.00" {
		t.Errorf("Display() = %q, want %q", got, "INR 5,000.00")
	}

	// Two: both, and an explicit statement that they were not added.
	multi := CurrencyAmounts{"USD": "120.00", "INR": "5000.00"}.Display()
	for _, want := range []string{"2 currencies", "INR", "5,000.00", "USD", "120.00"} {
		if !strings.Contains(multi, want) {
			t.Errorf("Display() = %q, missing %q", multi, want)
		}
	}
	// Sorted, and nothing but the two named figures: a terminal has no room for
	// two currencies, so this is the form that has to say so rather than pick.
	if want := "2 currencies: INR 5,000.00, USD 120.00"; multi != want {
		t.Errorf("Display() = %q, want %q", multi, want)
	}
	// The original bug in one assertion: a window over a USD and an INR account
	// that rendered as one number. 5,000.00 + 120.00 is 5,120.00.
	if strings.Contains(multi, "5,120") {
		t.Errorf("Display() = %q added two currencies together", multi)
	}

	// Nothing in scope is a statement, not a rendering of nothing.
	if got := CurrencyAmounts(nil).Display(); got != "no currency" {
		t.Errorf("Display() on an empty map = %q, want %q", got, "no currency")
	}
}

func TestCurrencyAmountsSingleAndSub(t *testing.T) {
	if _, _, ok := (CurrencyAmounts{"INR": "1", "USD": "2"}).Single(); ok {
		t.Error("a two-currency map reported a single amount")
	}
	code, value, ok := CurrencyAmounts{"INR": "1.50"}.Single()
	if !ok || code != "INR" || value != "1.50" {
		t.Errorf("Single() = %q, %q, %t", code, value, ok)
	}

	// Sub is exact decimal text arithmetic, not float: Amount carries the
	// server's decimal and must not be recomputed through a float64.
	got := CurrencyAmounts{"INR": "0.30"}.Sub(CurrencyAmounts{"INR": "0.10"})
	if got["INR"] != "0.20" {
		t.Errorf("Sub = %q, want 0.20", got["INR"])
	}
}

func TestCurrencyAmountsSubUnionOfKeys(t *testing.T) {
	got := CurrencyAmounts{"INR": "5.00"}.Sub(CurrencyAmounts{"INR": "1.00", "USD": "2.00"})

	if got["INR"] != "4.00" {
		t.Errorf("INR = %q, want 4.00", got["INR"])
	}
	// A currency that only ever spends must come out negative, not vanish: a
	// foreign account with expenses and no income is the case the union is for.
	if got["USD"] != "-2.00" {
		t.Errorf("USD = %q, want -2.00", got["USD"])
	}
}

func TestCurrencyAmountsSubKeepsAKeyOnlyTheSubtrahendHolds(t *testing.T) {
	// The mirror image: a currency in the second operand alone. Dropping it would
	// leave a map that claims the window spent nothing in it.
	got := CurrencyAmounts{"INR": "1.00"}.Sub(CurrencyAmounts{"EUR": "3.50"})

	if got["EUR"] != "-3.50" {
		t.Errorf("EUR = %q, want -3.50", got["EUR"])
	}
	if got["INR"] != "1.00" {
		t.Errorf("INR = %q, want 1.00", got["INR"])
	}
	if len(got.Currencies()) != 2 {
		t.Errorf("Currencies() = %v, want both keys", got.Currencies())
	}
}

func TestCurrencyAmountsCurrenciesIsSorted(t *testing.T) {
	got := CurrencyAmounts{"USD": "1", "INR": "2", "EUR": "3"}.Currencies()
	want := []string{"EUR", "INR", "USD"}

	if len(got) != 3 || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Errorf("Currencies() = %v, want %v", got, want)
	}
}

func TestCurrencyAmountsAddFoldsOneCurrencyAtATime(t *testing.T) {
	// A nil map is the zero value a decoded-but-absent field arrives as, so Add
	// has to be usable on one.
	var m CurrencyAmounts
	m = m.Add("INR", "5.00")
	m = m.Add("INR", "0.50")
	m = m.Add("USD", "1.00")

	if m["INR"] != "5.50" {
		t.Errorf("INR = %q, want 5.50", m["INR"])
	}
	if m["USD"] != "1.00" {
		t.Errorf("USD = %q, want 1.00", m["USD"])
	}
	// Two keys is what Single refuses, and it must stay refused: Add never merges
	// one currency into another.
	if _, _, ok := m.Single(); ok {
		t.Error("a two-currency fold reported a single amount")
	}
}

func TestCurrencyAmountsIsNegativeNeedsEveryCurrency(t *testing.T) {
	// One sign to report, so one currency decides it.
	only := CurrencyAmounts{"INR": "-1.00"}
	if !only.IsNegative() {
		t.Error("a single negative currency was not negative")
	}
	positive := CurrencyAmounts{"INR": "1.00"}
	if positive.IsNegative() {
		t.Error("a positive currency was negative")
	}
	// Negative in one currency and positive in another is neither: there is no
	// sign for a caller to colour a figure by.
	mixed := CurrencyAmounts{"INR": "-1.00", "USD": "2.00"}
	if mixed.IsNegative() {
		t.Error("a map that is negative in one currency and positive in another was negative")
	}
	// An empty map has no sign either: absent is not negative. The parentheses
	// are what Go needs to read a composite literal in an if header.
	if empty := (CurrencyAmounts{}); empty.IsNegative() {
		t.Error("an empty map was negative")
	}
}

func TestCurrencyAmountsEmptyAndNilAreSafe(t *testing.T) {
	// The API promises a non-nil map on the wire, but a client must not panic or
	// invent a figure if one is ever absent, and none of these may claim a value.
	for name, m := range map[string]CurrencyAmounts{"nil": nil, "empty": {}} {
		if _, _, ok := m.Single(); ok {
			t.Errorf("%s: Single() reported an amount", name)
		}
		if got := len(m.Currencies()); got != 0 {
			t.Errorf("%s: Currencies() returned %d codes", name, got)
		}
		if got := m.Display(); got != "no currency" {
			t.Errorf("%s: Display() = %q, want %q", name, got, "no currency")
		}
		if m.IsNegative() {
			t.Errorf("%s: IsNegative() was true", name)
		}
	}
}

// TestAddDecimalIsExactWhereAFloatWouldNot pins the property the whole type
// rests on: the arithmetic is integer hundredths, and no value is ever routed
// through a float64. Amount carries the server's decimal text, and a float64 has
// neither the precision nor the exactness to reproduce it.
func TestAddDecimalIsExactWhereAFloatWouldNot(t *testing.T) {
	cases := []struct {
		name string
		a, b Amount
		want Amount
	}{
		// 2^53+1 is not representable as a float64, so a client that recomputed
		// this through one would lose the .01 entirely; the decimal text is exact
		// and the result keeps both decimals.
		{"magnitude above 2^53", "9007199254740993.01", "9007199254740993.01", "18014398509481986.02"},
		// The cents a float cannot represent at all below 1.0, and the carry that
		// a naive hundredths addition would get wrong.
		{"tenths", "0.30", "0.10", "0.40"},
		{"carry into the whole part", "0.99", "0.01", "1.00"},
		{"carry out of the fraction", "0.99", "0.99", "1.98"},
		// A zero operand leaves the other one alone but canonicalised: a key the
		// map carried with a zero in it must not turn the sum into 0.00.
		{"zero second operand", "5.00", "0.00", "5.00"},
		{"negatives add as negatives", "-1.00", "-2.00", "-3.00"},
		{"difference of signs", "1.00", "-2.00", "-1.00"},
		// A leading plus is a sign, not a digit: read as a digit it is 1.50, and
		// read as a non-digit it is 0.00 and the amount is quietly discarded.
		{"explicit plus", "+1.50", "1.50", "3.00"},
		{"surrounding space", " 1.50 ", "0.25", "1.75"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := addDecimal(tc.a, tc.b); got != tc.want {
				t.Errorf("addDecimal(%q, %q) = %q, want %q", tc.a, tc.b, got, tc.want)
			}
		})
	}
}

// TestCurrencyAmountsSubIsExactDecimalArithmetic drives the difference through
// Sub, because that is the only way a caller reaches a subtraction, and pins the
// cases where a float64 or a naive hundredths subtraction would be wrong: the
// hundredths a float cannot represent, the two-decimal difference below its
// precision at a large magnitude, and a result that lands exactly on zero.
func TestCurrencyAmountsSubIsExactDecimalArithmetic(t *testing.T) {
	cases := []struct {
		name     string
		a, b     Amount
		want     Amount
		negative bool
	}{
		{"tenths", "0.30", "0.10", "0.20", false},
		// 9007199254740993.00 is 2^53+1: a float64 round-trip lands on
		// 9007199254740992.00, and the difference a float would report is nothing
		// at all rather than a cent.
		{"below float precision", "9007199254740993.01", "9007199254740993.00", "0.01", false},
		{"exact zero", "1.00", "1.00", "0.00", false},
		{"zero stays unsigned", "0.00", "0.00", "0.00", false},
		{"borrow across the whole part", "2.50", "1.75", "0.75", false},
		{"borrow out of the whole part", "1.00", "1.10", "-0.10", true},
		{"borrow stays in the fraction", "1.05", "1.00", "0.05", false},
		// The sign follows the larger magnitude, not the first operand: this is
		// the case a "prepend a minus to b and add" that fails to strip an
		// existing minus would report as 1.00 + 10.00.
		{"larger subtrahend keeps its sign", "1.00", "10.00", "-9.00", true},
		// The subtrahend's sign is read, not assumed: a leading plus is a sign
		// here too, or this would add 2.00 instead of taking it off.
		{"explicit plus subtrahend", "1.00", "+2.00", "-1.00", true},
		{"larger minuend keeps its sign", "10.00", "1.00", "9.00", false},
		// A negative subtrahend has its sign flipped, so the two add. A net is
		// negative whenever a window spends more than it takes in, so comparing
		// two nets reaches this.
		{"negative subtrahend adds", "1.00", "-4.00", "5.00", false},
		{"negative minuend subtracts", "-4.00", "1.00", "-5.00", true},
		{"both negative", "-1.00", "-2.00", "1.00", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := CurrencyAmounts{"INR": tc.a}.Sub(CurrencyAmounts{"INR": tc.b})
			if got["INR"] != tc.want {
				t.Errorf("%q - %q = %q, want %q", tc.a, tc.b, got["INR"], tc.want)
			}
			// The sign IsNegative reports is read off this text, so a difference
			// has to carry the one its magnitude says it has — and an exact zero
			// has to carry none.
			if tc.negative != got.IsNegative() {
				t.Errorf("%q - %q: IsNegative() = %t, want %t", tc.a, tc.b, got.IsNegative(), tc.negative)
			}
		})
	}
}

// TestCurrencyAmountsRoundTripsAsAnObject pins the shape the API promises for an
// empty aggregate: {} and never null, so a response covering no accounts reads
// the same as one whose accounts merely spent nothing, and re-encoding a decoded
// response (a bundle, a cache) cannot turn an absent map into a JSON null that
// every reader then has to special-case.
func TestCurrencyAmountsRoundTripsAsAnObject(t *testing.T) {
	body := `{"days":[],"markers":[],"cycles":[],"totalIncome":{},"totalExpense":{},"net":{},"maxAbsNet":{},` +
		`"currencyScope":{"currencies":[],"accounts":[]}}`
	var cal CashFlowCalendar
	if err := json.Unmarshal([]byte(body), &cal); err != nil {
		t.Fatalf("unmarshalling: %v", err)
	}
	for name, amounts := range map[string]CurrencyAmounts{
		"totalIncome": cal.TotalIncome, "totalExpense": cal.TotalExpense,
		"net": cal.Net, "maxAbsNet": cal.MaxAbsNet,
	} {
		if amounts == nil {
			t.Errorf("%s decoded to a nil map, so it would re-encode as null", name)
		}
	}
	out, err := json.Marshal(cal)
	if err != nil {
		t.Fatalf("marshalling: %v", err)
	}
	for _, field := range []string{`"totalIncome":{}`, `"totalExpense":{}`, `"net":{}`, `"maxAbsNet":{}`} {
		if !strings.Contains(string(out), field) {
			t.Errorf("re-encoded as %s, missing %s", out, field)
		}
	}
}

func TestNormaliseDecimalRendersTwoDecimals(t *testing.T) {
	cases := []struct {
		in   Amount
		want Amount
	}{
		{"", "0.00"},
		{"0", "0.00"},
		{"5", "5.00"},
		{"5.5", "5.50"},
		{"-5.5", "-5.50"},
		{"-0.00", "0.00"},
		{"1234.567", "1234.56"},
	}
	for _, tc := range cases {
		if got := normaliseDecimal(tc.in); got != tc.want {
			t.Errorf("normaliseDecimal(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestSplitDecimalDiscardsTheSign(t *testing.T) {
	cases := []struct {
		in          Amount
		whole, frac int64
	}{
		{"", 0, 0},
		{"0.05", 0, 5},
		{"-0.05", 0, 5},
		{"+0.05", 0, 5},
		{"-12.5", 12, 50},
		{"12", 12, 0},
		{"12.", 12, 0},
	}
	for _, tc := range cases {
		whole, frac := splitDecimal(tc.in)
		if whole != tc.whole || frac != tc.frac {
			t.Errorf("splitDecimal(%q) = %d, %d, want %d, %d", tc.in, whole, frac, tc.whole, tc.frac)
		}
	}
}
