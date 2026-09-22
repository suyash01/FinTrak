import { describe, it, expect, vi, beforeEach } from "vitest";
import {
  act,
  render,
  screen,
  waitFor,
  fireEvent,
  within,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import LoanScheduleDialog from "./LoanScheduleDialog";
import { formatCurrency, formatDate } from "../../utils/formatters";
import { todayLocalISO } from "../../lib/dates";
import type {
  Account,
  LoanDisbursement,
  LoanPayoff,
  LoanScheduleDetail,
  Transaction,
} from "../../types";

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
    getLoanPayoff: vi.fn(),
    saveLoanSchedule: vi.fn(),
    deleteLoanSchedule: vi.fn(),
    linkLoanDisbursement: vi.fn(),
    unlinkLoanDisbursement: vi.fn(),
    getTransactions: vi.fn(),
    bulkLoan: vi.fn(),
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

function disbursement(
  overrides: Partial<LoanDisbursement> = {},
): LoanDisbursement {
  return {
    sanctioned: 1000,
    processingFee: 50,
    paidOut: 0,
    net: 950,
    creditTransactionId: "credit-1",
    creditAmount: 950,
    verified: true,
    difference: 0,
    ...overrides,
  };
}

function credit(
  id: string,
  accountId: string,
  accountName: string,
): Transaction {
  return {
    id,
    accountId,
    accountName,
    date: "2024-03-28T00:00:00Z",
    description: "Loan credit",
    amount: 950,
    type: "credit",
  };
}

// The dialog initials the transfer form to the *local* day (the payoff it
// quotes is the one accrued through that date), so the fixtures and the
// assertions take the same value from the same helper the component uses.
const TODAY = todayLocalISO();

// The payoff the API quotes for a transfer date: the outstanding principal plus
// the interest accrued since the last EMI payment, i.e. what a balance transfer
// settles. The figures come from the lender's own statement, so the tests never
// have to add them up.
function payoff(overrides: Partial<LoanPayoff> = {}): LoanPayoff {
  return {
    loanAccountName: "Car Loan",
    asOf: TODAY,
    fromDate: "2024-04-01",
    days: 75,
    outstandingPrincipal: 1956632.46,
    accruedInterest: 26292.25,
    payoff: 1982924.71,
    ...overrides,
  };
}

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
    disbursement: disbursement(),
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
  apiMock.getLoanPayoff.mockResolvedValue(payoff());
  apiMock.getTransactions.mockResolvedValue({
    data: [],
    total: 0,
    page: 1,
    pages: 0,
  });
});

