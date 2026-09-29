# FinTrak — Feature Proposals for Review

**Status:** proposals only. Nothing here is designed, scheduled or approved. Each entry
states the problem, the proposed change, the blast radius, the risks and the open
questions, so it can be argued with before any of it becomes a spec.

**Date:** 2026-09-27
**Source:** a read-only sweep of the repository at `143caff`.

> **Two parts of this document are now behind the tree: #1 part 1, and the `ccy:`
> query field from #1 part 3.** The multi-currency work landed on this branch and
> fixed the bug this document opens with — every aggregate on the five reporting
> endpoints now reports per currency, with the reasons kept below and the current
> shape in the API section of `README.md`. The unconverted set is exposed in the UI
> too: `MultiCurrencyNotice`, mounted on the Dashboard, Money Flow and Cash Flow
> Calendar, names each currency a report is not showing, the accounts behind it and
> their income and expense, and says the figures are not added to the ones above.
> The `ccy:` field is in the tree as well (`backend/internal/query/fields.go`, added
> in `c074cd2`, which is part of this change — it is the plan's `ccy:` task),
> so the "`ccy:` is genuinely cheap" claim below is a record of work done rather
> than a suggestion. Still **not** done: dated exchange rates, and a `group by
> currency` option on the dashboard. #1 is below as the reasoning behind the change,
> not as an open finding — and the line numbers it quotes moved when the aggregates
> were rewritten.

---

## How to read this

The proposals are deliberately **not** a wishlist. They were chosen after mapping what
already exists, so that nothing here duplicates a shipped feature, and so that each one
names the specific existing machinery it would extend. Where an idea is a *new
subsystem* rather than an extension, that is called out — those are the expensive ones.

Section 2 is the most important part for a reviewer: **what any new route or table
actually costs in this repository.** Several ideas that look small in the abstract are
large here, and one idea that looks large is small. Read section 2 before the list.

Suggested review order: **#1** (it was a live bug, not a feature — its first part
shipped with this change, per the banner above, and the reasoning is kept below),
then **#2**, **#3** and **#4**, then **#9**. **#10** is small enough to ship in a
single sitting.

---

## 1. What already exists (so nothing here is a duplicate)

Features shipped today, by area:

| Area | Shipped |
| --- | --- |
| Ledger | transactions, bulk create/edit/delete, CSV/PDF/camt/OFX/Paperless import, filter grammar + typed `q=` query language, filter-aware CSV export |
| Accounts | multiple accounts, custom account types with a positive-transaction convention, optional billing day, closed accounts |
| Classification | category groups, categories, payees, free-text tags with rename-everywhere, priority-ordered rules with preview + apply |
| Structure | transaction links (transfer / cashback / bill payment), recurring series with effective-dated terms, loan accounts with generated amortization schedules, balance transfers, payoff, disbursement reconciliation |
| Reporting | dashboard summary, Money Flow Sankey + timeline, Cash Flow Calendar, billing-cycle statement periods, link-cycle diagnostics |
| Surfaces | React PWA (offline reads + write outbox), Bubble Tea TUI + optional SSH door, read-only MCP server, shared Go API client |
| Platform | JWT + rotating server-side refresh families, per-IP and per-identity rate limits, versioned JSON backup/restore, statement parser service |

Confirmed **absent** (searched for, zero real hits): budgets, savings goals, envelope
allocation, transaction splits, a cleared/reconciled state, webhooks or notifications,
receipt or attachment storage, multi-currency conversion, a full-text or trigram search
index, per-field edit history, and a scripting CLI.

---

## 2. The cost of a new route or a new table here

This repository is unusually strict about drift, and that strictness is the main budget
constraint on any proposal. Verified against the code, not assumed.

### 2.1 Adding one endpoint

1. Handler in `backend/handlers/<resource>.go` as a `*Server` method (dependencies come
   from the explicit `handlers.Server`, never a package global).
2. Route in `backend/main.go` `setupRouter`.
3. `backend/openapi.yaml` entry — otherwise `make openapi-check`
   (`TestOpenAPI`, `TestServeOpenAPISpec`) fails.
4. `client/api/endpoints_*.go` method **and** a `routeCase` in
   `client/api/spec_parity_test.go`. That suite is bidirectional: a route with no
   client claim fails, and a client claim with no route fails.
5. If read-only: an MCP tool in `mcp/internal/mcpserver`, plus a decision in
   `tools_test.go`'s `unexposedRoutes` if deliberately not exposed. The route must be a
   `GET` or one of only two admitted `POST`s. A `GET` that writes must be added to
   `readonly.SideEffectingGETs` **and** carry a `SideEffect` on its tool — the audit
   fails in both directions, so the pair cannot drift apart.
