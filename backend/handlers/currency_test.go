package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/pashagolub/pgxmock/v5"

	"github.com/fintrak/backend/internal/money"
)

func TestParseCurrencyNormalisesCaseAndRejectsAnythingElse(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cases := []struct {
		raw      string
		want     string
		wantCode int
	}{
		{raw: "", want: "", wantCode: 0},
		{raw: "USD", want: "USD", wantCode: 0},
		{raw: "usd", want: "USD", wantCode: 0},
		{raw: "uSd", want: "USD", wantCode: 0},
		// TrimSpace is load-bearing and the URL is escaped, so the row really
		// carries the surrounding spaces rather than a percent-encoded pair.
		{raw: " usd ", want: "USD", wantCode: 0},
		{raw: "   ", want: "", wantCode: 0},
		{raw: "US", want: "", wantCode: http.StatusBadRequest},
		{raw: "USDD", want: "", wantCode: http.StatusBadRequest},
		{raw: "US1", want: "", wantCode: http.StatusBadRequest},
		{raw: "12", want: "", wantCode: http.StatusBadRequest},
	}

	for _, tc := range cases {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, "/?currency="+url.QueryEscape(tc.raw), nil)

		got, ok := parseCurrency(c)

		if tc.wantCode != 0 {
			if ok {
				t.Errorf("currency=%q was accepted, want a 400", tc.raw)
			}
			if w.Code != tc.wantCode {
				t.Errorf("currency=%q answered %d, want %d", tc.raw, w.Code, tc.wantCode)
			}
			continue
		}
		if !ok || got != tc.want {
			t.Errorf("currency=%q = %q, %t; want %q, true", tc.raw, got, ok, tc.want)
		}
	}
}

func TestCurrencyScopeFoldsPerCurrencyTotalsAndNamesEveryAccount(t *testing.T) {
	mock, err := newGuardedPool(t)
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()
	srv := newTestServer(mock)

	acctINR := uuid.New()
	acctUSD := uuid.New()

	mock.ExpectQuery("FROM accounts a\\s+LEFT JOIN transactions t").
		WithArgs(testUserID(), "2026-07-01", "2026-07-31").
		WillReturnRows(pgxmock.NewRows([]string{"id", "name", "currency", "income", "expense"}).
			AddRow(acctINR, "Salary account", "INR", money.FromFloat(50000), money.FromFloat(30000.50)).
			// The account with nothing in the window still appears: the date
			// filter lives in the JOIN's ON clause, not the WHERE.
			AddRow(acctUSD, "Travel card", "USD", 0, money.FromFloat(80)))

	got, err := srv.currencyScope(context.Background(), mock, testUserID(), scopeOptions{
		DateFrom: "2026-07-01", DateTo: "2026-07-31",
	})
	if err != nil {
		t.Fatalf("currencyScope: %v", err)
	}

	if len(got.Scope.Currencies) != 2 {
		t.Fatalf("Currencies = %v, want [INR USD]", got.Scope.Currencies)
	}
	if got.Scope.Currencies[0] != "INR" || got.Scope.Currencies[1] != "USD" {
		t.Errorf("Currencies = %v, want [INR USD]", got.Scope.Currencies)
	}
	if len(got.Scope.Accounts) != 2 {
		t.Fatalf("got %d accounts, want 2", len(got.Scope.Accounts))
	}
	if got.Income["INR"] != money.FromFloat(50000) {
		t.Errorf("Income[INR] = %v, want 50000", got.Income["INR"])
	}
	// A foreign account that only ever spends must still hold a key, or its
	// net would vanish.
	if _, ok := got.Income["USD"]; ok {
		t.Error("Income holds a USD key for an account with no credits")
	}
	if got.Expense["USD"] != money.FromFloat(80) {
		t.Errorf("Expense[USD] = %v, want 80", got.Expense["USD"])
	}
	if got.Net()["USD"] != -money.FromFloat(80) {
		t.Errorf("Net[USD] = %v, want -80", got.Net()["USD"])
	}
	if got.Scope.Accounts[1].Currency != "USD" {
		t.Errorf("account currency = %q, want USD", got.Scope.Accounts[1].Currency)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Error(err)
	}
}

