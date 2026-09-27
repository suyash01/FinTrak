# Multi-Currency Aggregates Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Every amount on the five aggregate reporting endpoints becomes a currency-keyed map, so no total is ever computed across currencies; plus a `ccy:` query field, and three documentation corrections.

**Architecture:** A new `models.CurrencyAmounts` (`map[string]money.Amount`) replaces `money.Amount` on every amount field of `/dashboard/summary`, `/dashboard/money-flow`, `/dashboard/money-flow/timeline`, `/dashboard/cash-flow-calendar` and `/links/cycles`. One shared query, `currencyScope`, produces both the per-currency headline totals and a `currencyScope` block naming the accounts behind each currency, so a total can never describe a scope the response does not also name. A new `currency` query parameter scopes a report to one currency. The map propagates verbatim into `client/api`, the MCP tools, the TUI and the React app, each of which renders one currency at a time and says what it is not showing.

**Tech Stack:** Go 1.27 / Gin / pgx / pgxmock (backend); Go module `github.com/fintrak/client` (shared API client); Go module `github.com/fintrak/tui` (Bubble Tea v2); Go module `github.com/fintrak/mcp` (official MCP Go SDK); React 19 / TypeScript / Vite / Vitest (frontend); `backend/openapi.yaml` as the machine-readable contract.

**Spec:** `docs/superpowers/specs/2026-09-27-multi-currency-aggregates-design.md` — read it before starting; this plan argues from it.

## Global Constraints

- **Money is never `float64`.** Amounts are `money.Amount` (int64 minor units) in the backend and `api.Amount` (decimal text) in `client/api`. `Float64()` is display-only (bar widths, heatmap intensity) and is already documented as such on both types.
- **No exchange rates, no conversion, no base currency.** This change refuses a number it cannot compute; it never approximates one.
- **No `combined` boolean.** `len(currencyScope.currencies) == 1` is the answer. The repository does not keep derivable state.
- **An aggregate with no matching transactions returns `{}`, and a missing key reads as zero** — never as an error, never as absent data.
- **Accounts with no transactions in the window must still appear in `currencyScope`.** The date filter belongs in the `LEFT JOIN ... ON` clause, never in the `WHERE`.
- **Every logging call uses typed attrs** — `slog.String("error", err.Error())`, never the bare key/value form.
- **Handlers take dependencies from the explicit `handlers.Server`; there is no package global.** Tests build a `Server` over a `pgxmock` pool via `newMockServer(t)`.
- **Tests that hit SQL expect exact queries and args against the mock.** Changing a query string or arg order fails the test by design.
- **Every `gin.SetMode(gin.TestMode)`** in a new test file that builds a router.
- **Coverage floors are release gates:** backend 85%, `client/api` 85%, TUI 18%, MCP 80%, frontend 85%. New code must carry its share.
- **No new table, so no `BackupBundle` work.** `readonly.SideEffectingGETs` is unchanged — no route, verb or write changes in this plan.

## Review Focus

Five input classes the spec implies that no task's happy-path test exercises. Each has a test added to the task that owns the code.

1. **`accounts.currency` is `VARCHAR(3) DEFAULT 'INR'` with no `NOT NULL`** (`backend/db/migrations/000001_initial_schema.up.sql:104`), so a NULL or empty code is representable. A NULL reaching the map becomes a `""` key, which the frontend hands to `Intl.NumberFormat` and which renders as a bare number. **Expected: every account reports a real code; a NULL reads as `INR`.** Test in Task 2 (SQL uses `COALESCE`) and Task 13 (the formatter refuses an empty code).
2. **A lower- or mixed-case `?currency=usd` or `ccy:usd`** binds against `'USD'` and matches nothing, so the report renders as zeros with no error at all. **Expected: normalised to upper case, so `usd` and `USD` are the same request.** Test in Task 2 and Task 3.
3. **A currency code the browser's `Intl.NumberFormat` rejects** (an account set to `XYZ`) throws `RangeError` from `formatCurrency` and takes the whole dashboard down. **Expected: the UI degrades to `XYZ 1,234.00` rather than throwing.** Test in Task 13.
4. **A currency present in expenses but absent from income** — a foreign account that only ever spends. `totalIncome` has no key for it, and `Sub` must still produce a negative `net` for it rather than omitting the currency. **Expected: `net` covers the union of the key sets.** Test in Task 1.
5. **The per-currency top-15 window function over a `LEFT JOIN`** partitions on the account's currency, and the categories with no matching transactions land in their own `NULL` partition where `ROW_NUMBER` restarts at 1 — so a category with no spend can pass a `<= 15` filter it should have failed. **Expected: only categories with a positive total are returned, as before.** Test in Task 4.

---

## File Structure

**New files**

| File | Responsibility |
| --- | --- |
| `backend/handlers/currency.go` | The `currency` query-parameter validation and the one shared `currencyScope` query every reporting endpoint uses. |
| `backend/handlers/currency_test.go` | Tests for both of the above. |
| `client/api/currency.go` | `api.CurrencyAmounts` and its methods, the client-side twin of `models.CurrencyAmounts`. |
| `client/api/currency_test.go` | Its tests, including that `Display` refuses to pick a currency. |
| `frontend/src/lib/currency.ts` | `CurrencyAmounts` type, `useCurrencyScope` hook, `formatScoped` and `formatScopedMulti`. |
| `frontend/src/lib/currency.test.ts` | Their tests, including the `Intl`-rejecting code. |
| `frontend/src/components/MultiCurrencyNotice/MultiCurrencyNotice.tsx` | The notice naming the currencies the current report is not showing. |

**Modified files** — `backend/models/models.go`, `backend/handlers/{dashboard,money_flow,money_flow_timeline,cash_flow_calendar,link_cycles}.go` and their `_test.go` files, `backend/internal/query/{fields,compile}.go`, `backend/internal/query/testdata/corpus.json`, `backend/openapi.yaml`, `client/api/{types,endpoints_dashboard,endpoints_links,spec_parity_test}.go`, `mcp/internal/mcpserver/{mcpserver,tools_dashboard,tools_links,tools_transactions}.go`, `mcp/internal/mcpserver/tools_test.go`, `tui/internal/ui/{dashboard,moneyflow,calendar}.go`, `tui/internal/ui/currency_test.go`, `frontend/src/types.ts`, `frontend/src/lib/query/fields.ts`, `frontend/src/lib/query/parse.ts`, `frontend/src/components/{Dashboard,MoneyFlow,CashFlowCalendar}/*.tsx` and their tests, `backend/integration_test.go`, `README.md`, `AGENTS.md`, `docs/feature-proposals.md`.

---

### Task 1: `CurrencyAmounts` in the backend models

The type every later task builds on. No handler changes, so the module stays green.

**Files:**
- Modify: `backend/models/models.go` (append at the end)
- Test: `backend/models/models_test.go`

**Interfaces:**
- Consumes: `money.Amount` (`backend/internal/money/money.go:19`) — an `int64` of minor units, so `+` and `-` are exact integer arithmetic.
- Produces: `models.CurrencyAmounts`, `models.CurrencyScope`, `models.ScopedAccount`, and the methods `NewCurrencyAmounts`, `Add`, `Single`, `Currencies`, `Sub`. Tasks 2–8 use all of them.

- [ ] **Step 1: Write the failing test**

Append to `backend/models/models_test.go`:

```go
func TestCurrencyAmountsSingleRefusesMoreThanOneCurrency(t *testing.T) {
	m := CurrencyAmounts{"INR": 500000, "USD": 12000}

	if _, _, ok := m.Single(); ok {
		t.Error("a two-currency map reported a single amount")
	}

	code, value, ok := CurrencyAmounts{"INR": 500000}.Single()
	if !ok || code != "INR" || value != 500000 {
		t.Errorf("Single() = %q, %d, %t; want \"INR\", 500000, true", code, value, ok)
	}

	if _, _, ok := (CurrencyAmounts{}).Single(); ok {
		t.Error("an empty map reported a single amount")
	}
	if _, _, ok := (nil).Single(); ok {
		t.Error("a nil map reported a single amount")
	}
}

func TestCurrencyAmountsSubTreatsAMissingKeyAsZero(t *testing.T) {
	// A foreign account that only ever spends: the income map has no key for
	// it, and the difference must still name it rather than drop it.
	got := CurrencyAmounts{"INR": 500000}.Sub(CurrencyAmounts{"INR": 200000, "USD": 8000})

	if len(got) != 2 {
		t.Fatalf("Sub returned %d keys, want 2: %v", len(got), got)
	}
	if got["INR"] != 300000 {
		t.Errorf("INR = %d, want 300000", got["INR"])
	}
	if got["USD"] != -8000 {
		t.Errorf("USD = %d, want -8000", got["USD"])
	}
}

func TestCurrencyAmountsAddAllocatesFromNil(t *testing.T) {
	var m CurrencyAmounts
	m = m.Add("INR", 100)
	m = m.Add("INR", 250)
	m = m.Add("USD", 700)

	if m["INR"] != 350 || m["USD"] != 700 {
		t.Errorf("folded to %v, want INR 350 and USD 700", m)
	}
}

func TestCurrencyAmountsAddSkipsAZeroContribution(t *testing.T) {
	// A missing key must read as zero, so a currency with no money in it is
	// absent rather than present-and-zero. That is what makes len() mean "how
	// many currencies this aggregate touched" — every account's currency
	// appearing in every total even with nothing in it would make it
	// meaningless — and it is the asymmetry Sub relies on for an account that
	// only ever spends: it contributes a key to the expense side and none to
	// the income side, and Sub still produces its negative net.
	m := NewCurrencyAmounts()
	m = m.Add("INR", 0)
	if len(m) != 0 {
		t.Errorf("a zero contribution created the key %v", m)
	}

	m = m.Add("INR", money.FromFloat(80))
	m = m.Add("USD", money.FromFloat(80))
	if got := m.Currencies(); len(got) != 2 || got[0] != "INR" || got[1] != "USD" {
		t.Errorf("Currencies() = %v, want [INR USD]", got)
	}
}

func TestCurrencyAmountsCurrenciesIsSorted(t *testing.T) {
	got := CurrencyAmounts{"USD": 1, "INR": 2, "EUR": 3}.Currencies()

	want := []string{"EUR", "INR", "USD"}
	if len(got) != len(want) {
		t.Fatalf("Currencies() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Currencies() = %v, want %v (map order must never reach a UI)", got, want)
		}
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd backend && go test ./models/ -run TestCurrencyAmounts -count=1`
Expected: FAIL to compile — `undefined: CurrencyAmounts`.

- [ ] **Step 3: Write the implementation**

Append to `backend/models/models.go`:

```go
// CurrencyAmounts is one aggregate's value, keyed by the currency code of each
// account that contributed to it.
//
// accounts.currency is stored and validated, but transactions carry no currency
// of their own, so an aggregate over more than one account can span currencies
// and a single number cannot represent that. So there is no single number: every
// amount on the reporting endpoints is one of these. One key means the value is
// exact for the whole scope it covers. More than one means no total exists, and a
// consumer must either choose a currency or say that it cannot add them.
//
// Addition within a currency is meaningful and is what Sub performs. Addition
// *across* keys is not, and no method here does it: nothing in this package
// collapses a CurrencyAmounts into one amount, because that is the number this
// type exists to refuse.
type CurrencyAmounts map[string]money.Amount

// ScopedAccount is one account inside a response's scope, with the per-currency
// income and expense it contributes. It is what lets a client say what a
// currency it is not currently displaying is worth, rather than dropping it
// silently.
type ScopedAccount struct {
	ID       uuid.UUID       `json:"id"`
	Name     string          `json:"name"`
	Currency string          `json:"currency"`
	Income   CurrencyAmounts `json:"income"`
	Expense  CurrencyAmounts `json:"expense"`
}

// CurrencyScope names every currency a response covers and the accounts behind
// each one. A reporting response carries it so a mixed-currency result explains
// itself: the caller can see that USD 120.00 exists, and which account holds it,
// instead of receiving a total that quietly dropped it.
type CurrencyScope struct {
	// Currencies holds every code in the response, sorted, so a picker and a
	// test do not depend on map iteration order. It is never nil: a response
	// covering no accounts carries an empty slice, not a null.
	Currencies []string        `json:"currencies"`
	Accounts   []ScopedAccount `json:"accounts"`
}

// NewCurrencyAmounts returns an empty, non-nil map, so a handler folding SQL
// rows into one can never trip over a nil map on the first write.
func NewCurrencyAmounts() CurrencyAmounts { return CurrencyAmounts{} }

// Add folds one account's contribution in and returns the map, so it works both
// on a map from NewCurrencyAmounts (which it mutates in place) and on a nil one
// (which it cannot mutate, so it allocates and hands the new map back).
//
// A zero contribution adds NO key. That is what makes "a missing key reads as
// zero" true everywhere rather than only by convention: a currency with no money
// in it is simply absent, so len() counts the currencies the aggregate actually
// touched, and an account that only ever spends contributes a key to the expense
// side and none to the income side — which is exactly the asymmetry Sub needs to
// still produce its negative net. (Contributions that later cancel to zero leave
// the key in place; a present zero and an absent key are the same value to every
// reader, and removing keys mid-accumulation would be a surprising thing for a
// method named Add to do.)
func (m CurrencyAmounts) Add(currency string, amount money.Amount) CurrencyAmounts {
	if amount == 0 {
		return m
	}
	if m == nil {
		m = CurrencyAmounts{}
	}
	m[currency] += amount
	return m
}

// Single returns the map's only entry, and ok=false whenever it does not hold
// exactly one currency — including when it is empty or nil. It is the answer to
// "may this be treated as a single number", and the only sanctioned way to reach
// a bare amount out of this type.
func (m CurrencyAmounts) Single() (string, money.Amount, bool) {
	if len(m) != 1 {
		return "", 0, false
	}
	for code, amount := range m {
		return code, amount, true
	}
	return "", 0, false
}

// Currencies returns the codes in sorted order.
func (m CurrencyAmounts) Currencies() []string {
	out := make([]string, 0, len(m))
	for code := range m {
		out = append(out, code)
	}
	sort.Strings(out)
	return out
}

// Sub returns the per-currency difference m - other. A currency missing from
// either operand counts as zero and the result carries the union of the key
// sets, so a currency that appears in only one operand still yields a
// well-defined difference — a foreign account that only spends gets a negative
// net rather than disappearing — and no key is ever invented.
func (m CurrencyAmounts) Sub(other CurrencyAmounts) CurrencyAmounts {
	out := make(CurrencyAmounts, len(m)+len(other))
	for code, amount := range m {
		out[code] += amount
	}
	for code, amount := range other {
		out[code] -= amount
	}
	return out
}
```

If `models.go` does not already import `sort` and `github.com/google/uuid`, add them.

- [ ] **Step 4: Run the test to verify it passes**

Run: `cd backend && go test ./models/ -count=1`
Expected: PASS.

- [ ] **Step 5: Verify the whole backend still builds**

Run: `cd backend && go build ./... && go vet ./models/`
Expected: no output.

- [ ] **Step 6: Commit**

```bash
git add backend/models/models.go backend/models/models_test.go
git commit -m "feat(models): add CurrencyAmounts, the per-currency amount map

Every amount on the reporting endpoints becomes one of these, so no
total can be computed across currencies. Sub treats a missing key as
zero and returns the union, which is what keeps a foreign account that
only spends visible as a negative net."
```

---

### Task 2: The `currency` parameter and the shared `currencyScope` query

**Files:**
- Create: `backend/handlers/currency.go`
- Create: `backend/handlers/currency_test.go`

