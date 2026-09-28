package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/fintrak/backend/internal/money"
	"github.com/fintrak/backend/models"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/pashagolub/pgxmock/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// asOfDay is the instant every row below quotes. It is a plain stored day
// string in the shape parseAsOf validates, because parseAsOf compares it
// lexically against the window it is handed.
const asOfDay = "2026-03-31"

// balanceQueryRegex is the matcher every balance-query expectation registers.
//
// It is deliberately not a loose "SELECT a.id" : the loan branch is the branch
// whose ABSENCE fails silently. A car loan holds no transactions of its own
// (backend/db/migrations/000001_initial_schema.up.sql), so a balance query with
// no loan_attachments subquery still returns a row per account, still returns
// the right columns, and still answers 200 - it just reports every loan as
// roughly zero, which reads as "you owed nothing" rather than as a bug. Pinning
// the table name in the matcher is what turns that silent wrong number into a
// failing test.
//
// The `t.date <= $2` is pinned for the same reason and the opposite case: an
// unbounded balance query would be right for asOf=today and wrong for every
// other day, and only a report asked for a past date can tell the difference.
const balanceQueryRegex = `loan_attachments la JOIN transactions t.*t\.date <= \$2`

// balanceCols is the column set accountBalancesAsOf selects, in order.
var balanceCols = []string{"id", "name", "currency", "balance"}

// expectBalanceQuery registers the one balance query. It is the single place the
// expectations are built so the loan branch cannot be dropped from one case and
// kept in the others.
func expectBalanceQuery(mock pgxmock.PgxPoolIface, userID uuid.UUID, asOf string, rows *pgxmock.Rows) {
	mock.ExpectQuery(balanceQueryRegex).
		WithArgs(userID, asOf).
		WillReturnRows(rows)
}

// expectOrdinarySummary registers every query a summary with no asOf makes, in
// the order the handler issues them. No balance query is registered, so a
// handler that issued one unconditionally would hit pgxmock's "was not
// expected" and answer 500 — that is what case 1 and case 8 rest on.
func expectOrdinarySummary(mock pgxmock.PgxPoolIface, userID uuid.UUID) {
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM accounts WHERE user_id").
		WithArgs(userID).
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(2))
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM transactions").
		WithArgs(userID).
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectQuery("FROM accounts a\\s+LEFT JOIN transactions t").
		WithArgs(userID).
		WillReturnRows(pgxmock.NewRows([]string{"id", "name", "currency", "income", "expense"}))
	expectRestOfSummary(mock, userID)
}

// expectRestOfSummary registers the sections after the scope query, with the
// argument list every transaction-backed section shares.
func expectRestOfSummary(mock pgxmock.PgxPoolIface, userID uuid.UUID, args ...any) {
	all := append([]any{userID}, args...)
	mock.ExpectQuery(catWindowRegex + "[\\s\\S]*t\\.type = 'debit' AND t\\.user_id").
		WithArgs(all...).
		WillReturnRows(pgxmock.NewRows([]string{"id", "name", "color", "icon", "currency", "total", "count"}))
	mock.ExpectQuery(catWindowRegex + "[\\s\\S]*t\\.type = 'credit' AND t\\.user_id").
		WithArgs(all...).
		WillReturnRows(pgxmock.NewRows([]string{"id", "name", "color", "icon", "currency", "total", "count"}))
	mock.ExpectQuery("TO_CHAR\\(t.date, 'YYYY-MM'\\)").
		WithArgs(all...).
		WillReturnRows(pgxmock.NewRows([]string{"month", "currency", "income", "expense"}))
	mock.ExpectQuery("SELECT t.id, t.account_id").
		WithArgs(all...).
		WillReturnRows(pgxmock.NewRows([]string{
			"id", "account_id", "date", "description", "amount", "type",
			"category_id", "tags", "notes", "payee_id", "payee", "created_at",
			"account_name", "category_name", "category_icon", "category_color",
		}))
}

