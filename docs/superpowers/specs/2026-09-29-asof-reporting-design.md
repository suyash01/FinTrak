# asOf reporting, slice 1 — `asOf` on `/dashboard/summary` and `/transactions`

Implements the first slice of #37.

## Problem

Every aggregate answers *"over this window"*. There is no way to ask what the
ledger looked like at a past moment, and no history table, so a past state cannot
be recovered after the fact.

- *"What was my net worth the day I switched banks?"*
- *"How much had I actually paid on this loan by March?"*

The loan schedule can tell you what was *owed* each period, but not what you
*paid*. The dashboard can tell you a window, but not a past instant.

## Proposal

`asOf=YYYY-MM-DD` reconstructs the state of the world at the **end of that day**:
income, expense, net, per-category totals, per-account balance, and a filtered
list.

The load-bearing constraint is **balance at a date**, not filtering by date —
filtering already works. A running balance at a past instant has to respect the
same sign conventions (`account_types.positiveTxnType`), the same link rollup and
the same `billing_cycle_id` attachment rules that apply today. That is the real
work.

## Why derived rather than snapshotted

Because nothing is stored, an `asOf` report cannot disagree with the present. A
snapshot or audit-log design can. The repository's instinct is derived-not-stored
throughout — the loan table is generated, the Sankey is computed, billing cycles
are materialized on read — and this is the same instinct applied to time.

## Scope of this slice

**In:** `asOf` on `GET /dashboard/summary` and `GET /transactions` — income,
expense, net, per-category totals, per-account balance at a date, and a list
filtered to that date.

**Deliberately out, stated so it is a decision and not a gap:**

- the `q=` grammar term (`asof:2026-03-01`). The parameter is the honest first
  surface: it rides the filter path every reporting query already shares, so the
  two endpoints in this slice cannot drift. A grammar term would make the query
  compiler carry a stateful second axis, which is the riskiest version to start
  on and buys nothing until more endpoints accept `q`.
- `/dashboard/money-flow`, `/dashboard/money-flow/timeline`,
  `/dashboard/cash-flow-calendar`. The money-flow stages are denominated by the
  currency the *link's* amount is in (`linkCurrencyColumn`), so how an `asOf` date
  interacts with a link's own date is a separate decision, not a copy of this one.
- schema history, deleted rows, per-transaction edit history. `asOf` reconstructs
  from the **current** ledger. Per-field history is a different feature and is not
  smuggled in here.

---

## Design

### 1. `asOf` is a query parameter, not a `q=` term

It rides the same path as `dateFrom`/`dateTo`: parsed with `parseQueryDate`,
validated, and appended to the filter fragment every query in the handler already
shares. In `dashboard.go` that fragment (`catFilter`) is consumed by the count,
the income/expense totals, both category breakdowns, the monthly trend and the
transaction list, so one addition reaches all of them and none of them can
describe a different set of transactions than its neighbour.

### 2. `asOf` is the window's upper bound

`asOf=2026-03-01` means "the state at the **end of** 2026-03-01". A transaction
dated on or before it counts, whatever the window says.

- If `dateTo` is also given, `asOf` is clamped to the **earlier** of the two. The
  window never widens past the instant asked for.
- A `dateFrom` strictly after the clamped `asOf` is a **400** naming both, not an
  empty result. An empty ledger reads as "you had nothing then", which is a
  different and wrong claim.
- `asOf` alone is legal: the implicit window is unbounded below, which is what
  makes a balance meaningful.

The alternative — `asOf` silently replacing `dateTo` — was rejected: it makes two
parameters fight over one meaning, and a caller who set a window would not expect
it overridden.

### 3. Balance at a date is a separate query, deliberately

A window bounds **activity**; a balance is **state**. A balance that depended on
`dateFrom` would be a different number for the same account on the same day
depending on which window was open, which is exactly the silent-wrong-number class
the reporting endpoints were reworked to eliminate.

So the balance query sums over every transaction dated on or before `asOf`, with
**no lower bound at all**, signed exactly as `GetAccounts` signs a balance
(`account.go:35-48`): `at.positive_txn_type = 'credit'` gives a credit `+amount`
and a debit `-amount`, `'debit'` inverts it, and the expression is reached
through the account's own type rather than re-derived.

**The loan branch is part of that expression and is not optional.** `GetAccounts`
special-cases `at.id = 'loan'`, which does *not* sum the loan account's own
transactions: it sums the transactions **attached to the loan** through
`loan_attachments`, and always as a *debit*-positive sum — money paid on a loan
reduces what is owed, so a loan's "balance" is outstanding principal, not a
bank-style net. An `asOf` balance that ignored this branch would report a car
loan's balance as its own ledger total (almost always zero, since a loan's
transactions live in the bank account) rather than what was still owed, which is
the single most misleading number this feature could produce.

So the balance query reproduces both branches with `t.date <= asOf` added to each,
and the loan branch is bounded by the **attached transaction's** date — the date
the payment happened, not the attachment's — so "how much had I paid on this loan
by March" answers with principal actually paid to that date.

It is issued **only when `asOf` is present**. An ordinary summary issues no
balance query at all — pinned by a test, because otherwise this quietly adds a
query to every dashboard load.

### 4. The response shape

`asOf` is a **request** parameter. The response does **not** gain a field of the
same name that is always present, because `models.LoanPayoff.AsOf` already
serializes as `asOf` and two meanings in one payload is a trap.

Instead:

- `balances` — `[]AccountBalance`, present only when `asOf` was supplied
- `asOf` — the echoed `YYYY-MM-DD`, present only when `asOf` was supplied

A caller that did not ask gets neither field, so **today's responses are
byte-identical** and no existing client breaks. Both are `omitempty`, so this is
an additive change to the spec.

