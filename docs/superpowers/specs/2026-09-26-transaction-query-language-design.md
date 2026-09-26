# Transaction Query Language — Design

**Date:** 2026-09-26
**Status:** Design approved in conversation; awaiting written review
**Surfaces:** `GET /transactions` (v1). `GET /transactions/export` inherits it for free.

## Purpose

Let a power user type one expression into the Transactions search box and get the
rows they meant, instead of clicking through a multi-select filter bar. The
expression is evaluated **server-side and pushed into SQL**, so counts, pagination
and CSV export all agree with what is on screen.

The other three read surfaces (Dashboard, Money Flow, Cash Flow Calendar) keep
their existing `dateFrom`/`dateTo`/`accountId` filters. The grammar is
deliberately shaped so they *could* adopt it later (Section 11).

## 1. Scope

**In:**

- A `q=` query parameter on `GET /transactions`, AND-ed with the 20 existing
  parameters.
- A lenient tokenizer + parser in a new `backend/internal/query` package.
- A compiler that emits into the existing `txnFilter` builder.
- A replacement input with context-aware autocomplete on the Transactions page.
- Per-term diagnostics returned in the response body.
- Parity work in `openapi.yaml`, `client/api`, and the MCP server.

**Out (v1):**

- `or`, parentheses, or any boolean tree. All terms are AND-ed.
- Sorting in the language. `sortBy`/`sortOrder` stay whitelisted parameters.
- The Dashboard, Money Flow and Cash Flow Calendar surfaces.
- Name resolution on the server. The server resolves nothing (Section 4).
- Any 4xx response caused by `q`. `q` can never fail a request (Section 5).

## 2. Grammar

```
query      := term*
term       := [ "not" ] ( field ":" value | field op value | plain )
plain      := word ( word )*          -- one or more bare words = free-text search
field      := desc | note | cat | group | acct | payee | tag
             | type | amt | date | linked | recurring
op         := "=" | "!=" | ">" | ">=" | "<" | "<=" | "~"
value      := quoted | bare
bare       := csv-item ( "," csv-item )*
quoted     := '"' ( any-char | '\"' | '\\' )* '"'
word       := any run of non-space, non-delimiter characters
```

`plain` is the common case and is deliberately part of the grammar rather than a
fallback bolted on afterwards: consecutive bare words accumulate into a single
free-text term, so `coffee shop` is one search for two words, while
`desc:coffee shop` is `desc:coffee` **plus** a plain search for `shop` (the
`field:` form consumes exactly one value, quoted or not).

**Everything is AND-ed.** `not` negates a single term. There is no `or` and no
grouping in v1, which means the parser is flat and needs no precedence handling.

**OR lives inside the value, as CSV.** `cat:8a3f,2b1c,9d4e` means "any of these
three" — identical to today's `categoryId=8a3f,2b1c,9d4e`. This is deliberate:
the existing filter semantics are already OR-within-a-dimension /
AND-across-dimensions, so v1 needs no boolean composition machinery at all, and
the two sentinels the API already has (`uncategorized` for a null category,
`none` for a null payee) keep working unchanged. `not` binds to the whole term,
so `not cat:a,b` is "neither a nor b", not "not a, and not b" spelled twice.

**A sentinel is per-field, and only where the column is nullable.** `cat` and
`payee` may take `none` / `uncategorized`, because the compiler turns one into
`IS NULL`. `acct` and `group` may not: their columns are not nullable, so a
sentinel there would bind the literal string against a uuid column and the
database would answer with a type error — a 500 from a parameter that is supposed
to be incapable of failing. Such a term is refused with a diagnostic that names
what the field does accept, and the grammar sheet does not advertise it.

**`not` on a multi-word plain term negates the whole term.** `not coffee shop` is
"not (coffee AND shop)", so the `NOT` wraps the conjunction once. Wrapping each
word would be "neither coffee nor shop", which is a much broader request than
the one made. This was a real bug: the plain-search branch computed the negation
and then ignored it, so `not coffee` returned exactly the coffee rows.

**A token that is not `field:value` shaped is plain search.** It is compiled to
the existing 4-way OR over description, notes, payee name and tags. So `coffee`
works, and a typo in a *field name* is loud while a typo in a *value* is quiet.

