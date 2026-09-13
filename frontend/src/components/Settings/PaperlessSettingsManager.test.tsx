import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { toast } from "sonner";
import PaperlessSettingsManager from "./PaperlessSettingsManager";

const { apiMock, domainMock, refreshSettings } = vi.hoisted(() => ({
  apiMock: { updatePaperlessSettings: vi.fn() },
  domainMock: { useDomainData: vi.fn() },
  refreshSettings: vi.fn(),
}));

vi.mock("../../api/client", () => ({ default: apiMock }));
vi.mock("../../context/DomainDataContext", () => ({
  useDomainData: domainMock.useDomainData,
}));
vi.mock("sonner", () => ({ toast: { error: vi.fn(), success: vi.fn() } }));

function setDomain(
  settings: Record<string, unknown> | null,
  loading = false,
) {
  domainMock.useDomainData.mockReturnValue({
    settings,
    loading,
    refreshSettings,
  });
}

function renderManager() {
  return render(<PaperlessSettingsManager />);
}

beforeEach(() => {
  vi.clearAllMocks();
  setDomain({
    paperlessUrl: "http://paperless.local:8000",
    hasToken: false,
    paperlessTag: "fintrak",
  });
  apiMock.updatePaperlessSettings.mockResolvedValue(null);
});

describe("PaperlessSettingsManager", () => {
  it("shows a loading indicator while domain data loads", () => {
    setDomain(null, true);
    renderManager();
    expect(screen.getByText("Loading...")).toBeInTheDocument();
  });

  it("loads existing settings without a saved token", () => {
    renderManager();
    expect(screen.getByDisplayValue("http://paperless.local:8000")).toBeInTheDocument();
    expect(screen.getByDisplayValue("fintrak")).toBeInTheDocument();
    expect(
      screen.getByPlaceholderText("Paperless-ngx API token"),
    ).toBeInTheDocument();
    expect(
      screen.getByText("The token is stored encrypted and is never shown again."),
    ).toBeInTheDocument();
  });

  it("marks the token as saved when one exists", () => {
    setDomain({
      paperlessUrl: "http://paperless.local:8000",
      hasToken: true,
      paperlessTag: "fintrak",
    });
    renderManager();
    expect(
      screen.getByPlaceholderText("Leave blank to keep the saved token"),
    ).toBeInTheDocument();
    expect(
      screen.getByText(
        "An API token is saved. Enter a new one only to replace it.",
      ),
    ).toBeInTheDocument();
  });

  it("renders empty fields when no settings have been loaded", () => {
    setDomain(null);
    renderManager();
    expect(screen.getByPlaceholderText("http://localhost:8000")).toHaveValue("");
    expect(screen.getByPlaceholderText("Paperless-ngx API token")).toHaveValue("");
    expect(screen.getByPlaceholderText("e.g. fintrak")).toHaveValue("");
  });

  it("saves URL, token and tag", async () => {
    const user = userEvent.setup();
    renderManager();

    const urlInput = screen.getByDisplayValue("http://paperless.local:8000");
    await user.clear(urlInput);
    await user.type(urlInput, "https://paperless.example");
    await user.type(
      screen.getByPlaceholderText("Paperless-ngx API token"),
      "secret-token",
    );
    const tagInput = screen.getByDisplayValue("fintrak");
    await user.clear(tagInput);
    await user.type(tagInput, "invoices");
    await user.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() =>
      expect(apiMock.updatePaperlessSettings).toHaveBeenCalledWith({
        paperlessUrl: "https://paperless.example",
        paperlessTag: "invoices",
        paperlessToken: "secret-token",
      }),
    );
    expect(toast.success).toHaveBeenCalledWith("Paperless settings saved");
    expect(refreshSettings).toHaveBeenCalled();
  });

  it("omits the token when the field is left blank", async () => {
    setDomain({
      paperlessUrl: "http://paperless.local:8000",
      hasToken: true,
      paperlessTag: "fintrak",
    });
    const user = userEvent.setup();
    renderManager();

    await user.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() =>
      expect(apiMock.updatePaperlessSettings).toHaveBeenCalledWith({
        paperlessUrl: "http://paperless.local:8000",
        paperlessTag: "fintrak",
      }),
    );
  });

  it("can clear the stored settings", async () => {
    const user = userEvent.setup();
    renderManager();

    await user.clear(screen.getByDisplayValue("http://paperless.local:8000"));
    await user.clear(screen.getByDisplayValue("fintrak"));
    await user.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() =>
      expect(apiMock.updatePaperlessSettings).toHaveBeenCalledWith({
        paperlessUrl: "",
        paperlessTag: "",
      }),
    );
  });

  it("shows the Saved badge after a successful save", async () => {
    const user = userEvent.setup();
    renderManager();

    await user.click(screen.getByRole("button", { name: "Save" }));

    expect(await screen.findByText("Saved")).toBeInTheDocument();
  });

  it("surfaces an error toast when saving fails", async () => {
    apiMock.updatePaperlessSettings.mockRejectedValueOnce(new Error("Save failed"));
    const user = userEvent.setup();
    renderManager();

    await user.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() => expect(toast.error).toHaveBeenCalledWith("Save failed"));
    expect(refreshSettings).not.toHaveBeenCalled();
  });

  it("disables the button and shows progress while saving", async () => {
    let resolveSave: (value: null) => void = () => {};
    apiMock.updatePaperlessSettings.mockReturnValueOnce(
      new Promise<null>((resolve) => {
        resolveSave = resolve;
      }),
    );
    const user = userEvent.setup();
    renderManager();

    await user.click(screen.getByRole("button", { name: "Save" }));

    const savingButton = screen.getByRole("button", { name: "Saving..." });
    expect(savingButton).toBeDisabled();

    resolveSave(null);
    await waitFor(() =>
      expect(screen.getByRole("button", { name: "Save" })).toBeEnabled(),
    );
  });
});
