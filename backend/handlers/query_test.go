package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/fintrak/backend/internal/query"
	"github.com/google/uuid"
	"github.com/pashagolub/pgxmock/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGetTransactionsQueryParamIntersectsTheExistingParams is Review Focus #3:
// q= AND-s with the filter bar's selection, it never replaces it. Selecting a
// category and typing amt>50 must show only large transactions in that category,
// not one set or the other.
func TestGetTransactionsQueryParamIntersectsTheExistingParams(t *testing.T) {
	r, srv, mock := newTransactionTestRouter(t)
	r.GET("/transactions", srv.GetTransactions)

	userID := testUserID()
	categoryID := uuid.New().String()

	// Argument order proves the composition: the user id, then the existing
	// category parameter (bound as the string the query parameter carries),
	// then the query's amount in minor units, then paging.
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM transactions t WHERE t.user_id").
		WithArgs(userID, categoryID, int64(5000)).
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(1))
	mock.ExpectQuery("SELECT t.id, t.account_id").
		WithArgs(userID, categoryID, int64(5000), 50, 0).
		WillReturnRows(pgxmock.NewRows(txnListCols))

	req, _ := http.NewRequest("GET", "/transactions?categoryId="+categoryID+"&q=amt%3E50", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestGetTransactionsQueryOfPureGarbageReturnsEverythingAndSaysSo is Review
// Focus #1: a query the parser cannot understand must return 200 with the full
// ledger plus a diagnostic. It must not 400, and it must not look like a
// working filter — the user asked a question and got the whole ledger back.
func TestGetTransactionsQueryOfPureGarbageReturnsEverythingAndSaysSo(t *testing.T) {
	r, srv, mock := newTransactionTestRouter(t)
	r.GET("/transactions", srv.GetTransactions)

	userID := testUserID()

	// No predicate at all: the dropped term must not narrow anything, so the
	// count query takes the user id and nothing else.
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM transactions t WHERE t.user_id").
		WithArgs(userID).
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(1))
	mock.ExpectQuery("SELECT t.id, t.account_id").
		WithArgs(userID, 50, 0).
		WillReturnRows(pgxmock.NewRows(txnListCols))

	req, _ := http.NewRequest("GET", "/transactions?q=catgory%3Afood", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code, "a bad q must never fail the request")

	var body struct {
		Total            int                `json:"total"`
		QueryDiagnostics []query.Diagnostic `json:"queryDiagnostics"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, 1, body.Total, "a dropped term must not filter")
	require.Len(t, body.QueryDiagnostics, 1)
	assert.Equal(t, query.CodeUnknownField, body.QueryDiagnostics[0].Code)
	assert.Equal(t, "catgory:food", body.QueryDiagnostics[0].Term)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestGetTransactionsQueryDiagnosticsOmittedWhenEmpty keeps the key off the wire
// when nothing was ignored, so a caller can tell "nothing was dropped" without a
// null check.
func TestGetTransactionsQueryDiagnosticsOmittedWhenEmpty(t *testing.T) {
	r, srv, mock := newTransactionTestRouter(t)
	r.GET("/transactions", srv.GetTransactions)

	userID := testUserID()
	pattern := "%coffee%"

	mock.ExpectQuery("SELECT COUNT").
		WithArgs(userID, pattern, pattern, pattern, pattern).
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectQuery("SELECT t.id, t.account_id").
		WithArgs(userID, pattern, pattern, pattern, pattern, 50, 0).
		WillReturnRows(pgxmock.NewRows(txnListCols))

	req, _ := http.NewRequest("GET", "/transactions?q=coffee", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.NotContains(t, w.Body.String(), "queryDiagnostics")
}

// TestGetTransactionsQueryAmountIsMajorUnits is the money guard at the handler
// boundary: amt>50 is fifty dollars and binds 5000 minor units, never 50. A
// query for $50 that matched a $0.50 transaction would be silently wrong.
func TestGetTransactionsQueryAmountIsMajorUnits(t *testing.T) {
	r, srv, mock := newTransactionTestRouter(t)
	r.GET("/transactions", srv.GetTransactions)

	userID := testUserID()

	mock.ExpectQuery("SELECT COUNT").
		WithArgs(userID, int64(5000)).
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectQuery("SELECT t.id, t.account_id").
		WithArgs(userID, int64(5000), 50, 0).
		WillReturnRows(pgxmock.NewRows(txnListCols))

	req, _ := http.NewRequest("GET", "/transactions?q=amt%3E50", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestGetTransactionsQueryHonoursTheNoneSentinel: cat:none is the query-language
// spelling of the existing uncategorized filter, and it binds nothing.
func TestGetTransactionsQueryHonoursTheNoneSentinel(t *testing.T) {
	r, srv, mock := newTransactionTestRouter(t)
	r.GET("/transactions", srv.GetTransactions)

	userID := testUserID()

	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM transactions t WHERE t\\.user_id = \\$1 AND \\(t\\.category_id IS NULL\\)").
		WithArgs(userID).
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectQuery("SELECT t.id, t.account_id").
		WithArgs(userID, 50, 0).
		WillReturnRows(pgxmock.NewRows(txnListCols))

	req, _ := http.NewRequest("GET", "/transactions?q=cat%3Anone", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestGetTransactionsExportHonoursTheQuery is Review Focus #5: the CSV export
// goes through the same builder, so it cannot drift from the table. It also
// refuses rather than truncates past maxExportRows, which depends on the count
// query seeing exactly the query's predicates.
func TestGetTransactionsExportHonoursTheQuery(t *testing.T) {
	r, srv, mock := newTransactionTestRouter(t)
	r.GET("/transactions/export", srv.ExportTransactions)

	userID := testUserID()

	mock.ExpectQuery("SELECT COUNT").
		WithArgs(userID, int64(5000)).
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(1))
	mock.ExpectQuery("SELECT t.date, t.description").
		WithArgs(userID, int64(5000)).
		WillReturnRows(pgxmock.NewRows([]string{
			"date", "description", "amount", "type", "category_name",
			"account_name", "payee", "tags", "notes",
		}))

	req, _ := http.NewRequest("GET", "/transactions/export?q=amt%3E50", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}