// newBalanceUnitServer builds a Server over a mock pool for the unit rows, which
// call accountBalancesAsOf directly rather than through the endpoint. The pool
// satisfies scopeQueryer, so the production signature is the one under test.
func newBalanceUnitServer(t *testing.T) (*Server, pgxmock.PgxPoolIface) {
	t.Helper()
	mock, err := newGuardedPool(t)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { mock.Close() })
	return newTestServer(mock), mock
}

// TestAccountBalancesAsOf drives the query itself over a pgxmock pool.
//
// Every row is a case the fold or the SQL gets wrong in a way that looks
// plausible on the wire, which is the failure mode this task exists to prevent:
// a sign chosen rather than read, a key invented, an account dropped because it
// held nothing.
func TestAccountBalancesAsOf(t *testing.T) {
	userID := testUserID()

	// A credit-positive account: salary account, positive balance.
	t.Run("a credit-positive account reports its balance under its own currency", func(t *testing.T) {
		srv, mock := newBalanceUnitServer(t)
		acct := uuid.New()

		expectBalanceQuery(mock, userID, asOfDay, pgxmock.NewRows(balanceCols).
			AddRow(acct, "Everyday", "INR", money.FromFloat(500.00)))

		balances, err := srv.accountBalancesAsOf(context.Background(), mock, userID, asOfDay)

		require.NoError(t, err)
		require.Len(t, balances, 1)
		assert.Equal(t, acct, balances[0].ID)
		assert.Equal(t, "Everyday", balances[0].Name)
		assert.Equal(t, "INR", balances[0].Currency)
		assert.Equal(t, models.CurrencyAmounts{"INR": money.FromFloat(500.00)}, balances[0].Balance)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	// A card is debit-positive, so it balances NEGATIVE. The sign is the data's,
	// not a choice the fold makes: a card reported positive would say the
	// holder is owed 25.00 rather than that they owe it.
	t.Run("a debit-positive account reports a negative balance", func(t *testing.T) {
		srv, mock := newBalanceUnitServer(t)
		acct := uuid.New()

		expectBalanceQuery(mock, userID, asOfDay, pgxmock.NewRows(balanceCols).
			AddRow(acct, "Visa", "USD", money.FromFloat(-25.00)))

		balances, err := srv.accountBalancesAsOf(context.Background(), mock, userID, asOfDay)

		require.NoError(t, err)
		require.Len(t, balances, 1)
		// The key is present, and its value is what the row said.
		require.Contains(t, balances[0].Balance, "USD")
		assert.Equal(t, money.FromFloat(-25.00), balances[0].Balance["USD"])
		assert.Equal(t, "USD", balances[0].Currency)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	// Review Focus #1: the loan branch. A loan's balance is outstanding
	// principal, summed from the transactions ATTACHED to the loan, not from the
	// loan account's own ledger — which is empty. The value here is a negative,
	// because a payment on a loan reduces what is owed, and the whole point is
	// that the SQL named loan_attachments: the matcher required it before this
	// row could return at all.
	t.Run("a loan balances from its attached payments, not its own ledger", func(t *testing.T) {
		srv, mock := newBalanceUnitServer(t)
		loan := uuid.New()

		expectBalanceQuery(mock, userID, asOfDay, pgxmock.NewRows(balanceCols).
			AddRow(loan, "Car loan", "INR", money.FromFloat(-12000.00)))

		balances, err := srv.accountBalancesAsOf(context.Background(), mock, userID, asOfDay)

		require.NoError(t, err)
		require.Len(t, balances, 1)
		require.Contains(t, balances[0].Balance, "INR")
		assert.Equal(t, money.FromFloat(-12000.00), balances[0].Balance["INR"])
		// The statement itself, asserted as well as matched: the matcher proves
		// the query that ran named the table, and this proves the statement is
		// built rather than assembled per call, so the two cannot diverge.
		assert.Contains(t, accountBalancesAsOfSQL, "loan_attachments")
		assert.Contains(t, accountBalancesAsOfSQL, "t.date <= $2")
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	// A zero balance adds NO key. Add skips a zero contribution on purpose, so
	// len() counts the accounts that actually held money. Inventing a key here
	// would also make Single() report a figure the server called empty.
	t.Run("a zero balance adds no key but still names the account", func(t *testing.T) {
		srv, mock := newBalanceUnitServer(t)
		acct := uuid.New()

		expectBalanceQuery(mock, userID, asOfDay, pgxmock.NewRows(balanceCols).
			AddRow(acct, "Everyday", "INR", 0))

		balances, err := srv.accountBalancesAsOf(context.Background(), mock, userID, asOfDay)

		require.NoError(t, err)
		require.Len(t, balances, 1)
		assert.Len(t, balances[0].Balance, 0)
		// The currency is the account's own, present even with no money in it:
		// it is what says which ledger the zero belongs to.
		assert.Equal(t, "INR", balances[0].Currency)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	// Review Focus #2: an account holding nothing at the instant is still an
	// account, and "how much had I saved on the day I started" is answered by a
	// report of zeros, not by an empty list. A GROUP BY over the transactions
	// would drop the account and make an empty slice read as "no accounts".
	t.Run("an instant before everything still returns every account", func(t *testing.T) {
		srv, mock := newBalanceUnitServer(t)
		accounts := []uuid.UUID{uuid.New(), uuid.New(), uuid.New()}

		rows := pgxmock.NewRows(balanceCols).
			AddRow(accounts[0], "Everyday", "INR", 0).
			AddRow(accounts[1], "Savings", "INR", 0).
			AddRow(accounts[2], "Car loan", "INR", 0)
		expectBalanceQuery(mock, userID, "2020-01-01", rows)

		balances, err := srv.accountBalancesAsOf(context.Background(), mock, userID, "2020-01-01")

		require.NoError(t, err)
		// Non-empty, and exactly the accounts: an implementation that filtered
		// the zero balances out, or inner-joined transactions, would answer 0
		// here and the response would read as "you had no accounts".
		assert.Len(t, balances, len(accounts))
		for i, acct := range accounts {
			assert.Equal(t, acct, balances[i].ID)
			assert.Len(t, balances[i].Balance, 0)
		}
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	// A query that fails must be an error, not an empty list. Returning
	// `[]` here would be indistinguishable from "no accounts", which is the
	// same silent-wrong-answer shape the empty slice rule exists to prevent.
	t.Run("a query failure is an error, not an empty list", func(t *testing.T) {
		srv, mock := newBalanceUnitServer(t)
		mock.ExpectQuery(balanceQueryRegex).
			WithArgs(userID, asOfDay).
			WillReturnError(assert.AnError)

		balances, err := srv.accountBalancesAsOf(context.Background(), mock, userID, asOfDay)

		require.Error(t, err)
		assert.Nil(t, balances)
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

// TestDashboardAsOf drives the whole endpoint, because the fields are pointers
// on a response and a value assertion cannot see a missing JSON key.
func TestDashboardAsOf(t *testing.T) {
	userID := testUserID()

	// Review Focus #3. The balance query is one of the summary's reads, and a
	// summary is the most-polled endpoint in the app; running it for callers who
	// never asked for it would be a silent tax on every request. pgxmock is
	// ordered, so an unexpected statement between the registered ones is an
	// error, and the handler would answer 500 instead of 200.
	t.Run("no asOf issues no balance query", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		require.NoError(t, err)
		defer mock.Close()
		r := newDashboardTestRouter(newTestServer(mock))

		mock.ExpectBeginTx(pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
		expectOrdinarySummary(mock, userID)
		mock.ExpectCommit()

		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/dashboard/summary", nil))

		assert.Equal(t, http.StatusOK, w.Code)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	// The pointer/omitempty trap. A bare slice is omitted when empty even under
	// omitempty's presence... no: encoding/json omits an empty slice, so a bare
	// `[]AccountBalance` could not say "asked, and there were none". And a
	// non-nil pointer to a NIL slice serialises as `"balances": null`, which is
	// neither absent nor []. Both are invisible to a struct assertion, which is
	// why this is a body assertion.
	t.Run("no asOf omits both fields from the body", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		require.NoError(t, err)
		defer mock.Close()
		r := newDashboardTestRouter(newTestServer(mock))

		mock.ExpectBeginTx(pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
		expectOrdinarySummary(mock, userID)
		mock.ExpectCommit()

		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/dashboard/summary", nil))

		require.Equal(t, http.StatusOK, w.Code)
		body := w.Body.String()
		assert.NotContains(t, body, `"asOf"`)
		assert.NotContains(t, body, `"balances"`)

		// And the decoded value agrees, so this is not just a naming accident:
		// the keys are absent, not present-and-empty.
		var raw map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &raw))
		_, hasAsOf := raw["asOf"]
		_, hasBalances := raw["balances"]
		assert.False(t, hasAsOf, "asOf present without a request for one")
		assert.False(t, hasBalances, "balances present without a request for one")
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	// The whole feature, end to end. Every transaction-backed section must
	// narrow to the SAME instant the balances are computed at, so the bound
	// argument list is asserted on all of them at once: a response whose trend
	// described a wider window than its balances would be the silent-wrong-
	// number case the handler was reworked to remove.
	t.Run("an as-of report narrows every section and carries the balances", func(t *testing.T) {
		mock, err := newGuardedPool(t)
		require.NoError(t, err)
		defer mock.Close()
		r := newDashboardTestRouter(newTestServer(mock))

		acct := uuid.New()
		loan := uuid.New()

		mock.ExpectBeginTx(pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
		mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM accounts WHERE user_id").
			WithArgs(userID).
			WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(2))
		// The count, the scope totals and the four sections below all bind the
		// instant as the upper bound of the window.
		mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM transactions t[\\s\\S]*AND t\\.date <= \\$2").
			WithArgs(userID, asOfDay).
			WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(3))
		mock.ExpectQuery("FROM accounts a\\s+LEFT JOIN transactions t[\\s\\S]*t\\.date <= \\$2").
			WithArgs(userID, asOfDay).
			WillReturnRows(pgxmock.NewRows([]string{"id", "name", "currency", "income", "expense"}))
		expectRestOfSummary(mock, userID, asOfDay)
		expectBalanceQuery(mock, userID, asOfDay, pgxmock.NewRows(balanceCols).
			AddRow(acct, "Everyday", "INR", money.FromFloat(500.00)).
			AddRow(loan, "Car loan", "INR", money.FromFloat(-12000.00)))
		mock.ExpectCommit()

		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/dashboard/summary?asOf="+asOfDay, nil))

		require.Equal(t, http.StatusOK, w.Code)

		var summary models.DashboardSummary
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &summary))

		require.NotNil(t, summary.AsOf)
		assert.Equal(t, asOfDay, *summary.AsOf)
		require.NotNil(t, summary.Balances)
		require.Len(t, *summary.Balances, 2)
		assert.Equal(t, "Everyday", (*summary.Balances)[0].Name)
		assert.Equal(t, models.CurrencyAmounts{"INR": money.FromFloat(500.00)}, (*summary.Balances)[0].Balance)
		// The loan is present with a negative balance rather than a zero one —
		// the review finding is that its absence is invisible, so its presence
		// is the assertion.
		assert.Equal(t, "Car loan", (*summary.Balances)[1].Name)
		require.Contains(t, (*summary.Balances)[1].Balance, "INR")
		assert.Equal(t, money.FromFloat(-12000.00), (*summary.Balances)[1].Balance["INR"])
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	// An account-free user asked for an instant still gets `"balances": []` and
	// not null. This is the case the allocation-before-address rule exists for,
	// and it is the one place a nil slice behind a non-nil pointer is legal Go
	// and wrong JSON.
	t.Run("an as-of report with no accounts carries an empty list, not null", func(t *testing.T) {
		mock, err := newGuardedPool(t)
		require.NoError(t, err)
		defer mock.Close()
		r := newDashboardTestRouter(newTestServer(mock))

		mock.ExpectBeginTx(pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
		mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM accounts WHERE user_id").
			WithArgs(userID).
			WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(0))
		mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM transactions t[\\s\\S]*AND t\\.date <= \\$2").
			WithArgs(userID, asOfDay).
			WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(0))
		mock.ExpectQuery("FROM accounts a\\s+LEFT JOIN transactions t[\\s\\S]*t\\.date <= \\$2").
			WithArgs(userID, asOfDay).
			WillReturnRows(pgxmock.NewRows([]string{"id", "name", "currency", "income", "expense"}))
		expectRestOfSummary(mock, userID, asOfDay)
		expectBalanceQuery(mock, userID, asOfDay, pgxmock.NewRows(balanceCols))
		mock.ExpectCommit()

		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/dashboard/summary?asOf="+asOfDay, nil))

		require.Equal(t, http.StatusOK, w.Code)
		assert.Contains(t, w.Body.String(), `"balances":[]`)

		var raw struct {
			Balances *[]models.AccountBalance `json:"balances"`
		}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &raw))
		require.NotNil(t, raw.Balances)
		assert.Len(t, *raw.Balances, 0)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	// Review Focus #5. Two currencies in scope and nothing anywhere may collapse
	// them into one number. Each account's balance is keyed by its OWN currency —
	// a USD account's money can never appear under the INR key, even though the
	// two rows come back from one statement.
	t.Run("two currencies stay two keys and are both named in the scope", func(t *testing.T) {
		mock, err := newGuardedPool(t)
		require.NoError(t, err)
		defer mock.Close()
		r := newDashboardTestRouter(newTestServer(mock))

		acctINR := uuid.New()
		acctUSD := uuid.New()

		mock.ExpectBeginTx(pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
		mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM accounts WHERE user_id").
			WithArgs(userID).
			WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(2))
		mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM transactions t[\\s\\S]*AND t\\.date <= \\$2").
			WithArgs(userID, asOfDay).
			WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(4))
		// The scope covers both currencies, and BOTH accounts spend, which is what
		// makes the refusal below meaningful: a response that named one currency
		// while the other held money would be a silent gap, and a side that
		// genuinely spans one currency would not test anything — the same
		// asymmetry TestGetDashboardSummaryRefusesToCombineCurrencies notes.
		mock.ExpectQuery("FROM accounts a\\s+LEFT JOIN transactions t[\\s\\S]*t\\.date <= \\$2").
			WithArgs(userID, asOfDay).
			WillReturnRows(pgxmock.NewRows([]string{"id", "name", "currency", "income", "expense"}).
				AddRow(acctINR, "Everyday", "INR", money.FromFloat(500.00), money.FromFloat(120.00)).
				AddRow(acctUSD, "Travel card", "USD", 0, money.FromFloat(25.00)))
		expectRestOfSummary(mock, userID, asOfDay)
		expectBalanceQuery(mock, userID, asOfDay, pgxmock.NewRows(balanceCols).
			AddRow(acctINR, "Everyday", "INR", money.FromFloat(500.00)).
			AddRow(acctUSD, "Travel card", "USD", money.FromFloat(-25.00)))
		mock.ExpectCommit()

		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/dashboard/summary?asOf="+asOfDay, nil))

		require.Equal(t, http.StatusOK, w.Code)
		var summary models.DashboardSummary
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &summary))

		require.NotNil(t, summary.Balances)
		require.Len(t, *summary.Balances, 2)
		assert.Equal(t, models.CurrencyAmounts{"INR": money.FromFloat(500.00)}, (*summary.Balances)[0].Balance)
		assert.Equal(t, models.CurrencyAmounts{"USD": money.FromFloat(-25.00)}, (*summary.Balances)[1].Balance)

		// The response names both currencies, so a mixed result explains itself.
		assert.Equal(t, []string{"INR", "USD"}, summary.CurrencyScope.Currencies)

		// And no total anywhere offers a single number for the two.
		if _, _, ok := summary.TotalExpense.Single(); ok {
			t.Error("a two-currency expense total reported as a single amount")
		}
		if _, _, ok := summary.TotalNet.Single(); ok {
			t.Error("a two-currency net reported as a single amount")
		}
		// Each balance is one currency, so each IS single — that is the shape
		// that makes the above meaningful rather than uniformly evasive.
		if _, _, ok := (*summary.Balances)[0].Balance.Single(); !ok {
			t.Error("a one-currency balance did not report as single")
		}
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	// The instant is resolved once and every later reader sees the RESOLVED
	// value. Here the request asks for more than the window covers, so the
	// clamp pulls it back to dateTo — and the bound argument must be the pulled-
	// back date. A handler that re-read c.Query("asOf") for the balance query
	// while the sections used the clamp (or the reverse) would answer with two
	// different instants in one response.
	t.Run("a clamped instant is the one every section is bounded by", func(t *testing.T) {
		mock, err := newGuardedPool(t)
		require.NoError(t, err)
		defer mock.Close()
		r := newDashboardTestRouter(newTestServer(mock))

		const clamped = "2026-02-15"

		mock.ExpectBeginTx(pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
		mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM accounts WHERE user_id").
			WithArgs(userID).
			WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(1))
		mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM transactions t[\\s\\S]*AND t\\.date <= \\$3").
			WithArgs(userID, "2026-01-01", clamped).
			WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(1))
		mock.ExpectQuery("FROM accounts a\\s+LEFT JOIN transactions t[\\s\\S]*t\\.date <= \\$3").
			WithArgs(userID, "2026-01-01", clamped).
			WillReturnRows(pgxmock.NewRows([]string{"id", "name", "currency", "income", "expense"}))
		expectRestOfSummary(mock, userID, "2026-01-01", clamped)
		expectBalanceQuery(mock, userID, clamped, pgxmock.NewRows(balanceCols))
		mock.ExpectCommit()

		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet,
			"/dashboard/summary?dateFrom=2026-01-01&dateTo=2026-02-15&asOf=2026-03-31", nil))

		require.Equal(t, http.StatusOK, w.Code)
		var summary models.DashboardSummary
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &summary))

		// The echo is the RESOLVED instant too, so a client caching the response
		// is told what it is actually looking at.
		require.NotNil(t, summary.AsOf)
		assert.Equal(t, clamped, *summary.AsOf)
		require.NotNil(t, summary.Balances)
		assert.NotContains(t, w.Body.String(), "2026-03-31")
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	// A balance query that fails must be a 500 like every other read in the
	// handler. Returning a summary with a silently empty balances list would be
	// the worst version of this feature: a report that says every account was
	// empty on the day asked about.
	t.Run("a balance query failure is a 500", func(t *testing.T) {
		mock, err := newGuardedPool(t)
		require.NoError(t, err)
		defer mock.Close()
		r := newDashboardTestRouter(newTestServer(mock))

		mock.ExpectBeginTx(pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
		mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM accounts WHERE user_id").
			WithArgs(userID).
			WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(1))
		mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM transactions t[\\s\\S]*AND t\\.date <= \\$2").
			WithArgs(userID, asOfDay).
			WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(1))
		mock.ExpectQuery("FROM accounts a\\s+LEFT JOIN transactions t[\\s\\S]*t\\.date <= \\$2").
			WithArgs(userID, asOfDay).
			WillReturnRows(pgxmock.NewRows([]string{"id", "name", "currency", "income", "expense"}))
		expectRestOfSummary(mock, userID, asOfDay)
		mock.ExpectQuery(balanceQueryRegex).
			WithArgs(userID, asOfDay).
			WillReturnError(assert.AnError)

		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/dashboard/summary?asOf="+asOfDay, nil))

		assert.Equal(t, http.StatusInternalServerError, w.Code)
		// No commit is expected: the handler returns mid-transaction and the
		// deferred Rollback is what closes it.
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	// The instant is validated before the transaction opens, so a bad one costs
	// no query at all. Registered expectations are the ordinary summary's, and
	// pgxmock refuses a Begin that was not expected — so an implementation that
	// opened the transaction before validating would fail here rather than
	// quietly accept the value.
	t.Run("a malformed instant is a 400 before any query", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		require.NoError(t, err)
		defer mock.Close()
		r := newDashboardTestRouter(newTestServer(mock))

		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/dashboard/summary?asOf=01/03/2026", nil))

		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.True(t, strings.Contains(w.Body.String(), "asOf"), "the 400 names the offending parameter")
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}
