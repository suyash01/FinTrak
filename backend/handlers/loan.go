package handlers

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/fintrak/backend/auth"
	"github.com/fintrak/backend/db"
	"github.com/fintrak/backend/internal/validation"
	"github.com/fintrak/backend/models"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// loanAccountTypeID is the built-in account type for Loan / EMI accounts.
// Loan accounts hold no transactions of their own: EMI payments are
// transactions on other accounts that are attached via loan_attachments.
const loanAccountTypeID = "loan"

// BulkLinkLoan attaches many transactions to a single loan/EMI account, or
// detaches them from whatever loan account they are currently attached to
// when loanAccountId is omitted/null. One transaction can be attached to at
// most one loan account (UNIQUE on loan_attachments.transaction_id), enforced
// here with a pre-check plus a constraint-violation guard.
//
// Attaching also sets each transaction's payee to the loan account's linked
// payee (so the payee column reads as the loan account). Detaching leaves
// payees untouched. Closing an account does not affect linking:
// attaching/detaching is the "linking" that remains possible on closed
// accounts, so this endpoint does not consult the closed flag.
func BulkLinkLoan(c *gin.Context) {
	var req models.BulkLoanRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		validation.RespondBindError(c, err)
		return
	}
	if len(req.TransactionIDs) == 0 {
		validation.RespondError(c, "no transaction ids provided", http.StatusBadRequest)
		return
	}
	if len(req.TransactionIDs) > maxBulkBatch {
		validation.RespondError(c, fmt.Sprintf("too many transaction ids (max %d per request)", maxBulkBatch), http.StatusBadRequest)
		return
	}

	userID := auth.GetUserID(c)

	// Every target transaction must exist and belong to the user.
	var owned int
	if err := db.Pool.QueryRow(c,
		"SELECT COUNT(*) FROM transactions t WHERE t.id = ANY($1) AND t.user_id = $2",
		req.TransactionIDs, userID).Scan(&owned); err != nil {
		slog.Error("BulkLinkLoan (checking transactions)", "error", err)
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	if owned != len(req.TransactionIDs) {
		validation.RespondError(c, "one or more transactions not found", http.StatusBadRequest)
		return
	}

	// None of the transactions may live on a loan account: loan accounts have
	// no transactions of their own, so such a row would be nonsense.
	var onLoan int
	if err := db.Pool.QueryRow(c,
		`SELECT COUNT(*) FROM transactions t
		 JOIN accounts a ON t.account_id = a.id
		 WHERE t.id = ANY($1) AND t.user_id = $2 AND a.account_type_id = 'loan'`,
		req.TransactionIDs, userID).Scan(&onLoan); err != nil {
		slog.Error("BulkLinkLoan (checking loan-account transactions)", "error", err)
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	if onLoan > 0 {
		validation.RespondError(c, "one or more transactions belong to a loan account and cannot be attached", http.StatusBadRequest)
		return
	}

	// Detach path: loanAccountId absent/null removes the attachments.
	if req.LoanAccountID == nil {
		result, err := db.Pool.Exec(c,
			"DELETE FROM loan_attachments WHERE transaction_id = ANY($1) AND user_id = $2",
			req.TransactionIDs, userID)
		if err != nil {
			slog.Error("BulkLinkLoan (detach)", "error", err)
			validation.RespondError(c, "internal server error", http.StatusInternalServerError)
			return
		}
		c.JSON(http.StatusOK, gin.H{"detached": result.RowsAffected()})
		return
	}

	// Attach path: the target must be an owned loan account. Closed loan
	// accounts can still receive attachments (linking is the one operation
	// allowed on closed accounts), so the closed flag is not consulted.
	var ownerID uuid.UUID
	var accountTypeID string
	err := db.Pool.QueryRow(c,
		"SELECT user_id, account_type_id FROM accounts WHERE id = $1",
		*req.LoanAccountID).Scan(&ownerID, &accountTypeID)
	if errors.Is(err, pgx.ErrNoRows) {
		validation.RespondError(c, "loan account not found", http.StatusNotFound)
		return
	}
	if err != nil {
		slog.Error("BulkLinkLoan (checking loan account)", "error", err)
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	if ownerID != userID {
		validation.RespondError(c, "forbidden", http.StatusForbidden)
		return
	}
	if accountTypeID != loanAccountTypeID {
		validation.RespondError(c, "transactions can only be attached to a Loan / EMI account", http.StatusBadRequest)
		return
	}

	// Pre-check that none of the transactions is already attached (one
	// transaction -> one loan account). The UNIQUE index is the hard guard;
	// this check produces a clean 409 instead of a constraint violation.
	var already int
	if err := db.Pool.QueryRow(c,
		"SELECT COUNT(*) FROM loan_attachments WHERE transaction_id = ANY($1) AND user_id = $2",
		req.TransactionIDs, userID).Scan(&already); err != nil {
		slog.Error("BulkLinkLoan (checking existing attachments)", "error", err)
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	if already > 0 {
		validation.RespondError(c,
			fmt.Sprintf("%d of the selected transactions are already linked to a loan account — detach them first", already),
			http.StatusConflict)
		return
	}

	// The write (attachment rows + payee sync) is atomic so a failure never
	// leaves transactions half-linked.
	var attached int64
	err = db.WithTx(c, func(tx pgx.Tx) error {
		res, err := tx.Exec(c,
			`INSERT INTO loan_attachments (loan_account_id, transaction_id, user_id)
			 SELECT $1, t, $3 FROM unnest($2::uuid[]) AS t`,
			*req.LoanAccountID, req.TransactionIDs, userID)
		if err != nil {
			return err
		}
		attached = res.RowsAffected()

		// Sync the transactions' payee to the loan account's linked payee so
		// an EMI payment reads as "paid to <loan account>" in the payee column
		// instead of needing a separate indicator. Loans whose linked payee
		// was deleted manually leave payees untouched.
		var payeeID uuid.UUID
		err = tx.QueryRow(c,
			"SELECT id FROM payees WHERE account_id = $1 AND user_id = $2",
			*req.LoanAccountID, userID).Scan(&payeeID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		_, err = tx.Exec(c,
			"UPDATE transactions SET payee_id = $1 WHERE id = ANY($2) AND user_id = $3",
			payeeID, req.TransactionIDs, userID)
		return err
	})
	if err != nil {
		// Race guard: a concurrent attach can still hit the unique index.
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			validation.RespondError(c, "one or more transactions are already linked to a loan account", http.StatusConflict)
			return
		}
		slog.Error("BulkLinkLoan (attach)", "error", err)
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	c.JSON(http.StatusOK, gin.H{"attached": attached})
}
