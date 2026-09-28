# asOf Reporting, Slice 1 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `asOf=YYYY-MM-DD` on `GET /dashboard/summary` and `GET /transactions`, reporting the ledger's state at the end of that day — windowed totals plus a per-account balance, which for a loan account is outstanding principal rather than a bank-style net.

**Architecture:** `asOf` is a query parameter that rides the filter path the reporting handlers already share, clamped against `dateTo`. The windowed figures are that shared fragment plus one `t.date <= $n` clause, so every section of the response describes the same transactions. The per-account balance is a **separate** query with no lower date bound, because a balance is state and a window is activity; it reproduces `GetAccounts`' two branches (including the loan branch) so an `asOf` balance never disagrees with `/accounts` about the same account on the same day. Both response fields are `omitempty`, so a request without `asOf` gets byte-identical output to today.

**Tech Stack:** Go 1.27, Gin, pgx v5, PostgreSQL, `pgxmock` for handler tests; `openapi.yaml` kept in lockstep by `make openapi-check`.

**Spec:** `docs/superpowers/specs/2026-09-29-asof-reporting-design.md`

## Global Constraints

- Amounts are `money.Amount` (int64 minor units). **Never** do money arithmetic in `float64`.
- A reporting amount is `models.CurrencyAmounts`, **never** a scalar. `Add` skips a zero contribution, so a zero adds no key.
- Read an account's currency only through the shared expression: `currencysql.Column("a.currency")`, aliased in `dashboard.go` as the package var `accountCurrency`. Never write a second `COALESCE(NULLIF(...), 'INR')`.
- Tests need no database. Use `newTestServer(mock)` + `newDashboardTestRouter(...)`; always `gin.SetMode(gin.TestMode)`. `pgxmock` asserts **exact** SQL and argument order.
- Log with `log/slog` and typed attrs: `slog.Error("...", slog.String("error", err.Error()))`.
- Errors go through `validation.RespondError(c, msg, http.StatusBadRequest)`.
- `openapi.yaml` and the routes must stay in lockstep; `make openapi-check` enforces it.
- Balance arithmetic against a real ledger is only reachable in the integration suite: `backend/integration_test.go`, build tag `integration`, run with `make test-integration`.

## Review Focus

Each of these is a way this feature can be *plausibly* wrong in a way the spec implies but the happy-path tests will not catch. Each line names a test that must exist, in the task that owns the code.

1. **A loan with payments on both sides of `asOf`.** The balance must reflect only the payments dated on or before it. Omitting the loan branch does not error — it reports a car loan's balance as roughly zero, which reads as "you owed nothing" rather than as a bug. *(Task 5, integration)*
2. **`asOf` before the earliest transaction.** Must be 200 with every account listed and a zero balance, *not* an empty `balances` array. An empty array and a list of zeroes are different answers, and only the second is true. *(Task 5)*
3. **An ordinary request with no `asOf`.** Must issue **no** balance query at all. Silently adding one taxes every dashboard load in production and no functional test fails. *(Task 4)*
4. **`asOf` mixed with a `dateFrom` later than it.** Must be 400 naming both dates. Returning an empty ledger instead reads as "you had nothing then". *(Task 2)*
5. **A mixed-currency response with `asOf`.** Two accounts in two currencies must produce two keys and no total, in both `balances` and `CurrencyScope`. A test asserting only the INR account passes while the USD money silently vanishes. *(Task 4)*

---

### Task 1: `parseAsOf` — validation, the window, and the clamp

**Files:**
- Create: `backend/handlers/asof.go`
- Test: `backend/handlers/asof_test.go`

**Interfaces:**
- Consumes: `validation.RespondError`, `validation.CheckTransactionDate(value string, now time.Time) (time.Time, string)`, and the existing `parseQueryDate(c *gin.Context, name, value string) (string, bool)` from `transaction.go:70`.
- Produces:
  ```go
  // parseAsOf resolves the `asOf` parameter against the window. It writes a 400
  // and returns ok=false on a malformed or out-of-window value, or when dateFrom
  // is strictly after the clamped asOf.
  func parseAsOf(c *gin.Context, dateFrom, dateTo string) (asOf string, ok bool)
  ```
  `asOf` is `""` when the caller did not supply the parameter, and otherwise the
  effective `YYYY-MM-DD` after clamping against `dateTo`.