**`q` AND-s with `search`.** The frontend never sends both, but a curl caller
may, and the two must compose rather than one silently winning.

**Unquoted values stop at whitespace.** `desc:coffee shop` is
`desc:coffee` plus a plain search for `shop`. The autocomplete always inserts a
quoted value when the value contains whitespace, so this is only reachable by
hand-typing.

**Caps.** `q` is capped at 2000 characters and 32 terms. Terms past either cap
are dropped with code `too_long`. The cap is counted in **characters (runes)**
and the cut lands on a character boundary; slicing the raw string at a byte
offset split multi-byte characters, and the invalid UTF-8 went into a bound
argument and came back from the database as an encoding error. The cap matters
because the compiler emits one SQL fragment and one bound argument per term, so
it bounds the statement a single request can produce.

### Fields

| Field | Value | Compiles to |
|---|---|---|
| `desc` | text | `LOWER(t.description) LIKE LOWER($n)` |
| `note` | text | `LOWER(COALESCE(t.notes,'')) LIKE LOWER($n)` |
| `cat` | category id, or `none` | `t.category_id = $n` / `t.category_id IS NULL` |
| `group` | group id or slug | `EXISTS (SELECT 1 FROM categories cat WHERE cat.id = t.category_id AND cat.group_id = $n)` |
| `acct` | account id | `t.account_id = $n` |
| `payee` | payee id, or `none` | `t.payee_id = $n` / `t.payee_id IS NULL` |
| `tag` | tag **name** | `t.tags && $n::text[]` |
| `type` | `debit` \| `credit` | `t.type = $n` |
| `amt` | decimal major units | `t.amount <op> $n` (stored minor units) |
| `date` | `YYYY-MM-DD` | `t.date <op> $n` |
| `linked` | `true` \| `false` | `EXISTS`/`NOT EXISTS` over `links` |
| `recurring` | `linked` \| `unlinked` | `EXISTS`/`NOT EXISTS` over `recurring_attachments` |

Every fragment is the same shape the corresponding existing parameter already
emits, so the compiler is a re-targeting of `txnQueryFilter`, not new SQL.

**`tag` takes a name, not an id**, because there is no tag table — tags are free
text in `transactions.tags`. This is the same limitation the current CSV `tags`
parameter has, documented at `frontend/src/components/Transactions/transactionConstants.ts:37`.

**`amt` is a decimal in major units, in the query and on the wire.** `amt>50` is
fifty dollars and is sent as `amt>=50`. The single conversion to the column's
integer minor units happens on the server, with `money.Parse`
(`backend/internal/money`), whose grammar `client/api.ParseAmount` already
mirrors. `.`, `-`, `.5`, `5.` and `--1` are errors, not zeroes.

> **Corrected after review.** This section originally said the wire carried minor
> units and that the client converted. Both were wrong in the same direction: the
> client converted *and* the server converted, so `amt>50` filtered above $5,000.
> The wire carries major units, like the JSON boundary everywhere else in this
> API, and only the server converts. A client that resolves a name must NOT also
> normalise a unit — one conversion, in one place.

**`acct` does not cover loan accounts.** The existing API splits them into
`accountId` and `loanAccountId` because a loan account matches through
`loan_attachments`, not `t.account_id`. In v1 `q=acct:<id>` uses the plain
`account_id` predicate; loan attachment filtering stays on the `loanAccountId`
parameter. Selecting a loan account in the filter bar still works and AND-s with
`q`.

**Named date periods are a frontend concept.** `date:this-month` is suggested by
the autocomplete and resolved by the frontend into
`date>=2026-09-01 AND date<=2026-09-30` in the canonical form. The server never
sees a period name, and a shared URL means the same range it meant when it was
written. This matches what Money Flow and the calendar already do with
`applyPeriod` — they store resolved dates in the URL.

## 3. The query input

The existing `search` box in `TransactionFilters.tsx` is replaced. Because a bare
word is plain search, `q` fully subsumes `search`; the `search` **parameter**
keeps working server-side for curl, MCP and TUI callers.

### The in-progress token rule

