import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { toast } from "sonner";
import DataSettingsManager from "./DataSettingsManager";
import type { BackupImportResult } from "../../types";

const { apiMock, domainMock, refreshAll } = vi.hoisted(() => ({
  apiMock: { exportUserData: vi.fn(), importUserData: vi.fn() },
  domainMock: { useDomainData: vi.fn() },
  refreshAll: vi.fn(),
}));

vi.mock("../../api/client", () => ({ default: apiMock }));
vi.mock("../../context/DomainDataContext", () => ({
  useDomainData: domainMock.useDomainData,
}));
vi.mock("sonner", () => ({
  toast: { error: vi.fn(), success: vi.fn(), warning: vi.fn() },
}));

const emptyResult: BackupImportResult = {
  accounts: 2,
  categoryGroups: 1,
  categories: 1,
  payees: 1,
  billingCycles: 0,
  transactions: 10,
  links: 0,
  loanAttachments: 0,
  recurringSeries: 0,
  recurringTerms: 0,
  recurringAttachments: 0,
  rules: 0,
};

function uploadBackup(user: ReturnType<typeof userEvent.setup>, body = "{}") {
  const file = new File([body], "backup.json", { type: "application/json" });
  return user.upload(screen.getByLabelText("Backup file"), file);
}

beforeEach(() => {
  vi.clearAllMocks();
  domainMock.useDomainData.mockReturnValue({ refreshAll });
  apiMock.exportUserData.mockResolvedValue(undefined);
  apiMock.importUserData.mockResolvedValue(emptyResult);
});

describe("DataSettingsManager", () => {
  it("renders export and import controls", () => {
    render(<DataSettingsManager />);
    expect(screen.getByRole("button", { name: "Export" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Import" })).toBeInTheDocument();
  });

  it("exports a backup", async () => {
    const user = userEvent.setup();
    render(<DataSettingsManager />);

    await user.click(screen.getByRole("button", { name: "Export" }));

    await waitFor(() => expect(apiMock.exportUserData).toHaveBeenCalled());
    expect(toast.success).toHaveBeenCalledWith("Backup downloaded");
  });

  it("surfaces an export error", async () => {
    apiMock.exportUserData.mockRejectedValueOnce(new Error("Export failed"));
    const user = userEvent.setup();
    render(<DataSettingsManager />);

    await user.click(screen.getByRole("button", { name: "Export" }));

    await waitFor(() => expect(toast.error).toHaveBeenCalledWith("Export failed"));
  });

  it("rejects a file that is not valid JSON", async () => {
    const user = userEvent.setup();
    render(<DataSettingsManager />);

    await uploadBackup(user, "not json");

    await waitFor(() =>
      expect(toast.error).toHaveBeenCalledWith(
        "That file is not a valid JSON backup",
      ),
    );
    expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument();
  });

  it("confirms, imports and refreshes after a valid file is selected", async () => {
    const user = userEvent.setup();
    render(<DataSettingsManager />);

    await uploadBackup(user, JSON.stringify({ format: "fintrak.backup" }));

    const dialog = await screen.findByRole("alertdialog");
    expect(within(dialog).getByText(/backup.json/)).toBeInTheDocument();
    await user.click(within(dialog).getByRole("button", { name: "Import" }));

    await waitFor(() =>
      expect(apiMock.importUserData).toHaveBeenCalledWith({
        format: "fintrak.backup",
      }),
    );
    expect(refreshAll).toHaveBeenCalled();
    expect(toast.success).toHaveBeenCalledWith(
      expect.stringContaining("Backup restored"),
    );
  });

  it("reports skipped rows as a warning", async () => {
    apiMock.importUserData.mockResolvedValueOnce({
      ...emptyResult,
      warnings: ["skipped transaction: its account is not in the backup"],
    });
    const user = userEvent.setup();
    render(<DataSettingsManager />);

    await uploadBackup(user);
    const dialog = await screen.findByRole("alertdialog");
    await user.click(within(dialog).getByRole("button", { name: "Import" }));

    await waitFor(() =>
      expect(toast.warning).toHaveBeenCalledWith(
        expect.stringContaining("1 row skipped"),
      ),
    );
  });

  it("surfaces an import error", async () => {
    apiMock.importUserData.mockRejectedValueOnce(
      new Error("cannot import into an account that already has data"),
    );
    const user = userEvent.setup();
    render(<DataSettingsManager />);

    await uploadBackup(user);
    const dialog = await screen.findByRole("alertdialog");
    await user.click(within(dialog).getByRole("button", { name: "Import" }));

    await waitFor(() =>
      expect(toast.error).toHaveBeenCalledWith(
        "cannot import into an account that already has data",
      ),
    );
    expect(refreshAll).not.toHaveBeenCalled();
  });

  it("cancels the import", async () => {
    const user = userEvent.setup();
    render(<DataSettingsManager />);

    await uploadBackup(user);
    const dialog = await screen.findByRole("alertdialog");
    await user.click(within(dialog).getByRole("button", { name: "Cancel" }));

    await waitFor(() =>
      expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument(),
    );
    expect(apiMock.importUserData).not.toHaveBeenCalled();
  });
});
