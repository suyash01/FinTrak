package handlers

import (
	"crypto/sha256"
	"fmt"
	"log"
	"math"
	"net/http"
	"strconv"
	"strings"

	"github.com/fintrak/backend/db"
	"github.com/fintrak/backend/models"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func GetTransactions(c *gin.Context) {
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
	if limit < 0 {
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
			  t.category_id, t.tags, t.notes, t.payee_id, COALESCE(p.name, '') as payee, t.hash, t.created_at,
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
			  WHERE 1=1`

	countQuery := `SELECT COUNT(*) FROM transactions t WHERE 1=1`
	args := []interface{}{}
	countArgs := []interface{}{}
	paramIdx := 1

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
			&t.CategoryID, &t.Tags, &t.Notes, &t.PayeeID, &t.Payee, &t.Hash, &t.CreatedAt,
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

	_, err = db.Pool.Exec(c,
		"UPDATE transactions SET category_id = $1, tags = $2, notes = $3, payee_id = $4 WHERE id = $5",
		req.CategoryID, req.Tags, req.Notes, req.PayeeID, id,
	)
	if err != nil {
		log.Printf("Error in UpdateTransaction: %v\n", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
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

	ids := make([]string, len(req.TransactionIDs))
	for i, id := range req.TransactionIDs {
		ids[i] = fmt.Sprintf("'%s'", id.String())
	}

	query := fmt.Sprintf("UPDATE transactions SET category_id = $1 WHERE id IN (%s)", strings.Join(ids, ","))
	result, err := db.Pool.Exec(c, query, req.CategoryID)
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

	ids := make([]string, len(req.TransactionIDs))
	for i, id := range req.TransactionIDs {
		ids[i] = fmt.Sprintf("'%s'", id.String())
	}

	query := fmt.Sprintf("UPDATE transactions SET payee_id = $1 WHERE id IN (%s)", strings.Join(ids, ","))
	result, err := db.Pool.Exec(c, query, req.PayeeID)
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

	ids := make([]string, len(req.TransactionIDs))
	for i, id := range req.TransactionIDs {
		ids[i] = fmt.Sprintf("'%s'", id.String())
	}

	query := fmt.Sprintf("DELETE FROM transactions WHERE id IN (%s)", strings.Join(ids, ","))
	result, err := db.Pool.Exec(c, query)
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

	_, err = db.Pool.Exec(c, "DELETE FROM transactions WHERE id = $1", id)
	if err != nil {
		log.Printf("Error in DeleteTransaction: %v\n", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
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

	imported := 0
	skipped := 0
	log.Printf("Starting import of %d transactions for account %s\n", len(req.Transactions), req.AccountID)

	for _, t := range req.Transactions {
		// Generate hash for dedup
		hashStr := fmt.Sprintf("%s|%s|%s|%.2f|%s", req.AccountID, t.Date, t.Description, t.Amount, t.Type)
		hash := fmt.Sprintf("%x", sha256.Sum256([]byte(hashStr)))

		// Auto-categorize
		var categoryID *uuid.UUID
		var payeeID *uuid.UUID
		categoryID, payeeID = autoCategorize(c, t.Description)

		// If rule gives no payee, try to find one by name from import
		if payeeID == nil && t.PayeeID != nil {
			payeeID = t.PayeeID
		}

		if categoryID != nil {
			log.Printf("Auto-categorized transaction '%s' to category %s (PayeeID: %v)\n", t.Description, categoryID, payeeID)
		}

		_, err := db.Pool.Exec(c,
			`INSERT INTO transactions (account_id, date, description, amount, type, category_id, payee_id, hash)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
			req.AccountID, t.Date, t.Description, t.Amount, t.Type, categoryID, payeeID, hash,
		)
		if err != nil {
			log.Printf("Error in ImportTransactions: %v\n", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error", "imported": imported})
			return
		}
		imported++
	}

	c.JSON(http.StatusOK, gin.H{
		"imported": imported,
		"skipped":  skipped,
		"total":    len(req.Transactions),
	})
	log.Printf("Import complete: %d imported, %d skipped out of %d total.\n", imported, skipped, len(req.Transactions))
}

func autoCategorize(c *gin.Context, description string) (*uuid.UUID, *uuid.UUID) {
	rows, err := db.Pool.Query(c,
		"SELECT pattern, match_type, category_id, payee_id FROM rules ORDER BY priority DESC")
	if err != nil {
		return nil, nil
	}
	defer rows.Close()

	descLower := strings.ToLower(description)

	for rows.Next() {
		var pattern, matchType string
		var catID uuid.UUID
		var payeeID *uuid.UUID
		rows.Scan(&pattern, &matchType, &catID, &payeeID)

		patternLower := strings.ToLower(pattern)
		matched := false

		switch matchType {
		case "contains":
			matched = strings.Contains(descLower, patternLower)
		case "starts_with":
			matched = strings.HasPrefix(descLower, patternLower)
		case "exact":
			matched = descLower == patternLower
		}

		if matched {
			return &catID, payeeID
		}
	}

	return nil, nil
}
