# Multi-currency aggregates — design

**Date:** 2026-09-27
**Issue:** #35 (also closes #40, #41, #42)
**Status:** approved, ready to plan

---

## 1. The problem

`accounts.currency` is stored, validated to 3 characters, returned by the API and
carried in the backup bundle — and never read by anything that computes a number.
`transactions` has no currency column, so every aggregate sums `amount` blind.

With no `accountId` filter set, a USD account's dollars are added to an INR
account's rupees. Every headline number on the dashboard, every Sankey edge and
every calendar cell is affected. It survives because the default account filter
means a single-account user never sees it: it is invisible exactly when it is
harmless, and wrong exactly when it matters. Nothing errors — the number is just
wrong.

The sites, all with an **optional** account filter:

| Site | File:lines |
| --- | --- |
| Dashboard summary, headline totals | `backend/handlers/dashboard.go:107-111` |
| Dashboard summary, category breakdowns | `dashboard.go:119-126`, `146-153` |
| Dashboard summary, monthly trend | `dashboard.go:173-179` |
| Money-flow Sankey, four aggregations | `money_flow.go:198-204`, `235-241`, `273-280`, `313-322` |
| Money-flow graph assembly (Go-side sums) | `money_flow.go:693-850`, `454-578` |
| Money-flow timeline, monthly | `money_flow_timeline.go:76-84` |
| Cash-flow calendar, daily + Go-side window totals | `cash_flow_calendar.go:175-184`, `207-215` |
| Link-cycle report (spans two accounts by construction) | `link_cycles.go:85-122`, `129+` |

`money_flow.go:280` is structurally the worst: `GROUP BY` omits the account
entirely, so different-currency debits are already merged into one row.

## 2. What this is not

**No exchange rates, no conversion, no base currency.** That is a separate
feature: dated, user-entered rates, conversion at the aggregate boundary only,
never inside a stored `amount`. This design deliberately refuses to produce a
number it cannot compute, and says what it left out.

The rule it holds to is one `frontend/src/lib/bankfiles/` already holds to —
*a value the reader cannot read exactly is dropped and counted, never rounded* —
and the parser's `no_transactions` vs `unsupported_format` distinction. **A
refused number is better than a wrong one.**

## 3. Two rules that make the change coherent

**Rule 1 — subtraction inside a currency is meaningful; addition across currencies
is not.** So a `net` is *also* a per-currency map
(`net[c] = income[c] - expense[c]`), and a figure that can only exist by adding
across currencies — the calendar's `maxAbsNet` scale denominator, a link-cycle
pair total — is a map with no scalar escape hatch. There is no field anywhere
whose value is "the sum of the map".

**Rule 2 — the client is told, not left to guess.** Every response carries
`currencyScope`: the accounts in scope, each with its currency and its own
income/expense. A consumer can therefore say *"USD 120.00 sits in 1 account you
are not looking at"* instead of silently dropping it.

## 4. The core query

One helper, shared by all five aggregate endpoints, replaces the blind scalar
total **and** produces the scope block:

```sql
SELECT a.id, a.name, a.currency,
       COALESCE(SUM(t.amount) FILTER (WHERE t.type = 'credit'), 0),
       COALESCE(SUM(t.amount) FILTER (WHERE t.type = 'debit'), 0)
FROM accounts a
LEFT JOIN transactions t ON t.account_id = a.id AND t.user_id = $1 <date conds>
WHERE a.user_id = $1 <account conds> <currency cond>
GROUP BY a.id, a.name, a.currency
ORDER BY a.name, a.id
```

Its rows are simultaneously the per-currency headline totals and the accounts
behind `currencyScope`, so `dashboard.go:107`'s `scalarQuery` disappears into it.
Every *other* aggregate keeps its own query but selects `a.currency` and folds
rows into a map in Go.

This is the first read of `accounts.currency` by any aggregate in the codebase.

## 5. Wire shape

A single new type replaces every amount on these five endpoints:

```jsonc
"totalIncome":   { "INR": "50000.00" },
"totalExpense":  { "INR": "30000.50" },
"totalNet":      { "INR": "19999.50" },
"byCategory":    [{ "categoryName": "Rent", "count": 1,
                    "total": { "INR": "15000.00" } }],
"currencyScope": {
  "currencies": ["INR", "USD"],
  "accounts": [{ "id": "…", "name": "…", "currency": "USD",
                 "income": "120.00", "expense": "80.00" }]
}
```