- [ ] **Step 1: Write the failing tests**

In `backend/handlers/asof_test.go`, `package handlers`, use `gin.CreateTestContext` +
`httptest.NewRecorder()` to drive a request, and assert on `w.Code` and the
recorded body. Cover exactly these five cases:

| Case | Request | Want |
|---|---|---|
| absent | `?` | `asOf == ""`, `ok == true`, no 400 |
| valid | `?asOf=2026-03-01` | `"2026-03-01"`, ok |
| clamped by `dateTo` | `?asOf=2026-03-01&dateTo=2026-02-15` | `"2026-02-15"`, ok |
| `dateTo` later does not widen | `?asOf=2026-03-01&dateTo=2026-06-30` | `"2026-03-01"`, ok |
| malformed | `?asOf=01/03/2026` | 400, body contains `asOf must be YYYY-MM-DD` |
| out of window | `?asOf=1800-01-01` | 400, body contains the `CheckTransactionDate` range message |
| `dateFrom` after `asOf` | `?asOf=2026-03-01&dateFrom=2026-04-01` | 400, body names both `dateFrom` and `asOf` values |

Give the table-driven test a helper that sets the query on the request before
running the handler, so each case differs only in its query string.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd backend && go test ./handlers/ -run TestParseAsOf -v`
Expected: FAIL — build error, `undefined: parseAsOf`.

- [ ] **Step 3: Implement `parseAsOf` in `backend/handlers/asof.go`**

Signature exactly as in the Interfaces block. Behaviour, in order:

1. `raw := c.Query("asOf")`. If `""`, return `"", true`.
2. Reject a malformed value with `validation.RespondError(c, "asOf must be YYYY-MM-DD", http.StatusBadRequest)`. Reuse the same `time.Parse("2006-01-02", value)` check `parseQueryDate` uses, so the two cannot word the same failure differently.
3. Enforce the ledger window with `validation.CheckTransactionDate(raw, time.Now())`; on a non-empty message, `validation.RespondError(c, "asOf: "+msg, http.StatusBadRequest)`.
4. Clamp: if `dateTo != ""` and `dateTo < raw` (both are zero-padded `YYYY-MM-DD`, so lexical order is chronological), return `dateTo`.
5. If `dateFrom != ""` and `dateFrom > asOf`, `validation.RespondError(c, fmt.Sprintf("dateFrom %s is after asOf %s", dateFrom, asOf), http.StatusBadRequest)` and return `"", false`. The comparison is `>` not `>=`: `dateFrom == asOf` is a single day, which is legitimate.

Add a doc comment stating that `asOf` means the state at the **end** of the day
(a transaction dated that day counts) and that the clamp never lets the window
extend past it.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `cd backend && go test ./handlers/ -run TestParseAsOf -v`
Expected: PASS, all seven cases.

- [ ] **Step 5: Commit**

```bash
git add backend/handlers/asof.go backend/handlers/asof_test.go
git commit -m "feat(dashboard): parseAsOf validates the instant and clamps it to the window"
```

---

### Task 2: `asOf` narrows the transaction list

**Files:**
- Modify: `backend/handlers/transaction.go` (`txnQueryFilter`, which builds `txnFilter`)
- Test: `backend/handlers/asof_transaction_test.go`

**Interfaces:**
- Consumes: `parseAsOf(c, dateFrom, dateTo) (string, bool)` from Task 1. The `dateFrom`/`dateTo` it is given must be the values `txnQueryFilter` has already parsed, so the clamp and the filter cannot disagree.
- Produces: `txnFilter` gains nothing new — `asOf` is appended through the existing `clause`/`param` path as a `t.date <=` condition, so the list, its `COUNT(*)` and `ExportTransactions` (which shares `txnQueryFilter`) all narrow together.

**Note:** `ExportTransactions` shares `txnQueryFilter`, so it inherits `asOf`
automatically. That is correct and is not a separate task — but do **not** add
`asOf` to `openapi.yaml` for the export operation in this task. Task 6 covers the
spec, and only for the two operations the slice names.

- [ ] **Step 1: Write the failing test**

In `backend/handlers/asof_transaction_test.go`, build a router with
`r.GET("/transactions", srv.GetTransactions)`, seed a `pgxmock` pool whose
`ListTransactions` expectation is:

```go
mock.ExpectQuery("SELECT t.id, .* FROM transactions t").
    WithArgs(userID, "2026-03-01").          // the asOf bound value
    WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow("r1"))