6. Frontend: `src/api/client.ts` + `src/types.ts` + a component.
7. Offline: add the path to `CACHEABLE_PATHS` in `src/api/offlineCache.ts` if it should
   work with no network. It is an explicit allowlist, so silence means "not offline".
8. TUI screen/keys if the feature is user-facing.
9. Tests: `pgxmock` with the **exact** query and arg order; `money.Amount` at every
   boundary; `validation.CheckTransactionDate` on every date write edge.
10. Coverage floors: backend 85, client 85, mcp 80, frontend 78 (lines), parser 90, tui 18.

### 2.2 Adding one table

On top of everything above:

1. `backend/db/migrations/NNNNNN_*.up.sql` + `.down.sql`. The schema is never edited in
   place; `000001_initial_schema` is a squashed base that later migrations alter.
2. The tenant convention: composite `(user_id, id)` primary key and composite tenant
   foreign keys. Every existing table does this, including transactions, links and
   billing cycles.
3. A deliberate `ON DELETE` choice per reference (cascade vs. set null), matching what
   the parent resource does today.
4. An index for the tenant-scoped access path.
5. **Backup bundle support** — the expensive one. `models.BackupBundle` is a fixed list
   of typed arrays, so a new table needs: a `Backup*` struct, an export query, a restore
   insert that mints a **fresh id** and remaps every reference, a `warnings` path for
   unreferenceable rows, and a decision on `version`.
6. Money as `BIGINT` minor units via `money.Amount` — never `float64`.

### 2.3 Invariants any proposal must respect

- `transactions.tags` is never NULL (`'{}'`, enforced at the DB boundary since
  migration `000013`). A NULL silently breaks `unnest(tags || …)` so a bulk tag add
  stores nothing, and serializes as `"tags": null`.
- Every transaction-date write edge goes through `validation.CheckTransactionDate`
  (`[1900-01-01, today+1y]`).
- `billing_cycle_id` follows the date unless the user detached it
  (`transactions.billing_cycle_detached`).
- Transactions on a closed account are immutable to every write path.
- Money never round-trips through `float64`.
- The app flags rather than hides, and never auto-categorizes, auto-links or
  auto-creates. This is a product ethos, not an accident — proposals that auto-mutate
  will be argued against on these grounds alone.

---

## 3. The proposals

### #1 — Multi-currency that refuses to lie

**Category:** correctness fix, then feature
**Size:** M (the fix alone is S)

#### Problem

`accounts.currency` is stored, validated to 3 characters, returned by the API and
carried in the backup bundle — and then never read by anything that computes a number.
`transactions` has no currency column, so every aggregate sums `amount` blind:

```sql
-- backend/handlers/dashboard.go:107
COALESCE(SUM(amount) FILTER (WHERE type = 'credit'), 0),
COALESCE(SUM(amount) FILTER (WHERE type = 'debit'), 0)
FROM transactions WHERE user_id = $1
```

The same pattern repeats in the category breakdown (`:119`, `:146`, `:424`, `:449`),
the trend series (`:174`), and the money-flow and calendar handlers. With no account
filter set, a USD account's dollars are added to an INR account's rupees. Every headline
number on the Dashboard, every Sankey edge and every calendar cell is affected.

It has survived because the default account filter means a single-account user never
sees it. It is invisible exactly when it is harmless and wrong exactly when it matters.

#### Proposal

Three separable parts. **Part 1 is a bug fix and should ship first, on its own.**

1. **Stop the lie (no new UI).** When an aggregate spans more than one currency and no
   conversion is possible, do not return a total. Return per-currency subtotals plus an
   explicit `unconverted` list naming the accounts and currencies involved. This mirrors
   the rule `frontend/src/lib/bankfiles/` already holds to — *"a value the reader cannot
   read exactly is dropped and counted, never rounded"* — and the parser's
   `no_transactions` vs `unsupported_format` distinction. A wrong total is worse than a
   refused one.
2. **Dated exchange rates.** A `exchange_rates` table (base, quote, date, rate as a
   rational or a scaled integer — **not** `float64`), user-entered. A per-user base
   currency. Conversion at the aggregate boundary, using the rate for each transaction's
   own date, with an explicit policy for a date with no rate (nearest prior rate, and
   say so).
3. **Query + reporting surface.** Add `ccy:` to the query field table, add a
   `group by currency` option to the dashboard, and expose the unconverted set in the UI.

Adding `ccy:` to the query language is genuinely cheap and is the best demonstration of
how well-designed that substrate is: one entry in `backend/internal/query/fields.go`, one
in `frontend/src/lib/query/fields.ts`, and the shared `testdata/corpus.json` pins them
together. The parser, the compiler, the autocomplete and the grammar sheet all read that
one table, so a field cannot be suggested that the parser will reject.

