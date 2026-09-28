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

// newGuardedPool builds a mock pool with the adjacent-placeholder guard installed.
//
// pgxmock has no package-wide default for a QueryMatcher: the matcher is only ever
// consulted by a pool built with QueryMatcherOption, so a guard that is correct
// and installed on one pool says nothing about the next pool. That is why this
// exists rather than a bare pgxmock.NewPool() at each site — see
// noAdjacentPlaceholders in dashboard_test.go for the defect it catches, and
// TestGuardedPoolRejectsADoubledPlaceholder for the proof it has teeth.
//
// Every test whose SQL contains one of the currency fragments should build its
// pool here rather than with pgxmock.NewPool(), because those fragments are the
// ones the guard exists for: they are assembled from a COALESCE expression plus a
// placeholder, which is exactly the shape that produced `= $4 $4` and a 500 from
// PostgreSQL on every request. A doubled placeholder is invisible to the regexp
// matcher these tests otherwise use — pgxmock's stripQuery collapses whitespace
// before an unanchored match, so `= $2` is found inside the malformed `= $2 $2`.
//
// Closing the pool is left to the caller, so this returns the same shape as
// pgxmock.NewPool and a test can still defer or register its own cleanup.
func newGuardedPool(t *testing.T) (pgxmock.PgxPoolIface, error) {
	t.Helper()
	return pgxmock.NewPool(pgxmock.QueryMatcherOption(pgxmock.QueryMatcherFunc(noAdjacentPlaceholders)))
}
