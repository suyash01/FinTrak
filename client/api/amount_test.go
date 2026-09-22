package api

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestAmountDisplay(t *testing.T) {
	tests := []struct {
		amount Amount
		want   string
	}{
		{Amount("1250.50"), "1,250.50"},
		{Amount("0.00"), "0.00"},
		{Amount("-0.05"), "-0.05"},
		{Amount("-1234567.89"), "-1,234,567.89"},
		{Amount("999.99"), "999.99"},
		{Amount("1000"), "1,000.00"},
		{Amount(""), "0.00"},
	}
	for _, tc := range tests {
		if got := tc.amount.Display(); got != tc.want {
			t.Errorf("Amount(%q).Display() = %q, want %q", tc.amount, got, tc.want)
		}
	}
}

func TestAmountSignAndAbs(t *testing.T) {
	if !Amount("-1.00").IsNegative() {
		t.Error("-1.00 should read as negative")
	}
	if Amount("1.00").IsNegative() {
		t.Error("1.00 should not read as negative")
	}
	if !Amount("0.00").IsZero() || !Amount("").IsZero() {
		t.Error("0.00 and the empty amount are both zero")
	}
	if got := Amount("-12.34").Abs(); got != "12.34" {
		t.Errorf("Abs() = %q", got)
	}
}

// TestParseAmountMirrorsTheBackendGrammar pins the rules the API enforces
// (money.Parse in backend/internal/money/money.go) so the TUI rejects exactly
// what the server would reject, and normalizes to two decimals either way. The
// valid and invalid tables mirror that package's own TestParse, so a change on
// either side of the boundary fails here.
func TestParseAmountMirrorsTheBackendGrammar(t *testing.T) {
	valid := map[string]string{
		"5":       "5.00",
		"5.5":     "5.50",
		"5.55":    "5.55",
		" 5.55 ":  "5.55",
		"+5.55":   "5.55",
		"-5.55":   "-5.55",
		"0":       "0.00",
		"-0":      "0.00",
		"-0.01":   "-0.01",
		"1000000": "1000000.00",
		// The integer part is rendered from the parsed digits, so a leading
		// zero run cannot reach the wire (JSON forbids it, and the body would
		// not parse even though the value itself is fine).
		"007":    "7.00",
		"0007.5": "7.50",
	}
	for in, want := range valid {
		got, err := ParseAmount(in)
		if err != nil {
			t.Errorf("ParseAmount(%q) errored: %v", in, err)
			continue
		}
		if got != Amount(want) {
			t.Errorf("ParseAmount(%q) = %q, want %q", in, got, want)
		}
	}

	// Re-pinned to the tightened grammar: ".5", "5." and "." used to be
	// accepted (as 0.50, 5.00 and 0.00), which meant the form accepted input
	// the API now rejects. The backend answers all of them with an error, so
	// the client has to as well.
	invalid := []string{
		"", "   ", "abc", "1.234", "-", "+", "1,000", "1.2.3", "--1", "1e3", "1e30", "$5",
		".5", "5.", ".", "+.", "-.", ".00",
		// A second sign is a typo, not a number: it is rejected here rather
		// than double-negated into an amount the user did not type.
		"+-1", "-+1",
		// Above MaxInt64 the whole part is not a number at all, exactly as on
		// the backend, where strconv.ParseInt fails before the bound is even
		// consulted.
		"99999999999999999999",
	}
	for _, in := range invalid {
		if got, err := ParseAmount(in); err == nil {
			t.Errorf("ParseAmount(%q) = %q, want an error", in, got)
		}
	}
}

// TestParseAmountBoundsTheValue covers the bound ParseAmount shares with
// money.MaxMinorUnits (1<<62 minor units): above it whole*100 overflows int64
// on the backend, so the API refuses the amount and the client must not send
// it. 4611686018427387904 cents is the largest value the backend accepts, so
// ...3879.04 is inside and ...3879.05 is not.
func TestParseAmountBoundsTheValue(t *testing.T) {
	for in, want := range map[string]string{
		"46116860184273878.99":  "46116860184273878.99",
		"46116860184273879.04":  "46116860184273879.04",  // exactly MaxMinorUnits
		"-46116860184273879.04": "-46116860184273879.04", // the bound is on the magnitude
	} {
		got, err := ParseAmount(in)
		if err != nil {
			t.Errorf("ParseAmount(%q) errored: %v", in, err)
			continue
		}
		if got != Amount(want) {
			t.Errorf("ParseAmount(%q) = %q, want %q", in, got, want)
		}
	}

	for _, in := range []string{
		"46116860184273879.05", // one cent above the bound
		"46116860184273880",    // one whole unit above it
		"-46116860184273880",   // the sign does not widen it
		"92233720368547758.08", // the backend's own overflow case
	} {
		got, err := ParseAmount(in)
		if err == nil {
			t.Errorf("ParseAmount(%q) = %q, want an out-of-range error", in, got)
			continue
		}
		if !strings.Contains(err.Error(), "out of range") {
			t.Errorf("ParseAmount(%q) error = %v, want it to name the range rather than read as a typo", in, err)
		}
	}
}

// TestAmountJSONRoundTrip checks the two boundary behaviours: the wire form is
// an unquoted JSON number (money.Amount marshals with MarshalJSON), and a value
// received is carried verbatim rather than recomputed.
func TestAmountJSONRoundTrip(t *testing.T) {
	type payload struct {
		Amount Amount `json:"amount"`
	}

	decoded := payload{}
	if err := json.Unmarshal([]byte(`{"amount":1250.5}`), &decoded); err != nil {
		t.Fatalf("unmarshal number: %v", err)
	}
	if decoded.Amount != "1250.5" {
		t.Errorf("amount = %q, want the raw text preserved", decoded.Amount)
	}

	quoted := payload{}
	if err := json.Unmarshal([]byte(`{"amount":"1250.50"}`), &quoted); err != nil {
		t.Fatalf("unmarshal quoted: %v", err)
	}
	if quoted.Amount != "1250.50" {
		t.Errorf("quoted amount = %q", quoted.Amount)
	}

	null := payload{}
	if err := json.Unmarshal([]byte(`{"amount":null}`), &null); err != nil {
		t.Fatalf("unmarshal null: %v", err)
	}
	if null.Amount != "" {
		t.Errorf("null amount = %q, want empty", null.Amount)
	}

	encoded, err := json.Marshal(payload{Amount: "-12.34"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(encoded) != `{"amount":-12.34}` {
		t.Errorf("marshal = %s, want an unquoted number", encoded)
	}

	encoded, err = json.Marshal(payload{})
	if err != nil {
		t.Fatalf("marshal empty: %v", err)
	}
	if string(encoded) != `{"amount":0}` {
		t.Errorf("marshal empty = %s, want 0", encoded)
	}
}

func TestAmountRejectsUnquotedGarbage(t *testing.T) {
	var a Amount
	if err := json.Unmarshal([]byte(`"nope"`), &a); err == nil {
		t.Error("a quoted non-number must be rejected")
	}
}
