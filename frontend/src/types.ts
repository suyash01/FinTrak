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
  // Billing cycle attachment (credit cards)
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

export interface CategorySpend {
  categoryId: string;
  categoryName: string;
  categoryColor: string;
  categoryIcon: string;
  total: number;
  count: number;
}

export interface MonthlyData {
  month: string;
  income: number;
  expense: number;
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
  income: number;
  expense: number;
}

export interface DashboardSummary {
  totalAccounts: number;
  totalTransactions: number;
  totalIncome: number;
  totalExpense: number;
  byCategory: CategorySpend[];
  incomeByCategory: CategorySpend[];
  monthlyTrend: MonthlyData[];
  recentTransactions: Transaction[];
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
  total: number;
}

export interface MoneyFlowEdge {
  source: string;
  target: string;
  value: number;
}

// Per-type rollup of the user's transaction links in the same window. Shown
// beside the graph rather than drawn as account-to-account edges.
export interface MoneyFlowLinkSummary {
  type: LinkType;
  count: number;
  total: number;
}

export interface MoneyFlowGraph {
  nodes: MoneyFlowNode[];
  links: MoneyFlowEdge[];
  totalIncome: number;
  totalExpense: number;
  linkSummary: MoneyFlowLinkSummary[];
}

// ---- Money-flow timeline ----

// One period of the Money Flow timeline strip. startDate/endDate are inclusive
// YYYY-MM-DD bounds that can be passed straight back to getMoneyFlow.
export interface MoneyFlowTimelinePeriod {
  key: string;
  label: string;
  startDate: string;
  endDate: string;
  income: number;
  expense: number;
  net: number;
}

export type MoneyFlowTimelineGroupBy = "month" | "billing_cycle";

export interface MoneyFlowTimeline {
  groupBy: MoneyFlowTimelineGroupBy;
  periods: MoneyFlowTimelinePeriod[];
}

// ---- Circular money (link cycles) ----

// Per-type rollup of one account-to-account flow.
export interface LinkFlowTypeTotal {
  type: LinkType;
  count: number;
  total: number;
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
  amount: number;
  count: number;
  types: LinkFlowTypeTotal[];
}

// One circular money flow between accounts. `kind` is "reciprocal" for a pair
// that flows both ways (netted into a single Sankey edge) or "cycle" for a
// longer loop broken by dropping its back edge. `net` is the smallest leg — the
// amount that actually circulates the whole loop.
export interface LinkCycle {
  kind: "reciprocal" | "cycle";
  accounts: LinkCycleAccount[];
  legs: LinkCycleLeg[];
  net: number;
  gross: number;
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
  total: number;
  count: number;
  types: LinkFlowTypeTotal[];
}

export interface LinkCycleReport {
  cycles: LinkCycle[];
  totalCircular: number;
  oneSidedFlows: LinkOneSidedFlow[];
}

// ---- Cash-flow calendar heatmap ----

// One day of daily net flow. Days with no transactions are omitted by the API;
// the page fills the gaps to render a continuous calendar.
export interface CashFlowCalendarDay {
  date: string;
  income: number;
  expense: number;
  net: number;
  count: number;
}

// A synthetic summary point overlaid on the calendar: a month-end running
// balance ("balance") or a per-cycle total outstanding ("outstanding").
export interface CashFlowCalendarMarker {
  date: string;
  label: string;
  kind: "balance" | "outstanding";
  amount: number;
}

// A billing-cycle boundary, used to mark statement periods on the heatmap.
export interface CashFlowCalendarCycle {
  id: string;
  label: string;
  startDate: string;
  endDate: string;
  outstanding: number;
}

export interface CashFlowCalendar {
  days: CashFlowCalendarDay[];
  markers: CashFlowCalendarMarker[];
  cycles: CashFlowCalendarCycle[];
  totalIncome: number;
  totalExpense: number;
  net: number;
  // Largest absolute daily net in the window, for heatmap scaling.
  maxAbsNet: number;
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

// A principal balance transfer: the source loan was settled at `amount` on
// `transferDate` and the target loan's remaining installments were recast to
// absorb it.
export interface LoanPrincipalTransfer {
  id: string;
  fromLoanAccountId: string;
  fromLoanAccountName?: string;
  toLoanAccountId: string;
  toLoanAccountName?: string;
  amount: number;
  transferDate: string;
  createdAt: string;
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
  nextDueDate?: string;
  completed: boolean;
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
