import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { ApiError, NetworkError } from "../api/errors";
import {
  enqueueBulk,
  enqueueCreate,
  enqueueEdit,
  getOutboxSnapshot,
  recordConflict,
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
  getAccounts: vi.fn(),
  updateAccount: vi.fn(),
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
//
// In act() because the offline store is a subscriber to that event: without it
// a case that flips connectivity while a provider is mounted re-renders it
// outside React's knowledge and the suite reports the noise as a warning.
function setOnline(online: boolean) {
  act(() => {
    Object.defineProperty(window.navigator, "onLine", {
      configurable: true,
      value: online,
    });
    window.dispatchEvent(new Event(online ? "online" : "offline"));
  });
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
    discardConflict,
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
      <button onClick={() => discardConflict?.(held)}>discard conflict</button>
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
    apiMock.getAccounts.mockReset();
    apiMock.updateAccount.mockReset();
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
    expect(toastMock.success).toHaveBeenCalledWith("Synced 1 offline write");
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
    expect(toastMock.success).toHaveBeenCalledWith("Discarded 1 offline write");
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

  it("refuses to re-create a row that is only conflicted, not gone", async () => {
    const user = userEvent.setup();
    queueEdit();
    // The row is still there — that is what a conflict means: the field moved,
    // the transaction did not. A create here would insert a second money row and
    // report the user's conflict resolved, which is the worst answer available.
    apiMock.getTransactions.mockResolvedValue({
      data: [serverRow("Someone else's coffee")],
    });

    renderProvider();
    await waitFor(() =>
      expect(screen.getByTestId("conflicts")).toHaveTextContent("1"),
    );

    await user.click(screen.getByText("re-create"));

    // The dialog is expected to hide the action, and it does — but it is a
    // public method on a context value, and the precondition belongs to the seam
    // that owns the effect, not to whichever surface happens to call it.
    await waitFor(() => expect(toastMock.error).toHaveBeenCalled());
    expect(apiMock.createTransaction).not.toHaveBeenCalled();
    expect(screen.getByTestId("pending")).toHaveTextContent("1");
    expect(screen.getByTestId("conflicts")).toHaveTextContent("1");
    expect(screen.getByTestId("synced")).toHaveTextContent("no");
  });

  it("tells the user when a write this seam cannot make stops the sync", async () => {
    // A bulk entry against an op with no batch endpoint. Nothing here can be
    // sent, and saying so loudly is right — skipping it would remove the entry
    // and report a batch that never left the browser — but `sync` is called as
    // `void sync()` from both the reconnect effect and the banner, so a throw
    // with no catch is an unhandled rejection and no message at all. The user
    // would be left watching a pending count that never drops.
    enqueueBulk("u1", "account.put", "name", "Renamed", ["a1"], { a1: "Old" });
    apiMock.getAccounts.mockResolvedValue([{ id: "a1", name: "Old" }]);

    renderProvider();

    await waitFor(() =>
      expect(toastMock.error).toHaveBeenCalledWith(
        expect.stringContaining("no batch endpoint"),
      ),
    );
    // Refused before the wire, so no request emptied the row, and the entry is
    // still queued for a build that can send it.
    expect(apiMock.updateAccount).not.toHaveBeenCalled();
    expect(screen.getByTestId("pending")).toHaveTextContent("1");
    expect(screen.getByTestId("syncing")).toHaveTextContent("false");
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

  it("sends a resolution the moment the user gives it, without waiting for a reconnect", async () => {
    // The user has just answered a question in a dialog on a connected device,
    // so the answer is not a note to be sent later. The entry behind it is
    // queued and nothing else would flush it: the reconnect effect keys on the
    // queue's length, and recording a resolution clears the hold and adds an
    // answer without changing that.
    const user = userEvent.setup();
    queueEdit();
    apiMock.getTransactions.mockResolvedValue({
      data: [serverRow("Someone else's coffee")],
    });
    apiMock.updateTransaction.mockResolvedValue({ id: "txn-1", queued: false });

    renderProvider();
    await waitFor(() =>
      expect(screen.getByTestId("conflicts")).toHaveTextContent("1"),
    );

    await user.click(screen.getByText("resolve"));

    // The decided value, and not a second merge: the user answered against the
    // row they were shown, so re-reading the server's row would hold the same
    // question again over a change they never saw.
    await waitFor(() =>
      expect(apiMock.updateTransaction).toHaveBeenCalledWith(
        "txn-1",
        { description: "Tea" },
        { queue: false },
      ),
    );
    await waitFor(() =>
      expect(screen.getByTestId("pending")).toHaveTextContent("0"),
    );
    expect(screen.getByTestId("conflicts")).toHaveTextContent("0");
    // A decided write is a write, so the pages holding the ledger are stale
    // until they reload — and the banner's counter is gone because there is
    // nothing left to answer.
    expect(screen.getByTestId("synced")).toHaveTextContent("yes");
  });

  it("keeps a resolution on the entry when the send cannot be made", async () => {
    // The answer is recorded *before* anything goes out, so a send that never
    // reached the server leaves the entry queued with the answer still on it.
    // That is what makes the decision durable rather than one that only ever
    // existed in a dialog: the next flush re-sends it as it stands, instead of
    // merging the row again and holding the same question.
    const user = userEvent.setup();
    queueEdit();
    apiMock.getTransactions.mockResolvedValue({
      data: [serverRow("Someone else's coffee")],
    });
    apiMock.updateTransaction.mockRejectedValue(new NetworkError());

    renderProvider();
    await waitFor(() =>
      expect(screen.getByTestId("conflicts")).toHaveTextContent("1"),
    );

    await user.click(screen.getByText("resolve"));

    await waitFor(() =>
      expect(apiMock.updateTransaction).toHaveBeenCalled(),
    );
    const [entry] = getOutboxSnapshot("u1");
    expect(entry.conflict).toBeUndefined();
    expect(entry.resolution).toEqual({ description: "mine" });
    expect(screen.getByTestId("pending")).toHaveTextContent("1");
  });

  it("keeps a decision made with no connection, and leaves the send to the reconnect", async () => {
    // The banner's Resolve button is enabled with no connection — recording the
    // answer is a write to this device's own queue, and the two competing
    // values the user is choosing between are already in it — so this is the
    // path a user is really on. The decision has to survive it, and nothing may
    // go out: the entry stays queued, which is what the reconnect sends.
    //
    // Held directly rather than through a flush, because a flush needs the very
    // connection this case takes away. The queue is where the hold lives, so
    // writing one there is the same state a completed merge leaves behind.
    const user = userEvent.setup();
    apiMock.getTransactions.mockResolvedValue({
      data: [serverRow("Someone else's coffee")],
    });
    apiMock.updateTransaction.mockResolvedValue({ id: "txn-1", queued: false });
    setOnline(false);

    // Mounted with an empty queue and filled afterwards, so nothing is waiting
    // at mount: a mount-time flush is not what the case is about, and a queue
    // already held when the provider mounts would be sent off against the
    // server's row instead of being left for the reconnect. A device that was
    // offline when the user answered had its conflict held by a flush that
    // still had a connection, and nothing else happens to the entry until they
    // answer.
    renderProvider();

    // In act() for the reason setOnline is: this is a write the mounted
    // provider subscribes to.
    act(() => {
      const edit = queueEdit();
      recordConflict("u1", edit.key, {
        units: [
          {
            rowId: "txn-1",
            field: "description",
            base: "Coffee",
            mine: "Tea",
            theirs: "Someone else's coffee",
          },
        ],
      });
    });

    await user.click(screen.getByText("resolve"));

    await waitFor(() =>
      expect(screen.getByTestId("conflicts")).toHaveTextContent("0"),
    );
    expect(getOutboxSnapshot("u1")[0].resolution).toEqual({
      description: "mine",
    });
    expect(apiMock.updateTransaction).not.toHaveBeenCalled();
    expect(screen.getByTestId("pending")).toHaveTextContent("1");

    // And the reconnect is what sends it, which is the other half of the case:
    // an answer given with no connection is not an answer that waits for the
    // next launch, it is one that goes out as soon as there is a server to
    // send it to.
    setOnline(true);
    await waitFor(() =>
      expect(apiMock.updateTransaction).toHaveBeenCalledWith(
        "txn-1",
        { description: "Tea" },
        { queue: false },
      ),
    );
    await waitFor(() =>
      expect(screen.getByTestId("pending")).toHaveTextContent("0"),
    );
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
    expect(toastMock.success).toHaveBeenCalledWith("Discarded 1 offline write");
  });

  it("discards only the held entry it was given, and leaves the other held", async () => {
    const user = userEvent.setup();
    queueEdit();
    const other = projectTransaction(serverRow());
    enqueueEdit("u1", "transaction.patch", "txn-2", other, { description: "Tea" }, other);
    // Both rows moved under the two queued edits, so both entries are held. A
    // dialog can show several at once and its Discard sits on the one the user
    // is reading, which is why this is not the queue-wide discard.
    apiMock.getTransactions.mockResolvedValue({
      data: [serverRow("Someone else's coffee")],
    });

    renderProvider();
    await waitFor(() =>
      expect(screen.getByTestId("conflicts")).toHaveTextContent("2"),
    );

    await user.click(screen.getByText("discard conflict"));

    await waitFor(() =>
      expect(screen.getByTestId("conflicts")).toHaveTextContent("1"),
    );
    // The other edit is still queued and still held: a per-entry Discard that
    // took it too is a user's change disappearing without anybody being asked.
    expect(screen.getByTestId("pending")).toHaveTextContent("1");
    expect(toastMock.success).toHaveBeenCalledWith(
      "Discarded the offline change",
    );
  });
});
