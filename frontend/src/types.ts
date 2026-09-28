// API model types mirroring backend/models/models.go. These describe the JSON
// shapes returned by the FinTrak API under /api/v1.

export type QueryParams = Record<string, string | number | boolean>;

export interface AccountType {
  id: string;
  name: string;
  positiveTxnType: string;
}

export interface Account {
  id: string;
  name: string;
  accountTypeId: string;
  accountTypeName?: string;
  bank: string;
  currency: string;
  color: string;
  isDefault: boolean;
  // Closed accounts are immutable for transactions (no add/edit/remove);
  // linking stays possible.
  closed: boolean;
  balance: number;
  // Optional billing day (1-31). When set, per-cycle summary rows are shown
  // for the account regardless of its type; null means none.
  billingDay?: number | null;
  createdAt?: string;
}

export interface Payee {
  id: string;
  name: string;
  accountId?: string | null;
  createdAt?: string;
  updatedAt?: string;
}

export interface CategoryGroup {
  id: string;
  name: string;
  icon: string;
  color: string;
  isBase: boolean;
  isGlobal: boolean;
  userId?: string;
  sortOrder: number;
}

export interface Category {
  id: string;
  name: string;
  icon: string;
  color: string;
  groupId: string;
  isGlobal?: boolean;
  // Joined
  groupName?: string;
  groupIsBase?: boolean;
}

export interface DeleteCategoryResult {
  clearedTransactions: number;
  deletedRules: number;
}

export type TransactionType = "debit" | "credit";

export interface Transaction {
  id: string;
  accountId: string;
  date: string;
  description: string;
  amount: number;
  type: TransactionType;
  categoryId?: string | null;
  tags?: string[];
  notes?: string;
  payeeId?: string | null;
  payee?: string;
  createdAt?: string;
  // Joined fields
  accountName?: string;
  categoryName?: string;
  categoryIcon?: string;
  categoryColor?: string;
  isLinked?: boolean;
  isSummary?: boolean;
  // Billing cycle attachment for an account with a configured billing day.
  billingCycleId?: string | null;
  billingCycleLabel?: string;
  // Loan/EMI attachment: the loan account this transaction is linked to as an
  // EMI payment. At most one loan account per transaction.
  loanAccountId?: string | null;
  loanAccountName?: string;
  // Recurring subscription attachment. At most one series per transaction.
  recurringSeriesId?: string | null;
  recurringSeriesName?: string;
  }

export interface BillingCycle {
  id: string;
  accountId: string;
  startDate: string;
  endDate: string;
  label: string;
  totalOutstanding: number;
  transactionCount: number;
}

export interface Rule {
  id: string;
  pattern: string;
  matchType: string;
  categoryId: string;
  payeeId?: string | null;
  payee?: string;
  priority: number;
  // Conditions (all optional; all must match)
  accountId?: string | null;
  filterCategoryId?: string | null;
  filterPayeeId?: string | null;
  minAmount?: number | null;
  maxAmount?: number | null;
  txnType?: string;
  dateFrom?: string | null;
  dateTo?: string | null;
  isLinked?: boolean | null;
  isRecurring?: boolean | null;
  // Extra actions
  addTags?: string[];
  notes?: string;
  // Joined
  categoryName?: string;
  accountName?: string;
  filterCategoryName?: string;
  filterPayeeName?: string;
}

export interface RulePreview {
  matched: number;
}

export interface TagCount {
  name: string;
  count: number;
}

export interface BulkUpdateTagsRequest {
  transactionIds: string[];
  add?: string[];
  remove?: string[];
}

export interface RenameTagRequest {
  from: string;
  to: string;
}

// Admin console: the shared global catalog with usage counts.
export interface AdminCatalogGroup extends CategoryGroup {
  categoryCount: number;
}

export interface AdminCatalogCategory extends Category {
  transactionCount: number;
}

export interface AdminCatalog {
  groups: AdminCatalogGroup[];
  categories: AdminCatalogCategory[];
}

export type LinkType = "transfer" | "cashback" | "refund" | "bill_payment";

