# Offline Conflict Resolution Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** An offline edit is queued as a field-level patch, merged against the server's current row on flush, and never silently discarded — a field both sides changed is held for the user to resolve.

**Architecture:** One pure three-way merge engine (`merge.ts`) with no I/O. One write queue (`outbox.ts`) holding a discriminated union of create / edit / bulk entries. A registry (`registry.ts`) declaring, per write op, how to read the server's current row and how to send a diff — the apply shape (`patch` / `putPartial` / `putWhole`) is declared because the handlers genuinely differ. The flush reads theirs live, merges, and either applies, holds a conflict, or records a gone row. `OfflineBanner` gains a third counter and a dialog resolves what the queue holds.

**Tech Stack:** TypeScript 5, React 19, Vite, Vitest + jsdom (`~30.0.1`, pinned), Tailwind 4, shadcn/Radix; Go 1.27, Gin, pgx, pgxmock.

**Spec:** `docs/superpowers/specs/2026-09-29-offline-conflict-resolution-design.md` — the spec travels with this plan; executors read both.

## Global Constraints

These hold for every task. Copy them verbatim into any test or type that touches them.

- **Absent is not null.** A key missing from a patch object is `ABSENT` (untouched). `null` is an explicit clear. `[]` is a real empty collection, distinct from absent. No sentinel value is used anywhere.
- **Collections compare order-insensitively.** `tags: ["a","b"]` equals `["b","a"]`, or re-ordering tags offline manufactures a conflict out of nothing.
- **The merge engine does no arithmetic.** Amounts are compared as the numbers the API sent. Never sum, rescale, or convert. `money.Amount` / `money.MaxMinorUnits` are untouched by this work.
- **The storage write decides what the caller may claim.** Every mutating queue function returns a boolean or throws; none reports a write that did not land.
- **Refuse, never evict.** Both the entry cap (100) and the byte cap (1MB) refuse the new entry. Evicting a queued write is unrecoverable data loss.
- **Read theirs live, never from the offline cache.** The cache holds the base, so answering from it could not detect a conflict.
- **The apply shape is declared per op, never inferred.** `account.go:277-291` and friends write `COALESCE(NULLIF($n,''), col)` (absent ⇒ leave alone); `payee.go:118` writes every column every time (absent ⇒ nulled). Guessing nulls a column.
- **A `putPartial` family can never clear a field to `""`** — the empty string means "not provided". Clearing is expressed by omitting the key.
- **The settings adapter never sends a token it does not hold.** `PaperlessSettingsResponse` carries `hasToken: bool` and never the token.
- **A conflict is not an error.** `discardFailed` drops entries carrying `error` and must never drop a held conflict.
- **Log with nothing.** This layer adds no logging; the frontend has no logger.
- **UI:** modals are `Dialog`, destructive confirms are `AlertDialog`, and only semantic tokens (`bg-card`, `text-muted-foreground`, `text-destructive`, `border-border`) — never raw palette classes.
- **jsdom stays pinned to `~30.0.1`.** Re-run the frontend suite before relaxing it.
- **Verify the frontend with** `bun run test`, `bun run typecheck`, `bun run build`. The backend with `go test ./...` and `make vet`.
- **Do not wire `POST /tags/rename` or `POST /transactions/bulk-delete`.** They are excluded on purpose, not overlooked — see the spec's Scope. An implementer who "helpfully" queues a delete has built an entry kind with no merge.

## Review Focus

Five input classes the spec implies but does not spell out, most likely to bite first. Each has its test assigned to the owning task below.

1. **Two queued edits to the same row before any flush.** The second entry's base must be the *locally projected* row — the server row plus every queued entry for it — not the server read and not the pre-first-edit row. Reading the server row makes entry 2 see a phantom conflict on the field entry 1 changed; reading the pre-edit row makes it see one on every field entry 1 touched. *(Task 4)*
2. **A `putPartial` clear to `""` that the server silently ignores.** `COALESCE(NULLIF('',''), col)` leaves the column as it was, so the queue removes the entry as "applied" and the user's edit vanishes — the exact silent discard this feature exists to prevent. The adapter must refuse to send an empty-string clear and surface it. *(Task 8)*
3. **A bulk write where some rows are clean, some conflict, and some are gone.** The three outcomes must not be conflated: the clean rows apply, the conflicted rows are held, and the gone rows are reported — or the whole entry is held. Decide and pin one. *(Task 10)*
4. **`tags: []` versus absent.** Emptying every tag is a change; not touching tags is not. A compare that treats them alike silently drops a real edit. *(Task 1)*
5. **A `putWhole` overlay producing `undefined` for a required column.** Merging a diff onto a row read that lacks a field the diff sets must not emit `undefined` into a NOT NULL column. *(Task 9)*

---

## File Structure

| File | Responsibility |
| --- | --- |
| `frontend/src/api/merge.ts` **(new)** | The pure engine: `valuesEqual`, `diffAgainstBase`, `mergeFields`. No I/O, no storage, no React. |
| `frontend/src/api/merge.test.ts` **(new)** | Table tests for all four rules and the three correctness details. |
| `frontend/src/api/registry.ts` **(new)** | `OPS`: per write op, the apply shape, how to read theirs, how to apply. |
| `frontend/src/api/registry.test.ts` **(new)** | Per-op apply-shape tests asserting the exact request body. |
| `frontend/src/api/outbox.ts` **(modify)** | The queue: entry union, v1 default, caps, enqueue, flush dispatch, conflict/gone/resolution recording. |
| `frontend/src/api/outbox.test.ts` **(modify)** | Extends the existing suite. |
| `frontend/src/api/client.ts` **(modify)** | `updateTransaction`'s `base` option and offline path; one offline path per write method. |
| `frontend/src/context/OfflineContext.tsx` **(modify)** | Conflict counts, `resolveConflict`, `reCreateGone`, `syncedAt` semantics. |
| `frontend/src/components/Layout/OfflineBanner.tsx` **(modify)** | The third counter and the Resolve button. |
| `frontend/src/components/Offline/ConflictDialog.tsx` **(new)** | Per-field keep-mine / keep-theirs. |
| `backend/internal/query/{fields,compile}.go` **(modify)** | The `id` term. |
| `frontend/src/lib/query/{fields,resolve.test}.ts` **(modify)** | The `id` mirror and its resolve case. |

---

### Task 1: The merge engine

The whole policy in one pure module. Nothing else in the plan can be built until this exists, and nothing here depends on anything else.

**Files:**
- Create: `frontend/src/api/merge.ts`
- Test: `frontend/src/api/merge.test.ts`

**Interfaces:**
- Consumes: nothing.
- Produces:
  ```ts
  export type FieldValue = string | number | boolean | null | string[] | undefined;
  export interface FieldPatch { [field: string]: FieldValue }
  export interface FieldConflict { field: string; base: FieldValue; mine: FieldValue; theirs: FieldValue }
  export interface MergeResult { patch: FieldPatch; conflicts: FieldConflict[] }
  export function valuesEqual(a: FieldValue, b: FieldValue): boolean;
  export function diffAgainstBase(base: FieldPatch, mine: FieldPatch): FieldPatch;
  export function mergeFields(base: FieldPatch, mine: FieldPatch, theirs: FieldPatch): MergeResult;
  ```

- [ ] **Step 1: Write the failing table test**

`frontend/src/api/merge.test.ts`. Cover all four rules, and the three details that decide correctness:

```ts
describe("valuesEqual", () => {
  it("treats an absent key and an empty collection as different", () => {
    // Review Focus #4. Emptying every tag is a change; not touching tags is not.
    expect(valuesEqual(undefined, [])).toBe(false);
    expect(valuesEqual([], [])).toBe(true);
  });
  it("compares tag collections without regard to order", () => {
    expect(valuesEqual(["a", "b"], ["b", "a"])).toBe(true);
  });
  it("distinguishes null (a clear) from an absent key", () => {
    expect(valuesEqual(null, undefined)).toBe(false);
    expect(valuesEqual(null, null)).toBe(true);
  });
});

describe("diffAgainstBase", () => {
  it("keeps only the fields whose value differs from the base", () => {
    const base = { notes: "", tags: ["a"], amount: 250.5 };
    const mine = { notes: "coffee", tags: ["a"], amount: 250.5, categoryId: null };
    expect(diffAgainstBase(base, mine)).toEqual({ notes: "coffee", categoryId: null });
  });
  it("treats a field absent from mine as untouched", () => {
    expect(diffAgainstBase({ notes: "a" }, {})).toEqual({});
  });
});

describe("mergeFields", () => {
  it("takes theirs when the user did not change the field", () => {
    // rule 1
    const r = mergeFields({ notes: "a" }, { notes: "a" }, { notes: "b" });
    expect(r).toEqual({ patch: { notes: "b" }, conflicts: [] });
  });
  it("takes mine when nobody else changed the field", () => {
    // rule 2
    const r = mergeFields({ notes: "a" }, { notes: "c" }, { notes: "a" });
    expect(r).toEqual({ patch: { notes: "c" }, conflicts: [] });
  });
  it("converges when both sides agree", () => {
    // rule 3
    const r = mergeFields({ notes: "a" }, { notes: "c" }, { notes: "c" });
    expect(r).toEqual({ patch: { notes: "c" }, conflicts: [] });
  });
  it("holds a conflict when both changed the field differently", () => {
    // rule 4 — nothing is written, so the field is absent from the patch
    const r = mergeFields({ notes: "a" }, { notes: "mine" }, { notes: "theirs" });
    expect(r.patch).toEqual({});
    expect(r.conflicts).toEqual([
      { field: "notes", base: "a", mine: "mine", theirs: "theirs" },
    ]);
  });
  it("resolves an untouched field to theirs without reporting a conflict", () => {
    // ABSENT/ABSENT is rule 1: a field nobody touched is never sent.
    const r = mergeFields({}, { notes: "a" }, { notes: "b" });
    expect(r).toEqual({ patch: { notes: "b" }, conflicts: [] });
  });
  it("merges the clean fields and holds only the conflicting one", () => {
    const r = mergeFields(
      { notes: "a", amount: 10 },
      { notes: "mine", amount: 20 },
      { notes: "theirs", amount: 10 },
    );
    expect(r.patch).toEqual({ amount: 20 });
    expect(r.conflicts.map((c) => c.field)).toEqual(["notes"]);
  });
  it("does no arithmetic on amounts", () => {
    // 0.1 + 0.2 !== 0.3 in float64; the engine compares, it never sums.
    const r = mergeFields({ amount: 0.1 }, { amount: 0.2 }, { amount: 0.1 });
    expect(r.patch).toEqual({ amount: 0.2 });
  });
});
```

- [ ] **Step 2: Run it to verify it fails**

Run: `bun run test -- src/api/merge.test.ts`
Expected: FAIL — `valuesEqual` is not exported (module does not exist).

- [ ] **Step 3: Implement `frontend/src/api/merge.ts`**

Export the four types and three functions above. `valuesEqual` is strict equality with one addition: when both sides are arrays, compare as sets (length equal and every member present on both sides). `diffAgainstBase` returns a new object holding only the keys of `mine` whose value is not `valuesEqual` to `base`'s. `mergeFields` iterates the union of `base`, `mine` and `theirs` keys and applies rules 1–4 **in that order**, reading a missing key as `undefined`; a conflicting field is pushed to `conflicts` and omitted from `patch`.

Header comment, in the house voice, stating: absent is not null; collections compare unordered; the engine does no arithmetic.

- [ ] **Step 4: Run it to verify it passes**

Run: `bun run test -- src/api/merge.test.ts`
Expected: PASS, all cases.

- [ ] **Step 5: Run the full frontend suite to confirm nothing else moved**

Run: `bun run test`
Expected: PASS, same count as before plus these.

- [ ] **Step 6: Commit**

```bash
git add frontend/src/api/merge.ts frontend/src/api/merge.test.ts
git commit -m "feat(offline): the three-way merge engine as a pure function"
```

---

### Task 2: The `id:` query term

Backend and frontend halves land together — the corpus is a two-way guard, so the Go suite and the vitest suite both fail on a half-done change. This is what lets the flush read one transaction without adding a route, and therefore without forcing a method onto the shared Go client, the TUI or the MCP audit.

**Files:**
- Modify: `backend/internal/query/fields.go` (the `fieldTable`, ~line 57)
- Modify: `backend/internal/query/compile.go` (the `emit` switch, beside `case "cat"`)
- Modify: `backend/internal/query/testdata/corpus.json`
- Modify: `frontend/src/lib/query/fields.ts` (`FIELD_TABLE`)
- Modify: `frontend/src/lib/query/resolve.test.ts` (`FIELD_CASES`)
- Modify: `backend/openapi.yaml` (the `q` description at ~1077 and ~1671)

**Interfaces:**
- Consumes: nothing.
- Produces: `GET /transactions?q=id:<uuid>` returns the single matching row. Callers read it as `getTransactions({ q: \`id:${rowId}\`, limit: 1 })` and take `data[0] ?? null`.

- [ ] **Step 1: Add the corpus case first, and watch both guards fail**

Append to `backend/internal/query/testdata/corpus.json` a case mirroring the existing `cat:` shape, with `"sql"` present (this is what pins the Go compiler, not just the parser):

```json
{
  "name": "id equality",
  "surface": "id:11111111-1111-4111-8111-111111111111",
  "canonical": "id:11111111-1111-4111-8111-111111111111",
  "terms": [{ "field": "id", "op": "=", "values": ["11111111-1111-4111-8111-111111111111"], "negated": false, "position": 0 }],
  "diagnostics": [],
  "sql": { "clauses": ["(t.id = $2)"], "args": ["11111111-1111-4111-8111-111111111111"] }
}
```

Run: `go test ./internal/query/ -run 'TestCorpus' -count=1`
Expected: FAIL — `TestCorpusGrammarContract` reports `id` as an unknown field.

- [ ] **Step 2: Add the Go field table entry**

In `backend/internal/query/fields.go`'s `fieldTable`, beside `"acct"`:

```go
"id":       {userTyped: true, kind: kindUUID, ops: opsEq},
```

No `sentinels`: the column is the primary key, so `IS NULL` can never match and the compiler would bind the literal string against it.

- [ ] **Step 3: Add the Go compiler case**

In `backend/internal/query/compile.go`'s `emit` switch, beside `case "acct"`:

```go
case "id":
    return emitColumn(t, sink, "t.id = $%d", "", negate)
```

The fragment names only `transactions t`, which is what keeps the list and its `COUNT(*)` sharing one predicate.

- [ ] **Step 4: Run the Go query suite**

Run: `go test ./internal/query/ -count=1`
Expected: PASS — including `TestCorpusCoversEveryUserField`, `TestCorpusGrammarContract` and `TestCorpusSQLContract`.

- [ ] **Step 5: Add the TypeScript mirror and its resolve case**

In `frontend/src/lib/query/fields.ts`'s `FIELD_TABLE`, beside `acct`:

```ts
id: { userTyped: true, kind: "uuid", ops: EQ },
```

In `frontend/src/lib/query/resolve.test.ts`'s `FIELD_CASES`, beside the `acct` line:

```ts
["id", "id:11111111-1111-4111-8111-111111111111", 1],
```

`resolveIds` already passes a value matching `UUID_RE` straight through, so no resolver change is needed; a non-uuid value reports `no id named …`, which is the honest answer for a row that has no name.

- [ ] **Step 6: Run the frontend query suites**

Run: `bun run test -- src/lib/query`
Expected: PASS — `parse.test.ts` reads the shared corpus and `resolve.test.ts`'s "has a case for every user-typed field" now passes.

- [ ] **Step 7: Update the `q` description in openapi.yaml**

In both the `GET /transactions` and `GET /transactions/export` `q` descriptions, add `id` to the field list, and state that a value is a transaction uuid. The surrounding sentence already says values are ids, so extending the parenthetical list is enough:

> (fields: desc, note, cat, group, acct, payee, tag, type, amt, date, linked, recurring, ccy, id)

Add one clause: `` `id` takes a transaction uuid, which is the only field that selects a single row. ``

- [ ] **Step 8: Verify the spec and docs gates**

Run: `make openapi-check` then `make docs-check`
Expected: both pass.

- [ ] **Step 9: Commit**

```bash
git add backend/internal/query/fields.go backend/internal/query/compile.go \
  backend/internal/query/testdata/corpus.json frontend/src/lib/query/fields.ts \
  frontend/src/lib/query/resolve.test.ts backend/openapi.yaml
git commit -m "feat(query): an id: term, so one transaction can be read back"
```

---

### Task 3: The queue's storage layer — union and v1 default

**Files:**
- Modify: `frontend/src/api/outbox.ts`
- Test: `frontend/src/api/outbox.test.ts`