describe("LoanScheduleDialog", () => {
  it("renders the amortization table with the principal/interest split", async () => {
    renderDialog();

    expect(await screen.findByText("Car Loan — Amortization")).toBeInTheDocument();
    expect(screen.getByText("Outstanding principal")).toBeInTheDocument();
    // The summary and the disbursement breakdown both report the fee.
    expect(screen.getAllByText("Processing fee")).toHaveLength(2);
    expect(screen.getByText("Net released")).toBeInTheDocument();
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

  it("unlinks the payment covering an installment and reloads the schedule", async () => {
    const user = userEvent.setup();
    apiMock.bulkLoan.mockResolvedValue(null);
    renderDialog();

    // Only the installment that carries a payment can have it detached; the
    // tooltip spells out that the later matches shift up.
    const unlink = await screen.findAllByRole("button", {
      name: "Unlink the payment covering this installment",
    });
    expect(unlink).toHaveLength(1);
    expect(unlink[0].getAttribute("title")).toContain(
      "the later matches shift up",
    );

    await user.click(unlink[0]);

    await waitFor(() =>
      expect(apiMock.bulkLoan).toHaveBeenCalledWith({
        transactionIds: ["t1"],
        loanAccountId: null,
      }),
    );
    expect(toastMock.success).toHaveBeenCalledWith(
      "Payment unlinked from this loan",
    );
    await waitFor(() => expect(apiMock.getLoanSchedule).toHaveBeenCalledTimes(2));
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
            principal: 422,
            accruedInterest: 9.15,
            transferDate: "2024-06-15T00:00:00Z",
            mode: "recast",
            createdAt: "2024-06-15T00:00:00Z",
          },
        ],
      }),
    );
    renderDialog();

    // The processing fee is shown for reference and the totals come from the
    // payload; the fee itself is never amortized into them. It shows in the
    // summary and in the disbursement breakdown.
    expect(
      await screen.findByText(formatCurrency(66.19)),
    ).toBeInTheDocument();
    expect(screen.getAllByText("Processing fee")).toHaveLength(2);
    expect(screen.getAllByText(formatCurrency(50))).toHaveLength(2);
    // The cancelled installment is voided by the transfer; the recast one was
    // regenerated by it.
    expect(screen.getByText("Settled")).toBeInTheDocument();
    expect(screen.getByText("Recast")).toBeInTheDocument();
    expect(screen.getByText(/Settled by balance transfer on 15 Jun 2024/)).toBeInTheDocument();
    // The transfer reconciles with the lender: the amount it settled is shown
    // alongside the principal and the interest the payoff carried.
    expect(
      screen.getByText(
        `${formatCurrency(422)} principal · ${formatCurrency(9.15)} accrued interest`,
      ),
    ).toBeInTheDocument();
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
            principal: 480,
            accruedInterest: 20,
            transferDate: "2024-06-15T00:00:00Z",
            mode: "recast",
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

    // Undoing a settlement is destructive and now confirms first (AGENTS.md:
    // destructive confirms → AlertDialog), naming what it reverts.
    const confirm = await screen.findByRole("alertdialog");
    expect(apiMock.deleteLoanTransfer).not.toHaveBeenCalled();
    expect(
      within(confirm).getByText(/500\.00 settlement of 15 Jun 2024/),
    ).toBeInTheDocument();

    await user.click(
      within(confirm).getByRole("button", { name: "Undo transfer" }),
    );

    await waitFor(() =>
      expect(apiMock.deleteLoanTransfer).toHaveBeenCalledWith("loan-1", "tr1"),
    );
    await waitFor(() => expect(apiMock.getLoanSchedule).toHaveBeenCalledTimes(2));
  });

  it("keeps the transfer when the undo confirmation is cancelled", async () => {
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
            principal: 480,
            accruedInterest: 20,
            transferDate: "2024-06-15T00:00:00Z",
            mode: "recast",
            createdAt: "2024-06-15T00:00:00Z",
          },
        ],
      }),
    );
    renderDialog();

    await user.click(
      await screen.findByRole("button", { name: "Undo this balance transfer" }),
    );
    const confirm = await screen.findByRole("alertdialog");
    await user.click(within(confirm).getByRole("button", { name: "Cancel" }));

    await waitFor(() =>
      expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument(),
    );
    expect(apiMock.deleteLoanTransfer).not.toHaveBeenCalled();
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

    // A target without a schedule opens one from the amount, so it takes terms
    // and no mode.
    expect(
      screen.queryByRole("combobox", { name: "Transfer mode" }),
    ).not.toBeInTheDocument();

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
    // The amount that will move is the API's quoted payoff on the transfer
    // date — principal plus accrued interest, never just the outstanding
    // principal, and never arithmetic done here.
    expect(screen.queryByText("Amount to transfer")).not.toBeInTheDocument();
    expect(
      await screen.findByText(formatCurrency(1982924.71)),
    ).toBeInTheDocument();

    await user.click(screen.getByRole("combobox"));
    await user.click(await screen.findByRole("option", { name: "Home Loan" }));

    expect(
      screen.queryByLabelText("Annual interest rate (%)"),
    ).not.toBeInTheDocument();
    expect(screen.queryByLabelText("Tenure (months)")).not.toBeInTheDocument();
    expect(
      screen.queryByLabelText("First installment date"),
    ).not.toBeInTheDocument();
    // A target with a schedule takes a mode, defaulting to absorbing the
    // balance into its remaining installments.
    expect(
      screen.getByRole("combobox", { name: "Transfer mode" }),
    ).toBeInTheDocument();

    fireEvent.change(screen.getByLabelText("Transfer date"), {
      target: { value: "2024-06-15" },
    });
    await user.click(screen.getByRole("button", { name: "Confirm transfer" }));

    await waitFor(() =>
      expect(apiMock.transferLoanBalance).toHaveBeenCalledWith("loan-1", {
        toLoanAccountId: "loan-2",
        transferDate: "2024-06-15",
        mode: "recast",
      }),
    );
  });

  it("ignores a superseded target's schedule", async () => {
    const user = userEvent.setup();
    const bike: Account = { ...account, id: "loan-4", name: "Bike Loan" };
    domainMock.useDomainData.mockReturnValue({ accounts: [...accounts, bike] });

    // tsconfig targets ES2022, so Promise.withResolvers is not available here.
    let landHome: (value: LoanScheduleDetail) => void = () => {};
    const slowHome = new Promise<LoanScheduleDetail>((resolve) => {
      landHome = resolve;
    });
    apiMock.getLoanSchedule.mockImplementation((id: string) => {
      if (id === "loan-1") return Promise.resolve(detail()); // the dialog's own loan
      if (id === "loan-2") return slowHome; // Home Loan: lands late
      // Bike Loan has no schedule yet, so it takes terms rather than a mode.
      return Promise.resolve(detail({ schedule: null, entries: [] }));
    });

    renderDialog();
    await user.click(
      await screen.findByRole("button", { name: /Transfer balance/ }),
    );
    await user.click(screen.getByRole("combobox"));
    await user.click(await screen.findByRole("option", { name: "Home Loan" }));
    await user.click(screen.getByRole("combobox"));
    await user.click(await screen.findByRole("option", { name: "Bike Loan" }));

    expect(
      await screen.findByLabelText("Annual interest rate (%)"),
    ).toBeInTheDocument();

    // Home Loan's schedule lands after the switch: it must not describe Bike
    // Loan, or the transfer would be submitted with the wrong target's mode and
    // terms.
    landHome(detail());
    await act(async () => {});
    expect(screen.getByLabelText("Annual interest rate (%)")).toBeInTheDocument();
    expect(
      screen.queryByRole("combobox", { name: "Transfer mode" }),
    ).toBeNull();
  });

  it("shows the quoted payoff breakdown for the transfer date", async () => {
    const user = userEvent.setup();
    renderDialog();

    await user.click(
      await screen.findByRole("button", { name: /Transfer balance/ }),
    );

    await waitFor(() =>
      expect(apiMock.getLoanPayoff).toHaveBeenCalledWith("loan-1", TODAY),
    );
    // Principal, accrued interest and the payoff all come from the quote; the
    // dialog never does money arithmetic of its own.
    expect(screen.getByText("Payoff")).toBeInTheDocument();
    expect(
      await screen.findByText(formatCurrency(1982924.71)),
    ).toBeInTheDocument();
    expect(screen.getByText(formatCurrency(1956632.46))).toBeInTheDocument();
    expect(screen.getByText(formatCurrency(26292.25))).toBeInTheDocument();
    // The accrual names the day it runs from and how many days it covers.
    expect(
      screen.getByText(
        new RegExp(`over 75 days from ${formatDate("2024-04-01")}`),
      ),
    ).toHaveTextContent(
      `${formatCurrency(26292.25)} of interest accrued over 75 days`,
    );
  });

  it("re-quotes the payoff when the transfer date changes", async () => {
    const user = userEvent.setup();
    apiMock.getLoanPayoff
      .mockResolvedValueOnce(payoff())
      .mockResolvedValueOnce(
        payoff({
          asOf: "2024-06-15",
          outstandingPrincipal: 900000,
          accruedInterest: 30000,
          payoff: 930000,
        }),
      );
    renderDialog();

    await user.click(
      await screen.findByRole("button", { name: /Transfer balance/ }),
    );
    expect(
      await screen.findByText(formatCurrency(1982924.71)),
    ).toBeInTheDocument();

    fireEvent.change(screen.getByLabelText("Transfer date"), {
      target: { value: "2024-06-15" },
    });

    await waitFor(() =>
      expect(apiMock.getLoanPayoff).toHaveBeenLastCalledWith(
        "loan-1",
        "2024-06-15",
      ),
    );
    // The displayed amount follows the date, because the payoff is a function
    // of it.
    expect(await screen.findByText(formatCurrency(930000))).toBeInTheDocument();
    expect(screen.getByText(formatCurrency(900000))).toBeInTheDocument();
    expect(
      screen.queryByText(formatCurrency(1982924.71)),
    ).not.toBeInTheDocument();
  });

  it("keeps the form usable while the quote is in flight and after it fails", async () => {
    const user = userEvent.setup();
    let rejectQuote: (err: Error) => void = () => {};
    apiMock.getLoanPayoff.mockImplementation(
      () =>
        new Promise<LoanPayoff>((_resolve, reject) => {
          rejectQuote = reject;
        }),
    );
    renderDialog();

    await user.click(
      await screen.findByRole("button", { name: /Transfer balance/ }),
    );

    expect(await screen.findByText("Quoting the payoff…")).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "Confirm transfer" }),
    ).toBeEnabled();

    // A quote that cannot be produced is reported, but it does not gate the
    // transfer: the endpoint settles the loan at the payoff itself.
    rejectQuote(new Error("This loan has no amortization schedule"));
    expect(
      await screen.findByText(/No payoff quote for this date/),
    ).toBeInTheDocument();
    expect(toastMock.error).toHaveBeenCalledWith(
      "This loan has no amortization schedule",
    );
    expect(
      screen.getByRole("button", { name: "Confirm transfer" }),
    ).toBeEnabled();
  });

  it("shows Matched when the linked credit equals the net disbursement", async () => {
    renderDialog();

    expect(await screen.findByText("Net released")).toBeInTheDocument();
    // The net and the linked credit are both shown, and they agree.
    expect(screen.getAllByText(formatCurrency(950))).toHaveLength(2);
    expect(screen.getByText("Bank credit")).toBeInTheDocument();
    expect(screen.getByText("Matched")).toBeInTheDocument();
    expect(screen.queryByText(/Off by/)).not.toBeInTheDocument();
  });

  it("shows the difference when the linked credit does not match", async () => {
    apiMock.getLoanSchedule.mockResolvedValue(
      detail({
        disbursement: disbursement({
          creditAmount: 900,
          verified: false,
          difference: -50,
        }),
      }),
    );
    renderDialog();

    expect(await screen.findByText(/Off by/)).toHaveTextContent(
      `Off by ${formatCurrency(50)}`,
    );
    expect(screen.queryByText("Matched")).not.toBeInTheDocument();
  });

  it("links a credit found by net amount and by the widened window", async () => {
    const user = userEvent.setup();
    apiMock.getLoanSchedule.mockResolvedValue(
      detail({
        disbursement: disbursement({
          creditTransactionId: undefined,
          creditAmount: undefined,
          verified: false,
          difference: 0,
        }),
      }),
    );
    apiMock.getTransactions.mockResolvedValue({
      data: [
        credit("c-savings", "bank-1", "Savings"),
        credit("c-loan", "loan-2", "Home Loan"),
      ],
      total: 2,
      page: 1,
      pages: 1,
    });
    apiMock.linkLoanDisbursement.mockResolvedValue(detail());
    renderDialog();

    // The exact net amount is searched with no date bounds; the window falls
    // back to 240 days before the first installment, because this schedule has
    // no disbursal date and the money is released before the EMIs start.
    await waitFor(() => expect(apiMock.getTransactions).toHaveBeenCalledTimes(2));
    expect(apiMock.getTransactions).toHaveBeenNthCalledWith(1, {
      type: "credit",
      amount: 950,
      limit: 100,
    });
    expect(apiMock.getTransactions).toHaveBeenNthCalledWith(2, {
      type: "credit",
      dateFrom: "2023-08-05",
      dateTo: "2024-05-16",
      limit: 100,
    });

    const picker = await screen.findByRole("combobox", {
      name: "Link a bank credit",
    });
    await waitFor(() => expect(picker).toBeEnabled());
    await user.click(picker);
    // A credit sitting on another loan can never have funded this one, and the
    // two queries return the same credit only once.
    expect(
      screen.queryByRole("option", { name: /Home Loan/ }),
    ).not.toBeInTheDocument();
    expect(
      screen.getAllByRole("option", { name: /Savings/ }),
    ).toHaveLength(1);
    await user.click(await screen.findByRole("option", { name: /Savings/ }));

    await waitFor(() =>
      expect(apiMock.linkLoanDisbursement).toHaveBeenCalledWith("loan-1", {
        transactionId: "c-savings",
      }),
    );
    expect(await screen.findByText("Matched")).toBeInTheDocument();
  });

  it("anchors the window on the disbursal date when the loan has one", async () => {
    apiMock.getLoanSchedule.mockResolvedValue(
      detail({
        schedule: {
          ...detail().schedule!,
          disbursalDate: "2024-03-01T00:00:00Z",
        },
        disbursement: disbursement({ creditTransactionId: undefined }),
      }),
    );
    renderDialog();

    await waitFor(() => expect(apiMock.getTransactions).toHaveBeenCalledTimes(2));
    expect(apiMock.getTransactions).toHaveBeenNthCalledWith(2, {
      type: "credit",
      dateFrom: "2024-01-16",
      dateTo: "2024-04-15",
      limit: 100,
    });
  });

  it("shows a credit already linked to another loan but does not offer it", async () => {
    const user = userEvent.setup();
    apiMock.getLoanSchedule.mockResolvedValue(
      detail({
        disbursement: disbursement({
          creditTransactionId: undefined,
          creditAmount: undefined,
          verified: false,
          difference: 0,
        }),
      }),
    );
    apiMock.getTransactions.mockResolvedValue({
      data: [
        {
          ...credit("c-attached", "bank-1", "Savings"),
          loanAccountId: "loan-2",
          loanAccountName: "Home Loan",
        },
      ],
      total: 1,
      page: 1,
      pages: 1,
    });
    renderDialog();

    const picker = await screen.findByRole("combobox", {
      name: "Link a bank credit",
    });
    await waitFor(() => expect(picker).toBeEnabled());
    await user.click(picker);

    // The record is visible, labelled with why it cannot be linked, and the
    // API's 409 is never triggered.
    const option = await screen.findByRole("option", {
      name: /already linked to Home Loan/,
    });
    expect(option).toHaveAttribute("aria-disabled", "true");
    fireEvent.click(option);
    expect(apiMock.linkLoanDisbursement).not.toHaveBeenCalled();
  });

  it("explains the empty state with the window it searched", async () => {
    apiMock.getLoanSchedule.mockResolvedValue(
      detail({
        disbursement: disbursement({
          creditTransactionId: undefined,
          creditAmount: undefined,
          verified: false,
          difference: 0,
        }),
      }),
    );
    renderDialog();

    expect(await screen.findByText(/No credit of/)).toHaveTextContent(
      `No credit of ${formatCurrency(950)} between ${formatDate("2023-08-05")} and ` +
        `${formatDate("2024-05-16")}. Import the bank statement covering the ` +
        `disbursement, or set this loan's disbursal date.`,
    );
  });

  it("posts a takeover without target terms when the target has a schedule", async () => {
    const user = userEvent.setup();
    apiMock.getLoanSchedule
      .mockResolvedValueOnce(detail())
      .mockResolvedValueOnce(
        detail({
          disbursement: disbursement({
            sanctioned: 5000,
            processingFee: 0,
            net: 5000,
            creditAmount: 5000,
          }),
        }),
      );
    apiMock.transferLoanBalance.mockResolvedValue({});
    // The takeover pays the quoted payoff out of the target's disbursement.
    apiMock.getLoanPayoff.mockResolvedValue(
      payoff({
        outstandingPrincipal: 921.15,
        accruedInterest: 78.85,
        payoff: 1000,
      }),
    );
    renderDialog();

    await user.click(
      await screen.findByRole("button", { name: /Transfer balance/ }),
    );
    await user.click(screen.getByRole("combobox"));
    await user.click(await screen.findByRole("option", { name: "Home Loan" }));

    await user.click(screen.getByRole("combobox", { name: "Transfer mode" }));
    await user.click(await screen.findByRole("option", { name: /Take over/ }));

    // A takeover shows what the target released and what is left of it once
    // the source's payoff has been paid out.
    expect(screen.getByText("Target net disbursement")).toBeInTheDocument();
    expect(screen.getByText(formatCurrency(5000 - 1000))).toBeInTheDocument();

    fireEvent.change(screen.getByLabelText("Transfer date"), {
      target: { value: "2024-06-15" },
    });
    await user.click(screen.getByRole("button", { name: "Confirm transfer" }));

    await waitFor(() =>
      expect(apiMock.transferLoanBalance).toHaveBeenCalledWith("loan-1", {
        toLoanAccountId: "loan-2",
        transferDate: "2024-06-15",
        mode: "takeover",
      }),
    );
    expect(
      screen.queryByLabelText("Annual interest rate (%)"),
    ).not.toBeInTheDocument();
  });

  it("removes the schedule", async () => {
    const user = userEvent.setup();
    apiMock.deleteLoanSchedule.mockResolvedValue({ deleted: 1 });
    renderDialog();

    await user.click(
      await screen.findByRole("button", { name: /Remove schedule/ }),
    );

    // Removing the schedule is destructive and now confirms first.
    const confirm = await screen.findByRole("alertdialog");
    expect(apiMock.deleteLoanSchedule).not.toHaveBeenCalled();
    expect(
      within(confirm).getByText(/Car Loan's amortization table/),
    ).toBeInTheDocument();

    await user.click(within(confirm).getByRole("button", { name: "Remove" }));

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