export interface Link {
  id: string;
  type: LinkType;
  fromTxnId: string;
  toTxnId: string;
  notes?: string;
  createdAt?: string;
  // Joined
  fromTxn?: Transaction;
  toTxn?: Transaction;
}

export interface User {
  id: string;
  email: string;
  role?: string;
  createdAt?: string;
}

export interface UserSettings {
  paperlessUrl?: string;
  hasToken?: boolean;
  paperlessTag?: string;
  pageSize?: number | null;
}

export interface PaperlessDocument {
  id: number;
  title: string;
  correspondent: string;
  documentType: string;
  created: string;
  tags: string[];
}

export interface AuthResponse {
  user: User;
}

export interface ImportTransaction {
  date: string;
  description: string;
  amount: number;
  type: TransactionType;
  payeeId?: string | null;
}

export interface ValidateTransactionResult {
  index: number;
  exists: boolean;
  date: string;
  description: string;
  amount: number;
  type: string;
}

export interface ValidateTransactionsResponse {
  total: number;
  existingCount: number;
  missingCount: number;
  results: ValidateTransactionResult[];
}

/**
 * One aggregate's value, keyed by the currency of each account that contributed.
 * A single-currency scope has exactly one key. More than one means the API
 * refused to return a total, and a missing key reads as zero — never as an
 * error, and never as missing data.
 */
export type CurrencyAmounts = Record<string, number>;

/** One account inside a response's scope, with what it contributes. */
export interface ScopedAccount {
  id: string;
  name: string;
  currency: string;
  income: CurrencyAmounts;
  expense: CurrencyAmounts;
}

/**
 * Every currency a reporting response covers, and the accounts behind each.
 * `currencies` is never null: a scope covering no accounts carries [].
 */
export interface CurrencyScope {
  currencies: string[];
  accounts: ScopedAccount[];
}

export interface CategorySpend {
  categoryId: string;
  categoryName: string;
  categoryColor: string;
  categoryIcon: string;
  total: CurrencyAmounts;
  count: number;
}

export interface MonthlyData {
  month: string;
  income: CurrencyAmounts;
  expense: CurrencyAmounts;
}

export interface CurrentCycleInfo {
  id: string;
  startDate: string;
  endDate: string;
  label: string;
}

export interface BillingCycleTrendItem {
  label: string;
  startDate: string;
  endDate: string;
  income: CurrencyAmounts;
  expense: CurrencyAmounts;
}

export interface DashboardSummary {
  totalAccounts: number;
  totalTransactions: number;
  totalIncome: CurrencyAmounts;
  totalExpense: CurrencyAmounts;
  // Computed by the server. Subtracting totalIncome from totalExpense is
  // undefined across currencies, so there is nothing for a client to derive.
  totalNet: CurrencyAmounts;
  byCategory: CategorySpend[];
  incomeByCategory: CategorySpend[];
  monthlyTrend: MonthlyData[];
  recentTransactions: Transaction[];
  currencyScope: CurrencyScope;
  // Present only in billing-cycle view (groupBy=billing_cycle): totals reflect
  // the current statement period and billingCycleTrend replaces monthlyTrend.
  currentCycle?: CurrentCycleInfo;
  billingCycleTrend?: BillingCycleTrendItem[];
}

// ---- Money-flow Sankey ----

export type MoneyFlowNodeKind = "income" | "account" | "category" | "payee";

// One node of the money-flow graph. `id` is stable and stage-prefixed, e.g.
// "account:<uuid>", "category:uncategorized", "income:other".
export interface MoneyFlowNode {
  id: string;
  name: string;
  kind: MoneyFlowNodeKind;
  color?: string;
  group?: string;
  total: CurrencyAmounts;
}

export interface MoneyFlowEdge {
  source: string;
  target: string;
  value: CurrencyAmounts;
}

// Per-type rollup of the user's transaction links in the same window. Shown
// beside the graph rather than drawn as account-to-account edges.
export interface MoneyFlowLinkSummary {
  type: LinkType;
  count: number;
  total: CurrencyAmounts;
}

/**
 * One leg of a cycle the graph could not draw, and what it removed from it.
 *
 * `gross` is the leg's full flow before anything was netted away; `discarded` is
 * the part of it that is in no node total and no edge of the drawing. A currency
 * present in `gross` but absent from the graph is in `discarded`, per currency —
 * which is the question a client asks, and the reason the field is per currency
 * rather than one number.
 */
