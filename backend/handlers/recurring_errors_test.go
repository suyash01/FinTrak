package handlers

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/pashagolub/pgxmock/v5"
	"github.com/stretchr/testify/assert"
)

// recurringSeriesRows returns a single-series row set for the load query.
func recurringSeriesRows(id, accountID uuid.UUID) *pgxmock.Rows {
	start, _ := time.Parse("2006-01-02", "2099-01-15")
	return pgxmock.NewRows(recurringSeriesColumnsForTest).
		AddRow(recurringSeriesRow(id, accountID, "Rent", 1000, recurringFreqMonthly, 1, start, nil)...)
}

// recurringTermRows returns a single open-ended term row set.
func recurringTermRows(id, accountID uuid.UUID) *pgxmock.Rows {
	start, _ := time.Parse("2006-01-02", "2099-01-15")
	return pgxmock.NewRows(recurringTermLoadCols).
		AddRow(recurringTermRow(uuid.New(), id, start, 1000, accountID)...)
}

func doRecurring(r http.Handler, method, path, body string) *httptest.ResponseRecorder {
	var req *http.Request
	if body == "" {
		req, _ = http.NewRequest(method, path, nil)
	} else {
		req, _ = http.NewRequest(method, path, bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// TestRecurringInvalidIDs covers every handler's malformed-id guard.
func TestRecurringInvalidIDs(t *testing.T) {
	r, _, _ := recurringTestRouter(t)
	valid := uuid.New().String()
	cases := []struct{ method, path string }{
		{http.MethodPut, "/recurring/not-a-uuid"},
		{http.MethodDelete, "/recurring/not-a-uuid"},
		{http.MethodGet, "/recurring/not-a-uuid/terms"},
		{http.MethodPut, "/recurring/not-a-uuid/terms"},
		{http.MethodPut, "/recurring/" + valid + "/terms/not-a-uuid"},
		{http.MethodDelete, "/recurring/x/terms/y"},
		{http.MethodGet, "/recurring/not-a-uuid/forecast"},
		{http.MethodGet, "/recurring/not-a-uuid/suggestions"},
		{http.MethodGet, "/recurring/not-a-uuid/transactions"},
	}
	for _, tc := range cases {
		w := doRecurring(r, tc.method, tc.path, "{}")
		assert.Equal(t, http.StatusBadRequest, w.Code, tc.path)
	}
}

func TestDeleteRecurringSeriesError(t *testing.T) {
	r, _, mock := recurringTestRouter(t)
	expectExecAny(mock, "DELETE FROM recurring_series WHERE id", 2).WillReturnError(assert.AnError)

	w := doRecurring(r, http.MethodDelete, "/recurring/"+uuid.New().String(), "")
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// --- UpdateRecurringSeries error paths ---

func TestUpdateRecurringSeriesLoadError(t *testing.T) {
	r, _, mock := recurringTestRouter(t)
	expectQueryAny(mock, "FROM recurring_series WHERE id", 2).WillReturnError(assert.AnError)

	w := doRecurring(r, http.MethodPut, "/recurring/"+uuid.New().String(), `{"name":"x"}`)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateRecurringSeriesTermsLoadError(t *testing.T) {
	r, _, mock := recurringTestRouter(t)
	id, accountID := uuid.New(), uuid.New()
	expectQueryAny(mock, "FROM recurring_series WHERE id", 2).WillReturnRows(recurringSeriesRows(id, accountID))
	expectQueryAny(mock, "FROM recurring_series_terms t JOIN accounts a", 2).WillReturnError(assert.AnError)

	w := doRecurring(r, http.MethodPut, "/recurring/"+id.String(), `{"name":"x"}`)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateRecurringSeriesEffectiveBeforeStart(t *testing.T) {
	r, _, mock := recurringTestRouter(t)
	id, accountID := uuid.New(), uuid.New()
	expectQueryAny(mock, "FROM recurring_series WHERE id", 2).WillReturnRows(recurringSeriesRows(id, accountID))
	expectQueryAny(mock, "FROM recurring_series_terms t JOIN accounts a", 2).WillReturnRows(recurringTermRows(id, accountID))

	// An explicit effective date before the series start is rejected.
	w := doRecurring(r, http.MethodPut, "/recurring/"+id.String(), `{"amount":20,"effectiveDate":"2098-01-01"}`)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateRecurringSeriesAccountNotOwned(t *testing.T) {
	r, _, mock := recurringTestRouter(t)
	id, accountID := uuid.New(), uuid.New()
	expectQueryAny(mock, "FROM recurring_series WHERE id", 2).WillReturnRows(recurringSeriesRows(id, accountID))
	expectQueryAny(mock, "FROM recurring_series_terms t JOIN accounts a", 2).WillReturnRows(recurringTermRows(id, accountID))
	// The change lands exactly on the existing term's start, so it updates in
	// place; the account ownership guard matches no rows.
	mock.ExpectBegin()
	expectExecAny(mock, "UPDATE recurring_series_terms t SET", 4).WillReturnResult(pgxmock.NewResult("UPDATE", 0))
	mock.ExpectRollback()

	w := doRecurring(r, http.MethodPut, "/recurring/"+id.String(),
		`{"amount":20,"accountId":"`+uuid.New().String()+`"}`)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateRecurringSeriesTermWriteError(t *testing.T) {
	r, _, mock := recurringTestRouter(t)
	id, accountID := uuid.New(), uuid.New()
	expectQueryAny(mock, "FROM recurring_series WHERE id", 2).WillReturnRows(recurringSeriesRows(id, accountID))
	expectQueryAny(mock, "FROM recurring_series_terms t JOIN accounts a", 2).WillReturnRows(recurringTermRows(id, accountID))
	mock.ExpectBegin()
	expectExecAny(mock, "UPDATE recurring_series_terms t SET", 4).WillReturnError(assert.AnError)
	mock.ExpectRollback()

	w := doRecurring(r, http.MethodPut, "/recurring/"+id.String(), `{"amount":20}`)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateRecurringSeriesUpdateError(t *testing.T) {
	r, _, mock := recurringTestRouter(t)
	id, accountID := uuid.New(), uuid.New()
	expectQueryAny(mock, "FROM recurring_series WHERE id", 2).WillReturnRows(recurringSeriesRows(id, accountID))
	expectQueryAny(mock, "FROM recurring_series_terms t JOIN accounts a", 2).WillReturnRows(recurringTermRows(id, accountID))
	mock.ExpectBegin()
	expectQueryAny(mock, "UPDATE recurring_series SET", 11).WillReturnError(assert.AnError)
	mock.ExpectRollback()

	w := doRecurring(r, http.MethodPut, "/recurring/"+id.String(), `{"name":"x"}`)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// --- Terms handlers error paths ---

func TestGetRecurringTermsLoadError(t *testing.T) {
	r, _, mock := recurringTestRouter(t)
	expectQueryAny(mock, "FROM recurring_series WHERE id", 2).WillReturnError(assert.AnError)

	w := doRecurring(r, http.MethodGet, "/recurring/"+uuid.New().String()+"/terms", "")
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetRecurringTermsQueryError(t *testing.T) {
	r, _, mock := recurringTestRouter(t)
	id, accountID := uuid.New(), uuid.New()
	expectQueryAny(mock, "FROM recurring_series WHERE id", 2).WillReturnRows(recurringSeriesRows(id, accountID))
	expectQueryAny(mock, "FROM recurring_series_terms t JOIN accounts a", 2).WillReturnError(assert.AnError)

	w := doRecurring(r, http.MethodGet, "/recurring/"+id.String()+"/terms", "")
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateRecurringTermLoadError(t *testing.T) {
	r, _, mock := recurringTestRouter(t)
	expectQueryAny(mock, "FROM recurring_series WHERE id", 2).WillReturnError(assert.AnError)

	w := doRecurring(r, http.MethodPut, "/recurring/"+uuid.New().String()+"/terms",
		`{"startDate":"2099-06-01","amount":10,"accountId":"`+uuid.New().String()+`"}`)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateRecurringTermTermsError(t *testing.T) {
	r, _, mock := recurringTestRouter(t)
	id, accountID := uuid.New(), uuid.New()
	expectQueryAny(mock, "FROM recurring_series WHERE id", 2).WillReturnRows(recurringSeriesRows(id, accountID))
	expectQueryAny(mock, "FROM recurring_series_terms t JOIN accounts a", 2).WillReturnError(assert.AnError)

	w := doRecurring(r, http.MethodPut, "/recurring/"+id.String()+"/terms",
		`{"startDate":"2099-06-01","amount":10,"accountId":"`+accountID.String()+`"}`)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateRecurringTermLoadError(t *testing.T) {
	r, _, mock := recurringTestRouter(t)
	expectQueryAny(mock, "FROM recurring_series WHERE id", 2).WillReturnError(assert.AnError)

	w := doRecurring(r, http.MethodPut, "/recurring/"+uuid.New().String()+"/terms/"+uuid.New().String(), `{"amount":10}`)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateRecurringTermTermsError(t *testing.T) {
	r, _, mock := recurringTestRouter(t)
	id, accountID := uuid.New(), uuid.New()
	expectQueryAny(mock, "FROM recurring_series WHERE id", 2).WillReturnRows(recurringSeriesRows(id, accountID))
	expectQueryAny(mock, "FROM recurring_series_terms t JOIN accounts a", 2).WillReturnError(assert.AnError)

	w := doRecurring(r, http.MethodPut, "/recurring/"+id.String()+"/terms/"+uuid.New().String(), `{"amount":10}`)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestDeleteRecurringTermLoadError(t *testing.T) {
	r, _, mock := recurringTestRouter(t)
	expectQueryAny(mock, "FROM recurring_series WHERE id", 2).WillReturnError(assert.AnError)

	w := doRecurring(r, http.MethodDelete, "/recurring/"+uuid.New().String()+"/terms/"+uuid.New().String(), "")
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestDeleteRecurringTermTermsError(t *testing.T) {
	r, _, mock := recurringTestRouter(t)
	id, accountID := uuid.New(), uuid.New()
	expectQueryAny(mock, "FROM recurring_series WHERE id", 2).WillReturnRows(recurringSeriesRows(id, accountID))
	expectQueryAny(mock, "FROM recurring_series_terms t JOIN accounts a", 2).WillReturnError(assert.AnError)

	w := doRecurring(r, http.MethodDelete, "/recurring/"+id.String()+"/terms/"+uuid.New().String(), "")
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestDeleteRecurringTermWriteError(t *testing.T) {
	r, _, mock := recurringTestRouter(t)
	id, accountID := uuid.New(), uuid.New()
	termID := uuid.New()
	expectQueryAny(mock, "FROM recurring_series WHERE id", 2).WillReturnRows(recurringSeriesRows(id, accountID))
	// Two ranges, so the request reaches the write instead of the last-range
	// refusal.
	expectQueryAny(mock, "FROM recurring_series_terms t JOIN accounts a", 2).
		WillReturnRows(pgxmock.NewRows(recurringTermLoadCols).
			AddRow(recurringTermRow(termID, id, mustDate("2099-01-15"), 1000, accountID)...).
			AddRow(recurringTermRowEnd(uuid.New(), id, mustDate("2099-06-15"), mustDate("2099-12-15"), 1200, accountID)...))
	mock.ExpectBegin()
	expectExecAny(mock, "DELETE FROM recurring_series_terms", 3).WillReturnError(assert.AnError)
	mock.ExpectRollback()

	w := doRecurring(r, http.MethodDelete, "/recurring/"+id.String()+"/terms/"+termID.String(), "")
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// --- Forecast error paths ---

func TestGetRecurringForecastLoadError(t *testing.T) {
	r, _, mock := recurringTestRouter(t)
	expectQueryAny(mock, "FROM recurring_series WHERE id", 2).WillReturnError(assert.AnError)

	w := doRecurring(r, http.MethodGet, "/recurring/"+uuid.New().String()+"/forecast", "")
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetRecurringForecastAttachError(t *testing.T) {
	r, _, mock := recurringTestRouter(t)
	id, accountID := uuid.New(), uuid.New()
	expectQueryAny(mock, "FROM recurring_series WHERE id", 2).WillReturnRows(recurringSeriesRows(id, accountID))
	expectQueryAny(mock, "SELECT t.date FROM recurring_attachments ra", 2).WillReturnError(assert.AnError)

	w := doRecurring(r, http.MethodGet, "/recurring/"+id.String()+"/forecast", "")
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetRecurringForecastTermsError(t *testing.T) {
	r, _, mock := recurringTestRouter(t)
	id, accountID := uuid.New(), uuid.New()
	expectQueryAny(mock, "FROM recurring_series WHERE id", 2).WillReturnRows(recurringSeriesRows(id, accountID))
	expectQueryAny(mock, "SELECT t.date FROM recurring_attachments ra", 2).
		WillReturnRows(pgxmock.NewRows([]string{"date"}))
	expectQueryAny(mock, "FROM recurring_series_terms t JOIN accounts a", 2).WillReturnError(assert.AnError)

	w := doRecurring(r, http.MethodGet, "/recurring/"+id.String()+"/forecast", "")
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// --- Suggestions / transactions error paths ---

func TestGetRecurringSuggestionsLoadError(t *testing.T) {
	r, _, mock := recurringTestRouter(t)
	expectQueryAny(mock, "FROM recurring_series WHERE id", 2).WillReturnError(assert.AnError)

	w := doRecurring(r, http.MethodGet, "/recurring/"+uuid.New().String()+"/suggestions", "")
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetRecurringSuggestionsTermsError(t *testing.T) {
	r, _, mock := recurringTestRouter(t)
	id, accountID := uuid.New(), uuid.New()
	expectQueryAny(mock, "FROM recurring_series WHERE id", 2).WillReturnRows(recurringSeriesRows(id, accountID))
	expectQueryAny(mock, "FROM recurring_series_terms t JOIN accounts a", 2).WillReturnError(assert.AnError)

	w := doRecurring(r, http.MethodGet, "/recurring/"+id.String()+"/suggestions", "")
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetRecurringTransactionsLoadError(t *testing.T) {
	r, _, mock := recurringTestRouter(t)
	expectQueryAny(mock, "FROM recurring_series WHERE id", 2).WillReturnError(assert.AnError)

	w := doRecurring(r, http.MethodGet, "/recurring/"+uuid.New().String()+"/transactions", "")
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetRecurringTransactionsQueryError(t *testing.T) {
	r, _, mock := recurringTestRouter(t)
	id, accountID := uuid.New(), uuid.New()
	expectQueryAny(mock, "FROM recurring_series WHERE id", 2).WillReturnRows(recurringSeriesRows(id, accountID))
	expectQueryAny(mock, "SELECT t.id, t.account_id, t.date, t.description", 2).WillReturnError(assert.AnError)

	w := doRecurring(r, http.MethodGet, "/recurring/"+id.String()+"/transactions", "")
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}
