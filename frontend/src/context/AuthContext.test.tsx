import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { AuthProvider, useAuth } from "./AuthContext";
import { NetworkError } from "../api/errors";
import { readCached, writeCached } from "../api/offlineCache";
import { enqueueCreate, getOutboxSnapshot } from "../api/outbox";

const { mockApi, mockStoreUser, stored } = vi.hoisted(() => ({
  mockApi: {
    login: vi.fn(),
    register: vi.fn(),
    logout: vi.fn(),
    me: vi.fn(),
  },
  mockStoreUser: vi.fn(),
  // The identity the provider clears a departing session's cached ledger by, and
  // the key a second tab overwrites. It is mirrored into storeUser so the
  // provider reads back what it wrote.
  stored: { value: null as { id: number; email: string } | null },
}));

vi.mock("../api/client", () => ({
  default: mockApi,
  STORED_USER_KEY: "fintrak_user",
  getStoredUser: () => stored.value,
  storeUser: (user: unknown) => {
    stored.value = user as { id: number; email: string } | null;
    mockStoreUser(user);
  },
}));

function Harness() {
  const { isAuthenticated, initializing, user, login, register, logout } =
    useAuth();
  return (
    <div>
      <span data-testid="init">{String(initializing)}</span>
      <span data-testid="auth">{String(isAuthenticated)}</span>
      <span data-testid="user">{user ? user.email : "none"}</span>
      <button onClick={() => login("a@b.c", "pw")}>login</button>
      <button onClick={() => register("a@b.c", "pw")}>register</button>
      <button onClick={logout}>logout</button>
    </div>
  );
}

function renderHarness() {
  return render(
    <AuthProvider>
      <Harness />
    </AuthProvider>,
  );
}

describe("AuthProvider", () => {
  beforeEach(() => {
    localStorage.clear();
    stored.value = { id: 1, email: "stored@example.com" };
    mockApi.login.mockReset();
    mockApi.register.mockReset();
    mockApi.logout.mockReset();
    mockApi.me.mockReset();
    mockStoreUser.mockReset();
    // Default: a valid session cookie resolves to the known user.
    mockApi.me.mockResolvedValue({ id: 1, email: "stored@example.com" });
    mockApi.logout.mockResolvedValue(null);
  });

  it("verifies the session on mount and hydrates the user", async () => {
    renderHarness();
    expect(screen.getByTestId("init").textContent).toBe("true");

    await waitFor(() =>
      expect(screen.getByTestId("init").textContent).toBe("false"),
    );
    expect(mockApi.me).toHaveBeenCalled();
    expect(screen.getByTestId("auth").textContent).toBe("true");
    expect(screen.getByTestId("user").textContent).toBe("stored@example.com");
  });

  it("clears the session when the cookie is rejected", async () => {
    writeCached("1", "/accounts", [{ id: "a1" }]);
    mockApi.me.mockRejectedValue(new Error("Unauthorized"));
    renderHarness();

    await waitFor(() =>
      expect(screen.getByTestId("auth").textContent).toBe("false"),
    );
    expect(screen.getByTestId("user").textContent).toBe("none");
    expect(mockStoreUser).toHaveBeenCalledWith(null);
    // The cached reads are namespaced by the id of a user the app has just
    // forgotten, so nothing could ever read or clear them again: a session that
    // ends here has to drop them exactly as an explicit logout does.
    expect(readCached("1", "/accounts")).toBeNull();
  });

  it("login calls the API and persists the new session", async () => {
    const user = userEvent.setup();
    mockApi.login.mockResolvedValue({
      user: { id: 2, email: "a@b.c" },
    });
    renderHarness();
    await waitFor(() =>
      expect(screen.getByTestId("init").textContent).toBe("false"),
    );

    await user.click(screen.getByText("login"));

    expect(mockApi.login).toHaveBeenCalledWith({
      email: "a@b.c",
      password: "pw",
    });
    await waitFor(() =>
      expect(screen.getByTestId("user").textContent).toBe("a@b.c"),
    );
    expect(screen.getByTestId("auth").textContent).toBe("true");
    expect(mockStoreUser).toHaveBeenCalledWith({ id: 2, email: "a@b.c" });
  });

  it("register calls the API and persists the new session", async () => {
    const user = userEvent.setup();
    mockApi.register.mockResolvedValue({
      user: { id: 3, email: "a@b.c" },
    });
    renderHarness();
    await waitFor(() =>
      expect(screen.getByTestId("init").textContent).toBe("false"),
    );

    await user.click(screen.getByText("register"));

    expect(mockApi.register).toHaveBeenCalledWith({
      email: "a@b.c",
      password: "pw",
    });
    await waitFor(() =>
      expect(screen.getByTestId("user").textContent).toBe("a@b.c"),
    );
  });

  it("keeps the session when the probe never reached the server", async () => {
    // An installed app launched offline must show its cached data instead of
    // the sign-in screen: only a *rejected* probe is a real sign-out.
    mockApi.me.mockRejectedValue(new NetworkError());
    renderHarness();

    await waitFor(() =>
      expect(screen.getByTestId("init").textContent).toBe("false"),
    );
    expect(screen.getByTestId("auth").textContent).toBe("true");
    expect(screen.getByTestId("user").textContent).toBe("stored@example.com");
    expect(mockStoreUser).not.toHaveBeenCalledWith(null);
  });

  it("drops the cached ledger on logout but keeps the unsent queue", async () => {
    const user = userEvent.setup();
    writeCached("1", "/accounts", [{ id: "a1" }]);
    enqueueCreate(
      "1",
      {
        accountId: "a1",
        date: "2024-01-15",
        description: "Coffee",
        amount: 250.5,
        type: "debit",
      },
      "key-1",
    );
    renderHarness();
    await waitFor(() =>
      expect(screen.getByTestId("init").textContent).toBe("false"),
    );

    await user.click(screen.getByText("logout"));

    expect(readCached("1", "/accounts")).toBeNull();
    expect(getOutboxSnapshot("1")).toHaveLength(1);
  });

  it("drops the previous user's ledger when a different user signs in", async () => {
    const user = userEvent.setup();
    writeCached("1", "/accounts", [{ id: "a1" }]);
    mockApi.login.mockResolvedValue({ user: { id: 2, email: "b@c.d" } });
    renderHarness();
    await waitFor(() =>
      expect(screen.getByTestId("init").textContent).toBe("false"),
    );

    await user.click(screen.getByText("login"));

    await waitFor(() =>
      expect(screen.getByTestId("user").textContent).toBe("b@c.d"),
    );
    // The cache is namespaced by an id this tab has just stopped being, so it
    // has to go with the session that owned it.
    expect(readCached("1", "/accounts")).toBeNull();
  });

  it("logout clears the session and expires the cookie", async () => {
    const user = userEvent.setup();
    renderHarness();
    await waitFor(() =>
      expect(screen.getByTestId("init").textContent).toBe("false"),
    );

    await user.click(screen.getByText("logout"));

    expect(screen.getByTestId("auth").textContent).toBe("false");
    expect(screen.getByTestId("user").textContent).toBe("none");
    expect(mockStoreUser).toHaveBeenCalledWith(null);
    expect(mockApi.logout).toHaveBeenCalled();
  });
});

