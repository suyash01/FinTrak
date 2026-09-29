import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import OfflineBanner from "./OfflineBanner";
import type { CreateEntry, EditEntry } from "../../api/outbox";

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

// A create, which is all this banner has ever had to render: the queue is a
// union now, and a helper typed to the whole union could not build one.
function entry(overrides: Partial<CreateEntry> = {}): CreateEntry {
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

// A held change, which is the one queued write the merge can neither apply nor
// reject: somebody else moved the same field, so nothing about it is the
// server's answer and nothing about it is queued for a send. The banner only
// counts these, so what the change actually says is not what a case here is
// about — but the shape is, because a create has no base to have been edited
// away from and can never be held.
function held(overrides: Partial<EditEntry> = {}): EditEntry {
  return {
    key: "key-2",
    queuedAt: 2,
    kind: "edit",
    op: "transaction.patch",
    rowId: "txn-1",
    base: { description: "Coffee" },
    patch: { description: "Tea" },
    snapshot: { description: "Coffee", amount: 250.5 },
    conflict: {
      units: [
        {
          rowId: "txn-1",
          field: "description",
          base: "Coffee",
          mine: "Tea",
          theirs: "Someone else's coffee",
        },
      ],
    },
    ...overrides,
  };
}

// The opener App supplies. A required prop rather than an optional one, so a
// caller that forgets it is a compile error instead of a Resolve button that
// reaches nothing — which is how this surface stood for a whole task.
const noop = () => {};

function setState(state: Record<string, unknown>) {
  offlineMock.state = {
    online: true,
    servedFromCache: false,
    pending: [],
    conflicts: [],
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
    const { container } = render(<OfflineBanner onOpenConflicts={noop} />);
    expect(container).toBeEmptyDOMElement();
  });

  it("says the app is offline and showing saved data", () => {
    setState({ online: false, servedFromCache: true });
    render(<OfflineBanner onOpenConflicts={noop} />);

    expect(screen.getByRole("status")).toHaveTextContent(
      "Offline — showing the data you last loaded",
    );
  });

  it("distinguishes a cached read from a lost connection", () => {
    setState({ online: true, servedFromCache: true });
    render(<OfflineBanner onOpenConflicts={noop} />);

    expect(screen.getByRole("status")).toHaveTextContent(
      "Showing saved data — the server could not be reached",
    );
  });

  it("counts the entries waiting to sync and syncs on demand", async () => {
    const user = userEvent.setup();
    setState({ pending: [entry(), entry({ key: "key-2" })] });
    render(<OfflineBanner onOpenConflicts={noop} />);

    expect(screen.getByRole("status")).toHaveTextContent("2 waiting to sync");

    await user.click(screen.getByRole("button", { name: "Sync now" }));

    expect(offlineMock.sync).toHaveBeenCalled();
  });

  it("cannot sync while there is no connection to send over", () => {
    setState({ online: false, pending: [entry()] });
    render(<OfflineBanner onOpenConflicts={noop} />);

    expect(screen.getByRole("button", { name: "Sync now" })).toBeDisabled();
  });

  it("retries a rejected entry on demand", async () => {
    const user = userEvent.setup();
    setState({ pending: [entry({ error: "account is closed" })] });
    render(<OfflineBanner onOpenConflicts={noop} />);

    expect(screen.getByRole("status")).toHaveTextContent(
      "1 rejected by the server",
    );

    await user.click(screen.getByRole("button", { name: "Retry" }));

    expect(offlineMock.sync).toHaveBeenCalledWith({ retryFailed: true });
  });

  it("confirms before discarding rejected entries", async () => {
    const user = userEvent.setup();
    setState({ pending: [entry({ error: "account is closed" })] });
    render(<OfflineBanner onOpenConflicts={noop} />);

    await user.click(screen.getByRole("button", { name: "Discard" }));
    expect(offlineMock.discardFailed).not.toHaveBeenCalled();

    // Only the confirmation dialog's action commits the discard.
    const dialog = await screen.findByRole("alertdialog");
    await user.click(within(dialog).getByRole("button", { name: "Discard" }));

    expect(offlineMock.discardFailed).toHaveBeenCalled();
  });

  it("counts a change the user has to decide rather than one that is merely queued", () => {
    // Three numbers side by side have to mean three different things, so the
    // held entry is counted here and not as waiting: it is in the queue, but
    // nothing will send it until the user answers, and counting it twice would
    // promise a sync the user is waiting for that the flush will not perform.
    const change = held();
    setState({ pending: [change, entry()], conflicts: [change] });
    render(<OfflineBanner onOpenConflicts={noop} />);

    expect(screen.getByRole("status")).toHaveTextContent(
      "1 needs your attention",
    );
    expect(screen.getByRole("status")).toHaveTextContent("1 waiting to sync");
  });

  it("shows neither the count nor the button when nothing is held", () => {
    // A zero beside a real count is noise, and the button is an affordance for
    // a question that is not being asked.
    setState({ pending: [entry()] });
    render(<OfflineBanner onOpenConflicts={noop} />);

    expect(screen.getByRole("status")).not.toHaveTextContent(
      "needs your attention",
    );
    expect(screen.queryByRole("button", { name: "Resolve" })).toBeNull();
  });

  it("opens the conflict dialog from the banner", async () => {
    // The counter is only half a surface. The dialog is mounted by App from
    // state this button cannot reach, so without this the whole feature is
    // built, tested, and unreachable — which is exactly how it stood while
    // App rendered this banner with no props at all.
    const user = userEvent.setup();
    const onOpenConflicts = vi.fn();
    const change = held();
    setState({ pending: [change], conflicts: [change] });
    render(<OfflineBanner onOpenConflicts={onOpenConflicts} />);

    await user.click(screen.getByRole("button", { name: "Resolve" }));

    expect(onOpenConflicts).toHaveBeenCalled();
  });

  it("does not count a rejected entry as one the user has to decide", () => {
    // The two questions are different and the answers are different: a
    // rejection is the server's, and a hold is the user's. One number per
    // question, or the Resolve button sends the user to a dialog with nothing
    // in it.
    setState({
      pending: [entry({ error: "account is closed" })],
      conflicts: [],
    });
    render(<OfflineBanner onOpenConflicts={noop} />);

    expect(screen.getByRole("status")).toHaveTextContent(
      "1 rejected by the server",
    );
    expect(screen.getByRole("status")).not.toHaveTextContent(
      "needs your attention",
    );
    expect(screen.queryByRole("button", { name: "Resolve" })).toBeNull();
  });

  it("names a rejected entry a write, because an edit can be rejected too", async () => {
    // The offline layer's own copy moved from "transaction" to "write" when
    // the queue widened, and a confirmation that says "transaction" beside a
    // 200-row bulk write describes a kind of thing the count no longer means.
    const user = userEvent.setup();
    setState({ pending: [entry({ error: "account is closed" })] });
    render(<OfflineBanner onOpenConflicts={noop} />);

    await user.click(screen.getByRole("button", { name: "Discard" }));

    const dialog = await screen.findByRole("alertdialog");
    expect(dialog).toHaveTextContent("Discard 1 unsent write?");
  });
});