**Interfaces:**
- Consumes: `FieldPatch`, `FieldValue` from Task 1.
- Produces:
  ```ts
  export type WriteOp = /* see the full union in Step 3 */;
  export interface ConflictUnit { rowId: string; field: string; base: FieldValue; mine: FieldValue; theirs: FieldValue }
  export interface PendingConflict { units: ConflictUnit[] }
  export interface WriteEnvelope {
    key: string; queuedAt: number;
    error?: string; conflict?: PendingConflict; resolution?: Record<string, "mine" | "theirs">; gone?: boolean;
  }
  export interface CreateEntry extends WriteEnvelope { kind?: "create"; request: CreateTransactionRequest }
  export interface EditEntry extends WriteEnvelope {
    kind: "edit"; op: WriteOp; rowId: string; base: FieldPatch; patch: FieldPatch; snapshot: FieldPatch;
  }
  export interface BulkEntry extends WriteEnvelope {
    kind: "bulk"; op: WriteOp; field: string; value: FieldValue; rows: string[]; bases: Record<string, FieldValue>;
    // The op's non-row identifiers, named after what each endpoint actually takes:
    // `seriesId` for transaction.recurring (RecurringAttachRequest), and
    // `loanAccountId` for transaction.loan (BulkLoanRequest, where omitting it
    // means detach). Detach and disbursement need neither.
    seriesId?: string;
    loanAccountId?: string;
  }
  export type QueuedWrite = CreateEntry | EditEntry | BulkEntry;
  export function getOutboxSnapshot(userId: string): QueuedWrite[];
  export function removeEntry(userId: string, key: string): boolean;
  export const MAX_ENTRIES: number;      // 100
  ```

  The byte budget is **not** in this task — it lands with `enqueueEdit` in Task 4,
  because a create is far too small to reach 1MB inside a 100-entry cap and only an
  edit can fill the queue. A cap added here would ship with no test.

- [ ] **Step 1: Write the failing test for the v1 default**

Append to `frontend/src/api/outbox.test.ts`. The byte-cap test lives in Task 4, because a create is far too small to reach 1MB inside a 100-entry cap and only `enqueueEdit` can fill the queue:

```ts
// A v1 blob has no `kind` and is already a create's shape. An unsent create is
// user-recorded money, so it is adopted rather than dropped — the alternative is
// silent data loss the first time a returning user flushes.
it("adopts a v1 entry with no kind as a create instead of dropping it", () => {
  localStorage.setItem(
    "fintrak_outbox:v1:user-1",
    JSON.stringify([{ key: "key-1", queuedAt: 1, request: request("Legacy") }]),
  );
  const entries = getOutboxSnapshot(USER);
  expect(entries).toHaveLength(1);
  expect(entries[0].kind ?? "create").toBe("create");
  if (entries[0].kind !== "bulk") {
    expect(entries[0].request.description).toBe("Legacy");
  }
});
```

- [ ] **Step 2: Run it to verify it fails**

Run: `bun run test -- src/api/outbox.test.ts`
Expected: FAIL — `enqueueEdit` is not exported.

- [ ] **Step 3: Widen the entry types**

Replace `OutboxEntry` with the union above. `WriteOp` is the full list of 19 ops the registry keys on — eight `transaction.*`

```ts
export type WriteOp =
  | "transaction.patch" | "transaction.categorize" | "transaction.payee"
  | "transaction.billingCycle" | "transaction.tags" | "transaction.loan"
  | "transaction.recurring" | "transaction.loanDisbursement"
  | "account.put" | "accountType.put" | "group.put" | "category.put"
  | "adminCategory.put" | "payee.put" | "rule.put" | "recurring.put"
  | "recurringTerm.put" | "loanSchedule.put" | "settings.put";
```

- [ ] **Step 4: Default a missing `kind` to `"create"`**

In `parseEntries`, after `JSON.parse`, map each parsed object through a normalizer that returns `{ ...raw, kind: raw.kind ?? "create" }`. Leave the existing "an unchecked cast of our own persisted queue" comment in place and extend it to say why the default is safe.

- [ ] **Step 5: Run it to verify it passes**

Run: `bun run test -- src/api/outbox.test.ts`
Expected: PASS — including all 20 pre-existing create tests, unchanged.

- [ ] **Step 6: Commit**

```bash
git add frontend/src/api/outbox.ts frontend/src/api/outbox.test.ts
git commit -m "feat(offline): the write queue holds creates, edits and bulk writes"
```

---

### Task 4: Enqueuing an edit, with the base projected through the queue

This is where Review Focus #1 is pinned: the base of a second edit to the same row must be the locally projected row, not the server's.

**Files:**
- Modify: `frontend/src/api/outbox.ts`
- Test: `frontend/src/api/outbox.test.ts`

**Interfaces:**
- Consumes: `enqueueCreate`'s signature and `OutboxStorageError` from `outbox.ts`; `FieldPatch` from Task 1.
- Produces:
  ```ts
  export const MAX_TOTAL_CHARS: number;   // 1_000_000
  export function enqueueEdit(
    userId: string, op: WriteOp, rowId: string,
    base: FieldPatch, patch: FieldPatch, snapshot: FieldPatch,
  ): EditEntry;
  export function enqueueBulk(
    userId: string, op: WriteOp, field: string, value: FieldValue,
    rows: string[], bases: Record<string, FieldValue>,
    ids?: { seriesId?: string; loanAccountId?: string },
  ): BulkEntry;
  export function queuedPatchFor(userId: string, op: WriteOp, rowId: string, field: string): FieldValue;
  export function queuedRowProjection(userId: string, op: WriteOp, rowId: string): FieldPatch | null;
  ```

  **The entry key is generated, not derived.** `enqueueEdit` and `enqueueBulk` mint a
  `crypto.randomUUID()` and return the entry, so a caller or test reads `entry.key`.
  A key like `` `${op}:${rowId}` `` would be wrong: two edits to the same row before a
  flush are the whole reason the locally projected base exists, and a deterministic
  key would collapse them into one and lose the first.

- [ ] **Step 1: Write the failing tests**

Append to `frontend/src/api/outbox.test.ts`:

```ts
// A byte budget beside the entry cap: a bulk entry carries N base values, and
// size rather than count is what fills the ~5MB quota. Refuse, never evict.
it("refuses a new entry when the byte cap is reached, keeping every entry queued", () => {
  const fat: FieldPatch = { notes: "x".repeat(200_000) };
  // Five ~200KB entries exhaust the 1MB budget; the sixth is refused.
  for (let i = 0; i < 5; i++) {
    enqueueEdit(USER, "transaction.patch", `row-${i}`, { notes: "" }, fat, fat);
  }
  expect(() =>
    enqueueEdit(USER, "transaction.patch", "row-5", { notes: "" }, fat, fat),
  ).toThrow(/queue is full/i);
  const left = getOutboxSnapshot(USER);
  expect(left).toHaveLength(5);
  expect(left[4]).toMatchObject({ kind: "edit" });
});

// Review Focus #1. The base of a second edit to the same row must be the row as
// it stands locally (server row + everything already queued for it), or entry 2
// sees a phantom conflict on the field entry 1 changed.
it("bases a second edit to the same row on the locally projected row", () => {
  enqueueEdit(USER, "transaction.patch", "row-1", { notes: "" }, { notes: "first" }, { notes: "" });
  expect(queuedRowProjection(USER, "transaction.patch", "row-1")).toEqual({ notes: "first" });

  enqueueEdit(USER, "transaction.patch", "row-1", { notes: "" }, { notes: "second" }, { notes: "" });

  const edits = getOutboxSnapshot(USER).filter((e) => e.kind === "edit");
  expect(edits).toHaveLength(2);
  expect(edits[1].base).toEqual({ notes: "first" });
});

it("reports the effective queued value for a field, or undefined when untouched", () => {
  expect(queuedPatchFor(USER, "transaction.patch", "row-1", "notes")).toBeUndefined();
  enqueueEdit(USER, "transaction.patch", "row-1", { notes: "" }, { notes: "x" }, { notes: "" });
  expect(queuedPatchFor(USER, "transaction.patch", "row-1", "notes")).toBe("x");
});
```

- [ ] **Step 2: Run it to verify it fails**

Run: `bun run test -- src/api/outbox.test.ts`
Expected: FAIL — `enqueueEdit` is not exported.

- [ ] **Step 3: Implement `enqueueEdit` and the byte cap**

