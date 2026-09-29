import { describe, it, expect, beforeEach, vi } from "vitest";
import type { CreateTransactionRequest } from "../types";
import { ApiError, NetworkError } from "./errors";
import type { FieldPatch } from "./merge";
import {
  discardFailed,
  enqueueBulk,
  enqueueCreate,
  enqueueEdit,
  flushOutbox,
  getOutboxSnapshot,
  OutboxStorageError,
  queuedPatchFor,
  queuedRowProjection,
  removeEntry,
  subscribeOutbox,
  type QueuedWrite,
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
    expect(outcome).toEqual({ sent: 2, remaining: 0, failed: 0, unsaved: 0 });
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
    expect(outcome).toEqual({ sent: 1, remaining: 2, failed: 0, unsaved: 1 });
  });

  it("counts the entries the server accepted, not the queue delta", async () => {
    enqueueCreate(USER, request("First"), "key-1");

    const outcome = await flushOutbox(USER, async () => {
      // The user keeps recording while the flush is in flight; the entry that
      // arrives mid-flush was not sent by it, so it must not cancel out the one
      // that was (which is what a queue-length delta does).
      enqueueCreate(USER, request("Second"), "key-2");
    });

    expect(outcome).toEqual({ sent: 1, remaining: 1, failed: 0, unsaved: 0 });
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
    expect(outcome).toEqual({ sent: 0, remaining: 2, failed: 0, unsaved: 0 });
  });

  it("stops on a session or server failure without blaming the entry", async () => {
    enqueueCreate(USER, request(), "key-1");

    const outcome = await flushOutbox(USER, async () => {
      throw new ApiError("session expired", 401);
    });

    expect(outcome).toEqual({ sent: 0, remaining: 1, failed: 0, unsaved: 0 });
    expect(getOutboxSnapshot(USER)[0].error).toBeUndefined();
  });

  it("marks a rejected entry with the server's message and continues", async () => {
    enqueueCreate(USER, request("First"), "key-1");
    enqueueCreate(USER, request("Second"), "key-2");

    const outcome = await flushOutbox(USER, async (entry) => {
      if (entry.key === "key-1") throw new ApiError("account is closed", 409);
    });

    expect(outcome).toEqual({ sent: 1, remaining: 1, failed: 1, unsaved: 0 });
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
});
