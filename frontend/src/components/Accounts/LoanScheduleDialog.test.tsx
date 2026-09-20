import { describe, it, expect, vi, beforeEach } from "vitest";
import {
  render,
  screen,
  waitFor,
  fireEvent,
} from "@testing-library/react";
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

const { apiMock, toastMock, domainMock } = vi.hoisted(() => ({
  apiMock: {
    getLoanSchedule: vi.fn(),
    saveLoanSchedule: vi.fn(),
    deleteLoanSchedule: vi.fn(),
    transferLoanBalance: vi.fn(),
    deleteLoanTransfer: vi.fn(),
  },
  toastMock: { success: vi.fn(), error: vi.fn() },
  domainMock: { useDomainData: vi.fn() },
}));

vi.mock("../../api/client", () => ({ default: apiMock }));
vi.mock("sonner", () => ({ toast: toastMock }));
vi.mock("../../context/DomainDataContext", () => ({
  useDomainData: domainMock.useDomainData,
}));
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

// Transfer targets: this loan, a second open loan, a closed loan and a
// non-loan account (only the first two are eligible).
const accounts: Account[] = [
  account,
  { ...account, id: "loan-2", name: "Home Loan" },
  { ...account, id: "loan-3", name: "Closed Loan", closed: true },
  { ...account, id: "bank-1", name: "Savings", accountTypeId: "bank" },
];

function detail(overrides: Partial<LoanScheduleDetail> = {}): LoanScheduleDetail {
  const schedule = {
    id: "s1",
    loanAccountId: "loan-1",
    principal: 1000,
    processingFee: 50,
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
        recast: false,
        cancelled: false,
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
        recast: false,
        cancelled: false,
      },
    ],
    paidInstallments: 1,
    paidAmount: 88.85,
    principalPaid: 78.85,
    interestPaid: 10,
    outstandingPrincipal: 921.15,
    transfers: [],
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
  domainMock.useDomainData.mockReturnValue({ accounts });
  apiMock.getLoanSchedule.mockResolvedValue(detail());
});