```

Assert the handler returns 200 and that the query matched. `pgxmock` fails the
test if the arguments do not match exactly, so this is what pins the parameter
into the statement.

Add a second case: `?asOf=2026-03-01&dateFrom=2026-04-01` must return 400 **and
issue no query at all** — assert with `mock.ExpectationsWereMet()` that no
`ExpectQuery` was registered, so an unfulfilled expectation is the failure.

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd backend && go test ./handlers/ -run TestAsOfList -v`
Expected: FAIL — the handler issues no `asOf` argument, so `pgxmock` reports an
argument mismatch, or the 400 case returns 200.

- [ ] **Step 3: Append the clause in `txnQueryFilter`**

Inside `txnQueryFilter`, after `dateFrom`/`dateTo` are parsed and **before**
`compileQuery` is called (which must stay last — see `query_sink.go:20-26`):

```go
asOf, ok := parseAsOf(c, dateFrom, dateTo)
if !ok {
    return f, uuid.Nil, nil, false
}
if asOf != "" {
    f.clause("t.date <=", asOf)
}
```

Use `f.clause` so the placeholder is allocated by the existing numbering; do not
hand-write a `$n`. Then set `dateTo = asOf` on the filter's own date range so any
later code reading the window sees the clamped upper bound rather than the
requested one.

- [ ] **Step 4: Run the test to verify it passes**

Run: `cd backend && go test ./handlers/ -run TestAsOfList -v`
Expected: PASS.

- [ ] **Step 5: Run the full backend suite for regressions**

Run: `cd backend && go test ./...`
Expected: PASS. If any existing test fails, it pinned an exact argument list that
has legitimately changed — update its expectation rather than loosening it.

- [ ] **Step 6: Commit**

```bash
git add backend/handlers/transaction.go backend/handlers/asof_transaction_test.go
git commit -m "feat(transactions): the list narrows to asOf, clamped to the window"
```

---

### Task 3: `AccountBalance` on the model

**Files:**
- Modify: `backend/models/models.go` (add `AccountBalance` after `ScopedAccount` at line 1821; add two fields to `DashboardSummary` at line 962)
- Test: `backend/models/models_test.go` — one test for the JSON shape.

This task is a declaration with no logic, so TDD's red step is the JSON test
rather than a behaviour: write the test that asserts a summary with no `asOf`
serialises without the two keys, watch it fail against the current
`DashboardSummary` (the fields do not exist), then add them.

- [ ] **Step 1: Write the failing JSON test**

In `backend/models/models_test.go`: marshal a `DashboardSummary` with both new
fields left nil and assert the body contains neither `"asOf"` nor `"balances"`.
Then marshal one with `AsOf` set to `"2026-03-01"` and `Balances` set to a
one-element slice, and assert both keys are present and `balance` is an object
keyed by currency code rather than a number.

This is the test that catches the pointer requirement. With a bare
`[]AccountBalance`, the first assertion fails on `"balances": []`.

- [ ] **Step 2: Run it to verify it fails**

Run: `cd backend && go test ./models/ -run TestDashboardSummaryAsOfJSON -v`
Expected: FAIL — build error, `unknown field AsOf`.

- [ ] **Step 3: Add the type**

```go
// AccountBalance is one account's balance as of a date. Balance is a
// CurrencyAmounts and never a scalar: an account carries a currency, and an
// as-of response can span several, so a map is the only honest representation.
// A zero balance adds no key, so len() counts the accounts that actually held
// money at that date.
//
// For a loan account this is outstanding principal, derived from the
// transactions attached to the loan — the same figure /accounts shows — not a
// bank-style net over the loan account's own (empty) ledger.
type AccountBalance struct {
    ID       uuid.UUID       `json:"id"`
    Name     string          `json:"name"`
    Currency string          `json:"currency"`
    Balance  CurrencyAmounts `json:"balance"`
}
```

