import { describe, it, expect, vi, beforeEach } from "vitest";
import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { markSynced } from "../api/offlineStatus";
import { DomainDataProvider, useDomainData } from "./DomainDataContext";

const apiMock = vi.hoisted(() => ({
  getAccounts: vi.fn(),
  getAccountTypes: vi.fn(),
  getCategories: vi.fn(),
  getGroups: vi.fn(),
  getPayees: vi.fn(),
  getPaperlessSettings: vi.fn(),
}));

const toastMock = vi.hoisted(() => ({ error: vi.fn() }));

vi.mock("../api/client", () => ({ default: apiMock }));
vi.mock("sonner", () => ({ toast: toastMock }));

// jsdom never changes navigator.onLine on its own, so a test drives both the
// flag and the event the browser would fire with it.
function setOnline(online: boolean) {
  Object.defineProperty(window.navigator, "onLine", {
    configurable: true,
    value: online,
  });
  window.dispatchEvent(new Event(online ? "online" : "offline"));
}

// Probe renders the parts of the context each assertion needs.
function Probe() {
  const { loading, errors, accounts, payees, refreshPayees } = useDomainData();
  return (
    <div>
      <span data-testid="loading">{loading ? "loading" : "loaded"}</span>
      <span data-testid="accounts">{accounts.length}</span>
      <span data-testid="payees">{payees.length}</span>
      <span data-testid="payees-error">{errors.payees ?? ""}</span>
      <button onClick={() => void refreshPayees()}>retry-payees</button>
    </div>
  );
}

function renderProvider() {
  return render(
    <DomainDataProvider>
      <Probe />
    </DomainDataProvider>,
  );
}

beforeEach(() => {
  vi.clearAllMocks();
  setOnline(true);
  markSynced(0);
  apiMock.getAccounts.mockResolvedValue([{ id: "a1" }]);
  apiMock.getAccountTypes.mockResolvedValue([]);
  apiMock.getCategories.mockResolvedValue([]);
  apiMock.getGroups.mockResolvedValue([]);
  apiMock.getPayees.mockResolvedValue([{ id: "p1" }, { id: "p2" }]);
  apiMock.getPaperlessSettings.mockResolvedValue({
    paperlessUrl: "",
    hasToken: false,
  });
});

describe("DomainDataProvider", () => {
  it("loads every resource in parallel and clears loading", async () => {
    renderProvider();

    expect(screen.getByTestId("loading")).toHaveTextContent("loading");
    await waitFor(() =>
      expect(screen.getByTestId("loading")).toHaveTextContent("loaded"),
    );

    expect(apiMock.getAccounts).toHaveBeenCalledTimes(1);
    expect(apiMock.getAccountTypes).toHaveBeenCalledTimes(1);
    expect(apiMock.getCategories).toHaveBeenCalledTimes(1);
    expect(apiMock.getGroups).toHaveBeenCalledTimes(1);
    expect(apiMock.getPayees).toHaveBeenCalledTimes(1);
    expect(apiMock.getPaperlessSettings).toHaveBeenCalledTimes(1);

    expect(screen.getByTestId("accounts")).toHaveTextContent("1");
    expect(screen.getByTestId("payees")).toHaveTextContent("2");
    expect(screen.getByTestId("payees-error")).toHaveTextContent("");
  });

  it("isolates a partial failure and clears it on retry", async () => {
    const user = userEvent.setup();
    apiMock.getPayees.mockRejectedValueOnce(new Error("payees down"));

    renderProvider();
    await waitFor(() =>
      expect(screen.getByTestId("loading")).toHaveTextContent("loaded"),
    );

    // Only payees failed; the other resources still loaded.
    expect(screen.getByTestId("accounts")).toHaveTextContent("1");
    expect(screen.getByTestId("payees-error")).toHaveTextContent("payees down");
    expect(screen.getByTestId("payees")).toHaveTextContent("0");

    // A retry succeeds and clears the error.
    apiMock.getPayees.mockResolvedValue([{ id: "p1" }]);
    await user.click(screen.getByRole("button", { name: "retry-payees" }));

    await waitFor(() =>
      expect(screen.getByTestId("payees-error")).toHaveTextContent(""),
    );
    expect(screen.getByTestId("payees")).toHaveTextContent("1");
  });

  // The lookups are loaded once per session, so an offline boot would keep
  // showing the last online snapshot for the rest of it: categories, payees and
  // accounts created elsewhere stay invisible, and the account balances (which
  // the server computes from the transactions) keep the value they had when the
  // app started.
  it("revalidates the lookups when the connection returns", async () => {
    renderProvider();
    await waitFor(() =>
      expect(screen.getByTestId("loading")).toHaveTextContent("loaded"),
    );
    expect(apiMock.getAccounts).toHaveBeenCalledTimes(1);

    await act(async () => setOnline(false));
    expect(apiMock.getAccounts).toHaveBeenCalledTimes(1);

    await act(async () => setOnline(true));
    await waitFor(() => expect(apiMock.getAccounts).toHaveBeenCalledTimes(2));
    expect(apiMock.getPayees).toHaveBeenCalledTimes(2);
    expect(apiMock.getPaperlessSettings).toHaveBeenCalledTimes(2);
  });

  it("revalidates the lookups after the outbox flushes", async () => {
    renderProvider();
    await waitFor(() =>
      expect(screen.getByTestId("loading")).toHaveTextContent("loaded"),
    );

    await act(async () => markSynced(Date.now()));

    await waitFor(() => expect(apiMock.getAccounts).toHaveBeenCalledTimes(2));
  });

  it("reports a failed load instead of leaving an empty state behind it", async () => {
    apiMock.getPayees.mockRejectedValueOnce(new Error("payees down"));

    renderProvider();
    await waitFor(() =>
      expect(screen.getByTestId("loading")).toHaveTextContent("loaded"),
    );

    // AGENTS.md routes errors through a toast, and the per-resource message
    // stays available for the page that renders the list itself.
    expect(toastMock.error).toHaveBeenCalledWith(
      "Payees could not be loaded: payees down",
    );
  });
});
