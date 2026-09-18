import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen } from "@testing-library/react";
import App from "./App";

const auth = vi.hoisted(() => ({
  isAuthenticated: false,
  initializing: false,
}));

vi.mock("./context/AuthContext", () => ({
  AuthProvider: ({ children }: { children: React.ReactNode }) => children,
  useAuth: () => ({
    isAuthenticated: auth.isAuthenticated,
    initializing: auth.initializing,
  }),
}));

vi.mock("./components/Layout/Sidebar", () => ({
  default: () => <div>Sidebar</div>,
}));

vi.mock("./context/DomainDataContext", () => ({
  DomainDataProvider: ({ children }: { children: React.ReactNode }) => children,
  useDomainData: () => ({ settings: null }),
}));

vi.mock("./components/Auth/Login", () => ({
  default: () => <div>Login screen</div>,
}));
vi.mock("./components/Dashboard/Dashboard", () => ({
  default: () => <div>Dashboard page</div>,
}));
vi.mock("./components/Import/Import", () => ({
  default: () => <div>Import page</div>,
}));
vi.mock("./components/PaperlessImport/PaperlessImport", () => ({
  default: () => <div>Paperless page</div>,
}));
vi.mock("./components/Transactions/Transactions", () => ({
  default: () => <div>Transactions page</div>,
}));
vi.mock("./components/Accounts/Accounts", () => ({
  default: () => <div>Accounts page</div>,
}));
vi.mock("./components/Categories/Categories", () => ({
  default: () => <div>Categories page</div>,
}));
vi.mock("./components/Payees/Payees", () => ({
  default: () => <div>Payees page</div>,
}));
vi.mock("./components/Linking/Linking", () => ({
  default: () => <div>Linking page</div>,
}));
vi.mock("./components/Settings/Settings", () => ({
  default: () => <div>Settings page</div>,
}));

function setPath(path: string) {
  window.history.pushState({}, "", path);
}

describe("App", () => {
  beforeEach(() => {
    auth.isAuthenticated = false;
    auth.initializing = false;
    setPath("/");
  });

  it("shows the route fallback while the session is initializing", () => {
    auth.initializing = true;
    render(<App />);

    expect(screen.queryByText("Login screen")).toBeNull();
    expect(screen.queryByText("Dashboard page")).toBeNull();
  });

  it("renders the login screen when unauthenticated", async () => {
    render(<App />);

    expect(await screen.findByText("Login screen")).toBeInTheDocument();
    expect(screen.queryByText("Sidebar")).toBeNull();
  });

  it("renders the sidebar and dashboard when authenticated", async () => {
    auth.isAuthenticated = true;
    render(<App />);

    expect(await screen.findByText("Dashboard page")).toBeInTheDocument();
    expect(screen.getByText("Sidebar")).toBeInTheDocument();
  });

  it("renders the routed page for a deep link", async () => {
    auth.isAuthenticated = true;
    setPath("/transactions");
    render(<App />);

    expect(await screen.findByText("Transactions page")).toBeInTheDocument();
  });

  it("falls back to the dashboard for an unknown authenticated route", async () => {
    auth.isAuthenticated = true;
    setPath("/does-not-exist");
    render(<App />);

    expect(await screen.findByText("Dashboard page")).toBeInTheDocument();
  });

  it("opens the command palette on Ctrl+K", async () => {
    auth.isAuthenticated = true;
    render(<App />);
    await screen.findByText("Dashboard page");

    window.dispatchEvent(
      new KeyboardEvent("keydown", { key: "k", ctrlKey: true }),
    );

    expect(
      await screen.findByPlaceholderText("Search pages and commands..."),
    ).toBeInTheDocument();
  });
});
