package handlers

import (
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/fintrak/backend/auth"
	"github.com/fintrak/backend/internal/money"
	"github.com/fintrak/backend/internal/validation"
	"github.com/fintrak/backend/models"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// maxImportBatch caps the number of transactions accepted in a single import so
// that a malformed or malicious request can't queue an unbounded batch.
const maxImportBatch = 10000

// maxBulkBatch caps how many IDs a single bulk operation may target, so a
// crafted request can't force a giant ANY($1) array or a very long query.
const maxBulkBatch = 5000

// maxPageSize caps how many transactions a single page can return, matching the
// frontend's limit, so a crafted request can't bypass it and fetch everything.
const maxPageSize = 1000

// maxPage caps the page number so (page-1)*limit can never overflow int and
// produce a negative SQL offset (a 500). At 1e6 and the 1000-row page cap the
// largest offset is 1e9, which fits comfortably in a 32-bit int.
const maxPage = 1_000_000

// GetTransactions returns a paginated, filterable list of the user's
// transactions. Filters cover account, category, payee, free-text description,
// date range, type, exact amount, and linked state; sorting and pagination are
// validated/clamped server-side. When filtering a single account that has a
// billing day set (any account type) and sorting by date, synthetic summary
// rows (per-cycle outstanding totals) are merged into the response.
func (srv *Server) GetTransactions(c *gin.Context) {
	userID := auth.GetUserID(c)
	accountID := c.Query("accountId")
	categoryID := c.Query("categoryId")
	groupId := c.Query("groupId")
	search := c.Query("search")
	dateFrom := c.Query("dateFrom")
	dateTo := c.Query("dateTo")
	txnType := c.Query("type")
	sortBy := c.DefaultQuery("sortBy", "date")
	sortOrder := c.DefaultQuery("sortOrder", "DESC")
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	uncategorized := c.Query("uncategorized")
	payeeID := c.Query("payeeId")
	amountStr := c.Query("amount")

	if page < 1 {
		page = 1
	}
	// Reject an out-of-range page rather than letting (page-1)*limit overflow
	// int into a negative SQL offset.
	if page > maxPage {
		validation.RespondError(c, "page out of range", http.StatusBadRequest)
		return
	}
	// Clamp the page size so a crafted request can't bypass the frontend limit
	// (0 or negative values fall back to the default).
	if limit < 1 {
		limit = 50
	}
	if limit > maxPageSize {
		limit = maxPageSize
	}

	// Parse the account filter once: the summary-row path needs the UUID and a
	// malformed value should surface as a 400 rather than a database error.
	var accountUUID *uuid.UUID
	if accountID != "" {
		parsed, err := uuid.Parse(accountID)
		if err != nil {
			validation.RespondError(c, "invalid accountId", http.StatusBadRequest)
			return
		}
		accountUUID = &parsed
	}

	// Validate sort column
	validSorts := map[string]string{
		"date":      "t.date",
		"amount":    "t.amount",
		"createdAt": "t.created_at",
	}
	sortCol, ok := validSorts[sortBy]
	if !ok {
		sortCol = "t.date"
	}
	if sortOrder != "ASC" {
		sortOrder = "DESC"
	}

	// Build the WHERE predicates once. Every clause references only
	// `transactions t` (using correlated EXISTS subqueries where a join would
	// otherwise be required), so the list and count queries share the exact same
	// clause and args and can never drift apart.
	f := newTxnFilter(userID)

	if accountID != "" {
		f.param("t.account_id = $%d", accountID)
	}
	if categoryID != "" {
		// The "uncategorized" sentinel (from the frontend's category filter)
		// means transactions with no category assigned.
		if categoryID == "uncategorized" {
			f.raw("t.category_id IS NULL")
		} else if _, err := uuid.Parse(categoryID); err != nil {
			// Not a UUID -> group-level filter: every category in the given
			// group (a base group slug like "expense" or a custom group id).
			f.param("EXISTS (SELECT 1 FROM categories cat WHERE cat.id = t.category_id AND cat.group_id = $%d)", categoryID)
		} else {
			// Filter to the selected category (scoped to the user). Categories
			// are flat, so this is a plain equality against the category id.
			f.param("t.category_id = $%d", categoryID)
		}
	}
	if uncategorized == "true" {
		f.raw("t.category_id IS NULL")
	}
	if groupId != "" {
		// Group-level filter: every transaction whose category belongs to the
		// given group. Works for base group slugs ("expense") and custom group
		// ids alike, unlike the non-UUID fallback on categoryId.
		f.param("EXISTS (SELECT 1 FROM categories cat WHERE cat.id = t.category_id AND cat.group_id = $%d)", groupId)
	}
	if search != "" {
		// Escape % and _ so "100%" matches the literal text, not "1000" —
		// same semantics as the rules engine's matchRule.
		f.param("LOWER(t.description) LIKE LOWER($%d)", "%"+escapeLikePattern(search)+"%")
	}
	if dateFrom != "" {
		f.param("t.date >= $%d", dateFrom)
	}
	if dateTo != "" {
		f.param("t.date <= $%d", dateTo)
	}
	if txnType != "" {
		f.param("t.type = $%d", txnType)
	}
	if payeeID != "" {
		f.param("t.payee_id = $%d", payeeID)
	}
	if amountStr != "" {
		if amount, err := money.Parse(amountStr); err == nil {
			f.param("t.amount = $%d", amount)
		}
	}
	switch c.Query("linked") {
	case "true":
		f.raw("EXISTS (SELECT 1 FROM links WHERE from_txn_id = t.id OR to_txn_id = t.id)")
	case "false":
		f.raw("NOT EXISTS (SELECT 1 FROM links WHERE from_txn_id = t.id OR to_txn_id = t.id)")
	}

	// Loan/EMI filters: loanAccountId narrows to transactions attached to one
	// loan account (its EMI payments); excludeAttached=true narrows to
	// transactions not attached to any loan account (attach candidates).
	if loanAccountID := c.Query("loanAccountId"); loanAccountID != "" {
		f.param("EXISTS (SELECT 1 FROM loan_attachments la WHERE la.transaction_id = t.id AND la.loan_account_id = $%d)", loanAccountID)
	}
	if c.Query("excludeAttached") == "true" {
		f.raw("NOT EXISTS (SELECT 1 FROM loan_attachments la WHERE la.transaction_id = t.id)")
	}

	// Recurring subscription filters: recurringId narrows to transactions
	// attached to one series; recurring=linked|unlinked filters by whether the
	// transaction is attached to any series.
	if recurringID := c.Query("recurringId"); recurringID != "" {
		f.param("EXISTS (SELECT 1 FROM recurring_attachments ra WHERE ra.transaction_id = t.id AND ra.series_id = $%d)", recurringID)
	}
	switch c.Query("recurring") {
	case "linked":
		f.raw("EXISTS (SELECT 1 FROM recurring_attachments ra WHERE ra.transaction_id = t.id)")
	case "unlinked":
		f.raw("NOT EXISTS (SELECT 1 FROM recurring_attachments ra WHERE ra.transaction_id = t.id)")
	}

	where := f.where()
	query := `SELECT t.id, t.account_id, t.date, t.description, t.amount, t.type, t.category_id,
				t.tags, t.notes, t.payee_id, COALESCE(p.name, '') as payee, t.created_at, a.name as account_name,
				COALESCE(c.name, '') as category_name, COALESCE(c.icon, '') as category_icon,
			  COALESCE(c.color, '') as category_color,
			  EXISTS(SELECT 1 FROM links WHERE from_txn_id = t.id OR to_txn_id = t.id) as is_linked,
			  t.billing_cycle_id,
			  COALESCE(bc.label, '') as billing_cycle_label,
			  la.loan_account_id,
			  COALESCE(loan_acct.name, '') as loan_account_name,
			  rca.series_id,
			  COALESCE(rcs.name, '') as recurring_series_name
			  FROM transactions t
			  JOIN accounts a ON t.account_id = a.id
			  LEFT JOIN categories c ON t.category_id = c.id
			  LEFT JOIN payees p ON t.payee_id = p.id
			  LEFT JOIN billing_cycles bc ON t.billing_cycle_id = bc.id
			  LEFT JOIN loan_attachments la ON la.transaction_id = t.id
			  LEFT JOIN accounts loan_acct ON loan_acct.id = la.loan_account_id
			  LEFT JOIN recurring_attachments rca ON rca.transaction_id = t.id
			  LEFT JOIN recurring_series rcs ON rcs.id = rca.series_id` + where

	countQuery := `SELECT COUNT(*) FROM transactions t` + where

	// Get total count
	var total int
	if err := srv.db.QueryRow(c, countQuery, f.args...).Scan(&total); err != nil {
		slog.Error("GetTransactions (count)", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	offset := (page - 1) * limit
	paramIdx := len(f.args) + 1
	query += fmt.Sprintf(" ORDER BY %s %s LIMIT $%d OFFSET $%d", sortCol, sortOrder, paramIdx, paramIdx+1)
	args := append(f.args, limit, offset)

	rows, err := srv.db.Query(c, query, args...)
	if err != nil {
		slog.Error("GetTransactions", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	transactions := []models.Transaction{}
	for rows.Next() {
		var t models.Transaction
		if err := rows.Scan(&t.ID, &t.AccountID, &t.Date, &t.Description, &t.Amount, &t.Type,
			&t.CategoryID, &t.Tags, &t.Notes, &t.PayeeID, &t.Payee, &t.CreatedAt,
			&t.AccountName, &t.CategoryName, &t.CategoryIcon, &t.CategoryColor, &t.IsLinked,
			&t.BillingCycleID, &t.BillingCycleLabel, &t.LoanAccountID, &t.LoanAccountName,
			&t.RecurringSeriesID, &t.RecurringSeriesName); err != nil {
			slog.Error("GetTransactions scan", slog.String("error", err.Error()))
			validation.RespondError(c, "internal server error", http.StatusInternalServerError)
			return
		}
		transactions = append(transactions, t)
	}
	if err := rows.Err(); err != nil {
		slog.Error("GetTransactions rows", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	// When a single account is filtered and sorted by date, inject computed
	// summary rows: per-cycle "Total outstanding" rows for accounts with a
	// billing day set, and month-end "Running balance" rows for accounts
	// without one (loan accounts never reach this path — the frontend filters
	// them via loanAccountId). These are synthetic and are never persisted.
	// Summary rows only make sense in a date-ordered list, so other sort
	// columns skip them entirely.
	if accountUUID != nil && sortBy == "date" {
		summaryTxns, balanceTxns := srv.buildAccountSummaryRows(c, userID, *accountUUID, dateFrom, dateTo)
		transactions = mergeSummaryRows(transactions, summaryTxns, sortBy, sortOrder)
		transactions = mergeMonthEndRows(transactions, balanceTxns, sortOrder)
	}

	pages := 1
	pages = int(math.Ceil(float64(total) / float64(limit)))

	c.JSON(http.StatusOK, gin.H{
		"data":  transactions,
		"total": total,
		"page":  page,
		"limit": limit,
		"pages": pages,
	})
}

// txnFilter accumulates the WHERE predicates shared by GetTransactions' list
// and count queries. Every clause references only the `transactions t` table
// (using correlated EXISTS subqueries where a join would otherwise be needed),
// so both queries run the identical predicate with the identical args — the
// count and the page can never diverge. args[0] is always the user id.
type txnFilter struct {
	clauses []string
	args    []any
}

func newTxnFilter(userID uuid.UUID) *txnFilter {
	return &txnFilter{args: []any{userID}}
}

// param appends a predicate containing a single %d, substituted with the next
// positional placeholder, and binds value.
func (f *txnFilter) param(clause string, value any) {
	f.args = append(f.args, value)
	f.clauses = append(f.clauses, fmt.Sprintf(clause, len(f.args)))
}

// raw appends a predicate with no bound parameter.
func (f *txnFilter) raw(clause string) {
	f.clauses = append(f.clauses, clause)
}

// where renders the shared " WHERE t.user_id = $1 [AND ...]" fragment.
func (f *txnFilter) where() string {
	where := " WHERE t.user_id = $1"
	if len(f.clauses) > 0 {
		where += " AND " + strings.Join(f.clauses, " AND ")
	}
	return where
}

// CreateTransaction validates and inserts a single transaction. It auto-applies
// the first matching categorization rule when no category is supplied, enforces
// ownership of the account/category/payee/billing cycle, and attaches
// credit-card transactions to a (possibly auto-generated) billing cycle. The
// whole write runs in one transaction.
func (srv *Server) CreateTransaction(c *gin.Context) {
	var req models.CreateTransactionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		validation.RespondBindError(c, err)
		return
	}

	if req.Type != "debit" && req.Type != "credit" {
		validation.RespondError(c, "type must be 'debit' or 'credit'", http.StatusBadRequest)
		return
	}
	if req.Amount <= 0 {
		validation.RespondError(c, "amount must be positive", http.StatusBadRequest)
		return
	}
	if _, err := time.Parse("2006-01-02", req.Date); err != nil {
		validation.RespondError(c, "invalid date (expected YYYY-MM-DD)", http.StatusBadRequest)
		return
	}

	userID := auth.GetUserID(c)

	// Run the whole write (account check, insert, billing-cycle generation and
	// assignment) inside one database transaction so a failure never leaves a
	// half-persisted transaction behind.
	tx, err := srv.db.Begin(c)
	if err != nil {
		slog.Error("CreateTransaction (begin)", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	defer tx.Rollback(c)

	// The account must exist and belong to the authenticated user. Also fetch
	// its billing day (so transactions can be attached to a billing cycle),
	// its closed flag, and its account type (loan accounts hold no
	// transactions of their own).
	var ownerID uuid.UUID
	var billingDay *int
	var closed bool
	var accountTypeID string
	err = tx.QueryRow(c,
		"SELECT user_id, billing_day, closed, account_type_id FROM accounts WHERE id = $1",
		req.AccountID).Scan(&ownerID, &billingDay, &closed, &accountTypeID)
	if errors.Is(err, pgx.ErrNoRows) {
		validation.RespondError(c, "account not found", http.StatusNotFound)
		return
	}
	if err != nil {
		slog.Error("CreateTransaction (checking account)", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	if ownerID != userID {
		validation.RespondError(c, "forbidden", http.StatusForbidden)
		return
	}
	if closed {
		validation.RespondError(c, "account is closed — no transactions can be added", http.StatusConflict)
		return
	}
	if accountTypeID == loanAccountTypeID {
		validation.RespondError(c, "loan accounts cannot have transactions — link EMI payments from other accounts instead", http.StatusBadRequest)
		return
	}

	// Auto-categorize from rules when no explicit category is supplied.
	categoryID, payeeID := req.CategoryID, req.PayeeID
	if categoryID == nil {
		rules, err := srv.loadRules(c, userID)
		if err != nil {
			slog.Error("CreateTransaction (getting rules)", slog.String("error", err.Error()))
			validation.RespondError(c, "internal server error", http.StatusInternalServerError)
			return
		}
		matchedCat, matchedPayee := autoCategorize(rules, req.Description)
		categoryID = matchedCat
		if payeeID == nil {
			payeeID = matchedPayee
		}
	}

	// The explicitly chosen billing cycle must belong to this user AND to the
	// transaction's own account, otherwise cycle totals for another account
	// would be corrupted.
	if billingDay != nil && req.BillingCycleID != nil {
		var owned bool
		err := tx.QueryRow(c,
			"SELECT EXISTS(SELECT 1 FROM billing_cycles bc WHERE bc.id = $1 AND bc.user_id = $2 AND bc.account_id = $3)",
			*req.BillingCycleID, userID, req.AccountID).Scan(&owned)
		if err != nil {
			slog.Error("CreateTransaction (checking billing cycle)", slog.String("error", err.Error()))
			validation.RespondError(c, "internal server error", http.StatusInternalServerError)
			return
		}
		if !owned {
			validation.RespondError(c, "billing cycle not found", http.StatusBadRequest)
			return
		}
	}

	// Insert, but only when any supplied category/payee belongs to this user.
	// Rules-derived values are already user-scoped, so the predicates only
	// reject explicit cross-user references.
	var id uuid.UUID
	err = tx.QueryRow(c,
		`INSERT INTO transactions (account_id, user_id, date, description, amount, type, category_id, payee_id, tags, notes)
		 SELECT $1, $2, $3, $4, $5, $6, $7, $8, $9, $10
		 WHERE ($7::uuid IS NULL OR EXISTS (SELECT 1 FROM categories c WHERE c.id = $7 AND (c.user_id = $2 OR c.user_id IS NULL)))
		   AND ($8::uuid IS NULL OR EXISTS (SELECT 1 FROM payees p WHERE p.id = $8 AND p.user_id = $2))
		 RETURNING id`,
		req.AccountID, userID, req.Date, req.Description, req.Amount, req.Type, categoryID, payeeID, req.Tags, req.Notes).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		validation.RespondError(c, "referenced category or payee not found", http.StatusBadRequest)
		return
	}
	if err != nil {
		slog.Error("CreateTransaction (insert)", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	// Transactions on accounts with a billing day are attached to a billing
	// cycle: by default the cycle matching the transaction date (the suggested
	// default), or the explicitly chosen cycle when the client supplied one.
	// Cycles are only generated for accounts that have a billing day set.
	if billingDay != nil {
		if err := ensureBillingCycles(c, tx, userID, req.AccountID, *billingDay); err != nil {
			slog.Error("CreateTransaction (ensure billing cycles)", slog.String("error", err.Error()))
			validation.RespondError(c, "internal server error", http.StatusInternalServerError)
			return
		}
		if req.BillingCycleID != nil {
			if _, err := tx.Exec(c,
				"UPDATE transactions SET billing_cycle_id = $1 WHERE id = $2 AND user_id = $3",
				*req.BillingCycleID, id, userID); err != nil {
				slog.Error("CreateTransaction (set billing cycle)", slog.String("error", err.Error()))
				validation.RespondError(c, "internal server error", http.StatusInternalServerError)
				return
			}
		}
	}

	if err := tx.Commit(c); err != nil {
		slog.Error("CreateTransaction (commit)", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	c.JSON(http.StatusCreated, gin.H{"id": id})
}

// UpdateTransaction applies a partial update to a transaction. Only fields
// present in the request are changed; OptionalUUID fields let an explicit null
// clear a foreign key. Any account/category/payee/billing cycle referenced must
// belong to the user, otherwise the update is rejected.
func (srv *Server) UpdateTransaction(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		validation.RespondError(c, "invalid id", http.StatusBadRequest)
		return
	}

	var req models.UpdateTransactionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		validation.RespondBindError(c, err)
		return
	}

	// Validate date/amount the same way CreateTransaction does: a PATCH must
	// not bypass the create-path checks with an invalid date (which would
	// surface as a driver error, i.e. a 500) or a zero/negative amount (which
	// would corrupt balance math and billing-cycle totals).
	if req.Date != nil {
		if _, err := time.Parse("2006-01-02", *req.Date); err != nil {
			validation.RespondError(c, "invalid date (expected YYYY-MM-DD)", http.StatusBadRequest)
			return
		}
	}
	if req.Amount != nil {
		if *req.Amount <= 0 {
			validation.RespondError(c, "amount must be positive", http.StatusBadRequest)
			return
		}
	}

	// Build dynamic SET clauses
	setClauses := []string{}
	args := []any{}
	paramIdx := 1

	// Record the parameter index of each ownership-checked FK being set to a
	// non-null value so the WHERE clause can constrain it to the same user.
	var accountParam, categoryParam, payeeParam, cycleParam int

	// Conditionally update fields based on presence in the request.
	// CategoryID/PayeeID use OptionalUUID so that an explicit `null`
	// (clear the field) can be distinguished from an absent key.
	if req.CategoryID.Set() {
		setClauses = append(setClauses, fmt.Sprintf("category_id = $%d", paramIdx))
		if v := req.CategoryID.Value(); v != nil {
			args = append(args, *v)
			categoryParam = paramIdx
		} else {
			args = append(args, nil)
		}
		paramIdx++
	}
	if req.Tags != nil {
		setClauses = append(setClauses, fmt.Sprintf("tags = $%d", paramIdx))
		args = append(args, *req.Tags)
		paramIdx++
	}
	if req.Notes != nil {
		setClauses = append(setClauses, fmt.Sprintf("notes = $%d", paramIdx))
		args = append(args, *req.Notes)
		paramIdx++
	}
	if req.PayeeID.Set() {
		setClauses = append(setClauses, fmt.Sprintf("payee_id = $%d", paramIdx))
		if v := req.PayeeID.Value(); v != nil {
			args = append(args, *v)
			payeeParam = paramIdx
		} else {
			args = append(args, nil)
		}
		paramIdx++
	}
	if req.Date != nil {
		setClauses = append(setClauses, fmt.Sprintf("date = $%d", paramIdx))
		args = append(args, *req.Date)
		paramIdx++
	}
	if req.Description != nil {
		setClauses = append(setClauses, fmt.Sprintf("description = $%d", paramIdx))
		args = append(args, *req.Description)
		paramIdx++
	}
	if req.Amount != nil {
		setClauses = append(setClauses, fmt.Sprintf("amount = $%d", paramIdx))
		args = append(args, *req.Amount)
		paramIdx++
	}
	if req.Type != nil {
		if *req.Type != "debit" && *req.Type != "credit" {
			validation.RespondError(c, "type must be 'debit' or 'credit'", http.StatusBadRequest)
			return
		}
		setClauses = append(setClauses, fmt.Sprintf("type = $%d", paramIdx))
		args = append(args, *req.Type)
		paramIdx++
	}
	if req.AccountID != nil {
		setClauses = append(setClauses, fmt.Sprintf("account_id = $%d", paramIdx))
		args = append(args, *req.AccountID)
		accountParam = paramIdx
		paramIdx++
	}
	if req.BillingCycleID.Set() {
		setClauses = append(setClauses, fmt.Sprintf("billing_cycle_id = $%d", paramIdx))
		if v := req.BillingCycleID.Value(); v != nil {
			args = append(args, *v)
			cycleParam = paramIdx
		} else {
			args = append(args, nil)
		}
		paramIdx++
	}

	if len(setClauses) == 0 {
		validation.RespondError(c, "no fields to update", http.StatusBadRequest)
		return
	}

	// WHERE id = $N AND user_id = $N+1, plus an ownership predicate for each
	// FK being set to a non-null value so a user can't point their transaction
	// at another user's account/category/payee/billing cycle.
	idIdx := paramIdx
	args = append(args, id)
	paramIdx++
	userIdx := paramIdx
	args = append(args, auth.GetUserID(c))

	where := fmt.Sprintf("WHERE id = $%d AND user_id = $%d", idIdx, userIdx)
	// Transactions on closed accounts are immutable (only linking remains
	// possible): an update that touches such a row is a no-op.
	where += " AND NOT EXISTS (SELECT 1 FROM accounts closed_acct WHERE closed_acct.id = transactions.account_id AND closed_acct.closed)"
	if accountParam != 0 {
		// The target account must be owned, open, and not a loan account
		// (loan/EMI accounts hold no transactions of their own).
		where += fmt.Sprintf(" AND EXISTS (SELECT 1 FROM accounts a WHERE a.id = $%d AND a.user_id = $%d AND NOT a.closed AND a.account_type_id <> '%s')", accountParam, userIdx, loanAccountTypeID)
	}
	if categoryParam != 0 {
		where += fmt.Sprintf(" AND EXISTS (SELECT 1 FROM categories ct WHERE ct.id = $%d AND (ct.user_id = $%d OR ct.user_id IS NULL))", categoryParam, userIdx)
	}
	if payeeParam != 0 {
		where += fmt.Sprintf(" AND EXISTS (SELECT 1 FROM payees py WHERE py.id = $%d AND py.user_id = $%d)", payeeParam, userIdx)
	}
	if cycleParam != 0 {
		// The cycle must belong to the user AND to the transaction's account.
		// When the account is being changed in the same UPDATE, the WHERE
		// clause sees the old account_id, so compare against the new one.
		if accountParam != 0 {
			where += fmt.Sprintf(" AND EXISTS (SELECT 1 FROM billing_cycles bc WHERE bc.id = $%d AND bc.user_id = $%d AND bc.account_id = $%d)", cycleParam, userIdx, accountParam)
		} else {
			where += fmt.Sprintf(" AND EXISTS (SELECT 1 FROM billing_cycles bc WHERE bc.id = $%d AND bc.user_id = $%d AND bc.account_id = transactions.account_id)", cycleParam, userIdx)
		}
	}

	query := fmt.Sprintf("UPDATE transactions SET %s %s", strings.Join(setClauses, ", "), where)

	result, err := srv.db.Exec(c, query, args...)
	if err != nil {
		slog.Error("UpdateTransaction", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	if result.RowsAffected() == 0 {
		validation.RespondError(c, "transaction or referenced resource not found", http.StatusNotFound)
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "updated"})
}

// DeleteTransaction removes a single transaction owned by the user.
func (srv *Server) DeleteTransaction(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		validation.RespondError(c, "invalid id", http.StatusBadRequest)
		return
	}

	userID := auth.GetUserID(c)
	// Transactions on closed accounts are immutable (only linking remains
	// possible), so the delete becomes a no-op for them.
	result, err := srv.db.Exec(c,
		`DELETE FROM transactions WHERE id = $1 AND user_id = $2
		 AND NOT EXISTS (SELECT 1 FROM accounts closed_acct WHERE closed_acct.id = transactions.account_id AND closed_acct.closed)`,
		id, userID)
	if err != nil {
		slog.Error("DeleteTransaction", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	if result.RowsAffected() == 0 {
		validation.RespondError(c, "transaction not found", http.StatusNotFound)
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "deleted"})
}
