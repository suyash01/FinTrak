# Brainstorming — Issue #36: offline conflict resolution

Status: **in progress** (classify → clarify → approaches → design → spec → plan)

## Classification

**Architectural.** New wire contract on `PATCH /transactions/:id` (a conflict
response), a `version` token on the transaction read, the outbox generalized
from creates to field-level patches, and a new conflict-resolution surface.
Other clients (shared Go client, TUI, MCP) depend on that operation, so it
alters interfaces others rely on.

## Checklist

- [x] 1. Explore project context (outbox, client, OfflineContext, OfflineBanner,
      `UpdateTransaction`, `GET /transactions`, openapi, `docs/feature-proposals.md` #11)
- [x] 2. Visual companion — **not offered**: every question here was a scope or
      semantics choice, which is text, not layout. No question was clearer shown.
- [x] 3. Ask clarifying questions, one at a time (6 asked, all answered)
- [x] 4. Approaches — 2 proposed (queue structure), 1 chosen
- [x] 5. Present design in sections, approval per section — **all six approved**
- [x] 6. Write spec to `docs/superpowers/specs/2026-09-29-offline-conflict-resolution-design.md`,
      commit (`450f7d5`)
- [x] 7. Spec self-review — fixed 2 factual errors of my own (the family count
      9→10; the loan schedule described as a period-array replacement when
      `loan.go:324` upserts loan *terms* and the periods are derived), plus 3
      ambiguities (the byte budget had no number; the envelope was missing
      `resolution?`; a dangling `compile.go:93` line reference)
- [ ] 8. User reviews the written spec ← **here**
- [ ] 9. Invoke writing-plans

## Decisions (agreed with the user)

| # | Question | Decision |
|---|---|---|
| 1 | Who is the "other writer"? | **The offline queue vs the server only.** No 409, no optimistic-concurrency token, no `updated_at` change, no compatibility break for any existing client. |
| 2 | How to read one row ("theirs")? | **An `id:` term in the query language** (`q=id:<uuid>`), not a new route. The small PUT families read their whole live collection and find by id. |
| 3 | Which edits are queued? | **Every editable resource** — all 13 row updates across 9 families, plus every write that decomposes into per-row field units (6 `bulk-*`, `recurring/attach`/`detach`). `/tags/rename` is **excluded** with the reason written down. |
| 4 | On a real field conflict? | **Hold the entry, resolve per field** (keep mine / keep theirs, with keep-all shortcuts). Nothing is written until the user decides. |
| 5 | Row deleted server-side? | **Surface it, offer re-create** (through the existing idempotent create path) or discard. Never decide silently. |
| 6 | Online path too? | **Yes — one `diffAgainstBase()` for both.** An online edit also PATCHes only what changed, so it can no longer revert a concurrent edit to an untouched field. |
| 7 | Queue structure? | **One write queue + a resource registry.** A discriminated union of entry kinds, one storage key, one flush loop, one set of banner counters. |

## Grounded findings that shape the spec

- The update families are **not** uniform. `account.go:277-291` is a true partial
  update (`COALESCE(NULLIF($1,''), name)` + conditional `billing_day`/`closed`)
  so a diff can be sent directly; `payee.go:118` writes every column
  unconditionally, so the diff must be overlaid onto theirs first. The registry
  declares which, per family — it is not guessed.
- An account diff **cannot clear a field to `""`** — the empty string means "not
  provided" to `COALESCE(NULLIF(...))`. Clearing is expressed by omitting.
- `PaperlessSettingsResponse` carries `hasToken: bool` and never the token, so
  the settings adapter must never send a token it does not hold.
- `GET /transactions/{id}` is not registered; the list does not select
  `updated_at`, and `UpdateTransaction` never bumps it. No version token exists
  anywhere today — consistent with decision 1.
- `EditTransactionModal.tsx:204` PATCHes the whole row today; the inline
  `EditableSelect` cells (`Transactions.tsx:559,608`) already PATCH one field.
- `discardFailed` drops every entry carrying an `error`. A **conflict is not an
  error** — it must survive a "discard rejected", or the user loses a held edit
  by clicking the wrong button.

## Findings that shape the design

- `outbox.ts` entries are `{ key, queuedAt, request, error? }` — creates only.
- `api.updateTransaction` has no offline path at all: a `NetworkError` propagates
  to the caller, so an edit made offline is simply lost today.
- `EditTransactionModal` PATCHes the **whole row**; the "PATCH only the field the
  user changed" rule in AGENTS.md applies to the inline `EditableSelect` cells
  (`Transactions.tsx:559,608`).
- `transactions.updated_at` exists (`000001_initial_schema.up.sql`) but is never
  bumped by `UpdateTransaction` and never selected by `GET /transactions` — so
  there is no version token on the wire today, in any client.
- The outbox is bounded at `MAX_ENTRIES = 100`; the read cache at 40 entries / 2MB
  against a ~5MB localStorage quota.
- `OfflineBanner.tsx` is the only place the offline layer speaks to the user.

## Open questions carried from the issue

1. True three-way merge, or last-write-wins with a visible warning?
2. Transactions only, or every editable resource?
3. Offline-then-reconnect only, or any concurrent writer (two tabs, two devices)?
