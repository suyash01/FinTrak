import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import LoanScheduleDialog from "./LoanScheduleDialog";
import { formatCurrency } from "../../utils/formatters";
import type { Account, LoanScheduleDetail } from "../../types";

if (!Element.prototype.hasPointerCapture) {
  Element.prototype.hasPointerCapture = () => false;
  Element.prototype.setPointerCapture = () => {};
  Element.prototype.releasePointerCapture = () => {};
}
if (!Element.prototype.scrollIntoView) {
  Element.prototype.scrollIntoView = () => {};
}

const { apiMock, toastMock } = vi.hoisted(() => ({
  apiMock: {
    getLoanSchedule: vi.fn(),
    saveLoanSchedule: vi.fn(),
    deleteLoanSchedule: vi.fn(),
  },
  toastMock: { success: vi.fn(), error: vi.fn() },
}));

vi.mock("../../api/client", () => ({ default: apiMock }));
vi.mock("sonner", () => ({ toast: toastMock }));
vi.mock("../../context/SettingsContext", () => ({
  useSettings: () => ({ compactLayout: false }),
}));

const account: Account = {
  id: "loan-1",
  name: "Car Loan",
  accountTypeId: "loan",
  bank: "",
  currency: "INR",
  color: "#f00",
  isDefault: false,
  closed: false,
  balance: 0,
};

function detail(overrides: Partial<LoanScheduleDetail> = {}): LoanScheduleDetail {
  const schedule = {
    id: "s1",
    loanAccountId: "loan-1",
    principal: 1000,
    annualRateBps: 1200,
    tenureMonths: 12,
    startDate: "2024-04-01T00:00:00Z",
    createdAt: "2024-04-01T00:00:00Z",
    updatedAt: "2024-04-01T00:00:00Z",
  };
  return {
    schedule,
    loanAccountName: "Car Loan",
    emi: 88.85,
    totalInterest: 66.19,
    totalPayable: 1066.19,
    entries: [
      {
        number: 1,
        dueDate: "2024-04-01T00:00:00Z",
        amount: 88.85,
        principal: 78.85,
        interest: 10,
        balance: 921.15,
        paid: true,
        transactionId: "t1",
      },
      {
        number: 2,
        dueDate: "2024-05-01T00:00:00Z",
        amount: 88.85,
        principal: 79.64,
        interest: 9.21,
        balance: 841.51,
        paid: false,
      },
    ],
    paidInstallments: 1,
    paidAmount: 88.85,
    principalPaid: 78.85,
    interestPaid: 10,
    outstandingPrincipal: 921.15,
    nextDueDate: "2024-05-01T00:00:00Z",
    completed: false,
    ...overrides,
  };
}

function renderDialog(onClose = vi.fn()) {
  render(<LoanScheduleDialog account={account} onClose={onClose} />);
  return onClose;
}

beforeEach(() => {
  vi.clearAllMocks();
  apiMock.getLoanSchedule.mockResolvedValue(detail());
});

describe("LoanScheduleDialog", () => {
  it("renders the amortization table with the principal/interest split", async () => {
    renderDialog();

    expect(await screen.findByText("Car Loan — Amortization")).toBeInTheDocument();
    expect(screen.getByText("Outstanding principal")).toBeInTheDocument();
    expect(screen.getByText("Interest paid")).toBeInTheDocument();
    expect(screen.getByText("1 of 12 installments paid")).toBeInTheDocument();

    // The first installment is split and marked paid, the second is not.
    expect(screen.getByText(formatCurrency(78.85))).toBeInTheDocument();
    // Interest paid (summary) and the first installment's interest.
    expect(screen.getAllByText(formatCurrency(10))).toHaveLength(2);
    expect(screen.getByText("Paid")).toBeInTheDocument();
    // The EMI shows in the summary and on both installments.
    expect(screen.getAllByText(formatCurrency(88.85))).toHaveLength(3);
  });

  it("shows the terms form when the loan has no schedule yet", async () => {
    apiMock.getLoanSchedule.mockResolvedValue(
      detail({ schedule: null, entries: [], emi: 0, paidInstallments: 0 }),
    );
    renderDialog();

    expect(await screen.findByLabelText("Principal")).toBeInTheDocument();
    expect(screen.getByLabelText("Annual interest rate (%)")).toBeInTheDocument();
    expect(screen.getByLabelText("Tenure (months)")).toBeInTheDocument();
    expect(screen.queryByText("Save schedule")).toBeInTheDocument();
  });

  it("saves the terms as basis points and renders the new table", async () => {
    const user = userEvent.setup();
    apiMock.getLoanSchedule.mockResolvedValue(
      detail({ schedule: null, entries: [], emi: 0, paidInstallments: 0 }),
    );
    apiMock.saveLoanSchedule.mockResolvedValue(detail());
    renderDialog();

    await user.type(await screen.findByLabelText("Principal"), "1000");
    await user.type(screen.getByLabelText("Annual interest rate (%)"), "12");
    await user.type(screen.getByLabelText("Tenure (months)"), "12");
    await user.type(screen.getByLabelText("First installment date"), "2024-04-01");
    await user.click(screen.getByRole("button", { name: "Save schedule" }));

    await waitFor(() =>
      expect(apiMock.saveLoanSchedule).toHaveBeenCalledWith("loan-1", {
        principal: 1000,
        annualRateBps: 1200,
        tenureMonths: 12,
        startDate: "2024-04-01",
      }),
    );
    expect(await screen.findByText("1 of 12 installments paid")).toBeInTheDocument();
  });

  it("prefills the terms form when editing an existing schedule", async () => {
    const user = userEvent.setup();
    renderDialog();

    await user.click(
      await screen.findByRole("button", { name: /Edit terms/ }),
    );

    expect(screen.getByLabelText("Principal")).toHaveValue(1000);
    expect(screen.getByLabelText("Annual interest rate (%)")).toHaveValue(12);
    expect(screen.getByLabelText("Tenure (months)")).toHaveValue(12);
  });

  it("removes the schedule", async () => {
    const user = userEvent.setup();
    apiMock.deleteLoanSchedule.mockResolvedValue({ deleted: 1 });
    renderDialog();

    await user.click(
      await screen.findByRole("button", { name: /Remove schedule/ }),
    );

    await waitFor(() =>
      expect(apiMock.deleteLoanSchedule).toHaveBeenCalledWith("loan-1"),
    );
    expect(await screen.findByLabelText("Principal")).toBeInTheDocument();
  });

  it("surfaces a load failure with a retry", async () => {
    apiMock.getLoanSchedule.mockRejectedValue(new Error("boom"));
    renderDialog();

    expect(await screen.findByText("boom")).toBeInTheDocument();
    apiMock.getLoanSchedule.mockResolvedValue(detail());
    await userEvent.click(screen.getByRole("button", { name: "Retry" }));

    expect(await screen.findByText("Car Loan — Amortization")).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: /Edit terms/ }),
    ).toBeInTheDocument();
  });
});