Follow `enqueueCreate`'s shape exactly: read entries, return the existing entry if one with the same `key` is queued, throw the cap error if either cap is reached, build the entry, and `throw new OutboxStorageError()` unless `writeEntries` returned true.

The byte cap lands here rather than in Task 3, because a create is far too small to reach 1MB inside a 100-entry cap and only an edit can fill the queue. Add `MAX_TOTAL_CHARS = 1_000_000`; compute `JSON.stringify(entries).length` before writing and throw the same `queue is full` `Error` the entry cap throws when either cap is reached, with a message naming both. Do not change the refusal-not-eviction behaviour.

Compute the stored `base` as `queuedRowProjection(userId, op, rowId)` when that returns a value, falling back to the caller's `base` when the row has nothing queued for it. The caller's `base` is the row the form opened with; the projection is that row with every queued patch for it applied. This is the whole of Review Focus #1, and the comment must say why reading the server row instead would be wrong.

- [ ] **Step 4: Implement `enqueueBulk` and the two readers**

`enqueueBulk` mirrors `enqueueEdit`. `queuedPatchFor` walks the queued entries newest-last and returns the last `patch[field]` for `(op, rowId)`, or `undefined`. `queuedRowProjection` returns `null` when nothing is queued for the row, else the base with every queued patch overlaid — merging with `valuesEqual` semantics is unnecessary here because it is a plain last-write overlay.

- [ ] **Step 5: Run it to verify it passes**

Run: `bun run test -- src/api/outbox.test.ts`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add frontend/src/api/outbox.ts frontend/src/api/outbox.test.ts
git commit -m "feat(offline): enqueue an edit against the locally projected row"
```

---

### Task 5: Flushing an edit or bulk — merge, apply, hold, or record a gone row

**Files:**
- Modify: `frontend/src/api/outbox.ts`
- Test: `frontend/src/api/outbox.test.ts`

**Interfaces:**
- Consumes: `mergeFields` (Task 1), the entry union (Task 3). `theirs` is injected as a callback rather than imported from the registry, so this task is testable before Task 6 lands and so the "never from the cache" rule is visible at every call site.
- Produces:
  ```ts
  export interface FlushOutcome {
    sent: number; remaining: number; failed: number; unsaved: number;
    conflicted: number; gone: number; recreated: number;
  }
  export type TheirsReader = (op: WriteOp, rowId: string) => Promise<FieldPatch | null>;
  export async function flushOutbox(
    userId: string,
    send: (entry: QueuedWrite) => Promise<void>,
    options?: { retryFailed?: boolean; theirs?: TheirsReader },
  ): Promise<FlushOutcome>;
  export function recordConflict(userId: string, key: string, conflict: PendingConflict): boolean;
  export function recordGone(userId: string, key: string): boolean;
  export function resolveConflict(userId: string, key: string, resolution: Record<string, "mine" | "theirs">): boolean;
  export function discardConflicts(userId: string): number;
  ```

  `theirs` takes `(op, rowId)` rather than the entry, so the same reader serves a single-row entry and each row of a bulk entry.

- [ ] **Step 1: Write the failing tests**

```ts
it("holds a conflicted entry without blocking the entry behind it", async () => {
  enqueueEdit(USER, "transaction.patch", "row-1", { notes: "a" }, { notes: "mine" }, { notes: "a" });
  enqueueEdit(USER, "transaction.patch", "row-2", { notes: "a" }, { notes: "ok" }, { notes: "a" });
  const sent: string[] = [];
  const theirs: TheirsReader = async (_op, rowId) =>
    ({ notes: rowId === "row-1" ? "theirs" : "a" });

  const outcome = await flushOutbox(USER, async (e) => {
    if (e.kind === "edit") sent.push(e.rowId);
  }, { theirs });

  expect(outcome.conflicted).toBe(1);
  expect(outcome.sent).toBe(1);
  // row-1 was never sent: the merge held it before the apply was reached.
  expect(sent).toEqual(["row-2"]);
  const left = getOutboxSnapshot(USER);
  expect(left).toHaveLength(1);
  expect(left[0].conflict?.units[0]).toMatchObject({
    rowId: "row-1", field: "notes", base: "a", mine: "mine", theirs: "theirs",
  });
});

it("records a gone row when the read finds nothing", async () => {
  enqueueEdit(USER, "transaction.patch", "row-1", { notes: "a" }, { notes: "mine" }, { notes: "a" });
  const outcome = await flushOutbox(USER, async () => {}, { theirs: async () => null });
  expect(outcome.gone).toBe(1);
  expect(getOutboxSnapshot(USER)[0].gone).toBe(true);
});

it("applies the decided values without re-merging once resolved", async () => {
  enqueueEdit(USER, "transaction.patch", "row-1", { notes: "a" }, { notes: "mine" }, { notes: "a" });
  await flushOutbox(USER, async () => {}, { theirs: async () => ({ notes: "theirs" }) });
  resolveConflict(USER, "key-1", { notes: "mine" });

  const sent: FieldPatch[] = [];
  const outcome = await flushOutbox(USER, async (e) => {
    if (e.kind === "edit") sent.push(e.patch);
  }, { theirs: async () => ({ notes: "changed-again" }) });

  // The user decided "mine" and the server moved again: the decision stands and
  // the entry is not re-held.
  expect(outcome.sent).toBe(1);
  expect(outcome.conflicted).toBe(0);
  expect(sent).toEqual([{ notes: "mine" }]);
  expect(getOutboxSnapshot(USER)).toHaveLength(0);
});

