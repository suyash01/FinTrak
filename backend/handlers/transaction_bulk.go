package handlers

import (
	"fmt"
	"log/slog"
	"net/http"

	"github.com/fintrak/backend/auth"
	"github.com/fintrak/backend/internal/validation"
	"github.com/fintrak/backend/models"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// The bulk row-naming writes: one category / payee / billing cycle / tag
// applied to a set of transactions the user selected.
//
// They share one design and it is worth naming, because it is what makes this
// file look repetitive and is easy to break by "tidying":
//
//   - One statement, not a loop. Each handler is a single UPDATE with
//     `id = ANY($n)`, so the whole selection costs one round trip and either
//     lands or does not.
//   - Ownership is folded into the WHERE rather than checked per row. The
//     category/payee/billing-cycle targets are validated by an EXISTS clause
//     *on the same statement that writes*, so a target the user does not own
//     silently matches nothing rather than being written. The response count is
//     then honest about it: RowsAffected is what actually changed.
//   - Closed accounts are excluded everywhere by the same NOT EXISTS guard.
//     A closed account's transactions are immutable, so a bulk action silently
//     skips them instead of failing the whole selection.
//
// Every one of these is a `patch`-shaped op in the frontend's offline outbox
// (src/api/registry.ts), which is why a bulk write is queued as one entry rather
// than one per row — see AGENTS.md. None of them accepts a base version of the
// rows, so unlike a transaction edit none of them can be merged on replay; the
// callsite wiring that would supply one does not exist yet.

// BulkCategorize assigns one category to many of the user's transactions in a
// single UPDATE.
func (srv *Server) BulkCategorize(c *gin.Context) {
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
	//
	// Clearing and setting are two different statements rather than one
	// parameterized UPDATE because clearing has no target to validate: the
	// sentinel branch has no `$1` category at all, so the placeholder numbering
	// genuinely differs between them and a single statement would have to carry
	// a cast and a NULLIF to make the one shape serve both cases.
	//
	// The sentinel is the string "uncategorized" rather than an empty
	// categoryId, following the Radix Select rule in the frontend: a sentinel is
	// the only way to express "no category" through a control that cannot hold
	// an empty option value.
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
	result, err := srv.db.Exec(c, query, args...)
	if err != nil {
		slog.Error("BulkCategorize", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	c.JSON(http.StatusOK, gin.H{"updated": result.RowsAffected()})
}

// BulkUpdatePayee assigns one payee to many of the user's transactions in a
// single UPDATE.
//
// Unlike BulkCategorize there is no "no payee" sentinel branch: an empty
// PayeeID fails the request binding rather than clearing. Clearing a payee is
// a `putPartial`-shaped problem (see the file header) and is not offered here.
func (srv *Server) BulkUpdatePayee(c *gin.Context) {
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
	result, err := srv.db.Exec(c, query, req.PayeeID, req.TransactionIDs, auth.GetUserID(c))
	if err != nil {
		slog.Error("BulkUpdatePayee", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	c.JSON(http.StatusOK, gin.H{"updated": result.RowsAffected()})
}

// BulkUpdateBillingCycle attaches one billing cycle to many of the user's
// transactions in a single UPDATE.
func (srv *Server) BulkUpdateBillingCycle(c *gin.Context) {
	var req models.BulkBillingCycleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		validation.RespondBindError(c, err)
		return
	}
	if len(req.TransactionIDs) > maxBulkBatch {
		validation.RespondError(c, fmt.Sprintf("too many transaction ids (max %d per request)", maxBulkBatch), http.StatusBadRequest)
		return
	}

	// The cycle must belong to the user AND to each transaction's own account,
	// so a bulk assignment can't attach one account's transactions to another
	// account's cycle. The detach flag is cleared with it: a row the user had
	// detached ("Unassigned") and then assigned by hand is assigned, not
	// detached, so a later move of its date/account may re-derive it.
	query := `UPDATE transactions SET billing_cycle_id = $1, billing_cycle_detached = FALSE
	          WHERE id = ANY($2) AND user_id = $3
	            AND EXISTS (SELECT 1 FROM billing_cycles bc WHERE bc.id = $1 AND bc.user_id = $3 AND bc.account_id = transactions.account_id)
	            AND NOT EXISTS (SELECT 1 FROM accounts closed_acct WHERE closed_acct.id = transactions.account_id AND closed_acct.closed)`
	result, err := srv.db.Exec(c, query, req.BillingCycleID, req.TransactionIDs, auth.GetUserID(c))
	if err != nil {
		slog.Error("BulkUpdateBillingCycle", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	c.JSON(http.StatusOK, gin.H{"updated": result.RowsAffected()})
}

// BulkDeleteTransactions deletes many of the user's transactions in one call.
//
// This is the one bulk write with no target to validate, which is why it has
// only two predicates where the others have three. What it does still cascade:
// links, loan attachments and recurring attachments referencing a deleted
// transaction are removed by the schema's ON DELETE, so a delete here can
// silently detach a transaction from a loan's repayment record. That is
// inherent to deleting the row rather than something this statement chooses.
//
// The response key is `deleted` rather than the `updated` the sibling handlers
// return, because the UI reports the two differently — a partial delete of a
// selection is a normal outcome here (ids that do not exist, or rows on closed
// accounts) and the count is what tells the user which.
func (srv *Server) BulkDeleteTransactions(c *gin.Context) {
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
	result, err := srv.db.Exec(c, query, req.TransactionIDs, auth.GetUserID(c))
	if err != nil {
		slog.Error("BulkDeleteTransactions", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	c.JSON(http.StatusOK, gin.H{"deleted": result.RowsAffected()})
}