**Interfaces:**
- Consumes: `models.CurrencyAmounts`, `models.CurrencyScope`, `models.ScopedAccount` (Task 1); `validation.RespondError`.
- Produces:
  - `func parseCurrency(c *gin.Context) (string, bool)` — returns the normalised upper-case code, or `("", false)` after having answered 400.
  - `type scopeOptions struct { DateFrom, DateTo, AccountID, Currency string }`
  - `type scopeResult struct { Scope models.CurrencyScope; Income, Expense models.CurrencyAmounts }` with `func (r scopeResult) Net() models.CurrencyAmounts`.
  - `func (s *Server) currencyScope(ctx context.Context, q scopeQueryer, userID uuid.UUID, opts scopeOptions) (scopeResult, error)`
  - `type scopeQueryer interface { Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) }`
  - `const scopeSQL` — the exact statement, so tests can pin it.
- Tasks 4–8 call `parseCurrency` and `currencyScope`.

- [ ] **Step 1: Write the failing test**

Create `backend/handlers/currency_test.go`:

```go
package handlers

import (
	"context"
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

// TestCurrencyScopeReadsTheProjectedCurrencyEverywhere pins the invariant the
// whole feature rests on: the SELECT projection, the `?currency=` predicate and
// the GROUP BY must all read COALESCE(NULLIF(a.currency, ''), 'INR').
//
// accounts.currency is nullable, and the "" that a restored backup bundle can
// carry reaches the column verbatim. If the WHERE omitted the NULLIF that the
// SELECT has, `?currency=INR` would exclude exactly the account the filter
// exists to find while the rest of the same response projected it as INR — a
// silent drop of one row, with no error, which is the class of defect this
// entire change was written to eliminate.
//
// One assertion spanning the whole statement, because three narrow ones would
// each pass while the other two sites drifted: a reader cannot tell from a test
// named for this invariant that it only ever checked the WHERE.
//
// The options carry a Currency on purpose. With none, the predicate never
// renders, so the middle segment has nothing to match and the guard silently
// narrows back to the two sites it was written to close.
func TestCurrencyScopeReadsTheProjectedCurrencyEverywhere(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()
	srv := newTestServer(mock)

	projected := `COALESCE\(NULLIF\(a\.currency, ''\), 'INR'\)`
	statement := "SELECT a\\.id, a\\.name, " + projected + " AS currency" +
		"[\\s\\S]*? AND " + projected + " = \\$2" +
		"[\\s\\S]*?GROUP BY a\\.id, a\\.name, " + projected
	mock.ExpectQuery(statement).
		WithArgs(testUserID(), "INR").
		WillReturnRows(pgxmock.NewRows([]string{"id", "name", "currency", "income", "expense"}).
			AddRow(uuid.New(), "Travel card", "INR", 0, 0))

	if _, err := srv.currencyScope(context.Background(), mock, testUserID(), scopeOptions{
		Currency: "INR",
	}); err != nil {
		t.Fatalf("currencyScope: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Error(err)
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
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd backend && go test ./handlers/ -run 'TestParseCurrency|TestCurrencyScope' -count=1`
Expected: FAIL to compile — `undefined: parseCurrency`, `undefined: currencyScope`, `undefined: scopeOptions`.

- [ ] **Step 3: Write the implementation**

Create `backend/handlers/currency.go`:

```go
package handlers

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/fintrak/backend/internal/money"
	"github.com/fintrak/backend/internal/validation"
	"github.com/fintrak/backend/models"
)

// scopeSQL is the one query behind every reporting endpoint's per-currency
// totals and its currencyScope block. It reads accounts, not transactions,
// because the currency lives on the account and a transaction has none: the
// answer to "which currencies does this report span" is a question about the
// accounts in the window.
//
// The two filter fragments are spliced into different clauses on purpose. Date
// bounds belong in the JOIN's ON, so an account with no transactions in the
// window still appears in the scope — moving them to the WHERE would drop it,
// and the caller would be told a currency is absent when the account holding it
// is simply quiet this month.
//
// `COALESCE(NULLIF(a.currency, ''), 'INR')` appears THREE times and all three
// must stay identical: the SELECT projection, the GROUP BY, and the `currency`
// predicate appended in currencyScope. accounts.currency is
// `VARCHAR(3) DEFAULT 'INR'` with no NOT NULL, so NULL is representable, and
// NULLIF additionally covers the `''` a restored backup bundle can carry
// (backup.go scans COALESCE(currency, '') and re-inserts it verbatim). If the
// WHERE omits the NULLIF that the SELECT has, `?currency=INR` would exclude
// exactly the account the filter exists to find, while the rest of the same
// response projects it as INR. The literal is repeated rather than interpolated
// from a const so the SQL has exactly two %s, matching the two filter
// fragments; a third %s for the default would make the count fragile against
// any future edit to this statement.
const scopeSQL = `SELECT a.id, a.name, COALESCE(NULLIF(a.currency, ''), 'INR') AS currency,
	  COALESCE(SUM(t.amount) FILTER (WHERE t.type = 'credit'), 0) AS income,
	  COALESCE(SUM(t.amount) FILTER (WHERE t.type = 'debit'), 0) AS expense
	FROM accounts a
	LEFT JOIN transactions t ON t.account_id = a.id AND t.user_id = $1%s
	WHERE a.user_id = $1%s
	GROUP BY a.id, a.name, COALESCE(NULLIF(a.currency, ''), 'INR')
	ORDER BY a.name, a.id`

// defaultCurrency is the code an account is read as when its own is unset, and
// it must match the literal inside scopeSQL's three
// COALESCE(NULLIF(a.currency, ''), ...) expressions. See that comment for why
// all three have to agree, and for why the literal is repeated rather than
// interpolated from here.
const defaultCurrency = "INR"

// scopeOptions is the window one aggregate covers. It is the reporting
// endpoints' query parameters, in the shape the shared query wants.
type scopeOptions struct {
	DateFrom  string
	DateTo    string
	AccountID string
	// Currency is already normalised by parseCurrency: upper case, or empty.
	Currency string
}

// scopeResult is what one currencyScope query yields: the scope the client is
// told about, and the two headline totals folded from the same rows. They travel
// together on purpose — a total that could describe a scope the response does not
// name would be exactly the silent gap this change exists to close.
type scopeResult struct {
	Scope   models.CurrencyScope
	Income  models.CurrencyAmounts
	Expense models.CurrencyAmounts
}

// Net is the per-currency difference, computed here so no client ever subtracts
// two maps by hand. A currency with expenses but no income still gets a key,
// because Sub counts a missing operand as zero.
func (r scopeResult) Net() models.CurrencyAmounts { return r.Income.Sub(r.Expense) }

// scopeQueryer is the pool surface currencyScope needs, so a test can hand it a
// pgxmock pool directly.
type scopeQueryer interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// parseCurrency reads the `currency` query parameter and normalises it to the
// upper-case three-letter form the accounts.currency column stores. Case is
// folded rather than compared case-sensitively because the failure is invisible:
// "usd" would bind against 'USD', match nothing, and render a report of zeros
// with no error anywhere. A code that is not three ASCII letters is a 400,
// naming the shape, because silently ignoring it would be the same silence.
func parseCurrency(c *gin.Context) (string, bool) {
	raw := strings.TrimSpace(c.Query("currency"))
	if raw == "" {
		return "", true
	}
	code := strings.ToUpper(raw)
	if len(code) != 3 {
		validation.RespondError(c, "invalid currency", 400)
		return "", false
	}
	for i := range len(code) {
		if code[i] < 'A' || code[i] > 'Z' {
			validation.RespondError(c, "invalid currency", 400)
			return "", false
		}
	}
	return code, true
}

// currencyScope runs scopeSQL once and returns the response's currency scope
// alongside the per-currency income and expense it folds from the same rows.
func (s *Server) currencyScope(ctx context.Context, q scopeQueryer, userID uuid.UUID, opts scopeOptions) (scopeResult, error) {
	// joinConds lands in the LEFT JOIN's ON clause and whereConds in the WHERE.
	// Keeping them apart is what stops a date filter from excluding an account
	// that simply has no transactions in the window.
	var (
		joinConds  []string
		whereConds []string
		args       []any
	)
	param := 2
	// add appends one already-formatted condition and its bound value. The
	// caller builds the $n placeholder from param rather than letting add do
	// it, because the currency predicate has to splice the column default into
	// the fragment as well and one fmt.Sprintf cannot fill both a %s and a %d
	// from different sources.
	add := func(conds *[]string, fragment string, value any) {
		*conds = append(*conds, fragment)
		args = append(args, value)
		param++
	}
	if opts.DateFrom != "" {
		add(&joinConds, fmt.Sprintf(" AND t.date >= $%d", param), opts.DateFrom)
	}
	if opts.DateTo != "" {
		add(&joinConds, fmt.Sprintf(" AND t.date <= $%d", param), opts.DateTo)
	}
	if opts.AccountID != "" {
		add(&whereConds, fmt.Sprintf(" AND a.id = $%d", param), opts.AccountID)
	}
	if opts.Currency != "" {
		// NULLIF must appear here exactly as it does in the SELECT's projection
		// and its GROUP BY. Without it this predicate would exclude an account
		// whose currency is '' while every other part of the same response
		// projected it as INR — a filter that silently drops the one row it
		// exists to find. backup.go is the path that can store '': it scans
		// COALESCE(currency, '') and re-inserts it verbatim.
		add(&whereConds, fmt.Sprintf(" AND COALESCE(NULLIF(a.currency, ''), '%s') = $%d", defaultCurrency, param), opts.Currency)
	}

	stmt := fmt.Sprintf(scopeSQL, strings.Join(joinConds, ""), strings.Join(whereConds, ""))
	rows, err := q.Query(ctx, stmt, append([]any{userID}, args...)...)
	if err != nil {
		return scopeResult{}, err
	}
	defer rows.Close()

	res := scopeResult{Income: models.NewCurrencyAmounts(), Expense: models.NewCurrencyAmounts()}
	res.Scope.Accounts = []models.ScopedAccount{}
	seen := map[string]bool{}

	for rows.Next() {
		var (
			id               uuid.UUID
			name, code       string
			income, expense  money.Amount
		)
		if err := rows.Scan(&id, &name, &code, &income, &expense); err != nil {
			return scopeResult{}, err
		}
		res.Scope.Accounts = append(res.Scope.Accounts, models.ScopedAccount{
			ID: id, Name: name, Currency: code,
			Income:  models.NewCurrencyAmounts().Add(code, income),
			Expense: models.NewCurrencyAmounts().Add(code, expense),
		})
		res.Income.Add(code, income)
		res.Expense.Add(code, expense)
		seen[code] = true
	}
	if err := rows.Err(); err != nil {
		return scopeResult{}, err
	}

	res.Scope.Currencies = make([]string, 0, len(seen))
	for code := range seen {
		res.Scope.Currencies = append(res.Scope.Currencies, code)
	}
	sort.Strings(res.Scope.Currencies)
	return res, nil
}
```
- [ ] **Step 4: Run the test to verify it passes**

Run: `cd backend && go test ./handlers/ -run 'TestParseCurrency|TestCurrencyScope' -count=1`
Expected: PASS.

- [ ] **Step 5: Run the whole handler suite to confirm nothing else broke**

Run: `cd backend && go test ./handlers/ -count=1`
Expected: PASS — the helper is new, nothing calls it yet.

- [ ] **Step 6: Commit**

```bash
git add backend/handlers/currency.go backend/handlers/currency_test.go
git commit -m "feat(handlers): add the currency parameter and the shared scope query

One query over accounts produces both the per-currency headline totals
and the currencyScope block, so a total can never describe a scope the
response does not name. The currency parameter is case-folded, because
usd would otherwise bind against USD, match nothing and render a report
of zeros with no error anywhere."
```

---

### Task 3: The `ccy:` query field

Independent of the reporting change — it touches the filter grammar, not the aggregates.

