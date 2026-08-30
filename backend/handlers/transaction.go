package handlers

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/fintrak/backend/auth"
	"github.com/fintrak/backend/db"
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

// GetTransactions returns a paginated, filterable list of the user's
// transactions. Filters cover account, category, payee, free-text description,
// date range, type, exact amount, and linked state; sorting and pagination are
// validated/clamped server-side. When filtering a single account that has a
// billing day set (any account type) and sorting by date, synthetic summary
// rows (per-cycle outstanding totals) are merged into the response.
func GetTransactions(c *gin.Context) {
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
	// Clamp the page size so a crafted request can't bypass the frontend limit
	// (0 or negative values fall back to the default).
	if limit < 1 {
		limit = 50
	}
	if limit > maxPageSize {
		limit = maxPageSize
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

	query := `SELECT t.id, t.account_id, t.date, t.description, t.amount, t.type, t.category_id,
				t.tags, t.notes, t.payee_id, COALESCE(p.name, '') as payee, t.created_at, a.name as account_name,
				COALESCE(c.name, '') as category_name, COALESCE(c.icon, '') as category_icon,
			  COALESCE(c.color, '') as category_color,
			  EXISTS(SELECT 1 FROM links WHERE from_txn_id = t.id OR to_txn_id = t.id) as is_linked,
			  t.billing_cycle_id,
			  COALESCE(bc.label, '') as billing_cycle_label
			  FROM transactions t
			  JOIN accounts a ON t.account_id = a.id
			  LEFT JOIN categories c ON t.category_id = c.id
			  LEFT JOIN payees p ON t.payee_id = p.id
			  LEFT JOIN billing_cycles bc ON t.billing_cycle_id = bc.id
			  WHERE t.user_id = $1`

	countQuery := `SELECT COUNT(*) FROM transactions t WHERE t.user_id = $1`
	args := []any{userID}
	countArgs := []any{userID}
	paramIdx := 2

	if accountID != "" {
		query += fmt.Sprintf(" AND t.account_id = $%d", paramIdx)
		countQuery += fmt.Sprintf(" AND t.account_id = $%d", paramIdx)
		args = append(args, accountID)
		countArgs = append(countArgs, accountID)
		paramIdx++
	}
	if categoryID != "" {
		// The "uncategorized" sentinel (from the frontend's category filter)
		// means transactions with no category assigned.
		if categoryID == "uncategorized" {
			query += " AND t.category_id IS NULL"
			countQuery += " AND t.category_id IS NULL"
		} else if _, err := uuid.Parse(categoryID); err != nil {
			// Not a UUID -> group-level filter: every category in the given
			// group (a base group slug like "expense" or a custom group id).
			query += fmt.Sprintf(" AND c.group_id = $%d", paramIdx)
			countQuery += fmt.Sprintf(" AND EXISTS (SELECT 1 FROM categories cat WHERE cat.id = t.category_id AND cat.group_id = $%d)", paramIdx)
			args = append(args, categoryID)
			countArgs = append(countArgs, categoryID)
			paramIdx++
		} else {
			// Filter to the selected category (scoped to the user). Categories
			// are flat, so this is a plain equality against the category id.
			query += fmt.Sprintf(" AND t.category_id = $%d", paramIdx)
			countQuery += fmt.Sprintf(" AND t.category_id = $%d", paramIdx)
			args = append(args, categoryID)
			countArgs = append(countArgs, categoryID)
			paramIdx++
		}
	}
	if uncategorized == "true" {
		query += " AND t.category_id IS NULL"
		countQuery += " AND t.category_id IS NULL"
	}
	if groupId != "" {
		// Group-level filter: every transaction whose category belongs to the
		// given group. Works for base group slugs ("expense") and custom group
		// ids alike, unlike the non-UUID fallback on categoryId.
		query += fmt.Sprintf(" AND c.group_id = $%d", paramIdx)
		countQuery += fmt.Sprintf(" AND EXISTS (SELECT 1 FROM categories cat WHERE cat.id = t.category_id AND cat.group_id = $%d)", paramIdx)
		args = append(args, groupId)
		countArgs = append(countArgs, groupId)
		paramIdx++
	}
	if search != "" {
		query += fmt.Sprintf(" AND LOWER(t.description) LIKE LOWER($%d)", paramIdx)
		countQuery += fmt.Sprintf(" AND LOWER(t.description) LIKE LOWER($%d)", paramIdx)
		// Escape % and _ so "100%" matches the literal text, not "1000" —
		// same semantics as the rules engine's matchRule.
		args = append(args, "%"+escapeLikePattern(search)+"%")
		countArgs = append(countArgs, "%"+escapeLikePattern(search)+"%")
		paramIdx++
	}
	if dateFrom != "" {
		query += fmt.Sprintf(" AND t.date >= $%d", paramIdx)
		countQuery += fmt.Sprintf(" AND t.date >= $%d", paramIdx)
		args = append(args, dateFrom)
		countArgs = append(countArgs, dateFrom)
		paramIdx++
	}
	if dateTo != "" {
		query += fmt.Sprintf(" AND t.date <= $%d", paramIdx)
		countQuery += fmt.Sprintf(" AND t.date <= $%d", paramIdx)
		args = append(args, dateTo)
		countArgs = append(countArgs, dateTo)
		paramIdx++
	}
	if txnType != "" {
		query += fmt.Sprintf(" AND t.type = $%d", paramIdx)
		countQuery += fmt.Sprintf(" AND t.type = $%d", paramIdx)
		args = append(args, txnType)
		countArgs = append(countArgs, txnType)
		paramIdx++
	}
	if payeeID != "" {
		query += fmt.Sprintf(" AND t.payee_id = $%d", paramIdx)
		countQuery += fmt.Sprintf(" AND t.payee_id = $%d", paramIdx)
		args = append(args, payeeID)
		countArgs = append(countArgs, payeeID)
		paramIdx++
	}
	if amountStr != "" {
		if amount, err := strconv.ParseFloat(amountStr, 64); err == nil {
			query += fmt.Sprintf(" AND t.amount = $%d", paramIdx)
			countQuery += fmt.Sprintf(" AND t.amount = $%d", paramIdx)
			args = append(args, amount)
			countArgs = append(countArgs, amount)
			paramIdx++
		}
	}

	linked := c.Query("linked")
	switch linked {
	case "true":
		query += " AND EXISTS(SELECT 1 FROM links WHERE from_txn_id = t.id OR to_txn_id = t.id)"
		countQuery += " AND EXISTS(SELECT 1 FROM links WHERE from_txn_id = t.id OR to_txn_id = t.id)"
	case "false":
		query += " AND NOT EXISTS(SELECT 1 FROM links WHERE from_txn_id = t.id OR to_txn_id = t.id)"
		countQuery += " AND NOT EXISTS(SELECT 1 FROM links WHERE from_txn_id = t.id OR to_txn_id = t.id)"
	}

	// Get total count
	var total int
	db.Pool.QueryRow(c, countQuery, countArgs...).Scan(&total)

	offset := (page - 1) * limit
	query += fmt.Sprintf(" ORDER BY %s %s LIMIT $%d OFFSET $%d", sortCol, sortOrder, paramIdx, paramIdx+1)
	args = append(args, limit, offset)

	rows, err := db.Pool.Query(c, query, args...)
	if err != nil {
		slog.Error("GetTransactions", "error", err)
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
			&t.BillingCycleID, &t.BillingCycleLabel); err != nil {
			slog.Error("GetTransactions scan", "error", err)
			validation.RespondError(c, "internal server error", http.StatusInternalServerError)
			return
		}
		transactions = append(transactions, t)
	}

	// When a single account is filtered and sorted by date, inject computed
	// summary rows: the per-cycle total outstanding for accounts that have a
	// billing day set. These are synthetic and are never persisted. Summary
	// rows only make sense in a date-ordered list, so other sort columns skip
	// them; accounts without a billing day show their raw transactions only.
	if accountID != "" && sortBy == "date" {
		summaryTxns := buildAccountSummaryRows(c, userID, accountID, dateFrom, dateTo)
		transactions = mergeSummaryRows(transactions, summaryTxns, sortBy, sortOrder)
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

// CreateTransaction validates and inserts a single transaction. It auto-applies
// the first matching categorization rule when no category is supplied, enforces
// ownership of the account/category/payee/billing cycle, and attaches
// credit-card transactions to a (possibly auto-generated) billing cycle. The
// whole write runs in one transaction.
func CreateTransaction(c *gin.Context) {
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
	tx, err := db.Pool.Begin(c)
	if err != nil {
		slog.Error("CreateTransaction (begin)", "error", err)
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	defer tx.Rollback(c)

	// The account must exist and belong to the authenticated user. Also fetch its
	// billing day so transactions can be attached to a billing cycle (cycles
	// only exist for accounts with a billing day set).
	var ownerID uuid.UUID
	var billingDay *int
	err = tx.QueryRow(c,
		"SELECT user_id, billing_day FROM accounts WHERE id = $1",
		req.AccountID).Scan(&ownerID, &billingDay)
	if errors.Is(err, pgx.ErrNoRows) {
		validation.RespondError(c, "account not found", http.StatusNotFound)
		return
	}
	if err != nil {
		slog.Error("CreateTransaction (checking account)", "error", err)
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	if ownerID != userID {
		validation.RespondError(c, "forbidden", http.StatusForbidden)
		return
	}

	// Auto-categorize from rules when no explicit category is supplied.
	categoryID, payeeID := req.CategoryID, req.PayeeID
	if categoryID == nil {
		rules, err := loadRules(c, userID)
		if err != nil {
			slog.Error("CreateTransaction (getting rules)", "error", err)
			validation.RespondError(c, "internal server error", http.StatusInternalServerError)
			return
		}
		matchedCat, matchedPayee := autoCategorize(rules, req.Description)
		categoryID = matchedCat
		if payeeID == nil {
			payeeID = matchedPayee
		}
	}

	// The explicitly chosen billing cycle must belong to this user, otherwise
	// the assignment below would silently no-op.
	if billingDay != nil && req.BillingCycleID != nil {
		var owned bool
		err := tx.QueryRow(c,
			"SELECT EXISTS(SELECT 1 FROM billing_cycles bc WHERE bc.id = $1 AND bc.user_id = $2)",
			*req.BillingCycleID, userID).Scan(&owned)
		if err != nil {
			slog.Error("CreateTransaction (checking billing cycle)", "error", err)
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
		slog.Error("CreateTransaction (insert)", "error", err)
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	// Transactions on accounts with a billing day are attached to a billing
	// cycle: by default the cycle matching the transaction date (the suggested
	// default), or the explicitly chosen cycle when the client supplied one.
	// Cycles are only generated for accounts that have a billing day set.
	if billingDay != nil {
		if err := ensureBillingCycles(c, tx, userID, req.AccountID, *billingDay); err != nil {
			slog.Error("CreateTransaction (ensure billing cycles)", "error", err)
			validation.RespondError(c, "internal server error", http.StatusInternalServerError)
			return
		}
		if req.BillingCycleID != nil {
			if _, err := tx.Exec(c,
				"UPDATE transactions SET billing_cycle_id = $1 WHERE id = $2 AND user_id = $3",
				*req.BillingCycleID, id, userID); err != nil {
				slog.Error("CreateTransaction (set billing cycle)", "error", err)
				validation.RespondError(c, "internal server error", http.StatusInternalServerError)
				return
			}
		}
	}

	if err := tx.Commit(c); err != nil {
		slog.Error("CreateTransaction (commit)", "error", err)
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	c.JSON(http.StatusCreated, gin.H{"id": id})
}

// UpdateTransaction applies a partial update to a transaction. Only fields
// present in the request are changed; OptionalUUID fields let an explicit null
// clear a foreign key. Any account/category/payee/billing cycle referenced must
// belong to the user, otherwise the update is rejected.
func UpdateTransaction(c *gin.Context) {
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
	if accountParam != 0 {
		where += fmt.Sprintf(" AND EXISTS (SELECT 1 FROM accounts a WHERE a.id = $%d AND a.user_id = $%d)", accountParam, userIdx)
	}
	if categoryParam != 0 {
		where += fmt.Sprintf(" AND EXISTS (SELECT 1 FROM categories ct WHERE ct.id = $%d AND (ct.user_id = $%d OR ct.user_id IS NULL))", categoryParam, userIdx)
	}
	if payeeParam != 0 {
		where += fmt.Sprintf(" AND EXISTS (SELECT 1 FROM payees py WHERE py.id = $%d AND py.user_id = $%d)", payeeParam, userIdx)
	}
	if cycleParam != 0 {
		where += fmt.Sprintf(" AND EXISTS (SELECT 1 FROM billing_cycles bc WHERE bc.id = $%d AND bc.user_id = $%d)", cycleParam, userIdx)
	}

	query := fmt.Sprintf("UPDATE transactions SET %s %s", strings.Join(setClauses, ", "), where)

	result, err := db.Pool.Exec(c, query, args...)
	if err != nil {
		slog.Error("UpdateTransaction", "error", err)
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	if result.RowsAffected() == 0 {
		validation.RespondError(c, "transaction or referenced resource not found", http.StatusNotFound)
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "updated"})
}

// BulkCategorize assigns one category to many of the user's transactions in a
// single UPDATE.
func BulkCategorize(c *gin.Context) {
	var req models.BulkCategorizeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		validation.RespondBindError(c, err)
		return
	}
	if len(req.TransactionIDs) > maxBulkBatch {
		validation.RespondError(c, fmt.Sprintf("too many transaction ids (max %d per request)", maxBulkBatch), http.StatusBadRequest)
		return
	}

	// The "uncategorized" sentinel clears the category on every selected
	// transaction; otherwise the target category must exist and be the user's.
	query := `UPDATE transactions SET category_id = NULL
	          WHERE id = ANY($1) AND user_id = $2`
	args := []interface{}{req.TransactionIDs, auth.GetUserID(c)}
	if req.CategoryID != "uncategorized" {
		catUUID, err := uuid.Parse(req.CategoryID)
		if err != nil {
			validation.RespondError(c, "invalid category id", http.StatusBadRequest)
			return
		}
		query = `UPDATE transactions SET category_id = $1
		          WHERE id = ANY($2) AND user_id = $3
		            AND EXISTS (SELECT 1 FROM categories c WHERE c.id = $1 AND (c.user_id = $3 OR c.user_id IS NULL))`
		args = []interface{}{catUUID, req.TransactionIDs, auth.GetUserID(c)}
	}
	result, err := db.Pool.Exec(c, query, args...)
	if err != nil {
		slog.Error("BulkCategorize", "error", err)
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	c.JSON(http.StatusOK, gin.H{"updated": result.RowsAffected()})
}

// BulkUpdatePayee assigns one payee to many of the user's transactions in a
// single UPDATE.
func BulkUpdatePayee(c *gin.Context) {
	var req models.BulkUpdatePayeeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		validation.RespondBindError(c, err)
		return
	}
	if len(req.TransactionIDs) > maxBulkBatch {
		validation.RespondError(c, fmt.Sprintf("too many transaction ids (max %d per request)", maxBulkBatch), http.StatusBadRequest)
		return
	}

	query := `UPDATE transactions SET payee_id = $1
	          WHERE id = ANY($2) AND user_id = $3
	            AND EXISTS (SELECT 1 FROM payees p WHERE p.id = $1 AND p.user_id = $3)`
	result, err := db.Pool.Exec(c, query, req.PayeeID, req.TransactionIDs, auth.GetUserID(c))
	if err != nil {
		slog.Error("BulkUpdatePayee", "error", err)
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	c.JSON(http.StatusOK, gin.H{"updated": result.RowsAffected()})
}

// BulkUpdateBillingCycle attaches one billing cycle to many of the user's
// transactions in a single UPDATE.
func BulkUpdateBillingCycle(c *gin.Context) {
	var req models.BulkBillingCycleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		validation.RespondBindError(c, err)
		return
	}
	if len(req.TransactionIDs) > maxBulkBatch {
		validation.RespondError(c, fmt.Sprintf("too many transaction ids (max %d per request)", maxBulkBatch), http.StatusBadRequest)
		return
	}

	query := `UPDATE transactions SET billing_cycle_id = $1
	          WHERE id = ANY($2) AND user_id = $3
	            AND EXISTS (SELECT 1 FROM billing_cycles bc WHERE bc.id = $1 AND bc.user_id = $3)`
	result, err := db.Pool.Exec(c, query, req.BillingCycleID, req.TransactionIDs, auth.GetUserID(c))
	if err != nil {
		slog.Error("BulkUpdateBillingCycle", "error", err)
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	c.JSON(http.StatusOK, gin.H{"updated": result.RowsAffected()})
}

// BulkDeleteTransactions deletes many of the user's transactions in one call.
func BulkDeleteTransactions(c *gin.Context) {
	var req models.BulkDeleteTransactionsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		validation.RespondBindError(c, err)
		return
	}
	if len(req.TransactionIDs) > maxBulkBatch {
		validation.RespondError(c, fmt.Sprintf("too many transaction ids (max %d per request)", maxBulkBatch), http.StatusBadRequest)
		return
	}

	query := "DELETE FROM transactions WHERE id = ANY($1) AND user_id = $2"
	result, err := db.Pool.Exec(c, query, req.TransactionIDs, auth.GetUserID(c))
	if err != nil {
		slog.Error("BulkDeleteTransactions", "error", err)
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	c.JSON(http.StatusOK, gin.H{"deleted": result.RowsAffected()})
}

// DeleteTransaction removes a single transaction owned by the user.
func DeleteTransaction(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		validation.RespondError(c, "invalid id", http.StatusBadRequest)
		return
	}

	userID := auth.GetUserID(c)
	result, err := db.Pool.Exec(c, "DELETE FROM transactions WHERE id = $1 AND user_id = $2", id, userID)
	if err != nil {
		slog.Error("DeleteTransaction", "error", err)
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	if result.RowsAffected() == 0 {
		validation.RespondError(c, "transaction not found", http.StatusNotFound)
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "deleted"})
}

// ImportTransactions batch-inserts a validated list of transactions for an
// account. It enforces payload bounds and ownership of the account, billing
// cycle, and any explicit payees, deduplicates rows when duplicateAction is
// "skip", applies categorization rules in memory, and commits everything in one
// transaction. Credit-card imports are attached to billing cycles, and source
// Paperless documents are tagged only after the commit succeeds.
func ImportTransactions(c *gin.Context) {
	var req models.ImportRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		validation.RespondBindError(c, err)
		return
	}

	userID := auth.GetUserID(c)

	// Validate payload before touching the database.
	if len(req.Transactions) == 0 {
		validation.RespondError(c, "no transactions to import", http.StatusBadRequest)
		return
	}
	if len(req.Transactions) > maxImportBatch {
		validation.RespondError(c, fmt.Sprintf("too many transactions (max %d per import)", maxImportBatch), http.StatusBadRequest)
		return
	}
	action := req.DuplicateAction
	if action != "" && action != "skip" && action != "keep" {
		validation.RespondError(c, "duplicateAction must be 'skip' or 'keep'", http.StatusBadRequest)
		return
	}
	for i, t := range req.Transactions {
		if t.Type != "debit" && t.Type != "credit" {
			validation.RespondError(c, fmt.Sprintf("transaction %d has invalid type '%s' (must be 'debit' or 'credit')", i+1, t.Type), http.StatusBadRequest)
			return
		}
		if t.Amount <= 0 {
			validation.RespondError(c, fmt.Sprintf("transaction %d has invalid amount %v", i+1, t.Amount), http.StatusBadRequest)
			return
		}
		if _, err := time.Parse("2006-01-02", t.Date); err != nil {
			validation.RespondError(c, fmt.Sprintf("transaction %d has invalid date '%s' (expected YYYY-MM-DD)", i+1, t.Date), http.StatusBadRequest)
			return
		}
	}

	// The account must exist and belong to the authenticated user. Also fetch its
	// billing day so imports can be attached to a billing cycle.
	var ownerID uuid.UUID
	var billingDay *int
	err := db.Pool.QueryRow(c,
		"SELECT user_id, billing_day FROM accounts WHERE id = $1",
		req.AccountID).Scan(&ownerID, &billingDay)
	if errors.Is(err, pgx.ErrNoRows) {
		validation.RespondError(c, "account not found", http.StatusNotFound)
		return
	}
	if err != nil {
		slog.Error("ImportTransactions (checking account)", "error", err)
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	if ownerID != userID {
		validation.RespondError(c, "forbidden", http.StatusForbidden)
		return
	}

	// Validate that any explicitly supplied billing cycle and payees belong to
	// this user, so a client can't import against another user's records.
	if req.BillingCycleID != nil {
		var owned bool
		err := db.Pool.QueryRow(c,
			"SELECT EXISTS(SELECT 1 FROM billing_cycles bc WHERE bc.id = $1 AND bc.user_id = $2)",
			*req.BillingCycleID, userID).Scan(&owned)
		if err != nil {
			slog.Error("ImportTransactions (checking billing cycle)", "error", err)
			validation.RespondError(c, "internal server error", http.StatusInternalServerError)
			return
		}
		if !owned {
			validation.RespondError(c, "billing cycle not found", http.StatusBadRequest)
			return
		}
	}

	// Collect the distinct explicitly supplied payees (rules-derived payees are
	// user-scoped already) and confirm every one belongs to this user.
	payeeIDs := make([]uuid.UUID, 0, len(req.Transactions))
	seen := map[uuid.UUID]bool{}
	for _, t := range req.Transactions {
		if t.PayeeID != nil && !seen[*t.PayeeID] {
			seen[*t.PayeeID] = true
			payeeIDs = append(payeeIDs, *t.PayeeID)
		}
	}
	if len(payeeIDs) > 0 {
		var owned int
		err := db.Pool.QueryRow(c,
			"SELECT COUNT(*) FROM payees WHERE id = ANY($1) AND user_id = $2",
			payeeIDs, userID).Scan(&owned)
		if err != nil {
			slog.Error("ImportTransactions (checking payees)", "error", err)
			validation.RespondError(c, "internal server error", http.StatusInternalServerError)
			return
		}
		if owned != len(payeeIDs) {
			validation.RespondError(c, "referenced payee not found", http.StatusBadRequest)
			return
		}
	}

	// Load rules once and match in memory to avoid N+1 queries.
	rules, err := loadRules(c, userID)
	if err != nil {
		slog.Error("ImportTransactions (getting rules)", "error", err)
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	// Run the whole import in a transaction so it is all-or-nothing.
	tx, err := db.Pool.Begin(c)
	if err != nil {
		slog.Error("ImportTransactions (begin)", "error", err)
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	defer tx.Rollback(c)

	// When the user asks to skip duplicates, load the existing transactions for
	// this account so we can compare against a consistent snapshot.
	existing := map[string]bool{}
	if action == "skip" {
		rows, err := tx.Query(c,
			"SELECT date, amount, type, description FROM transactions WHERE account_id = $1 AND user_id = $2",
			req.AccountID, userID)
		if err != nil {
			slog.Error("ImportTransactions (loading existing transactions)", "error", err)
			validation.RespondError(c, "internal server error", http.StatusInternalServerError)
			return
		}
		for rows.Next() {
			var d time.Time
			var amount float64
			var typ, description string
			if err := rows.Scan(&d, &amount, &typ, &description); err != nil {
				rows.Close()
				slog.Error("ImportTransactions (scanning existing transactions)", "error", err)
				validation.RespondError(c, "internal server error", http.StatusInternalServerError)
				return
			}
			existing[transactionFingerprint(d.Format("2006-01-02"), amount, typ, description)] = true
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			slog.Error("ImportTransactions (iterating existing transactions)", "error", err)
			validation.RespondError(c, "internal server error", http.StatusInternalServerError)
			return
		}
	}

	// If the user chose to skip duplicates, drop the rows that match an existing
	// transaction or repeat earlier in the same batch.
	insertTxns := req.Transactions
	duplicates := 0
	if action == "skip" {
		insertTxns, duplicates = dedupeTransactions(req.Transactions, existing)
	}

	batch := &pgx.Batch{}
	imported := 0
	for _, t := range insertTxns {
		categoryID, payeeID := autoCategorize(rules, t.Description)

		// If a rule gives no payee, fall back to the payee matched during import.
		if payeeID == nil && t.PayeeID != nil {
			payeeID = t.PayeeID
		}

		batch.Queue(
			`INSERT INTO transactions (account_id, user_id, date, description, amount, type, category_id, payee_id)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING id`,
			req.AccountID, userID, t.Date, t.Description, t.Amount, t.Type, categoryID, payeeID,
		)
		imported++
	}

	if imported > 0 {
		br := tx.SendBatch(c, batch)
		ids := make([]uuid.UUID, 0, imported)
		done := 0
		for done < imported {
			var id uuid.UUID
			if err := br.QueryRow().Scan(&id); err != nil {
				br.Close()
				slog.Error("ImportTransactions", "error", err)
				validation.RespondError(c, "internal server error", http.StatusInternalServerError)
				return
			}
			ids = append(ids, id)
			done++
		}
		br.Close()

		// Imports on accounts with a billing day: attach the new transactions
		// to their billing cycles. When the client chose an explicit cycle,
		// every imported transaction is attached to it (overriding the
		// date-based default); otherwise the suggested default (by transaction
		// date) applies. Runs inside the transaction so the assignment commits
		// atomically with the import.
		if billingDay != nil {
			if req.BillingCycleID != nil {
				if err := attachTransactionsToCycle(c, tx, *req.BillingCycleID, ids, userID); err != nil {
					slog.Error("ImportTransactions (set billing cycle)", "error", err)
					validation.RespondError(c, "internal server error", http.StatusInternalServerError)
					return
				}
			}
			// Cycles are only generated for accounts that have a billing day
			// set; the date-based default can't apply to accounts without one.
			if err := ensureBillingCycles(c, tx, userID, req.AccountID, *billingDay); err != nil {
				slog.Error("ImportTransactions (ensure billing cycles)", "error", err)
				validation.RespondError(c, "internal server error", http.StatusInternalServerError)
				return
			}
		}

		if err := tx.Commit(c); err != nil {
			slog.Error("ImportTransactions (commit)", "error", err)
			validation.RespondError(c, "internal server error", http.StatusInternalServerError)
			return
		}
	}

	// Tag the source Paperless documents only after the import has committed, so
	// the label reflects documents that were actually imported. Best-effort and
	// asynchronous: tagging costs up to four upstream HTTP round-trips per
	// document, so it must neither stall the response nor fail the import if
	// the user's Paperless instance is slow or unreachable. The detached
	// context outlives the request; tagPaperlessDocuments applies its own
	// overall timeout and bounded concurrency.
	if len(req.PaperlessDocumentIDs) > 0 {
		go tagPaperlessDocuments(context.Background(), userID, req.PaperlessDocumentIDs, c.GetString("tokenEncryptionKey"))
	}

	c.JSON(http.StatusOK, gin.H{
		"imported":   imported,
		"duplicates": duplicates,
		"total":      len(req.Transactions),
	})
	slog.Info("import complete", "imported", imported, "duplicates", duplicates, "total", len(req.Transactions), "account_id", req.AccountID)
}

// transactionFingerprint collapses a row into a stable value used for duplicate
// detection. Amounts are compared as integer cents so penny rounding and float
// noise don't produce false matches.
func transactionFingerprint(date string, amount float64, typ, description string) string {
	cents := int(math.Round(amount * 100))
	return fmt.Sprintf("%s\x00%d\x00%s\x00%s", date, cents, typ, strings.ToLower(strings.TrimSpace(description)))
}

// ValidateTransactions is a read-only check that reports which of the given
// candidate transactions already exist in the selected account. It reuses the
// same fingerprint matching as ImportTransactions (so the results agree with
// what an import with duplicateAction "skip" would drop) but writes nothing.
func ValidateTransactions(c *gin.Context) {
	var req models.ValidateTransactionsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		validation.RespondBindError(c, err)
		return
	}

	userID := auth.GetUserID(c)

	if len(req.Transactions) == 0 {
		validation.RespondError(c, "no transactions to validate", http.StatusBadRequest)
		return
	}
	if len(req.Transactions) > maxImportBatch {
		validation.RespondError(c, fmt.Sprintf("too many transactions (max %d per request)", maxImportBatch), http.StatusBadRequest)
		return
	}
	for i, t := range req.Transactions {
		if t.Type != "debit" && t.Type != "credit" {
			validation.RespondError(c, fmt.Sprintf("transaction %d has invalid type '%s' (must be 'debit' or 'credit')", i+1, t.Type), http.StatusBadRequest)
			return
		}
		if t.Amount <= 0 {
			validation.RespondError(c, fmt.Sprintf("transaction %d has invalid amount %v", i+1, t.Amount), http.StatusBadRequest)
			return
		}
		if _, err := time.Parse("2006-01-02", t.Date); err != nil {
			validation.RespondError(c, fmt.Sprintf("transaction %d has invalid date '%s' (expected YYYY-MM-DD)", i+1, t.Date), http.StatusBadRequest)
			return
		}
	}

	// The account must exist and belong to the authenticated user.
	var ownerID uuid.UUID
	err := db.Pool.QueryRow(c,
		"SELECT user_id FROM accounts WHERE id = $1",
		req.AccountID).Scan(&ownerID)
	if errors.Is(err, pgx.ErrNoRows) {
		validation.RespondError(c, "account not found", http.StatusNotFound)
		return
	}
	if err != nil {
		slog.Error("ValidateTransactions (checking account)", "error", err)
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	if ownerID != userID {
		validation.RespondError(c, "forbidden", http.StatusForbidden)
		return
	}

	// Load the account's existing transactions into a fingerprint set so each
	// candidate can be compared in memory.
	existing := map[string]bool{}
	rows, err := db.Pool.Query(c,
		"SELECT date, amount, type, description FROM transactions WHERE account_id = $1 AND user_id = $2",
		req.AccountID, userID)
	if err != nil {
		slog.Error("ValidateTransactions (loading existing transactions)", "error", err)
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	for rows.Next() {
		var d time.Time
		var amount float64
		var typ, description string
		if err := rows.Scan(&d, &amount, &typ, &description); err != nil {
			rows.Close()
			slog.Error("ValidateTransactions (scanning existing transactions)", "error", err)
			validation.RespondError(c, "internal server error", http.StatusInternalServerError)
			return
		}
		existing[transactionFingerprint(d.Format("2006-01-02"), amount, typ, description)] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		slog.Error("ValidateTransactions (iterating existing transactions)", "error", err)
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	results := make([]models.ValidateTransactionResult, 0, len(req.Transactions))
	existingCount := 0
	for i, t := range req.Transactions {
		exists := existing[transactionFingerprint(t.Date, t.Amount, t.Type, t.Description)]
		if exists {
			existingCount++
		}
		results = append(results, models.ValidateTransactionResult{
			Index:       i,
			Exists:      exists,
			Date:        t.Date,
			Description: t.Description,
			Amount:      t.Amount,
			Type:        t.Type,
		})
	}

	c.JSON(http.StatusOK, models.ValidateTransactionsResponse{
		Total:         len(req.Transactions),
		ExistingCount: existingCount,
		MissingCount:  len(req.Transactions) - existingCount,
		Results:       results,
	})
}

// attachTransactionsToCycle attaches the given transaction IDs to a billing
// cycle. Used by credit-card imports when the client chose an explicit cycle so
// every imported transaction lands in it, overriding the date-based default.
func attachTransactionsToCycle(ctx context.Context, q cycleQueryer, cycleID uuid.UUID, ids []uuid.UUID, userID uuid.UUID) error {
	_, err := q.Exec(ctx,
		"UPDATE transactions SET billing_cycle_id = $1 WHERE id = ANY($2) AND user_id = $3",
		cycleID, ids, userID)
	return err
}

// dedupeTransactions keeps the first occurrence of every unique row, dropping
// rows that match an existing transaction or repeat earlier in the batch. It
// returns the rows to insert and the number of duplicates dropped.
func dedupeTransactions(txns []models.ImportTransaction, existing map[string]bool) ([]models.ImportTransaction, int) {
	seen := make(map[string]bool, len(txns))
	kept := make([]models.ImportTransaction, 0, len(txns))
	duplicates := 0
	for _, t := range txns {
		fp := transactionFingerprint(t.Date, t.Amount, t.Type, t.Description)
		if existing[fp] || seen[fp] {
			duplicates++
			continue
		}
		seen[fp] = true
		kept = append(kept, t)
	}
	return kept, duplicates
}

// autoCategorize returns the category (and optional payee) of the first rule
// matching the transaction description, or nil/nil when no rule matches. Rules
// are expected to be pre-sorted by descending priority.
func autoCategorize(rules []ruleEntry, description string) (*uuid.UUID, *uuid.UUID) {
	for _, r := range rules {
		if matchRule(description, r.Pattern, r.MatchType) {
			return &r.CatID, r.PayeeID
		}
	}
	return nil, nil
}

// summaryNamespace seeds the deterministic UUIDs used for synthetic summary rows
// so React keys stay stable across requests.
var summaryNamespace = uuid.MustParse("00000000-0000-0000-0000-00000000f1a7")

// buildAccountSummaryRows looks up the filtered account and returns the
// synthetic summary rows to display. Accounts with a billing day set get
// per-cycle summary rows (regardless of account type); accounts without one
// show their raw transactions only.
func buildAccountSummaryRows(c *gin.Context, userID uuid.UUID, accountID, dateFrom, dateTo string) []models.Transaction {
	var acctName string
	var billingDay *int
	err := db.Pool.QueryRow(c,
		`SELECT a.name, a.billing_day
		 FROM accounts a
		 WHERE a.id = $1 AND a.user_id = $2`,
		accountID, userID).Scan(&acctName, &billingDay)
	if err != nil || billingDay == nil {
		return nil
	}

	if err := ensureBillingCycles(c, db.Pool, userID, uuid.MustParse(accountID), *billingDay); err != nil {
		slog.Error("buildAccountSummaryRows (ensure billing cycles)", "error", err)
		return nil
	}
	return computeSummaryRows(c, userID, uuid.MustParse(accountID), acctName, dateFrom, dateTo)
}

// computeSummaryRows builds the synthetic "Total outstanding" rows
// for an account (any type with a billing day set) from its explicit billing
// cycles. Each cycle that has attached transactions gets a row at its end date
// (the sum of its attached debit purchases), and the in-progress cycle
// containing the end of the range gets a row at the range end. Cycles are
// expected to already exist (callers run ensureBillingCycles first).
func computeSummaryRows(c *gin.Context, userID, accountID uuid.UUID, acctName, dateFrom, dateTo string) []models.Transaction {
	cycles, err := listBillingCycles(c, db.Pool, userID, accountID)
	if err != nil {
		slog.Error("computeSummaryRows (list cycles)", "error", err)
		return nil
	}

	// Resolve the date range (defaults: first cycle start to today).
	var from, to time.Time
	if dateFrom != "" {
		if t, err := time.Parse("2006-01-02", dateFrom); err == nil {
			from = t
		}
	}
	if dateTo != "" {
		if t, err := time.Parse("2006-01-02", dateTo); err == nil {
			to = t
		}
	}
	if to.IsZero() {
		to = dateOnly(time.Now())
	}
	if from.IsZero() {
		if len(cycles) > 0 {
			from = dateOnly(cycles[0].StartDate)
		} else {
			from = to
		}
	}

	buildRow := func(kind, description string, date time.Time, amount float64, billingCycleID *uuid.UUID) models.Transaction {
		return models.Transaction{
			ID:             summaryID(accountID.String(), kind, date),
			AccountID:      accountID,
			Date:           date,
			Description:    description,
			Amount:         amount,
			Type:           "credit",
			AccountName:    acctName,
			IsSummary:      true,
			BillingCycleID: billingCycleID,
		}
	}

	rows := []models.Transaction{}
	var current *models.BillingCycle
	for i := range cycles {
		bc := &cycles[i]
		end := dateOnly(bc.EndDate)
		// A row for every completed cycle (end date within the range) that has
		// attached transactions.
		if bc.TransactionCount > 0 && !end.Before(from) && !end.After(to) {
			rows = append(rows, buildRow("outstanding", "Total outstanding", end, bc.TotalOutstanding, &bc.ID))
		}
		// Track the in-progress cycle: the one whose date range contains `to`.
		if !dateOnly(bc.StartDate).After(to) && !end.Before(to) {
			current = bc
		}
	}

	// The in-progress cycle (its end date is beyond the range end) gets a row at
	// the range end with the debits attached so far.
	if current != nil && dateOnly(current.EndDate).After(to) {
		var total float64
		var count int
		err := db.Pool.QueryRow(c,
			`SELECT COALESCE(SUM(CASE WHEN t.type = 'debit' THEN t.amount ELSE 0 END), 0), COUNT(t.id)
			 FROM transactions t WHERE t.billing_cycle_id = $1 AND t.date <= $2`,
			current.ID, to).Scan(&total, &count)
		if err != nil {
			slog.Error("computeSummaryRows (current cycle)", "error", err)
			return nil
		}
		if count > 0 {
			rows = append(rows, buildRow("outstanding-current", "Total outstanding", to, total, &current.ID))
		}
	}

	return rows
}

// mergeSummaryRows interleaves synthetic summary rows into the already-sorted
// transaction list by date so they appear in the correct chronological position.
// Summary rows only make sense in a date-ordered list; when sorting by another
// column they are hidden entirely.
func mergeSummaryRows(transactions []models.Transaction, rows []models.Transaction, sortBy, sortOrder string) []models.Transaction {
	if len(rows) == 0 || sortBy != "date" {
		return transactions
	}
	// Only include the summary rows whose cycle window overlaps the transactions
	// on the current page, so rows are spread across pages instead of repeating
	// on every page.
	rows = filterSummaryRowsForPage(transactions, rows)
	if len(rows) == 0 {
		return transactions
	}
	return mergeByDate(transactions, rows, sortOrder)
}

// filterSummaryRowsForPage keeps only the summary rows whose window (the period
// since the previous summary row) contains at least one transaction on the
// current page. This distributes each "Total outstanding" / balance row to the
// page holding the transactions that produced it rather than showing it on
// every page.
func filterSummaryRowsForPage(transactions []models.Transaction, rows []models.Transaction) []models.Transaction {
	if len(rows) == 0 || len(transactions) == 0 {
		return nil
	}
	// Lower bound for the first row's window: the earliest page transaction.
	earliest := time.Time{}
	for _, t := range transactions {
		d := dateOnly(t.Date)
		if earliest.IsZero() || d.Before(earliest) {
			earliest = d
		}
	}
	prev := earliest.AddDate(0, 0, -1)

	sorted := make([]models.Transaction, len(rows))
	copy(sorted, rows)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Date.Before(sorted[j].Date) })

	kept := []models.Transaction{}
	for _, r := range sorted {
		d := dateOnly(r.Date)
		for _, t := range transactions {
			tx := dateOnly(t.Date)
			if tx.After(prev) && !tx.After(d) {
				kept = append(kept, r)
				break
			}
		}
		prev = d
	}
	return kept
}

// mergeByDate interleaves summary rows into the sorted transaction list by
// date, grouping each page's transactions by billing cycle so a "Total
// outstanding" row stays adjacent to its own cycle's transactions: after them
// in ascending order, before them in descending. Cycle transactions always
// fall on or before their cycle's row date, so the grouped output remains
// date-ordered. Transactions not attached to a cycle that has a kept row are
// preserved too (before the rows when descending, after them when ascending)
// so no transaction is ever dropped.
func mergeByDate(transactions []models.Transaction, rows []models.Transaction, sortOrder string) []models.Transaction {
	asc := sortOrder == "ASC"
	sort.Slice(rows, func(i, j int) bool {
		if asc {
			return rows[i].Date.Before(rows[j].Date)
		}
		return rows[i].Date.After(rows[j].Date)
	})

	// Group the page's transactions by the cycle whose row they belong to;
	// transactions with no matching kept cycle are held aside and re-added
	// below so they are never dropped.
	grouped := make(map[uuid.UUID][]models.Transaction, len(rows))
	rowCycles := make(map[uuid.UUID]bool, len(rows))
	for _, r := range rows {
		rowCycles[*r.BillingCycleID] = true
	}
	var ungrouped []models.Transaction
	for _, t := range transactions {
		if t.BillingCycleID != nil && rowCycles[*t.BillingCycleID] {
			grouped[*t.BillingCycleID] = append(grouped[*t.BillingCycleID], t)
		} else {
			ungrouped = append(ungrouped, t)
		}
	}

	merged := make([]models.Transaction, 0, len(transactions)+len(rows))
	if asc {
		for _, r := range rows {
			merged = append(merged, grouped[*r.BillingCycleID]...)
			merged = append(merged, r)
		}
		merged = append(merged, ungrouped...)
	} else {
		merged = append(merged, ungrouped...)
		for _, r := range rows {
			merged = append(merged, r)
			merged = append(merged, grouped[*r.BillingCycleID]...)
		}
	}

	return merged
}

// summaryID returns a deterministic UUID for a summary row so it is stable
// across requests and safe to use as a React key.
func summaryID(accountID, kind string, date time.Time) uuid.UUID {
	return uuid.NewSHA1(summaryNamespace, fmt.Appendf(nil, "%s|%s|%s", accountID, kind, date.Format("2006-01-02")))
}

// dateOnly normalizes a time to midnight UTC for date-only comparisons.
func dateOnly(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// daysInMonth returns the number of days in the given month.
func daysInMonth(y int, m time.Month) int {
	return time.Date(y, m+1, 0, 0, 0, 0, 0, time.UTC).Day()
}

// billingDateInMonth returns the billing day (clamped to the month length)
// within the month containing t.
func billingDateInMonth(t time.Time, day int) time.Time {
	y, m := t.Year(), t.Month()
	if day > daysInMonth(y, m) {
		day = daysInMonth(y, m)
	}
	return time.Date(y, m, day, 0, 0, 0, 0, time.UTC)
}