> The token under the caret at the end of the input is **in progress**, not
> invalid. It is excluded from diagnostics and is not serialized, so the server
> never receives it. A token becomes diagnosable once it is finished — a
> delimiter is typed after it, or the caret moves left of it. Pressing Enter
> finalizes the in-progress token, so `cat:` + Enter does report a missing
> value.

Without this rule, lenient matching plus autocomplete means every keystroke is a
parse: `c` then `ca` would report `unknown field: ca`, then `cat` would be valid,
then `cat:` invalid, then `cat:g` unresolvable — the warning banner would strobe
while the user types. This rule is the single most important UI behaviour in the
feature and is pinned by tests.

### Autocomplete

Context-aware, driven off the same field table the parser uses, so it cannot
suggest a term the parser will reject.

| Caret position | Suggests |
|---|---|
| fresh token | field names |
| after `field:` | values from `DomainDataContext` (accounts, categories, groups, payees) plus the page-local `tags` the page already loads (`Transactions.tsx:343`) |
| after a field with a fixed domain | the enum: `type:` → `debit`/`credit`; `linked:` → `true`/`false`; `recurring:` → `linked`/`unlinked`; `date:` → the existing `PERIOD_VALUES` vocabulary |

cmdk is already a dependency. Three behaviours make it usable rather than
decorative:

- **Insertion quotes.** `Whole Foods` has a space, so it is inserted as
  `payee:"Whole Foods"`. Without this a suggestion produces a query that parses
  as two tokens.
- **Categories disambiguate by group.** Ambiguous category suggestions are
  inserted qualified — `cat:Food/Groceries` — mirroring the nesting the filter
  bar already produces via `buildCategorySections`. A bare name that matches two
  categories still filters as an OR, and reports `ambiguous_value` (Section 5).
- **Keys.** `Enter` runs the query and never accepts a suggestion — the existing
  search box runs on Enter and that reflex is worth more than autocomplete
  convenience. `Tab` or `→` accepts the highlighted suggestion, `↑`/`↓` move,
  `Esc` closes the popup. A `?` button beside the input opens a grammar cheat
  sheet rendered from the same field table.

The request stays debounced at the existing 300 ms at the loader
(`Transactions.tsx:305`), not at the input.

## 4. Resolution: the frontend resolves names, the server resolves nothing

The surface syntax and the wire syntax are **the same syntax**. The frontend
parses with the same grammar, rewrites values (name → id, period → dates), and
re-serializes. It does not define a second grammar, which is what keeps the two
parsers from drifting: the only shared surface that can diverge is the resolver
table, and that is small.

- `category:groceries` → `cat:<uuid>`; a `Food/Groceries` qualified name
  disambiguates.
- `payee:costco` → `payee:<uuid>`.
- `acct:checking` → `acct:<uuid>`.
- `tag:vacation` → unchanged, because tags are names already.

Cost of this choice, stated plainly: a curl, MCP or TUI caller sending
`q=cat:groceries` gets **no category filter at all** and a diagnostic saying so.
That is the accepted trade for keeping name subqueries out of the SQL. The MCP
tool's argument description must say that values are ids.

## 5. Leniency: nothing fails

`q` never produces a 4xx. Every term that cannot be compiled is **dropped**, and
reported. Because dropping a constraint widens the result set, the report is part
of the contract, not a debug extra.

| Code | Raised by | Cause | Treatment |
|---|---|---|---|
| `unknown_field` | either | `catgory:food` | drop, report |
| `bad_operator` | either | `amount>>50` | drop, report |
| `missing_value` | either | `cat:` (after Enter) | drop, report |
| `unresolved_value` | either | a value the raiser cannot interpret | drop, report |
| `malformed_amount` | either | `amt:5.` | drop, report |
| `malformed_date` | either | `date:2026-13-45`, or outside the transaction date window | drop, report |
| `ambiguous_value` | **frontend only** | a name matched two rows | **keep the filter** (as an OR), report the ambiguity |
| `too_long` | backend | past the 2000-char / 32-term cap | drop, report |

`ambiguous_value` is local-only because the server only ever receives ids
(Section 4) and cannot know a name was ambiguous. Both raisers can produce
`unresolved_value`: the frontend when a name matches nothing in the user's data,
the server when a value is not an id it recognises — which is the case for every
curl, MCP and TUI caller, and is the visible cost of the resolution choice.

