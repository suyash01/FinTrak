import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { toast } from "sonner";
import SessionsManager from "./SessionsManager";
import type { Session } from "../../types";

const { apiMock } = vi.hoisted(() => ({
  apiMock: {
    getSessions: vi.fn(),
    revokeSession: vi.fn(),
    revokeOtherSessions: vi.fn(),
    revokeAllSessions: vi.fn(),
  },
}));

vi.mock("../../api/client", () => ({ default: apiMock }));
vi.mock("sonner", () => ({
  toast: { error: vi.fn(), success: vi.fn() },
}));

const CHROME_WINDOWS =
  "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/141.0.0.0 Safari/537.36";
const SAFARI_IPHONE =
  "Mozilla/5.0 (iPhone; CPU iPhone OS 18_1 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.1 Mobile/15E148 Safari/604.1";

// A fixed "now" for every expiry assertion. The panel's whole claim is that a
// dead session is never shown as a live one, and a live clock would let that
// test pass today and fail next month.
const NOW = new Date("2026-10-10T12:00:00Z");

function session(over: Partial<Session> = {}): Session {
  return {
    id: "11111111-1111-4111-8111-111111111111",
    userAgent: CHROME_WINDOWS,
    createdAt: "2026-10-01T09:00:00Z",
    lastSeen: "2026-10-10T11:00:00Z",
    expiresAt: "2026-11-10T09:00:00Z",
    current: false,
    ...over,
  };
}

function row(device: string): HTMLElement {
  const heading = screen.getByText(device);
  const el = heading.closest("li");
  if (!el) throw new Error(`no row for ${device}`);
  return el as HTMLElement;
}

async function renderManager() {
  render(<SessionsManager />);
  await waitFor(() => expect(apiMock.getSessions).toHaveBeenCalled());
}

beforeEach(() => {
  vi.clearAllMocks();
  vi.useFakeTimers({ shouldAdvanceTime: true });
  vi.setSystemTime(NOW);
  apiMock.getSessions.mockResolvedValue([]);
  apiMock.revokeSession.mockResolvedValue(undefined);
  apiMock.revokeOtherSessions.mockResolvedValue(undefined);
  apiMock.revokeAllSessions.mockResolvedValue(undefined);
});

afterEach(() => {
  vi.useRealTimers();
});