**Always a map, in every response, with no deprecation window.** A
single-currency response is the same shape with one key. There is exactly one
thing to parse, render and test in each of the four consumers. A union — a
number today, a map when mixed — would put a branch in ~40 places and be wrong
for exactly the users the bug hits.

The old scalar fields are **not** kept for a release. They are the wrong
numbers; keeping them keeps the bug. This is a breaking `/api/v1` response
change and needs a release note.

**`totalNet` is new and server-computed.** `Dashboard.tsx:336` and
`MoneyFlow.tsx:457` currently compute `totalIncome - totalExpense` themselves.
That is the same blind arithmetic in a second place, and it is the reason Rule 1
exists. Removing it removes a class of bug rather than relocating it.

No `combined` boolean: `len(currencies) == 1` is the answer, and the repository
does not keep derivable state.

### 5.1 Go types (`backend/models`)

```go
// CurrencyAmounts is one aggregate's value, keyed by the currency code of each
// account that contributed. One key means the value is exact for the whole
// scope; more than one means no single number can represent it, and every
// consumer must say so rather than add them.
type CurrencyAmounts map[string]money.Amount
```

with `CurrencyScope` and `ScopedAccount`. Every amount field on
`DashboardSummary`, `CategorySpend`, `MonthlyData`, `BillingCycleTrendItem`,
`MoneyFlowNode`, `MoneyFlowEdge`, `MoneyFlowLinkSummary`, `MoneyFlowGraph`,
`MoneyFlowTimelinePeriod`, `CashFlowCalendar` and its day/marker/cycle types, and
the link-cycle report, changes type — about 40 fields.

Two edge cases the map has to answer, stated here so no consumer guesses:

- An aggregate with no matching transactions returns `{}`. A **missing key reads
  as zero**, never as an error and never as absent data. This is what
  `CashFlowCalendar.tsx:132-136`'s `?? 0` zero-fill becomes.
- `Sub` treats a key missing from either operand as zero and returns the union of
  the key sets, so `{"INR":5000}` minus `{"INR":2000,"USD":100}` is
  `{"INR":3000,"USD":-100}`. A currency that appears in only one operand still
  yields a well-defined difference, and the result never invents a key.

`/links/cycles` is in scope because it sums two accounts' transactions by
construction, not by accident.

### 5.2 `currency` query parameter

A `currency` parameter beside the existing `accountId` on all five endpoints,
validated the same way (a 3-letter ASCII code, upper-cased, matching the column's
`VARCHAR(3)`), and carried by `WindowFilter` in the shared client and by the
three filters derived from it.

## 6. Backend

- `backend/handlers/`: a `currencyScope` helper, `a.currency` added to the
  select and group-by of every aggregate query, and Go-side folding into maps.
- The **top-15 category limit becomes per-currency**, so it becomes
  `ROW_NUMBER() OVER (PARTITION BY a.currency ORDER BY SUM(...) DESC) <= 15`
  alongside the existing `HAVING`.
  **Risk to settle first, in TDD:** a window function at the same level as the
  aggregate, combined with `HAVING` over a `LEFT JOIN`, is the one non-obvious
  piece of SQL here. If Postgres rejects that combination, the fallback is to
  drop the SQL limit and take the top 15 for the selected currency in the UI —
  category counts are small, and the limit's meaning ("top 15 by spend") is
  inherently currency-relative.
- `queryCategoryPayeeFlows` (`money_flow.go:263-297`) gains the account's
  currency to its `GROUP BY`; today it merges different-currency debits into one
  row before any map exists.
- Go-side sums move in lockstep: `buildMoneyFlowGraph` (`money_flow.go:693-850`),
  `analyzeAccountFlows` (`454-578`), the calendar's window totals
  (`cash_flow_calendar.go:207-215`), `buildLinkCycleReport` (`link_cycles.go:129+`).
- `readonly.SideEffectingGETs` is **unchanged** — no route, verb or write changes.
- No new table, so no `BackupBundle` work.