export interface MoneyFlowSuppressedLeg {
  from: string;
  to: string;
  gross: CurrencyAmounts;
  discarded: CurrencyAmounts;
}

/**
 * One circular account-to-account flow the graph cannot draw, with the money it
 * removed. A Sankey must stay acyclic, so reciprocal pairs are netted and longer
 * loops are broken by dropping their back edge.
 *
 * `accounts` holds the participants in flow order as ids — the same accounts
 * `currencyScope` names — and each leg runs from `accounts[i]` to
 * `accounts[(i+1) % length]`.
 */
export interface MoneyFlowSuppressedCycle {
  kind: "reciprocal" | "cycle";
  accounts: string[];
  legs: MoneyFlowSuppressedLeg[];
}

export interface MoneyFlowGraph {
  nodes: MoneyFlowNode[];
  links: MoneyFlowEdge[];
  totalIncome: CurrencyAmounts;
  totalExpense: CurrencyAmounts;
  // Server-computed for the same reason as DashboardSummary.totalNet.
  totalNet: CurrencyAmounts;
  linkSummary: MoneyFlowLinkSummary[];
  // What the cycle-break removed from the drawing. The linkSummary rollup
  // already counts these same links, so the graph and the rollup state two
  // different totals for the same money unless this is reported: never null,
  // and empty when nothing was withheld.
  suppressedCycles: MoneyFlowSuppressedCycle[];
  currencyScope: CurrencyScope;
}

// ---- Money-flow timeline ----

// One period of the Money Flow timeline strip. startDate/endDate are inclusive
// YYYY-MM-DD bounds that can be passed straight back to getMoneyFlow.
export interface MoneyFlowTimelinePeriod {
  key: string;
  label: string;
  startDate: string;
  endDate: string;
  income: CurrencyAmounts;
  expense: CurrencyAmounts;
  net: CurrencyAmounts;
}

export type MoneyFlowTimelineGroupBy = "month" | "billing_cycle";

export interface MoneyFlowTimeline {
  groupBy: MoneyFlowTimelineGroupBy;
  periods: MoneyFlowTimelinePeriod[];
  currencyScope: CurrencyScope;
}

// ---- Circular money (link cycles) ----

// Per-type rollup of one account-to-account flow.
export interface LinkFlowTypeTotal {
  type: LinkType;
  count: number;
  total: CurrencyAmounts;
}

export interface LinkCycleAccount {
  id: string;
  name: string;
  color?: string;
}

// One directed leg of a cycle. `amount` is the gross flow in the window.
export interface LinkCycleLeg {
  fromAccountId: string;
  fromAccountName: string;
  fromAccountColor?: string;
  toAccountId: string;
  toAccountName: string;
  toAccountColor?: string;
  amount: CurrencyAmounts;
  count: number;
  types: LinkFlowTypeTotal[];
}

// One circular money flow between accounts. `kind` is "reciprocal" for a pair
// that flows both ways (netted into a single Sankey edge) or "cycle" for a
// longer loop broken by dropping its back edge. `net` is the smallest leg — the
// amount that actually circulates the whole loop, which is only a circulation
// figure when the map holds exactly one currency.
export interface LinkCycle {
  kind: "reciprocal" | "cycle";
  accounts: LinkCycleAccount[];
  legs: LinkCycleLeg[];
  net: CurrencyAmounts;
  gross: CurrencyAmounts;
  transactions: number;
}

// A directed account-to-account flow with no flow in the opposite direction.
export interface LinkOneSidedFlow {
  fromAccountId: string;
  fromAccountName: string;
  fromAccountColor?: string;
  toAccountId: string;
  toAccountName: string;
  toAccountColor?: string;
  total: CurrencyAmounts;
  count: number;
  types: LinkFlowTypeTotal[];
}

export interface LinkCycleReport {
  cycles: LinkCycle[];
  totalCircular: CurrencyAmounts;
  oneSidedFlows: LinkOneSidedFlow[];
  currencyScope: CurrencyScope;
}

// ---- Cash-flow calendar heatmap ----

