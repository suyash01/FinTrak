package handlers

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/pashagolub/pgxmock/v5"
	"github.com/stretchr/testify/assert"
)

func TestExportTransactions(t *testing.T) {
	r, srv, mock := newAccountTestRouter(t)
	r.GET("/transactions/export", srv.ExportTransactions)

	userID := testUserID()
	accountID := uuid.New()
	now := time.Now()

	rows := pgxmock.NewRows([]string{
		"date", "description", "amount", "type", "account_name",
		"category_name", "group_name", "payee", "tags", "notes",
	}).
		AddRow(now, "Coffee", 250.5, "debit", "Checking", "Food", "Expense", "Cafe", []string{"trip"}, "morning").
		AddRow(now.AddDate(0, 0, -1), "Salary", 50000.0, "credit", "Checking", "Income", "Income", "Acme", nil, "")

	// The match count runs first: the cap has to be applied before any body.
	mock.ExpectQuery(`SELECT COUNT\(\*\)`).
		WithArgs(userID, accountID.String(), []string{"trip"}).
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(2))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT t.date, t.description, t.amount, t.type")+".*"+regexp.QuoteMeta("COALESCE(t.tags, '{}') AS tags")).
		WithArgs(userID, accountID.String(), []string{"trip"}).
		WillReturnRows(rows)

	req, _ := http.NewRequest("GET", "/transactions/export?accountId="+accountID.String()+"&tags=trip", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Header().Get("Content-Type"), "text/csv")
	assert.Contains(t, w.Header().Get("Content-Disposition"), "fintrak_transactions.csv")
	assert.Contains(t, w.Body.String(), "Date,Description,Amount,Type,Account,Category,Group,Payee,Tags,Notes")
	assert.Contains(t, w.Body.String(), "Coffee")
	assert.Contains(t, w.Body.String(), "Salary")

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestExportTransactionsInvalidAccount(t *testing.T) {
	r, srv, _ := newAccountTestRouter(t)
	r.GET("/transactions/export", srv.ExportTransactions)

	req, _ := http.NewRequest("GET", "/transactions/export?accountId=not-a-uuid", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestExportTransactionsQueryError(t *testing.T) {
	r, srv, mock := newAccountTestRouter(t)
	r.GET("/transactions/export", srv.ExportTransactions)

	mock.ExpectQuery(`SELECT COUNT\(\*\)`).
		WithArgs(testUserID()).
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(1))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT t.date, t.description, t.amount, t.type") + ".*" + regexp.QuoteMeta("COALESCE(t.tags, '{}') AS tags")).
		WithArgs(testUserID()).
		WillReturnError(assert.AnError)

	req, _ := http.NewRequest("GET", "/transactions/export", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestExportTransactionsCountError(t *testing.T) {
	r, srv, mock := newAccountTestRouter(t)
	r.GET("/transactions/export", srv.ExportTransactions)

	mock.ExpectQuery(`SELECT COUNT\(\*\)`).
		WithArgs(testUserID()).
		WillReturnError(assert.AnError)

	req, _ := http.NewRequest("GET", "/transactions/export", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// A filter that matches more than the cap is refused, not truncated: the caller
// cannot tell a partial CSV from a complete one.
func TestExportTransactionsRefusesMoreThanTheCap(t *testing.T) {
	r, srv, mock := newAccountTestRouter(t)
	r.GET("/transactions/export", srv.ExportTransactions)

	mock.ExpectQuery(`SELECT COUNT\(\*\)`).
		WithArgs(testUserID()).
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(maxExportRows + 1))

	req, _ := http.NewRequest("GET", "/transactions/export", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "export limit")
	// No rows are streamed, so no CSV header line either.
	assert.NotContains(t, w.Body.String(), "Date,Description")
	assert.NoError(t, mock.ExpectationsWereMet())
}
