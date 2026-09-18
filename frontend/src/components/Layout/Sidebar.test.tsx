import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter, Routes, Route } from "react-router-dom";
import Sidebar from "./Sidebar";

const { useAuthMock, useDomainDataMock } = vi.hoisted(() => ({
  useAuthMock: vi.fn(),
  useDomainDataMock: vi.fn(),
}));

vi.mock("../../context/AuthContext", () => ({ useAuth: useAuthMock }));
vi.mock("../../context/DomainDataContext", () => ({
  useDomainData: useDomainDataMock,
}));

const logout = vi.fn();

function renderSidebar() {
  return render(
    <MemoryRouter initialEntries={["/"]}>
      <Routes>
        <Route path="/" element={<Sidebar />} />
        <Route path="/login" element={<div>Login route</div>} />
      </Routes>
    </MemoryRouter>,
  );
}

describe("Sidebar", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    useAuthMock.mockReturnValue({
      user: { id: 1, email: "me@example.com" },
      logout,
    });
    useDomainDataMock.mockReturnValue({
      settings: { paperlessUrl: "", hasToken: false },
    });
  });

  it("renders the primary navigation links", () => {
    renderSidebar();

    for (const name of [
      "Dashboard",
      "Import",
      "Transactions",
      "Accounts",
      "Categories & Rules",
      "Payees",
      "Transfers & Cashbacks",
      "Settings",
    ]) {
      expect(screen.getByRole("link", { name })).toBeInTheDocument();
    }
  });

  it("shows the user email and version", () => {
    renderSidebar();
    expect(screen.getByText("me@example.com")).toBeInTheDocument();
    expect(screen.getByText(/FinTrak v/)).toBeInTheDocument();
  });

  it("hides the Paperless link unless both url and token are configured", () => {
    renderSidebar();
    expect(screen.queryByRole("link", { name: "Paperless" })).toBeNull();
  });

  it("shows the Paperless link when settings have a url and token", () => {
    useDomainDataMock.mockReturnValue({
      settings: { paperlessUrl: "https://paperless.example", hasToken: true },
    });
    renderSidebar();
    expect(screen.getByRole("link", { name: "Paperless" })).toBeInTheDocument();
  });

  it("collapses and expands the sidebar, hiding the labels", async () => {
    const user = userEvent.setup();
    renderSidebar();

    expect(screen.getByText("Dashboard")).toBeInTheDocument();
    await user.click(screen.getByTitle("Collapse sidebar"));
    expect(screen.queryByText("Dashboard")).toBeNull();
    expect(screen.getByTitle("Expand sidebar")).toBeInTheDocument();

    await user.click(screen.getByTitle("Expand sidebar"));
    expect(screen.getByText("Dashboard")).toBeInTheDocument();
  });

  it("logs out and navigates to the login route", async () => {
    const user = userEvent.setup();
    renderSidebar();

    await user.click(screen.getByTitle("Log out"));

    expect(logout).toHaveBeenCalledTimes(1);
    expect(screen.getByText("Login route")).toBeInTheDocument();
  });

  it("opens the command palette from the search trigger", async () => {
    const user = userEvent.setup();
    const onOpenCommandPalette = vi.fn();
    render(
      <MemoryRouter initialEntries={["/"]}>
        <Routes>
          <Route
            path="/"
            element={<Sidebar onOpenCommandPalette={onOpenCommandPalette} />}
          />
        </Routes>
      </MemoryRouter>,
    );

    await user.click(
      screen.getByRole("button", { name: "Open command palette" }),
    );

    expect(onOpenCommandPalette).toHaveBeenCalledTimes(1);
  });
});
