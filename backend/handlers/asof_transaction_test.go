package handlers

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/pashagolub/pgxmock/v5"
	"github.com/stretchr/testify/assert"
)

// TestAsOfList pins `asOf` on the transaction list. The bound value is what
// proves the parameter reached the statement: pgxmock compares the argument
// list exactly, so a filter that dropped the clause, bound it to the wrong
// column, or allocated a placeholder by hand all fail here.
func TestAsOfList(t *testing.T) {
	// The list and its COUNT(*) run the same predicate with the same args, so
	// both are expected — and the COUNT's matcher names the predicate itself
	// (`t.date <= $N`), not just the value. Matching on the value alone would
	// accept a query that bound the instant to some other column.
	expectAsOfQuery := func(mock pgxmock.PgxPoolIface, args ...any) {
		t.Helper()
		// The instant rides the window's upper bound, so the matcher anchors on
		// that bound and its placeholder — which is the last one the filter
		// allocated, hence len(args). Naming the predicate as well as the value
		// is what rejects a query that bound the instant to some other column.
		mock.ExpectQuery(`SELECT COUNT\(\*\) FROM transactions t WHERE t\.user_id = \$1.*AND t\.date <= \$` + strconv.Itoa(len(args))).
			WithArgs(args...).
			WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(0))

		listArgs := append(append([]any{}, args...), 50, 0)
		mock.ExpectQuery("SELECT t.id, .* FROM transactions t").
			WithArgs(listArgs...).
			WillReturnRows(pgxmock.NewRows([]string{"id"}))
	}

	t.Run("binds the instant as an inclusive upper bound", func(t *testing.T) {
		r, srv, mock := newTransactionTestRouter(t)
		r.GET("/transactions", srv.GetTransactions)

		expectAsOfQuery(mock, testUserID(), "2026-03-01")

		req, _ := http.NewRequest("GET", "/transactions?asOf=2026-03-01", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	// asOf is a window and not a moment, so it takes its place in the
	// date-range bounds rather than replacing them: the lower bound must still
	// be bound, and the upper bound must be the instant, not a second one.
	t.Run("keeps dateFrom and bounds the upper end with the instant", func(t *testing.T) {
		r, srv, mock := newTransactionTestRouter(t)
		r.GET("/transactions", srv.GetTransactions)

		expectAsOfQuery(mock, testUserID(), "2026-01-01", "2026-03-01")

		req, _ := http.NewRequest("GET", "/transactions?dateFrom=2026-01-01&asOf=2026-03-01", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	// The clamp is the point of the feature: a window ending before the instant
	// asks for a shorter window, and answering with the later instant would
	// report as much as the window covers. The expectation is a single bound
	// argument, so an implementation that appended the instant as a second
	// `t.date <=` alongside the requested dateTo would bind three args and
	// fail — which is the outcome this row exists to prevent.
	t.Run("clamps to the window end", func(t *testing.T) {
		r, srv, mock := newTransactionTestRouter(t)
		r.GET("/transactions", srv.GetTransactions)

		expectAsOfQuery(mock, testUserID(), "2026-02-15")

		req, _ := http.NewRequest("GET", "/transactions?asOf=2026-03-01&dateTo=2026-02-15", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	// A bare dateTo=2026-02-15 already produces that single bound argument, so
	// the row above cannot by itself tell "asOf clamped to dateTo" from "asOf
	// ignored". This one can: the message names the *clamped* instant, which
	// only exists if the list handed parseAsOf the window it had already parsed
	// and the clamp ran before the dateFrom check. With no ExpectQuery
	// registered, a 200 (a window that still resolved) fails the assertion.
	t.Run("the rejection after a clamp names the clamped instant", func(t *testing.T) {
		r, srv, mock := newTransactionTestRouter(t)
		r.GET("/transactions", srv.GetTransactions)

		req, _ := http.NewRequest("GET", "/transactions?asOf=2026-03-01&dateTo=2026-02-15&dateFrom=2026-02-20", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Contains(t, w.Body.String(), "dateFrom 2026-02-20 is after asOf 2026-02-15")
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	// The window is empty once the instant is inside it, and an empty window
	// must be an explicit 400 rather than a bare figure. No ExpectQuery is
	// registered on this pool, so a filter that resolved and ran the query
	// would hit pgxmock's "was not expected" error and answer 500 instead.
	t.Run("dateFrom after asOf is a 400 with no query", func(t *testing.T) {
		r, srv, mock := newTransactionTestRouter(t)
		r.GET("/transactions", srv.GetTransactions)

		req, _ := http.NewRequest("GET", "/transactions?asOf=2026-03-01&dateFrom=2026-04-01", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Contains(t, w.Body.String(), "dateFrom 2026-04-01 is after asOf 2026-03-01")
		// Nothing was registered, so this only proves no expectation went
		// unmet; the 400 is what proves the pool was never asked.
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}