**Files:**
- Modify: `backend/internal/query/fields.go` (add `kindCurrency`, add the `ccy` entry, add the validate case)
- Modify: `backend/internal/query/compile.go` (add the `ccy` emitter)
- Modify: `backend/internal/query/testdata/corpus.json` (three cases)
- Modify: `backend/internal/query/compile_test.go` (add `ccy` to the no-join query list)
- Modify: `frontend/src/lib/query/fields.ts` (add `"currency"` to `FieldKind`, add the `ccy` entry)
- Modify: `frontend/src/lib/query/parse.ts` (add the `"currency"` validate case)
- Modify: `frontend/src/lib/query/resolve.ts` (add the `"currency"` case to `resolveQuery`'s switch)
- Modify: `frontend/src/lib/query/QueryInput.tsx` (`describeField`, so the grammar sheet stops advertising `~` on a field that refuses it)
- Test: `frontend/src/lib/query/resolve.test.ts` (a `ccy` round-trip case), plus the corpus.

**`resolve.ts` is the one that makes the field work, and it is easy to miss.** `resolveQuery` switches on `def.kind` to decide what a term *becomes* before it goes on the wire: ids get resolved to uuids, dates get expanded from named periods, tags and amounts pass through. A kind with no `case` **falls out of the switch silently** — the term is never pushed, no diagnostic is produced, and the SPA sends `q=` with the filter simply not applied. The user sees the unfiltered list and no error.

That failure is invisible to `bun run typecheck`, because the switch has no exhaustiveness check, and invisible to the corpus, because `parse.test.ts` calls `parseQuery` directly and never routes through `resolveQuery`. So the whole feature can be "complete" by every test in the report while doing nothing in the product. A new `kind` needs a `case` here, and a `resolve.test.ts` case that pins the round-trip.

The other two are drift guards this language already keeps. `compile_test.go` enumerates the fields whose compiled fragments must not contain a `JOIN` — `ccy` emits a correlated `EXISTS` and belongs in that list, or the "never name a joined table" invariant is unasserted for the one field most likely to want one. `QueryInput.tsx`'s `describeField` renders the grammar sheet from `FIELD_TABLE`'s `ops`, and without the entry the sheet would document `ccy~USD`, which the parser rejects.

**Interfaces:**
- Consumes: nothing from Tasks 1–2.
- Produces: the query field `ccy`, usable as `ccy:USD`, `ccy:usd`, `ccy:USD,EUR`.

- [ ] **Step 1: Add a corpus case and watch it fail**

Append to the `cases` array in `backend/internal/query/testdata/corpus.json`:

Note the second case: the **parser keeps what the user typed** (`values: ["usd"]`, `canonical: "ccy:usd"`) and the **compiler** folds the case when it binds (`args: ["USD"]`). That is the same split `amt` uses — `surface: "amt>=50.75"` keeps its text while `convertAmount` turns it into minor units at compile time — so the TypeScript mirror never has to fold anything, and the fold lives in exactly one place.

```json
    {
      "name": "a currency is matched case-insensitively",
      "surface": "ccy:usd",
      "canonical": "ccy:usd",
      "terms": [{ "field": "ccy", "op": "=", "values": ["usd"], "negated": false, "position": 0 }],
      "diagnostics": [],
      "sql": {
        "clauses": ["(EXISTS (SELECT 1 FROM accounts ac WHERE ac.id = t.account_id AND ac.currency = $2))"],
        "args": ["USD"]
      }
    },
    {
      "name": "a currency that is not three letters is refused",
      "surface": "ccy:US",
      "canonical": "ccy:US",
      "terms": [],
      "diagnostics": [{ "term": "ccy:US", "code": "unresolved_value", "message": "ccy: \"US\" is not a currency code (three letters, e.g. USD)", "position": 0 }]
    },
    {
      "name": "a currency csv is an or set",
      "surface": "ccy:usd,eur",
      "canonical": "ccy:usd,eur",
      "terms": [{ "field": "ccy", "op": "=", "values": ["usd", "eur"], "negated": false, "position": 0 }],
      "diagnostics": [],
      "sql": {
        "clauses": ["(EXISTS (SELECT 1 FROM accounts ac WHERE ac.id = t.account_id AND ac.currency = $2) OR EXISTS (SELECT 1 FROM accounts ac WHERE ac.id = t.account_id AND ac.currency = $3))"],
        "args": ["USD", "EUR"]
      }
    }
```

- [ ] **Step 2: Run the corpus test to verify it fails**

Run: `cd backend && go test ./internal/query/ -run 'TestCorpus' -count=1`
Expected: FAIL — `ccy` is reported as a field with no case, or the case itself fails to parse.

- [ ] **Step 3: Add the field to the backend table**

In `backend/internal/query/fields.go`, add to the `valueKind` block:

```go
	kindCurrency
```

Add to the `fieldTable` map, after the `"acct"` line:

```go
	"ccy":       {userTyped: true, kind: kindCurrency, ops: opsEq},
```

Add a case to `fieldDef.validate`, after the `kindEnum` case:

```go
		case kindCurrency:
			// Three ASCII letters, folded to upper case: the column is
			// VARCHAR(3) and every code stored in it is upper case, so an
			// un-folded "usd" would bind against 'USD', match nothing, and
			// return an empty ledger with no error to explain it. Not a
			// kindEnum: the domain is the user's own accounts, so a fixed
			// list would refuse a currency they legitimately hold.
			if !isCurrencyCode(v) {
				return errf(CodeUnresolved, "%s: %q is not a currency code (three letters, e.g. USD)", t.Field, v)
			}
```

And the helper, next to `isUUID`:

```go
// isCurrencyCode reports whether v is three ASCII letters, in either case. The
// fold to upper case happens in the compiler, not here, so validate only has to
// answer "is this the right shape".
func isCurrencyCode(v string) bool {
	if len(v) != 3 {
		return false
	}
	for i := range len(v) {
		c := v[i]
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') {
			return false
		}
	}
	return true
}
```

- [ ] **Step 4: Add the compiler emitter**

In `backend/internal/query/compile.go`, add a case to `emit`, after the `"acct"` case:

```go
	case "ccy":
		return emitCurrency(t, sink, negate)
```

And add `emitCurrency` immediately after `emitGroup` (`compile.go:159`), because it is `emitGroup` with a case fold and belongs beside the shape it copies:

```go
// currencyPredicate matches a transaction whose account holds one currency,
// through a correlated EXISTS so the fragment still names only `transactions
// t`. A join is not an option and the reason is at the top of this file: the
// list query and its COUNT(*) share this predicate, so a fragment naming a
// joined table would make the count fail while the page rendered fine.
// compile_safety_test.go enforces that.
//
// It is emitGroup's shape deliberately — one clause per value, OR-ed by
// wrapGroup — because that is what keeps the placeholder numbering in one
// place. sink.Clause allocates each $n, so a csv is several placeholders and
// never a hand-counted one, which is what would break the moment another term
// preceded this one on the same query.
const currencyPredicate = "EXISTS (SELECT 1 FROM accounts ac WHERE ac.id = t.account_id AND ac.currency = $%d)"

// emitCurrency binds a ccy term. Every value is folded to upper case here, at
// bind time rather than at parse time, so the parser keeps what the user typed
// (the same split `amt` uses, where convertAmount normalises at compile time)
// and the TypeScript mirror has nothing to fold. Without the fold, "usd" would
// bind against 'USD', match nothing, and hand the user an empty ledger with no
// error to explain it.
func emitCurrency(t Term, sink Sink, negate bool) *Diagnostic {
	clauses := make([]string, 0, len(t.Values))
	for _, v := range t.Values {
		clauses = append(clauses, sink.Clause(currencyPredicate, strings.ToUpper(v)))
	}
	wrapGroup(sink, clauses, negate)
	return nil
}
```

This produces exactly the SQL Step 1's corpus cases pin: one value becomes `(EXISTS (... ac.currency = $2))` with arg `USD`, and a csv becomes `(EXISTS (... $2) OR EXISTS (... $3))` with args `USD`, `EUR`. Note the **single** outer parenthesis pair: `wrapGroup` delegates to `sink.AnyOf`, which wraps the group once and does not parenthesise each branch, so a csv is `A OR B` inside one group rather than `(A) OR (B)`. That is the same shape `cat:a,b` produces, which is why the two must agree — the corpus compares these strings exactly.

- [ ] **Step 5: Run the backend corpus test to verify it passes**

Run: `cd backend && go test ./internal/query/ -count=1`
Expected: PASS, including `TestCorpusCoversEveryUserField`, which now sees `ccy` covered.

- [ ] **Step 6: Mirror the field in the TypeScript table**

In `frontend/src/lib/query/fields.ts`, change the kind union and add the entry:

```ts
export type FieldKind = "text" | "uuid" | "enum" | "amount" | "date" | "tags" | "currency";
```

```ts
  ccy: { userTyped: true, kind: "currency", ops: EQ },
```

- [ ] **Step 7: Add the TypeScript validate case**

In `frontend/src/lib/query/parse.ts`, add to the `switch (def.kind)` in `validate`, after `case "tags"`:

```ts
      case "currency":
        // Three ASCII letters, folded to upper case by the server. Not an enum:
        // the domain is the user's own accounts, so a fixed list would refuse a
        // currency they legitimately hold. The fold happens server-side at bind
        // time (backend/internal/query/compile.go), so the canonical form in
        // testdata/corpus.json keeps the text the user typed ("ccy:usd") and
        // this only checks shape.
        if (!/^[A-Za-z]{3}$/.test(v)) {
          return {
            code: DIAG.unresolved,
            message: `${field}: ${JSON.stringify(v)} is not a currency code (three letters, e.g. USD)`,
          };
        }
        break;
```

- [ ] **Step 8: Add the resolver case and its round-trip test**

In `frontend/src/lib/query/resolve.ts`, add a case to the switch in `resolveQuery`, after the `"uuid"` block:

```ts
      case "currency":
        // Already the code the column stores, so there is nothing to resolve.
        // The fold is the server's, at bind time
        // (backend/internal/query/compile.go), which is why this mirror needs no
        // normalisation either.
        terms.push(term);
        continue;
```

In `frontend/src/lib/query/resolve.test.ts`, add a case in the style of the existing ones, asserting that `resolveQuery(parseQuery("ccy:usd"), src)` yields one term with `values: ["usd"]` and serialises back to `ccy:usd` — and that no diagnostic is produced, since a silently-dropped term produces none either. That last assertion is the one that would have caught this gap.

- [ ] **Step 9: Run the frontend query tests**

Run: `cd frontend && bun run test -- src/lib/query`
Expected: PASS — the corpus is read by both suites, so a divergence between the two tables fails here, and the new `resolve.test.ts` case fails if the resolver drops a `ccy` term.

- [ ] **Step 9: Commit**

```bash
git add backend/internal/query/fields.go backend/internal/query/compile.go backend/internal/query/testdata/corpus.json frontend/src/lib/query/fields.ts frontend/src/lib/query/parse.ts
git commit -m "feat(query): add the ccy filter field

Scopes the transactions list to an account's currency, the same word the
dashboard's currency selector shows. Not a kindEnum: the domain is the
user's own accounts, so a fixed list would refuse a currency they hold.
Case is folded in the compiler, because an un-folded usd would match
nothing and return an empty ledger with no error."
```

---

### Task 4: Dashboard summary

The largest handler. Model change and handler change land together so the module compiles at the end of the task.

**Files:**
- Modify: `backend/models/models.go` (`DashboardSummary`, `CategorySpend`, `MonthlyData`, `BillingCycleTrendItem`)
- Modify: `backend/handlers/dashboard.go` (both `GetDashboardSummary` and `getDashboardSummaryBillingCycle`)
- Test: `backend/handlers/dashboard_test.go`, `backend/handlers/dashboard_errors_test.go`

**Interfaces:**
- Consumes: `parseCurrency`, `currencyScope`, `scopeOptions`, `scopeResult` (Task 2); `models.CurrencyAmounts`, `models.CurrencyScope` (Task 1).
- Produces: the new field names `DashboardSummary.TotalNet` and `DashboardSummary.CurrencyScope`, consumed by Task 10 (client) and Task 13 (frontend).

- [ ] **Step 1: Change the models**

In `backend/models/models.go`, replace `DashboardSummary` and the three types it references:

```go
// DashboardSummary is the dashboard aggregate. Every amount is a
// CurrencyAmounts rather than a single figure, because the aggregate can span
// accounts in more than one currency and no number can represent that; see
// CurrencyAmounts. CurrencyScope names the accounts behind each currency so a
// mixed-currency response explains itself.
type DashboardSummary struct {
	TotalAccounts      int                     `json:"totalAccounts"`
	TotalTransactions  int                     `json:"totalTransactions"`
	TotalIncome        CurrencyAmounts          `json:"totalIncome"`
	TotalExpense       CurrencyAmounts          `json:"totalExpense"`
	TotalNet           CurrencyAmounts          `json:"totalNet"`
	ByCategory         []CategorySpend         `json:"byCategory"`
	IncomeByCategory   []CategorySpend         `json:"incomeByCategory"`
	MonthlyTrend       []MonthlyData           `json:"monthlyTrend"`
	RecentTransactions []Transaction           `json:"recentTransactions"`
	CurrencyScope      CurrencyScope           `json:"currencyScope"`
	CurrentCycle       *CurrentCycleInfo       `json:"currentCycle,omitempty"`
	BillingCycleTrend  []BillingCycleTrendItem `json:"billingCycleTrend,omitempty"`
}

// CategorySpend aggregates one category's total and transaction count. Total is
// per-currency: a category can be spent in more than one currency at once, and
// the two are not addable.
type CategorySpend struct {
	CategoryID    string          `json:"categoryId"`
	CategoryName  string          `json:"categoryName"`
	CategoryColor string          `json:"categoryColor"`
	CategoryIcon  string          `json:"categoryIcon"`
	Total         CurrencyAmounts `json:"total"`
	Count         int             `json:"count"`
}

// MonthlyData holds one month's totals, keyed "YYYY-MM".
type MonthlyData struct {
	Month   string          `json:"month"`
	Income  CurrencyAmounts `json:"income"`
	Expense CurrencyAmounts `json:"expense"`
}

// BillingCycleTrendItem holds one cycle's income and expense totals.
type BillingCycleTrendItem struct {
	Label     string          `json:"label"`
	StartDate string          `json:"startDate"`
	EndDate   string          `json:"endDate"`
	Income    CurrencyAmounts `json:"income"`
	Expense   CurrencyAmounts `json:"expense"`
}
```

Note `BillingCycleTrendItem.StartDate`/`EndDate` change from `time.Time` to `string` so the client and the spec agree; if the existing Go code relies on `time.Time`, leave those two fields alone and only change `Income`/`Expense`. Check what `dashboard.go:358-363` actually assigns before editing.

- [ ] **Step 2: Write the failing test**

In `backend/handlers/dashboard_test.go`, replace `TestGetDashboardSummary` with a version that expects the per-currency shape. Keep the router, auth and `filterArgs` setup exactly as it is; change only the expectations:

```go
	assert.Equal(t, models.CurrencyAmounts{"INR": money.FromFloat(50000.00)}, summary.TotalIncome)
	assert.Equal(t, models.CurrencyAmounts{"INR": money.FromFloat(30000.50)}, summary.TotalExpense)
	assert.Equal(t, models.CurrencyAmounts{"INR": money.FromFloat(19999.50)}, summary.TotalNet)
	assert.Equal(t, []string{"INR"}, summary.CurrencyScope.Currencies)

	if _, _, ok := summary.TotalIncome.Single(); !ok {
		t.Error("a single-currency total did not report as single")
	}

	assert.Len(t, summary.ByCategory, 1)
	assert.Equal(t, "Food", summary.ByCategory[0].CategoryName)
	assert.Equal(t, 8, summary.ByCategory[0].Count)
	assert.Equal(t, models.CurrencyAmounts{"INR": money.FromFloat(12000.00)}, summary.ByCategory[0].Total)

	assert.Len(t, summary.IncomeByCategory, 1)
	assert.Equal(t, "Salary", summary.IncomeByCategory[0].CategoryName)

	assert.Len(t, summary.MonthlyTrend, 1)
	assert.Equal(t, "2026-07", summary.MonthlyTrend[0].Month)
	assert.Equal(t, models.CurrencyAmounts{"INR": money.FromFloat(50000.00)}, summary.MonthlyTrend[0].Income)
```

And add a new test that pins the whole point of the change — a mixed-currency window:

```go
func TestGetDashboardSummaryRefusesToCombineCurrencies(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()
	srv := newTestServer(mock)
	r := newDashboardTestRouter(srv)
	userID := testUserID()

	acctINR := uuid.New()
	acctUSD := uuid.New()

	mock.ExpectBeginTx(pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM accounts WHERE user_id").
		WithArgs(userID).
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(2))
	// The scope query: two accounts, two currencies.
	mock.ExpectQuery("FROM accounts a\\s+LEFT JOIN transactions t").
		WithArgs(userID).
		WillReturnRows(pgxmock.NewRows([]string{"id", "name", "currency", "income", "expense"}).
			AddRow(acctINR, "Salary", "INR", money.FromFloat(50000), money.FromFloat(30000.50)).
			AddRow(acctUSD, "Travel card", "USD", 0, money.FromFloat(80)))
	// Category and trend queries now carry the account's currency.
	mock.ExpectQuery("a.currency").
		WillReturnRows(pgxmock.NewRows([]string{"id", "name", "color", "icon", "currency", "total", "count"}).
			AddRow("c1", "Food", "#f00", "food", "INR", money.FromFloat(12000), 8))
	mock.ExpectQuery("a.currency").
		WillReturnRows(pgxmock.NewRows([]string{"id", "name", "color", "icon", "currency", "total", "count"}))
	mock.ExpectQuery("TO_CHAR\\(t.date, 'YYYY-MM'\\)").
		WillReturnRows(pgxmock.NewRows([]string{"month", "currency", "income", "expense"}).
			AddRow("2026-07", "INR", money.FromFloat(50000), money.FromFloat(30000.50)).
			AddRow("2026-07", "USD", 0, money.FromFloat(80)))
	mock.ExpectQuery("SELECT t.id, t.account_id").
		WillReturnRows(pgxmock.NewRows([]string{
			"id", "account_id", "date", "description", "amount", "type",
			"category_id", "tags", "notes", "payee_id", "payee", "created_at",
			"account_name", "category_name", "category_icon", "category_color",
		}))
	mock.ExpectCommit()

	req, _ := http.NewRequest(http.MethodGet, "/dashboard/summary", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	var summary models.DashboardSummary
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &summary))

	// Every amount is a map, keyed by the currency that produced it.
	assert.Equal(t, models.CurrencyAmounts{"INR": money.FromFloat(50000)}, summary.TotalIncome)
	assert.Equal(t, models.CurrencyAmounts{"INR": money.FromFloat(30000.50), "USD": money.FromFloat(80)}, summary.TotalExpense)
	assert.Equal(t, models.CurrencyAmounts{"INR": money.FromFloat(19999.50), "USD": -money.FromFloat(80)}, summary.TotalNet)

	// A two-currency scope refuses to report as a single number, which is what
	// stops a client from adding 50000 rupees to 80 dollars.
	if _, _, ok := summary.TotalIncome.Single(); ok {
		t.Error("a two-currency total reported as a single amount")
	}
	assert.Equal(t, []string{"INR", "USD"}, summary.CurrencyScope.Currencies)
	assert.Len(t, summary.CurrencyScope.Accounts, 2)

	// The body carries no bare number for any of them: the old field names are
	// gone rather than still holding a wrong value.
	var raw map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &raw))
	for _, field := range []string{"totalIncome", "totalExpense", "totalNet"} {
		if _, isNumber := raw[field].(float64); isNumber {
			t.Errorf("%s is a bare number in the response body", field)
		}
	}

	assert.NoError(t, mock.ExpectationsWereMet())
}
```

- [ ] **Step 3: Run the test to verify it fails**

Run: `cd backend && go test ./handlers/ -run 'TestGetDashboardSummary' -count=1`
Expected: FAIL to compile — `TotalNet` and `CurrencyScope` do not exist yet, or the assertions fail on the old shape.

- [ ] **Step 4: Replace the blind scalar query with the scope query**

In `backend/handlers/dashboard.go`, in `GetDashboardSummary`:

Delete the `scalarQuery` block (`dashboard.go:107-111`) entirely.

Read the currency parameter next to the account one:

```go
	currency, ok := parseCurrency(c)
	if !ok {
		return
	}
