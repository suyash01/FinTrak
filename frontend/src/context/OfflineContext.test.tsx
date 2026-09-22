import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { ApiError, NetworkError } from "../api/errors";
import { enqueueCreate } from "../api/outbox";
import { markSynced } from "../api/offlineStatus";
import { OfflineProvider, useOffline } from "./OfflineContext";

const apiMock = vi.hoisted(() => ({ createTransaction: vi.fn() }));
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
  const { online, pending, syncing, syncedAt, sync, discardFailed } =
    useOffline();
  return (
    <div>
      <span data-testid="online">{String(online)}</span>
      <span data-testid="pending">{pending.length}</span>
      <span data-testid="failed">
        {pending.filter((entry) => entry.error !== undefined).length}
      </span>
      <span data-testid="syncing">{String(syncing)}</span>
      <span data-testid="synced">{syncedAt > 0 ? "yes" : "no"}</span>
      <button onClick={() => void sync()}>sync</button>
      <button onClick={discardFailed}>discard</button>
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
});