- [ ] **Step 4: Add the two fields to `DashboardSummary`**

```go
    // AsOf is the instant the balances below were computed, echoed so a client
    // holding a cached response can tell what it is looking at. Present only when
    // the request asked for one: a caller that did not ask gets no field, so
    // responses without asOf are unchanged.
    AsOf *string `json:"asOf,omitempty"`
    // Balances is each account's balance at AsOf, including accounts holding
    // nothing at that date. Present only when the request asked for one.
    //
    // It is a POINTER, not a bare slice, and that is load-bearing: encoding/json
    // does not omit an empty slice, so a plain `[]AccountBalance` with omitempty
    // would serialise as `"balances": []` on every ordinary summary. A nil
    // pointer is omitted; a non-nil pointer to an empty slice is not, which is
    // exactly the distinction Review Focus #2 depends on — "no accounts" and "no
    // account held money" are different answers and only the second is true.
    Balances *[]AccountBalance `json:"balances,omitempty"`
```

Note in the comment that `asOf` here is a *response* field and that
`models.LoanPayoff.AsOf` is a different thing sharing the JSON name — they never
appear in the same payload, and that is deliberate.

- [ ] **Step 5: Run the test to verify it passes**

Run: `cd backend && go test ./models/ -run TestDashboardSummaryAsOfJSON -v`
Expected: PASS. Then `cd backend && go build ./... && go vet ./models/`
Expected: no output.

- [ ] **Step 6: Commit**

```bash
git add backend/models/models.go backend/models/models_test.go
git commit -m "feat(models): AccountBalance, and the two as-of fields on the summary"
```

---

### Task 4: The balance query, on the summary

**Files:**
- Create: `backend/handlers/asof_balance.go`
- Modify: `backend/handlers/dashboard.go` (in `GetDashboardSummary`, after `currency` is parsed at line 93; the `catFilter` fragment is built at lines 145-169)
- Test: `backend/handlers/asof_balance_test.go`

**Interfaces:**
- Consumes: `parseAsOf` (Task 1), `models.AccountBalance` (Task 3), the `accountCurrency` var and `currencyPredicate` in `dashboard.go`, and the `q` queryer the handler already holds inside its read-only transaction (line 181).
- Produces:
  ```go
  // accountBalancesAsOf returns every one of the user's accounts with its balance
  // at asOf, including the ones holding nothing. The returned slice is non-nil
  // whenever the query succeeds, so an empty list means "no accounts", never
  // "no money".
  func (srv *Server) accountBalancesAsOf(ctx context.Context, q scopeQueryer, userID uuid.UUID, asOf string) ([]models.AccountBalance, error)
  ```

- [ ] **Step 1: Write the failing tests**

In `backend/handlers/asof_balance_test.go`. The unit test drives
`accountBalancesAsOf` against a `pgxmock` pool and the `scopeQueryer` interface
already used by `currencyScope`; the handler test drives the whole endpoint.

Cases, each with its own `pgxmock` expectation:

1. **`asOf` absent issues no balance query.** Register only the queries an
   ordinary summary makes, call `GetDashboardSummary` with no `asOf`, then
   `mock.ExpectationsWereMet()`. This is Review Focus #3 — if the balance query
   ran unconditionally, `pgxmock` would report an unexpected query and fail.
2. **A credit-positive account.** Rows: `a1` / `Everyday` / `INR` /
   `{INR: 50000}`. Assert `len(balances) == 1` and
   `balances[0].Balance["INR"]` is 50000.
3. **A debit-positive account** (a card). Rows: `a1` / `Visa` / `USD` /
   `{-USD: 2500}`. Assert the key is present and negative — `IsNegative` is true
   and the sign is the data's, not chosen.