```

Replace the scalar query with the scope call, after the `TotalAccounts` count:

```go
	scope, err := currencyScope(ctx, tx, userID, scopeOptions{
		DateFrom:  dateFrom,
		DateTo:    dateTo,
		AccountID: accountID,
		Currency:  currency,
	})
	if err != nil {
		slog.Error("fetching currency scope", "error", err.Error())
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
```

`validation.RespondError(c, "internal server error", http.StatusInternalServerError)` is what every other failure in this handler already answers with — do not invent a helper.

- [ ] **Step 5: Make the category query per-currency**

`accounts` is joined onto the transaction side of the existing `LEFT JOIN` so the currency is in scope, and the `LIMIT 15` becomes a per-currency `ROW_NUMBER`, so "top 15 by spend" keeps meaning top 15 *within a currency* rather than 15 rows drawn from a mixed ordering:

```go
	catQuery := `SELECT id, name, color, icon, currency, total, count FROM (
				 SELECT c.id, c.name, c.color, c.icon, COALESCE(NULLIF(a.currency, ''), 'INR') AS currency,
				        COALESCE(SUM(t.amount), 0) as total, COUNT(t.id) as count,
				        ROW_NUMBER() OVER (PARTITION BY COALESCE(NULLIF(a.currency, ''), 'INR') ORDER BY SUM(t.amount) DESC) AS rn
				 FROM categories c
				 LEFT JOIN transactions t ON t.category_id = c.id AND t.type = 'debit' AND t.user_id = $1
				 LEFT JOIN accounts a ON a.id = t.account_id AND a.user_id = $1` + catFilter + `
				 WHERE (c.user_id = $1 OR c.user_id IS NULL)
				 GROUP BY c.id, c.name, c.color, c.icon, COALESCE(NULLIF(a.currency, ''), 'INR')
				 HAVING COALESCE(SUM(t.amount), 0) > 0
				 ) ranked WHERE rn <= 15
				 ORDER BY total DESC, name, id`
```

`incomeCatQuery` is the same query with `t.type = 'credit'`.

Three things make this correct, and the test in Step 2 pins all three:

- **The window function may use `SUM(t.amount)`.** Postgres evaluates windows *after* grouping, so an aggregate result is available in a window's `ORDER BY` at the same query level. The alias is `rn` rather than `rank` only because the latter names a built-in.
- **The `LEFT JOIN`s cannot leak empty categories into the ranking** (Review Focus #5). A category with no matching transaction has `SUM` NULL, so `COALESCE(..., 0) > 0` in the `HAVING` drops it — and the `HAVING` runs *before* the window function, so `ROW_NUMBER` never sees the row and the partition it would land in is empty. That evaluation order is what makes the query correct, so the test asserts a category with no spend is **absent**, not merely uncapped.
- **`catFilter` is unchanged.** It filters `t.date` and `t.account_id`, and the chained `LEFT JOIN accounts` disturbs neither, so the same fragment serves both queries and both keep their existing parameter numbering.

If Postgres rejects the combination anyway, the fallback is to drop `rn` and the outer wrapper, drop the `LIMIT 15`, and let the client take the top 15 for the selected currency. Category counts are small and the limit's meaning is currency-relative regardless. **State in the commit message which path shipped** — the two are not equivalent, and a reader comparing them will want to know.

- [ ] **Step 6: Fold the category and trend rows into maps in Go**

Where the handler builds `ByCategory`, `IncomeByCategory` and `MonthlyTrend`, fold instead of assigning:

```go
	totals := map[string]*models.CategorySpend{}
	order := []string{}
	for rows.Next() {
		var (
			id, name, color, icon, code string
			total                        money.Amount
			count                        int
		)
		if err := rows.Scan(&id, &name, &color, &icon, &code, &total, &count); err != nil {
			return
		}
		entry, ok := totals[id]
		if !ok {
			entry = &models.CategorySpend{
				CategoryID: id, CategoryName: name, CategoryColor: color,
				CategoryIcon: icon, Total: models.NewCurrencyAmounts(),
			}
			totals[id] = entry
			order = append(order, id)
		}
		entry.Total.Add(code, total)
		entry.Count += count
	}
	for _, id := range order {
		byCategory = append(byCategory, *totals[id])
	}
```

The trend folds the same way, keyed by month:

```go
	trend := map[string]*models.MonthlyData{}
	trendOrder := []string{}
	for rows.Next() {
		var month, code string
		var income, expense money.Amount
		if err := rows.Scan(&month, &code, &income, &expense); err != nil {
			return
		}
		entry, ok := trend[month]
		if !ok {
			entry = &models.MonthlyData{Month: month, Income: models.NewCurrencyAmounts(), Expense: models.NewCurrencyAmounts()}
			trend[month] = entry
			trendOrder = append(trendOrder, month)
		}
		entry.Income.Add(code, income)
		entry.Expense.Add(code, expense)
	}
```

- [ ] **Step 7: Populate the response**

Where the summary struct is built:

```go
	summary := models.DashboardSummary{
		TotalAccounts:      totalAccounts,
		TotalTransactions:  totalTransactions,
		TotalIncome:        scope.Income,
		TotalExpense:       scope.Expense,
		TotalNet:           scope.Net(),
		ByCategory:         byCategory,
		IncomeByCategory:   incomeByCategory,
		MonthlyTrend:       monthlyTrend,
		RecentTransactions: recent,
		CurrencyScope:      scope.Scope,
	}
```

- [ ] **Step 8: Do the same in `getDashboardSummaryBillingCycle`**

That function is already account-scoped, so its scope query is trivially single-currency, but it must still fill `CurrencyScope` and the maps rather than being left inconsistent:

```go
	scope, err := currencyScope(ctx, tx, userID, scopeOptions{AccountID: accountID, Currency: currency})
```

and `TotalIncome`/`TotalExpense` come from it, with `TotalNet` from `scope.Net()`. The cycle, trend and category queries gain `COALESCE(NULLIF(a.currency, ''), 'INR')` and fold the same way. Because an account has exactly one currency, the `ROW_NUMBER` partition is not needed here — keep that function's `LIMIT 15` as it is.

- [ ] **Step 9: Update the remaining dashboard tests**

- `TestGetDashboardSummaryWithDateFilter` and `TestGetDashboardSummaryWithAccountFilter`: replace the `SELECT COUNT\(\*\),` expectation with the `FROM accounts a` scope query, and add the currency to the category/trend result column lists.
- `TestGetDashboardSummaryIncomeQueryError` and `TestGetDashboardSummaryErrors`: point the failure at the new query.
- `TestGetDashboardSummaryBillingCycle`: `TotalIncome` becomes `models.CurrencyAmounts{...}`; add `TotalNet`.
- `dashboard_errors_test.go:280` asserts `"byCategory":[]` in the raw body — that still holds, since the key is unchanged.
- `TestGetDashboardSummaryRejectsMalformedFilters`: add a `?currency=US` case expecting 400.

- [ ] **Step 10: Run the dashboard tests**

Run: `cd backend && go test ./handlers/ -run 'Dashboard' -count=1`
Expected: PASS.

- [ ] **Step 11: Run the full backend suite and the integration tests**

Run: `cd backend && go test ./... -count=1`
Expected: PASS. `money_flow`, `timeline` and `calendar` tests are untouched by this task and must still pass — if they do not, this task changed something shared.

Run: `make test-integration`
Expected: the dashboard assertions pass; `TestIntegrationMoneyFlowGraph` and friends still use the old shape for their endpoints and are updated in Tasks 5–7, so a failure there naming those tests is expected and is not this task's to fix.

- [ ] **Step 12: Commit**

```bash
git add backend/models/models.go backend/handlers/dashboard.go backend/handlers/dashboard_test.go backend/handlers/dashboard_errors_test.go
git commit -m "fix(dashboard): stop adding amounts across currencies

accounts.currency is stored and validated but no aggregate read it, so
a window over a USD and an INR account added dollars to rupees. Every
amount is now a currency-keyed map and the response names the accounts
behind each currency, so a mixed-currency total is refused rather than
wrong. totalNet moves to the server, which is where the subtraction
belongs."
```

---

### Task 5: Money-flow graph

**Files:**
- Modify: `backend/models/models.go` (`MoneyFlowNode`, `MoneyFlowEdge`, `MoneyFlowLinkSummary`, `MoneyFlowGraph`)
- Modify: `backend/handlers/money_flow.go` (all four queries plus `buildMoneyFlowGraph` and `analyzeAccountFlows`)
- Test: `backend/handlers/money_flow_test.go`

**Interfaces:**
- Consumes: `parseCurrency`, `currencyScope` (Task 2); `models.CurrencyAmounts` (Task 1).
- Produces: `models.MoneyFlowGraph.CurrencyScope`, `.TotalNet`.

- [ ] **Step 1: Change the models**

```go
// MoneyFlowNode is one node of the money-flow graph. Kind is income, account,
// category or payee; IDs are stage-prefixed (e.g. "account:<uuid>"). Total is
// per-currency: a node aggregates flows across accounts, which may hold more
// than one currency.
type MoneyFlowNode struct {
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Kind  string          `json:"kind"`
	Color string          `json:"color,omitempty"`
	Group string          `json:"group,omitempty"`
	Total CurrencyAmounts `json:"total"`
}

// MoneyFlowEdge is one aggregated flow between two node IDs.
type MoneyFlowEdge struct {
	Source string          `json:"source"`
	Target string          `json:"target"`
	Value  CurrencyAmounts `json:"value"`
}

// MoneyFlowLinkSummary is a per-link-type rollup for the graph's window. Its
// total spans both endpoints of each link, which may be in different
// currencies, so it is per-currency for that reason alone.
type MoneyFlowLinkSummary struct {
	Type  string          `json:"type"`
	Count int             `json:"count"`
	Total CurrencyAmounts `json:"total"`
}

// MoneyFlowGraph is the GET /dashboard/money-flow response: an acyclic graph
// plus the link-type summary that is not drawn as edges.
type MoneyFlowGraph struct {
	Nodes         []MoneyFlowNode        `json:"nodes"`
	Links         []MoneyFlowEdge        `json:"links"`
	TotalIncome   CurrencyAmounts        `json:"totalIncome"`
	TotalExpense  CurrencyAmounts        `json:"totalExpense"`
	TotalNet      CurrencyAmounts        `json:"totalNet"`
	LinkSummary   []MoneyFlowLinkSummary `json:"linkSummary"`
	CurrencyScope CurrencyScope          `json:"currencyScope"`
}
```

- [ ] **Step 2: Write the failing test**

Add to `backend/handlers/money_flow_test.go`:

```go
func TestGetMoneyFlowKeepsCurrenciesApartInTheGraph(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()
	srv := newTestServer(mock)
	r := newMoneyFlowTestRouter(srv)
	userID := testUserID()

	acctINR := uuid.New()
	acctUSD := uuid.New()

	mock.ExpectBeginTx(pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	mock.ExpectQuery("FROM accounts a\\s+LEFT JOIN transactions t").
		WithArgs(userID).
		WillReturnRows(pgxmock.NewRows([]string{"id", "name", "currency", "income", "expense"}).
			AddRow(acctINR, "Salary", "INR", money.FromFloat(50000), money.FromFloat(12000)).
			AddRow(acctUSD, "Travel card", "USD", 0, money.FromFloat(300)))
	// The four stage queries now project the account's currency.
	for i := 0; i < 4; i++ {
		mock.ExpectQuery("currency").
			WillReturnRows(pgxmock.NewRows([]string{"id", "name", "color", "currency", "total"}))
	}
	mock.ExpectCommit()

	req, _ := http.NewRequest(http.MethodGet, "/dashboard/money-flow", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	var graph models.MoneyFlowGraph
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &graph))

	assert.Equal(t, models.CurrencyAmounts{"INR": money.FromFloat(50000)}, graph.TotalIncome)
	assert.Equal(t, models.CurrencyAmounts{"INR": money.FromFloat(12000), "USD": money.FromFloat(300)}, graph.TotalExpense)
	assert.Equal(t, models.CurrencyAmounts{"INR": money.FromFloat(38000), "USD": -money.FromFloat(300)}, graph.TotalNet)
	assert.Equal(t, []string{"INR", "USD"}, graph.CurrencyScope.Currencies)
}
```

`newMoneyFlowTestRouter(srv)` is the existing helper (`money_flow_test.go:20`); use it as written.

- [ ] **Step 3: Run the test to verify it fails**

Run: `cd backend && go test ./handlers/ -run TestGetMoneyFlowKeepsCurrenciesApart -count=1`
Expected: FAIL — the response has bare totals.

- [ ] **Step 4: Add the scope call and per-currency queries**

In `money_flow.go`'s handler, read the currency and run the scope query alongside the four stage queries:

```go
	currency, ok := parseCurrency(c)
	if !ok {
		return
	}
```

Pass `currency` into `flowFilter` so the `a.currency = $n` predicate joins the existing filter builder rather than being appended by hand:

```go
// flowFilter renders the shared window for the money-flow queries. currency is
// a currency code or empty; it filters through the accounts alias every one of
// these queries already joins, so a currency narrows the graph the same way an
// account does.
func flowFilter(dateAlias, accountAlias string, start int, dateFrom, dateTo, accountID, currency string) (string, []any, int) {
	...
	if currency != "" {
		parts = append(parts, fmt.Sprintf("COALESCE(%s.currency, 'INR') = $%d", accountAlias, n))
		args = append(args, currency)
		n++
	}
	...
}
```

Update all four call sites (`queryIncomeFlows`, `queryAccountCategoryFlows`, `queryCategoryPayeeFlows`, `queryMoneyFlowLinks`) for the new parameter, and add `COALESCE(NULLIF(a.currency, ''), 'INR') AS currency` to each one's select and group-by.

`queryCategoryPayeeFlows` is the one that must gain the account to its `GROUP BY` as well as its select — today it groups by category and payee only, so different-currency debits are already merged into one row before any map exists:

```sql
	GROUP BY c.id, c.name, c.color, cg.id, cg.color, p.id, p.name, COALESCE(NULLIF(a.currency, ''), 'INR')
```

- [ ] **Step 5: Fold the graph assembly in Go**

In `buildMoneyFlowGraph`, `addNode`'s accumulation and every rollup become per-currency. The cycle-breaking in `analyzeAccountFlows` stays as it is — it compares edge identity, not amounts — but every `Total +=` becomes `Total.Add(code, amount)`, which requires the node to remember the code each contribution arrived with. Thread it through the row types (`flowIncomeRow` and friends gain a `currency string` field).

- [ ] **Step 6: Update the existing money-flow tests**

`TestGetMoneyFlow`, `TestBuildMoneyFlowGraphRollup`, `TestAccountFlowEdges*` and `TestGetMoneyFlowWithFilters` all change shape. `TestGetMoneyFlow`'s `graph.TotalIncome == money.FromFloat(50000)` becomes `models.CurrencyAmounts{"INR": money.FromFloat(50000)}`, and each expectation's result rows gain a `currency` column. Add a `?currency=US` 400 case.

- [ ] **Step 7: Run the tests**

Run: `cd backend && go test ./handlers/ -run 'MoneyFlow' -count=1`
Expected: PASS.

Run: `cd backend && go test ./... -count=1`
Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add backend/models/models.go backend/handlers/money_flow.go backend/handlers/money_flow_test.go
git commit -m "fix(money-flow): keep currencies apart in the Sankey

queryCategoryPayeeFlows grouped by category and payee only, so debits in
two currencies were merged into one row before any total existed. Every
node total, edge value and link total is now a currency-keyed map."
```

---

### Task 6: Money-flow timeline

**Files:**
- Modify: `backend/models/models.go` (`MoneyFlowTimelinePeriod`, `MoneyFlowTimeline`)
- Modify: `backend/handlers/money_flow_timeline.go` (the monthly path)
- Test: `backend/handlers/money_flow_timeline_test.go`

**Interfaces:**
- Consumes: `parseCurrency`, `currencyScope` (Task 2).
- Produces: `models.MoneyFlowTimeline.CurrencyScope`.

- [ ] **Step 1: Change the models**

```go
// MoneyFlowTimelinePeriod is one period of the flow timeline; the bounds are
// inclusive and can be fed straight back into the graph query. Net is
// per-currency because a difference within one currency is meaningful even
// when the window as a whole spans several.
type MoneyFlowTimelinePeriod struct {
	Key       string          `json:"key"`
	Label     string          `json:"label"`
	StartDate string          `json:"startDate"`
	EndDate   string          `json:"endDate"`
	Income    CurrencyAmounts `json:"income"`
	Expense   CurrencyAmounts `json:"expense"`
	Net       CurrencyAmounts `json:"net"`
}

// MoneyFlowTimeline is the GET /dashboard/money-flow/timeline response.
type MoneyFlowTimeline struct {
	GroupBy       string                    `json:"groupBy"`
	Periods       []MoneyFlowTimelinePeriod `json:"periods"`
	CurrencyScope CurrencyScope             `json:"currencyScope"`
}
```

- [ ] **Step 2: Write the failing test**

In `money_flow_timeline_test.go`, change `TestGetMoneyFlowTimelineMonthly`'s expectations to the map form, add a `currency` column to the period rows, and add:

```go
func TestGetMoneyFlowTimelineRefusesACrossCurrencyNet(t *testing.T) {
	// A window covering an INR and a USD account: each period carries a net per
	// currency, and nothing anywhere carries a net across the two.
	// ... same mock setup as TestGetMoneyFlowTimelineMonthly, with two rows per
	// month distinguished by their currency column ...
	// assert that Periods[0].Net has both keys and that the response's
	// CurrencyScope names both accounts.
}
```

- [ ] **Step 3: Run the test to verify it fails**

Run: `cd backend && go test ./handlers/ -run 'MoneyFlowTimeline' -count=1`
Expected: FAIL.

- [ ] **Step 4: Implement**

Read the currency, run the scope query, add `COALESCE(NULLIF(a.currency, ''), 'INR')` to the monthly query's select and `GROUP BY 1` becomes `GROUP BY 1, 2` (with the currency as the second select expression), and fold per period and currency. Compute each period's net with `Sub`, never across the map:

```go
			entry.Net = entry.Income.Sub(entry.Expense)
```

The billing-cycle branch is account-scoped and needs only the scope call plus the currency column.

- [ ] **Step 5: Update the existing tests and run**

Run: `cd backend && go test ./handlers/ -run 'MoneyFlowTimeline' -count=1`
Expected: PASS.

Run: `cd backend && go test ./... -count=1`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add backend/models/models.go backend/handlers/money_flow_timeline.go backend/handlers/money_flow_timeline_test.go
git commit -m "fix(money-flow-timeline): make each period's net per-currency

A difference within one currency is meaningful even when the window spans
several, so net is a map computed with Sub. No net is ever taken across
two currencies."
```

---

### Task 7: Cash-flow calendar

**Files:**
- Modify: `backend/models/models.go` (`CashFlowCalendarDay`, `CashFlowCalendarMarker`, `CashFlowCalendarCycle`, `CashFlowCalendar`)
- Modify: `backend/handlers/cash_flow_calendar.go` (daily query, Go-side window totals, `maxAbsNet`)
- Test: `backend/handlers/cash_flow_calendar_test.go`

**Interfaces:**
- Consumes: `parseCurrency`, `currencyScope` (Task 2).
- Produces: `models.CashFlowCalendar.CurrencyScope`.

- [ ] **Step 1: Change the models**

```go
// CashFlowCalendarDay is one day of daily net flow; days without transactions
// are omitted and filled in by the UI. Net is per-currency, and a day on which
// one currency spent and another earned has a net in both.
type CashFlowCalendarDay struct {
	Date    string          `json:"date"`
	Income  CurrencyAmounts `json:"income"`
	Expense CurrencyAmounts `json:"expense"`
	Net     CurrencyAmounts `json:"net"`
	Count   int             `json:"count"`
}

// CashFlowCalendarMarker is a synthetic summary point (a month-end running
// balance or a cycle's total outstanding). The overlay belongs to one account,
// so its amount is a map with one key; it is a map anyway so a client never has
// to learn two shapes for the same field.
type CashFlowCalendarMarker struct {
	Date   string          `json:"date"`
	Label  string          `json:"label"`
	Kind   string          `json:"kind"`
	Amount CurrencyAmounts `json:"amount"`
}

// CashFlowCalendarCycle is a billing-cycle boundary so the calendar can mark
// statement periods.
type CashFlowCalendarCycle struct {
	ID          string          `json:"id"`
	Label       string          `json:"label"`
	StartDate   string          `json:"startDate"`
	EndDate     string          `json:"endDate"`
	Outstanding CurrencyAmounts `json:"outstanding"`
}

// CashFlowCalendar is the GET /dashboard/cash-flow-calendar response.
// MaxAbsNet is the largest absolute daily net **per currency**, because a
// single scale across currencies is meaningless: it would make a quiet foreign
// account's real deficit look flat next to a large domestic one.
type CashFlowCalendar struct {
	Days          []CashFlowCalendarDay    `json:"days"`
	Markers       []CashFlowCalendarMarker `json:"markers"`
	Cycles        []CashFlowCalendarCycle  `json:"cycles"`
	TotalIncome   CurrencyAmounts           `json:"totalIncome"`
	TotalExpense  CurrencyAmounts           `json:"totalExpense"`
	Net           CurrencyAmounts           `json:"net"`
	MaxAbsNet     CurrencyAmounts           `json:"maxAbsNet"`
	CurrencyScope CurrencyScope             `json:"currencyScope"`
}
```

- [ ] **Step 2: Write the failing test**

In `cash_flow_calendar_test.go`, change `TestGetCashFlowCalendar` to the map form, add a `currency` column to the day rows, and add:

```go
func TestGetCashFlowCalendarScalesEachCurrencySeparately(t *testing.T) {
	// maxAbsNet is per-currency: a USD day of -300 and an INR day of -50000
	// must not share one denominator, or the USD cell renders as flat and the
	// INR cell as the only coloured one on the grid.
	// ... two accounts, two currencies; assert MaxAbsNet has both keys with
	// their own magnitudes, and that Days[0].Net has both keys ...
}
```

- [ ] **Step 3: Run the test to verify it fails**

Run: `cd backend && go test ./handlers/ -run 'CashFlowCalendar' -count=1`
Expected: FAIL.

- [ ] **Step 4: Implement**

Read the currency, run the scope query, add `COALESCE(NULLIF(a.currency, ''), 'INR')` to the daily query's select and group-by, and replace the Go-side window totals (`cash_flow_calendar.go:207-215`) and `maxAbsNet` with per-currency folds:

```go
	maxAbs := models.NewCurrencyAmounts()
	for _, day := range days {
		for code, net := range day.Net {
			if abs := money.Amount(net.Abs()); abs > maxAbs[code] {
				maxAbs[code] = abs
			}
		}
	}
```

`money.Amount.Abs()` already exists. The `totalIncome`/`totalExpense`/`net` come from the scope result rather than the Go loop, so the calendar's window totals and the dashboard's are computed by the same query.

- [ ] **Step 5: Update the existing tests and run**

`TestGetCashFlowCalendar`, `TestGetCashFlowCalendarAccountFilter` (the marker's `Amount` becomes a map) and `TestGetCashFlowCalendarBillingCycles` (the cycle's `Outstanding` likewise). `cash_flow_calendar_test.go:245` asserts `"markers":[]` in the raw body, which still holds.

Run: `cd backend && go test ./handlers/ -run 'CashFlowCalendar' -count=1`
Expected: PASS.

Run: `cd backend && go test ./... -count=1`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add backend/models/models.go backend/handlers/cash_flow_calendar.go backend/handlers/cash_flow_calendar_test.go
git commit -m "fix(calendar): scale the heatmap per currency

One maxAbsNet across currencies made a quiet foreign account's real
deficit render as a flat cell next to a large domestic one. Each
currency now carries its own scale, and the window totals come from the
same scope query the dashboard uses."
```

---

### Task 8: Link-cycle report

**Files:**
- Modify: `backend/models/models.go` (`LinkFlowTypeTotal`, `LinkCycleLeg`, `LinkCycle`, `LinkOneSidedFlow`, `LinkCycleReport`)
- Modify: `backend/handlers/link_cycles.go` (the query and `buildLinkCycleReport`)
- Test: the link-cycle handler test file

**Interfaces:**
- Consumes: `parseCurrency`, `currencyScope` (Task 2).
- Produces: `models.LinkCycleReport.CurrencyScope`.

- [ ] **Step 1: Change the models**

Every amount on the link-cycle report becomes `CurrencyAmounts`: `LinkFlowTypeTotal.Total`, `LinkCycleLeg.Amount`, `LinkCycle.Net`, `LinkCycle.Gross`, `LinkOneSidedFlow.Total`, `LinkCycleReport.TotalCircular`. Add `CurrencyScope CurrencyScope` to `LinkCycleReport`.

This report is in scope because it sums two accounts' transactions **by construction** — a link has a from-account and a to-account, and they may hold different currencies.

- [ ] **Step 2: Write the failing test**

Add a test with two accounts in different currencies linked to each other, asserting the leg's `Amount` has one key per endpoint's currency rather than one summed figure.

- [ ] **Step 3: Run it to verify it fails**

Run: `cd backend && go test ./handlers/ -run 'LinkCycle' -count=1`
Expected: FAIL.

- [ ] **Step 4: Implement**

`queryAccountLinkDetails` already joins both endpoints' accounts (`fa`, `ta`), so it selects `COALESCE(fa.currency, 'INR')` and `COALESCE(ta.currency, 'INR')` and the Go side folds each leg by the from-account's currency. `buildLinkCycleReport`'s `pair.total +=`, `byType.Total +=` and `out.Gross +=` all become `Add` calls, and `Net`/`Gross` keep the "smallest leg" meaning per currency.

- [ ] **Step 5: Run the tests**

Run: `cd backend && go test ./handlers/ -run 'Link' -count=1`
Expected: PASS.

Run: `cd backend && go test ./... -count=1`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add backend/models/models.go backend/handlers/link_cycles.go
git commit -m "fix(links): keep a link's two accounts' currencies apart

A link has a from-account and a to-account and they may hold different
currencies, so the report summed them by construction. Each amount is
now a currency-keyed map."
```

---

### Task 9: `openapi.yaml` and `client/api`, together

These two must land in one commit: `TestClientTypesMatchTheSpecSchemas` walks every struct in `types.go` via `go/ast` and compares its JSON tags against `components.schemas` in both directions, so either alone fails.

**Files:**
- Modify: `backend/openapi.yaml` (all reporting schemas, the five paths' `currency` parameter, two new schemas)
- Modify: `client/api/currency.go` (create), `client/api/currency_test.go` (create)
- Modify: `client/api/types.go`, `client/api/endpoints_dashboard.go`, `client/api/endpoints_links.go`, `client/api/spec_parity_test.go`

**Interfaces:**
- Consumes: the response shapes from Tasks 4–8.
- Produces: `api.CurrencyAmounts` with `Single`, `Currencies`, `Sub`, `Display`, `IsNegative`; `api.CurrencyScope`; `api.ScopedAccount`; `WindowFilter.Currency`. Tasks 11–13 use all of them.

- [ ] **Step 1: Write the failing test**

Create `client/api/currency_test.go`:

```go
package api

import "testing"

func TestCurrencyAmountsDisplayRefusesToChoose(t *testing.T) {
	// One currency: the formatted value, named.
	got := CurrencyAmounts{"INR": "5000.00"}.Display()
	if got != "INR 5,000.00" {
		t.Errorf("Display() = %q, want %q", got, "INR 5,000.00")
	}

	// Two: both, and an explicit statement that they were not added.
	multi := CurrencyAmounts{"USD": "120.00", "INR": "5000.00"}.Display()
	for _, want := range []string{"2 currencies", "INR", "5,000.00", "USD", "120.00"} {
		if !contains(multi, want) {
			t.Errorf("Display() = %q, missing %q", multi, want)
		}
	}
	if contains(multi, "5,120") {
		t.Errorf("Display() = %q added two currencies together", multi)
	}
}

func TestCurrencyAmountsSingleAndSub(t *testing.T) {
	if _, _, ok := (CurrencyAmounts{"INR": "1", "USD": "2"}).Single(); ok {
		t.Error("a two-currency map reported a single amount")
	}
	code, value, ok := CurrencyAmounts{"INR": "1.50"}.Single()
	if !ok || code != "INR" || value != "1.50" {
		t.Errorf("Single() = %q, %q, %t", code, value, ok)
	}

	// Sub is exact decimal text arithmetic, not float: Amount carries the
	// server's decimal and must not be recomputed through a float64.
	got := CurrencyAmounts{"INR": "0.30"}.Sub(CurrencyAmounts{"INR": "0.10"})
	if got["INR"] != "0.20" {
		t.Errorf("Sub = %q, want 0.20", got["INR"])
	}
}

func TestCurrencyAmountsSubUnionOfKeys(t *testing.T) {
	got := CurrencyAmounts{"INR": "5.00"}.Sub(CurrencyAmounts{"INR": "1.00", "USD": "2.00"})

	if got["INR"] != "4.00" {
		t.Errorf("INR = %q, want 4.00", got["INR"])
	}
	if got["USD"] != "-2.00" {
		t.Errorf("USD = %q, want -2.00", got["USD"])
	}
}

func TestCurrencyAmountsCurrenciesIsSorted(t *testing.T) {
	got := CurrencyAmounts{"USD": "1", "INR": "2", "EUR": "3"}.Currencies()
	want := []string{"EUR", "INR", "USD"}

	if len(got) != 3 || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Errorf("Currencies() = %v, want %v", got, want)
	}
}
```

`contains` is a local helper:

```go
func contains(haystack, needle string) bool {
	return len(needle) == 0 || (len(haystack) >= len(needle) && indexOf(haystack, needle) >= 0)
}

