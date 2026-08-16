import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { AuthProvider, useAuth } from "./AuthContext";

const { mockApi, mockSetToken, mockStoreUser } = vi.hoisted(() => ({
  mockApi: { login: vi.fn(), register: vi.fn() },
  mockSetToken: vi.fn(),
  mockStoreUser: vi.fn(),
}));

vi.mock("../api/client", () => ({
  default: mockApi,
  getToken: () => "stored-token",
  setToken: mockSetToken,
  getStoredUser: () => ({ id: 1, email: "stored@example.com" }),
  storeUser: mockStoreUser,
}));

function Harness() {
  const { isAuthenticated, user, token, login, register, logout } = useAuth();
  return (
    <div>
      <span data-testid="auth">{String(isAuthenticated)}</span>
      <span data-testid="user">{user ? user.email : "none"}</span>
      <span data-testid="token">{token || "none"}</span>
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
    mockSetToken.mockReset();
    mockStoreUser.mockReset();
  });

  it("initializes from stored token and user", () => {
    renderHarness();
    expect(screen.getByTestId("auth").textContent).toBe("true");
    expect(screen.getByTestId("user").textContent).toBe("stored@example.com");
    expect(screen.getByTestId("token").textContent).toBe("stored-token");
  });

  it("login calls the API and persists the new session", async () => {
    const user = userEvent.setup();
    mockApi.login.mockResolvedValue({
      token: "new-token",
      user: { id: 2, email: "a@b.c" },
    });
    renderHarness();

    await user.click(screen.getByText("login"));

    expect(mockApi.login).toHaveBeenCalledWith({
      email: "a@b.c",
      password: "pw",
    });
    expect(screen.getByTestId("auth").textContent).toBe("true");
    expect(screen.getByTestId("user").textContent).toBe("a@b.c");
    expect(screen.getByTestId("token").textContent).toBe("new-token");
    expect(mockSetToken).toHaveBeenCalledWith("new-token");
    expect(mockStoreUser).toHaveBeenCalledWith({ id: 2, email: "a@b.c" });
  });

  it("register calls the API and persists the new session", async () => {
    const user = userEvent.setup();
    mockApi.register.mockResolvedValue({
      token: "reg-token",
      user: { id: 3, email: "a@b.c" },
    });
    renderHarness();

    await user.click(screen.getByText("register"));

    expect(mockApi.register).toHaveBeenCalledWith({
      email: "a@b.c",
      password: "pw",
    });
    expect(screen.getByTestId("token").textContent).toBe("reg-token");
    expect(mockSetToken).toHaveBeenCalledWith("reg-token");
  });

  it("logout clears the session and storage", async () => {
    const user = userEvent.setup();
    renderHarness();

    await user.click(screen.getByText("logout"));

    expect(screen.getByTestId("auth").textContent).toBe("false");
    expect(screen.getByTestId("user").textContent).toBe("none");
    expect(screen.getByTestId("token").textContent).toBe("none");
    expect(mockSetToken).toHaveBeenCalledWith(null);
    expect(mockStoreUser).toHaveBeenCalledWith(null);
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
