package handlers

import (
	"fmt"
	"log"
	"math"
	"net/http"
	"strconv"
	"strings"

	"github.com/fintrak/backend/auth"
	"github.com/fintrak/backend/db"
	"github.com/fintrak/backend/models"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

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
	if limit < 0 || limit > 200 {
		limit = 50
	}

	// Validate sort column
	validSorts := map[string]string{
		"date":        "t.date",
		"amount":      "t.amount",
		"description": "t.description",
		"createdAt":   "t.created_at",
	}
	sortCol, ok := validSorts[sortBy]
	if !ok {
		sortCol = "t.date"
	}
	if sortOrder != "ASC" {
		sortOrder = "DESC"
	}

	query := `SELECT t.id, t.account_id, t.date, t.description, t.amount, t.type, 
			  t.category_id, t.tags, t.notes, t.payee_id, COALESCE(p.name, '') as payee, t.created_at,
			  a.name as account_name,
			  COALESCE(c.name, '') as category_name,
			  COALESCE(c.icon, '') as category_icon,
			  COALESCE(c.color, '') as category_color,
			  EXISTS(SELECT 1 FROM links WHERE from_txn_id = t.id OR to_txn_id = t.id) as is_linked,
			  (SELECT id FROM links WHERE from_txn_id = t.id OR to_txn_id = t.id LIMIT 1) as link_id
			  FROM transactions t
			  JOIN accounts a ON t.account_id = a.id
			  LEFT JOIN categories c ON t.category_id = c.id
			  LEFT JOIN payees p ON t.payee_id = p.id
			  WHERE t.user_id = $1`

	countQuery := `SELECT COUNT(*) FROM transactions t WHERE t.user_id = $1`
	args := []interface{}{userID}
	countArgs := []interface{}{userID}
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
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
		return
	}
	defer rows.Close()

	transactions := []models.Transaction{}
	for rows.Next() {
		var t models.Transaction
		if err := rows.Scan(&t.ID, &t.AccountID, &t.Date, &t.Description, &t.Amount, &t.Type,
			&t.CategoryID, &t.Tags, &t.Notes, &t.PayeeID, &t.Payee, &t.CreatedAt,
			&t.AccountName, &t.CategoryName, &t.CategoryIcon, &t.CategoryColor, &t.IsLinked, &t.LinkID); err != nil {
			log.Printf("Error in GetTransactions scan: %v\n", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
			return
		}
		transactions = append(transactions, t)
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

func UpdateTransaction(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	var req models.UpdateTransactionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
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
			c.JSON(http.StatusBadRequest, gin.H{"error": "type must be 'debit' or 'credit'"})
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

	if len(setClauses) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "no fields to update"})
		return
	}

	// WHERE id = $N AND user_id = $N+1
	args = append(args, id, auth.GetUserID(c))
	query := fmt.Sprintf("UPDATE transactions SET %s WHERE id = $%d AND user_id = $%d",
		strings.Join(setClauses, ", "), paramIdx, paramIdx+1)

	result, err := db.Pool.Exec(c, query, args...)
	if err != nil {
		log.Printf("Error in UpdateTransaction: %v\n", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
		return
	}

	if result.RowsAffected() == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "transaction not found"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "updated"})
}

func BulkCategorize(c *gin.Context) {
	var req models.BulkCategorizeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	query := "UPDATE transactions SET category_id = $1 WHERE id = ANY($2) AND user_id = $3"
	result, err := db.Pool.Exec(c, query, req.CategoryID, req.TransactionIDs, auth.GetUserID(c))
	if err != nil {
		log.Printf("Error in BulkCategorize: %v\n", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"updated": result.RowsAffected()})
}

func BulkUpdatePayee(c *gin.Context) {
	var req models.BulkUpdatePayeeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	query := "UPDATE transactions SET payee_id = $1 WHERE id = ANY($2) AND user_id = $3"
	result, err := db.Pool.Exec(c, query, req.PayeeID, req.TransactionIDs, auth.GetUserID(c))
	if err != nil {
		log.Printf("Error in BulkUpdatePayee: %v\n", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"updated": result.RowsAffected()})
}

func BulkDeleteTransactions(c *gin.Context) {
	var req models.BulkDeleteTransactionsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	query := "DELETE FROM transactions WHERE id = ANY($1) AND user_id = $2"
	result, err := db.Pool.Exec(c, query, req.TransactionIDs, auth.GetUserID(c))
	if err != nil {
		log.Printf("Error in BulkDeleteTransactions: %v\n", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"deleted": result.RowsAffected()})
}

func DeleteTransaction(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	userID := auth.GetUserID(c)
	result, err := db.Pool.Exec(c, "DELETE FROM transactions WHERE id = $1 AND user_id = $2", id, userID)
	if err != nil {
		log.Printf("Error in DeleteTransaction: %v\n", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
		return
	}

	if result.RowsAffected() == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "transaction not found"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "deleted"})
}

func ImportTransactions(c *gin.Context) {
	var req models.ImportRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	userID := auth.GetUserID(c)
	log.Printf("Starting import of %d transactions for account %s\n", len(req.Transactions), req.AccountID)

	// Load rules once and match in memory to avoid N+1 queries
	rules, err := loadRules(c, userID)
	if err != nil {
		log.Printf("Error in ImportTransactions (getting rules): %v\n", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
		return
	}

	// Send all inserts in a single batch round trip
	batch := &pgx.Batch{}
	for _, t := range req.Transactions {
		categoryID, payeeID := autoCategorize(rules, t.Description)

		// If rule gives no payee, try to find one by name from import
		if payeeID == nil && t.PayeeID != nil {
			payeeID = t.PayeeID
		}

		batch.Queue(
			`INSERT INTO transactions (account_id, user_id, date, description, amount, type, category_id, payee_id)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
			req.AccountID, userID, t.Date, t.Description, t.Amount, t.Type, categoryID, payeeID,
		)
	}

	br := db.Pool.SendBatch(c, batch)
	defer br.Close()

	imported := 0
	for range req.Transactions {
		if _, err := br.Exec(); err != nil {
			log.Printf("Error in ImportTransactions: %v\n", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error", "imported": imported})
			return
		}
		imported++
	}

	c.JSON(http.StatusOK, gin.H{
		"imported": imported,
		"total":    len(req.Transactions),
	})
	log.Printf("Import complete: %d imported out of %d total.\n", imported, len(req.Transactions))
}

func autoCategorize(rules []ruleEntry, description string) (*uuid.UUID, *uuid.UUID) {
	for _, r := range rules {
		if matchRule(description, r.Pattern, r.MatchType) {
			return &r.CatID, r.PayeeID
		}
	}
	return nil, nil
}
