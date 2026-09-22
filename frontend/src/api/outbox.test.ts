import { describe, it, expect, beforeEach, vi } from "vitest";
import type { CreateTransactionRequest } from "../types";
import { ApiError, NetworkError } from "./errors";
import {
  discardFailed,
  enqueueCreate,
  flushOutbox,
  getOutboxSnapshot,
  removeEntry,
  subscribeOutbox,
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
    expect(entries[0].request.description).toBe("Coffee");
    expect(listener).toHaveBeenCalledTimes(1);
    unsubscribe();
  });

  it("keeps one entry per key, so a retry cannot duplicate a queue entry", () => {
    enqueueCreate(USER, request(), "key-1");
    enqueueCreate(USER, request("Lunch"), "key-1");

    const entries = getOutboxSnapshot(USER);
    expect(entries).toHaveLength(1);
    expect(entries[0].request.description).toBe("Coffee");
  });

  it("scopes the queue to its user", () => {
    enqueueCreate(USER, request(), "key-1");
    expect(getOutboxSnapshot("other-user")).toHaveLength(0);
  });

  it("keeps the newest entries when the cap is reached", () => {
    for (let i = 1; i <= 105; i++) {
      enqueueCreate(USER, request(`Entry ${i}`), `key-${i}`);
    }

    const entries = getOutboxSnapshot(USER);
    expect(entries).toHaveLength(100);
    expect(entries[0].key).toBe("key-6");
    expect(entries[99].key).toBe("key-105");
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
    expect(outcome).toEqual({ sent: 2, remaining: 0, failed: 0 });
    expect(getOutboxSnapshot(USER)).toHaveLength(0);
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
    expect(outcome).toEqual({ sent: 0, remaining: 2, failed: 0 });
  });

  it("stops on a session or server failure without blaming the entry", async () => {
    enqueueCreate(USER, request(), "key-1");

    const outcome = await flushOutbox(USER, async () => {
      throw new ApiError("session expired", 401);
    });

    expect(outcome).toEqual({ sent: 0, remaining: 1, failed: 0 });
    expect(getOutboxSnapshot(USER)[0].error).toBeUndefined();
  });

  it("marks a rejected entry with the server's message and continues", async () => {
    enqueueCreate(USER, request("First"), "key-1");
    enqueueCreate(USER, request("Second"), "key-2");

    const outcome = await flushOutbox(USER, async (entry) => {
      if (entry.key === "key-1") throw new ApiError("account is closed", 409);
    });

    expect(outcome).toEqual({ sent: 1, remaining: 1, failed: 1 });
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
});