Out-of-window dates reuse `validation.CheckTransactionDate`
(`backend/internal/validation/date.go`), which `AGENTS.md` documents as a write
edge. Reusing it on a read filter is deliberate: a typo'd year is the failure
this project has already been bitten by, and catching it here turns a silently
empty result into a named diagnostic.

Three consequences, because the report is the only safeguard:

- The response count renders as `22 transactions · 1 term ignored`, so the
  number itself carries the caveat.
- Diagnostics are per-term with a reason, never a count.
- A banner in the Transactions header lists each dropped term verbatim and says
  why. Not a toast, not a console log.

**Diagnostics merge from two sources**, in order: local (produced by the
frontend's resolver, with zero latency) → server (from the response) → dedupe by
canonical token. Most diagnostics are therefore correct before the request lands.

### Response shape

`GetTransactions` renders its response from an inline `gin.H`
(`backend/handlers/transaction.go`), not a `models` struct, so there is no
`TransactionListResponse` type to extend. The handler gains one key, omitted
when the slice is empty:

```json
{
  "data": [ ... ],
  "total": 22,
  "page": 1,
  "limit": 50,
  "pages": 1,
  "queryDiagnostics": [
    { "term": "payee:costo", "code": "unresolved_value",
      "message": "no payee named \"costo\"", "position": 12 }
  ]
}
```

`position` is the byte offset of the term in the raw `q` text, so the UI can
highlight it.

## 6. Backend architecture

New package `backend/internal/query`:

- **`parse.go`** — tokenizer and lenient parser. **Never returns an error.**
  Produces `{Expr, []Diagnostic}`.
- **`compile.go`** — turns an `Expr` into clauses emitted into the caller's
  existing `txnFilter`.
- **`fields.go`** — the single field table: field name → validator → predicate
  emitter. The one place a field is defined; the parser, the suggester and the
  cheat sheet all read it.

`backend/handlers/transaction.go:103` (`txnQueryFilter`) reads `c.Query("q")`,
parses it, and hands the `Expr` to the compiler. That is the whole handler
change — roughly six lines.

**The compiler emits into `txnFilter`; it does not replace it.**
`txnFilter` hardwires `args[0] = userID` and `where()` hardcodes
`t.user_id = $1` (`transaction.go:443-504`). Every index in this schema leads
with `user_id`, so tenant scope must remain the outermost conjunct or no index is
used. The compiler appends through the existing `f.param` / `f.clause` / `f.anyOf`,
which appends new argument indices at the end.

**Consequence: existing pgxmock tests keep passing untouched.** They pin exact
query strings and argument order, and those are unchanged unless `q` is present.
Only new tests send `q`.

**Predicate generation is data-independent.** Every fragment is a compile-time
format string with a bound placeholder; user text only ever reaches a bound
argument. `~` uses the existing `escapeLikePattern` (`handlers/rule.go:484`) so
`100%` matches literally.

**CSV export inherits this.** `transaction_export.go:56` already calls the same
`txnQueryFilter`, and the frontend's `handleExport` copies the filter set. The CSV
therefore cannot drift from the table.

## 7. Frontend architecture

New directory `src/lib/query/`:

| File | Job |
|---|---|
| `parse.ts` | the grammar, in TypeScript |
| `resolve.ts` | name → id via `DomainDataContext` + page-local `tags`; period → dates |
| `serialize.ts` | `Expr` → the `q=` string |
| `fields.ts` | the field table, mirroring `backend/internal/query/fields.go` |
| `useQueryLanguage.ts` | text → debounce → resolve → send → merged banner |

`TransactionFilters.tsx` swaps its `search` input for the query input and renders
the banner. `api/client.ts` needs no change — `getTransactions` already takes a
flat `QueryParams`.

### The offline cache skips queried reads

`readCached`/`writeCached` are keyed on the **full URL**, so a query is not a
correctness hazard here — `?q=a` and `?q=b` are separate entries and cannot serve
each other's rows. The real cost is **entry proliferation**: every distinct `q`
occupies one of the 40 slots in `offlineCache.ts` and competes for the 2 MB total
cap, so a user exploring queries can evict the default unfiltered view they would
otherwise want offline.

Policy: `client.ts` skips the offline cache write for a `/transactions` read that
carries a `q`. A power user's exploratory queries therefore cannot displace the
last-known-good default view, and the offline experience stays "show me what I
last looked at" rather than becoming an unbounded query log.

## 8. Parity obligations

This repo treats drift as a build failure, so the language ships with all of
these or not at all:

- `backend/openapi.yaml` — a `q` parameter on `GET /transactions` and
  `GET /transactions/export`, including the repeated cross-site-guard 403 prose
  those operations already carry. Both operations are writing GETs guarded by
  `crossSiteGetGuard` (`main.go:389`); adding a parameter does not change that, and
  the guard list and `readonly.SideEffectingGETs` stay as they are.
- `client/api/endpoints_transactions.go` — `TransactionFilter.Query` and
  `setQuery`, mirroring the existing field/sentinel pattern.- `mcp/internal/mcpserver/tools_transactions.go` — a `q` argument with
  jsonschema prose stating that values are ids, not names.
  `tools_test.go` audits every argument, so it fails until this lands.
- `tui/` — nothing required. Route parity is per-route, and the shared client
  carries the parameter. A TUI query field is a separate, optional change.

## 9. Testing

**One shared corpus is the drift guard.**
`backend/internal/query/testdata/corpus.json` holds `(surface, canonical)` and
`(canonical, SQL fragment + arguments)` pairs. Go table tests read it; a vitest
suite reads *the same JSON* across the module boundary. A new field or operator
that is not in the corpus fails both suites. This cross-module read in a test is
an established pattern here — `client/api/spec_parity_test.go` already reads
`../../backend/openapi.yaml` the same way.

- **Backend** — parse/compile table tests over the corpus; an invariant test that
  every emitted fragment references only `t.` (or a correlated `EXISTS`), so
  the list query and the `COUNT(*)` query can never disagree; the cap test; and
  a handler test proving `q` AND-s with existing params and that diagnostics
  appear in the response.
- **Frontend** — parse/resolve/serialize unit tests over the corpus; the
  in-progress token rule (no diagnostics while typing, diagnostics after Enter);
  a component test that a dropped term produces a visible banner **and is not
  silently applied**; a test that the offline cache write contains no
  diagnostics.
- **Autocomplete** — insertion quoting, group-qualified categories, and that
  `Enter` submits rather than accepting a suggestion. Note the pinned `jsdom`
  constraint in `AGENTS.md`: re-run the full frontend suite before relaxing it.

## 10. Risks

- **Dropping a date term is the dangerous direction.** Every other dropped term
  widens the result set; a dropped `date>=2026-01-01` widens it across the whole
  period, and a date range is the user's main scoping tool. This follows the
  chosen "ignore invalid values" rule for consistency, and is mitigated by the
  frontend reporting locally and by `malformed_date` also catching out-of-window
  dates. It is called out here because it is the one place where the chosen
  policy is most likely to be wrong; it is a one-line change to make
  `malformed_date` fail closed if review disagrees.
- **An index gap the feature makes more reachable.** Neither the tag overlap
  (`t.tags && $n`) nor the free-text `LOWER(...) LIKE '%…%'` has a supporting
  index — there is no GIN and no `pg_trgm` anywhere in the repo. Both are
  sequential scans today. A query language makes composing them natural, so
  expect this to be felt. A GIN index on `tags` and trigram indexes are
  prerequisites if this becomes a performance issue; they are deliberately not
  part of this change.
- **`ensureBillingCycles` runs on `GET /transactions`.** A `q` that narrows to
  nothing does not change that, but the interaction is worth a regression check.

## 11. Why the other three surfaces are deferred

Dashboard, Money Flow and the calendar build their predicates with
`flowFilter` (`backend/handlers/money_flow.go:642`) and an inline `addCond`
(`backend/handlers/dashboard.go:71`) — a *second and third* predicate builder,
aliased, with a positional start index, and filtering on `accounts.id` rather
than `transactions.account_id`. Neither is composable with `txnFilter` without
rework, and the calendar computes its totals in Go after the query.

The grammar is kept free of Transactions-page assumptions — field names map to
columns and sentinels, not to UI concepts — so extending the compiler to
`flowFilter` later is a second emitter over the same `Expr`, not a new grammar.
It is a separate spec.