## 7. Shared Go client (`client/api`)

```go
type CurrencyAmounts map[string]Amount

func (m CurrencyAmounts) Single() (code string, value Amount, ok bool)
func (m CurrencyAmounts) Currencies() []string   // sorted, for a stable UI order
func (m CurrencyAmounts) Sub(other CurrencyAmounts) CurrencyAmounts
func (m CurrencyAmounts) Display() string
```

`Display()` must **not** silently pick a currency from a multi-currency map: it
renders an explicitly-multi form (`2 currencies: INR 5000.00, USD 120.00`) when
`Single()` is not `ok`. The TUI's dashboard already refuses to compute a net it
was not given and says so; this extends that rule rather than inventing one.

`Sub` is the per-currency difference, and exists so no client ever re-does
`income - expense` across a map by hand. `client/api/spec_parity_test.go` stub
bodies stay decodable.

## 8. MCP server

No result structs of its own — each tool returns the `client/api` struct
verbatim, so the shape propagates into model context with zero translation. What
changes is the prose:

- Tool descriptions drop "the total" framing and say amounts arrive per currency.
- `mcpserver.go`'s instructions currently promise *"every figure is already
  computed by the server"*, which becomes false for a cross-currency total.
- The five tools gain a `currency` argument, with matching `argumentCases` in
  `tools_test.go` — the audit asserts the *arguments* each tool sends, not just
  its route.
- `tools_transactions.go:76` and `tools_links.go:73` cross-reference
  `get_dashboard_summary` in prose and are swept for the same claim.

## 9. TUI

The three screens (dashboard, money flow, calendar) each already have a filter
overlay, so the currency joins the account filter in that existing form and is
sent as the `currency` parameter. Where the filter names no currency and the
user's accounts differ, the response is multi-currency and the TUI **says so
rather than choosing** — the same rule the dashboard's card line already follows
with *"net not computed — income and expense are shown exactly as the API
returned them"*. The TUI never does arithmetic on money, so it has nothing to
unlearn here; `Display()` refusing to pick a currency is what keeps that true.

## 10. Frontend

- `types.ts`: the same ~40 fields become `Record<string, number>`.
- `formatCurrency(v, code)` **already** takes a currency code
  (`frontend/src/utils/formatters.ts:2`), so the code is threaded through rather
  than a formatter being written.
- `useCurrencyScope(scope)` in `src/lib/` — one hook returning the selectable
  codes and the current choice, and a `<MultiCurrencyNotice>` naming what is not
  being shown. A `Select` sits beside the existing account filter.
- `Dashboard.tsx:552`'s hardcoded `₹` Y-axis tick formatter becomes
  currency-aware. It is a hardcoded rupee in a multi-currency change, so it goes.
- The Sankey and the heatmap scale by the **selected** currency's figure, never a
  global one: `MoneyFlow.tsx:764-767` and `1014-1019`, and
  `CashFlowCalendar.tsx:188`.
- Every `net >= 0` sign test becomes a per-currency test.
- `offlineCache` already allowlists `/dashboard/summary` and
  `/dashboard/cash-flow-calendar`; the scope block adds a little payload against
  the existing 40-entry / 200k-char quotas.

### 10.1 The `ccy:` query field

`ccy:` scopes the **Transactions** filter to a currency, so the same word the
dashboard selector shows works in the ledger. Three files, as the issue notes:
`backend/internal/query/fields.go`, `frontend/src/lib/query/fields.ts`, and
`backend/internal/query/testdata/corpus.json` (which both suites read).

- New `kindCurrency` / `"currency"` in both field tables, with `opsEq` only. It is
  **not** `kindEnum` with a static list: the domain is the user's own accounts,
  so a fixed enum would reject a currency the user legitimately holds.
- Three ASCII letters, upper-cased by the compiler, matching the column's
  `VARCHAR(3)` — so `ccy:usd` and `ccy:USD` are the same term.
- The compiler emits a **correlated `EXISTS`**, not a join:
  `EXISTS (SELECT 1 FROM accounts ac WHERE ac.id = t.account_id AND ac.currency = $n)`.
  This is forced by `compile.go:28-32`: the list query and its `COUNT(*)` share
  one predicate, and a fragment naming a joined table would make the count fail
  while the page rendered fine. `compile_safety_test.go` enforces it.
