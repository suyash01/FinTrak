package api

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

// TestTransactionQueryParamIsSent pins that the query language reaches the API
// as a single q= parameter carrying the expression verbatim, including the `>`
// in `amt>50` and the `:` in `cat:`.
func TestTransactionQueryParamIsSent(t *testing.T) {
	got := captureQuery(t, func(c *Client) error {
		_, err := c.ListTransactions(context.Background(), TransactionFilter{
			Query: "cat:8a3f amt>50",
		})
		return err
	}, "")

	if want := "cat:8a3f amt>50"; got.Get("q") != want {
		t.Errorf("q = %q, want %q", got.Get("q"), want)
	}
}

// TestTransactionQueryParamIsEncodedOnTheWire is the encoding half, kept apart
// from the value assertion so a failure says which half broke. A quoted value
// with a space must arrive encoded, and a `&` inside an expression must not be
// able to terminate the parameter and append another one.
func TestTransactionQueryParamIsEncodedOnTheWire(t *testing.T) {
	var raw string
	c := newStub(t, func(w http.ResponseWriter, r *http.Request) {
		raw = r.URL.RawQuery
		_, _ = w.Write([]byte(`{"data":[],"total":0,"page":1,"limit":50,"pages":0}`))
	})
	if _, err := c.ListTransactions(context.Background(), TransactionFilter{
		Query: `payee:"Whole Foods" tag:a&b`,
	}); err != nil {
		t.Fatalf("ListTransactions: %v", err)
	}
	if strings.Contains(raw, "Whole Foods") {
		t.Errorf("raw query %q contains an unencoded space", raw)
	}
	if strings.Contains(raw, "a&b") {
		t.Errorf("raw query %q contains an unencoded &, which would append a parameter", raw)
	}
	// Exactly one parameter must have been produced.
	if strings.Count(raw, "&") != 0 {
		t.Errorf("raw query %q should be a single parameter, got %d separators", raw, strings.Count(raw, "&"))
	}
}

// TestTransactionQueryParamIsOmittedWhenEmpty keeps an empty Query off the wire,
// so a caller that never uses the language sends exactly the same request as
// before it existed.
func TestTransactionQueryParamIsOmittedWhenEmpty(t *testing.T) {
	got := captureQuery(t, func(c *Client) error {
		_, err := c.ListTransactions(context.Background(), TransactionFilter{AccountID: "acct-1"})
		return err
	}, "")

	if _, present := got["q"]; present {
		t.Errorf("q was sent as %q for an empty Query; it must be omitted", got.Get("q"))
	}
}

// TestTransactionQuerySetter pins the fluent setter the other filter builders
// follow: it mutates in place and returns the same filter for chaining.
func TestTransactionQuerySetter(t *testing.T) {
	var f TransactionFilter
	got := f.SetQuery("amt>50")
	if got != &f {
		t.Error("SetQuery should return the receiver for chaining")
	}
	if f.Query != "amt>50" {
		t.Errorf("Query = %q, want %q", f.Query, "amt>50")
	}
}

// TestTransactionQueryComposesWithTheOtherFilters: q= is AND-ed with the
// existing parameters, so both must be on the wire at once.
func TestTransactionQueryComposesWithTheOtherFilters(t *testing.T) {
	linked := false
	got := captureQuery(t, func(c *Client) error {
		_, err := c.ListTransactions(context.Background(), TransactionFilter{
			CategoryID: UncategorizedCategory,
			Linked:     &linked,
			Query:      "amt>50",
		})
		return err
	}, "")

	for name, want := range map[string]string{
		"categoryId": UncategorizedCategory,
		"linked":     "false",
		"q":          "amt>50",
	} {
		if got.Get(name) != want {
			t.Errorf("%s = %q, want %q", name, got.Get(name), want)
		}
	}
}