describe("SessionsManager", () => {
  it("renders each session's parsed user agent and marks the caller's own", async () => {
    apiMock.getSessions.mockResolvedValue([
      session({ id: "a", current: true }),
      session({
        id: "b",
        userAgent: SAFARI_IPHONE,
        lastSeen: "2026-10-09T20:00:00Z",
      }),
    ]);
    await renderManager();

    expect(await screen.findByText("Chrome on Windows")).toBeInTheDocument();
    expect(screen.getByText("Safari on iPhone")).toBeInTheDocument();
    // current is computed in Go from the access token's sid — the panel only
    // reads the flag, and "This device" must appear on exactly one row.
    expect(within(row("Chrome on Windows")).getByText("This device")).toBeInTheDocument();
    expect(
      within(row("Safari on iPhone")).queryByText("This device"),
    ).not.toBeInTheDocument();
  });

  it("renders Unknown device for a session with no user agent", async () => {
    // A row backfilled by migration 000002 from a family that predates the
    // table. It must be nameable and still revokable, not blank.
    apiMock.getSessions.mockResolvedValue([session({ id: "a", userAgent: "" })]);
    await renderManager();

    expect(await screen.findByText("Unknown device")).toBeInTheDocument();
    expect(
      within(row("Unknown device")).getByRole("button", { name: "Sign out" }),
    ).toBeInTheDocument();
  });

  it("renders a session past its expiry as expired, never as active", async () => {
    apiMock.getSessions.mockResolvedValue([
      session({ id: "a", expiresAt: "2026-10-10T11:59:00Z" }),
      session({
        id: "b",
        userAgent: SAFARI_IPHONE,
        expiresAt: "2026-10-10T12:00:01Z",
      }),
    ]);
    await renderManager();

    const dead = row("Chrome on Windows");
    expect(await within(dead).findByText("Expired")).toBeInTheDocument();
    expect(within(dead).queryByText("Active")).not.toBeInTheDocument();

    const live = row("Safari on iPhone");
    expect(within(live).queryByText("Expired")).not.toBeInTheDocument();
    expect(within(live).getByText("Active")).toBeInTheDocument();
  });

  it("requires a confirmation before revoking one session", async () => {
    apiMock.getSessions.mockResolvedValue([session({ id: "sess-1" })]);
    const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime });
    await renderManager();

    await user.click(within(row("Chrome on Windows")).getByRole("button", { name: "Sign out" }));

    const dialog = await screen.findByRole("alertdialog");
    expect(apiMock.revokeSession).not.toHaveBeenCalled();

    await user.click(within(dialog).getByRole("button", { name: /cancel/i }));
    expect(apiMock.revokeSession).not.toHaveBeenCalled();

    await user.click(within(row("Chrome on Windows")).getByRole("button", { name: "Sign out" }));
    await user.click(
      within(await screen.findByRole("alertdialog")).getByRole("button", {
        name: "Sign out",
      }),
    );

    await waitFor(() =>
      expect(apiMock.revokeSession).toHaveBeenCalledWith("sess-1"),
    );
  });

  it("puts the two bulk revokes behind their own confirmations", async () => {
    apiMock.getSessions.mockResolvedValue([session({ id: "sess-1" })]);
    const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime });
    await renderManager();

    // Each action opens its own dialog, and cancelling leaves the API untouched.
    await user.click(screen.getByRole("button", { name: "Sign out other devices" }));
    let dialog = await screen.findByRole("alertdialog");
    expect(apiMock.revokeOtherSessions).not.toHaveBeenCalled();
    await user.click(within(dialog).getByRole("button", { name: /cancel/i }));

    await user.click(screen.getByRole("button", { name: "Sign out everywhere" }));
    dialog = await screen.findByRole("alertdialog");
    expect(apiMock.revokeAllSessions).not.toHaveBeenCalled();
    await user.click(within(dialog).getByRole("button", { name: /cancel/i }));

    await user.click(screen.getByRole("button", { name: "Sign out other devices" }));
    await user.click(
      within(await screen.findByRole("alertdialog")).getByRole("button", {
        name: "Sign out",
      }),
    );
    await waitFor(() => expect(apiMock.revokeOtherSessions).toHaveBeenCalledTimes(1));
    expect(apiMock.revokeAllSessions).not.toHaveBeenCalled();

    await user.click(screen.getByRole("button", { name: "Sign out everywhere" }));
    await user.click(
      within(await screen.findByRole("alertdialog")).getByRole("button", {
        name: "Sign out",
      }),
    );
    await waitFor(() => expect(apiMock.revokeAllSessions).toHaveBeenCalledTimes(1));
  });

  it("does not re-read the list after signing out of every device", async () => {
    // revokeAllSessions revokes the caller's OWN session, so the next request
    // 401s and the app signs the user out. A getSessions() here would race that
    // logout: it would 401 too, and the panel would report the failure as if
    // the sign-out had not worked.
    apiMock.getSessions.mockResolvedValue([session({ id: "sess-1", current: true })]);
    const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime });
    await renderManager();
    expect(apiMock.getSessions).toHaveBeenCalledTimes(1);

    await user.click(screen.getByRole("button", { name: "Sign out everywhere" }));
    await user.click(
      within(await screen.findByRole("alertdialog")).getByRole("button", {
        name: "Sign out",
      }),
    );

    await waitFor(() => expect(apiMock.revokeAllSessions).toHaveBeenCalledTimes(1));
    expect(apiMock.getSessions).toHaveBeenCalledTimes(1);
  });

  it("re-reads the list after revoking another device, but not after its own", async () => {
    apiMock.getSessions.mockResolvedValue([
      session({ id: "mine", current: true }),
      session({ id: "theirs", userAgent: SAFARI_IPHONE }),
    ]);
    const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime });
    await renderManager();
    expect(apiMock.getSessions).toHaveBeenCalledTimes(1);

    // Ending someone else's device leaves this one signed in, so the list is
    // stale until it is re-read.
    await user.click(within(row("Safari on iPhone")).getByRole("button", { name: "Sign out" }));
    await user.click(
      within(await screen.findByRole("alertdialog")).getByRole("button", {
        name: "Sign out",
      }),
    );
    await waitFor(() => expect(apiMock.getSessions).toHaveBeenCalledTimes(2));

    // Ending your OWN is "log me out here": RequireAuth refuses the next
    // request, so re-reading here would race the sign-out the app performs.
    await user.click(within(row("Chrome on Windows")).getByRole("button", { name: "Sign out" }));
    await user.click(
      within(await screen.findByRole("alertdialog")).getByRole("button", {
        name: "Sign out",
      }),
    );
    await waitFor(() =>
      expect(apiMock.revokeSession).toHaveBeenCalledWith("mine"),
    );
    expect(apiMock.getSessions).toHaveBeenCalledTimes(2);
  });

  it("re-reads the list after signing out of the other devices", async () => {
    apiMock.getSessions.mockResolvedValue([session({ id: "sess-1" })]);
    const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime });
    await renderManager();

    await user.click(screen.getByRole("button", { name: "Sign out other devices" }));
    await user.click(
      within(await screen.findByRole("alertdialog")).getByRole("button", {
        name: "Sign out",
      }),
    );
    await waitFor(() => expect(apiMock.getSessions).toHaveBeenCalledTimes(2));
  });

  it("surfaces a failure as a toast and keeps the list", async () => {
    apiMock.getSessions.mockResolvedValue([session({ id: "sess-1" })]);
    apiMock.revokeSession.mockRejectedValueOnce(new Error("nope"));
    const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime });
    await renderManager();

    await user.click(within(row("Chrome on Windows")).getByRole("button", { name: "Sign out" }));
    await user.click(
      within(await screen.findByRole("alertdialog")).getByRole("button", {
        name: "Sign out",
      }),
    );

    await waitFor(() => expect(toast.error).toHaveBeenCalledWith("nope"));
  });

  it("shows a retryable error when the list cannot be loaded", async () => {
    apiMock.getSessions.mockRejectedValue(new Error("network down"));
    const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime });
    await renderManager();

    expect(await screen.findByText(/network down/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /retry/i })).toBeInTheDocument();

    apiMock.getSessions.mockResolvedValue([session({ id: "sess-1" })]);
    await user.click(screen.getByRole("button", { name: /retry/i }));
    expect(await screen.findByText("Chrome on Windows")).toBeInTheDocument();
  });
});