4. **A loan.** Rows: `loan1` / `Car loan` / `INR` / `{-INR: 1200000}`. Assert the
   same, and that the generated SQL contains `loan_attachments` and
   `t.date <=` — this is what pins the loan branch, which is the branch whose
   absence fails silently (Review Focus #1).
5. **A zero balance adds no key.** Row: `a1` / `Everyday` / `INR` / `nil`. Assert
   `len(balances[0].Balance) == 0` and `balances[0].Currency == "INR"`.
6. **Two accounts, two currencies** (Review Focus #5). Assert two entries, two
   distinct keys across them, and that the response's `CurrencyScope.Currencies`
   names both. Assert nothing anywhere presents a single total.
7. **`asOf` before everything.** All balances zero, `balances` **non-empty**
   (Review Focus #2). Assert `len(balances) == <number of accounts>`, not 0.
8. **No `asOf` omits both fields.** A summary with no `asOf` must serialise
   without the keys `"asOf"` and `"balances"` anywhere in the body. This is the
   assertion that catches the pointer/omitempty mistake described in Task 3, and
   it is a body assertion rather than a struct-field one precisely because the
   bug is in the JSON, not the Go value.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd backend && go test ./handlers/ -run "TestAccountBalancesAsOf|TestDashboardAsOf" -v`
Expected: FAIL — build error, `undefined: accountBalancesAsOf`.

- [ ] **Step 3: Implement `accountBalancesAsOf` in `backend/handlers/asof_balance.go`**

One query over `accounts a JOIN account_types at`, selecting
`a.id, a.name, <accountCurrency>, <balance>`, `WHERE a.user_id = $1`.

The balance expression is a `CASE` that reproduces `GetAccounts` (`account.go:35-48`)
exactly, with `t.date <= $2` added to **each** branch:

- `WHEN at.id = 'loan'` → a correlated sum over
  `loan_attachments la JOIN transactions t ON t.id = la.transaction_id`
  where `la.loan_account_id = a.id AND la.user_id = $1 AND t.date <= $2`,
  of `CASE WHEN t.type = 'debit' THEN t.amount ELSE -t.amount END`.
  Debit-positive always: a payment on a loan reduces what is owed. Bounded by the
  **attached transaction's** date, so "how much had I paid by March" is answered
  by principal actually paid to that date.
- `ELSE` → `COALESCE((SELECT SUM(CASE
  WHEN at.positive_txn_type = 'credit' THEN (CASE WHEN t.type = 'credit' THEN t.amount ELSE -t.amount END)
  WHEN at.positive_txn_type = 'debit'  THEN (CASE WHEN t.type = 'debit'  THEN t.amount ELSE -t.amount END)
  ELSE 0 END) FROM transactions t WHERE t.account_id = a.id AND t.user_id = $1 AND t.date <= $2), 0)`

Use a correlated subquery rather than a `GROUP BY` so **every** account returns a
row, including one with no transactions at all — an account holding nothing at
that date is exactly the case Review Focus #2 is about, and a `GROUP BY` would
drop it. The outer `COALESCE(..., 0)` around the whole `CASE` keeps the column
non-null.

Copy the expression verbatim from `GetAccounts` rather than retyping it from this
plan, and add a comment on the function saying that the two must stay identical
and why. Then `ORDER BY a.created_at DESC` to match `/accounts`, so the same
account is not in a different place in the two responses.

Fold rows into `[]models.AccountBalance`, building `CurrencyAmounts` with
`models.NewCurrencyAmounts()` and `.Add(currency, amount)` so a zero adds no key.
Initialise the slice with `make([]models.AccountBalance, 0, ...)` so an
account-free response serialises `[]`, never `null`.

- [ ] **Step 4: Wire it into `GetDashboardSummary`**

In `dashboard.go`:

- Call `parseAsOf` after the `currency` parse (line 93), before the billing-cycle
  `groupBy` branch at line 102 — so `groupBy=billing_cycle` gets the same 400s
  rather than silently ignoring `asOf`.
- Append `addCond("t.date <=", asOf)` to `catFilter` when `asOf != ""`, so the
  count, both category breakdowns, the trend and the recent list all narrow
  together. Without this the response's sections would describe different sets of
  transactions, which is the silent-wrong-number case the handler was reworked to
  remove.
- Inside the read-only transaction (after `q := tx`, line 181), call
  `accountBalancesAsOf` **only** `if asOf != ""`, and in that same branch set
  `summary.AsOf = &asOf` and `summary.Balances = &balances`. Both are pointers
  (Task 3), so leaving them nil is what omits the fields entirely. On query
  error, log with
  `slog.Error("accountBalancesAsOf", slog.String("error", err.Error()))` and
  `RespondError(500)` like every other read in the handler.

- [ ] **Step 5: Run the tests to verify they pass**

Run: `cd backend && go test ./handlers/ -run "TestAccountBalancesAsOf|TestDashboardAsOf" -v`
Expected: PASS, all seven cases.

- [ ] **Step 6: Run the full backend suite**

Run: `cd backend && go test ./...`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add backend/handlers/asof_balance.go backend/handlers/asof_balance_test.go backend/handlers/dashboard.go
git commit -m "feat(dashboard): an as-of report carries each account's balance at that date"
```

---

### Task 5: The integration case

**Files:**
- Modify: `backend/integration_test.go` (add a test; the file is `package main` with build tag `integration`)

**Interfaces:**
- Consumes: the live router from the existing integration harness, the real
  `POST /accounts` + `POST /transactions` write edges, and
  `loan_attachments` created by attaching a transaction to a loan account.
- Produces: no new interface. This task exists because balance arithmetic against
  a real ledger is the part `pgxmock` cannot reach.

- [ ] **Step 1: Write the failing test**

`TestAsOfBalanceEndToEnd`. Following the file's existing helpers: register and
authenticate a user, then create

- a bank account (`positive_txn_type = 'credit'`, currency `INR`) with two
  transactions dated `2026-01-15` (+100.00) and `2026-04-20` (-30.00), and
- a loan account with two attached transactions dated `2026-02-10` and
  `2026-05-01`.

Assert:

1. `GET /dashboard/summary?asOf=2026-03-01` → the bank balance is `100.00` (the
   April transaction is excluded) and the loan balance reflects only the
   February payment. **This is Review Focus #1** — the assertion that fails if
   the loan branch is missing or bounded by the wrong date.
2. The same call without `asOf` → 200, and the response body contains **neither**
   `"asOf"` nor `"balances"`. This is the compatibility guarantee: today's
   responses are byte-identical.
3. `?asOf=2026-01-01` (before every transaction) → 200, `balances` present and
   **non-empty**, every balance zero (Review Focus #2).
4. `?asOf=2026-03-01&dateFrom=2026-04-01` → 400 naming both dates (Review
   Focus #4).

- [ ] **Step 2: Run it to verify it fails**

Run: `make test-integration`
Expected: FAIL — the parameter is either ignored or rejected. (Requires Docker;
if Docker is unavailable, say so and stop rather than skipping it silently.)

- [ ] **Step 3: No implementation step**

This task has nothing to implement. It exists to prove Tasks 1-4 against a real
database. If it fails, the fix belongs to the task that owns the code it
contradicts — go back there, and add the failing assertion to that task's test
file too, so `make test` catches it without Docker.

- [ ] **Step 4: Run it to verify it passes**

Run: `make test-integration`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/integration_test.go
git commit -m "test(integration): an as-of balance is real principal, real dates, real absence"
```

---

### Task 6: The spec, the docs, and the clients

**Files:**
- Modify: `backend/openapi.yaml` (`/dashboard/summary` and `/transactions`: the
  `asOf` parameter; the summary's two response fields)
- Modify: `client/api/types.go` (`DashboardSummary`: the two fields; the
  `TransactionFilter` / endpoint method that carries `asOf`)
- Modify: `client/api/spec_parity_test.go` (a `routeCase` asserting the parameter
  reaches `/transactions?asOf=...`)
- Modify: `AGENTS.md`, `README.md`

**Interfaces:**
- Consumes: the `asOf` parameter as the server accepts it.
- Produces: `api.DashboardSummary.AsOf *string` and
  `api.DashboardSummary.Balances *[]AccountBalance`, plus
  `api.AccountBalance` mirroring the server's. Both are **pointers for the same
  reason the server's are**: `encoding/json` does not omit an empty slice or an
  empty string, so a value type would make "not asked" indistinguishable from
  "asked and empty" on the client too. `AsOf` is a `*string` rather than one of
  the module's `OptionalString` types because it is a response field, not a
  request one, and the optional types are shaped for `omitzero` request bodies.

- [ ] **Step 1: Update `openapi.yaml`**

Add to **both** operations:

```yaml
        - name: asOf
          in: query
          required: false
          schema:
            type: string
            format: date
          description: >-
            Report the ledger's state at the END of this day: a transaction dated
            on or before it counts, whatever the window says. Clamped to the
            earlier of asOf and dateTo; a dateFrom after the clamped value is a
            400. Filters by date only, so a transaction dated before asOf counts
            even if the statement carrying it was imported later. Invalid dates,
            and dates outside the ledger's supported window, are a 400.
```

On the summary response, add `asOf` (string, date) and `balances` (array of
`AccountBalance`, with `id`/`name`/`currency`/`balance`, where `balance` is an
additionalProperties numeric map keyed by currency code) and the
`AccountBalance` schema. Both must be documented as present only when `asOf` was
supplied.

- [ ] **Step 2: Update the shared client**

In `client/api/types.go`, add `AccountBalance` mirroring the server, and the two
`omitempty` fields on `DashboardSummary`. Add `AsOf string` to
`TransactionFilter` and `setQuery("asOf", f.AsOf)` in its `apply` method
(`client/api/endpoints_transactions.go:87-109`), so the shared client can reach
the new parameter and `TransactionFilter` stays the one filter the list, the
export and the MCP tool all use.

- [ ] **Step 3: Extend the parity test**

In `client/api/spec_parity_test.go`, the existing `TestEveryRouteHitsItsDocumentedPath`
executes every `routeCase` against a stub asserting method, path and query. Add
`asOf` to the `/transactions` case's expected query set, so the stub fails if the
parameter is not actually sent. `TestRouteTableMatchesTheSpec` needs no change —
no route was added.

- [ ] **Step 4: Run the spec and client gates**

Run: `make openapi-check test-client test-client-cover-check`
Expected: PASS. Client coverage must stay above its 85% floor; the new
`AccountBalance` type is a declaration, so it should not move the number.

- [ ] **Step 5: Update the docs**

In `AGENTS.md`, extend the money/reporting bullet to say that `asOf` is a
parameter rather than a `q` term, why a balance is computed with no lower date
bound while the windowed figures are not, and that the balance reproduces
`GetAccounts`' loan branch so the two cannot disagree. In `README.md`, add `asOf`
to the reporting section's parameter list.

- [ ] **Step 6: Commit**

```bash
git add backend/openapi.yaml client/api AGENTS.md README.md
git commit -m "docs(api): asOf on the summary and the list, and the clients that reach it"
```

---

### Task 7: Close the loop on the issue

**Files:**
- Modify: none. This task files the gap the spec recorded.

- [ ] **Step 1: File the resolver drift gap**

The spec's "Known limitation" section records that `api.ResolveQuery` and
`frontend/src/lib/query/resolve.ts` are hand-mirrored with no shared corpus
joining them, so the drift guard covering the parsers does not cover the
resolvers. `asOf` deliberately adds nothing to that surface (it is a parameter,
not a `q` term), so the gap is untouched and worth its own issue.

Create an issue, modelled on closed #46, describing: the two resolvers are
mirrored by hand; `client/api/query_resolve_test.go` and
`frontend/src/lib/query/resolve.test.ts` cover the same cases but nothing joins
them mechanically; a rule changed on one side fails nothing; the fix is a
`resolve` corpus alongside the existing `testdata/corpus.json` that both sides
read.

- [ ] **Step 2: Comment on #37 with what shipped and what did not**

State that slice 1 covers `asOf` on `/dashboard/summary` and `/transactions`,
list the semantics decided (clamp, end-of-day, date-not-created-at, the loan
branch, the omitempty response fields), and restate what is deliberately
deferred: the `q=` grammar term, `/dashboard/money-flow`,
`/dashboard/money-flow/timeline`, `/dashboard/cash-flow-calendar`, and per-field
history. Link the spec and the plan. Do **not** close #37 — the remaining
endpoints are still open work.

- [ ] **Step 3: Commit**

```bash
git add -A
git commit -m "docs: file the resolver drift gap #37's slice 1 left visible"
```
