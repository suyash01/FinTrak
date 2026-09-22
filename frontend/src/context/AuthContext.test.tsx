import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { AuthProvider, useAuth } from "./AuthContext";
import { NetworkError } from "../api/errors";
import { readCached, writeCached } from "../api/offlineCache";
import { enqueueCreate, getOutboxSnapshot } from "../api/outbox";

const { mockApi, mockStoreUser } = vi.hoisted(() => ({
  mockApi: {
    login: vi.fn(),
    register: vi.fn(),
    logout: vi.fn(),
    me: vi.fn(),
  },
  mockStoreUser: vi.fn(),
}));

vi.mock("../api/client", () => ({
  default: mockApi,
  getStoredUser: () => ({ id: 1, email: "stored@example.com" }),
  storeUser: mockStoreUser,
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
    mockApi.me.mockRejectedValue(new Error("Unauthorized"));
    renderHarness();

    await waitFor(() =>
      expect(screen.getByTestId("auth").textContent).toBe("false"),
    );
    expect(screen.getByTestId("user").textContent).toBe("none");
    expect(mockStoreUser).toHaveBeenCalledWith(null);
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

describe("useAuth", () => {
  it("throws when used outside an AuthProvider", () => {
    const spy = vi.spyOn(console, "error").mockImplementation(() => {});
    expect(() => render(<Harness />)).toThrow(
      "useAuth must be used within an AuthProvider",
    );
    spy.mockRestore();
  });
});