// One day of daily net flow. Days with no transactions are omitted by the API;
// the page fills the gaps to render a continuous calendar.
export interface CashFlowCalendarDay {
  date: string;
  income: CurrencyAmounts;
  expense: CurrencyAmounts;
  net: CurrencyAmounts;
  count: number;
}

// A synthetic summary point overlaid on the calendar: a month-end running
// balance ("balance") or a per-cycle total outstanding ("outstanding").
export interface CashFlowCalendarMarker {
  date: string;
  label: string;
  kind: "balance" | "outstanding";
  amount: CurrencyAmounts;
}

// A billing-cycle boundary, used to mark statement periods on the heatmap.
export interface CashFlowCalendarCycle {
  id: string;
  label: string;
  startDate: string;
  endDate: string;
  outstanding: CurrencyAmounts;
}

export interface CashFlowCalendar {
  days: CashFlowCalendarDay[];
  markers: CashFlowCalendarMarker[];
  cycles: CashFlowCalendarCycle[];
  totalIncome: CurrencyAmounts;
  totalExpense: CurrencyAmounts;
  net: CurrencyAmounts;
  // Largest absolute daily net in the window, per currency, for heatmap
  // scaling. A single scale across currencies would flatten a quiet foreign
  // account's real deficit, so the page scales by the currency on screen.
  maxAbsNet: CurrencyAmounts;
  currencyScope: CurrencyScope;
}

export interface TransactionsResponse {
  data: Transaction[];
  total: number;
  page: number;
  pages: number;
}

export interface ImportResult {
  imported: number;
  total: number;
  duplicates: number;
}

// BackupImportResult summarizes a user-level backup restore: how many rows were
// created per resource, plus any rows skipped because a referenced row was
// missing from the bundle.
export interface BackupImportResult {
  accounts: number;
  categoryGroups: number;
  categories: number;
  payees: number;
  billingCycles: number;
  transactions: number;
  links: number;
  loanAttachments: number;
  recurringSeries: number;
  recurringTerms: number;
  recurringAttachments: number;
  rules: number;
  warnings?: string[];
}

export interface ApplyRulesResult {
  updated: number;
}

export interface StatementExtractor {
  name: string;
  display_name?: string;
}

export interface StatementParseResult {
  transactions?: ImportTransaction[];
  summary?: Record<string, string | number>;
  // Parser-reported mismatches between a page's rebuilt subtotal and the
  // printed one; non-empty means the extracted rows are suspect.
  validationErrors?: string[];
}

export interface PaperlessDocumentsResponse {
  documents: PaperlessDocument[];
  page: number;
  pageSize: number;
  totalCount: number;
  totalPages: number;
  correspondents: string[];
  documentTypes: string[];
  tags: string[];
}

export interface PaperlessDocumentsParams {
  search?: string;
  page?: number;
  pageSize?: number;
  correspondentInc?: string[];
  correspondentExc?: string[];
  documentTypeInc?: string[];
  documentTypeExc?: string[];
  tagInc?: string[];
  tagExc?: string[];
}

export interface PaperlessImportResult {
  transactions?: ImportTransaction[];
  validationErrors?: string[];
}

// ---- Request payloads ----

export interface LoginRequest {
  email: string;
  password: string;
}

export interface RegisterRequest {
  email: string;
  password: string;
}

export interface CreateAccountRequest {
  name: string;
  accountTypeId: string;
  bank?: string;
  currency?: string;
  color?: string;
  isDefault?: boolean;
  billingDay?: number | null;
}

export interface UpdateAccountRequest {
  name: string;
  accountTypeId: string;
  bank?: string;
  currency?: string;
  color?: string;
  isDefault?: boolean;
  closed?: boolean;
  billingDay?: number | null;
}

export interface CreateAccountTypeRequest {
  id: string;
  name: string;
  positiveTxnType: string;
}

export interface UpdateAccountTypeRequest {
  name: string;
  positiveTxnType: string;
}

export interface CreateCategoryRequest {
  name: string;
  icon: string;
  color: string;
  groupId: string;
}

export interface UpdateCategoryRequest {
  name?: string;
  icon?: string;
  color?: string;
  groupId?: string;
}

