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
  balance: number;
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
  linkCount?: number;
  linkId?: string | null;
  isSummary?: boolean;
  // Billing cycle attachment (credit cards)
  billingCycleId?: string | null;
  billingCycleLabel?: string;
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
  // Joined
  categoryName?: string;
}

export interface Link {
  id: string;
  type: string;
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
  token: string;
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

export interface DashboardSummary {
  totalAccounts: number;
  totalTransactions: number;
  totalIncome: number;
  totalExpense: number;
  byCategory: CategorySpend[];
  incomeByCategory: CategorySpend[];
  monthlyTrend: MonthlyData[];
  recentTransactions: Transaction[];
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
}

export interface PaperlessDocumentsResponse {
  documents: PaperlessDocument[];
}

export interface PaperlessImportResult {
  transactions?: ImportTransaction[];
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
}

export interface UpdateAccountRequest {
  name: string;
  accountTypeId: string;
  bank?: string;
  currency?: string;
  color?: string;
  isDefault?: boolean;
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
}

export interface UpdateRuleRequest {
  pattern?: string;
  matchType?: string;
  categoryId?: string;
  payeeId?: string | null;
  priority?: number;
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
  type: string;
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