func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}
```

Use `strings.Contains` instead if this package already imports `strings` in a test file — it does not matter which, as long as there is no duplicate `contains` in the package. Check before adding.

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd client && go test ./api/ -run TestCurrencyAmounts -count=1`
Expected: FAIL to compile — `undefined: CurrencyAmounts`.

- [ ] **Step 3: Write `client/api/currency.go`**

```go
package api

import (
	"fmt"
	"sort"
	"strings"
)

// CurrencyAmounts is one aggregate's value keyed by the currency code of each
// account that contributed to it. The API refuses to return a total that spans
// currencies, so there is never a bare amount on a reporting endpoint: one key
// means the value is exact for the whole scope, and more than one means no
// single number can represent it.
//
// It is the twin of the backend's models.CurrencyAmounts, and the same rule
// governs both: nothing here adds across keys, because that is the number this
// type exists to refuse.
type CurrencyAmounts map[string]Amount

// CurrencyScope names every currency a response covers and the accounts behind
// each one, so a mixed-currency result explains itself rather than being
// quietly narrowed to one currency.
type CurrencyScope struct {
	Currencies []string       `json:"currencies"`
	Accounts   []ScopedAccount `json:"accounts"`
}

// ScopedAccount is one account in a response's scope, with what it contributes.
type ScopedAccount struct {
	ID       string          `json:"id"`
	Name     string          `json:"name"`
	Currency string          `json:"currency"`
	Income   CurrencyAmounts `json:"income"`
	Expense  CurrencyAmounts `json:"expense"`
}

// Add folds one contribution in and returns the map, so it works on a nil map
// as well as one from the decoder.
func (m CurrencyAmounts) Add(currency string, amount Amount) CurrencyAmounts {
	if m == nil {
		m = CurrencyAmounts{}
	}
	m[currency] = addDecimal(m[currency], amount)
	return m
}

// Single returns the map's only entry, and ok=false whenever it does not hold
// exactly one currency. It is the answer to "may this be treated as a single
// number", and the only sanctioned way to reach a bare amount.
func (m CurrencyAmounts) Single() (string, Amount, bool) {
	if len(m) != 1 {
		return "", "", false
	}
	for code, amount := range m {
		return code, amount, true
	}
	return "", "", false
}

// Currencies returns the codes in sorted order.
func (m CurrencyAmounts) Currencies() []string {
	out := make([]string, 0, len(m))
	for code := range m {
		out = append(out, code)
	}
	sort.Strings(out)
	return out
}

// Sub returns the per-currency difference m - other. A currency missing from
// either operand counts as zero and the result carries the union of the key
// sets, so a currency that only ever spends still yields a negative net.
func (m CurrencyAmounts) Sub(other CurrencyAmounts) CurrencyAmounts {
	out := make(CurrencyAmounts, len(m)+len(other))
	for code, amount := range m {
		out[code] = amount
	}
	for code, amount := range other {
		out[code] = addDecimal(out[code], "-" + strings.TrimPrefix(string(amount), "-"))
	}
	return out
}

// Display renders the value for a terminal. One currency formats as
// "INR 5,000.00". More than one renders as an explicit refusal —
// "2 currencies: EUR 1.00, INR 5,000.00" — because picking one silently would
// be the exact bug this type was introduced to stop, and adding them would be
// worse.
func (m CurrencyAmounts) Display() string {
	if code, amount, ok := m.Single(); ok {
		return code + " " + amount.Display()
	}
	if len(m) == 0 {
		return "no currency"
	}
	codes := m.Currencies()
	parts := make([]string, 0, len(codes))
	for _, code := range codes {
		parts = append(parts, code+" "+m[code].Display())
	}
	return fmt.Sprintf("%d currencies: %s", len(codes), strings.Join(parts, ", "))
}

// IsNegative reports whether the value is below zero in every currency it
// covers. A map that is negative in one currency and positive in another is
// neither, which is the honest answer: there is no sign to report.
func (m CurrencyAmounts) IsNegative() bool {
	if len(m) == 0 {
		return false
	}
	for _, amount := range m {
		if !amount.IsNegative() {
			return false
		}
	}
	return true
}

// addDecimal adds two decimal amounts exactly, in minor units, and renders the
// result with two decimals. It never routes through a float64: Amount carries
// the server's decimal text and a float round-trip of a large minor-unit value
// loses cents. An absent operand is zero.
func addDecimal(a, b Amount) Amount {
	if a.IsZero() {
		return normaliseDecimal(b)
	}
	if b.IsZero() {
		return normaliseDecimal(a)
	}
	negA, negB := a.IsNegative(), b.IsNegative()
	wholeA, fracA := splitDecimal(a)
	wholeB, fracB := splitDecimal(b)
	// Same sign: magnitudes add.
	if negA == negB {
		w, f := wholeA+wholeB, fracA+fracB
		if f >= 100 {
			w, f = w+1, f-100
		}
		out := fmt.Sprintf("%d.%02d", w, f)
		if negA {
			out = "-" + out
		}
		return Amount(out)
	}
	// Opposite signs: magnitudes subtract, keeping the larger one's sign.
	if wholeA*100+fracA >= wholeB*100+fracB {
		w, f := wholeA-wholeB, fracA-fracB
		if f < 0 {
			w, f = w-1, f+100
		}
		if w == 0 && f == 0 {
			return "0.00"
		}
		if negA {
			return Amount(fmt.Sprintf("-%d.%02d", w, f))
		}
		return Amount(fmt.Sprintf("%d.%02d", w, f))
	}
	w, f := wholeB-wholeA, fracB-fracA
	if f < 0 {
		w, f = w-1, f+100
	}
	if w == 0 && f == 0 {
		return "0.00"
	}
	if negB {
		return Amount(fmt.Sprintf("-%d.%02d", w, f))
	}
	return Amount(fmt.Sprintf("%d.%02d", w, f))
}

// splitDecimal separates a decimal amount into its whole and hundredths parts,
// discarding the sign. It reports zero for an absent amount, which is what makes
// a missing key in a map read as zero rather than as an error.
func splitDecimal(a Amount) (whole, frac int64) {
	s := strings.TrimPrefix(strings.TrimSpace(a.String()), "-")
	if s == "" {
		return 0, 0
	}
	w, f, hasFrac := strings.Cut(s, ".")
	whole = atoi64(w)
	if hasFrac {
		switch {
		case len(f) == 0:
			frac = 0
		case len(f) == 1:
			frac = atoi64(f) * 10
		default:
			frac = atoi64(f[:2])
		}
	}
	return whole, frac
}

// normaliseDecimal renders an amount with exactly two decimals, so a sum is
// never the ragged "5.5" a raw operand happened to carry.
func normaliseDecimal(a Amount) Amount {
	if a.IsZero() {
		return "0.00"
	}
	w, f := splitDecimal(a)
	if a.IsNegative() && (w != 0 || f != 0) {
		return Amount(fmt.Sprintf("-%d.%02d", w, f))
	}
	return Amount(fmt.Sprintf("%d.%02d", w, f))
}

// atoi64 parses a run of ASCII digits, reporting zero for anything else rather
// than failing: an amount that arrived malformed must not panic a render.
func atoi64(s string) int64 {
	var n int64
	for i := range len(s) {
		if s[i] < '0' || s[i] > '9' {
			return n
		}
		n = n*10 + int64(s[i]-'0')
	}
	return n
}
```

