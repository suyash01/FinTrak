import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import Login from "./Login";
import { AuthProvider } from "../../context/AuthContext";

const { mockApi, mockStoreUser } = vi.hoisted(() => ({
  mockApi: {
    login: vi.fn(),
    register: vi.fn(),
    logout: vi.fn(),
    me: vi.fn(),
  },
  mockStoreUser: vi.fn(),
}));

vi.mock("../../api/client", () => ({
  default: mockApi,
  getStoredUser: () => null,
  storeUser: mockStoreUser,
}));

function renderLogin() {
  return render(
    <AuthProvider>
      <Login />
    </AuthProvider>,
  );
}

const PASSWORD_PLACEHOLDER = "••••••••";

describe("Login", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockApi.me.mockResolvedValue(null);
    mockApi.logout.mockResolvedValue(null);
  });

  it("renders login mode by default", () => {
    renderLogin();
    expect(screen.getByRole("heading", { name: "Welcome back" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Sign In" })).toBeInTheDocument();
    expect(screen.queryByText("Confirm Password")).toBeNull();
  });

  it("submits credentials to the login API", async () => {
    const user = userEvent.setup();
    mockApi.login.mockResolvedValue({ user: { id: 1, email: "a@b.c" } });
    renderLogin();

    await user.type(screen.getByPlaceholderText("you@example.com"), "a@b.c");
    await user.type(screen.getByPlaceholderText(PASSWORD_PLACEHOLDER), "supersecret1");
    await user.click(screen.getByRole("button", { name: "Sign In" }));

    expect(mockApi.login).toHaveBeenCalledWith({
      email: "a@b.c",
      password: "supersecret1",
    });
  });

  it("shows an error when login fails", async () => {
    const user = userEvent.setup();
    mockApi.login.mockRejectedValue(new Error("Invalid credentials"));
    renderLogin();

    await user.type(screen.getByPlaceholderText("you@example.com"), "a@b.c");
    await user.type(screen.getByPlaceholderText(PASSWORD_PLACEHOLDER), "supersecret1");
    await user.click(screen.getByRole("button", { name: "Sign In" }));

    expect(await screen.findByText("Invalid credentials")).toBeInTheDocument();
  });

  it("switches to register mode and clears any error", async () => {
    const user = userEvent.setup();
    mockApi.login.mockRejectedValue(new Error("Invalid credentials"));
    renderLogin();

    await user.type(screen.getByPlaceholderText("you@example.com"), "a@b.c");
    await user.type(screen.getByPlaceholderText(PASSWORD_PLACEHOLDER), "supersecret1");
    await user.click(screen.getByRole("button", { name: "Sign In" }));
    await screen.findByText("Invalid credentials");

    await user.click(screen.getByRole("button", { name: "Create one" }));

    expect(
      screen.getByRole("heading", { name: "Create your account" }),
    ).toBeInTheDocument();
    expect(screen.getByText("Confirm Password")).toBeInTheDocument();
    expect(screen.queryByText("Invalid credentials")).toBeNull();
  });

  it("rejects a short password before calling register", async () => {
    const user = userEvent.setup();
    renderLogin();
    await user.click(screen.getByRole("button", { name: "Create one" }));

    await user.type(screen.getByPlaceholderText("you@example.com"), "a@b.c");
    const password = screen.getAllByPlaceholderText(PASSWORD_PLACEHOLDER)[0];
    await user.type(password, "short");
    // Bypass native minLength validation to exercise the component guard.
    fireEvent.submit(password.closest("form")!);

    expect(
      screen.getByText("Password must be at least 12 characters"),
    ).toBeInTheDocument();
    expect(mockApi.register).not.toHaveBeenCalled();
  });

  it("rejects mismatched passwords before calling register", async () => {
    const user = userEvent.setup();
    renderLogin();
    await user.click(screen.getByRole("button", { name: "Create one" }));

    await user.type(screen.getByPlaceholderText("you@example.com"), "a@b.c");
    const [password, confirm] = screen.getAllByPlaceholderText(PASSWORD_PLACEHOLDER);
    await user.type(password, "longenough123");
    await user.type(confirm, "different12345");
    fireEvent.submit(password.closest("form")!);

    expect(screen.getByText("Passwords do not match")).toBeInTheDocument();
    expect(mockApi.register).not.toHaveBeenCalled();
  });

  it("registers when the passwords match and are long enough", async () => {
    const user = userEvent.setup();
    mockApi.register.mockResolvedValue({ user: { id: 3, email: "a@b.c" } });
    renderLogin();
    await user.click(screen.getByRole("button", { name: "Create one" }));

    await user.type(screen.getByPlaceholderText("you@example.com"), "a@b.c");
    const [password, confirm] = screen.getAllByPlaceholderText(PASSWORD_PLACEHOLDER);
    await user.type(password, "longenough123");
    await user.type(confirm, "longenough123");
    await user.click(screen.getByRole("button", { name: "Create Account" }));

    expect(mockApi.register).toHaveBeenCalledWith({
      email: "a@b.c",
      password: "longenough123",
    });
  });

  it("disables the submit button and reflects the pending state", async () => {
    const user = userEvent.setup();
    let resolveLogin: (value: unknown) => void = () => {};
    mockApi.login.mockImplementation(
      () => new Promise((resolve) => (resolveLogin = resolve)),
    );
    renderLogin();

    await user.type(screen.getByPlaceholderText("you@example.com"), "a@b.c");
    await user.type(screen.getByPlaceholderText(PASSWORD_PLACEHOLDER), "supersecret1");
    await user.click(screen.getByRole("button", { name: "Sign In" }));

    const pending = screen.getByRole("button", { name: "Signing in..." });
    expect(pending).toBeDisabled();

    resolveLogin({ user: { id: 1, email: "a@b.c" } });
    await waitFor(() =>
      expect(screen.getByRole("button", { name: "Sign In" })).toBeEnabled(),
    );
  });
});