export interface CreateCategoryGroupRequest {
  id: string;
  name: string;
  icon: string;
  color: string;
}

export interface UpdateCategoryGroupRequest {
  name?: string;
  icon?: string;
  color?: string;
}

export interface CreateTransactionRequest {
  accountId: string;
  date: string;
  description: string;
  amount: number;
  type: TransactionType;
  categoryId?: string | null;
  payeeId?: string | null;
  tags?: string[];
  notes?: string;
  billingCycleId?: string | null;
  // clientKey makes the create idempotent: the server returns the existing
  // transaction when it has already recorded this key, so an offline entry (or
  // a request whose response was lost) is applied exactly once.
  clientKey?: string;
}

export interface UpdateTransactionRequest {
  categoryId?: string | null;
  tags?: string[];
  notes?: string;
  payeeId?: string | null;
  date?: string;
  description?: string;
  amount?: number;
  type?: TransactionType;
  accountId?: string;
  billingCycleId?: string | null;
}

export interface BulkCategorizeRequest {
  transactionIds: string[];
  categoryId: string;
}

export interface BulkUpdatePayeeRequest {
  transactionIds: string[];
  payeeId: string;
}

export interface BulkBillingCycleRequest {
  transactionIds: string[];
  billingCycleId: string;
}

export interface BulkDeleteTransactionsRequest {
  transactionIds: string[];
}

export interface BulkLoanRequest {
  transactionIds: string[];
  // Loan / EMI account to attach to; null/omitted detaches from any loan.
  loanAccountId?: string | null;
}

// ---- Loan amortization schedule ----

// The optional amortization terms of a Loan / EMI account. `annualRateBps` is
// basis points (950 = 9.50% p.a.); `principal` and the derived EMI are plain
// numbers in major units, like every other money field.
export interface LoanSchedule {
  id: string;
  loanAccountId: string;
  principal: number;
  // What the lender charged (major units). Reference only: it is never
  // amortized, so the table repays the whole `principal` whatever it was.
  processingFee: number;
  // Date the loan was disbursed. When present and not exactly one anchored
  // month before `startDate`, installment 1 is a stub that carries day-count
  // interest over the broken first period.
  disbursalDate?: string;
  annualRateBps: number;
  tenureMonths: number;
  startDate: string;
  createdAt: string;
  updatedAt: string;
}

// One installment: the EMI split into principal and interest, with the
// principal still outstanding after it. An entry is `paid` once an attached EMI
// payment covers it (payments are matched in date order). A `recast` entry was
// regenerated by a balance transfer; a `cancelled` one was voided because a
// transfer settled this loan.
export interface LoanScheduleEntry {
  number: number;
  dueDate: string;
  amount: number;
  principal: number;
  interest: number;
  balance: number;
  paid: boolean;
  recast: boolean;
  cancelled: boolean;
  transactionId?: string;
}

// How a balance transfer reshapes the target loan. `recast` raises the
// target's still-due installments to absorb the amount; `opens` starts the
// target's schedule from the amount; `takeover` leaves the target's own
// schedule alone and pays the amount out of the target's disbursement.
export type LoanTransferMode = "recast" | "opens" | "takeover";

// What a loan actually released: its sanctioned principal less the processing
// fee and the takeovers it funded, reconciled against the linked bank credit
// that released it. `net` is the cash the loan paid out.
export interface LoanDisbursement {
  sanctioned: number;
  processingFee: number;
  paidOut: number;
  net: number;
  creditTransactionId?: string;
  creditAmount?: number;
  verified: boolean;
  difference: number;
}

export interface LoanDisbursementRequest {
  transactionId: string;
}

// A principal balance transfer: the source loan was settled on `transferDate`
// at its payoff and the target loan was reshaped by `mode`. The payoff is the
// outstanding `principal` plus the `accruedInterest` that ran from the source's
// last EMI payment up to the transfer date, so `amount` is their sum.
export interface LoanPrincipalTransfer {
  id: string;
  fromLoanAccountId: string;
  fromLoanAccountName?: string;
  toLoanAccountId: string;
  toLoanAccountName?: string;
  amount: number;
  principal: number;
  accruedInterest: number;
  transferDate: string;
  mode: LoanTransferMode;
  createdAt: string;
}

