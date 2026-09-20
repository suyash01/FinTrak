package api

import (
	"encoding/json"
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
// what the server would reject, and normalizes to two decimals either way.
func TestParseAmountMirrorsTheBackendGrammar(t *testing.T) {
	valid := map[string]string{
		"5":       "5.00",
		"5.5":     "5.50",
		"5.55":    "5.55",
		" 5.55 ":  "5.55",
		"+5.55":   "5.55",
		"-5.55":   "-5.55",
		".5":      "0.50",
		"0":       "0.00",
		"-0":      "0.00",
		"1000000": "1000000.00",
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

	invalid := []string{"", "   ", "abc", "1.234", "-", "+", "1,000", "1.2.3", "--1", "1e3", "$5"}
	for _, in := range invalid {
		if got, err := ParseAmount(in); err == nil {
			t.Errorf("ParseAmount(%q) = %q, want an error", in, got)
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