#### Why it fits

The refusal semantics are the point, and the repo has an established precedent for
exactly this posture in the bank-file readers. This is the one proposal here that is a
*bug*, so it should not wait for a design cycle.

#### Blast radius

Parts 1–2: new migration, new handler, changes to all five aggregate handlers, a new
client method set + parity cases, possibly an MCP tool, dashboard UI. Backup bundle
support if rates are per-user persisted. All four client surfaces if exposed.

#### Risks

- Refusing to return a total is a visible behaviour change for anyone who has multiple
  currencies today and has been reading a wrong number. Worth doing anyway; needs a
  release note.
- Rate sourcing: manual entry is honest and offline-safe; automatic rates mean a network
  dependency and a new failure mode. Manual first.
- Rounding at conversion is unavoidable somewhere. It must land in a documented place,
  and the summed minor units must stay exact per currency — conversion belongs at the
  display/aggregate boundary, never in a stored `amount`.

#### Open questions

- Should a transaction ever carry its own currency (a foreign-currency purchase on a
  domestic card)? That is a different and larger problem than account-level conversion.
- Historical rates: does editing a past rate retroactively change past reports, or are
  rates append-only?

---

### #2 — As-of (time-travel) reporting

**Category:** new analytical capability — a new subsystem
**Size:** L

#### Problem

There is no way to ask what the ledger looked like at a past moment. Every aggregate
answers "over this window", and there is no history table, so a past state cannot be
recovered after the fact. *"What was my net worth on the day I switched banks?"* and
*"how much had I actually paid on this loan by March?"* are both unanswerable.

#### Proposal

`asOf=YYYY-MM-DD` on the aggregates and on the query language, reconstructing the state
of the world at the end of that day: income, expense, net, per-category totals, per
account balance, outstanding loan principal, and cycle-attached totals.

The load-bearing constraint is **balance at a date**, not filtering by date. Filtering
by date already works; a running balance at a past instant is the real work, and it has
to respect the same sign conventions (`account_types.positiveTxnType`), the same link
rollup, and the same `billing_cycle_id` attachment rules that apply today.

This composes rather than parallels: `asOf` rides alongside the existing period window,
the `groupBy=billing_cycle` reframing, the loan schedule and the recurring forecast. The
interesting cases are exactly the interactions — an `asOf` inside a billing cycle, an
`asOf` before a loan's first installment, an `asOf` before an FX rate exists.

#### Why it fits

The app's whole character is derived, not stored: the loan table is generated, the
Money Flow graph is computed, cycles are materialized on read. Time-travel is the same
instinct applied to time. It also has a property worth stating in the spec: because
nothing is stored, `asOf` cannot disagree with the present, whereas an audit-log or
snapshot-table design can.

#### Blast radius

