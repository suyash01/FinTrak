package handlers

import (
	"context"
	"errors"
	"fmt"
	"log"
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

func GetTransactions(c *gin.Context) {
	userID := auth.GetUserID(c)
	accountID := c.Query("accountId")
	categoryID := c.Query("categoryId")
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
	// limit of 0 means "no pagination" (return everything); cap at 200.
	if limit < 0 {
		limit = 0
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
			  (SELECT COUNT(*) FROM links WHERE from_txn_id = t.id OR to_txn_id = t.id) as link_count,
			  CASE WHEN (SELECT COUNT(*) FROM links WHERE from_txn_id = t.id OR to_txn_id = t.id) = 1
			       THEN (SELECT id FROM links WHERE from_txn_id = t.id OR to_txn_id = t.id LIMIT 1)
			  END as link_id,
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
		query += fmt.Sprintf(" AND t.category_id = $%d", paramIdx)
		countQuery += fmt.Sprintf(" AND t.category_id = $%d", paramIdx)
		args = append(args, categoryID)
		countArgs = append(countArgs, categoryID)
		paramIdx++
	}
	if uncategorized == "true" {
		query += " AND t.category_id IS NULL"
		countQuery += " AND t.category_id IS NULL"
	}
	if search != "" {
		query += fmt.Sprintf(" AND LOWER(t.description) LIKE LOWER($%d)", paramIdx)
		countQuery += fmt.Sprintf(" AND LOWER(t.description) LIKE LOWER($%d)", paramIdx)
		args = append(args, "%"+search+"%")
		countArgs = append(countArgs, "%"+search+"%")
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

	if limit == 0 {
		// No pagination — return all results
		query += fmt.Sprintf(" ORDER BY %s %s", sortCol, sortOrder)
	} else {
		offset := (page - 1) * limit
		query += fmt.Sprintf(" ORDER BY %s %s LIMIT $%d OFFSET $%d", sortCol, sortOrder, paramIdx, paramIdx+1)
		args = append(args, limit, offset)
	}

	rows, err := db.Pool.Query(c, query, args...)
	if err != nil {
		log.Printf("Error in GetTransactions: %v\n", err)
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	transactions := []models.Transaction{}
	for rows.Next() {
		var t models.Transaction
		if err := rows.Scan(&t.ID, &t.AccountID, &t.Date, &t.Description, &t.Amount, &t.Type,
			&t.CategoryID, &t.Tags, &t.Notes, &t.PayeeID, &t.Payee, &t.CreatedAt,
			&t.AccountName, &t.CategoryName, &t.CategoryIcon, &t.CategoryColor, &t.IsLinked, &t.LinkCount, &t.LinkID,
			&t.BillingCycleID, &t.BillingCycleLabel); err != nil {
			log.Printf("Error in GetTransactions scan: %v\n", err)
			validation.RespondError(c, "internal server error", http.StatusInternalServerError)
			return
		}
		transactions = append(transactions, t)
	}

	// When a single account is filtered, inject computed summary rows: the
	// current-cycle total outstanding for credit cards and the running balance
	// at the end of each month for bank accounts. These are synthetic and are
	// never persisted.
	if accountID != "" {
		summaryTxns := buildAccountSummaryRows(c, userID, accountID, dateFrom, dateTo)
		transactions = mergeSummaryRows(transactions, summaryTxns, sortBy, sortOrder)
	}

	pages := 1
	if limit > 0 {
		pages = int(math.Ceil(float64(total) / float64(limit)))
	}

	c.JSON(http.StatusOK, gin.H{
		"data":  transactions,
		"total": total,
		"page":  page,
		"limit": limit,
		"pages": pages,
	})
}

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

	// The account must exist and belong to the authenticated user. Also fetch
	// its type so credit-card transactions can be attached to a billing cycle.
	var ownerID uuid.UUID
	var acctTypeID string
	err := db.Pool.QueryRow(c,
		"SELECT user_id, account_type_id FROM accounts WHERE id = $1",
		req.AccountID).Scan(&ownerID, &acctTypeID)
	if errors.Is(err, pgx.ErrNoRows) {
		validation.RespondError(c, "account not found", http.StatusNotFound)
		return
	}
	if err != nil {
		log.Printf("Error in CreateTransaction (checking account): %v\n", err)
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
			log.Printf("Error in CreateTransaction (getting rules): %v\n", err)
			validation.RespondError(c, "internal server error", http.StatusInternalServerError)
			return
		}
		matchedCat, matchedPayee := autoCategorize(rules, req.Description)
		categoryID = matchedCat
		if payeeID == nil {
			payeeID = matchedPayee
		}
	}

	var id uuid.UUID
	err = db.Pool.QueryRow(c,
		`INSERT INTO transactions (account_id, user_id, date, description, amount, type, category_id, payee_id, tags, notes)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10) RETURNING id`,
		req.AccountID, userID, req.Date, req.Description, req.Amount, req.Type, categoryID, payeeID, req.Tags, req.Notes).Scan(&id)
	if err != nil {
		log.Printf("Error in CreateTransaction (insert): %v\n", err)
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	// Credit-card transactions are attached to a billing cycle: by default the
	// cycle matching the transaction date (the suggested default), or the
	// explicitly chosen cycle when the client supplied one.
	if acctTypeID == "credit_card" {
		if err := ensureBillingCycles(c, db.Pool, userID, req.AccountID); err != nil {
			log.Printf("Error in CreateTransaction (ensure billing cycles): %v\n", err)
			validation.RespondError(c, "internal server error", http.StatusInternalServerError)
			return
		}
		if req.BillingCycleID != nil {
			if _, err := db.Pool.Exec(c,
				"UPDATE transactions SET billing_cycle_id = $1 WHERE id = $2 AND user_id = $3",
				*req.BillingCycleID, id, userID); err != nil {
				log.Printf("Error in CreateTransaction (set billing cycle): %v\n", err)
				validation.RespondError(c, "internal server error", http.StatusInternalServerError)
				return
			}
		}
	}

	c.JSON(http.StatusCreated, gin.H{"id": id})
}

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

	// Build dynamic SET clauses
	setClauses := []string{}
	args := []interface{}{}
	paramIdx := 1

	// Conditionally update fields based on presence in the request.
	// CategoryID/PayeeID use OptionalUUID so that an explicit `null`
	// (clear the field) can be distinguished from an absent key.
	if req.CategoryID.Set() {
		setClauses = append(setClauses, fmt.Sprintf("category_id = $%d", paramIdx))
		if v := req.CategoryID.Value(); v != nil {
			args = append(args, *v)
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
		paramIdx++
	}
	if req.BillingCycleID.Set() {
		setClauses = append(setClauses, fmt.Sprintf("billing_cycle_id = $%d", paramIdx))
		if v := req.BillingCycleID.Value(); v != nil {
			args = append(args, *v)
		} else {
			args = append(args, nil)
		}
		paramIdx++
	}

	if len(setClauses) == 0 {
		validation.RespondError(c, "no fields to update", http.StatusBadRequest)
		return
	}

	// WHERE id = $N AND user_id = $N+1
	args = append(args, id, auth.GetUserID(c))
	query := fmt.Sprintf("UPDATE transactions SET %s WHERE id = $%d AND user_id = $%d",
		strings.Join(setClauses, ", "), paramIdx, paramIdx+1)

	result, err := db.Pool.Exec(c, query, args...)
	if err != nil {
		log.Printf("Error in UpdateTransaction: %v\n", err)
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	if result.RowsAffected() == 0 {
		validation.RespondError(c, "transaction not found", http.StatusNotFound)
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "updated"})
}

func BulkCategorize(c *gin.Context) {
	var req models.BulkCategorizeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		validation.RespondBindError(c, err)
		return
	}

	query := "UPDATE transactions SET category_id = $1 WHERE id = ANY($2) AND user_id = $3"
	result, err := db.Pool.Exec(c, query, req.CategoryID, req.TransactionIDs, auth.GetUserID(c))
	if err != nil {
		log.Printf("Error in BulkCategorize: %v\n", err)
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	c.JSON(http.StatusOK, gin.H{"updated": result.RowsAffected()})
}

func BulkUpdatePayee(c *gin.Context) {
	var req models.BulkUpdatePayeeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		validation.RespondBindError(c, err)
		return
	}

	query := "UPDATE transactions SET payee_id = $1 WHERE id = ANY($2) AND user_id = $3"
	result, err := db.Pool.Exec(c, query, req.PayeeID, req.TransactionIDs, auth.GetUserID(c))
	if err != nil {
		log.Printf("Error in BulkUpdatePayee: %v\n", err)
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	c.JSON(http.StatusOK, gin.H{"updated": result.RowsAffected()})
}

func BulkUpdateBillingCycle(c *gin.Context) {
	var req models.BulkBillingCycleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		validation.RespondBindError(c, err)
		return
	}

	query := "UPDATE transactions SET billing_cycle_id = $1 WHERE id = ANY($2) AND user_id = $3"
	result, err := db.Pool.Exec(c, query, req.BillingCycleID, req.TransactionIDs, auth.GetUserID(c))
	if err != nil {
		log.Printf("Error in BulkUpdateBillingCycle: %v\n", err)
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	c.JSON(http.StatusOK, gin.H{"updated": result.RowsAffected()})
}

func BulkDeleteTransactions(c *gin.Context) {
	var req models.BulkDeleteTransactionsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		validation.RespondBindError(c, err)
		return
	}

	query := "DELETE FROM transactions WHERE id = ANY($1) AND user_id = $2"
	result, err := db.Pool.Exec(c, query, req.TransactionIDs, auth.GetUserID(c))
	if err != nil {
		log.Printf("Error in BulkDeleteTransactions: %v\n", err)
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	c.JSON(http.StatusOK, gin.H{"deleted": result.RowsAffected()})
}

func DeleteTransaction(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		validation.RespondError(c, "invalid id", http.StatusBadRequest)
		return
	}

	userID := auth.GetUserID(c)
	result, err := db.Pool.Exec(c, "DELETE FROM transactions WHERE id = $1 AND user_id = $2", id, userID)
	if err != nil {
		log.Printf("Error in DeleteTransaction: %v\n", err)
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	if result.RowsAffected() == 0 {
		validation.RespondError(c, "transaction not found", http.StatusNotFound)
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "deleted"})
}

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

	// The account must exist and belong to the authenticated user. Also fetch
	// its type so credit-card imports can be attached to a billing cycle.
	var ownerID uuid.UUID
	var acctTypeID string
	err := db.Pool.QueryRow(c,
		"SELECT user_id, account_type_id FROM accounts WHERE id = $1",
		req.AccountID).Scan(&ownerID, &acctTypeID)
	if errors.Is(err, pgx.ErrNoRows) {
		validation.RespondError(c, "account not found", http.StatusNotFound)
		return
	}
	if err != nil {
		log.Printf("Error in ImportTransactions (checking account): %v\n", err)
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	if ownerID != userID {
		validation.RespondError(c, "forbidden", http.StatusForbidden)
		return
	}

	// Load rules once and match in memory to avoid N+1 queries.
	rules, err := loadRules(c, userID)
	if err != nil {
		log.Printf("Error in ImportTransactions (getting rules): %v\n", err)
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	// Run the whole import in a transaction so it is all-or-nothing.
	tx, err := db.Pool.Begin(c)
	if err != nil {
		log.Printf("Error in ImportTransactions (begin): %v\n", err)
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
			log.Printf("Error in ImportTransactions (loading existing transactions): %v\n", err)
			validation.RespondError(c, "internal server error", http.StatusInternalServerError)
			return
		}
		for rows.Next() {
			var d time.Time
			var amount float64
			var typ, description string
			if err := rows.Scan(&d, &amount, &typ, &description); err != nil {
				rows.Close()
				log.Printf("Error in ImportTransactions (scanning existing transactions): %v\n", err)
				validation.RespondError(c, "internal server error", http.StatusInternalServerError)
				return
			}
			existing[transactionFingerprint(d.Format("2006-01-02"), amount, typ, description)] = true
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			log.Printf("Error in ImportTransactions (iterating existing transactions): %v\n", err)
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
				log.Printf("Error in ImportTransactions: %v\n", err)
				validation.RespondError(c, "internal server error", http.StatusInternalServerError)
				return
			}
			ids = append(ids, id)
			done++
		}
		br.Close()

		// Credit-card imports: attach the new transactions to their billing
		// cycles. When the client chose an explicit cycle, every imported
		// transaction is attached to it (overriding the date-based default);
		// otherwise the suggested default (by transaction date) applies. Runs
		// inside the transaction so the assignment commits atomically with the
		// import.
		if acctTypeID == "credit_card" {
			if req.BillingCycleID != nil {
				if err := attachTransactionsToCycle(c, tx, *req.BillingCycleID, ids, userID); err != nil {
					log.Printf("Error in ImportTransactions (set billing cycle): %v\n", err)
					validation.RespondError(c, "internal server error", http.StatusInternalServerError)
					return
				}
			}
			if err := ensureBillingCycles(c, tx, userID, req.AccountID); err != nil {
				log.Printf("Error in ImportTransactions (ensure billing cycles): %v\n", err)
				validation.RespondError(c, "internal server error", http.StatusInternalServerError)
				return
			}
		}

		if err := tx.Commit(c); err != nil {
			log.Printf("Error in ImportTransactions (commit): %v\n", err)
			validation.RespondError(c, "internal server error", http.StatusInternalServerError)
			return
		}
	}

	// Tag the source Paperless documents only after the import has committed, so
	// the label reflects documents that were actually imported. Best-effort.
	tagPaperlessDocuments(c, userID, req.PaperlessDocumentIDs)

	c.JSON(http.StatusOK, gin.H{
		"imported":   imported,
		"duplicates": duplicates,
		"total":      len(req.Transactions),
	})
	log.Printf("Import complete: %d imported, %d duplicates skipped out of %d total for account %s\n",
		imported, duplicates, len(req.Transactions), req.AccountID)
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
		log.Printf("Error in ValidateTransactions (checking account): %v\n", err)
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
		log.Printf("Error in ValidateTransactions (loading existing transactions): %v\n", err)
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	for rows.Next() {
		var d time.Time
		var amount float64
		var typ, description string
		if err := rows.Scan(&d, &amount, &typ, &description); err != nil {
			rows.Close()
			log.Printf("Error in ValidateTransactions (scanning existing transactions): %v\n", err)
			validation.RespondError(c, "internal server error", http.StatusInternalServerError)
			return
		}
		existing[transactionFingerprint(d.Format("2006-01-02"), amount, typ, description)] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		log.Printf("Error in ValidateTransactions (iterating existing transactions): %v\n", err)
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

// acctTxn is a minimal account transaction row used to compute running balances.
type acctTxn struct {
	Date   time.Time
	Amount float64
	Type   string
}

// buildAccountSummaryRows looks up the filtered account's type and, when it is
// a credit card or a bank account, returns the synthetic summary rows to
// display. Credit cards summarize by explicit billing cycle (transactions are
// attached to cycles rather than bucketed by date); bank accounts get month-end
// running balances.
func buildAccountSummaryRows(c *gin.Context, userID uuid.UUID, accountID, dateFrom, dateTo string) []models.Transaction {
	var acctName, acctTypeID, positiveTxnType string
	err := db.Pool.QueryRow(c,
		`SELECT a.name, a.account_type_id, at.positive_txn_type
		 FROM accounts a JOIN account_types at ON a.account_type_id = at.id
		 WHERE a.id = $1 AND a.user_id = $2`,
		accountID, userID).Scan(&acctName, &acctTypeID, &positiveTxnType)
	if err != nil {
		return nil
	}

	if acctTypeID == "credit_card" {
		if err := ensureBillingCycles(c, db.Pool, userID, uuid.MustParse(accountID)); err != nil {
			log.Printf("Error in buildAccountSummaryRows (ensure billing cycles): %v\n", err)
			return nil
		}
		return computeCreditCardSummaryRows(c, userID, uuid.MustParse(accountID), acctName, dateFrom, dateTo)
	}

	rows, err := db.Pool.Query(c,
		`SELECT date, amount, type FROM transactions WHERE account_id = $1 AND user_id = $2 ORDER BY date ASC`,
		accountID, userID)
	if err != nil {
		log.Printf("Error in buildAccountSummaryRows: %v\n", err)
		return nil
	}
	defer rows.Close()

	txns := []acctTxn{}
	for rows.Next() {
		var t acctTxn
		if err := rows.Scan(&t.Date, &t.Amount, &t.Type); err != nil {
			log.Printf("Error in buildAccountSummaryRows scan: %v\n", err)
			return nil
		}
		txns = append(txns, t)
	}
	if err := rows.Err(); err != nil {
		log.Printf("Error in buildAccountSummaryRows iterate: %v\n", err)
		return nil
	}

	return computeSummaryRows(acctName, accountID, acctTypeID, positiveTxnType, dateFrom, dateTo, txns)
}

// computeSummaryRows builds the synthetic summary rows for a bank account: a
// "Month-end balance" row after every month-end plus a final "Balance" row at
// the end of the range. Credit-card summaries are computed separately from
// explicit billing cycles (see computeCreditCardSummaryRows).
func computeSummaryRows(accountName, accountID, acctTypeID, positiveTxnType string, dateFrom, dateTo string, txns []acctTxn) []models.Transaction {
	rows := []models.Transaction{}
	today := dateOnly(time.Now())

	// sumByDirection adds txns to `running` up to `upTo` (inclusive), applying
	// the account type's positive/negative direction.
	sumByDirection := func(upTo time.Time, running *float64, idx *int) {
		for *idx < len(txns) {
			d := dateOnly(txns[*idx].Date)
			if d.After(upTo) {
				break
			}
			if txns[*idx].Type == positiveTxnType {
				*running += txns[*idx].Amount
			} else {
				*running -= txns[*idx].Amount
			}
			*idx++
		}
	}

	buildRow := func(kind, description string, date time.Time, amount float64) models.Transaction {
		return models.Transaction{
			ID:          summaryID(accountID, kind, date),
			AccountID:   uuid.MustParse(accountID),
			Date:        date,
			Description: description,
			Amount:      amount,
			Type:        "credit",
			AccountName: accountName,
			IsSummary:   true,
		}
	}

	// Resolve the date range (defaults: earliest transaction to today).
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
		to = today
	}
	if from.IsZero() {
		for _, tx := range txns {
			d := dateOnly(tx.Date)
			if from.IsZero() || d.Before(from) {
				from = d
			}
		}
	}
	if from.IsZero() {
		from = to
	}

	if acctTypeID == "bank" {
		running := 0.0
		idx := 0
		lastMonthEnd := time.Time{}
		for ms := time.Date(from.Year(), from.Month(), 1, 0, 0, 0, 0, time.UTC); !ms.After(to); ms = ms.AddDate(0, 1, 0) {
			me := lastDayOfMonth(ms)
			if me.After(to) {
				break
			}
			sumByDirection(me, &running, &idx)
			if !me.Before(from) {
				rows = append(rows, buildRow("monthend", "Month-end balance", me, running))
			}
			lastMonthEnd = me
		}

		// Final running balance at the end of the range. Skipped when the range
		// already ends exactly on a month-end (already emitted above).
		sumByDirection(to, &running, &idx)
		if !lastMonthEnd.Equal(dateOnly(to)) {
			rows = append(rows, buildRow("balance", "Balance", to, running))
		}
	}

	return rows
}

// computeCreditCardSummaryRows builds the synthetic "Total outstanding" rows for
// a credit-card account from its explicit billing cycles. Each cycle that has
// attached transactions gets a row at its end date (the sum of its attached
// debit purchases), and the in-progress cycle containing the end of the range
// gets a row at the range end. Cycles are expected to already exist (callers
// run ensureBillingCycles first).
func computeCreditCardSummaryRows(c *gin.Context, userID, accountID uuid.UUID, acctName, dateFrom, dateTo string) []models.Transaction {
	cycles, err := listBillingCycles(c, db.Pool, userID, accountID)
	if err != nil {
		log.Printf("Error in computeCreditCardSummaryRows (list cycles): %v\n", err)
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

	buildRow := func(kind, description string, date time.Time, amount float64) models.Transaction {
		return models.Transaction{
			ID:          summaryID(accountID.String(), kind, date),
			AccountID:   accountID,
			Date:        date,
			Description: description,
			Amount:      amount,
			Type:        "credit",
			AccountName: acctName,
			IsSummary:   true,
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
			rows = append(rows, buildRow("outstanding", "Total outstanding", end, bc.TotalOutstanding))
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
			log.Printf("Error in computeCreditCardSummaryRows (current cycle): %v\n", err)
			return nil
		}
		if count > 0 {
			rows = append(rows, buildRow("outstanding-current", "Total outstanding", to, total))
		}
	}

	return rows
}

// mergeSummaryRows interleaves synthetic summary rows into the already-sorted
// transaction list by date so they appear in the correct chronological position.
func mergeSummaryRows(transactions []models.Transaction, rows []models.Transaction, sortBy, sortOrder string) []models.Transaction {
	if len(rows) == 0 {
		return transactions
	}
	// Only include the summary rows whose cycle window overlaps the transactions
	// on the current page, so rows are spread across pages instead of repeating
	// on every page.
	rows = filterSummaryRowsForPage(transactions, rows)
	if len(rows) == 0 {
		return transactions
	}
	return mergeByDate(transactions, rows, sortBy, sortOrder)
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

// mergeByDate interleaves summary rows into the sorted transaction list by date,
// placing a summary row at the top of the day when it shares a date with the
// day's transactions.
func mergeByDate(transactions []models.Transaction, rows []models.Transaction, sortBy, sortOrder string) []models.Transaction {
	asc := sortOrder == "ASC"
	sort.Slice(rows, func(i, j int) bool {
		if asc {
			return rows[i].Date.Before(rows[j].Date)
		}
		return rows[i].Date.After(rows[j].Date)
	})
	if sortBy != "date" {
		return append(append([]models.Transaction{}, transactions...), rows...)
	}

	merged := make([]models.Transaction, 0, len(transactions)+len(rows))
	i, j := 0, 0
	for i < len(transactions) || j < len(rows) {
		if j >= len(rows) {
			merged = append(merged, transactions[i])
			i++
		} else if i >= len(transactions) {
			merged = append(merged, rows[j])
			j++
		} else {
			a, b := transactions[i], rows[j]
			if asc {
				// Ascending by date; on ties put the summary row (b) first.
				if !a.Date.Before(b.Date) {
					merged = append(merged, b)
					j++
				} else {
					merged = append(merged, a)
					i++
				}
			} else {
				// Descending by date; on ties put the summary row (b) first.
				if !a.Date.After(b.Date) {
					merged = append(merged, b)
					j++
				} else {
					merged = append(merged, a)
					i++
				}
			}
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

// lastDayOfMonth returns the last day of the month containing t, at midnight.
func lastDayOfMonth(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), daysInMonth(t.Year(), t.Month()), 0, 0, 0, 0, time.UTC)
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
