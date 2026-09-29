# Offline conflict resolution — #36

## Problem

The offline layer handles its stated scope well. Reads are cached per user, and
manual **creates** are queued in an outbox carrying a client-generated
`clientKey` that `POST /transactions` treats as an idempotency key, so a replay
can never double-post. The queue is honest about itself in a way that is rare
and worth keeping: `writeEntries` reports whether the write landed,
`enqueueCreate` throws rather than resolving an entry that does not exist, a
full queue refuses rather than evicting, and a flush that cannot write back
reports `unsaved` instead of claiming progress.

It covers creates only. An **edit** made offline has no defined behaviour at
all — `api.updateTransaction` (`client.ts:569`) is a bare `PATCH` with no
offline path, so a `NetworkError` propagates and the edit is simply lost.

The intent is already half-present. The frontend's own rule is that an inline
edit must PATCH only the field the user changed, precisely so it does not revert
a concurrent edit (`Transactions.tsx:559,608`). Nothing reconciles that with a
server-side change made while the client was away, so the last writer silently
wins and one side's edit is lost. The rule exists to prevent exactly the loss
the queue cannot currently see coming.

## Proposal

Generalize the outbox from creates to **field-level patches**, and define what
happens when the server row has moved on: a three-way merge — base / mine /
theirs — at field granularity, with a keep-mine / keep-theirs surface, and a
hard rule that **neither side is ever silently discarded**.

The wire primitive already exists. `OptionalUUID` / `OptionalInt` with
`omitzero` are exactly the representation for a field-level patch: absent, set,
or explicit null. The primitive is there; the conflict semantics are not.

## Why a client-side merge rather than a 409

A server-side optimistic-concurrency token was considered and rejected. Adding
`409` to `PATCH /transactions/:id` would reach every existing client — the SPA,
the shared Go client, the TUI, the MCP server — and a conflict response an older
client treats as a hard failure is a compatibility break on a single-user
ledger, for a case that is rare (an offline *edit* of an already-synced row, as
against an offline *create*).

The token is also not free. `transactions.updated_at` exists
(`000001_initial_schema.up.sql`) but is never bumped — `UpdateTransaction`
(`transaction.go:787`) builds a dynamic `SET` with no `updated_at` in it — and
`GET /transactions` does not select it, so no client has a version token today.
Making one real is a migration, a spec change, a response-shape change, and a
new failure mode on a PATCH that has never had one.

So the guarantee is scoped honestly: **the offline queue against the server**,
not live concurrent writers. Two open tabs editing the same row at the same
instant is still last-write-wins, and this spec does not pretend otherwise. A
write landing in the window between the flush's read and its PATCH is likewise
unprotected; that window is one request wide.

The rule the layer already holds is the one that matters, and it holds without a
token: the storage write decides what the caller may claim. A queued edit is
never dropped on the floor, and a conflict is never resolved without the user
saying which side wins.

## Scope

**In:** every editable resource, and every write that decomposes into per-row
field units.

- The 12 row-update operations across 10 families: `PATCH /transactions/{id}`,
  `PUT /accounts/{id}`, `PUT /account-types/{id}`, `PUT /groups/{id}`,
  `PUT /categories/{id}`, `PUT /admin/categories/{id}`, `PUT /payees/{id}`,
  `PUT /rules/{id}`, `PUT /recurring/{id}`,
  `PUT /recurring/{id}/terms/{termId}`, `PUT /accounts/{id}/loan-schedule`,
  `PUT /paperless/settings`.
- The writes that name their rows: `POST /transactions/bulk-categorize`,
  `/bulk-payee`, `/bulk-billing-cycle`, `/bulk-loan`, `/bulk-tags`,
  `POST /recurring/attach`, `POST /recurring/detach`, and
  `PUT /accounts/{id}/loan-disbursement`. Each names its transactions — an
  explicit `transactionIds: string[]`, or a single `transactionId` for the
  disbursement — so each is N independent *(row, field)* units, the same unit
  the engine already works in. The loan disbursement is here rather than in the
  list above because it is not a row update at all: it writes a
  `loan_attachments` row pointing at a transaction that already exists, which is
  the `loanAccountId` field on the transaction and nothing more.
- The `id:` query-language term, which is how the flush reads one transaction.
- The online path, which begins sending the same diff.

**Deliberately out, stated so it is a decision and not a gap:**

- **`POST /transactions/bulk-delete`**, alongside every other delete. A delete
  has no merge — the row is gone, so the answer is keep or discard, not
  three-way. Deletes are a different entry kind and are left for a follow-up
  rather than half-built here.