// What settling a loan on a date costs: its outstanding principal plus the
// interest accrued since the last EMI payment. Both the quote and the transfer
// endpoint run the identical computation, so a preview and the transfer it
// precedes can never disagree.
export interface LoanPayoff {
  loanAccountName?: string;
  // The date quoted for.
  asOf: string;
  // The last EMI payment date the accrual runs from.
  fromDate: string;
  // Whole days between `fromDate` and `asOf`.
  days: number;
  outstandingPrincipal: number;
  accruedInterest: number;
  // outstandingPrincipal + accruedInterest: what a transfer moves.
  payoff: number;
}

export interface LoanScheduleDetail {
  schedule: LoanSchedule | null;
  loanAccountName?: string;
  emi: number;
  totalInterest: number;
  totalPayable: number;
  entries: LoanScheduleEntry[];
  paidInstallments: number;
  paidAmount: number;
  principalPaid: number;
  interestPaid: number;
  outstandingPrincipal: number;
  // The amount actually amortized: schedule.principal less the processing fee.
  // Every transfer touching this loan, in date order.
  transfers: LoanPrincipalTransfer[];
  // Date on which a transfer settled this loan; absent otherwise.
  settledOn?: string;
  // Date of the payment covering the highest covered installment. A payoff
  // quote accrues interest from here; absent when no payment is attached yet.
  lastPaidDate?: string;
  nextDueDate?: string;
  completed: boolean;
  // Absent when the loan has no schedule. What it released, reconciled against
  // the bank credit the user linked.
  disbursement?: LoanDisbursement;
}

export interface LoanScheduleRequest {
  principal: number;
  processingFee: number;
  // "YYYY-MM-DD"; an empty string clears the disbursal date.
  disbursalDate: string;
  annualRateBps: number;
  tenureMonths: number;
  startDate: string;
}

export interface LoanTransferRequest {
  toLoanAccountId: string;
  // "YYYY-MM-DD".
  transferDate: string;
  // How the target loan absorbs the amount. Omitted (or empty) means `recast`
  // when the target already has a schedule, `opens` when it does not.
  mode?: LoanTransferMode;
  // Target amortization terms, required only when the target loan has no
  // schedule yet.
  targetAnnualRateBps?: number | null;
  targetTenureMonths?: number | null;
  targetStartDate?: string;
}

export interface LoanTransferResult {
  transfer: LoanPrincipalTransfer;
  source: LoanScheduleDetail;
  target: LoanScheduleDetail;
}

export interface ImportTransactionsRequest {
  accountId: string;
  transactions: ImportTransaction[];
  duplicateAction: "skip" | "keep";
  billingCycleId?: string | null;
  paperlessDocumentIds?: number[];
}

export interface ValidateTransactionsRequest {
  accountId: string;
  transactions: ImportTransaction[];
}

export interface CreateRuleRequest {
  pattern: string;
  matchType: string;
  categoryId: string;
  payeeId?: string | null;
  priority: number;
  accountId?: string | null;
  filterCategoryId?: string | null;
  filterPayeeId?: string | null;
  minAmount?: number | null;
  maxAmount?: number | null;
  txnType?: string;
  dateFrom?: string | null;
  dateTo?: string | null;
  isLinked?: boolean | null;
  isRecurring?: boolean | null;
  addTags?: string[];
  notes?: string;
}

export interface UpdateRuleRequest {
  pattern?: string;
  matchType?: string;
  categoryId?: string;
  payeeId?: string | null;
  priority?: number;
  accountId?: string | null;
  filterCategoryId?: string | null;
  filterPayeeId?: string | null;
  minAmount?: number | null;
  maxAmount?: number | null;
  txnType?: string;
  dateFrom?: string | null;
  dateTo?: string | null;
  isLinked?: boolean | null;
  isRecurring?: boolean | null;
  addTags?: string[];
  notes?: string;
}

export interface CreatePayeeRequest {
  name: string;
  accountId?: string | null;
}

export interface UpdatePayeeRequest {
  name: string;
  accountId?: string | null;
}

