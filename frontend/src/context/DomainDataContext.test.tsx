import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { DomainDataProvider, useDomainData } from "./DomainDataContext";

const apiMock = vi.hoisted(() => ({
  getAccounts: vi.fn(),
  getAccountTypes: vi.fn(),
  getCategories: vi.fn(),
  getGroups: vi.fn(),
  getPayees: vi.fn(),
  getPaperlessSettings: vi.fn(),
}));

vi.mock("../api/client", () => ({ default: apiMock }));

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
});