- **`POST /tags/rename`.** It takes `{from, to}` and rewrites *every*
  transaction carrying the tag. The client cannot enumerate that membership
  while offline, so there is no base to diff against and no set of rows to merge
  per-row. It keeps today's behaviour: it fails loudly offline rather than
  rewriting rows the user was no longer looking at. A tag-rename merge is
  reference-record merging, which is proposal #7 in
  `docs/feature-proposals.md` and a different feature.
- **Live concurrent writers** (two open tabs, two devices, both online). Out by
  the decision above.
- **A 409, a version token, and `updated_at` maintenance.** Out by the same
  decision.
- **Cross-device sync.** The queue is per browser profile and per user; nothing
  here makes two devices reconcile.

---

## Design

### 1. The merge engine is one pure function

The unit of merge is **`(resource, rowId, field)`**. Three values per unit:
`base` (what the client believed when the edit was made), `mine` (what the user
set), `theirs` (what the server holds now). Four rules, in this order, per
field:

1. `mine` equals `base` → the user did not change it → take **theirs**
2. `theirs` equals `base` → nobody else changed it → take **mine**
3. `mine` equals `theirs` → both landed on the same value → take it
4. otherwise → **conflict**, recorded as `{field, base, mine, theirs}`

The result is `{patch, conflicts[]}`: `patch` holds the resolved non-conflicting
fields, `conflicts` holds what the user must decide. No network, no storage, no
React — so the entire policy is provable under a table test, and the UI cannot
quietly disagree with it.

Three details decide correctness:

- **Absent is not null.** A field the form never carried is `ABSENT`; `null` is
  the user *clearing* it. Rule 1 resolves `ABSENT`/`ABSENT` to theirs, so a
  field nobody touched is never sent. This is what makes a field-level patch
  safe to build from a whole-row form payload.
- **Collections compare order-insensitively.** `tags` is a `string[]`;
  `["a","b"]` and `["b","a"]` must not read as a conflict, or re-ordering tags
  offline manufactures one out of nothing.
- **No arithmetic, ever.** Amounts are compared as the numbers the API sent. The
  engine never sums, rescales or converts, so the `money.Amount` minor-unit rule
  and `money.MaxMinorUnits` are untouched by this work.

A `BulkEntry` is N units, so it is `clean` only when every unit is.

### 2. One write queue, three entry kinds

`outbox.ts` becomes a queue of pending writes under a discriminated union —
`CreateEntry | EditEntry | BulkEntry` — sharing an envelope of `key`,
`queuedAt`, `error?`, `conflict?`, `resolution?`, and `gone?`. At most one of
`error`, `conflict` and `gone` is ever set, and they mean different things:
`error` is a rejection to retry or discard, `conflict` is a held merge awaiting
the user, `gone` is a row that no longer exists, and `resolution` is the user's
answer to a `conflict`.

The v1 blob stays readable. A v1 entry has no `kind` field and its shape is
already identical to a v2 create, so a missing `kind` **defaults to
`"create"`**. An unsent create is user-recorded money, so it is adopted in
place rather than dropped, and there is no migration step to get wrong.

One flush loop, dispatching per kind. Creates keep today's path exactly, with
`queue: false` so the flush owns the retry. An edit or bulk reads theirs live —
**never from the offline cache**, because the cache holds the base, so
answering from it could not detect anything — then merges, then:

| Outcome | Action |
| --- | --- |
| clean | apply the patch, remove the entry |
| conflict | record `conflict` on the entry, **continue** past it |
| row gone | record `gone` on the entry, continue past it |
| 4xx rejection | record `error` (today's rule), continue past it |
| 401 / 403 / 5xx / transport | **stop the flush**, order preserved (today's rule) |

A held conflict is not an error and does not block the entries behind it, which
extends the existing "one bad entry must not block the rest" rule to a case
that is not a failure at all. While a conflict is held, each later flush re-reads
and re-merges it, so the user resolves against the server's latest state rather
than a snapshot that has since moved again.

Caps: `MAX_ENTRIES` stays 100. A byte budget is added beside it at **1MB**,
because a bulk entry carries N base values and size, not count, is what fills the
~5MB localStorage quota — and the read cache already claims up to 2MB of it
(`offlineCache.ts` `MAX_TOTAL_CHARS`), so 1MB keeps both inside the quota with
room for the theme and session cache. The rule is unchanged and now has a second
dimension: **refuse, never evict**. A queue already at either cap refuses the new
entry, and the refusal reaches the user as that edit's error, which is what lets
them sync first.

### 3. The resource registry, and why the apply shape is declared

Each family declares how to read theirs and how to send a diff. The apply shape
is **per family and declared, not inferred** — the handler code proves the
families genuinely differ, and a registry that guessed would silently null a
column:

- **`patch(diff)` — transactions.** `PATCH /transactions/{id}` takes a genuine
  partial update (`transaction.go:817-922` builds `SET` clauses only for fields
  present in the request, and `OptionalUUID` distinguishes absent from an
  explicit null). The diff goes straight through.
- **`putPartial(diff)` — accounts, account-types, categories, admin categories,
  category groups, paperless settings.** These build a dynamic `SET` or
  `COALESCE(NULLIF(...), col)`, so an absent key genuinely means "leave alone".
  The diff goes straight through.
  - *The empty-string trap:* `account.go:277-291`, `account_type.go:136`,
    `category.go:133` and `:326`, `category_group.go:122` all write
    `col = COALESCE(NULLIF($n, ''), col)`, so an empty string means "not
    provided". A diff can set a field but **can never clear it to `""`** —
    clearing is expressed by omitting the key. This is a property of the
    existing API, recorded here so an adapter does not appear to lose a clear.
  - *Settings trap:* `PaperlessSettingsResponse` carries `hasToken: bool` and
    **never the token** (`models.go:272-277`). The settings adapter must never
    send a token it does not hold; absent means unchanged, which is correct.
- **`putWhole(merged)` — payees, rules, recurring series, recurring terms, loan
  schedule.** These write every column on every call, so a diff sent alone would
  null the ones it omits.
  - `payee.go:118` — `UPDATE payees SET name = $1, account_id = $2`.
  - `rule.go:287` — every column of `rules`.
  - `recurring.go:922` — every column of `recurring_series`.
  - `recurring.go:485` — `SET end_date = $1`, so a diff that omits `end_date`
    clears it.
  - `loan.go:324` — an `INSERT … ON CONFLICT DO UPDATE` over the loan **terms**
    (principal, processing fee, rate, tenure, start and disbursal dates). The
    amortization periods are *derived* by `loadLoanScheduleDetail`, so this is a
    plain whole-row upsert of terms, not a replacement of a period array.

So for a `putWhole` family the diff is overlaid onto theirs and the merged row
is sent; for a `putPartial` or `patch` family the diff is sent as-is.

Reads: transactions need the new `id:` term (§4). Every other family is a small
collection read plus a find-by-id — `GET /accounts`, `/account-types`,
  `/categories`, `/groups`, `/payees`, `/rules`, `/recurring` each return the
  whole set, and admin categories/groups come from `GET /admin/catalog`. The
  settings adapter reads the singleton `GET /paperless/settings`; the loan
  schedule adapter reads `GET /accounts/{id}/loan-schedule`. The
  `bulk-*` and attach/detach writes read their target rows through the same
  `id:` term, one per row.

### 4. The `id:` query term

`q=id:<uuid>` is how the flush reads one transaction, chosen over a new
`GET /transactions/{id}` route because it adds no operation: the shared Go
client's `spec_parity_test.go` audits *operations*, the MCP `tools_test.go`
audits *routes*, and a filter term is neither, so no client is forced to grow a
method for it.

The change is one `fieldTable` entry (`fields.go:57-72`,
`{userTyped: true, kind: kindUUID, ops: opsEq}`) and one `emitColumn` case in
`compile.go`'s `emit` switch, beside `cat` and `acct` — `"t.id = $%d"` with no
null sentinel, because the column is the primary key. The fragment references
only `transactions t`, so the list and its `COUNT(*)` still share one predicate
and cannot disagree.

Three things must move with it, or the drift guards catch them:

- **A corpus case in `internal/query/testdata/corpus.json`.**
  `TestCorpusCoversEveryUserField` fails for any user-typed field with no case,
  which is the point: without one, the Go and TypeScript parsers are not pinned
  on it.
- **The TypeScript mirror at `frontend/src/lib/query/fields.ts`**, which is what
  the query input's autocomplete reads.
- **The `q` description in `openapi.yaml`**, which enumerates the fields twice —
  at `GET /transactions` (~1077) and at `GET /transactions/export` (~1671).
  `make openapi-check` and `make docs-check` both run over this file.

### 5. The online path sends the same diff

`diffAgainstBase(base, mine)` is the same pure function the queue uses.
`api.updateTransaction(id, mine, {base})` sends the diff when a base is supplied
and `mine` unchanged when it is not, so a caller holding no base row keeps
today's behaviour exactly.

`EditTransactionModal.tsx:204` passes the row it opened with. This is a
behaviour change and a deliberate one: a whole-row PATCH is precisely what
reverts a concurrent edit, so leaving it alone would keep the bug the issue
names while fixing only the queued case. The inline `EditableSelect` cells pass
their row too — already a single field, so the diff is a no-op, but the rule is
then uniform rather than per-call-site.

The modal's conditional `billingCycleId` is subsumed rather than special-cased:
absent from `mine` means absent from the diff, so nothing is sent. An account
move is already cleared server-side by the branch at `transaction.go:908`, so
the two paths cannot disagree about it.

### 6. The conflict surface

`OfflineBanner` — the only place the offline layer speaks to the user — gains a
third counter, **conflicts**, beside waiting and rejected, and a Resolve button.
The rejected surface (Retry / Discard) is untouched.

A dialog, opened by the authenticated layout per the repo's overlay rule, lists
one row per conflicting field: the field, your value, theirs, and the base that
makes the conflict legible. Keep-mine / keep-theirs per field, with keep-all
shortcuts. Resolving writes a `resolution` onto the entry; the next flush applies
the decided values **without re-merging** and removes the entry on success. A
resolved field is never re-decided by a later server change, because the user
already decided it.

A gone row appears as "deleted elsewhere" with **Re-create** / Discard.
Re-create posts the entry's stored row snapshot with the diff applied, through
`createTransaction` with a client-generated `clientKey` — the existing
idempotency guarantee, reused — so a retry cannot double-post. The new id is
stated in the UI, because the row that comes back is not the row that was
edited.

### 7. Failure semantics

- **The storage write decides what may be claimed**, extended to every new
  mutation. `enqueueEdit` throws rather than returning an entry that is not
  queued; resolving a conflict throws if the write fails; a re-create that
  cannot be recorded is reported, not swallowed.
- **`discardFailed` must not drop a conflict.** It drops every entry carrying an
  `error`, and a held conflict carries none, so it survives. This is pinned by a
  test, because the alternative is losing a held edit by clicking the wrong
  button. Discarding conflicts is a separate, explicit action.
- **`syncedAt` fires** on a clean apply and on a re-create, since the server's
  copy of the ledger moved under anything holding a snapshot of it. It does
  **not** fire on a held conflict, because nothing was written.
- A transport failure or a 401/5xx still stops the flush whole: an edit that may
  not have reached the server keeps its place in the order.

## Testing

- **The engine** is pure, so all four rules across absent / null / collection /
  amount are a dense table test, including the order-insensitive `tags` case and
  the `ABSENT`/`ABSENT` case that must resolve to theirs.
- **The queue** extends the existing `outbox.test.ts` patterns: cap refusal at
  both the entry and byte limits, `OutboxStorageError` on every new mutation,
  stop-versus-continue on each rejection class, a conflict held without blocking
  the entry behind it, and the re-create carrying a stable `clientKey`.
- **The v1 default** — an entry with no `kind` is adopted as a create, not
  dropped — is its own test, because the failure mode is silent data loss.
- **`discardFailed` keeps conflicts** is its own test.
- **The `id:` term** goes through the corpus and the compile tests, including
  the list/count agreement the shared predicate guarantees.
- **Each registry family** gets an apply-shape test asserting the exact request
  body its declared shape produces — in particular that a `putWhole` family
  sends the merged row and never a bare diff, since that is the failure that
  nulls a column rather than the one that is visible.
- **The dialog** gets interaction tests under the pinned `jsdom` (`~30.0.1`).

## Blast radius

- **Backend:** two small additions (the `fieldTable` entry and the `emitColumn`
  case), a corpus case, the `openapi.yaml` field lists. **No route is added and
  no response shape changes**, so `client/api/spec_parity_test.go`, the TUI and
  the MCP route audit are all unaffected.
- **Frontend:** `api/outbox.ts` grows the union; `api/client.ts` gains the
  `base` option and the `id:` reads; `EditTransactionModal.tsx` and
  `Transactions.tsx` pass a base; `OfflineContext.tsx` and `OfflineBanner.tsx`
  gain the conflict counter and the dialog.
- **Docs:** `AGENTS.md`'s frontend section describes the offline layer and its
  create-only scope, and `docs/feature-proposals.md` #11 should be marked
  implemented.

## Risks

- **An entry is larger than it was.** A queued edit carries a base row, and a
  bulk entry carries N of them. The byte budget bounds this, but it is a real
  cost against a ~5MB quota and the honest mitigation is the refusal, not
  eviction.
- **The read-then-write window is unprotected.** One request wide, by the
  decision above. A server-side token is the only thing that closes it, and that
  is the 409 this spec declines.
- **The PUT families are not uniform**, and an adapter that assumed they were
  would null a column — `recurring.go:485` clears `end_date` on a diff that
  omits it. This is why the shape is declared per family and tested per family.
- **`/tags/rename` and every delete stay unsupported offline**, so the promise
  is not perfectly uniform. Stated in Scope rather than left to be discovered.
