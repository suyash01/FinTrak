package handlers

import (
	"fmt"
	"log/slog"
	"net/http"

	"github.com/fintrak/backend/auth"
	"github.com/fintrak/backend/db"
	"github.com/fintrak/backend/internal/validation"
	"github.com/fintrak/backend/models"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

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
	// Transactions on closed accounts are skipped (immutable; linking only).
	query := `UPDATE transactions SET category_id = NULL
	          WHERE id = ANY($1) AND user_id = $2
	            AND NOT EXISTS (SELECT 1 FROM accounts closed_acct WHERE closed_acct.id = transactions.account_id AND closed_acct.closed)`
	args := []interface{}{req.TransactionIDs, auth.GetUserID(c)}
	if req.CategoryID != "uncategorized" {
		catUUID, err := uuid.Parse(req.CategoryID)
		if err != nil {
			validation.RespondError(c, "invalid category id", http.StatusBadRequest)
			return
		}
		query = `UPDATE transactions SET category_id = $1
		          WHERE id = ANY($2) AND user_id = $3
		            AND EXISTS (SELECT 1 FROM categories c WHERE c.id = $1 AND (c.user_id = $3 OR c.user_id IS NULL))
		            AND NOT EXISTS (SELECT 1 FROM accounts closed_acct WHERE closed_acct.id = transactions.account_id AND closed_acct.closed)`
		args = []interface{}{catUUID, req.TransactionIDs, auth.GetUserID(c)}
	}
	result, err := db.Pool.Exec(c, query, args...)
	if err != nil {
		slog.Error("BulkCategorize", slog.String("error", err.Error()))
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
	            AND EXISTS (SELECT 1 FROM payees p WHERE p.id = $1 AND p.user_id = $3)
	            AND NOT EXISTS (SELECT 1 FROM accounts closed_acct WHERE closed_acct.id = transactions.account_id AND closed_acct.closed)`
	result, err := db.Pool.Exec(c, query, req.PayeeID, req.TransactionIDs, auth.GetUserID(c))
	if err != nil {
		slog.Error("BulkUpdatePayee", slog.String("error", err.Error()))
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
	            AND EXISTS (SELECT 1 FROM billing_cycles bc WHERE bc.id = $1 AND bc.user_id = $3)
	            AND NOT EXISTS (SELECT 1 FROM accounts closed_acct WHERE closed_acct.id = transactions.account_id AND closed_acct.closed)`
	result, err := db.Pool.Exec(c, query, req.BillingCycleID, req.TransactionIDs, auth.GetUserID(c))
	if err != nil {
		slog.Error("BulkUpdateBillingCycle", slog.String("error", err.Error()))
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

	query := `DELETE FROM transactions WHERE id = ANY($1) AND user_id = $2
	          AND NOT EXISTS (SELECT 1 FROM accounts closed_acct WHERE closed_acct.id = transactions.account_id AND closed_acct.closed)`
	result, err := db.Pool.Exec(c, query, req.TransactionIDs, auth.GetUserID(c))
	if err != nil {
		slog.Error("BulkDeleteTransactions", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	c.JSON(http.StatusOK, gin.H{"deleted": result.RowsAffected()})
}