describe("LoanScheduleDialog", () => {
  it("renders the amortization table with the principal/interest split", async () => {
    renderDialog();

    expect(await screen.findByText("Car Loan — Amortization")).toBeInTheDocument();
    expect(screen.getByText("Outstanding principal")).toBeInTheDocument();
    expect(screen.getByText("Processing fee")).toBeInTheDocument();
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
    expect(screen.getByLabelText("Processing fee")).toBeInTheDocument();
    expect(screen.getByLabelText("Annual interest rate (%)")).toBeInTheDocument();
    expect(screen.getByLabelText("Tenure (months)")).toBeInTheDocument();
    expect(screen.getByLabelText("First installment date")).toBeInTheDocument();
    expect(screen.getByLabelText("Disbursal date")).toBeInTheDocument();
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
        processingFee: 0,
        disbursalDate: "",
        annualRateBps: 1200,
        tenureMonths: 12,
        startDate: "2024-04-01",
      }),
    );
    expect(await screen.findByText("1 of 12 installments paid")).toBeInTheDocument();
  });

  it("sends the processing fee and disbursal date with the terms", async () => {
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
    await user.type(screen.getByLabelText("Processing fee"), "50");
    await user.type(screen.getByLabelText("Disbursal date"), "2024-03-10");
    await user.click(screen.getByRole("button", { name: "Save schedule" }));

    await waitFor(() =>
      expect(apiMock.saveLoanSchedule).toHaveBeenCalledWith("loan-1", {
        principal: 1000,
        processingFee: 50,
        disbursalDate: "2024-03-10",
        annualRateBps: 1200,
        tenureMonths: 12,
        startDate: "2024-04-01",
      }),
    );
  });

  it("records a processing fee without letting it gate or change the EMI", async () => {
    const user = userEvent.setup();
    apiMock.getLoanSchedule.mockResolvedValue(
      detail({ schedule: null, entries: [], emi: 0, paidInstallments: 0 }),
    );
    renderDialog();

    await user.type(await screen.findByLabelText("Principal"), "1000");
    await user.type(screen.getByLabelText("Annual interest rate (%)"), "12");
    await user.type(screen.getByLabelText("Tenure (months)"), "12");
    await user.type(screen.getByLabelText("First installment date"), "2024-04-01");
    // A fee is reference data: it is stored as typed and never rejected for
    // its size, because it does not amortize anything.
    await user.type(screen.getByLabelText("Processing fee"), "1500");
    await user.click(screen.getByRole("button", { name: "Save schedule" }));

    await waitFor(() =>
      expect(apiMock.saveLoanSchedule).toHaveBeenCalledWith(
        "loan-1",
        expect.objectContaining({ principal: 1000, processingFee: 1500 }),
      ),
    );
  });

  it("prefills the terms form when editing an existing schedule", async () => {
    const user = userEvent.setup();
    renderDialog();

    await user.click(
      await screen.findByRole("button", { name: /Edit terms/ }),
    );

    expect(screen.getByLabelText("Principal")).toHaveValue(1000);
    expect(screen.getByLabelText("Processing fee")).toHaveValue(50);
    expect(screen.getByLabelText("Annual interest rate (%)")).toHaveValue(12);
    expect(screen.getByLabelText("Tenure (months)")).toHaveValue(12);
  });

  it("renders recast and settled installments with the transferred amounts", async () => {
    apiMock.getLoanSchedule.mockResolvedValue(
      detail({
        settledOn: "2024-06-15T00:00:00Z",
        outstandingPrincipal: 0,
        entries: [
          {
            number: 1,
            dueDate: "2024-04-01T00:00:00Z",
            amount: 88.85,
            principal: 78.85,
            interest: 10,
            balance: 921.15,
            paid: true,
            recast: false,
            cancelled: false,
            transactionId: "t1",
          },
          {
            number: 2,
            dueDate: "2024-05-01T00:00:00Z",
            amount: 500,
            principal: 490,
            interest: 10,
            balance: 431.15,
            paid: false,
            recast: true,
            cancelled: false,
          },
          {
            number: 3,
            dueDate: "2024-07-01T00:00:00Z",
            amount: 0,
            principal: 0,
            interest: 0,
            balance: 0,
            paid: false,
            recast: false,
            cancelled: true,
          },
        ],
        transfers: [
          {
            id: "tr1",
            fromLoanAccountId: "loan-1",
            fromLoanAccountName: "Car Loan",
            toLoanAccountId: "loan-2",
            toLoanAccountName: "Home Loan",
            amount: 431.15,
            transferDate: "2024-06-15T00:00:00Z",
            createdAt: "2024-06-15T00:00:00Z",
          },
        ],
      }),
    );
    renderDialog();

    // The processing fee is shown for reference and the totals come from the
    // payload; the fee itself is never amortized into them.
    expect(await screen.findByText("Processing fee")).toBeInTheDocument();
    expect(screen.getByText(formatCurrency(50))).toBeInTheDocument();
    expect(screen.getByText(formatCurrency(66.19))).toBeInTheDocument();
    // The cancelled installment is voided by the transfer; the recast one was
    // regenerated by it.
    expect(screen.getByText("Settled")).toBeInTheDocument();
    expect(screen.getByText("Recast")).toBeInTheDocument();
    expect(screen.getByText(/Settled by balance transfer on 15 Jun 2024/)).toBeInTheDocument();
    // A settled loan cannot be transferred again, and the out-transfer can be
    // undone.
    expect(
      screen.queryByRole("button", { name: /Transfer balance/ }),
    ).not.toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "Undo this balance transfer" }),
    ).toBeInTheDocument();
  });

  it("undoes an out-transfer and reloads the schedule", async () => {
    const user = userEvent.setup();
    apiMock.getLoanSchedule.mockResolvedValue(
      detail({
        transfers: [
          {
            id: "tr1",
            fromLoanAccountId: "loan-1",
            fromLoanAccountName: "Car Loan",
            toLoanAccountId: "loan-2",
            toLoanAccountName: "Home Loan",
            amount: 500,
            transferDate: "2024-06-15T00:00:00Z",
            createdAt: "2024-06-15T00:00:00Z",
          },
        ],
      }),
    );
    apiMock.deleteLoanTransfer.mockResolvedValue({ deleted: 1 });
    renderDialog();

    await user.click(
      await screen.findByRole("button", { name: "Undo this balance transfer" }),
    );

    await waitFor(() =>
      expect(apiMock.deleteLoanTransfer).toHaveBeenCalledWith("loan-1", "tr1"),
    );
    await waitFor(() => expect(apiMock.getLoanSchedule).toHaveBeenCalledTimes(2));
  });

  it("posts the target terms when the chosen target has no schedule", async () => {
    const user = userEvent.setup();
    apiMock.getLoanSchedule
      .mockResolvedValueOnce(detail())
      .mockResolvedValueOnce(detail({ schedule: null, entries: [] }));
    apiMock.transferLoanBalance.mockResolvedValue({});
    renderDialog();

    await user.click(
      await screen.findByRole("button", { name: /Transfer balance/ }),
    );
    await user.click(screen.getByRole("combobox"));
    expect(
      await screen.findByRole("option", { name: "Home Loan" }),
    ).toBeInTheDocument();
    // Closed and non-loan accounts are not transfer targets.
    expect(
      screen.queryByRole("option", { name: "Closed Loan" }),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByRole("option", { name: "Savings" }),
    ).not.toBeInTheDocument();
    await user.click(screen.getByRole("option", { name: "Home Loan" }));

    fireEvent.change(screen.getByLabelText("Transfer date"), {
      target: { value: "2024-06-15" },
    });
    await user.type(
      await screen.findByLabelText("Annual interest rate (%)"),
      "10",
    );
    await user.type(screen.getByLabelText("Tenure (months)"), "24");
    fireEvent.change(screen.getByLabelText("First installment date"), {
      target: { value: "2024-07-01" },
    });
    await user.click(screen.getByRole("button", { name: "Confirm transfer" }));

    await waitFor(() =>
      expect(apiMock.transferLoanBalance).toHaveBeenCalledWith("loan-1", {
        toLoanAccountId: "loan-2",
        transferDate: "2024-06-15",
        targetAnnualRateBps: 1000,
        targetTenureMonths: 24,
        targetStartDate: "2024-07-01",
      }),
    );
    expect(toastMock.success).toHaveBeenCalled();
  });

  it("omits the target terms when the chosen target already has a schedule", async () => {
    const user = userEvent.setup();
    apiMock.getLoanSchedule
      .mockResolvedValueOnce(detail())
      .mockResolvedValueOnce(detail());
    apiMock.transferLoanBalance.mockResolvedValue({});
    renderDialog();

    await user.click(
      await screen.findByRole("button", { name: /Transfer balance/ }),
    );
    // The amount that will move is this loan's outstanding principal.
    expect(screen.getByText("Amount to transfer")).toBeInTheDocument();

    await user.click(screen.getByRole("combobox"));
    await user.click(await screen.findByRole("option", { name: "Home Loan" }));

    expect(
      screen.queryByLabelText("Annual interest rate (%)"),
    ).not.toBeInTheDocument();
    expect(screen.queryByLabelText("Tenure (months)")).not.toBeInTheDocument();
    expect(
      screen.queryByLabelText("First installment date"),
    ).not.toBeInTheDocument();

    fireEvent.change(screen.getByLabelText("Transfer date"), {
      target: { value: "2024-06-15" },
    });
    await user.click(screen.getByRole("button", { name: "Confirm transfer" }));

    await waitFor(() =>
      expect(apiMock.transferLoanBalance).toHaveBeenCalledWith("loan-1", {
        toLoanAccountId: "loan-2",
        transferDate: "2024-06-15",
      }),
    );
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