func TestCurrencyScopeAppliesAccountAndCurrencyFilters(t *testing.T) {
	mock, err := newGuardedPool(t)
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()
	srv := newTestServer(mock)

	acct := uuid.New()
	// The account id arrives from the query string, so the bound argument is a
	// string; pgxmock compares arguments by type, hence String() here.
	mock.ExpectQuery("a.id = \\$2").
		WithArgs(testUserID(), acct.String(), "USD").
		WillReturnRows(pgxmock.NewRows([]string{"id", "name", "currency", "income", "expense"}).
			AddRow(acct, "Travel card", "USD", 0, 0))

	if _, err := srv.currencyScope(context.Background(), mock, testUserID(), scopeOptions{
		AccountID: acct.String(), Currency: "USD",
	}); err != nil {
		t.Fatalf("currencyScope: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Error(err)
	}
}

func TestCurrencyScopeKeepsTheDateFilterInTheJoinNotTheWhere(t *testing.T) {
	mock, err := newGuardedPool(t)
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()
	srv := newTestServer(mock)

	// The regexp pins both halves: the dates are the last predicates of the
	// JOIN's ON clause, and the WHERE that follows carries the user predicate
	// and nothing else, so a date leaking into the WHERE breaks it as surely as
	// one dropped from the ON. With the filter in the WHERE, an account quiet
	// this month drops out of the scope and its currency is reported absent.
	mock.ExpectQuery("LEFT JOIN transactions t ON [^\\n]*t\\.date >= \\$2[^\\n]*t\\.date <= \\$3\\s+WHERE a\\.user_id = \\$1\\s+GROUP BY").
		WithArgs(testUserID(), "2026-07-01", "2026-07-31").
		WillReturnRows(pgxmock.NewRows([]string{"id", "name", "currency", "income", "expense"}))

	if _, err := srv.currencyScope(context.Background(), mock, testUserID(), scopeOptions{
		DateFrom: "2026-07-01", DateTo: "2026-07-31",
	}); err != nil {
		t.Fatalf("currencyScope: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Error(err)
	}
}

func TestCurrencyScopeReadsTheProjectedCurrencyEverywhere(t *testing.T) {
	mock, err := newGuardedPool(t)
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()
	srv := newTestServer(mock)

	// All three sites in one regexp, in the order they appear in the statement:
	// the projection, the currency predicate the caller splices in, and the
	// GROUP BY. They have to be the same expression because a backup import
	// writes a bundle's empty currency through verbatim (backup.go scans
	// COALESCE(currency, '') and inserts it unchanged), so an account can hold
	// "". One site reading a bare COALESCE(a.currency, ...) while the other two
	// keep the NULLIF drops that account from an explicit ?currency=INR report —
	// the very silence the scopeSQL comment rules out.
	projected := `COALESCE\(NULLIF\(a\.currency, ''\), 'INR'\)`
	statement := "SELECT a\\.id, a\\.name, " + projected + " AS currency" +
		"[\\s\\S]*? AND " + projected + " = \\$2" +
		"[\\s\\S]*?GROUP BY a\\.id, a\\.name, " + projected
	mock.ExpectQuery(statement).
		WithArgs(testUserID(), "INR").
		WillReturnRows(pgxmock.NewRows([]string{"id", "name", "currency", "income", "expense"}).
			AddRow(uuid.New(), "Unset account", "INR", 0, 0))

	if _, err := srv.currencyScope(context.Background(), mock, testUserID(), scopeOptions{
		Currency: "INR",
	}); err != nil {
		t.Fatalf("currencyScope: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Error(err)
	}
}

func TestCurrencyScopeReportsAFailedQueryRatherThanAnEmptyScope(t *testing.T) {
	mock, err := newGuardedPool(t)
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()
	srv := newTestServer(mock)

	mock.ExpectQuery("FROM accounts a").WithArgs(testUserID()).WillReturnError(errors.New("connection reset"))

	res, err := srv.currencyScope(context.Background(), mock, testUserID(), scopeOptions{})
	if err == nil {
		t.Fatal("a failed query returned no error")
	}
	// An empty scope is indistinguishable from "this window spans no
	// currency", so the error has to travel rather than fold into a zero
	// result the caller would report as a quiet month.
	if len(res.Scope.Currencies) != 0 {
		t.Errorf("Currencies = %v on a failed query, want none", res.Scope.Currencies)
	}
}

func TestCurrencyScopePropagatesARowError(t *testing.T) {
	mock, err := newGuardedPool(t)
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()
	srv := newTestServer(mock)

	boom := errors.New("connection reset mid-scan")
	mock.ExpectQuery("FROM accounts a").
		WithArgs(testUserID()).
		WillReturnRows(pgxmock.NewRows([]string{"id", "name", "currency", "income", "expense"}).
			AddRow(uuid.New(), "Salary account", "INR", 0, 0).
			RowError(1, boom))

	// A half-read fold would report the accounts that did arrive and drop the
	// rest, which is the same silence as a quiet window; the error has to travel.
	res, err := srv.currencyScope(context.Background(), mock, testUserID(), scopeOptions{})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want %v", err, boom)
	}
	if len(res.Scope.Accounts) != 0 {
		t.Errorf("Accounts = %v on a row error, want none", res.Scope.Accounts)
	}
}

func TestCurrencyScopePropagatesAnUnreadableColumn(t *testing.T) {
	mock, err := newGuardedPool(t)
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()
	srv := newTestServer(mock)

	// A BIGINT of cents cannot arrive as this, so the Scan failure stands in for
	// any mis-typed or truncated column: the fold must stop rather than carry on
	// with a zero it invented.
	mock.ExpectQuery("FROM accounts a").
		WithArgs(testUserID()).
		WillReturnRows(pgxmock.NewRows([]string{"id", "name", "currency", "income", "expense"}).
			AddRow(uuid.New(), "Salary account", "INR", "not-an-amount", 0))

	if _, err := srv.currencyScope(context.Background(), mock, testUserID(), scopeOptions{}); err == nil {
		t.Fatal("an unreadable income column returned no error")
	}
}

func TestCurrencyScopeAlwaysReturnsAnEmptyCurrenciesSlice(t *testing.T) {
	mock, err := newGuardedPool(t)
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()
	srv := newTestServer(mock)

	mock.ExpectQuery("FROM accounts a").
		WithArgs(testUserID()).
		WillReturnRows(pgxmock.NewRows([]string{"id", "name", "currency", "income", "expense"}))

	got, err := srv.currencyScope(context.Background(), mock, testUserID(), scopeOptions{})
	if err != nil {
		t.Fatalf("currencyScope: %v", err)
	}
	if got.Scope.Currencies == nil {
		t.Error("Currencies is nil; a response must carry [] rather than null")
	}
	if len(got.Scope.Currencies) != 0 {
		t.Errorf("Currencies = %v, want empty", got.Scope.Currencies)
	}
}