- One corpus case pins the surface form, the canonical upper-cased form, the
  parsed term, the diagnostic-free parse and the exact SQL clause and argument.

## 11. Documentation (#40, #41, #42)

- **#40** — inline the rationale and drop the dangling references. `README.md`
  and `AGENTS.md` stop citing `#56` and `#59`; `mcpserver.go` states the two gaps
  as properties of the server: *no scoped read-only token endpoint exists, so the
  server signs in with full account credentials* and *the propose-and-apply half
  is not implemented*. Self-contained, and a reader can check the claim. The two
  gaps are filed as their own issues.
- **#41** — correct `AGENTS.md:71`, which credits the TUI with route-parity tests
  that do not exist. The guarantee is real but comes from
  `client/api/spec_parity_test.go`, inherited transitively by depending on
  `client/api`. The corrected sentence names that file and explains the
  inheritance.
- **#42** — the README's project tree lists an `IDEAS.md` that does not exist.
  `docs/feature-proposals.md` says of itself: *"proposals only. Nothing here is
  designed, scheduled or approved."* So it is committed where it is, as the
  review candidate it says it is, and the README line is **deleted** rather than
  pointing at a relabelled backlog. Adopting it as `IDEAS.md` is only right if
  the proposals are first accepted, which is a reviewer's decision, not this
  change's.
- `README.md`'s API overview entries for the five endpoints and its MCP and TUI
  sections are updated for the new shape and the new parameter.

## 12. Testing

TDD throughout, against the existing floors.

- **New**: a `currencyScope` helper test; a `CurrencyAmounts` unit test for
  `Single`/`Sub`/`Display`/`Currencies`, pinning that `Display` refuses to choose;
  folding tests for each aggregate; a `ccy:` corpus case.
- **Updated**: the ~40 exact-SQL expectations and `money.FromFloat` assertions
  across `dashboard_test.go`, `money_flow_test.go`, `money_flow_timeline_test.go`,
  `cash_flow_calendar_test.go`, `dashboard_errors_test.go`; the raw-JSON
  `"byCategory":[]` / `"markers":[]` assertions; the three frontend fixtures
  (`Dashboard.test.tsx`, `MoneyFlow.test.tsx`, `CashFlowCalendar.test.tsx`); the
  `client/api` stub bodies; the MCP `argumentCases`.
- **Integration** (`make test-integration`, Docker): a new mixed-currency case
  asserting that a window over two accounts in two currencies returns per-currency
  maps, that `currencyScope` names both accounts, and that **no scalar total
  survives** anywhere in the response.
- `backend/integration_test.go`'s existing `TestIntegrationMoneyFlowGraph`,
  `TestIntegrationCashFlowCalendar` and `TestIntegrationMoneyFlowAccountEdgesAndTimeline`
  move to the map form.
- `make openapi-check`, `make docs-check`, `make vet` across all four modules,
  and every coverage floor: 85% backend, 85% client, 18% TUI, 80% MCP, 85%
  frontend.
- **Release note** in the PR body: a breaking response change on `/api/v1`, and
  anyone currently reading a mixed-currency total will see it change.

## 13. Alternatives rejected

- **Store a currency per transaction.** Bigger change, and it does not fix the
  case where every account is domestic but the user also holds a foreign balance.
- **Convert silently with a fetched rate.** A network dependency, a new failure
  mode, and it hides the gap rather than naming it.
- **Document that totals assume one currency.** Leaves a silently wrong headline
  number in place.
- **Keep the number when the scope is single-currency** (a union response).
  Cheaper to migrate, but one branch in ~40 places and wrong for exactly the
  users the bug hits.
- **Render every currency side by side in the UI.** Multiplies the chart code, and
  stops answering "which currency is this dashboard about" at a glance.
- **Pick an implicit base currency.** Makes a choice the user never made and hides
  the foreign balances behind a warning rather than a control.
- **A TUI route-parity test as a second layer** (raised in #41). A genuinely good
  idea and the right place to catch a screen silently dropping a capability — but
  a new test, not a doc fix, so it is filed separately.
