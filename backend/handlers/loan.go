package handlers

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"time"

	"github.com/fintrak/backend/auth"
	"github.com/fintrak/backend/db"
	"github.com/fintrak/backend/internal/money"
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
func (srv *Server) BulkLinkLoan(c *gin.Context) {
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
	if err := srv.db.QueryRow(c,
		"SELECT COUNT(*) FROM transactions t WHERE t.id = ANY($1) AND t.user_id = $2",
		req.TransactionIDs, userID).Scan(&owned); err != nil {
		slog.Error("BulkLinkLoan (checking transactions)", slog.String("error", err.Error()))
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
	if err := srv.db.QueryRow(c,
		`SELECT COUNT(*) FROM transactions t
		 JOIN accounts a ON t.account_id = a.id
		 WHERE t.id = ANY($1) AND t.user_id = $2 AND a.account_type_id = 'loan'`,
		req.TransactionIDs, userID).Scan(&onLoan); err != nil {
		slog.Error("BulkLinkLoan (checking loan-account transactions)", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	if onLoan > 0 {
		validation.RespondError(c, "one or more transactions belong to a loan account and cannot be attached", http.StatusBadRequest)
		return
	}

	// Detach path: loanAccountId absent/null removes the attachments.
	if req.LoanAccountID == nil {
		result, err := srv.db.Exec(c,
			"DELETE FROM loan_attachments WHERE transaction_id = ANY($1) AND user_id = $2",
			req.TransactionIDs, userID)
		if err != nil {
			slog.Error("BulkLinkLoan (detach)", slog.String("error", err.Error()))
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
	err := srv.db.QueryRow(c,
		"SELECT user_id, account_type_id FROM accounts WHERE id = $1",
		*req.LoanAccountID).Scan(&ownerID, &accountTypeID)
	if errors.Is(err, pgx.ErrNoRows) {
		validation.RespondError(c, "loan account not found", http.StatusNotFound)
		return
	}
	if err != nil {
		slog.Error("BulkLinkLoan (checking loan account)", slog.String("error", err.Error()))
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
	if err := srv.db.QueryRow(c,
		"SELECT COUNT(*) FROM loan_attachments WHERE transaction_id = ANY($1) AND user_id = $2",
		req.TransactionIDs, userID).Scan(&already); err != nil {
		slog.Error("BulkLinkLoan (checking existing attachments)", slog.String("error", err.Error()))
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
	err = db.WithTx(c, srv.db, func(tx pgx.Tx) error {
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
		slog.Error("BulkLinkLoan (attach)", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	c.JSON(http.StatusOK, gin.H{"attached": attached})
}

// maxLoanTenureMonths mirrors the CHECK on loan_schedules.tenure_months.
const maxLoanTenureMonths = 600

// GetLoanSchedule returns a loan account's amortization schedule: the terms, the
// generated table (each installment split into principal and interest), and the
// progress derived from the EMI transactions attached to the loan. The schedule
// is optional, so an unconfigured loan answers 200 with `schedule: null` rather
// than a 404; a missing or non-loan account is still an error.
func (srv *Server) GetLoanSchedule(c *gin.Context) {
	accountID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		validation.RespondError(c, "invalid id", http.StatusBadRequest)
		return
	}
	userID := auth.GetUserID(c)
	name, ok := srv.loanAccountGuard(c, userID, accountID)
	if !ok {
		return
	}

	detail, err := srv.loadLoanScheduleDetail(c, userID, accountID)
	if err != nil {
		slog.Error("GetLoanSchedule", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	detail.LoanAccountName = name
	c.JSON(http.StatusOK, detail)
}

// UpsertLoanSchedule creates or replaces a loan account's amortization schedule
// and returns the same detail as GET, so the caller can render the generated
// table without a second request.
func (srv *Server) UpsertLoanSchedule(c *gin.Context) {
	accountID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		validation.RespondError(c, "invalid id", http.StatusBadRequest)
		return
	}
	var req models.LoanScheduleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		validation.RespondBindError(c, err)
		return
	}
	if req.Principal <= 0 {
		validation.RespondError(c, "principal must be positive", http.StatusBadRequest)
		return
	}
	if req.AnnualRateBps < 0 {
		validation.RespondError(c, "annualRateBps must not be negative", http.StatusBadRequest)
		return
	}
	if req.TenureMonths < 1 || req.TenureMonths > maxLoanTenureMonths {
		validation.RespondError(c, fmt.Sprintf("tenureMonths must be between 1 and %d", maxLoanTenureMonths), http.StatusBadRequest)
		return
	}
	start, err := parseRecurringDate(req.StartDate)
	if err != nil {
		validation.RespondError(c, err.Error(), http.StatusBadRequest)
		return
	}

	userID := auth.GetUserID(c)
	name, ok := srv.loanAccountGuard(c, userID, accountID)
	if !ok {
		return
	}

	// The INSERT ... SELECT re-checks ownership and the loan type inside the
	// statement, so a cross-user or non-loan account can never be written even
	// if it changes between the guard and the write.
	if _, err := srv.db.Exec(c,
		`INSERT INTO loan_schedules (loan_account_id, user_id, principal, annual_rate_bps, tenure_months, start_date)
		 SELECT a.id, $2, $3, $4, $5, $6
		 FROM accounts a
		 WHERE a.id = $1 AND a.user_id = $2 AND a.account_type_id = 'loan'
		 ON CONFLICT (user_id, loan_account_id) DO UPDATE
		 SET principal = EXCLUDED.principal,
		     annual_rate_bps = EXCLUDED.annual_rate_bps,
		     tenure_months = EXCLUDED.tenure_months,
		     start_date = EXCLUDED.start_date,
		     updated_at = NOW()`,
		accountID, userID, req.Principal, req.AnnualRateBps, req.TenureMonths, start,
	); err != nil {
		slog.Error("UpsertLoanSchedule", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	detail, err := srv.loadLoanScheduleDetail(c, userID, accountID)
	if err != nil {
		slog.Error("UpsertLoanSchedule (reload)", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	detail.LoanAccountName = name
	c.JSON(http.StatusOK, detail)
}

// DeleteLoanSchedule removes a loan account's amortization schedule. It is
// idempotent: deleting an unconfigured loan reports `deleted: 0`.
func (srv *Server) DeleteLoanSchedule(c *gin.Context) {
	accountID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		validation.RespondError(c, "invalid id", http.StatusBadRequest)
		return
	}
	userID := auth.GetUserID(c)
	if _, ok := srv.loanAccountGuard(c, userID, accountID); !ok {
		return
	}

	res, err := srv.db.Exec(c, "DELETE FROM loan_schedules WHERE loan_account_id = $1 AND user_id = $2", accountID, userID)
	if err != nil {
		slog.Error("DeleteLoanSchedule", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	c.JSON(http.StatusOK, models.DeleteLoanScheduleResult{Deleted: res.RowsAffected()})
}

// loanAccountGuard resolves the loan account named by the route, writing the
// matching 4xx and returning ok=false when it does not exist, is not the
// caller's, or is not a Loan / EMI account. It returns the account's name so
// callers can echo it without a second lookup.
func (srv *Server) loanAccountGuard(c *gin.Context, userID, accountID uuid.UUID) (string, bool) {
	var name, accountType string
	err := srv.db.QueryRow(c,
		"SELECT name, account_type_id FROM accounts WHERE id = $1 AND user_id = $2",
		accountID, userID).Scan(&name, &accountType)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		validation.RespondError(c, "account not found", http.StatusNotFound)
		return "", false
	case err != nil:
		slog.Error("loanAccountGuard", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return "", false
	case accountType != loanAccountTypeID:
		validation.RespondError(c, "not a loan account", http.StatusBadRequest)
		return "", false
	}
	return name, true
}

// loadLoanScheduleDetail reads the loan's schedule (if any) and the EMI
// transactions attached to it, then generates the amortization table and the
// progress figures. Attached payments are matched to installments in date
// order: the Nth EMI payment pays the Nth installment, the way a lender numbers
// them, so paying early or late does not misalign the table.
func (srv *Server) loadLoanScheduleDetail(ctx context.Context, userID, loanAccountID uuid.UUID) (models.LoanScheduleDetail, error) {
	detail := models.LoanScheduleDetail{Entries: []models.LoanScheduleEntry{}}

	var sched models.LoanSchedule
	err := srv.db.QueryRow(ctx,
		`SELECT id, loan_account_id, principal, annual_rate_bps, tenure_months, start_date, created_at, updated_at
		 FROM loan_schedules WHERE loan_account_id = $1 AND user_id = $2`,
		loanAccountID, userID,
	).Scan(&sched.ID, &sched.LoanAccountID, &sched.Principal, &sched.AnnualRateBps,
		&sched.TenureMonths, &sched.StartDate, &sched.CreatedAt, &sched.UpdatedAt)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return detail, nil
	case err != nil:
		return detail, err
	}
	detail.Schedule = &sched

	emi, entries := loanAmortization(sched.Principal, sched.AnnualRateBps, sched.TenureMonths, sched.StartDate)
	detail.EMI = emi
	detail.Entries = entries
	for _, e := range entries {
		detail.TotalInterest += e.Interest
		detail.TotalPayable += e.Amount
	}

	payments, err := srv.loadLoanPayments(ctx, userID, loanAccountID)
	if err != nil {
		return detail, err
	}
	paid := 0
	for _, p := range payments {
		if p.credit {
			// A refund against an EMI reduces what was actually paid but does
			// not cover another installment.
			detail.PaidAmount -= p.amount
			continue
		}
		detail.PaidAmount += p.amount
		if paid < len(entries) {
			txnID := p.id
			detail.Entries[paid].Paid = true
			detail.Entries[paid].TransactionID = &txnID
			paid++
		}
	}
	detail.PaidInstallments = paid
	for i := range paid {
		detail.PrincipalPaid += entries[i].Principal
		detail.InterestPaid += entries[i].Interest
	}
	detail.OutstandingPrincipal = sched.Principal - detail.PrincipalPaid
	if detail.OutstandingPrincipal < 0 {
		detail.OutstandingPrincipal = 0
	}
	detail.Completed = paid >= len(entries)
	if !detail.Completed {
		next := entries[paid].DueDate
		detail.NextDueDate = &next
	}
	return detail, nil
}

// loanPayment is one EMI transaction attached to a loan, in the order it counts
// toward the installments.
type loanPayment struct {
	id     uuid.UUID
	amount money.Amount
	credit bool
}

// loadLoanPayments reads the loan's attached EMI transactions, oldest first
// (created_at breaks a same-day tie so the numbering is stable).
func (srv *Server) loadLoanPayments(ctx context.Context, userID, loanAccountID uuid.UUID) ([]loanPayment, error) {
	rows, err := srv.db.Query(ctx,
		`SELECT t.id, t.amount, t.type
		 FROM loan_attachments la
		 JOIN transactions t ON t.id = la.transaction_id
		 WHERE la.loan_account_id = $1 AND la.user_id = $2
		 ORDER BY t.date, t.created_at`,
		loanAccountID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []loanPayment
	for rows.Next() {
		var p loanPayment
		var txnType string
		if err := rows.Scan(&p.id, &p.amount, &txnType); err != nil {
			return nil, err
		}
		p.credit = txnType == "credit"
		out = append(out, p)
	}
	return out, rows.Err()
}

// loanAmortization generates the amortization table for a schedule: every
// installment split into principal and interest, with its remaining balance.
// Monthly due dates come from the shared anchored-month helper the recurring
// engine uses, so a schedule starting on the 31st clamps to month length instead
// of drifting.
//
// The final installment clears whatever principal is left, so the table repays
// the loan exactly despite per-installment rounding. The rate factor is a ratio,
// not money: it is the one place float64 legitimately appears, and the EMI is
// rounded to the nearest minor unit before any further arithmetic.
func loanAmortization(principal money.Amount, annualRateBps, tenureMonths int, start time.Time) (money.Amount, []models.LoanScheduleEntry) {
	if tenureMonths < 1 {
		return principal, []models.LoanScheduleEntry{}
	}
	monthlyRate := float64(annualRateBps) / 120000.0
	emi := loanEMI(principal, monthlyRate, tenureMonths)

	anchor := dateOnly(start)
	remaining := principal
	entries := make([]models.LoanScheduleEntry, 0, tenureMonths)
	for i := 1; i <= tenureMonths; i++ {
		interest := money.Amount(math.Round(float64(remaining) * monthlyRate))
		var principalPart money.Amount
		if i == tenureMonths {
			principalPart = remaining
		} else {
			principalPart = emi - interest
			if principalPart < 0 {
				principalPart = 0
			}
		}
		remaining -= principalPart
		entries = append(entries, models.LoanScheduleEntry{
			Number:    i,
			DueDate:   addMonthsAnchored(anchor, i-1),
			Amount:    principalPart + interest,
			Principal: principalPart,
			Interest:  interest,
			Balance:   remaining,
		})
	}
	return emi, entries
}

// loanEMI returns the equated monthly installment: P * r * (1+r)^n / ((1+r)^n - 1)
// for a monthly rate r, or an even principal split when the loan is
// interest-free.
func loanEMI(principal money.Amount, monthlyRate float64, months int) money.Amount {
	if months <= 0 {
		return principal
	}
	if monthlyRate <= 0 {
		return money.Amount(math.Round(float64(principal) / float64(months)))
	}
	growth := math.Pow(1+monthlyRate, float64(months))
	return money.Amount(math.Round(float64(principal) * monthlyRate * growth / (growth - 1)))
}