`AccountBalance` is:

```go
type AccountBalance struct {
    ID       uuid.UUID       `json:"id"`
    Name     string          `json:"name"`
    Currency string          `json:"currency"`
    Balance  CurrencyAmounts `json:"balance"`
}
```

`Balance` is a `CurrencyAmounts`, **never a scalar**. An account carries a
currency and the response can span several, so a map is the only honest
representation — and `CurrencyScope` already names the accounts behind each
currency, so a balance in a currency the caller is not displaying is still
explained rather than dropped.

A zero balance adds **no key**, per the standing rule, so `len()` counts the
accounts that actually hold money at that date.

### 5. The currency rules still bind

Nothing here collapses a `CurrencyAmounts`. The balance query groups by
`accountCurrency` — the shared `internal/currency` expression, the one place it is
written — so an account with an unset currency is INR here exactly as it is
everywhere else. A zero contributes no key, and the sign is whatever the data
says; no surface picks one.

### 6. The `q=` diagnostics are unaffected

`asOf` is not a `q` term, so a term the compiler drops is still reported through
the existing `queryDiagnostics`. The two mechanisms do not interact, and this
slice does not change how either is worded.

### 7. Late-imported transactions count

A transaction dated 2026-01-15 counts toward an `asOf` of 2026-03-31 even if the
statement carrying it was imported in April. `asOf` filters by **date**, not by
when the row was created. There is no `created_at` clause and no history to
consult, so this falls out of the derived-not-stored design rather than being
chosen against it — and it is the only answer consistent with an `asOf` report
being unable to disagree with the present ledger.

This is stated in the API description, because it is a decision a user would
otherwise discover rather than read.

### 8. Validation and errors

| Condition | Response |
|---|---|
| `asOf` not `YYYY-MM-DD` | 400, `asOf must be YYYY-MM-DD` (same helper as `dateTo`) |
| `asOf` outside `[1900-01-01, today+1y]` | 400, through `validation.CheckTransactionDate` — the same window every write edge applies |
| `dateFrom` strictly after the clamped `asOf` | 400, naming both dates |
| `asOf` before the earliest transaction | 200. Balances are zero, `balances` still lists the accounts, no key is invented |

The last row is the important one: **an empty `balances` array and a list of
zero balances are different answers**, and only the second is true.

---

## Components

- **`backend/handlers/asof.go`** (new) — `parseAsOf(c, dateFrom, dateTo)` and the
  `t.date <= $n` fragment. One place decides what `asOf` means, so the two
  endpoints cannot drift on the clamp rule.
- **`backend/handlers/dashboard.go`** — resolve `asOf` after `dateTo`, append to
  the shared fragment, and run the balance query only when present.
- **`backend/handlers/transaction.go`** — the list honours it identically, through
  the same helper.
- **`backend/models/models.go`** — `AccountBalance`, and the two `omitempty`
  fields on the summary response.
- **`backend/openapi.yaml`** — the parameter on both operations, and the two
  response fields on the summary.

## Known limitation of the #39 work this builds on

`api.ResolveQuery` (landed in 5e46daa, closing #39) is a hand-mirrored Go
counterpart of `frontend/src/lib/query/resolve.ts`. Unlike the `parse`/`fields`
pair, **these two are not joined by a shared corpus**, so the drift guard that
covers the parsers does not cover the resolvers: a rule changed on one side would
fail nothing.

That is a real gap and it is recorded here rather than fixed inside this slice,
because #46 is the issue that describes the class of bug and the honest fix is a
corpus for the resolvers too, which is its own change. Until then the mitigation
is that both sides are pinned to the same case list — the Go tests in
`client/api/query_resolve_test.go` and the TypeScript tests in
`frontend/src/lib/query/resolve.test.ts` cover the same grammar — so a change to
one that is not made to the other shows up as a *failing test on the other side's
existing case*, which is weaker than a corpus but is not nothing.

`asOf` deliberately does not add to this surface: it is a parameter, not a `q`
term, so neither resolver is involved.

The synthetic summary rows in the transaction list (month-end running balance,
cycle "Total outstanding") need no change. They are already derived from the same
ledger and are already window-independent — `computeMonthEndBalanceRows`
(`transaction_summary.go:64`) sums per-month activity over the account's **full
ledger** and accumulates, so the balance at a month end is a state, not a window.
`asOf` therefore does not alter them.

One asymmetry is worth stating rather than leaving to be discovered: those rows use
the plain credit/debit sign and do **not** special-case a loan account the way
`GetAccounts` does. That is pre-existing, and this slice does not change it. The
`asOf` balance in the summary follows `GetAccounts`, because that is the number
`/accounts` shows and a report that disagreed with the accounts screen about the
same account on the same day would be the worse defect.

## Testing

- `pgxmock` handler tests for each error row in §8, and for the clamp.
- A test that an ordinary summary issues **no** balance query — the one that stops
  this taxing every dashboard load.
- A test that a zero balance adds no key, and that a mixed-currency response
  carries one key per currency with no total.
- A test for **each** balance branch: a `credit`-positive account, a `debit`-positive
  account, and a **loan**, because the loan branch is a different query and is the
  one that silently produces a wrong answer rather than an obviously wrong one.
- The **integration** suite gets an `asOf` case end to end: a loan with payments
  before and after the `asOf` date, asserting the outstanding principal falls
  between them, plus a bank account asserting the running balance. Balance
  arithmetic against a real ledger — real sign conventions, a real link rollup —
  is the part mocks cannot reach.

## Performance

One extra query, only when `asOf` is present, over `transactions` bounded by
`date <= asOf` and grouped by account. The list and count are unchanged. This is
the same shape as the existing per-account aggregate queries and needs no new
index; `transactions_list_index` and the tenant/account indexes cover it.
