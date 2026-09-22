import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import OfflineBanner from "./OfflineBanner";
import type { OutboxEntry } from "../../api/outbox";

const offlineMock = vi.hoisted(() => ({
  state: {} as Record<string, unknown>,
  sync: vi.fn(),
  discardFailed: vi.fn(),
}));

vi.mock("@/context/OfflineContext", () => ({
  useOffline: () => ({
    ...offlineMock.state,
    sync: offlineMock.sync,
    discardFailed: offlineMock.discardFailed,
  }),
}));

function entry(overrides: Partial<OutboxEntry> = {}): OutboxEntry {
  return {
    key: "key-1",
    queuedAt: 1,
    request: {
      accountId: "acct-1",
      date: "2024-01-15",
      description: "Coffee",
      amount: 250.5,
      type: "debit",
    },
    ...overrides,
  };
}

function setState(state: Record<string, unknown>) {
  offlineMock.state = {
    online: true,
    servedFromCache: false,
    pending: [],
    syncing: false,
    ...state,
  };
}

describe("OfflineBanner", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    setState({});
  });

  it("renders nothing while online with an empty outbox", () => {
    const { container } = render(<OfflineBanner />);
    expect(container).toBeEmptyDOMElement();
  });

  it("says the app is offline and showing saved data", () => {
    setState({ online: false, servedFromCache: true });
    render(<OfflineBanner />);

    expect(screen.getByRole("status")).toHaveTextContent(
      "Offline — showing the data you last loaded",
    );
  });

  it("distinguishes a cached read from a lost connection", () => {
    setState({ online: true, servedFromCache: true });
    render(<OfflineBanner />);

    expect(screen.getByRole("status")).toHaveTextContent(
      "Showing saved data — the server could not be reached",
    );
  });

  it("counts the entries waiting to sync and syncs on demand", async () => {
    const user = userEvent.setup();
    setState({ pending: [entry(), entry({ key: "key-2" })] });
    render(<OfflineBanner />);

    expect(screen.getByRole("status")).toHaveTextContent("2 waiting to sync");

    await user.click(screen.getByRole("button", { name: "Sync now" }));

    expect(offlineMock.sync).toHaveBeenCalled();
  });

  it("cannot sync while there is no connection to send over", () => {
    setState({ online: false, pending: [entry()] });
    render(<OfflineBanner />);

    expect(screen.getByRole("button", { name: "Sync now" })).toBeDisabled();
  });

  it("retries a rejected entry on demand", async () => {
    const user = userEvent.setup();
    setState({ pending: [entry({ error: "account is closed" })] });
    render(<OfflineBanner />);

    expect(screen.getByRole("status")).toHaveTextContent(
      "1 rejected by the server",
    );

    await user.click(screen.getByRole("button", { name: "Retry" }));

    expect(offlineMock.sync).toHaveBeenCalledWith({ retryFailed: true });
  });

  it("confirms before discarding rejected entries", async () => {
    const user = userEvent.setup();
    setState({ pending: [entry({ error: "account is closed" })] });
    render(<OfflineBanner />);

    await user.click(screen.getByRole("button", { name: "Discard" }));
    expect(offlineMock.discardFailed).not.toHaveBeenCalled();

    // Only the confirmation dialog's action commits the discard.
    const dialog = await screen.findByRole("alertdialog");
    await user.click(within(dialog).getByRole("button", { name: "Discard" }));

    expect(offlineMock.discardFailed).toHaveBeenCalled();
  });
});