export interface CreateLinkRequest {
  type: LinkType;
  fromTxnId: string;
  toTxnId: string;
  notes?: string;
}

export interface BulkDeleteLinksRequest {
  ids: string[];
}

export interface UpdateUserSettingsRequest {
  paperlessUrl?: string;
  paperlessToken?: string;
  paperlessTag?: string;
  pageSize?: number | null;
}

export interface PaperlessImportRequest {
  documentId: number;
  extractor?: string;
  password?: string;
  dateFormat?: string;
}

// ---- Recurring series & subscriptions ----

export type RecurringFrequency = "daily" | "weekly" | "monthly" | "yearly";

// A user-defined expectation of a repeating charge/income. It is a template
// only: FinTrak forecasts the schedule and suggests matches, but never creates
// transactions from it and never links transactions automatically.
export interface RecurringSeries {
  id: string;
  // accountId/amount/startDate/endDate are derived from the series' terms
  // (recurring_series_terms): accountId/amount are the term in effect today,
  // startDate is the earliest term start and endDate the latest term end.
  accountId: string;
  name: string;
  description: string;
  amount: number;
  type: TransactionType;
  frequency: RecurringFrequency;
  interval: number;
  startDate: string;
  endDate?: string | null;
  categoryId?: string | null;
  payeeId?: string | null;
  active: boolean;
  notes: string;
  createdAt?: string;
  // Joined fields
  accountName?: string;
  categoryName?: string;
  categoryIcon?: string;
  categoryColor?: string;
  payee?: string;
  // Derived fields
  nextDueDate?: string | null;
  monthlyAmount: number;
  attachedCount: number;
}

export interface RecurringForecastItem {
  date: string;
  amount: number;
  type: TransactionType;
  matched: boolean;
}

export interface RecurringSuggestion {
  txn: Transaction;
  score: number;
  occurrenceDate: string;
  daysOff: number;
}

// One date-ranged amount/account entry of a recurring series. A transaction
// matches it only when its date falls within [startDate, endDate] (endDate
// omitted = open-ended).
export interface RecurringSeriesTerm {
  id: string;
  seriesId: string;
  startDate: string;
  endDate?: string | null;
  amount: number;
  accountId: string;
  accountName?: string;
  createdAt?: string;
}

export interface CreateRecurringSeriesTermRequest {
  startDate: string;
  endDate?: string;
  amount: number;
  accountId: string;
}

export interface UpdateRecurringSeriesTermRequest {
  startDate?: string;
  // A date string to set, or "" to make the range open-ended.
  endDate?: string;
  amount?: number;
  accountId?: string;
}

export interface CreateRecurringSeriesRequest {
  // Either provide `ranges` (which derive the subscription's start/end) or a
  // single startDate + accountId + amount.
  accountId?: string;
  name: string;
  description?: string;
  amount?: number;
  type: TransactionType;
  frequency: RecurringFrequency;
  interval?: number;
  startDate?: string;
  endDate?: string;
  categoryId?: string | null;
  payeeId?: string | null;
  active?: boolean;
  notes?: string;
  ranges?: RecurringSeriesRange[];
}

// One date-ranged amount/account entry. Ranges are auto-contiguous: each one
// ends where the next begins (exclusive), and the last is open-ended, so only
// startDate is supplied.
export interface RecurringSeriesRange {
  startDate: string;
  endDate?: string;
  amount: number;
  accountId: string;
}

export interface UpdateRecurringSeriesRequest {
  accountId?: string;
  name?: string;
  description?: string;
  amount?: number;
  type?: TransactionType;
  frequency?: RecurringFrequency;
  interval?: number;
  categoryId?: string | null;
  payeeId?: string | null;
  active?: boolean;
  notes?: string;
  // When an amount/account change takes effect (default: today). Ignored
  // unless the amount or account actually changes.
  effectiveDate?: string;
  // When provided, replaces the series' whole range list. The series' dates
  // and per-period accounts/amounts live on its range list, not the series.
  ranges?: RecurringSeriesRange[];
}

export interface RecurringAttachRequest {
  seriesId: string;
  transactionIds: string[];
}

export interface RecurringDetachRequest {
  transactionIds: string[];
}