it("never lets discardFailed drop a held conflict", () => {
  enqueueEdit(USER, "transaction.patch", "row-1", { notes: "a" }, { notes: "mine" }, { notes: "a" });
  recordConflict(USER, "key-1", {
    units: [{ rowId: "row-1", field: "notes", base: "a", mine: "mine", theirs: "theirs" }],
  });
  expect(discardFailed(USER)).toBe(0);
  expect(getOutboxSnapshot(USER)).toHaveLength(1);
  expect(discardConflicts(USER)).toBe(1);
  expect(getOutboxSnapshot(USER)).toHaveLength(0);
});
```

  `enqueueEdit`'s third argument is the base, so every entry above keys on `"key-1"`; keep the call sites and the `record*` calls using the same literal.

- [ ] **Step 2: Run it to verify it fails**

Run: `bun run test -- src/api/outbox.test.ts`
Expected: FAIL — `recordConflict`, `resolveConflict` and the `theirs` option are not exported.

- [ ] **Step 3: Widen `flushOutbox`'s options**

```ts
export async function flushOutbox(
  userId: string,
  send: (entry: QueuedWrite) => Promise<void>,
  options: { retryFailed?: boolean; theirs?: (entry: EditEntry | BulkEntry) => Promise<FieldPatch | null> } = {},
): Promise<FlushOutcome>
```

`theirs` is injected rather than imported so this task is testable without the registry, and so the "never from the cache" rule is visible at the call site. When absent, an edit entry is applied without merging — the pre-registry behaviour, used only by tests.

- [ ] **Step 4: Implement the dispatch**

Inside the existing loop, for a `create` keep today's code path byte for byte. For an `edit`: skip when `error` is set and `retryFailed` is off; otherwise read theirs, and:

| Condition | Action |
| --- | --- |
| theirs is `null` | `recordGone`, `outcome.gone += 1`, `continue` |
| entry has a `resolution` | send the decided values against theirs, then remove |
| `mergeFields(...).conflicts.length > 0` | `recordConflict`, `outcome.conflicted += 1`, `continue` |
| otherwise | `send` the entry, then remove |

**What gets sent is `entry.patch` verbatim, not the merge's output.** `mergeFields`
iterates the union of all three key sets, so its `patch` echoes every field of the
row — sending that would make a queued edit revert a concurrent change to a field
the user never touched, which is the exact bug this feature exists to fix. On a
clean merge the engine's job is to confirm nothing conflicts, not to rewrite the
payload. The merge output's `patch` is used only to build the `putWhole` overlay,
whose endpoint writes every column anyway (the spec says a `putPartial` or `patch`
family sends the diff as-is).

`send` is the single dispatch point and receives the entry narrowed to what may
actually be applied, so the production path and the tests exercise the same seam: a
create posts it, an edit PATCHes or PUTs it, and a bulk calls
`OPS[op].applyMany(entry.rows, entry.value)`.

Every write of `error` / `conflict` / `gone` must go through the same
`if (!writeEntries(...)) { unsaved += 1; break; }` guard the existing error path
uses. A `bulk` entry runs `mergeFields` per row and treats the entry as conflicted
when any row is.

- [ ] **Step 5: Implement the four queue mutators**

`recordConflict`, `recordGone` and `resolveConflict` each read, mutate the one matching entry, `writeEntries`, and return whether the write landed. `discardConflicts` drops entries carrying a `conflict` or a `gone`, and throws `OutboxStorageError` if the write fails — mirroring `discardFailed`. Leave `discardFailed` itself filtering on `error` only; the test above is the guarantee.

- [ ] **Step 6: Run it to verify it passes**

Run: `bun run test -- src/api/outbox.test.ts`
Expected: PASS, including the 20 pre-existing create tests.

- [ ] **Step 7: Commit**

```bash
git add frontend/src/api/outbox.ts frontend/src/api/outbox.test.ts
git commit -m "feat(offline): flush an edit by merging, holding or recording it gone"
```

---

### Task 6: The op registry

Every write op declares its apply shape and how to read theirs. The shape is data, not a guess.

**Files:**
- Create: `frontend/src/api/registry.ts`
- Test: `frontend/src/api/registry.test.ts`

**Interfaces:**
- Consumes: `api` from `client.ts`; `FieldPatch` from Task 1; `WriteOp` from Task 3.
- Produces:
  ```ts
  export type ApplyShape = "patch" | "putPartial" | "putWhole";
  export interface OpSpec {
    op: WriteOp;
    shape: ApplyShape;
    /** The mergeable projection of a row, or null when it is gone. Live, never cached. */
    read(rowId: string): Promise<FieldPatch | null>;
    /** Send the resolved diff. `theirs` is the row the diff was merged onto. */
    apply(rowId: string, diff: FieldPatch, theirs: FieldPatch | null): Promise<void>;
    /** Present only for transaction rows, which can be re-created. */
    reCreate?(snapshot: FieldPatch, diff: FieldPatch): Promise<string>;
    /** Present on ops that write one field across named rows. */
    applyMany?(rows: string[], value: FieldValue): Promise<void>;
  }
  export const OPS: Record<WriteOp, OpSpec>;
  export function readTheirs(op: WriteOp, rowId: string): Promise<FieldPatch | null>;
  export function applyOp(op: WriteOp, rowId: string, diff: FieldPatch, theirs: FieldPatch | null): Promise<void>;
  ```

- [ ] **Step 1: Write the failing shape test**

```ts
describe("declared apply shapes", () => {
  it.each<[WriteOp, ApplyShape]>([
    ["transaction.patch", "patch"],
    ["account.put", "putPartial"],
    ["category.put", "putPartial"],
    ["settings.put", "putPartial"],
    ["payee.put", "putWhole"],
    ["recurringTerm.put", "putWhole"],
    ["loanSchedule.put", "putWhole"],
  ])("%s is %s", (op, shape) => {
    expect(OPS[op].shape).toBe(shape);
  });
});
```

- [ ] **Step 2: Run it to verify it fails**

Run: `bun run test -- src/api/registry.test.ts`
Expected: FAIL — module does not exist.

- [ ] **Step 3: Implement the transactions op**

`transaction.patch`: `read` calls `api.getTransactions({ q: \`id:${rowId}\`, limit: 1 })` and returns a projection of `data[0]` or `null`. `apply` calls `api.updateTransaction(rowId, diff, { queue: false })`. `reCreate` calls `api.createTransaction({ ...snapshot, ...diff, clientKey: newClientKey() }, { queue: false })` and returns the new id — the existing idempotency guarantee, reused, so a retry cannot double-post. `newClientKey` is currently private in `client.ts`; export it for this use rather than duplicating the UUID logic, since its fallback for a non-secure context (a LAN address without TLS) is load-bearing.

**`read` must not pass an `accountId`.** The list endpoint injects synthetic summary rows — per-cycle "Total outstanding" and month-end "Running balance" — when a single account is filtered and the sort is by date, and it guards on `accountUUID != nil`, which comes only from an `accountId` parameter, never from `q=`. So the read is safe precisely because it scopes with `q` alone, and stops being safe the moment an `accountId` is added. Say so in a comment, so nobody "optimises" the call by scoping it.

The projection maps a `Transaction` to `{ accountId, date, description, amount, type, categoryId, tags, notes, payeeId, billingCycleId }`, leaving a key off the object when the field is nullish so absent stays distinct from `null`.

- [ ] **Step 4: Implement the eleven single-row PUT ops**

`account.put`, `accountType.put`, `group.put`, `category.put`, `adminCategory.put`, `settings.put` are `putPartial`: `apply` sends the diff straight through. `payee.put`, `rule.put`, `recurring.put`, `recurringTerm.put`, `loanSchedule.put` are `putWhole`: `apply` overlays the diff onto `theirs` and sends the merged row.

Their `read` is the family collection read plus a find-by-id, returning the same projection shape. `settings.put`'s `read` is the singleton `getUserSettings()`; it must never send `paperlessToken`, because the response carries only `hasToken`.

- [ ] **Step 5: Implement the seven multi-row ops**
`transaction.categorize`, `.payee`, `.billingCycle`, `.tags`, `.loan`, `.recurring`, `.loanDisbursement` each carry `applyMany` and reuse `read`. `transaction.loanDisbursement` writes one row, so it takes a single `transactionId` and calls `api.linkLoanDisbursement`.

- [ ] **Step 6: Run it to verify it passes**

Run: `bun run test -- src/api/registry.test.ts`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add frontend/src/api/registry.ts frontend/src/api/registry.test.ts
git commit -m "feat(offline): a registry declaring each op's apply shape"
```

---

### Task 7: The transactions path end to end

The one family a reviewer can exercise completely: queue an edit offline, flush it, see it applied or held.

**Files:**
- Modify: `frontend/src/api/client.ts` (`updateTransaction`, plus `getTransaction`)
- Test: `frontend/src/api/client.test.ts`

**Interfaces:**
- Consumes: `enqueueEdit` (Task 4), `OPS` (Task 6), `FieldPatch` (Task 1).
- Produces:
  ```ts
  // client.ts
  getTransaction: (id: string) => Promise<Transaction | null>;   // q=id:<uuid>, limit 1
  updateTransaction: (
    id: string, data: UpdateTransactionRequest,
    options?: { base?: Transaction; queue?: boolean },
  ) => Promise<Transaction>;
  ```

- [ ] **Step 1: Write the failing tests**

Append to `frontend/src/api/client.test.ts`, inside the suite that already sets `fetchMock = vi.fn()` and `storeUser({ id: "u1", email: "a@b.c" } as never)`:

```ts
const baseTxn = {
  id: "t1", accountId: "a1", date: "2026-01-15", description: "Coffee",
  amount: 250.5, type: "debit", categoryId: null, tags: [], notes: "old",
  payeeId: null, billingCycleId: null,
} as Transaction;

it("queues an edit that never reached the server, and answers queued", async () => {
  fetchMock.mockRejectedValue(new TypeError("failed to fetch"));
  const result = await api.updateTransaction("t1", { notes: "offline" }, { base: baseTxn });
  expect(result).toEqual({ id: "t1", queued: true });
  const entries = getOutboxSnapshot("u1");
  expect(entries[0]).toMatchObject({ kind: "edit", op: "transaction.patch", rowId: "t1" });
});

it("sends only the changed field when a base is supplied", async () => {
  fetchMock.mockResolvedValue(jsonResponse({ message: "updated" }));
  await api.updateTransaction("t1", { notes: "new", description: "Coffee" }, { base: baseTxn });
  const body = JSON.parse(fetchMock.mock.calls[0][1].body);
  expect(body).toEqual({ notes: "new" });
});

it("sends the whole payload when no base is supplied", async () => {
  fetchMock.mockResolvedValue(jsonResponse({ message: "updated" }));
  await api.updateTransaction("t1", { notes: "new", description: "Coffee" });
  const body = JSON.parse(fetchMock.mock.calls[0][1].body);
  expect(body).toEqual({ notes: "new", description: "Coffee" });
});

it("surfaces a rejected edit rather than queueing it", async () => {
  fetchMock.mockResolvedValue(jsonResponse({ errors: [{ message: "amount must be positive" }] }, 400));
  await expect(api.updateTransaction("t1", { amount: -1 })).rejects.toThrow(/amount must be positive/);
  expect(getOutboxSnapshot("u1")).toHaveLength(0);
});

it("does not queue when the queue refuses the entry", async () => {
  // A queue that does not exist is an edit the user believes is saved.
  vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
    throw new Error("QuotaExceededError");
  });
  await expect(
    api.updateTransaction("t1", { notes: "x" }, { base: baseTxn }),
  ).rejects.toBeInstanceOf(OutboxStorageError);
});
```

- [ ] **Step 2: Run it to verify it fails**

Run: `bun run test -- src/api/client.test.ts`
Expected: FAIL — `updateTransaction` takes no `options` and never queues.

- [ ] **Step 3: Add `getTransaction`**

`getTransaction: (id) => api.getTransactions({ q: \`id:${id}\`, limit: 1 }).then(r => r.data[0] ?? null)`. It is a thin wrapper the registry and the UI share; it must not be cached beyond what `offlineCache` already does for `/transactions` without `q` — and note `isQueriedLedgerRead` already excludes `q=` reads from the cache, which is exactly right here.

- [ ] **Step 4: Widen `updateTransaction`**

Add the options parameter. When `options.base` is present, send `diffAgainstBase(projection(base), projection(data))` instead of `data` — one diff, both paths, per the spec. When it is absent, send `data` unchanged, so a caller holding no base row behaves exactly as today.

On a `NetworkError` (and only a `NetworkError`), enqueue via `enqueueEdit` and resolve `{ id, queued: true }`; rethrow when `options.queue === false` or the identity changed, mirroring `createTransaction`'s existing `stillOwner` guard. Extend the result type to `UpdateTransactionResult { id: string; queued: boolean }`.

- [ ] **Step 5: Run it to verify it passes**

Run: `bun run test -- src/api/client.test.ts src/api/client.methods.test.ts`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add frontend/src/api/client.ts frontend/src/api/client.test.ts
git commit -m "feat(offline): a transaction edit queues when the server is unreachable"
```

---

### Task 8: The `putPartial` families

Review Focus #2 lives here: a clear to `""` that the server silently ignores must not be reported as applied.

**Files:**
- Modify: `frontend/src/api/client.ts` (`updateAccount`, `updateAccountType`, `updateCategory`, `updateGlobalCategory`, `updateGroup`, `updateUserSettings`)
- Modify: `frontend/src/api/registry.ts`
- Test: `frontend/src/api/registry.test.ts`

**Interfaces:**
- Consumes: `OPS` (Task 6), `enqueueEdit` (Task 4).
- Produces: each of the six methods gains `options?: { base?: <RowType>; queue?: boolean }`, returning its row type unchanged when online and `{ queued: true }` when the request never reached the server.

- [ ] **Step 1: Write the failing test for the empty-string clear**

```ts
// Review Focus #2. account.go writes COALESCE(NULLIF($n,''), col), so an empty
// string means "not provided" — the server would ignore the clear and the queue
// would remove the entry as "applied". Refuse it instead.
it("refuses a putPartial clear to an empty string rather than reporting it applied", async () => {
  await expect(applyOp("account.put", "a1", { bank: "" }, { bank: "HDFC" })).rejects.toThrow(
    /cannot be cleared to an empty value/i,
  );
});
```

- [ ] **Step 2: Run it to verify it fails**

Run: `bun run test -- src/api/registry.test.ts`
Expected: FAIL — the call resolves instead of rejecting.

- [ ] **Step 3: Guard `putPartial` applies**

In `applyOp`, when `shape === "putPartial"`, reject any diff entry whose value is `""`, naming the field and the op. The error must be an `Error` the flush treats as a per-entry rejection, so the entry is marked with it and reaches the user rather than vanishing. Add a comment citing `account.go:277-291` and the four other `COALESCE(NULLIF(...))` sites.

- [ ] **Step 4: Widen the six client methods**

Give each the same shape as `updateTransaction`: an optional `base`, the diff computed from it, and a `NetworkError` path that enqueues. `updateUserSettings` is the singleton — its `base` is the `UserSettings` the settings form opened with, and it must never put a `paperlessToken` into a diff.

- [ ] **Step 5: Add a per-op apply-shape test for each of the six**

Assert the exact request body: a `putPartial` op sends the diff unchanged, and none of them sends a `paperlessToken`.

- [ ] **Step 6: Run it to verify it passes**

Run: `bun run test -- src/api/registry.test.ts src/api/client.test.ts`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add frontend/src/api/client.ts frontend/src/api/registry.ts frontend/src/api/registry.test.ts
git commit -m "feat(offline): queue the partial-update families, refusing an empty-string clear"
```

---

### Task 9: The `putWhole` families

Review Focus #5 lives here.

**Files:**
- Modify: `frontend/src/api/client.ts` (`updatePayee`, `updateRule`, `updateRecurringSeries`, `updateRecurringSeriesTerm`, `saveLoanSchedule`)
- Modify: `frontend/src/api/registry.ts`
- Test: `frontend/src/api/registry.test.ts`

**Interfaces:**
- Consumes: `OPS` (Task 6), `enqueueEdit` (Task 4).
- Produces: each of the five methods gains `options?: { base?: <RowType>; queue?: boolean }`.

- [ ] **Step 1: Write the failing tests**

```ts
// payee.go:118 writes every column every time, so a bare diff nulls account_id.
it("sends the merged row for a putWhole op, never the bare diff", async () => {
  const body = captureBody(() => applyOp("payee.put", "p1", { name: "New" }, { name: "Old", accountId: "a1" }));
  expect(body).toEqual({ name: "New", accountId: "a1" });
});

// Review Focus #5. Merging onto a read that lacks a field must not emit
// undefined into a NOT NULL column.
it("never emits undefined into a merged putWhole row", async () => {
  const body = captureBody(() => applyOp("recurringTerm.put", "t1", { endDate: "2026-01-01" }, { endDate: null }));
  expect(Object.values(body)).not.toContain(undefined);
  expect(body.endDate).toBe("2026-01-01");
});
```

- [ ] **Step 2: Run it to verify it fails**

Run: `bun run test -- src/api/registry.test.ts`
Expected: FAIL — a `putWhole` op currently forwards the diff.

- [ ] **Step 3: Implement the `putWhole` overlay**

In `applyOp`, when `shape === "putWhole"`, build the request as `{ ...theirs, ...diff }` with every `undefined` value dropped from the result, and throw when `theirs` is `null` (a gone row must reach the `recordGone` path, not an overlay of nothing). `recurringTerm.put` is the sharpest case — `recurring.go:485` writes `SET end_date = $1`, so an omitted `end_date` clears it.

- [ ] **Step 4: Widen the five client methods**

Same shape as Task 8. Note in a comment that `loanSchedule.put` upserts loan **terms** and the periods are derived server-side, so the merge is over the terms row.

- [ ] **Step 5: Run it to verify it passes**

Run: `bun run test -- src/api/registry.test.ts src/api/client.test.ts`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add frontend/src/api/client.ts frontend/src/api/registry.ts frontend/src/api/registry.test.ts
git commit -m "feat(offline): overlay the whole-row families onto the server's copy"
```

---

### Task 10: The multi-row writes

Review Focus #3 lives here: clean, conflicted and gone rows in one entry must not be conflated.

**Files:**
- Modify: `frontend/src/api/client.ts` (`bulkCategorize`, `bulkUpdatePayee`, `bulkUpdateBillingCycle`, `bulkLoan`, `bulkUpdateTags`, `attachRecurring`, `detachRecurring`, `linkLoanDisbursement`)
- Modify: `frontend/src/api/registry.ts`
- Test: `frontend/src/api/registry.test.ts`

**Interfaces:**
- Consumes: `enqueueBulk` (Task 4), `OPS` (Task 6).
- Produces: each of the eight methods gains `options?: { base?: Record<string, FieldValue>; queue?: boolean }` — the per-row base values for the one field the op writes. An offline call returns `{ queued: true, queuedRows: number }`.

- [ ] **Step 1: Write the failing test for the mixed-outcome bulk entry**

```ts
// Review Focus #3. Clean rows apply, conflicted rows are held, gone rows are
// reported — the three must not collapse into one outcome.
it("splits a mixed bulk entry into applied, held and gone", async () => {
  enqueueBulk(USER, "transaction.categorize", "categoryId", "c1",
    ["r1", "r2", "r3"], { r1: null, r2: null, r3: null });
  const applied: string[][] = [];
  const theirs: TheirsReader = async (_op, rowId) => {
    if (rowId === "r3") return null;                       // gone
    return { categoryId: rowId === "r2" ? "c-other" : null }; // r2 moved, r1 not
  };
  const outcome = await flushOutbox(USER, async (e) => {
    if (e.kind === "bulk") applied.push(e.rows);
  }, { theirs });

  expect(outcome.sent).toBe(1);
  expect(outcome.conflicted).toBe(1);
  expect(outcome.gone).toBe(1);
  // Only r1 went out; the entry stays queued for the other two.
  expect(applied).toEqual([["r1"]]);
  expect(getOutboxSnapshot(USER)).toHaveLength(1);
});
```

  The `send` callback receives the entry with `rows` narrowed to the clean ones, which is what `applyMany` is called with in production.

- [ ] **Step 2: Run it to verify it fails**

Run: `bun run test -- src/api/registry.test.ts`
Expected: FAIL.

- [ ] **Step 3: Implement the per-row split in the flush**

A `bulk` entry computes `mergeFields({ [field]: bases[rowId] }, { [field]: value }, theirsRow)` per row, partitions the rows into `clean` / `conflicted` / `gone`, calls `applyMany(clean, value)` when `clean` is non-empty, and records the `ConflictUnit`s and the gone rows on the entry. The entry stays queued while anything is held or gone, and the header comment must state the decision: a bulk write applies the rows it can and holds the rest, because the user asked for all of them and dropping the held ones silently is what this feature exists to prevent.

- [ ] **Step 4: Widen the eight client methods**

Each takes the per-row bases, calls `enqueueBulk` on a `NetworkError`, and otherwise calls the existing endpoint. `linkLoanDisbursement` takes a single `transactionId`, so it enqueues a one-row `BulkEntry` rather than an `EditEntry` — its `applyMany` is the single-id disbursement call.

- [ ] **Step 5: Run it to verify it passes**

Run: `bun run test -- src/api/registry.test.ts src/api/client.test.ts`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add frontend/src/api/client.ts frontend/src/api/registry.ts frontend/src/api/registry.test.ts
git commit -m "feat(offline): queue the row-naming multi-row writes per row"
```

---

### Task 11: The online path sends the same diff

A behaviour change, and a deliberate one: a whole-row PATCH is exactly what reverts a concurrent edit.

**Files:**
- Modify: `frontend/src/components/Transactions/EditTransactionModal.tsx` (the `updateTransaction` call, ~line 204)
- Modify: `frontend/src/components/Transactions/Transactions.tsx` (the two inline calls, ~559 and ~608)
- Test: `frontend/src/components/Transactions/EditTransactionModal.test.tsx`, `Transactions.test.tsx`

**Interfaces:**
- Consumes: `updateTransaction`'s `options.base` (Task 7).
- Produces: no new exports. Both call sites pass the row they are editing.

- [ ] **Step 1: Update the existing tests to the new call shape**

In `EditTransactionModal.test.tsx`, the suite already renders the modal inline against the `baseTransaction` fixture at line 63 and mocks `api.updateTransaction`. Change the expectation at ~line 265 from the full payload to the diff, and add:

```ts
it("PATCHes only the field the user changed", async () => {
  // The whole-row PATCH is what reverts a concurrent edit to an untouched field.
  apiMocks.updateTransaction.mockResolvedValue({ id: "t1", queued: false });
  apiMocks.getBillingCycles.mockResolvedValue({ data: [] });
  render(
    <EditTransactionModal
      transaction={baseTransaction}
      accounts={[account]}
      categories={noCategories}
      groups={noGroups}
      payees={noPayees}
      onClose={() => {}}
      onSaved={() => {}}
    />,
  );
  await userEvent.clear(screen.getByLabelText("Notes"));
  await userEvent.type(screen.getByLabelText("Notes"), "milk");
  await userEvent.click(screen.getByRole("button", { name: /save/i }));
  await waitFor(() => expect(apiMocks.updateTransaction).toHaveBeenCalledTimes(1));
  expect(apiMocks.updateTransaction.mock.calls[0][1]).toEqual({ notes: "milk" });
  expect(apiMocks.updateTransaction.mock.calls[0][2]).toMatchObject({ base: baseTransaction });
});
```

  Match the file's existing render shape and label casing (`"Notes"`, `"Amount"`); it already imports `screen`, `waitFor` and the api mock module.

In `Transactions.test.tsx`, add `expect.objectContaining({ base: expect.objectContaining({ id: "t1" }) })` as the third argument to the two inline-edit assertions at ~365 and ~383.

- [ ] **Step 2: Run it to verify it fails**

Run: `bun run test -- src/components/Transactions`
Expected: FAIL — no `base` is passed.

- [ ] **Step 3: Pass the base from both call sites**

`EditTransactionModal` passes `{ base: transaction }` — the row it opened with, already in scope as the `transaction` prop. `Transactions.tsx` passes `{ base: txn }` at both inline sites. Add one comment at each explaining why: a PATCH carrying the whole row silently reverts whatever another writer changed in a field the user never touched.

- [ ] **Step 4: Run it to verify it passes**

Run: `bun run test -- src/components/Transactions`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add frontend/src/components/Transactions/EditTransactionModal.tsx \
  frontend/src/components/Transactions/Transactions.tsx \
  frontend/src/components/Transactions/EditTransactionModal.test.tsx \
  frontend/src/components/Transactions/Transactions.test.tsx
git commit -m "fix(transactions): an online edit PATCHes only what the user changed"
```

---

### Task 12: The context — counts, resolving, re-creating

**Files:**
- Modify: `frontend/src/context/OfflineContext.tsx`
- Test: `frontend/src/context/OfflineContext.test.tsx`

**Interfaces:**
- Consumes: `discardConflicts`, `resolveConflict`, `recordConflict` (Task 5), `readTheirs` (Task 6).
- Produces: `OfflineContextValue` gains
  ```ts
  conflicts: QueuedWrite[];   // entries carrying a `conflict` or a `gone`
  resolveConflict: (key: string, resolution: Record<string, "mine" | "theirs">) => void;
  reCreate: (key: string) => Promise<void>;
  discardConflicts: () => void;
  ```
  and `sync`'s toast copy gains one line for conflicts and one for gone rows.

- [ ] **Step 1: Write the failing tests**

```ts
it("reports conflicts separately from rejections", () => {
  // A provider over a queue holding one conflicted and one errored entry
  // exposes both counts, so the banner can say which is which.
  expect(result.current.conflicts).toHaveLength(1);
});

it("marks synced only when something was actually written", async () => {
  // A held conflict wrote nothing, so nothing revalidates against the server.
  expect(markSynced).not.toHaveBeenCalled();
});

it("re-creates a gone row through the idempotent create path, and reports the new id", async () => {
  // Assert api.createTransaction was called with a clientKey, that markSynced
  // fired, that the entry left the queue, and that the toast names the new id —
  // the row that comes back is not the row that was edited.
});
```

- [ ] **Step 2: Run it to verify it fails**

Run: `bun run test -- src/context/OfflineContext.test.tsx`
Expected: FAIL.

- [ ] **Step 3: Wire the counts, the reader, and the actions**

Derive `conflicts` from `pending` with a `useMemo`. Most importantly, pass the reader into the flush — without it `theirs` is `undefined` and the merge is skipped entirely, so no conflict would ever be detected:

```ts
const outcome = await flushOutbox(userId, send, { ...options, theirs: readTheirs });
```

`readTheirs` is `registry.ts`'s `(op, rowId) => OPS[op].read(rowId)`, so the live read lives in one place and the "never from the offline cache" rule has exactly one implementation to get wrong.

`resolveConflict` calls the queue mutator and toasts its error on a `false` return, matching how `discardFailed` already reports. `reCreate` looks the entry up, calls `OPS[op].reCreate(snapshot, patch)`, removes the entry, calls `markSynced`, and toasts the new id — because a new row is a change to the server's copy of the ledger, and the row that comes back is not the row the user was editing.

- [ ] **Step 4: Set `syncedAt` correctly**

`markSynced` fires when `outcome.sent > 0` **or** `outcome.recreated > 0`, and not for `conflicted` or `gone` alone. The existing comment about why `sent` is counted where the server accepted it applies unchanged.

- [ ] **Step 5: Run it to verify it passes**

Run: `bun run test -- src/context/OfflineContext.test.tsx`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add frontend/src/context/OfflineContext.tsx frontend/src/context/OfflineContext.test.tsx
git commit -m "feat(offline): the context counts conflicts and re-creates a gone row"
```

---

### Task 13: The conflict dialog

**Files:**
- Create: `frontend/src/components/Offline/ConflictDialog.tsx`
- Test: `frontend/src/components/Offline/ConflictDialog.test.tsx`
- Modify: `frontend/src/App.tsx` (mount beside `CommandPalette`, ~line 132)

**Interfaces:**
- Consumes: `conflicts` (Task 12). Actions arrive as props, not from the context, so the dialog is testable without a provider.
- Produces:
  ```ts
  export interface ConflictDialogProps {
    open: boolean;
    onOpenChange: (open: boolean) => void;
    entries: QueuedWrite[];
    onResolve: (key: string, resolution: Record<string, "mine" | "theirs">) => void;
    onReCreate: (key: string) => void;
    onDiscard: (key: string) => void;
  }
  export default function ConflictDialog(props: ConflictDialogProps): JSX.Element | null;
  ```

  The dialog takes **every** held entry, not one. Several can be conflicted at once and nothing selects between them, so the dialog renders one section per entry.

- [ ] **Step 1: Write the failing interaction tests**

```ts
it("shows both values and the base for each conflicting field", () => {
  render(<ConflictDialog {...props} entry={conflictedEdit()} />);
  // one row per unit, with the field name, mine, theirs and base all rendered
  expect(screen.getByText("Notes")).toBeInTheDocument();
  expect(screen.getByText("mine")).toBeInTheDocument();
  expect(screen.getByText("theirs")).toBeInTheDocument();
  expect(screen.getByText("a")).toBeInTheDocument();
});

it("resolves per field and reports the whole resolution", async () => {
  const onResolve = vi.fn();
  render(<ConflictDialog {...props} onResolve={onResolve} entry={twoFieldConflict()} />);
  await userEvent.click(screen.getByRole("button", { name: /keep theirs/i }));  // notes
  await userEvent.click(screen.getByRole("button", { name: /keep mine/i }));     // amount
  await userEvent.click(screen.getByRole("button", { name: /^save$/i }));
  expect(onResolve).toHaveBeenCalledWith("key-1", { notes: "theirs", amount: "mine" });
});

it("offers re-create for a gone row, and discard behind a confirm", async () => {
  const onReCreate = vi.fn();
  const onDiscard = vi.fn();
  render(<ConflictDialog {...props} entry={goneEdit()} onReCreate={onReCreate} onDiscard={onDiscard} />);
  await userEvent.click(screen.getByRole("button", { name: /re-create/i }));
  expect(onReCreate).toHaveBeenCalledWith("key-1");
  // Discard is destructive, so it is behind an AlertDialog like the banner's.
  await userEvent.click(screen.getByRole("button", { name: /^discard$/i }));
  expect(onDiscard).not.toHaveBeenCalled();
  await userEvent.click(screen.getByRole("button", { name: /^discard$/i }));  // the confirm
  expect(onDiscard).toHaveBeenCalledWith("key-1");
});
```

  Build the fixtures (`conflictedEdit`, `twoFieldConflict`, `goneEdit`) as small local helpers in the test file that return a `QueuedWrite` with the `conflict` / `gone` fields set; do not import from the queue module, whose storage the test does not want.

- [ ] **Step 2: Run it to verify it fails**

Run: `bun run test -- src/components/Offline`
Expected: FAIL — component does not exist.

- [ ] **Step 3: Implement the dialog**

A `Dialog` listing one section per held entry, and within it one row per
`ConflictUnit`: the field, your value, theirs, and the base that makes the conflict
legible. Keep-mine / keep-theirs per field, with keep-all-mine /
keep-all-theirs in the header. A `gone` entry shows the row's description and amount
with Re-create (primary) and Discard (destructive, behind an `AlertDialog`) — the
same shape `OfflineBanner` already uses for discarding rejected entries.

A `FieldConflict.base` is `undefined` for the no-baseline case (the user set a field
the base never held), so the base cell must render an em-dash rather than the string
"undefined", and that row is the one case where "keep theirs" discards an edit the
user made — which is why the engine holds it instead of resolving it.

- [ ] **Step 4: Mount it in `App.tsx`**

Beside `<CommandPalette …>` at ~line 132, inside `OfflineProvider` so it can read the context, opened by a callback `OfflineBanner` supplies. Follow the repo's overlay rule: the parent owns the open state and passes `onOpenChange`.

- [ ] **Step 5: Run it to verify it passes**

Run: `bun run test -- src/components/Offline src/App.test.tsx`
Expected: PASS. If a Radix Select or Dialog interaction fails with a stuck trigger, check the `jsdom ~30.0.1` pin before changing anything.

- [ ] **Step 6: Commit**

```bash
git add frontend/src/components/Offline/ConflictDialog.tsx \
  frontend/src/components/Offline/ConflictDialog.test.tsx frontend/src/App.tsx
git commit -m "feat(offline): resolve a held conflict field by field"
```

---

### Task 14: The banner's third counter

**Files:**
- Modify: `frontend/src/components/Layout/OfflineBanner.tsx`
- Test: `frontend/src/components/Layout/OfflineBanner.test.tsx`

**Interfaces:**
- Consumes: `conflicts` and the dialog's open callback (Tasks 12–13).
- Produces: no new exports.

- [ ] **Step 1: Write the failing test**

```ts
it("shows a conflict count and a Resolve button, and neither when there are none", () => {
  // A queue with one conflicted entry renders "1 needs your attention" and a
  // Resolve button; a queue with none renders neither.
});
```

- [ ] **Step 2: Run it to verify it fails**

Run: `bun run test -- src/components/Layout/OfflineBanner.test.tsx`
Expected: FAIL.

- [ ] **Step 3: Add the counter and the button**

A third `<span>` beside waiting and rejected, in `text-destructive` with a `TriangleAlert`, and a `Button` that opens the dialog. Keep the existing `waiting` / `failed` maths and the Sync now / Retry / Discard block exactly as they are. The `role="status"` container and the `return null` guard must account for the new count.

- [ ] **Step 4: Run it to verify it passes**

Run: `bun run test -- src/components/Layout/OfflineBanner.test.tsx`
Expected: PASS, with the 8 pre-existing cases unchanged.

- [ ] **Step 5: Commit**

```bash
git add frontend/src/components/Layout/OfflineBanner.tsx \
  frontend/src/components/Layout/OfflineBanner.test.tsx
git commit -m "feat(offline): the banner counts what needs the user's attention"
```

---

### Task 15: Docs and the issue

**Files:**
- Modify: `AGENTS.md` (the frontend section's offline-layer bullet)
- Modify: `docs/feature-proposals.md` (proposal #11)

**Interfaces:** none.

- [ ] **Step 1: Update `AGENTS.md`**

The existing bullet describes the outbox as queuing "manual creates" and says the banner is the only place the layer speaks to the user. Rewrite it to say the queue holds creates, edits and bulk writes; that an edit is stored as a field-level diff against a base and merged three ways on flush; that a field both sides changed is held for the user; that a gone row offers re-create through the idempotent create path; and that the apply shape is declared per op because the handlers differ, citing `account.go`'s `COALESCE(NULLIF(...))` against `payee.go`'s unconditional write. Add `id:` to the query-language notes if that section enumerates fields.

- [ ] **Step 2: Mark proposal #11 implemented**

In `docs/feature-proposals.md`, add a line under `#11 — Offline conflict resolution` pointing at the spec and the plan, and stating the two things left out on purpose: `/tags/rename` and every delete.

- [ ] **Step 3: Run the whole gate**

Run, in order: `bun run test`, `bun run typecheck`, `bun run build`, `go test ./...`, `make vet`, `make test-cover-check`, `make openapi-check`, `make docs-check`.
Expected: all pass. The coverage floors are the ones that bite here — `make test-cover-check` enforces 85% on the backend and the new `id` branch must be covered by the corpus case.

- [ ] **Step 4: Commit**

```bash
git add AGENTS.md docs/feature-proposals.md
git commit -m "docs: the offline layer queues edits, not just creates"
```

---

## Execution Order

Tasks 1 → 2 → 3 → 4 → 5 are strictly sequential and each ends green. After Task 5 the backend is done and the frontend engine is done, so Tasks 6–10 are independent of each other and can be dispatched in parallel. Task 11 depends only on Task 7. Tasks 12 → 13 → 14 are sequential. Task 15 is last.

A useful stopping point for review: **after Task 7** the feature is end-to-end for transactions, which is the case the issue is actually about. Tasks 8–10 extend the same machinery to the remaining families and are the most mechanical part of the plan.
