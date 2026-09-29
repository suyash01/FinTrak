import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { ApiError, NetworkError } from "../api/errors";
import {
  enqueueBulk,
  enqueueCreate,
  enqueueEdit,
  getOutboxSnapshot,
} from "../api/outbox";
import { projectTransaction } from "../api/projections";
import { markSynced } from "../api/offlineStatus";
import type { Transaction } from "../types";
import { OfflineProvider, useOffline } from "./OfflineContext";

// The registry reaches the server only through client.ts, so mocking that module
// is also the statement of which calls the flush is allowed to make. It carries
// more than `createTransaction` now that `send` dispatches: the two the
// transaction op's read and apply make are here, so a test that drives an edit
// end to end exercises the real registry rather than a stand-in for it.
const apiMock = vi.hoisted(() => ({
  createTransaction: vi.fn(),
  getTransactions: vi.fn(),
  updateTransaction: vi.fn(),
  bulkCategorize: vi.fn(),
}));
const toastMock = vi.hoisted(() => ({ success: vi.fn(), error: vi.fn() }));

vi.mock("../api/client", () => ({ default: apiMock }));
vi.mock("sonner", () => ({ toast: toastMock }));
vi.mock("./AuthContext", () => ({
  useAuth: () => ({ user: { id: "u1", email: "a@b.c" } }),
}));

const create = {
  accountId: "acct-1",
  date: "2024-01-15",
  description: "Coffee",
  amount: 250.5,
  type: "debit" as const,
};

// The row the server holds, which the op registry's live read answers with.
// `description` is the field the queued edits below are about; `amount` is a
// field nobody queued a write for, which is what tells a patch apart from the
// merge's whole-row output.
function serverRow(description = "Coffee", amount = 250.5): Transaction {
  return {
    id: "txn-1",
    accountId: "acct-1",
    date: "2024-01-15",
    description,
    amount,
    type: "debit",
  };
}

// The queue entry a transaction edit makes in production: the base and the
// snapshot are the same projection of the row the form opened with (client.ts
// passes it twice), and the snapshot is the whole row a re-create rebuilds the
// transaction from.
function queueEdit() {
  const row = projectTransaction(serverRow());
  return enqueueEdit("u1", "transaction.patch", "txn-1", row, { description: "Tea" }, row);
}

// jsdom never changes navigator.onLine on its own, so a test drives both the
// flag and the event the browser would fire with it.
function setOnline(online: boolean) {
  Object.defineProperty(window.navigator, "onLine", {
    configurable: true,
    value: online,
  });
  window.dispatchEvent(new Event(online ? "online" : "offline"));
}

function Probe() {
  const {
    online,
    pending,
    conflicts,
    syncing,
    syncedAt,
    sync,
    discardFailed,
    discardConflicts,
    resolveConflict,
    reCreate,
  } = useOffline();
  // The harness reads the conflict surface the way a dialog would, and the
  // optional accesses are what keep a case that asserts on it failing *there*:
  // an unwired field must not take the seven create cases down with a render
  // error before any of them has run.
  //
  // The actions take a key, so the harness hands them the first held entry's —
  // the dialog that calls them for real passes one entry at a time.
  const held = conflicts?.[0]?.key ?? "";
  return (
    <div>
      <span data-testid="online">{String(online)}</span>
      <span data-testid="pending">{pending.length}</span>
      <span data-testid="failed">
        {pending.filter((entry) => entry.error !== undefined).length}
      </span>
      <span data-testid="conflicts">{conflicts?.length ?? 0}</span>
      <span data-testid="syncing">{String(syncing)}</span>
      <span data-testid="synced">{syncedAt > 0 ? "yes" : "no"}</span>
      <button onClick={() => void sync()}>sync</button>
      <button onClick={discardFailed}>discard</button>
      <button onClick={() => discardConflicts?.()}>discard conflicts</button>
      <button
        onClick={() => resolveConflict?.(held, { description: "mine" })}
      >
        resolve
      </button>
      <button onClick={() => void reCreate?.(held)}>re-create</button>
    </div>
  );
}

function renderProvider() {
  return render(
    <OfflineProvider>
      <Probe />
    </OfflineProvider>,
  );
}

