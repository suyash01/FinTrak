package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/pashagolub/pgxmock/v5"
	"github.com/stretchr/testify/assert"

	"github.com/fintrak/backend/models"
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
	//
	// boundAt is the placeholder the upper bound must occupy, given
	// explicitly rather than derived from len(args): most rows put the bound
	// last, so deriving it happens to work, but a row that appends another
	// filter afterwards puts the bound earlier and the derived number would
	// silently assert the wrong clause.
	//
	// In the common shape asOf is the final bound the filter appends, so it
	// takes the placeholder after the last argument; rows that append another
	// filter pass a smaller number.
	expectAsOfQuery := func(mock pgxmock.PgxPoolIface, boundAt int, args ...any) {
		t.Helper()
		mock.ExpectQuery(`SELECT COUNT\(\*\) FROM transactions t WHERE t\.user_id = \$1.*AND t\.date <= \$` + strconv.Itoa(boundAt)).
			WithArgs(args...).
			WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(0))

		listArgs := append(append([]any{}, args...), 50, 0)
		mock.ExpectQuery("SELECT t.id, .* FROM transactions t").
			WithArgs(listArgs...).
			WillReturnRows(pgxmock.NewRows([]string{"id"}))
	}

	// expectAsOfQueryWithRow is expectAsOfQuery but returning one real
	// transaction. A summary row only survives mergeMonthEndRows when the page
	// holds at least one transaction (filterSummaryRowsForPage drops the rows
	// otherwise), so a row that asserts on the response body needs one.
	expectAsOfQueryWithRow := func(mock pgxmock.PgxPoolIface, boundAt int, accountID uuid.UUID, args ...any) {
		t.Helper()
		mock.ExpectQuery(`SELECT COUNT\(\*\) FROM transactions t WHERE t\.user_id = \$1.*AND t\.date <= \$` + strconv.Itoa(boundAt)).
			WithArgs(args...).
			WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(1))

		listArgs := append(append([]any{}, args...), 50, 0)
		rows := txnListRow(uuid.New(), accountID, time.Date(2026, 1, 10, 0, 0, 0, 0, time.UTC),
			"Coffee", 250.5, "debit", nil, nil, "", nil, "", time.Now(), "Savings", "", "", "", false, nil, "", nil, "")
		mock.ExpectQuery("SELECT t.id, .* FROM transactions t").
			WithArgs(listArgs...).
			WillReturnRows(rows)
	}

	t.Run("binds the instant as an inclusive upper bound", func(t *testing.T) {
		r, srv, mock := newTransactionTestRouter(t)
		r.GET("/transactions", srv.GetTransactions)

		expectAsOfQuery(mock, 2, testUserID(), "2026-03-01")

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

		expectAsOfQuery(mock, 3, testUserID(), "2026-01-01", "2026-03-01")

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

		expectAsOfQuery(mock, 2, testUserID(), "2026-02-15")

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

	// The instant must also reach the synthetic summary rows, which are built
	// from the window the *request* asked for rather than from the window the
	// filter resolved. Nothing above exercises this: every other row omits
	// accountId, so accountUUID is nil and the summary branch never runs.
	//
	// Concretely, with asOf alone c.Query("dateTo") is "", both summary
	// builders default their end to today, and the in-progress "Running
	// balance" row is dated *today* carrying the balance as of today — ledger
	// state from months after the requested instant, inside an as-of list.
	// The `t.date <= $3` expectation below is the pin: it is bound to the
	// resolved instant, and would be bound to today if the summary path
	// re-read the request.
	t.Run("the summary rows are bounded by the instant, not by today", func(t *testing.T) {
		r, srv, mock := newTransactionTestRouter(t)
		r.GET("/transactions", srv.GetTransactions)

		userID := testUserID()
		accountID := uuid.New()
		accountIDStr := accountID.String()
		asOfDay := time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)

		expectAsOfQueryWithRow(mock, 3, accountID, userID, accountIDStr, "2026-01-15")

		// The account has no billing day, so it gets month-end "Running
		// balance" rows rather than per-cycle "Total outstanding" rows.
		mock.ExpectQuery("SELECT a.name, a.billing_day").
			WithArgs(accountID, userID).
			WillReturnRows(pgxmock.NewRows([]string{"name", "billing_day"}).
				AddRow("Savings", nil))

		// Per-month net over the full ledger: one month, a single 250.5 debit.
		// The month is the one containing the instant, so the month end is
		// still ahead of it and the in-progress branch below is the one taken.
		mock.ExpectQuery("SELECT date_trunc\\('month'").
			WithArgs(accountID, userID).
			WillReturnRows(pgxmock.NewRows([]string{"month", "net", "count"}).
				AddRow(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), -250.5, 1))

		// Balance as of the range end. This argument is the whole test.
		mock.ExpectQuery("SELECT COALESCE\\(SUM\\(").
			WithArgs(accountID, userID, asOfDay).
			WillReturnRows(pgxmock.NewRows([]string{"total", "count"}).
				AddRow(-250.5, 1))

		req, _ := http.NewRequest("GET", "/transactions?accountId="+accountIDStr+"&asOf=2026-01-15", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var res struct {
			Data []models.Transaction `json:"data"`
		}
		assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &res))
		assert.Len(t, res.Data, 2)

		// The synthetic row reports the instant, not the day the request
		// happened to be served.
		var summary models.Transaction
		for _, txn := range res.Data {
			if txn.IsSummary {
				summary = txn
			}
		}
		assert.True(t, summary.IsSummary, "expected a summary row in the response")
		assert.Equal(t, "Running balance", summary.Description)
		assert.Equal(t, "2026-01-15", summary.Date.Format("2006-01-02"))

		assert.NoError(t, mock.ExpectationsWereMet())
	})

	// Every other row puts the instant last, which is the only reason the
	// COUNT matcher's `t.date <= $len(args)` happens to be the final clause.
	// This one appends a clause after it, so the instant's placeholder is
	// pinned mid-sequence and the later clause is proven to have taken the
	// next one — a hand-counted $n would collide here.
	t.Run("a filter appended after the instant takes the next placeholder", func(t *testing.T) {
		r, srv, mock := newTransactionTestRouter(t)
		r.GET("/transactions", srv.GetTransactions)

		expectAsOfQuery(mock, 2, testUserID(), "2026-03-01", "debit")

		req, _ := http.NewRequest("GET", "/transactions?asOf=2026-03-01&type=debit", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}
