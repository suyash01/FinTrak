package api

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"testing"
)

// captureQuery runs a request and returns the query the server actually
// received, so the filter grammar is asserted on the wire rather than on an
// intermediate value.
func captureQuery(t *testing.T, invoke func(c *Client) error, body string) url.Values {
	t.Helper()
	if body == "" {
		body = `{"data":[],"total":0,"page":1,"limit":50,"pages":0}`
	}
	var got url.Values
	c := newStub(t, func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.Query()
		_, _ = w.Write([]byte(body))
	})
	if err := invoke(c); err != nil {
		t.Fatalf("request failed: %v", err)
	}
	return got
}

// TestTransactionFilterGrammar pins the parameter names and encodings the
// backend's txnQueryFilter expects, including the three that are easy to get
// wrong: the "uncategorized"/"none" sentinels, the comma-joined tags set, and
// the tri-state linked flag which must send an explicit "false".
func TestTransactionFilterGrammar(t *testing.T) {
	linked := false
	filter := TransactionFilter{
		AccountID:       "acct-1",
		CategoryID:      UncategorizedCategory,
		GroupID:         "expense",
		Search:          "netflix",
		Type:            "debit",
		PayeeID:         NoPayee,
		Amount:          "-499.00",
		DateFrom:        "2026-01-01",
		DateTo:          "2026-03-31",
		Tags:            []string{"work", "reimbursable"},
		Linked:          &linked,
		Uncategorized:   false,
		LoanAccountID:   "loan-1",
		ExcludeAttached: true,
		RecurringID:     "series-1",
		Recurring:       "unlinked",
		SortBy:          "amount",
		SortOrder:       "ASC",
		Page:            2,
		Limit:           100,
	}

	got := captureQuery(t, func(c *Client) error {
		_, err := c.ListTransactions(context.Background(), filter)
		return err
	}, "")

	want := map[string]string{
		"accountId":       "acct-1",
		"categoryId":      "uncategorized",
		"groupId":         "expense",
		"search":          "netflix",
		"type":            "debit",
		"payeeId":         "none",
		"amount":          "-499.00",
		"dateFrom":        "2026-01-01",
		"dateTo":          "2026-03-31",
		"tags":            "work,reimbursable",
		"linked":          "false",
		"excludeAttached": "true",
		"loanAccountId":   "loan-1",
		"recurringId":     "series-1",
		"recurring":       "unlinked",
		"sortBy":          "amount",
		"sortOrder":       "ASC",
		"page":            "2",
		"limit":           "100",
	}
	for key, wantValue := range want {
		if gotValue := got.Get(key); gotValue != wantValue {
			t.Errorf("%s = %q, want %q", key, gotValue, wantValue)
		}
	}
	if got.Has("uncategorized") {
		t.Error("uncategorized should be absent when false")
	}
}

func TestTransactionFilterOmitsEmptyValues(t *testing.T) {
	got := captureQuery(t, func(c *Client) error {
		_, err := c.ListTransactions(context.Background(), TransactionFilter{})
		return err
	}, "")

	for _, key := range []string{"accountId", "categoryId", "search", "tags", "linked", "page", "limit", "uncategorized"} {
		if got.Has(key) {
			t.Errorf("%s must be omitted when unset (server defaults apply)", key)
		}
	}
}

// TestExportSharesFiltersButNotPaging covers the one documented difference
// between the list and the CSV export: the export is always date-descending and
// takes no paging or sort parameters.
func TestExportSharesFiltersButNotPaging(t *testing.T) {
	var got url.Values
	c := newStub(t, func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.Query()
		w.Header().Set("Content-Type", "text/csv")
		w.Header().Set("Content-Disposition", `attachment; filename="fintrak_transactions.csv"`)
		_, _ = w.Write([]byte("Date,Description\n"))
	})

	filter := TransactionFilter{
		AccountID: "acct-1",
		Search:    "coffee",
		Tags:      []string{"work"},
		SortBy:    "amount",
		SortOrder: "ASC",
		Page:      3,
		Limit:     10,
	}
	name, err := c.ExportTransactionsCSV(context.Background(), filter, io.Discard)
	if err != nil {
		t.Fatalf("ExportTransactionsCSV: %v", err)
	}
	if name != "fintrak_transactions.csv" {
		t.Errorf("filename = %q, want the server-suggested name", name)
	}
	if got.Get("accountId") != "acct-1" || got.Get("search") != "coffee" || got.Get("tags") != "work" {
		t.Errorf("export dropped filters: %v", got)
	}
	for _, key := range []string{"page", "limit", "sortBy", "sortOrder"} {
		if got.Has(key) {
			t.Errorf("export must not send %s", key)
		}
	}
}

// TestDownloadSurfacesTheErrorEnvelope makes sure a failed download (a 409 from
// the restore endpoint, say) is reported as an API error rather than written to
// the output file.
func TestDownloadSurfacesTheErrorEnvelope(t *testing.T) {
	c := newStub(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"errors":[{"message":"target user already has accounts"}]}`))
	})

	var written []byte
	_, err := c.download(context.Background(), get("/export"), writerFunc(func(b []byte) (int, error) {
		written = append(written, b...)
		return len(b), nil
	}))
	if err == nil {
		t.Fatal("expected an error")
	}
	if len(written) != 0 {
		t.Errorf("error body must not be written to the sink, got %q", written)
	}
	if !StatusIs(err, http.StatusConflict) {
		t.Errorf("status not preserved: %v", err)
	}
}

type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(b []byte) (int, error) { return f(b) }
