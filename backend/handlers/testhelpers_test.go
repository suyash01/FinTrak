package handlers

import (
	"testing"

	"github.com/fintrak/backend/db"
	"github.com/pashagolub/pgxmock/v5"
)

// testParserURL is a sentinel parser base URL for handler tests that don't
// exercise the parser.
const testParserURL = "http://parser.test"

// txnListCols is the column set returned by GetTransactions, kept in one place
// so adding a joined column doesn't churn every transaction-list test.
var txnListCols = []string{
	"id", "account_id", "date", "description", "amount", "type", "category_id",
	"tags", "notes", "payee_id", "payee", "created_at", "account_name",
	"category_name", "category_icon", "category_color", "is_linked",
	"billing_cycle_id", "billing_cycle_label", "loan_account_id", "loan_account_name",
	"recurring_series_id", "recurring_series_name",
}

// txnListRow builds a GetTransactions row from its 21 base values, filling the
// recurring-linkage columns with "not linked".
func txnListRow(values ...any) *pgxmock.Rows {
	return pgxmock.NewRows(txnListCols).AddRow(append(values, nil, "")...)
}

// newTestServer builds a Server over the given pool for tests. Handlers read
// their dependencies from the Server, so each test owns an isolated instance
// instead of swapping a package global.
func newTestServer(pool db.DBPool) *Server {
	return NewServer(pool, testParserURL, 0)
}

// newMockServer creates a mock pool and a Server over it, closing the mock when
// the test finishes.
func newMockServer(t *testing.T) (*Server, pgxmock.PgxPoolIface) {
	t.Helper()
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(mock.Close)
	return newTestServer(mock), mock
}