The five aggregate handlers, the transaction filter grammar, a new client method or
parameter across `client/api`, parity cases, the TUI dashboard, a dashboard date
control, and the MCP tools (where it is especially valuable — a model asking "what did
I look like then" is a natural question).

#### Risks

- **Performance.** Every aggregate gains a second date axis. Indexes on
  `(user_id, account_id, date)` and the existing `transactions_list_index` help, but
  this is the main risk and should be measured on a realistic ledger before committing.
- **Correctness of the past.** Backdated entries, an `asOf` earlier than the oldest
  transaction, and a rate/rule change that happened after the `asOf` date all need
  defined semantics. The honest answer for rules is "rules are not versioned, so an
  `asOf` report reflects today's rules" — which should be stated in the API, not
  discovered.
- Scope creep into general time-travel (schema history, deleted rows). This should be
  explicitly out of scope: `asOf` reconstructs from the **current** ledger.

#### Open questions

- Does `asOf` need to account for transactions added *after* the `asOf` date
  (a January statement imported in March)? Almost certainly yes, but it should be a
  stated decision, because the alternative is a confusing default.
- Per-transaction audit history is a genuinely different feature and should not be
  smuggled in here.

---

### #3 — What-if simulator with diffs

**Category:** extension of an existing read-only preview
**Size:** L

#### Problem

`POST /rules/preview` answers exactly one question: how many currently-uncategorized
transactions would this one rule change. There is no way to ask what a *structural*
change would do — merging two payees, renaming a category, deleting a category with
usage, adding a rule that outranks three others. Today the only way to find out is to
perform the change and look.

#### Proposal

A simulator that takes a hypothetical mutation, applies it to a **copy** of the
reference and rule data, and returns the **diff in every aggregate** — category totals,
income/expense, Sankey edges and node weights, counts of affected rows — before
anything is written. Mutations to support first: add/reorder/disable a rule, rename or
merge a payee, rename or move a category, delete a category.

The response is a structured diff, not prose: `[{scope, before, after, delta}]`, so a
client can render it and a model can act on it.

#### Why it fits

This is the principled form of a surface the MCP server does not have. Its package doc
says only that the server "is read-only, and nothing else" and that its suggestion
tools "cannot apply them" — it never describes a deferral, and it does not need to:
nothing here contradicts it. The current stance — the server implements
the read half and nothing else, so "a model's suggestions are always confirmed in the
app" — is preserved exactly: the model proposes a mutation, the server returns a diff,
the user confirms, and the apply runs the **same validated code path** as a manual edit.
The propose-only promise becomes enforceable rather than aspirational, because the
proposal is a typed, validated, diff-previewed intent rather than free text.

It is also a genuinely useful feature with no agent involved at all.

#### Blast radius

A new read-only `POST` — which means a decision in `readonly.readOnlyPosts` (currently
exactly two admitted non-`GET` routes, with the audit test as the reviewer), a tool in
`mcp/internal/mcpserver` with an accurate `SideEffect` declaration, new models, the
reference-data caches in `DomainDataContext`, and diff UI in the web and TUI clients.

#### Risks

- **Simulated state must not leak.** A copy-on-write simulation that accidentally shares
  a connection or a cache with real data would corrupt the ledger. The isolation
  boundary is the whole design problem here; a transaction-scoped read-only snapshot is
  probably the right answer.
- Diff noise. A rule reorder can touch thousands of rows; the response needs a cap and
  a total, in the spirit of the existing `truncated` flag on `list_links`.
- Divergence between "what the simulator predicted" and "what applying actually did".
  Mitigated by having apply report the same diff shape, and ideally by a test that
  asserts they agree.

#### Open questions

- Does the simulator support a *sequence* of mutations ("rename then merge"), or only
  one at a time?
- Should a simulated diff be persistable, so a proposal can be reviewed later?

---

### #4 — Projected balance: "will I go negative?"

**Category:** new composition of existing models
**Size:** M–L

#### Problem

The app can forecast a subscription, generate a loan amortization table, and compute a
balance per billing cycle — but it cannot answer the question that actually matters:
*given what I know is coming, when does my balance go negative, and by how much?* Today
that requires the user to hold four models in their head simultaneously.

#### Proposal

A forward projection per account over a horizon: a running balance that folds in (a)
recurring series terms, honouring their effective-dated ranges, gaps and
discontinuations; (b) loan installment due dates from the generated schedule; (c)
billing-cycle boundaries; (d) the current balance. Output: the balance line, the
**projected minimum**, and the **date it dips**, with the obligation responsible.

#### Why it fits

All four inputs already exist and are already correct — including the hard parts
(broken first periods, balance transfers, mid-period payoff). This is composition, not
new modelling, which is why it is smaller than #2. It also slots naturally into the Cash
Flow Calendar, which is already a date-axis view of one account.

#### Blast radius

A new aggregate handler, a projection model, calendar/dashboard UI, a client method, a
parity case, and plausibly an MCP tool (a model asked "can I afford this" is a natural
use of it).

#### Risks

- **Confidence.** A projection is a forecast, and the app's ethos is to flag rather than
  hide. It must be visually and textually distinct from actuals — a projected dip must
  never be mistaken for a real one. This is the single most important design constraint.
- Recurring series are **never** auto-created, by deliberate policy. A projection that
  silently assumed a subscription would continue past its last linked transaction would
  quietly contradict that. The projection must be explicit about which obligations are
  covered and which are not — the forecast endpoint already has a `covered` concept to
  build on.
- A projection implies future transactions. It must be **derived only** and never write
  them, or it breaks the never-auto-create rule.

#### Open questions

- Which obligations count when a recurring series has no term covering a future date?
- Should the projection show income as well as obligations, and if so, on what authority —
  a recurring salary series, or a manual override?

---

### #5 — Split transactions

**Category:** data-model change
**Size:** L

#### Problem

One transaction, one category. A single supermarket bill that is partly groceries, partly
household and partly personal must be either miscategorised or faked with several
transactions. This is the most common real case the current model cannot express.

#### Proposal

A `transaction_splits` table: N category lines per parent transaction, each with its own
category, amount in minor units, and optional note. A split parent reports its children
in Money Flow (one transaction fanning out into several category edges, which the Sankey
can already render) and its own amount in account-balance and loan contexts.

#### Why it fits

Composes with bulk operations, the link graph, the query language and the closed-account
freeze. The Money Flow graph is the genuinely interesting payoff — a single source row
becoming several category edges is exactly the structure a Sankey wants.

#### Blast radius

The widest of any proposal. New migration with tenant keys; split-aware create/update/
delete and every bulk operation; rules must not double-apply to a split parent; the query
compiler needs a "has splits" predicate; the backup bundle needs the new array with
fresh-id remapping; the frontend needs a split editor in the transaction modal; the TUI
needs it inline; MCP needs to expose splits on `list_transactions`. Every aggregate needs
a decision about whether it reads the parent or the lines.

#### Risks

- **Exact totals.** Split lines must sum to the parent exactly, in `int64` minor units,
  with no float residue. A "remainder" line is a smell; the API should reject an
  unbalanced set rather than auto-closing it.
- Double counting is the classic failure: an aggregate that sums parents *and* lines
  inflates every total. This must be settled once, centrally, and tested per aggregate.
- Rules, the closed-account freeze, export CSV and the bank-file importers all have to
  learn what a split parent is. Each is a place to get it subtly wrong.

#### Open questions

- Can a split parent be linked, or is a split the terminal unit of categorization?
- Do loan attachments and recurring attachments work on a parent, a line, or both?
- Is a split parent exportable as multiple CSV rows, one per line?

---

### #6 — Goals as derived views, not stored state

**Category:** data-model change (small), reporting feature (large)
**Size:** M

#### Problem

No goals or sinking funds. The obvious implementation — a goals table with a
`currentAmount` column updated as transactions land — is exactly the design that makes
budget apps lie: the stored progress drifts from the ledger, and reconciling the two is a
permanent chore.

#### Proposal

A goal is a **target amount, a target date, and a definition of the money that counts**:
an account, or a tagged subset of transactions. Progress is computed on read, always.
No stored running total exists, so progress cannot be stale.

The "definition of the money that counts" is where the existing query language pays off:
a goal over `tag:vacation` is a first-class concept rather than a bolted-on filter.

#### Why it fits

Derived-not-stored is the repo's established instinct — the loan table, the Sankey and
the billing cycles are all computed. A goal that cannot drift is a better fit here than a
goal that could.

#### Blast radius

A small migration, a goals handler, computed progress in the aggregate layer, a goals
panel on the dashboard, a client method, a TUI view, a backup bundle array, and an MCP
tool. All of it is additive — no existing read changes shape.

#### Risks

- A goal over an account double-counts across goals if two goals watch the same money.
  That may be correct (two goals funded from one account) or wrong (double-counted
  savings), and it needs an explicit answer, not a default.
- `MAX_ENTRIES`/quota pressure is not a concern here (server-side), but the offline cache
  allowlist and prefetch set would need the new path if goals must render offline.
- Derived progress is a query per goal; a dashboard with many goals needs batching.

#### Open questions

- Does a goal support regular contributions ("₹5,000/month into vacation")? That
  overlaps #4's projection and may be better expressed *as* a recurring series, which
  already models exactly that.
- Should a completed goal be archived, and does archiving touch the ledger?

---

### #7 — Merge and alias for reference records

**Category:** data-model change
**Size:** M

#### Problem

`RenameTag` exists and rewrites every reference. Nothing equivalent exists for payees or
categories. A user who has "Amazon", "AMZN", "Amazon.com" and "Amazon Pay" as four
payees, or two categories that mean the same thing, has no recourse except editing every
transaction.

#### Proposal

A merge operation per reference type: reassign every reference from source to target in
one transaction, report exactly what moved, and offer an undo window. An **alias** table
recording a merge so a future import that reintroduces "AMZN" resolves to the canonical
payee — which also improves import and rules matching.

#### Why it fits

It generalizes an existing, well-tested operation rather than inventing a mechanism, and
`RenameTag` is the template for both the reassignment and the "report what moved" part.

#### Blast radius

A merge handler per type (or one polymorphic), a new alias table, changes to payee/category
deletion semantics, import-time alias resolution, the rule matcher, the query resolver
(so `payee:"AMZN"` still finds the canonical record), backup bundle support, and UI in
both clients.

#### Risks

- Aliases and the query resolver interact: a query naming a merged-away id should
  resolve, and a query naming the old *name* should too. Getting this wrong makes old
  queries silently return nothing.
- A merge is destructive and hard to reverse without an undo log, which reintroduces the
  history table #2 avoided. A time-boxed undo in the same transaction as the merge is the
  cheaper option.
- Category merge has to decide what happens to rules pointing at the source.

#### Open questions

- Is a merge across account types / global vs. user categories allowed?
- Does an alias resolve at import time, at rules time, or both?

---

### #8 — The parser as document intelligence, not a statement reader

**Category:** extension of an existing service
**Size:** M

#### Problem

`statement_parser/` already classifies documents and runs a registry of per-bank
extractors (`register_extractor`), surfaced at `GET /statements/extractors`. It is
built to answer one question: what transactions are on this statement. Several other
documents a finance user already has would be valuable to read, and the infrastructure
to do it is standing there.

#### Proposal

Generalize the classification to a document class, and add two classes:

- **Loan agreement / sanction letter → a draft loan schedule.** Principal, rate, tenure,
  first installment date, fee — exactly the fields `LoanScheduleRequest` already takes —
  returned as an **uncommitted draft** the user reviews and confirms. This is the
  highest-value one: it replaces a genuinely tedious manual data entry with a
  confirm-what-we-read flow, and it cannot corrupt anything because nothing is written
  until the user agrees.
- **Receipt / invoice → a draft transaction**, optionally attached to a ledger row.

#### Why it fits

The upload, preview-then-confirm and reconciliation-warning machinery all exist and are
already reused across the manual and Paperless paths via `forwardStatementToParser`. A
new document class is a new extractor plus a classifier branch — the narrowest change of
any proposal here relative to its payoff.

#### Risks

- The parser handles hostile PDFs from an unauthenticated network path, so a new
  extractor widens that attack surface. It needs the same care as the existing ones.
- Extraction is a guess. Every draft must be visibly a draft, pre-filled but uncommitted,
  with the extracted values shown next to the confidence. The app never auto-creates.
- A wrong principal or rate would produce a wrong amortization table, which is worse than
  no table. The confirmation step is load-bearing, not decorative.

#### Open questions

- Which banks' formats? The registry is per-bank, so this is a per-bank cost.
- Should a loan draft also detect an *existing* loan and propose an update?
- Is receipt image storage in scope, or is the draft transaction the whole feature?
  (Storage is a genuinely new subsystem — see #11's note on attachments.)

---

### #9 — A first-class ledger health report

**Category:** new read-only surface, composing existing checks
**Size:** S–M

#### Problem

The app flags rather than hides, and it does so **scattered**: parser reconciliation
warnings on import, loan disbursement mismatches, circular-money diagnostics on the
Money Flow and Links pages, duplicate flags in the import preview. A user has to visit
the right page to learn the right thing. There is no single answer to "is my ledger
sane?", and several plausible checks are not implemented at all.

#### Proposal

One read-only `GET /diagnostics` returning a severity-ranked list of findings, each with
the rows that produced it and a suggested fix. Checks to start with:

- fingerprint duplicates in an account (the matching `POST /transactions/validate`
  already defines the fingerprint)
- transactions dated outside their attached billing cycle
- recurring occurrences in the past with nothing linked
- EMI payments matching no installment
- single-use tags (junk vocabulary), single-transaction payees (merge candidates — #7)
- unlinked transfers and one-sided flows (`GET /links/cycles` already computes these)
- accounts sharing a currency mix, i.e. the #1 condition
- rules whose conditions match nothing, and rules shadowed by a higher-priority rule

#### Why it fits

Almost every check reuses an existing query or an existing handler — several are
already computed and returned to a different page. This is the cheapest high-value
proposal in the list, and it is a natural MCP tool: read-only, cheap, and something a
model is genuinely good at triaging and explaining.

#### Blast radius

Small. One handler, a model, a client method, a parity case, an MCP tool with no side
effects, and a panel (Settings, or the command palette). No schema change, so no backup
work.

#### Risks

- False positives are the whole risk. A noisy report gets ignored, which is worse than
  no report. Every check should ship with a stated tolerance, and anything ambiguous
  should be phrased as an observation, not an error.
- Cost: a full-ledger scan per request. It needs to be a single pass or an index-assisted
  query set, and it will want a cache or a limit.
- It will surface bugs in existing features. That is a feature, but it means this
  proposal may return findings that need separate fixes.

#### Open questions

- Severity model: three levels or a boolean?
- Should a finding link to a filtered Transactions view, or open a repair dialog?

---

### #10 — TUI filter parity

**Category:** bounded fix
**Size:** S

#### Problem

The TUI's transaction filter form exposes search, account, category (including the
`uncategorized` sentinel), group, payee, type, linked, date range, tags, amount, limit
and page. It exposes **none** of `q`, `loanAccountId`, `excludeAttached`, `recurringId`
or `recurring` — all of which the shared client implements, the API documents and the
MCP `list_transactions` tool accepts. The web UI exposes `recurring` and `ccy` as
query-language terms instead (`frontend/src/lib/query/fields.ts`) and uses neither
`excludeAttached` nor `recurringId` at all, so on those two the browser is no more
capable than the terminal. `q` is the one that costs capability against the browser
rather than convenience: it is the typed query language, so a terminal user cannot
negate a term or use the comparison operators, which is what the dropdowns above
cannot do.

#### Proposal

Expose the missing filters in the TUI filter form. Start with `q`, since the TUI gains
the full power of the query language from one text field, and the grammar sheet already
exists.

#### Why it fits

`client/api.TransactionFilter` already carries every one of these, and
`client/api/endpoints_transactions.go` already serializes them. This is a UI gap over a
finished client, not new backend work.

#### Blast radius

`internal/ui/transactions.go` (the filter form) plus tests. Possibly a new overlay or
picker. No backend, no client, no schema, no parity work.

#### Risks

- A text field is much cheaper to build than eleven dropdowns. Resist building eleven
  pickers; `q` covers most of the surface in one input.
- The TUI's coverage floor is 18% and the screens are lightly covered, so new form code
  lands untested by default. Add tests for the new bindings.

#### Open questions

- Is the TUI's own route-parity test supposed to exist? See section 4 — this is worth
  resolving regardless of this proposal.

---

### #11 — Offline conflict resolution

> **Implemented** on the `offline-conflict` branch — the design and the plan are
> `docs/superpowers/specs/2026-09-29-offline-conflict-resolution-design.md` and
> `docs/superpowers/plans/2026-09-29-offline-conflict-resolution.md`. The three-way
> merge is client-side and no backend change was needed, so the 409 contract this
> proposal's Blast radius predicted does not exist. **Two things were left out on
> purpose**: `POST /tags/rename`, because it selects rows by tag membership and the
> client cannot enumerate a tag's rows offline, so there is no base to diff against;
> and **every delete**, because a row that is gone has no three-way merge — there is
> nothing on the server's side to merge with, and the only answer is keep or discard.

**Category:** extension of the hand-written offline layer
**Size:** M–L

#### Problem

The offline layer is well-built for its stated scope: reads are cached per user, and
manual **creates** are queued in an outbox with a client-generated `clientKey` that
`POST /transactions` treats as an idempotency key, so a replay cannot double-post. But it
covers creates only. An **edit** made offline against a row the server has since changed
has no defined behaviour — and the frontend's own rule is that an inline edit must PATCH
only the field the user changed, precisely so it does not revert a concurrent edit.

#### Proposal

Generalize the outbox from creates to field-level patches, and define what happens when
the server row has moved on. Three-way merge (base / mine / theirs) at field
granularity, with a keep-mine / keep-theirs surface, and a hard rule that the app never
silently discards either side.

The existing `OptionalUUID`/`OptionalInt` types with `omitzero` are already exactly the
wire representation for a field-level patch — absent, set, or explicit null. The
primitive exists; the conflict semantics do not.

#### Why it fits

The offline layer is deliberately hand-written (`vite-plugin-pwa` does not accept Vite 8),
so this is bespoke work by necessity. The `clientKey` idempotency pattern is the precedent
for "the queue is the source of truth, and the storage write decides what the caller may
claim."

#### Blast radius

`frontend/src/api/outbox.ts`, `client.ts`, the transaction edit path, the offline banner
(the only place that talks to the user about offline state), plus a new
`PATCH`-with-conflict contract on the backend — which means an `updated_at`-based
optimistic-concurrency check in `UpdateTransaction`, a 409 shape, and a decision about
whether a conflict is retryable.

#### Risks

- Three-way merge needs a **base** value, which means the queued entry must store what
  the row looked like when the edit was made. That is new state in the outbox, and the
  entry cap (`MAX_ENTRIES`, 40 reads / outbox entries) was chosen against a ~5MB
  localStorage quota. Enlarging entries shrinks how many fit.
- This adds a 409 to `PATCH /transactions/:id`, which every existing client must
  tolerate. A conflict response that older clients treat as a hard failure is a
  compatibility break.
- It is the highest-effort item here relative to how rarely the situation arises
  (offline *edit* of an already-synced row).

#### Open questions

- Is last-write-wins with a visible warning acceptable, or is true merge required?
- Does this extend to editing categories, payees and accounts offline, or transactions only?
- Does it need cross-device sync, or only offline-then-reconnect?

---

### #12 — `fintrakctl`: a scripting CLI

**Category:** new binary over an existing module
**Size:** S–M

#### Problem

The shared Go client exists and is excellent, and is consumed by two binaries (TUI, MCP).
There is no way to script it. A month-end reconciliation, a bulk import from cron, or an
ad-hoc `q=` query all require a browser or hand-written `curl`.

#### Proposal

A small CLI on `client/api`: `fintrakctl query "cat:Food amt:>500"`,
`fintrakctl import statement.pdf`, `fintrakctl export --format csv`, `fintrakctl
accounts`, with `--json` on everything and credentials from the environment (never a
command-line password, which is visible to other processes — the same reasoning the MCP
README applies to `-password`).

#### Why it fits

A new module (or a `cmd/` under `client/`) with its own `main`, on a client that is
already drift-guarded by `spec_parity_test.go`. The TUI's SSH door and the MCP server
prove the pattern of a thin binary over the shared client.

#### Blast radius

A new module needs its own CI job, its own coverage floor and a Codecov flag — or it
lives in an existing module and inherits that module's floor. This governance cost is
proportional and is the main thing to decide up front.

#### Risks

- A third consumer of the client means a third place a breaking client change surfaces.
  The parity suite covers routes, not semantics, so a field rename would break three
  callers.
- Scope creep toward a general-purpose admin tool. The value is in the 20% that scripts
  well (import, export, query).

#### Open questions

- New module, or a subcommand of the TUI binary, or a `cmd/` in the client module?
- Does it need write operations, or is read-only plus import enough for the first cut?

---

## 4. Documentation defects found while surveying

These are not features, but they were found alongside and are cheap to fix.

> **Status: all three corrected in the documents; the filed issues are still open.**
> They were filed as issues #40, #41 and #42 and the multi-currency work corrected
> both documents and committed this file in place of the `IDEAS.md` the tree named.
> **The three issues remain open as of this writing** — closing them is a
> maintainer action once the release carrying the corrections ships, and nothing
> here should be read as saying otherwise.
>
> Findings 1 and 3 below are left as written because they described the repository
> as it stood at the commit named at the top of this document. Finding 2's clause
> is not one a commit hash can scope. Only finding 2's clause needed a scope the
> commit hash could not supply, so only it is corrected in place.

1. **`README.md` lists a file that does not exist.** The project tree claims
   `IDEAS.md  # Feature backlog`. There is no `IDEAS.md` in the repository. Either
   create it (this document is a reasonable starting point, relabeled as a backlog) or
   remove the line.
2. **The README and `mcp/internal/mcpserver/mcpserver.go` both cite "backlog idea #56"
   (scoped read-only tokens) and "idea #59" (propose-only agent surface) — and the
   GitHub issue tracker was completely empty** (0 open, 0 closed) when this sweep
   ran. *(It is not empty now: this document's findings and the rest of the review
   are filed there, and the two gaps are now stated in their own terms in the
   `mcp/internal/mcpserver` package doc and the README, so a reader can follow the
   claim without a number that points at nothing.)* Those references were
   load-bearing prose in the MCP server's own doc comment, which explains its whole design.
3. **`AGENTS.md` claims the TUI has route-parity tests.** It states the terminal client
   "covers every operation in `backend/openapi.yaml` through the shared client and TUI
   route-parity tests." There is no such test: `tui/` contains no file referencing
   `openapi`, and its only test files are under `internal/ui` and `internal/sshd`. The
   guarantee is real but comes solely from `client/api/spec_parity_test.go`, which the
   TUI inherits transitively. Either add the test or correct the claim — #10 is the
   natural place to notice this.

---

## 5. Suggested sequencing

| Order | Proposal | Why here |
| --- | --- | --- |
| 1 | ~~**#1 part 1**~~ (stop summing across currencies) | **Done** — it was the live bug, and every later aggregate change is easier now that it is fixed. |
| 2 | **#9** (health report) | Cheapest high-value item, no schema change, and it will surface bugs worth fixing before the bigger work. |
| 3 | **#10** (TUI filter parity) | Bounded; closes a real capability gap in a sitting. |
| 4 | **#1 parts 2–3** (rates, UI; the `ccy:` query field is already in) | Builds on part 1's groundwork. |
| 5 | **#3** (what-if simulator) | The most valuable *new* capability per unit of risk, and it would give the MCP server a propose-and-apply surface it does not have. |
| 6 | **#4** (projection) | Composition of correct existing models; pairs well with #3. |
| 7 | **#7** then **#6** | Reference-record hygiene before derived reporting on top of it. |
| 8 | **#8** (document intelligence) | High payoff, narrow change, but per-bank extractor work. |
| 9 | **#2** (as-of), **#5** (splits) | The two largest, and the two that most change the shape of the data model. Both deserve their own spec, not a combined one. |
| 10 | **#11** (offline conflicts), **#12** (CLI) | Independent of everything above; schedule on appetite. |

**Not recommended without a decision first:** budgets/envelopes. The absence of budgets is
the most conspicuous gap versus a typical personal-finance tracker, which is presumably
why it is worth naming — but every budget implementation that stores progress separately
drifts from the ledger, and #6 (goals as derived views) is the design that avoids that
trap. If budgets are wanted, the derived approach should be considered first, because the
stored approach is the one that will disappoint.