// jsdom throws "Not implemented: navigation" when reload() is called, so the
// location is replaced with a plain, inspectable object.
const locationStub = { pathname: "/transactions", href: "", reload: vi.fn() };

describe("AuthProvider session ownership", () => {
  const originalLocation = window.location;

  beforeEach(() => {
    localStorage.clear();
    stored.value = { id: 1, email: "stored@example.com" };
    mockApi.me.mockResolvedValue({ id: 1, email: "stored@example.com" });
    mockApi.logout.mockResolvedValue(null);
    locationStub.reload.mockClear();
    Object.defineProperty(window, "location", {
      configurable: true,
      value: locationStub,
    });
  });

  afterEach(() => {
    Object.defineProperty(window, "location", {
      configurable: true,
      value: originalLocation,
    });
  });

  // The session cookie is shared by every tab, so a second tab signing in as
  // another user silently re-points this tab's identity: it would keep rendering
  // the previous user's ledger, and serving their offline cache, while the API
  // layer already reads the new id.
  it("adopts a session another tab signed in and drops the previous ledger", async () => {
    writeCached("1", "/accounts", [{ id: "a1" }]);
    renderHarness();
    await waitFor(() =>
      expect(screen.getByTestId("init").textContent).toBe("false"),
    );

    stored.value = { id: 2, email: "other@example.com" };
    window.dispatchEvent(
      new StorageEvent("storage", { key: "fintrak_user" }),
    );

    expect(readCached("1", "/accounts")).toBeNull();
    expect(locationStub.reload).toHaveBeenCalled();
  });

  it("ignores a storage change that names the same user", async () => {
    renderHarness();
    await waitFor(() =>
      expect(screen.getByTestId("init").textContent).toBe("false"),
    );

    stored.value = { id: 1, email: "stored@example.com" };
    window.dispatchEvent(new StorageEvent("storage", { key: "fintrak_user" }));

    expect(locationStub.reload).not.toHaveBeenCalled();
  });
});

describe("useAuth", () => {
  it("throws when used outside an AuthProvider", () => {
    const spy = vi.spyOn(console, "error").mockImplementation(() => {});
    expect(() => render(<Harness />)).toThrow(
      "useAuth must be used within an AuthProvider",
    );
    spy.mockRestore();
  });
});