describe("OfflineProvider", () => {
  beforeEach(() => {
    localStorage.clear();
    setOnline(true);
    // syncedAt lives in the offline store, which outlives a single case.
    markSynced(0);
    apiMock.createTransaction.mockReset();
    apiMock.getTransactions.mockReset();
    apiMock.updateTransaction.mockReset();
    apiMock.bulkCategorize.mockReset();
    toastMock.success.mockReset();
    toastMock.error.mockReset();
  });

  afterEach(() => {
    setOnline(true);
  });

  it("sends a queue left over from a previous session", async () => {
    enqueueCreate("u1", create, "key-1");
    apiMock.createTransaction.mockResolvedValue({ id: "txn-1", queued: false });

    renderProvider();

    await waitFor(() => expect(screen.getByTestId("pending")).toHaveTextContent("0"));
    expect(apiMock.createTransaction).toHaveBeenCalledWith(
      expect.objectContaining({ description: "Coffee" }),
      { idempotencyKey: "key-1", queue: false },
    );
    expect(screen.getByTestId("synced")).toHaveTextContent("yes");
    expect(toastMock.success).toHaveBeenCalledWith(
      "Synced 1 offline transaction",
    );
  });

  it("keeps the queue when the send never reached the server", async () => {
    enqueueCreate("u1", create, "key-1");
    apiMock.createTransaction.mockRejectedValue(new NetworkError());

    renderProvider();

    await waitFor(() => expect(apiMock.createTransaction).toHaveBeenCalled());
    expect(screen.getByTestId("pending")).toHaveTextContent("1");
    expect(screen.getByTestId("synced")).toHaveTextContent("no");
    expect(toastMock.error).not.toHaveBeenCalled();
  });

  it("marks a rejected entry and reports why", async () => {
    enqueueCreate("u1", create, "key-1");
    apiMock.createTransaction.mockRejectedValue(
      new ApiError("account is closed", 409),
    );

    renderProvider();

    await waitFor(() => expect(screen.getByTestId("failed")).toHaveTextContent("1"));
    expect(screen.getByTestId("pending")).toHaveTextContent("1");
    expect(toastMock.error).toHaveBeenCalledWith(
      expect.stringContaining("account is closed"),
    );
  });

  it("sends nothing while offline and flushes when the connection returns", async () => {
    setOnline(false);
    enqueueCreate("u1", create, "key-1");
    apiMock.createTransaction.mockResolvedValue({ id: "txn-1", queued: false });

    renderProvider();
    await waitFor(() =>
      expect(screen.getByTestId("online")).toHaveTextContent("false"),
    );
    expect(apiMock.createTransaction).not.toHaveBeenCalled();

    setOnline(true);

    await waitFor(() => expect(screen.getByTestId("pending")).toHaveTextContent("0"));
    expect(apiMock.createTransaction).toHaveBeenCalledTimes(1);
  });

  it("drops rejected entries when the user discards them", async () => {
    const user = userEvent.setup();
    enqueueCreate("u1", create, "key-1");
    apiMock.createTransaction.mockRejectedValue(new ApiError("rejected", 400));

    renderProvider();
    await waitFor(() => expect(screen.getByTestId("failed")).toHaveTextContent("1"));

    await user.click(screen.getByText("discard"));

    await waitFor(() => expect(screen.getByTestId("pending")).toHaveTextContent("0"));
    expect(toastMock.success).toHaveBeenCalledWith(
      "Discarded 1 offline transaction",
    );
  });

  it("says so when the queue cannot be updated after a sync", async () => {
    enqueueCreate("u1", create, "key-1");
    apiMock.createTransaction.mockResolvedValue({ id: "txn-1", queued: false });
    // The server accepts the entry but the browser refuses to write the queue
    // back: without this the pending count would simply never drop.
    const setItem = vi
      .spyOn(Storage.prototype, "setItem")
      .mockImplementation(() => {
        throw new Error("QuotaExceededError");
      });

    renderProvider();

    await waitFor(() =>
      expect(toastMock.error).toHaveBeenCalledWith(
        expect.stringContaining("Could not update the offline queue"),
      ),
    );
    setItem.mockRestore();
    expect(screen.getByTestId("pending")).toHaveTextContent("1");
  });

  it("reports a discard the browser refused to persist", async () => {
    const user = userEvent.setup();
    enqueueCreate("u1", create, "key-1");
    apiMock.createTransaction.mockRejectedValue(new ApiError("rejected", 400));

    renderProvider();
    await waitFor(() => expect(screen.getByTestId("failed")).toHaveTextContent("1"));

    const setItem = vi
      .spyOn(Storage.prototype, "setItem")
      .mockImplementation(() => {
        throw new Error("QuotaExceededError");
      });
    await user.click(screen.getByText("discard"));
    setItem.mockRestore();

    expect(toastMock.error).toHaveBeenCalledWith(
      expect.stringContaining("Could not write the offline queue"),
    );
    expect(toastMock.success).not.toHaveBeenCalled();
    expect(screen.getByTestId("pending")).toHaveTextContent("1");
  });

  it("sends a queued edit as a field-level patch and removes the entry", async () => {
    // The whole path in one case, and the one this file exists to pin: an edit
    // recorded with no connection, read back on reconnect, merged against the
    // server's row, and applied. It fails if the flush is not given a reader
    // (planWrite throws and the merge never runs), if `send` still refuses
    // anything that is not a create, and if the entry is dropped rather than
    // removed once the server has it.
    queueEdit();
    // The merge's own answer would echo every field of the row, so a body
    // carrying `amount` is the merge's output rather than the user's patch —
    // and sending that would revert a change the user never saw.
    apiMock.getTransactions.mockResolvedValue({
      data: [serverRow("Coffee", 999)],
    });
    apiMock.updateTransaction.mockResolvedValue({ id: "txn-1", queued: false });

    renderProvider();

    await waitFor(() => expect(screen.getByTestId("pending")).toHaveTextContent("0"));
    expect(apiMock.updateTransaction).toHaveBeenCalledWith(
      "txn-1",
      { description: "Tea" },
      { queue: false },
    );
    expect(screen.getByTestId("synced")).toHaveTextContent("yes");
  });

  it("sends a bulk write as one request over the rows that did not conflict", async () => {
    // The third kind of queued write, and the one the counter gets wrong if it
    // is read as rows: one entry, one request, however many rows it names. The
    // server moved `txn-2`'s category and not `txn-1`'s, so the merge holds one
    // row and sends the other — a batch that dropped the held row silently is
    // the failure this feature exists to prevent.
    enqueueBulk(
      "u1",
      "transaction.categorize",
      "categoryId",
      "cat-2",
      ["txn-1", "txn-2"],
      { "txn-1": "cat-0", "txn-2": "cat-0" },
    );
    apiMock.getTransactions.mockImplementation(({ q }: { q?: string }) =>
      Promise.resolve({
        data: [
          {
            ...serverRow(q === "id:txn-2" ? "Someone else's coffee" : "Coffee"),
            // cat-0 is the base, so txn-1's row is untouched and merges clean;
            // txn-2's is somewhere else entirely, which is a conflict on the
            // field this write is about.
            categoryId: q === "id:txn-2" ? "cat-9" : "cat-0",
          },
        ],
      }),
    );
    apiMock.bulkCategorize.mockResolvedValue({ updated: 1 });

    renderProvider();

    await waitFor(() =>
      expect(screen.getByTestId("conflicts")).toHaveTextContent("1"),
    );
    // Only the clean row goes out. The held one keeps the entry queued, so the
    // count stays at 1 rather than draining.
    expect(apiMock.bulkCategorize).toHaveBeenCalledWith({
      transactionIds: ["txn-1"],
      categoryId: "cat-2",
    });
    expect(screen.getByTestId("pending")).toHaveTextContent("1");
  });

  it("reports conflicts separately from rejections", async () => {
    // Two entries one flush could not resolve, and the user has to be able to
    // tell them apart: a rejected create is the server's answer about a payload,
    // while a conflict is a question only the user can answer. Both stay queued,
    // so the count the banner shows for one must not include the other.
    enqueueCreate("u1", create, "key-1");
    queueEdit();
    apiMock.createTransaction.mockRejectedValue(
      new ApiError("account is closed", 409),
    );
    // The server moved the very field the user edited, so the merge holds it
    // rather than choosing a side.
    apiMock.getTransactions.mockResolvedValue({
      data: [serverRow("Someone else's coffee")],
    });

    renderProvider();

    await waitFor(() =>
      expect(screen.getByTestId("conflicts")).toHaveTextContent("1"),
    );
    expect(screen.getByTestId("failed")).toHaveTextContent("1");
    expect(screen.getByTestId("pending")).toHaveTextContent("2");
    // A held entry is not sent either: the question is the user's, not ours.
    expect(apiMock.updateTransaction).not.toHaveBeenCalled();
    expect(toastMock.error).toHaveBeenCalledWith(
      expect.stringContaining("needs your decision"),
    );
  });

  it("marks synced only when something was actually written", async () => {
    // A held conflict wrote nothing, so there is nothing for a page showing
    // ledger data to revalidate: the rows on screen are the rows the queue
    // already holds, and syncedAt must say so rather than claim a sync.
    queueEdit();
    apiMock.getTransactions.mockResolvedValue({
      data: [serverRow("Someone else's coffee")],
    });

    renderProvider();

    await waitFor(() =>
      expect(screen.getByTestId("conflicts")).toHaveTextContent("1"),
    );
    expect(screen.getByTestId("synced")).toHaveTextContent("no");
    expect(apiMock.updateTransaction).not.toHaveBeenCalled();
  });

  it("re-creates a gone row through the idempotent create path, and reports the new id", async () => {
    const user = userEvent.setup();
    queueEdit();
    // No such row any more: the read answers null and the entry is held as gone.
    apiMock.getTransactions.mockResolvedValue({ data: [] });
    apiMock.createTransaction.mockResolvedValue({ id: "txn-9", queued: false });

    renderProvider();
    await waitFor(() =>
      expect(screen.getByTestId("conflicts")).toHaveTextContent("1"),
    );

    await user.click(screen.getByText("re-create"));

    await waitFor(() => expect(screen.getByTestId("pending")).toHaveTextContent("0"));
    // The body is the row the user was editing with their decided change on it,
    // which is why a re-created row is that row and not a stub — and the
    // idempotency key is the entry's own, so a re-create whose response was lost
    // is recognised on the next attempt instead of inserting a second money row.
    expect(apiMock.createTransaction).toHaveBeenCalledTimes(1);
    expect(apiMock.createTransaction).toHaveBeenCalledWith(
      expect.objectContaining({ description: "Tea", accountId: "acct-1" }),
      { idempotencyKey: expect.stringMatching(/^e-/), queue: false },
    );
    expect(screen.getByTestId("synced")).toHaveTextContent("yes");
    // The row that comes back is not the row the user was editing, so the new id
    // is named: a user still holding the old row needs to know where their
    // change went.
    expect(toastMock.success).toHaveBeenCalledWith(
      expect.stringContaining("txn-9"),
    );
  });

  it("keeps a gone row queued when the re-create never reached the server", async () => {
    const user = userEvent.setup();
    queueEdit();
    apiMock.getTransactions.mockResolvedValue({ data: [] });
    apiMock.createTransaction.mockRejectedValue(new NetworkError());

    renderProvider();
    await waitFor(() =>
      expect(screen.getByTestId("conflicts")).toHaveTextContent("1"),
    );

    await user.click(screen.getByText("re-create"));

    // Nothing reached the server, so the entry stays held and re-creating again
    // is the right answer rather than a lost edit.
    await waitFor(() =>
      expect(toastMock.error).toHaveBeenCalledWith(expect.any(String)),
    );
    expect(screen.getByTestId("pending")).toHaveTextContent("1");
    expect(screen.getByTestId("conflicts")).toHaveTextContent("1");
  });

  it("records a resolution and drops the entry from the held list", async () => {
    const user = userEvent.setup();
    queueEdit();
    apiMock.getTransactions.mockResolvedValue({
      data: [serverRow("Someone else's coffee")],
    });

    renderProvider();
    await waitFor(() =>
      expect(screen.getByTestId("conflicts")).toHaveTextContent("1"),
    );

    await user.click(screen.getByText("resolve"));

    await waitFor(() =>
      expect(screen.getByTestId("conflicts")).toHaveTextContent("0"),
    );
    // The answer is on the entry and the entry is still queued: the decision is
    // not the send, so a flush that has not happened yet leaves it to be sent.
    const [entry] = getOutboxSnapshot("u1");
    expect(entry.conflict).toBeUndefined();
    expect(entry.resolution).toEqual({ description: "mine" });
    expect(screen.getByTestId("pending")).toHaveTextContent("1");
  });

  it("says so when a resolution cannot be stored", async () => {
    const user = userEvent.setup();
    queueEdit();
    apiMock.getTransactions.mockResolvedValue({
      data: [serverRow("Someone else's coffee")],
    });

    renderProvider();
    await waitFor(() =>
      expect(screen.getByTestId("conflicts")).toHaveTextContent("1"),
    );

    const setItem = vi
      .spyOn(Storage.prototype, "setItem")
      .mockImplementation(() => {
        throw new Error("QuotaExceededError");
      });
    await user.click(screen.getByText("resolve"));
    setItem.mockRestore();

    expect(toastMock.error).toHaveBeenCalledWith(
      expect.stringContaining("Could not record your answer"),
    );
    // Still held, because nothing was recorded: a dialog that closed over a
    // decision the queue never learned about is the silent discard this feature
    // exists to prevent.
    expect(screen.getByTestId("conflicts")).toHaveTextContent("1");
    expect(getOutboxSnapshot("u1")[0].resolution).toBeUndefined();
  });

  it("discards the held entries and keeps the rejected one", async () => {
    const user = userEvent.setup();
    enqueueCreate("u1", create, "key-1");
    queueEdit();
    apiMock.createTransaction.mockRejectedValue(new ApiError("rejected", 400));
    apiMock.getTransactions.mockResolvedValue({
      data: [serverRow("Someone else's coffee")],
    });

    renderProvider();
    await waitFor(() =>
      expect(screen.getByTestId("conflicts")).toHaveTextContent("1"),
    );

    await user.click(screen.getByText("discard conflicts"));

    await waitFor(() =>
      expect(screen.getByTestId("conflicts")).toHaveTextContent("0"),
    );
    // The rejected entry is not this one's business: two discards, two
    // questions, and dropping a held edit behind a discard of rejections is how
    // a user's change disappears without anybody being asked.
    expect(screen.getByTestId("failed")).toHaveTextContent("1");
    expect(screen.getByTestId("pending")).toHaveTextContent("1");
    expect(toastMock.success).toHaveBeenCalledWith(
      "Discarded 1 offline change",
    );
  });
});