If `strings` is already imported in `amount.go` and a decimal helper already exists there, reuse it and delete the duplicate rather than adding a second one.

- [ ] **Step 4: Run the test to verify it passes**

Run: `cd client && go test ./api/ -run TestCurrencyAmounts -count=1`
Expected: PASS.

- [ ] **Step 5: Update `client/api/types.go`**

Change every amount field on `DashboardSummary`, `CategorySpend`, `MonthlyData`, `BillingCycleTrendItem`, `MoneyFlowNode`, `MoneyFlowEdge`, `MoneyFlowLinkSummary`, `MoneyFlowGraph`, `MoneyFlowTimelinePeriod`, `MoneyFlowTimeline`, `CashFlowCalendarDay`, `CashFlowCalendarMarker`, `CashFlowCalendarCycle`, `CashFlowCalendar`, `LinkFlowTypeTotal`, `LinkCycleLeg`, `LinkCycle`, `LinkOneSidedFlow`, `LinkCycleReport` from `Amount` to `CurrencyAmounts`, and add `TotalNet` to `DashboardSummary` and `MoneyFlowGraph` and `CurrencyScope` to the five top-level types.

- [ ] **Step 6: Add the `currency` parameter to the filters**

In `endpoints_dashboard.go`:

```go
type WindowFilter struct {
	DateFrom string
	DateTo   string
	// AccountID narrows every section to one account.
	AccountID string
	// Currency narrows every section to the accounts holding one currency code.
	// An account already determines its currency, so this is for choosing across
	// several accounts at once. The API is case-insensitive here; send upper case
	// to be explicit.
	Currency string
}

func (f WindowFilter) apply(r *request) *request {
	return r.setQuery("dateFrom", f.DateFrom).
		setQuery("dateTo", f.DateTo).
		setQuery("accountId", f.AccountID).
		setQuery("currency", f.Currency)
}
```

`DashboardFilter`, `MoneyFlowFilter` and `TimelineFilter` embed `WindowFilter`, so they inherit it. Add the same field to the link-cycles filter in `endpoints_links.go`.

- [ ] **Step 7: Update the spec-parity cases**

In `spec_parity_test.go`, add `"currency"` to each of the four dashboard cases' `query` maps and to their `call` closures. `TestEveryRouteHitsItsDocumentedPath` compares the **whole** query set, so a missing entry fails there.

- [ ] **Step 8: Update `backend/openapi.yaml`**

Add the two new schemas, once, near the other dashboard schemas:

```yaml
    # One aggregate's value, keyed by the currency of each account that
    # contributed. A single-currency response has exactly one key. More than one
    # means no total can represent the scope, and the response's currencyScope
    # names what was not combined.
    CurrencyAmounts:
      type: object
      additionalProperties:
        type: number
        description: A decimal amount in major units, in this currency.

    # An account inside a response's scope, with what it contributes. This is how
    # a mixed-currency response says "the USD 120.00 you are not looking at is in
    # this account" instead of dropping it.
    ScopedAccount:
      type: object
      properties:
        id:
          type: string
          format: uuid
        name:
          type: string
        currency:
          type: string
          description: The account's currency code; a stored NULL reads as INR.
        income:
          $ref: "#/components/schemas/CurrencyAmounts"
        expense:
          $ref: "#/components/schemas/CurrencyAmounts"

    # Every currency a reporting response covers, and the accounts behind each.
    CurrencyScope:
      type: object
      properties:
        currencies:
          type: array
          items:
            type: string
          description: >-
            Every currency code in the response, sorted. An empty array means the
            scope covered no accounts.
        accounts:
          type: array
          items:
            $ref: "#/components/schemas/ScopedAccount"
```

Then change every amount property on `CategorySpend`, `MonthlyData`, `BillingCycleTrendItem`, `DashboardSummary`, `MoneyFlowNode`, `MoneyFlowEdge`, `MoneyFlowLinkSummary`, `MoneyFlowGraph`, `CashFlowCalendarDay`, `CashFlowCalendarMarker`, `CashFlowCalendarCycle`, `CashFlowCalendar`, `MoneyFlowTimelinePeriod`, `MoneyFlowTimeline`, `LinkFlowTypeTotal`, `LinkCycleLeg`, `LinkCycle`, `LinkOneSidedFlow`, `LinkCycleReport` from `type: number` to `$ref: "#/components/schemas/CurrencyAmounts"`, add `totalNet` to `DashboardSummary` and `MoneyFlowGraph`, and add `currencyScope` to the five top-level schemas.

Then add the parameter to the five paths, after each `accountId` parameter:

```yaml
        - name: currency
          in: query
          required: false
          description: >-
            Narrow the whole response to accounts holding one currency code
            (three letters, case-insensitive). Without it a response can span
            several currencies, in which case every amount is keyed by currency
            and currencyScope names the accounts behind each.
          schema:
            type: string
            minLength: 3
            maxLength: 3
            example: USD
```

- [ ] **Step 9: Run both parity tests and the client suite**

Run: `cd client && go test ./api/ -count=1`
Expected: PASS, including `TestClientTypesMatchTheSpecSchemas` and `TestRouteTableMatchesTheSpec`.

Run: `make openapi-check`
Expected: PASS.

- [ ] **Step 10: Commit**

```bash
git add backend/openapi.yaml client/api/
git commit -m "feat(api): publish per-currency amounts on the spec and the client

openapi.yaml and client/api land together because the parity test walks
every struct in types.go and compares its JSON tags against the spec's
schemas in both directions; either alone fails it."
```

---

### Task 10: MCP server

**Files:**
- Modify: `mcp/internal/mcpserver/tools_dashboard.go`
- Modify: `mcp/internal/mcpserver/mcpserver.go` (the `instructions` constant and the package doc)
- Modify: `mcp/internal/mcpserver/tools_links.go`, `tools_transactions.go` (prose cross-references)
- Modify: `mcp/internal/mcpserver/tools_test.go` (`argumentCases`)

**Interfaces:**
- Consumes: `api.CurrencyAmounts`, `api.WindowFilter.Currency` (Task 9).
- Produces: nothing other tasks consume. `readonly.SideEffectingGETs` and `Tool.SideEffect` are **unchanged** — no route, verb or write changes.

- [ ] **Step 1: Add the argument cases and watch the audit fail**

In `tools_test.go`, add `currency` to the argument maps:

```go
		"get_dashboard_summary":   {"accountId": testAccountID, "groupBy": "billing_cycle", "cycles": 3, "currency": "INR"},
		"get_money_flow":          {"limit": 5, "currency": "INR"},
		"get_money_flow_timeline": {"currency": "INR"},
		"get_cash_flow_calendar":  {"dateFrom": "2026-01-01", "dateTo": "2026-01-31", "currency": "INR"},
```

and to the link-cycles tool's case. The stub compares the **whole** query set, so a tool that does not forward the argument fails.

- [ ] **Step 2: Run the audit to verify it fails**

Run: `cd mcp && go test ./internal/mcpserver/ -count=1`
Expected: FAIL — the tools do not send `currency`.

- [ ] **Step 3: Add the argument and forward it**

In `tools_dashboard.go`, add `Currency` to `windowArgs`, `summaryArgs` and `moneyFlowArgs`:

```go
	Currency string `json:"currency,omitempty" jsonschema:"narrow the whole response to accounts holding one currency code (three letters); without it every amount is returned keyed by currency"`
```

and pass `Currency: in.Currency` into each `api.WindowFilter`. Do the same for the link-cycles tool in `tools_links.go`.

- [ ] **Step 4: Rewrite the descriptions**

Every description that says "the total" is now false. In `tools_dashboard.go`:

- `get_dashboard_summary`: "income and expense totals" becomes "income, expense and net **per currency** — a single-currency scope has one entry, a mixed one has several and no total is returned, because the API will not add across currencies".
- `get_money_flow`: "Every node carries the total flowing through it" becomes "Every node carries the flow through it **keyed by currency**; a node spanning two currencies has no single total and the API does not invent one".
- `get_money_flow_timeline`: "income, expense and net per calendar month" gains "each **keyed by currency**; a net is a difference within one currency and is never taken across two".
- `get_cash_flow_calendar`: "with window totals" becomes "with window totals **per currency**; `maxAbsNet` is also per currency, so a heatmap is scaled inside one currency rather than across them".

Add the same clarification to the link-cycles tool, whose totals span two accounts by construction.

- [ ] **Step 5: Correct the server instructions**

In `mcpserver.go`, the sentence that is now false:

> Money is returned as decimal major units (for example "1250.50") and every figure is already computed by the server. Do not add amounts up yourself: ask the aggregate tools … and never invent a number that a tool did not return.

becomes:

> Money is returned as decimal major units (for example "1250.50"), keyed by currency code, for example `{"INR": "1250.50"}`. A single-currency scope has exactly one key; a scope spanning several currencies has one key per currency and **no total**, because the API will not add across currencies — say which currency you mean, or pass the `currency` argument to narrow the response. Do not add amounts across keys yourself, and do not add them up at all: ask the aggregate tools (get_dashboard_summary, list_billing_cycles, get_money_flow, get_cash_flow_calendar) when a total is what the user wants, and never invent a number that a tool did not return. Dates are YYYY-MM-DD.

