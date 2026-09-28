package models

import (
	"encoding/json"
	"testing"

	"github.com/fintrak/backend/internal/money"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

func TestOptionalUUID(t *testing.T) {
	id := uuid.MustParse("11111111-1111-1111-1111-111111111111")

	var withValue, withNull, absent UpdateTransactionRequest
	assert.NoError(t, json.Unmarshal([]byte(`{"categoryId":"`+id.String()+`"}`), &withValue))
	assert.NoError(t, json.Unmarshal([]byte(`{"categoryId":null}`), &withNull))
	assert.NoError(t, json.Unmarshal([]byte(`{}`), &absent))

	assert.True(t, withValue.CategoryID.Set())
	assert.Equal(t, id, *withValue.CategoryID.Value())

	assert.True(t, withNull.CategoryID.Set())
	assert.Nil(t, withNull.CategoryID.Value())

	assert.False(t, absent.CategoryID.Set())
	assert.Nil(t, absent.CategoryID.Value())
}

func TestOptionalInt(t *testing.T) {
	var withValue, withNull, absent UpdateUserSettingsRequest
	assert.NoError(t, json.Unmarshal([]byte(`{"pageSize":25}`), &withValue))
	assert.NoError(t, json.Unmarshal([]byte(`{"pageSize":null}`), &withNull))
	assert.NoError(t, json.Unmarshal([]byte(`{}`), &absent))

	assert.True(t, withValue.PageSize.Set())
	assert.Equal(t, 25, *withValue.PageSize.Value())

	assert.True(t, withNull.PageSize.Set())
	assert.Nil(t, withNull.PageSize.Value())

	assert.False(t, absent.PageSize.Set())
	assert.Nil(t, absent.PageSize.Value())
}

func TestOptionalUUIDNilReceiver(t *testing.T) {
	var o *OptionalUUID
	assert.False(t, o.Set())
	assert.Nil(t, o.Value())
}

func TestOptionalIntNilReceiver(t *testing.T) {
	var o *OptionalInt
	assert.False(t, o.Set())
	assert.Nil(t, o.Value())
}

func TestCurrencyAmountsSingleRefusesMoreThanOneCurrency(t *testing.T) {
	m := CurrencyAmounts{"INR": 500000, "USD": 12000}

	if _, _, ok := m.Single(); ok {
		t.Error("a two-currency map reported a single amount")
	}

	code, value, ok := CurrencyAmounts{"INR": 500000}.Single()
	if !ok || code != "INR" || value != 500000 {
		t.Errorf("Single() = %q, %d, %t; want \"INR\", 500000, true", code, value, ok)
	}

	if _, _, ok := (CurrencyAmounts{}).Single(); ok {
		t.Error("an empty map reported a single amount")
	}
	var nilMap CurrencyAmounts
	if _, _, ok := nilMap.Single(); ok {
		t.Error("a nil map reported a single amount")
	}
}

func TestCurrencyAmountsSubTreatsAMissingKeyAsZero(t *testing.T) {
	// A foreign account that only ever spends: the income map has no key for
	// it, and the difference must still name it rather than drop it.
	got := CurrencyAmounts{"INR": 500000}.Sub(CurrencyAmounts{"INR": 200000, "USD": 8000})

	if len(got) != 2 {
		t.Fatalf("Sub returned %d keys, want 2: %v", len(got), got)
	}
	if got["INR"] != 300000 {
		t.Errorf("INR = %d, want 300000", got["INR"])
	}
	if got["USD"] != -8000 {
		t.Errorf("USD = %d, want -8000", got["USD"])
	}
}

func TestCurrencyAmountsAddAllocatesFromNil(t *testing.T) {
	var m CurrencyAmounts
	m = m.Add("INR", 100)
	m = m.Add("INR", 250)
	m = m.Add("USD", 700)

	if m["INR"] != 350 || m["USD"] != 700 {
		t.Errorf("folded to %v, want INR 350 and USD 700", m)
	}
}

func TestCurrencyAmountsAddSkipsAZeroContribution(t *testing.T) {
	// A missing key must read as zero, so a currency with no money in it is
	// absent rather than present-and-zero. That is what makes len() mean "how
	// many currencies this aggregate touched" — every account's currency
	// appearing in every total even with nothing in it would make it
	// meaningless — and it is the asymmetry Sub relies on for an account that
	// only ever spends: it contributes a key to the expense side and none to
	// the income side, and Sub still produces its negative net.
	m := NewCurrencyAmounts()
	m = m.Add("INR", 0)
	if len(m) != 0 {
		t.Errorf("a zero contribution created the key %v", m)
	}

	m = m.Add("INR", money.FromFloat(80))
	m = m.Add("USD", money.FromFloat(80))
	if got := m.Currencies(); len(got) != 2 || got[0] != "INR" || got[1] != "USD" {
		t.Errorf("Currencies() = %v, want [INR USD]", got)
	}
}

func TestNewCurrencyAmountsIsNonNil(t *testing.T) {
	// The constructor's whole purpose is that a handler can write to the result
	// without checking first, so "empty" must not mean "nil".
	m := NewCurrencyAmounts()
	if m == nil {
		t.Fatal("NewCurrencyAmounts() returned a nil map")
	}
	if len(m) != 0 {
		t.Errorf("NewCurrencyAmounts() = %v, want empty", m)
	}
}

// TestDashboardSummaryAsOfJSON pins the wire shape of the as-of response. The
// pointer requirement lives here: a summary for a caller that did not ask for an
// asOf must serialise exactly as it did before these fields existed, which a
// bare []AccountBalance with omitempty cannot do (encoding/json does not omit
// an empty slice, so it would emit "balances": [] on every ordinary load).
func TestDashboardSummaryAsOfJSON(t *testing.T) {
	t.Run("absent when the request did not ask", func(t *testing.T) {
		body, err := json.Marshal(DashboardSummary{})
		if err != nil {
			t.Fatalf("Marshal: %v", err)
		}
		var generic map[string]any
		if err := json.Unmarshal(body, &generic); err != nil {
			t.Fatalf("Unmarshal: %v", err)
		}
		if _, ok := generic["asOf"]; ok {
			t.Errorf("summary without an asOf request carries an asOf key: %s", body)
		}
		if _, ok := generic["balances"]; ok {
			t.Errorf("summary without an asOf request carries a balances key: %s", body)
		}
	})

	t.Run("present and per-currency when it did", func(t *testing.T) {
		asOf := "2026-03-01"
		balances := []AccountBalance{{
			ID:       uuid.MustParse("22222222-2222-2222-2222-222222222222"),
			Name:     "Savings",
			Currency: "INR",
			Balance:  CurrencyAmounts{"INR": 123400},
		}}
		body, err := json.Marshal(DashboardSummary{AsOf: &asOf, Balances: &balances})
		if err != nil {
			t.Fatalf("Marshal: %v", err)
		}
		var generic struct {
			AsOf     *string `json:"asOf"`
			Balances []struct {
				ID       string         `json:"id"`
				Name     string         `json:"name"`
				Currency string         `json:"currency"`
				Balance  map[string]any `json:"balance"`
			} `json:"balances"`
		}
		if err := json.Unmarshal(body, &generic); err != nil {
			t.Fatalf("Unmarshal: %v", err)
		}
		if generic.AsOf == nil || *generic.AsOf != asOf {
			t.Fatalf("asOf = %v, want %q (body: %s)", generic.AsOf, asOf, body)
		}
		if len(generic.Balances) != 1 {
			t.Fatalf("balances = %v, want one account (body: %s)", generic.Balances, body)
		}
		b := generic.Balances[0]
		if b.Currency != "INR" {
			t.Errorf("currency = %q, want INR", b.Currency)
		}
		// A bare number here would be the collapse CurrencyAmounts exists to
		// refuse, so assert the object shape rather than a parsed value.
		if got, ok := b.Balance["INR"]; !ok {
			t.Errorf("balance has no INR key, want the per-currency map: %v", b.Balance)
		} else if _, isNumber := got.(float64); !isNumber {
			t.Errorf("balance[INR] = %T, want a number inside the map", got)
		}
	})

	t.Run("an empty slice is still an answer", func(t *testing.T) {
		asOf := "2026-03-01"
		empty := []AccountBalance{}
		body, err := json.Marshal(DashboardSummary{AsOf: &asOf, Balances: &empty})
		if err != nil {
			t.Fatalf("Marshal: %v", err)
		}
		var generic map[string]any
		if err := json.Unmarshal(body, &generic); err != nil {
			t.Fatalf("Unmarshal: %v", err)
		}
		// "no accounts" and "no account held anything at that date" are different
		// answers; only the second is true of an asOf response, so the key must
		// survive an empty slice.
		balances, ok := generic["balances"]
		if !ok {
			t.Fatalf("an asOf response with no balances lost the key: %s", body)
		}
		list, ok := balances.([]any)
		if !ok || len(list) != 0 {
			t.Errorf("balances = %v, want an empty array", balances)
		}
	})
}

func TestCurrencyAmountsCurrenciesIsSorted(t *testing.T) {
	got := CurrencyAmounts{"USD": 1, "INR": 2, "EUR": 3}.Currencies()

	want := []string{"EUR", "INR", "USD"}
	if len(got) != len(want) {
		t.Fatalf("Currencies() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Currencies() = %v, want %v (map order must never reach a UI)", got, want)
		}
	}
}
