package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
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
		{raw: "US", want: "", wantCode: http.StatusBadRequest},
		{raw: "USDD", want: "", wantCode: http.StatusBadRequest},
		{raw: "US1", want: "", wantCode: http.StatusBadRequest},
		{raw: "12", want: "", wantCode: http.StatusBadRequest},
	}

	for _, tc := range cases {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, "/?currency="+tc.raw, nil)

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
	mock, err := pgxmock.NewPool()
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
	mock, err := pgxmock.NewPool()
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
	mock, err := pgxmock.NewPool()
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

func TestCurrencyScopeFiltersOnTheProjectedCurrencyNotTheRawColumn(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()
	srv := newTestServer(mock)

	// The predicate has to be the SELECT's own expression, NULLIF included: a
	// backup import writes a bundle's empty currency through verbatim
	// (backup.go scans COALESCE(currency, '')), so an account can hold "". It is
	// projected as INR everywhere else, and a bare COALESCE(a.currency, 'INR')
	// here would leave it out of an explicit ?currency=INR report — the very
	// silence the comment on the fragment rules out.
	mock.ExpectQuery("COALESCE\\(NULLIF\\(a\\.currency, ''\\), 'INR'\\) = \\$2").
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
	mock, err := pgxmock.NewPool()
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

func TestCurrencyScopeAlwaysReturnsAnEmptyCurrenciesSlice(t *testing.T) {
	mock, err := pgxmock.NewPool()
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
