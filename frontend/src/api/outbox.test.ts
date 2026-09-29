import { describe, it, expect, beforeEach, vi } from "vitest";
import type { CreateTransactionRequest } from "../types";
import { ApiError, NetworkError } from "./errors";
import { mergeFields, type FieldPatch } from "./merge";
import {
  discardConflicts,
  discardFailed,
  enqueueBulk,
  enqueueCreate,
  enqueueEdit,
  flushOutbox,
  getOutboxSnapshot,
  OutboxStorageError,
  queuedPatchFor,
  queuedRowProjection,
  recordConflict,
  removeEntry,
  resolveConflict,
  subscribeOutbox,
  type QueuedWrite,
  type TheirsReader,
} from "./outbox";

const USER = "user-1";

function request(description = "Coffee"): CreateTransactionRequest {
  return {
    accountId: "acct-1",
    date: "2024-01-15",
    description,
    amount: 250.5,
    type: "debit",
  };
}

describe("outbox", () => {
  beforeEach(() => localStorage.clear());

  it("records a create and notifies subscribers", () => {
    const listener = vi.fn();
    const unsubscribe = subscribeOutbox(listener);

    enqueueCreate(USER, request(), "key-1");

    const entries = getOutboxSnapshot(USER);
    expect(entries).toHaveLength(1);
    expect(entries[0].key).toBe("key-1");
    // Narrowed because the queue is a union now. The assertion is unchanged; what
    // changed is that reading `request` off it is only legal for the arm that
    // carries one, and this entry was written by enqueueCreate.
    const entry = entries[0];
    if (entry.kind !== "create") throw new Error("enqueueCreate queued a non-create");
    expect(entry.request.description).toBe("Coffee");
    expect(listener).toHaveBeenCalledTimes(1);
    unsubscribe();
  });

  it("keeps one entry per key, so a retry cannot duplicate a queue entry", () => {
    enqueueCreate(USER, request(), "key-1");
    enqueueCreate(USER, request("Lunch"), "key-1");

    const entries = getOutboxSnapshot(USER);
    expect(entries).toHaveLength(1);
    const entry = entries[0];
    if (entry.kind !== "create") throw new Error("enqueueCreate queued a non-create");
    expect(entry.request.description).toBe("Coffee");
  });

  it("scopes the queue to its user", () => {
    enqueueCreate(USER, request(), "key-1");
    expect(getOutboxSnapshot("other-user")).toHaveLength(0);
  });

  // The cap used to evict the oldest entries silently (`.slice(-MAX_ENTRIES)`),
  // which destroyed unsent transactions the user had recorded. The contract now
  // is that a full queue refuses the new entry: losing the oldest entry to make
  // room for a new one is data loss the user is never told about, while a
  // refusal reaches the create as its error and says what to do about it.
  it("refuses a new entry when the queue is full instead of dropping the oldest", () => {
    for (let i = 1; i <= 100; i++) {
      enqueueCreate(USER, request(`Entry ${i}`), `key-${i}`);
    }

    expect(() => enqueueCreate(USER, request("Entry 101"), "key-101")).toThrow(
      /queue is full/i,
    );

    // Every entry that was queued is still there, in order, and the refused one
    // was not half-written.
    const entries = getOutboxSnapshot(USER);
    expect(entries).toHaveLength(100);
    expect(entries[0].key).toBe("key-1");
    expect(entries[99].key).toBe("key-100");
  });

  it("still returns the queued entry when a retry reuses its key", () => {
    enqueueCreate(USER, request(), "key-1");
    // A replay of a key that is already queued is not a new entry, so the cap
    // cannot turn it into a refusal.
    expect(enqueueCreate(USER, request("Lunch"), "key-1").key).toBe("key-1");
    expect(getOutboxSnapshot(USER)).toHaveLength(1);
  });

  // The stored text is the only copy of the queue (getOutboxSnapshot re-reads
  // it), so a refused write has to reach the caller: reporting `queued: true`
  // for an entry that is not stored tells the user a transaction is saved when
  // nothing will ever flush it.
  it("throws instead of claiming a queue the browser refused to write", () => {
    const setItem = vi
      .spyOn(Storage.prototype, "setItem")
      .mockImplementation(() => {
        throw new Error("QuotaExceededError");
      });

    expect(() => enqueueCreate(USER, request(), "key-1")).toThrow(
      OutboxStorageError,
    );
    setItem.mockRestore();

    expect(getOutboxSnapshot(USER)).toHaveLength(0);
  });

  it("reports a removal the browser refused to persist", () => {
    enqueueCreate(USER, request(), "key-1");
    const setItem = vi
      .spyOn(Storage.prototype, "setItem")
      .mockImplementation(() => {
        throw new Error("QuotaExceededError");
      });

    const removed = removeEntry(USER, "key-1");
    setItem.mockRestore();

    expect(removed).toBe(false);
    expect(getOutboxSnapshot(USER)).toHaveLength(1);
  });

  it("throws rather than reporting a discard that did not happen", async () => {
    enqueueCreate(USER, request(), "key-1");
    await flushOutbox(USER, async () => {
      throw new ApiError("rejected", 400);
    });

    const setItem = vi
      .spyOn(Storage.prototype, "setItem")
      .mockImplementation(() => {
        throw new Error("QuotaExceededError");
      });

    expect(() => discardFailed(USER)).toThrow(OutboxStorageError);
    setItem.mockRestore();

    expect(getOutboxSnapshot(USER)).toHaveLength(1);
  });

  it("returns a stable snapshot between changes", () => {
    enqueueCreate(USER, request(), "key-1");
    expect(getOutboxSnapshot(USER)).toBe(getOutboxSnapshot(USER));

    removeEntry(USER, "key-1");
    expect(getOutboxSnapshot(USER)).toHaveLength(0);
  });

  it("sends queued entries in the order they were recorded", async () => {
    enqueueCreate(USER, request("First"), "key-1");
    enqueueCreate(USER, request("Second"), "key-2");
    const sent: string[] = [];

    const outcome = await flushOutbox(USER, async (entry) => {
      sent.push(entry.key);
    });

    expect(sent).toEqual(["key-1", "key-2"]);
    // The expected object names every field the outcome carries, and the flush
    // reports the edit counters on every run whether or not an edit was in the
    // queue: a caller (the offline banner) reads them without narrowing first,
    // so an outcome that omitted them would report "no conflicts" by not saying.
    expect(outcome).toEqual({
      sent: 2,
      remaining: 0,
      failed: 0,
      unsaved: 0,
      conflicted: 0,
      gone: 0,
      recreated: 0,
    });
    expect(getOutboxSnapshot(USER)).toHaveLength(0);
  });

  it("reports an accepted entry whose removal the browser refused to store", async () => {
    enqueueCreate(USER, request("First"), "key-1");
    enqueueCreate(USER, request("Second"), "key-2");

    const setItem = vi
      .spyOn(Storage.prototype, "setItem")
      .mockImplementation(() => {
        throw new Error("QuotaExceededError");
      });
    const sent: string[] = [];
    const outcome = await flushOutbox(USER, async (entry) => {
      sent.push(entry.key);
    });
    setItem.mockRestore();

    // key-1 reached the server, but the queue it has to be removed from cannot
    // be written: the flush stops there and reports it. Claiming an empty queue
    // would hide that the entry is replayed on every reconnect.
    expect(sent).toEqual(["key-1"]);
    expect(outcome).toEqual({ sent: 1, remaining: 2, failed: 0, unsaved: 1, conflicted: 0, gone: 0, recreated: 0 });
  });

  it("counts the entries the server accepted, not the queue delta", async () => {
    enqueueCreate(USER, request("First"), "key-1");

    const outcome = await flushOutbox(USER, async () => {
      // The user keeps recording while the flush is in flight; the entry that
      // arrives mid-flush was not sent by it, so it must not cancel out the one
      // that was (which is what a queue-length delta does).
      enqueueCreate(USER, request("Second"), "key-2");
    });

    expect(outcome).toEqual({ sent: 1, remaining: 1, failed: 0, unsaved: 0, conflicted: 0, gone: 0, recreated: 0 });
  });

  it("keeps the queue in order when the server is unreachable", async () => {
    enqueueCreate(USER, request("First"), "key-1");
    enqueueCreate(USER, request("Second"), "key-2");
    const attempted: string[] = [];

    const outcome = await flushOutbox(USER, async (entry) => {
      attempted.push(entry.key);
      throw new NetworkError();
    });

    // The first failure stops the flush: the second entry was never attempted,
    // so nothing is sent out of order once the connection returns.
    expect(attempted).toEqual(["key-1"]);
    expect(outcome).toEqual({ sent: 0, remaining: 2, failed: 0, unsaved: 0, conflicted: 0, gone: 0, recreated: 0 });
  });

  it("stops on a session or server failure without blaming the entry", async () => {
    enqueueCreate(USER, request(), "key-1");

    const outcome = await flushOutbox(USER, async () => {
      throw new ApiError("session expired", 401);
    });

    expect(outcome).toEqual({ sent: 0, remaining: 1, failed: 0, unsaved: 0, conflicted: 0, gone: 0, recreated: 0 });
    expect(getOutboxSnapshot(USER)[0].error).toBeUndefined();
  });

  it("marks a rejected entry with the server's message and continues", async () => {
    enqueueCreate(USER, request("First"), "key-1");
    enqueueCreate(USER, request("Second"), "key-2");

    const outcome = await flushOutbox(USER, async (entry) => {
      if (entry.key === "key-1") throw new ApiError("account is closed", 409);
    });

    expect(outcome).toEqual({ sent: 1, remaining: 1, failed: 1, unsaved: 0, conflicted: 0, gone: 0, recreated: 0 });
    const remaining = getOutboxSnapshot(USER);
    expect(remaining[0].key).toBe("key-1");
    expect(remaining[0].error).toBe("account is closed");
  });

  it("skips a rejected entry until a retry is asked for", async () => {
    enqueueCreate(USER, request("First"), "key-1");
    enqueueCreate(USER, request("Second"), "key-2");
    await flushOutbox(USER, async (entry) => {
      if (entry.key === "key-1") throw new ApiError("rejected", 400);
    });

    const send = vi.fn();
    await flushOutbox(USER, send);
    expect(send).not.toHaveBeenCalled();

    await flushOutbox(USER, send, { retryFailed: true });
    expect(send).toHaveBeenCalledTimes(1);
    expect(getOutboxSnapshot(USER)).toHaveLength(0);
  });

  it("discards only the entries the server rejected", async () => {
    enqueueCreate(USER, request("First"), "key-1");
    enqueueCreate(USER, request("Second"), "key-2");
    await flushOutbox(USER, async (entry) => {
      if (entry.key === "key-1") throw new ApiError("rejected", 400);
    });

    expect(discardFailed(USER)).toBe(1);
    expect(getOutboxSnapshot(USER)).toHaveLength(0);
  });

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
    // `kind` is asserted, not defaulted here: a reader that wrote nothing would
    // satisfy `entries[0].kind ?? "create"`, which is the behaviour this pins.
    const entry = entries[0];
    expect(entry.kind).toBe("create");
    // Narrowed rather than cast, so the request is read the way the flush reads
    // it — a defaulted kind that narrowed to nothing else would pass the line
    // above while leaving an entry the flush cannot send.
    if (entry.kind !== "create") throw new Error("not adopted as a create");
    expect(entry.request.description).toBe("Legacy");
  });

  // Widening the entry type opened a path this flush cannot take yet, and
  // quietly leaving such an entry queued is the one failure mode this module
  // exists to avoid: the queue is the source of truth, so a write that cannot be
  // sent has to say so rather than sit there looking like progress. It reads as
  // a programming gap rather than a rejection — nothing catches this but a
  // developer, which is the point.
  it("refuses to flush a non-create entry instead of leaving it queued silently", async () => {
    const edit: QueuedWrite = {
      kind: "edit",
      key: "key-1",
      queuedAt: 1,
      op: "transaction.patch",
      rowId: "row-1",
      base: { notes: "a" },
      patch: { notes: "mine" },
      snapshot: { notes: "a" },
    };
    localStorage.setItem(`fintrak_outbox:v1:${USER}`, JSON.stringify([edit]));

    await expect(flushOutbox(USER, async () => {})).rejects.toThrow(/edit/);

    // Refusing is not discarding: the entry is still the user's.
    expect(getOutboxSnapshot(USER)).toHaveLength(1);
  });

  // A byte budget beside the entry cap: a bulk entry carries N base values, and
  // size rather than count is what fills the ~5MB quota. Refuse, never evict.
  //
  // An edit stores the long string twice — once as the patch, once as the
  // snapshot — so each entry below is ~180KB, not the ~90KB the string suggests.
  // That is a little under a fifth of the budget: five entries fit, the sixth
  // does not, and the 100-entry cap is nowhere in sight.
  it("refuses a new entry when the byte cap is reached, keeping every entry queued", () => {
    const fat: FieldPatch = { notes: "x".repeat(90_000) };
    // Five ~180KB entries exhaust the 1MB budget; the sixth is refused.
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
  // sees a phantom conflict on the field entry 1 changed. Two entries and not one
  // is the other half of it: a key derived from the row would collapse them and
  // lose the first.
  it("bases a second edit to the same row on the locally projected row", () => {
    enqueueEdit(USER, "transaction.patch", "row-1", { notes: "" }, { notes: "first" }, { notes: "" });
    expect(queuedRowProjection(USER, "row-1")).toEqual({ notes: "first" });

    enqueueEdit(USER, "transaction.patch", "row-1", { notes: "" }, { notes: "second" }, { notes: "" });

    const edits = getOutboxSnapshot(USER).filter((e) => e.kind === "edit");
    expect(edits).toHaveLength(2);
    expect(edits[1].base).toEqual({ notes: "first" });
  });

  it("reports the effective queued value for a field, or undefined when untouched", () => {
    expect(queuedPatchFor(USER, "row-1", "notes")).toBeUndefined();
    enqueueEdit(USER, "transaction.patch", "row-1", { notes: "" }, { notes: "x" }, { notes: "" });
    expect(queuedPatchFor(USER, "row-1", "notes")).toBe("x");
  });

  // A bulk write records the base of every row it touches rather than one base
  // for the batch, and it travels with the op's non-row identifier: a tag add
  // needs nothing else, an attach to a recurring series needs the series, and
  // the queue is the only place any of that survives a reload.
  it("records a bulk write with the base of each row and the op's identifier", () => {
    const entry = enqueueBulk(
      USER,
      "transaction.tags",
      "tags",
      ["groceries"],
      ["row-1", "row-2"],
      { "row-1": ["food"], "row-2": [] },
    );

    expect(entry.key).toBeTruthy();
    expect(entry.value).toEqual(["groceries"]);
    expect(entry.bases).toEqual({ "row-1": ["food"], "row-2": [] });
    expect(getOutboxSnapshot(USER)).toHaveLength(1);

    const attached = enqueueBulk(
      USER,
      "transaction.recurring",
      "recurringTransactionId",
      "txn-1",
      ["row-3"],
      { "row-3": null },
      { seriesId: "series-1" },
    );
    expect(attached.seriesId).toBe("series-1");
  });

  // A bulk write is a queued write to a row like any other: one field set on two
  // hundred rows has to move the base of an edit to one of those rows, because the
  // bulk entry's field is the same field of the same row and the two ops only
  // differ in how they travel.
  it("folds a queued bulk write into the base of a later edit to the same row", () => {
    enqueueBulk(USER, "transaction.categorize", "categoryId", "c1", ["r1", "r2"], { r1: null, r2: null });
    // r1 was null before the bulk write and will be "c1" after it.
    enqueueEdit(USER, "transaction.patch", "r1", { categoryId: null, notes: "old" }, { notes: "new" }, { categoryId: null, notes: "old" });

    const edits = getOutboxSnapshot(USER).filter((e) => e.kind === "edit");
    // The stored base must be what the bulk write will produce, not the pre-bulk row.
    expect(edits[0].base.categoryId).toBe("c1");
    // And the merge must not then report a conflict the user never caused.
    const result = mergeFields(edits[0].base, edits[0].patch, { categoryId: "c1", notes: "old" });
    expect(result.conflicts).toEqual([]);

    // The pending value is read per row, because that is what a write has: the bulk
    // categorize and a single patch both set this field of this row, so they have
    // one answer between them. r2 was never edited, so only the bulk write has
    // anything to say about it.
    expect(queuedPatchFor(USER, "r1", "categoryId")).toBe("c1");
    expect(queuedPatchFor(USER, "r2", "categoryId")).toBe("c1");
  });

  // The conflict this projection prevents is on a field the user *did* change
  // after the bulk write. A form opened on the pre-bulk row bases the edit on the
  // old category, so a base that does not carry the queued bulk write reads as a
  // third party's change to a field the user is settling themselves — held in front
  // of them to decide, over a value they wrote.
  it("does not report a conflict on a field changed after a queued bulk write", () => {
    enqueueBulk(USER, "transaction.categorize", "categoryId", "c1", ["r1"], { r1: "c0" });
    enqueueEdit(USER, "transaction.patch", "r1", { categoryId: "c0" }, { categoryId: "c2" }, { categoryId: "c0" });

    const edits = getOutboxSnapshot(USER).filter((e) => e.kind === "edit");
    // "theirs" is what the bulk write left on the server: c1.
    const result = mergeFields(edits[0].base, edits[0].patch, { categoryId: "c1" });

    expect(result.conflicts).toEqual([]);
    expect(result.patch).toEqual({ categoryId: "c2" });
  });

  // The queue drains in the order it was recorded, so the projection has to fold
  // the writes in that order: a bulk write is a writer among the edits, and a pass
  // of its own would put the bulk's value in the base of every edit — including
  // the ones the user made after it, which are based on the edit before them.
  it("applies a queued bulk write in queue order, not in a pass of its own", () => {
    enqueueBulk(USER, "transaction.patch", "notes", "bulk", ["r1"], { r1: "old" });
    enqueueEdit(USER, "transaction.patch", "r1", { notes: "old" }, { notes: "first" }, { notes: "old" });
    enqueueEdit(USER, "transaction.patch", "r1", { notes: "old" }, { notes: "second" }, { notes: "old" });

    // The last writer of the field is the edit queued last.
    expect(queuedPatchFor(USER, "r1", "notes")).toBe("second");
    const edits = getOutboxSnapshot(USER).filter((e) => e.kind === "edit");
    // The first edit is based on the bulk write, and the second on the first edit:
    // a bulk write overlaid last would report "bulk" for both.
    expect(edits[0].base.notes).toBe("bulk");
    expect(edits[1].base.notes).toBe("first");
  });

  // A queued edit is a write to a row, not to a field of a form, so what advances
  // the next edit's base is every write queued for the row — whatever op it went
  // through. transaction.patch, .payee, .categorize, .tags, .billingCycle, .loan,
  // .recurring and .loanDisbursement all address the same transaction row, and two
  // edits to one transaction through two of them are one row's history: a base that
  // could not see the first would hold a conflict on the field the user edited.
  it("advances the base across queued edits made through different ops", () => {
    enqueueEdit(USER, "transaction.payee", "r1", { payeeId: "p0" }, { payeeId: "p1" }, { payeeId: "p0" });
    enqueueEdit(USER, "transaction.patch", "r1", { payeeId: "p0" }, { notes: "n" }, { payeeId: "p0" });
    enqueueEdit(USER, "transaction.tags", "r1", { payeeId: "p0" }, { tags: ["t"] }, { payeeId: "p0" });

    const edits = getOutboxSnapshot(USER).filter((e) => e.kind === "edit");
    expect(edits[0].base.payeeId).toBe("p0");
    expect(edits[1].base.payeeId).toBe("p1");
    expect(edits[2].base.payeeId).toBe("p1");
  });

  // The row the form opened on is the server's row *as of that moment*, and a
  // queued edit does not stop the server moving on: a flush that stopped at a
  // 401/5xx keeps its entries while somebody else edits the row, and the next read
  // brings the newer row back. The base has to start from that newer row, because
  // the earlier edit's own base is a snapshot of a moment that has been superseded
  // — and a field nobody queued a write for is one the base must not regress.
  it("keeps a re-read server row as the base of an edit queued after an earlier one", () => {
    enqueueEdit(USER, "transaction.patch", "r1", { notes: "n", amount: 100 }, { notes: "n1" }, { notes: "n", amount: 100 });
    // The row is read again while that entry is still queued: the server took the
    // notes, and somebody else moved the amount to 105.
    enqueueEdit(USER, "transaction.patch", "r1", { notes: "n1", amount: 105 }, { amount: 110 }, { notes: "n1", amount: 105 });

    const edits = getOutboxSnapshot(USER).filter((e) => e.kind === "edit");
    // notes comes from the queued write; amount comes from the row just re-read. The
    // first entry's base still says 100, and that snapshot must not win.
    expect(edits[1].base).toEqual({ notes: "n1", amount: 105 });
  });

  // The conflict this pins is the one a stale snapshot manufactures: the engine
  // reads theirs.amount=105 against a base of 100, sees a third party, and holds a
  // field the user has just set — with no third party in it. The assertion has to be
  // on amount, which is in both the patch and the base; a field absent from the
  // patch is attributed to nobody and would report no conflict either way.
  it("does not conflict on a field the server changed while an edit was queued", () => {
    enqueueEdit(USER, "transaction.patch", "r1", { notes: "n", amount: 100 }, { notes: "n1" }, { notes: "n", amount: 100 });
    enqueueEdit(USER, "transaction.patch", "r1", { notes: "n1", amount: 105 }, { amount: 110 }, { notes: "n1", amount: 105 });

    const edits = getOutboxSnapshot(USER).filter((e) => e.kind === "edit");
    const result = mergeFields(edits[1].base, edits[1].patch, { notes: "n1", amount: 105 });

    expect(result.conflicts).toEqual([]);
    expect(result.patch).toEqual({ notes: "n1", amount: 110 });
  });

  // The stored text is the only copy of the queue, so a refused write has to reach
  // the caller on the new paths too: an edit reported as queued that is stored
  // nowhere is a change the user believes is saved and that will never be sent.
  it("throws rather than claiming a queued edit the browser refused to write", () => {
    const setItem = vi
      .spyOn(Storage.prototype, "setItem")
      .mockImplementation(() => {
        throw new Error("QuotaExceededError");
      });

    expect(() =>
      enqueueEdit(USER, "transaction.patch", "row-1", { notes: "" }, { notes: "x" }, { notes: "" }),
    ).toThrow(OutboxStorageError);
    setItem.mockRestore();

    expect(getOutboxSnapshot(USER)).toHaveLength(0);
  });

  it("throws rather than claiming a queued bulk write the browser refused to write", () => {
    const setItem = vi
      .spyOn(Storage.prototype, "setItem")
      .mockImplementation(() => {
        throw new Error("QuotaExceededError");
      });

    expect(() =>
      enqueueBulk(USER, "transaction.tags", "tags", ["t"], ["row-1"], { "row-1": [] }),
    ).toThrow(OutboxStorageError);
    setItem.mockRestore();

    expect(getOutboxSnapshot(USER)).toHaveLength(0);
  });

  // A held conflict is a question, not a failure, so it must not stop the flush
  // the way a rejected or unreachable send does: the entry behind it is a write
  // the user recorded and can still make. The merge decides this before anything
  // is sent, so the conflicted row is never put on the wire.
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
    // The key is the one enqueueEdit minted. An edit is stored under a generated
    // key, so resolving a literal "key-1" would find no entry, and the decision
    // this test is about would never be recorded.
    const entry = enqueueEdit(USER, "transaction.patch", "row-1", { notes: "a" }, { notes: "mine" }, { notes: "a" });
    await flushOutbox(USER, async () => {}, { theirs: async () => ({ notes: "theirs" }) });
    resolveConflict(USER, entry.key, { notes: "mine" });
    // The answer replaces the question: an entry whose fields are all decided is
    // not a held conflict, and a record left on it would let discardConflicts
    // destroy a write the user has resolved but not yet sent.
    expect(getOutboxSnapshot(USER)[0].conflict).toBeUndefined();

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

  // discardFailed answers the question "the server rejected this"; a held entry
  // has no rejection, so it must survive it, and only the conflict-aware discard
  // may drop it. The two are separate buttons because they answer different ones.
  it("never lets discardFailed drop a held conflict", () => {
    const entry = enqueueEdit(USER, "transaction.patch", "row-1", { notes: "a" }, { notes: "mine" }, { notes: "a" });
    recordConflict(USER, entry.key, {
      units: [{ rowId: "row-1", field: "notes", base: "a", mine: "mine", theirs: "theirs" }],
    });
    expect(discardFailed(USER)).toBe(0);
    expect(getOutboxSnapshot(USER)).toHaveLength(1);
    expect(discardConflicts(USER)).toBe(1);
    expect(getOutboxSnapshot(USER)).toHaveLength(0);
  });

  // The merge walks the union of all three key sets, so its patch echoes every
  // field of the row. Sending that would revert a third party's change to
  // `amount` over an edit that never touched it — the exact bug this merge
  // exists to prevent — so the queued patch goes out verbatim and the merge's
  // job is only to confirm that nothing conflicts.
  it("sends the queued patch, not the whole row the merge produced", async () => {
    enqueueEdit(USER, "transaction.patch", "r1", { notes: "a", amount: 100 }, { notes: "mine" }, { notes: "a", amount: 100 });
    const sent: FieldPatch[] = [];

    await flushOutbox(USER, async (entry) => {
      if (entry.kind === "edit") sent.push(entry.patch);
    }, { theirs: async () => ({ notes: "a", amount: 105 }) });

    expect(sent).toEqual([{ notes: "mine" }]);
  });

  // A bulk is one request, but its rows can end in three states at once. The
  // clean ones go out, the rest are recorded, and the entry stays queued — the
  // user asked for all of them, and dropping the held ones with the batch is the
  // failure this feature exists to prevent. `send` sees only the rows that may
  // be applied, which is the same seam the production path dispatches through.
  it("sends the clean rows of a bulk and holds only the conflicted ones", async () => {
    enqueueBulk(USER, "transaction.categorize", "categoryId", "c2", ["r1", "r2"], { r1: "c0", r2: "c0" });
    const sent: string[][] = [];

    const outcome = await flushOutbox(USER, async (entry) => {
      if (entry.kind === "bulk") sent.push(entry.rows);
    }, { theirs: async (_op, rowId) => ({ categoryId: rowId === "r1" ? "c9" : "c0" }) });

    // r2's base still matches the server's row, so it is the user's write alone;
    // r1 has moved on and is held for the user to decide.
    expect(sent).toEqual([["r2"]]);
    expect(outcome).toEqual({
      sent: 1, remaining: 1, failed: 0, unsaved: 0, conflicted: 1, gone: 0, recreated: 0,
    });
    const left = getOutboxSnapshot(USER);
    expect(left).toHaveLength(1);
    expect(left[0].conflict?.units[0]).toMatchObject({
      rowId: "r1", field: "categoryId", mine: "c2", theirs: "c9",
    });
  });

  // A row the server no longer has is reported, not held: there is nothing to
  // decide about a row that is not there, and stranding the batch behind it
  // would block the writes the user can still make.
  it("sends the rows of a bulk whose server row still exists, and counts the one that does not", async () => {
    enqueueBulk(USER, "transaction.categorize", "categoryId", "c2", ["r1", "r2"], { r1: "c0", r2: "c0" });
    const sent: string[][] = [];

    const outcome = await flushOutbox(USER, async (entry) => {
      if (entry.kind === "bulk") sent.push(entry.rows);
    }, { theirs: async (_op, rowId) => (rowId === "r2" ? null : { categoryId: "c0" }) });

    expect(sent).toEqual([["r1"]]);
    expect(outcome).toEqual({
      sent: 1, remaining: 0, failed: 0, unsaved: 0, conflicted: 0, gone: 1, recreated: 0,
    });
    expect(getOutboxSnapshot(USER)).toHaveLength(0);
  });

  // The read of the server's row is part of the flush, not a lookup beside it: a
  // connection that dies mid-flush leaves the queue exactly where a failed send
  // would, in order, for the next reconnect.
  it("stops the flush, in order, when reading the server's row fails", async () => {
    enqueueEdit(USER, "transaction.patch", "r1", { notes: "a" }, { notes: "mine" }, { notes: "a" });
    enqueueEdit(USER, "transaction.patch", "r2", { notes: "a" }, { notes: "mine" }, { notes: "a" });
    const attempted: string[] = [];

    const outcome = await flushOutbox(USER, async (entry) => {
      if (entry.kind === "edit") attempted.push(entry.rowId);
    }, { theirs: async (_op, rowId) => {
      if (rowId === "r1") throw new NetworkError();
      return { notes: "a" };
    } });

    expect(attempted).toEqual([]);
    expect(outcome).toEqual({
      sent: 0, remaining: 2, failed: 0, unsaved: 0, conflicted: 0, gone: 0, recreated: 0,
    });
  });

  // Treating a broken reader as "offline" would report a flush that drained
  // nothing and looks like a network blip that never clears, with nothing in the
  // log to say why. A transport failure is the only read failure the flush can
  // act on; anything else is a bug and stays loud.
  it("lets a broken theirs reader fail loudly rather than read as offline", async () => {
    enqueueEdit(USER, "transaction.patch", "r1", { notes: "a" }, { notes: "mine" }, { notes: "a" });

    await expect(flushOutbox(USER, async () => {}, {
      theirs: async () => {
        throw new TypeError("reader is broken");
      },
    })).rejects.toThrow(/reader is broken/);

    expect(getOutboxSnapshot(USER)).toHaveLength(1);
  });

  // A held entry is the user's own edit, so reporting it as discarded while it
  // is still queued is the one thing this must not do — the same reason
  // discardFailed throws.
  it("throws rather than reporting a discarded conflict that did not happen", () => {
    const entry = enqueueEdit(USER, "transaction.patch", "row-1", { notes: "a" }, { notes: "mine" }, { notes: "a" });
    recordConflict(USER, entry.key, {
      units: [{ rowId: "row-1", field: "notes", base: "a", mine: "mine", theirs: "theirs" }],
    });
    const setItem = vi
      .spyOn(Storage.prototype, "setItem")
      .mockImplementation(() => {
        throw new Error("QuotaExceededError");
      });

    expect(() => discardConflicts(USER)).toThrow(OutboxStorageError);
    setItem.mockRestore();

    expect(getOutboxSnapshot(USER)).toHaveLength(1);
  });

  // A resolution names only the fields that conflicted, so it is an override of
  // the queued patch and never a replacement for it: the fields that merged clean
  // were always going to be sent, and a decided entry that dropped them would
  // remove the user's change to a field they were never asked about and then
  // report the entry as synced.
  it("keeps the fields that merged clean when only one of them conflicted", async () => {
    const entry = enqueueEdit(
      USER,
      "transaction.patch",
      "r1",
      { notes: "a", categoryId: "c0" },
      { notes: "mine", categoryId: "c2" },
      { notes: "a", categoryId: "c0" },
    );
    // The server moved the notes and nothing else, so the only unit a dialog can
    // produce is the notes.
    await flushOutbox(USER, async () => {}, {
      theirs: async () => ({ notes: "theirs", categoryId: "c0" }),
    });
    expect(getOutboxSnapshot(USER)[0].conflict?.units).toHaveLength(1);
    resolveConflict(USER, entry.key, { notes: "mine" });

    const sent: FieldPatch[] = [];
    const outcome = await flushOutbox(USER, async (e) => {
      if (e.kind === "edit") sent.push(e.patch);
    }, { theirs: async () => ({ notes: "theirs", categoryId: "c0" }) });

    expect(sent).toEqual([{ notes: "mine", categoryId: "c2" }]);
    expect(outcome).toEqual({
      sent: 1, remaining: 0, failed: 0, unsaved: 0, conflicted: 0, gone: 0, recreated: 0,
    });
  });

  // "theirs" is written by not naming the field: a patch that carried the queued
  // value for a field the user chose to leave alone would re-assert the very value
  // they just declined to write.
  it("drops a field the user decided to leave to the server", async () => {
    const entry = enqueueEdit(
      USER,
      "transaction.patch",
      "r1",
      { notes: "a", categoryId: "c0" },
      { notes: "mine", categoryId: "c2" },
      { notes: "a", categoryId: "c0" },
    );
    await flushOutbox(USER, async () => {}, {
      theirs: async () => ({ notes: "theirs", categoryId: "c0" }),
    });
    resolveConflict(USER, entry.key, { notes: "theirs" });

    const sent: FieldPatch[] = [];
    await flushOutbox(USER, async (e) => {
      if (e.kind === "edit") sent.push(e.patch);
    }, { theirs: async () => ({ notes: "theirs", categoryId: "c0" }) });

    expect(sent).toEqual([{ categoryId: "c2" }]);
  });

  // Every field answered "theirs" leaves a decided patch with no fields in it,
  // and the bulk path already reads an empty batch as "remove without a request".
  // An edit that dispatched an empty patch would put a request on the wire that
  // had nothing to do and count the entry as sent for it.
  it("sends nothing for a decided edit the user left entirely to the server", async () => {
    const entry = enqueueEdit(
      USER,
      "transaction.patch",
      "r1",
      { notes: "a", categoryId: "c0" },
      { notes: "mine", categoryId: "c2" },
      { notes: "a", categoryId: "c0" },
    );
    await flushOutbox(USER, async () => {}, {
      theirs: async () => ({ notes: "theirs", categoryId: "c0" }),
    });
    resolveConflict(USER, entry.key, { notes: "theirs", categoryId: "theirs" });

    const sent: FieldPatch[] = [];
    const outcome = await flushOutbox(USER, async (e) => {
      if (e.kind === "edit") sent.push(e.patch);
    }, { theirs: async () => ({ notes: "theirs", categoryId: "theirs-cat" }) });

    expect(sent).toEqual([]);
    // The question is answered, so the entry drains rather than being held: there
    // is nothing left to ask about and nothing left to write.
    expect(getOutboxSnapshot(USER)).toHaveLength(0);
    expect(outcome).toEqual({
      sent: 0, remaining: 0, failed: 0, unsaved: 0, conflicted: 0, gone: 0, recreated: 0,
    });
  });

  // The reader is written over api.get*, which throws ApiError for a 500. A row
  // the server will not serve is not a row to skip past, and it is not a
  // rejection of the entry either — it stops the flush with the queue in order,
  // exactly as a 500 from a send would, and records nothing on the entry.
  it("stops the flush, in order, when reading the row fails with a server error", async () => {
    enqueueEdit(USER, "transaction.patch", "r1", { notes: "a" }, { notes: "mine" }, { notes: "a" });
    enqueueEdit(USER, "transaction.patch", "r2", { notes: "a" }, { notes: "mine" }, { notes: "a" });
    const attempted: string[] = [];

    const outcome = await flushOutbox(USER, async (entry) => {
      if (entry.kind === "edit") attempted.push(entry.rowId);
    }, { theirs: async () => {
      throw new ApiError("server error", 500);
    } });

    expect(attempted).toEqual([]);
    expect(outcome).toEqual({
      sent: 0, remaining: 2, failed: 0, unsaved: 0, conflicted: 0, gone: 0, recreated: 0,
    });
    expect(getOutboxSnapshot(USER)[0].error).toBeUndefined();
  });

  // A dispatch wired wrong is a bug. A flush that stops quietly reports it as a
  // queue that did not drain, which is what an offline moment looks like: the
  // user is told nothing and the cause is nowhere.
  it("lets a broken send fail loudly rather than read as an offline moment", async () => {
    enqueueCreate(USER, request(), "key-1");

    await expect(flushOutbox(USER, async () => {
      throw new TypeError("dispatch is broken");
    })).rejects.toThrow(/dispatch is broken/);
  });

  // The whole-row family used to be refused here outright, on the grounds that a
  // field-level patch is the wrong payload for an endpoint that writes every
  // column its body can carry. The op registry now builds that payload — applyOp
  // overlays the patch on the row the reader returned — so what this flush owes
  // the family is the entry, and the entry is what it sends.
  it("sends a putWhole edit, which the family-wide refusal used to block", async () => {
    enqueueEdit(USER, "payee.put", "p1", { name: "old", accountId: "a1" }, { name: "new" }, { name: "old", accountId: "a1" });
    const sent: FieldPatch[] = [];

    const outcome = await flushOutbox(USER, async (entry) => {
      if (entry.kind === "edit") sent.push(entry.patch);
    }, { theirs: async () => ({ name: "new", accountId: "a1" }) });

    // The patch, not the row: what goes out is the user's change, and the row it
    // is overlaid onto is the reader's, reached through the op registry rather
    // than from here (see registry.ts's mergedRow, which registry.test.ts drives
    // end to end).
    expect(sent).toEqual([{ name: "new" }]);
    expect(outcome).toMatchObject({ sent: 1, remaining: 0, failed: 0 });
    expect(getOutboxSnapshot(USER)).toHaveLength(0);
  });

  // A gone row is a fact about the row whatever its shape, and the refusal used to
  // sit in front of the decision that records it. This is the reason the refusal
  // had to go rather than be narrowed: a payee deleted on another device is not
  // an entry to be thrown at, it is one the user has to be told about.
  it("records a putWhole edit whose row the server no longer has, rather than refusing it", async () => {
    enqueueEdit(USER, "payee.put", "p1", { name: "old", accountId: "a1" }, { name: "new" }, { name: "old", accountId: "a1" });

    const outcome = await flushOutbox(USER, async () => {
      throw new Error("nothing may be sent for a row that is gone");
    }, { theirs: async () => null });

    expect(outcome).toMatchObject({ sent: 0, gone: 1, remaining: 1 });
    expect(getOutboxSnapshot(USER)[0].gone).toBe(true);
  });

  // The other half of what the `.put` suffix used to catch by accident: the six
  // putPartial ops, whose endpoints leave an omitted key alone and which are
  // perfectly happy with a diff. They never needed a refusal, and a family-wide
  // one was refusing their edits for a reason that was not theirs.
  it("sends a putPartial op, which the suffix used to refuse as well", async () => {
    enqueueEdit(USER, "account.put", "acct-1", { name: "old", color: "#fff" }, { name: "new" }, { name: "old", color: "#fff" });
    const sent: FieldPatch[] = [];

    const outcome = await flushOutbox(USER, async (entry) => {
      if (entry.kind === "edit") sent.push(entry.patch);
    }, { theirs: async () => ({ name: "old", color: "#000" }) });

    expect(sent).toEqual([{ name: "new" }]);
    expect(outcome).toMatchObject({ sent: 1, remaining: 0, failed: 0 });
    expect(getOutboxSnapshot(USER)).toHaveLength(0);
  });

  // The heart of it: one request, three states. The clean row goes out, the
  // conflicted one is held, the gone one is counted, and the entry stays queued
  // because the user asked for all three and the held row is theirs to answer
  // for. A reader wired for the easy cases would drop one of the three.
  it("splits a bulk into clean, conflicted and gone rows in one flush", async () => {
    enqueueBulk(USER, "transaction.categorize", "categoryId", "c2", ["r1", "r2", "r3"], { r1: "c0", r2: "c0", r3: "c0" });
    const sent: string[][] = [];

    const outcome = await flushOutbox(USER, async (entry) => {
      if (entry.kind === "bulk") sent.push(entry.rows);
    }, { theirs: async (_op, rowId) => {
      if (rowId === "r3") return null;
      return { categoryId: rowId === "r2" ? "c9" : "c0" };
    } });

    expect(sent).toEqual([["r1"]]);
    expect(outcome).toEqual({
      sent: 1, remaining: 1, failed: 0, unsaved: 0, conflicted: 1, gone: 1, recreated: 0,
    });
    const left = getOutboxSnapshot(USER);
    expect(left).toHaveLength(1);
    expect(left[0].conflict?.units[0]).toMatchObject({ rowId: "r2", field: "categoryId" });
  });
});