- [ ] **Step 6: Fix the dangling issue references (part of #40)**

In the same file's package doc, the two `Idea #59` / `idea #56` references are replaced with the claims they were standing in for. Read the doc comment and rewrite:

- "Idea #59 describes an agent surface whose mutations are propose-only. This server implements the read half of that, and nothing else:" becomes "This server is read-only, and nothing else: it exposes the ledger, the derived aggregates and the API's read-only preview twins, and no tool can write." The rest of that paragraph already explains the two enforcement mechanisms and needs no change.
- "There is no scoped-token endpoint yet (idea #56), so the server signs in with the same credentials the other clients use:" becomes "There is no scoped read-only token endpoint, so the server signs in with the account's own credentials — which is the whole account's authority, not a read-only subset of it:".

- [ ] **Step 7: Run the MCP suite**

Run: `cd mcp && go test ./... -count=1`
Expected: PASS, including the route audit and `TestEveryToolHitsItsDeclaredRoute`.

Run: `make vet-mcp`
Expected: no output.

- [ ] **Step 8: Commit**

```bash
git add mcp/
git commit -m "fix(mcp): describe per-currency amounts, and drop the dangling idea refs

Every tool description that said \"the total\" was promising a number the
API no longer returns. The package doc's idea #56 and #59 references are
replaced with the claims they stood in for, so the rationale survives
without an issue that may rot again."
```

---

### Task 11: TUI

**Files:**
- Modify: `tui/internal/ui/dashboard.go`, `moneyflow.go`, `calendar.go`
- Create: `tui/internal/ui/currency_test.go`

**Interfaces:**
- Consumes: `api.CurrencyAmounts` (Task 9).
- Produces: `currencyLine(code string, amounts api.CurrencyAmounts) string` — the shared "here is what a currency holds, or why I cannot show one" line, which the tests pin.

- [ ] **Step 1: Write the failing test**

Create `tui/internal/ui/currency_test.go`:

```go
package ui

import (
	"strings"
	"testing"

	"github.com/fintrak/client/api"
)

// TestCurrencyLineRefusesToChooseACurrency is the TUI's half of the change: a
// two-currency map must never render as a single number, because a terminal has
// no room for two and picking one silently is the bug.
func TestCurrencyLineRefusesToChooseACurrency(t *testing.T) {
	single := currencyLine("INR", api.CurrencyAmounts{"INR": "5000.00"})
	if !strings.Contains(single, "INR") || !strings.Contains(single, "5,000.00") {
		t.Errorf("single-currency line = %q, want the code and the formatted amount", single)
	}

	// A currency that is not the selected one is named, not summed in.
	other := currencyLine("INR", api.CurrencyAmounts{"INR": "5000.00", "USD": "120.00"})
	if !strings.Contains(other, "USD") || !strings.Contains(other, "120.00") {
		t.Errorf("line = %q, want the unselected currency named", other)
	}
	if strings.Contains(other, "5,120") {
		t.Errorf("line = %q added two currencies together", other)
	}

	// Nothing to show is said, not rendered as a zero of some currency.
	empty := currencyLine("INR", api.CurrencyAmounts{})
	if empty == "" {
		t.Error("an empty map rendered as nothing at all")
	}
}

// TestCurrencyLineNamesEveryUnselectedCurrency keeps the notice complete: a
// window with three currencies must mention the two the user is not looking at.
func TestCurrencyLineNamesEveryUnselectedCurrency(t *testing.T) {
	got := currencyLine("INR", api.CurrencyAmounts{"INR": "1.00", "USD": "2.00", "EUR": "3.00"})

	for _, want := range []string{"EUR", "3.00", "USD", "2.00"} {
		if !strings.Contains(got, want) {
			t.Errorf("line = %q, missing %q", got, want)
		}
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd tui && go test ./internal/ui/ -run TestCurrencyLine -count=1`
Expected: FAIL to compile — `undefined: currencyLine`.

- [ ] **Step 3: Write `currencyLine`**

Add to `tui/internal/ui/format.go`, next to `signedAmount`:

```go
// currencyLine renders one amount for a screen that is showing a single
// currency. When the response holds the selected currency and nothing else, it
// is the formatted value. When it holds more, the line names the others and
// what they hold rather than choosing one: a terminal has no room for two, and
// picking silently is the bug the per-currency map was introduced to stop.
//
// Empty is "nothing to show", not a zero of some currency — a missing key means
// no transaction in the window touched that currency, which is a different fact
// from "it was zero".
func currencyLine(selected string, amounts api.CurrencyAmounts) string {
	if len(amounts) == 0 {
		return "no transactions"
	}
	if code, value, ok := amounts.Single(); ok {
		if code == selected {
			return value.Display()
		}
		// The response holds exactly one currency and it is not the one on
		// screen, which happens when a filter names a currency the window has
		// nothing in. Saying so is better than rendering a foreign number in
		// the wrong column.
		return "no transactions in " + selected
	}
	// More than one: say so and name what is being left out. Display already
	// renders the explicit multi-currency form ("2 currencies: EUR 1.00, INR
	// 5,000.00"), so this does not re-derive it.
	return amounts.Display() + " — not combined"
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `cd tui && go test ./internal/ui/ -run TestCurrencyLine -count=1`
Expected: PASS.

- [ ] **Step 5: Add the currency to the three screens' filters**

`Dashboard`, `MoneyFlow` and `Calendar` each hold an `api.*Filter` as a field and rebuild it in a `request()` method before the client call — `dashboard.go:129` is the pattern. Three changes per screen, in that order:

1. The filter struct gains `Currency string`.
2. `request()` copies it: `api.DashboardFilter{WindowFilter: api.WindowFilter{…, Currency: d.filter.Currency}, …}`. This is the only place a screen builds a request, so this is the only place the parameter is sent.
3. The filter overlay gains a currency picker. `Dashboard` posts its filter updates under the `dashTagWindow` tag (`dashboard.go:24`), so the new field's update uses that same tag and the screen's existing `Update` case picks it up with no new tag. Use the same picker primitive the account filter in that overlay already uses, so the three screens stay consistent with each other rather than each inventing its own.

The picker lists the codes from the account list the screen already holds. A screen cannot know which currencies a *window* spans before it asks, so the picker is built from the accounts' own currencies — which is exactly right for the common case, and the response's `currencyScope.currencies` is what the notice then reports if the window turned out to hold more.

- [ ] **Step 6: Route every render through `currencyLine`**

- `dashboard.go`'s `cardLine` and `trendRow`: `s.TotalIncome.Display()` becomes `currencyLine(code, s.TotalIncome)`, and the same for expense, net, `item.Income`, `item.Expense` and `spend.Total`.
- `moneyflow.go`'s `headerLines`, `nodeRow`, `linkSummaryLine` and `periodLine`: the same substitution. The `stageMax` and `busiest` denominators become the **selected** currency's figure, falling back to the largest single-currency magnitude when the selected currency has none, so a bar is never sized by a number from a currency the user is not looking at.
- `calendar.go`'s `header`, `openDayInfo` and `legendLine`: the same. `calendarLevel` scales by the selected currency's `MaxAbsNet`; when that is zero the cell is flat rather than dividing by zero (the existing `maxAbs <= 0` guard already does this).
- Keep the existing "net not computed" behaviour: the screens never do arithmetic on money, and `Sub` is the only subtraction, called on the server.

- [ ] **Step 7: Run the TUI suite and the floor**

Run: `cd tui && go test ./... -count=1`
Expected: PASS.

Run: `make test-tui-cover-check`
Expected: PASS — the 18% floor still holds, and the new `currency_test.go` adds covered statements.

- [ ] **Step 8: Commit**

```bash
git add tui/
git commit -m "feat(tui): show one currency at a time and name the others

A terminal has no room for two currencies, so each screen picks one
through its filter and says what it is not showing. The bar and heatmap
denominators are the selected currency's figure, so a quiet foreign
account no longer sizes a bar against a large domestic one."
```

---

### Task 12: Frontend

**Files:**
- Create: `frontend/src/lib/currency.ts`, `frontend/src/lib/currency.test.ts`
- Create: `frontend/src/components/MultiCurrencyNotice/MultiCurrencyNotice.tsx`
- Modify: `frontend/src/types.ts`, `frontend/src/utils/formatters.ts`
- Modify: `frontend/src/components/Dashboard/Dashboard.tsx`, `MoneyFlow/MoneyFlow.tsx`, `CashFlowCalendar/CashFlowCalendar.tsx` and their tests

**Interfaces:**
- Consumes: `CurrencyScope` from `frontend/src/types.ts` (Task 13's types land in this task).
- Produces:
  - `type CurrencyAmounts = Record<string, number>`
  - `function useCurrencyScope(scope: CurrencyScope | undefined): { code: string; codes: string[]; setCode: (c: string) => void; scoped: (a: CurrencyAmounts | undefined) => number; others: ScopedAccount[] }`
  - `function formatScoped(amounts: CurrencyAmounts | undefined, code: string, fallback?: string): string`
  - `function formatScopedMulti(amounts: CurrencyAmounts | undefined): string`
  - `<MultiCurrencyNotice scope={...} selected={...} />`

- [ ] **Step 1: Write the failing test**

Create `frontend/src/lib/currency.test.ts`:

```ts
import { describe, it, expect } from "vitest";
import { formatScoped, formatScopedMulti, formatOne } from "./currency";

describe("formatScoped", () => {
  it("renders the selected currency with its own code", () => {
    expect(formatScoped({ INR: 5000 }, "INR")).toContain("5,000");
    expect(formatScoped({ INR: 5000 }, "INR")).toContain("INR");
  });

  it("reads a missing key as zero rather than as an error", () => {
    // A foreign account that only ever spends: its currency has no income key.
    expect(formatScoped({ USD: -80 }, "USD")).not.toBe("");
    expect(formatScoped({}, "USD", "no transactions")).toBe("no transactions");
  });

  it("never adds two currencies together", () => {
    const out = formatScopedMulti({ INR: 5000, USD: 120 });
    expect(out).toContain("INR");
    expect(out).toContain("USD");
    expect(out.replace(/[^0-9]/g, "")).not.toContain("5120");
  });

  it("degrades instead of throwing on a code Intl does not know", () => {
    // A user can set an account's currency to anything three letters long, and
    // Intl.NumberFormat throws RangeError on a code it cannot resolve. That
    // would take the whole dashboard down over a display detail.
    expect(() => formatScoped({ XYZ: 1234 }, "XYZ")).not.toThrow();
    expect(formatScoped({ XYZ: 1234 }, "XYZ")).toContain("1,234");
  });

  it("degrades instead of throwing on an empty code", () => {
    // accounts.currency is nullable, so a "" key is representable. Intl
    // rejects it too.
    expect(() => formatScoped({ "": 1234 }, "")).not.toThrow();
  });
});

describe("formatScopedMulti", () => {
  it("says the value is not representable rather than picking one", () => {
    const out = formatScopedMulti({ INR: 5000, USD: 120 });
    expect(out).toContain("USD");
    expect(out).toContain("not combined");
  });
});

describe("formatOne", () => {
  it("falls back to a grouped number when Intl rejects the code", () => {
    // Exported so MultiCurrencyNotice can render an account's contribution
    // through the same safe path, rather than calling formatCurrency directly
    // and throwing on a code the user typed into an account.
    expect(formatOne(1234, "XYZ")).toContain("1,234");
    expect(formatOne(1234, "")).toContain("1,234");
  });
});
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd frontend && bun run test -- src/lib/currency.test.ts`
Expected: FAIL — `src/lib/currency.ts` does not exist.

- [ ] **Step 3: Change the types**

In `frontend/src/types.ts`, add at the top of the reporting section:

```ts
/**
 * One aggregate's value, keyed by the currency of each account that contributed.
 * A single-currency scope has exactly one key. More than one means the API
 * refused to return a total, and a missing key reads as zero — never as an
 * error, and never as missing data.
 */
export type CurrencyAmounts = Record<string, number>;

/** One account inside a response's scope, with what it contributes. */
export interface ScopedAccount {
  id: string;
  name: string;
  currency: string;
  income: CurrencyAmounts;
  expense: CurrencyAmounts;
}

/**
 * Every currency a reporting response covers, and the accounts behind each.
 * `currencies` is never null: a scope covering no accounts carries [].
 */
export interface CurrencyScope {
  currencies: string[];
  accounts: ScopedAccount[];
}
```

Then change every amount field on `CategorySpend`, `MonthlyData`, `BillingCycleTrendItem`, `DashboardSummary`, `MoneyFlowNode`, `MoneyFlowEdge`, `MoneyFlowLinkSummary`, `MoneyFlowGraph`, `MoneyFlowTimelinePeriod`, `MoneyFlowTimeline`, `CashFlowCalendarDay`, `CashFlowCalendarMarker`, `CashFlowCalendarCycle`, `CashFlowCalendar`, `LinkFlowTypeTotal`, `LinkCycleLeg`, `LinkCycle`, `LinkOneSidedFlow`, `LinkCycleReport` from `number` to `CurrencyAmounts`, add `totalNet: CurrencyAmounts` to `DashboardSummary` and `MoneyFlowGraph`, and add `currencyScope: CurrencyScope` to the five top-level types.

- [ ] **Step 4: Write `frontend/src/lib/currency.ts`**

```ts
import { useCallback, useEffect, useMemo, useState } from "react";
import { formatCurrency } from "@/utils/formatters";
import type { CurrencyAmounts, CurrencyScope, ScopedAccount } from "@/types";

/**
 * formatOne renders a single currency's amount, and is the only place in the
 * app that calls formatCurrency with a code it did not choose itself.
 *
 * Intl.NumberFormat throws a RangeError on a code it cannot resolve, and an
 * account's currency is three letters a user typed — so "XYZ" is reachable, and
 * so is the empty string accounts.currency can hold when it is NULL. A display
 * detail must not take the dashboard down, so an unusable code falls back to a
 * plain grouped number prefixed with the raw code, which is still honest.
 */
export function formatOne(amount: number, code: string): string {
  if (!code) {
    return groupDigits(amount);
  }
  try {
    return formatCurrency(amount, code);
  } catch {
    return `${code} ${groupDigits(amount)}`;
  }
}

/** groupDigits renders a number with thousands separators and no currency. */
function groupDigits(amount: number): string {
  return new Intl.NumberFormat("en-IN", {
    minimumFractionDigits: 2,
    maximumFractionDigits: 2,
  }).format(amount);
}

/**
 * formatScoped renders one currency's share of an amount, or `fallback` when the
 * map has no entry for it. A missing key is zero, not an error: an account that
 * only ever spends has no income key, and the dashboard still has to draw.
 */
export function formatScoped(
  amounts: CurrencyAmounts | undefined,
  code: string,
  fallback = "0.00",
): string {
  if (!amounts) return fallback;
  return formatOne(amounts[code] ?? 0, code);
}

/**
 * formatScopedMulti names every currency in a map and refuses to total them.
 * This is the notice's line, so it must read as a refusal rather than as a
 * figure: the digits of the two currencies must never appear adjacent.
 */
export function formatScopedMulti(amounts: CurrencyAmounts | undefined): string {
  const codes = Object.keys(amounts ?? {}).sort();
  if (codes.length === 0) return "no transactions";
  if (codes.length === 1) return formatOne(amounts![codes[0]], codes[0]);
  return `${codes.length} currencies: ${codes
    .map((code) => formatOne(amounts![code], code))
    .join(", ")} — not combined`;
}



export interface CurrencyScopeSelection {
  /** The currency on screen, or "" before any scope has loaded. */
  code: string;
  /** Every currency the response covers, sorted. Empty before it loads. */
  codes: string[];
  setCode: (code: string) => void;
  /** The selected currency's share of an amount, as a number for a chart. */
  scoped: (amounts: CurrencyAmounts | undefined) => number;
  /** Every account in a currency the user is not currently looking at. */
  others: ScopedAccount[];
}

/**
 * useCurrencyScope owns which currency a report is showing.
 *
 * The selection is deliberately NOT sent to the API: the response always carries
 * every currency in the window, so switching is instant and switching back costs
 * nothing. That also means the report is never silently narrowed — the other
 * currencies are in the payload and `others` names them.
 *
 * The default is the first code, which is sorted, so the choice is stable rather
 * than depending on object key order.
 */
export function useCurrencyScope(
  scope: CurrencyScope | undefined,
): CurrencyScopeSelection {
  const codes = useMemo(() => [...(scope?.currencies ?? [])].sort(), [scope]);
  const [chosen, setChosen] = useState("");

  // A response that no longer holds the chosen currency must not leave the
  // screen on a currency it does not have.
  useEffect(() => {
    setChosen((current) => (current && codes.includes(current) ? current : ""));
  }, [codes]);

  const code = chosen || codes[0] || "";

  const scoped = useCallback(
    (amounts: CurrencyAmounts | undefined) => (amounts ? (amounts[code] ?? 0) : 0),
    [code],
  );

  const others = useMemo(
    () => (scope?.accounts ?? []).filter((account) => account.currency !== code),
    [scope, code],
  );

  return { code, codes, setCode: setChosen, scoped, others };
}
```

- [ ] **Step 5: Run the test to verify it passes**

Run: `cd frontend && bun run test -- src/lib/currency.test.ts`
Expected: PASS — including the `Intl`-rejecting and empty-code cases, which are Review Focus #3 and #1's UI half.

- [ ] **Step 6: Write `MultiCurrencyNotice`**

```tsx
import { formatCurrency } from "@/utils/formatters";
import type { ScopedAccount } from "@/types";

/**
 * MultiCurrencyNotice names the money a report is not showing.
 *
 * It is not a warning about a mistake: the API refused to total these currencies
 * on purpose. It exists so the report can say what it left out and where it is,
 * which is the difference between "my USD account is not counted" and "it is,
 * here is the number, switch to see it".
 *
 * Renders nothing for a single currency — the common case must not pay for the
 * edge case with a permanent line of text.
 */
export default function MultiCurrencyNotice({
  accounts,
  selected,
}: {
  accounts: ScopedAccount[];
  selected: string;
}) {
  const others = accounts.filter((account) => account.currency !== selected);
  if (others.length === 0) return null;

  const byCurrency = new Map<string, ScopedAccount[]>();
  for (const account of others) {
    const list = byCurrency.get(account.currency) ?? [];
    list.push(account);
    byCurrency.set(account.currency, list);
  }

  return (
    <div className="mb-4 px-4 py-2 bg-muted border border-border rounded-lg text-[13px] text-muted-foreground">
      <span className="font-medium text-foreground">
        {byCurrency.size === 1 ? "1 other currency" : `${byCurrency.size} other currencies`}
      </span>{" "}
      in {others.length} {others.length === 1 ? "account" : "accounts"} not shown here
      {": "}
      {[...byCurrency.entries()]
        .sort(([a], [b]) => a.localeCompare(b))
        .map(([code, list]) => {
          const total = list.reduce((sum, account) => sum + (account.income[code] ?? 0), 0);
          return `${code} ${formatCurrency(total, code)} (${list.map((a) => a.name).join(", ")})`;
        })
        .join("; ")}
      . These are not added to the figures above.
    </div>
  );
}
```

`formatCurrency` here is called with a code from the account row, which is one the user typed. Wrap it the same way `formatOne` does in `currency.ts` — export `formatOne` from there and use it, rather than letting a bad code throw inside the notice.

- [ ] **Step 7: Update `Dashboard.tsx`**

- `netSavings` is **deleted**. `data.totalNet` is the server's number, and a client-side subtraction of two maps is the arithmetic this change exists to remove. `{formatCurrency(netSavings)}` becomes `{formatCurrency(scoped(data.totalNet))}` and the `netSavings >= 0` sign test becomes `scoped(data.totalNet) >= 0`.
- The trend chart's `dataKey="income"` / `"expense"` cannot read a map. Map the series first:

```tsx
  const trendSeries = useMemo(
    () =>
      trendData.map((point) => ({
        ...point,
        income: scoped((point as MonthlyData).income),
        expense: scoped((point as MonthlyData).expense),
      })),
    [trendData, scoped],
  );
```

- The Y-axis formatter at `Dashboard.tsx:552` is a hardcoded `₹` and goes:

```tsx
                      tickFormatter={(v) => formatCurrency(v, code)}
```

- `CategoryPieSection`'s `dataKey="total"` and its side list take the selected currency's share, via the same projection before the `<Pie>`.
- Add a currency `Select` beside the `AccountSelect` in the filter bar, and `<MultiCurrencyNotice>` above the stat cards.

- [ ] **Step 8: Update `MoneyFlow.tsx`**

- `const net = data.totalIncome - data.totalExpense` is **deleted**; use `data.totalNet`.
- `sankeyData`'s `value: l.value` becomes `value: scoped(l.value)`, and each node's `total` likewise.
- The stage and busiest denominators (`MoneyFlow.tsx:764-767`, `1014-1019`) become the selected currency's figure.
- The timeline strip's `incomePct`/`expensePct` and the `p.net >= 0` test go through `scoped`.
- Same currency `Select` and notice.

- [ ] **Step 9: Update `CashFlowCalendar.tsx`**

- `DayCell`'s flat numeric mirror becomes a currency-aware projection: build the weeks from `scoped(d.income)`, `scoped(d.expense)`, `scoped(d.net)` for the selected currency, so `dayBackground` and `cellLabel` keep receiving plain numbers and the `?? 0` zero-fill becomes "the map has no key for this currency", which is the same thing.
- `maxAbs={data.maxAbsNet}` becomes `maxAbs={scoped(data.maxAbsNet)}`.
- The three `StatCard`s and the tooltip's sign tests go through `scoped`.
- Same currency `Select` and notice.

- [ ] **Step 10: Update the three component test fixtures**

`Dashboard.test.tsx`'s `summary()`, `MoneyFlow.test.tsx`'s `graph()` and `timeline()`, and `CashFlowCalendar.test.tsx`'s `calendar()` all change to the map form, with a `currencyScope`. Each gains one test asserting the notice names an unselected currency when the fixture has two — that is the behaviour a user with two accounts would look for first.

- [ ] **Step 11: Run the frontend suite, typecheck and build**

Run: `cd frontend && bun run typecheck`
Expected: no errors. This is the step that finds every `data.totalIncome` the type change left behind.

Run: `cd frontend && bun run test`
Expected: PASS.

Run: `cd frontend && bun run build`
Expected: succeeds.

- [ ] **Step 12: Run the coverage floor**

Run: `cd frontend && bun run test:coverage`
Expected: PASS — the 85% regression floor holds.

- [ ] **Step 13: Commit**

```bash
git add frontend/
git commit -m "fix(frontend): show one currency per report and name the others

The currency selector sits beside the account filter and scopes the
whole report; a notice names the accounts behind every currency it is
not showing, with their totals. netSavings and the Money Flow net are
deleted rather than adapted: the server computes them now, and a
client-side subtraction of two maps is the arithmetic this change exists
to remove. The trend chart's hardcoded rupee axis goes with it."
```

---

### Task 13: Documentation (#40, #41, #42) and the release note

**Files:**
- Modify: `README.md` (three places)
- Modify: `AGENTS.md` (three places)
- Modify: `docs/feature-proposals.md` (commit it, unchanged)

**Interfaces:**
- Consumes: nothing. No code depends on this task.

- [ ] **Step 1: Fix #42 — the `IDEAS.md` that does not exist**

In `README.md`'s project-structure tree, delete the line:

```
├── IDEAS.md             # Feature backlog
```

and add, under `docs/`:

```
└── docs/
    ├── feature-proposals.md   # Unreviewed feature proposals, for maintainer review
```

`docs/feature-proposals.md` is untracked. Add it as it stands — its own header says *"proposals only. Nothing here is designed, scheduled or approved"*, so committing it as the review candidate it declares itself to be is honest, while adopting it as a committed backlog under the name `IDEAS.md` would mislabel it.

- [ ] **Step 2: Fix #40 — the dangling `#56` / `#59` references**

`README.md:292` currently reads: *"There is no scoped read-only token yet (backlog idea #56), which is why the server takes the account credentials; the propose-and-apply half of idea #59 is deliberately not implemented."*

Replace with the claims themselves, self-contained and checkable:

> There is no scoped read-only token endpoint, so the server signs in with the account's own credentials — which is the whole account's authority, not a read-only subset of it. A propose-and-apply surface, where an agent's mutations are typed, validated and diff-previewed before a user confirms them, is not implemented; the server is read-only and nothing else.

`AGENTS.md:87` and `:89` drop the `idea #59` parentheticals, and `AGENTS.md:90`'s closing sentence becomes *"Never add a write tool here without a decision about the propose-and-apply surface, which does not exist."*

- [ ] **Step 3: Fix #41 — the route-parity tests the TUI does not have**

`AGENTS.md:71` currently claims the TUI *"covers every operation in `backend/openapi.yaml` through the shared client and TUI route-parity tests."* No such test exists. Replace with the true version:

> ... and it covers every operation in `backend/openapi.yaml` through the shared client. The guarantee is `client/api/spec_parity_test.go`, which the TUI inherits transitively by depending on `client/api`: there is no route-parity test in this module.

- [ ] **Step 4: Document the new response shape and parameter**

In `README.md`'s API overview, update the five endpoint entries (`/dashboard/summary`, `/dashboard/money-flow`, `/dashboard/money-flow/timeline`, `/dashboard/cash-flow-calendar`, `/links/cycles`) to state that amounts arrive keyed by currency and that `currencyScope` names the accounts behind each, and document the new `currency` parameter. Update the MCP and TUI sections for the per-currency shape.

In `AGENTS.md`, add the invariant next to the money paragraph, since it is the same class of rule:

> **Amounts on the reporting endpoints are `CurrencyAmounts`, never a scalar.** `accounts.currency` is read by every aggregate for the first time in this change, and a `CurrencyAmounts` with more than one key means no total can represent its scope. Nothing collapses one into a number — not the SQL, not `client/api` (which has `Display` refuse rather than pick), not the TUI. A derived value is a difference within one currency and is computed with `Sub`; a value that could only exist by adding across currencies, like the calendar's `maxAbsNet`, is a map too.

- [ ] **Step 5: Run the docs gate**

Run: `make docs-check`
Expected: PASS — it validates Markdown links, and a removed line can orphan a reference.

- [ ] **Step 6: Commit**

```bash
git add README.md AGENTS.md docs/feature-proposals.md
git commit -m "docs: fix three documentation defects and document per-currency amounts

#42: the README's project tree listed an IDEAS.md that does not exist.
The proposals are committed where they are, as the review candidates
they declare themselves to be, rather than relabelled as a backlog.

#40: README and mcpserver.go cited backlog ideas #56 and #59, which a
reader cannot follow because the tracker is empty. The two gaps are now
stated as properties of the server, and the file no longer points at an
issue that may rot again.

#41: AGENTS.md credited the TUI with route-parity tests that do not
exist. The guarantee is real but comes from client/api/spec_parity_test.go,
which the TUI inherits transitively."
```

- [ ] **Step 7: Run the full gate**

Run: `make test-cover-check test-client-cover-check test-tui-cover-check test-mcp-cover-check test-parser-cover-check openapi-check docs-check`
Expected: PASS — every floor, the spec check and the docs check.

Run: `make vet vet-client vet-tui vet-mcp`
Expected: no output.

Run: `cd backend && go mod tidy && cd .. && git diff --exit-code go.mod go.sum backend/go.mod backend/go.sum client/go.sum tui/go.sum mcp/go.sum`
Expected: no diff. CI enforces this.

Run: `make test-integration`
Expected: PASS. Task 14 has already moved these tests to the map form.

- [ ] **Step 8: Write the release note into the PR body**

The PR description must carry, verbatim:

> **Breaking change.** The response shape of `/dashboard/summary`, `/dashboard/money-flow`, `/dashboard/money-flow/timeline`, `/dashboard/cash-flow-calendar` and `/links/cycles` changed: every amount is now an object keyed by currency code, for example `"totalIncome": {"INR": "50000.00"}`, instead of a bare number. Each response also carries a `currencyScope` naming the accounts behind each currency.
>
> **If you read a mixed-currency total, the number you had was wrong.** A USD account's dollars were being added to an INR account's rupees. It was silent — no error, just a number that could not be right. Those figures now arrive per currency with nothing summed across them.
>
> If you are on a single currency, the shape still changed and you will need to read `totalIncome.INR` rather than `totalIncome`. All four clients in this repository (web, TUI, MCP, shared Go client) are updated in the same release, so no action is needed if you use the app rather than the API directly.

---

### Task 14: Integration tests against real Postgres

The unit suite cannot see two of this change's guarantees, because `pgxmock` matches query *strings* and never executes them. Both live here, against a real database.

**Files:**
- Modify: `backend/integration_test.go` (build tag `integration`, `package main`)

**Interfaces:**
- Consumes: the finished handlers (Tasks 4–8) and the router.
- Produces: nothing other tasks consume.

- [ ] **Step 1: Update the three existing tests to the map form**

`TestIntegrationMoneyFlowGraph` asserts `graph.TotalIncome == 5000` and `graph.TotalExpense == 1500`; `TestIntegrationCashFlowCalendar` asserts `all.Net == 3500` and `all.MaxAbsNet == 5000`; `TestIntegrationMoneyFlowAccountEdgesAndTimeline` asserts `l.Value == 500` and a period's income/expense. Each becomes a `models.CurrencyAmounts{"INR": money.FromFloat(…)}` comparison, since the fixtures these tests create use the default `INR` currency. The per-node and per-day loop assertions become per-currency lookups.

- [ ] **Step 2: Add the mixed-currency case**

This is the test that proves the bug is fixed, and the only place the guarantee is checked end to end:

```go
// TestIntegrationAggregatesRefuseToCombineCurrencies is the guarantee this
// change exists for: a window over accounts in two currencies produces
// per-currency subtotals and a scope naming both accounts, and no figure
// anywhere in the response is a sum across them.
func TestIntegrationAggregatesRefuseToCombineCurrencies(t *testing.T) {
	// Create a second account whose currency is USD, add a credit in one
	// currency and a debit in the other, then assert against every one of the
	// five endpoints:
	//
	//   - the per-currency map holds both keys, with the right values
	//   - currencyScope.currencies is [INR USD] and currencyScope.accounts
	//     names both accounts with their own per-currency income/expense
	//   - a net is negative for the currency that only spent
	//   - decoding the raw body and walking it finds no JSON number at any
	//     path ending in a total, income, expense, net, value, maxAbsNet,
	//     outstanding, gross or totalCircular
	//
	// That last assertion is the one that matters: it fails if anyone ever
	// adds a scalar field back, which is the shape this change removed.
}
```

- [ ] **Step 3: Add the NULL-currency case (Review Focus #1)**

```go
// TestIntegrationNullCurrencyReadsAsINR covers accounts.currency being
// `VARCHAR(3) DEFAULT 'INR'` with no NOT NULL (migration 000001), so a row can
// genuinely hold NULL. It must read as INR rather than becoming a "" key, which
// is the only way a consumer would ever see a blank currency.
func TestIntegrationNullCurrencyReadsAsINR(t *testing.T) {
	// INSERT an account with currency explicitly NULL (the column default
	// does not apply to an explicit NULL), add a transaction, and assert the
	// response's currencyScope reports "INR" for it and that the map key is
	// "INR" rather than "".
}
```

- [ ] **Step 4: Add the category-ranking case (Review Focus #5)**

```go
// TestIntegrationCategoryTopFifteenIsPerCurrency runs the real window-function
// query, which pgxmock cannot: a category with no matching transaction must be
// absent from the breakdown rather than ranking first in an empty partition and
// displacing a real one.
func TestIntegrationCategoryTopFifteenIsPerCurrency(t *testing.T) {
	// Create 20 categories spending in INR and 20 spending in USD, plus one
	// category with no transactions at all, and assert each currency's
	// breakdown holds at most 15 rows, that the empty category appears in
	// neither, and that both currencies have their own top-ranked category.
}
```

- [ ] **Step 5: Run the integration suite**

Run: `make test-integration`
Expected: PASS. Docker and testcontainers are required; this is the one suite in the repository that needs them, and it is opt-in for that reason.

- [ ] **Step 6: Commit**

```bash
git add backend/integration_test.go
git commit -m "test(integration): cover the multi-currency guarantee end to end

pgxmock matches query strings and never executes them, so the two
guarantees only a real database can show — a NULL accounts.currency
reading as INR, and the per-currency top-15 window function not ranking
an empty partition — are pinned here, along with an assertion that walks
the raw response for any scalar that would reintroduce the bug."
```

---

## Execution Order

Tasks 1 → 2 → 3 are independent of each other and of everything after them; run them in any order, or in parallel. Tasks 4–8 are independent of each other and **must all complete before Task 9**, because Task 9's spec must describe the final shape. Task 9 must precede 10, 11 and 12, all three of which are independent of each other. Task 14 follows 4–8. Task 13 is independent and can run at any point.

```
1 ─┐
2 ─┼─→ 4 ─┐
3 ─┘      ├─→ 9 ─┬─→ 10
           5 ─┤     ├─→ 11
           6 ─┤     └─→ 12
           7 ─┤
           8 ─┘
           └─→ 14
13 (independent, any time)
```
