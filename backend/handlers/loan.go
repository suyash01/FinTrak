package handlers

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"strings"
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

// errLoanDisbursementCredit reports that the exclusivity guard inside the
// attach write refused a transaction because it is a loan's disbursement credit
// (a transaction is either an EMI payment or a disbursement, never both).
var errLoanDisbursementCredit = errors.New("transaction is a loan's disbursement credit")

// errLoanTransferTargetChanged reports that the schedule a balance transfer
// created on its target is no longer the transfer's: the target's terms were
// replaced (or another transfer landed on it) after the transfer was recorded,
// so deleting the transfer may not delete that schedule.
var errLoanTransferTargetChanged = errors.New("the target loan's terms changed after the transfer")

// BulkLinkLoan attaches many transactions to a single loan/EMI account, or
// detaches them from whatever loan account they are currently attached to
// when loanAccountId is omitted/null. One transaction can be attached to at
// most one loan account (UNIQUE on loan_attachments.transaction_id), enforced
// by the insert itself plus a constraint-violation guard, and a transaction
// that is a loan's disbursement credit can never become an EMI payment: the
// same insert skips it and the short row count answers 409.
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
	// leaves transactions half-linked. The credit that released a loan is money
	// received, not a repayment, so the INSERT itself refuses a transaction that
	// is a loan's disbursement credit — a pre-check could be won by a concurrent
	// disbursement link. The short row count is that refusal.
	var attached int64
	err = db.WithTx(c, srv.db, func(tx pgx.Tx) error {
		res, err := tx.Exec(c,
			`INSERT INTO loan_attachments (loan_account_id, transaction_id, user_id)
			 SELECT $1, t, $3 FROM unnest($2::uuid[]) AS t
			 WHERE NOT EXISTS (
			     SELECT 1 FROM loan_disbursements ld
			     WHERE ld.transaction_id = t AND ld.user_id = $3
			 )`,
			*req.LoanAccountID, req.TransactionIDs, userID)
		if err != nil {
			return err
		}
		attached = res.RowsAffected()
		if attached != int64(len(req.TransactionIDs)) {
			return errLoanDisbursementCredit
		}

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
		if errors.Is(err, errLoanDisbursementCredit) {
			validation.RespondError(c, "one or more transactions are a loan's disbursement credit and cannot be attached as EMI payments", http.StatusConflict)
			return
		}
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

// maxLoanAnnualRateBps bounds the rate for arithmetic, not for policy:
// loanEMI raises (1+r) to the tenure, so a rate the JSON decoder accepts
// (annual_rate_bps is a plain INTEGER) overflows float64 to +Inf — with the
// longest tenure the EMI turns into NaN, which money.Amount converts to an
// out-of-range int64. The threshold is ~271,000 bps (2,710%) at 600 months;
// 100,000 bps (1,000%) leaves a 2.7x margin and still admits every rate any
// real lender quotes.
const maxLoanAnnualRateBps = 100000

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
	if req.ProcessingFee < 0 {
		validation.RespondError(c, "processingFee must not be negative", http.StatusBadRequest)
		return
	}
	if req.AnnualRateBps < 0 {
		validation.RespondError(c, "annualRateBps must not be negative", http.StatusBadRequest)
		return
	}
	if req.AnnualRateBps > maxLoanAnnualRateBps {
		validation.RespondError(c, fmt.Sprintf("annualRateBps must not exceed %d", maxLoanAnnualRateBps), http.StatusBadRequest)
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
	// An empty disbursal date means the first period is a whole month, which is
	// what schedules without one have always been.
	var disbursal *time.Time
	if strings.TrimSpace(req.DisbursalDate) != "" {
		d, err := parseRecurringDate(req.DisbursalDate)
		if err != nil {
			validation.RespondError(c, err.Error(), http.StatusBadRequest)
			return
		}
		if !d.Before(start) {
			validation.RespondError(c, "disbursalDate must be before the first installment date", http.StatusBadRequest)
			return
		}
		disbursal = &d
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
		`INSERT INTO loan_schedules (loan_account_id, user_id, principal, processing_fee, annual_rate_bps, tenure_months, start_date, disbursal_date)
		 SELECT a.id, $2, $3, $4, $5, $6, $7, $8
		 FROM accounts a
		 WHERE a.id = $1 AND a.user_id = $2 AND a.account_type_id = 'loan'
		 ON CONFLICT (user_id, loan_account_id) DO UPDATE
		 SET principal = EXCLUDED.principal,
		     processing_fee = EXCLUDED.processing_fee,
		     annual_rate_bps = EXCLUDED.annual_rate_bps,
		     tenure_months = EXCLUDED.tenure_months,
		     start_date = EXCLUDED.start_date,
		     disbursal_date = EXCLUDED.disbursal_date,
		     updated_at = NOW()`,
		accountID, userID, req.Principal, req.ProcessingFee, req.AnnualRateBps, req.TenureMonths, start, disbursal,
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

// TransferLoanBalance moves a loan's remaining principal to another loan: the
// source is settled at its outstanding balance and the target absorbs that
// amount, recasting the installments it still owes over the larger balance. A
// target that has no schedule yet has nothing to recast, so the transferred
// amount starts one from the terms in the request instead.
//
// The amount is never taken from the caller — a balance transfer moves what the
// source still owes, the same figure its detail reports as the outstanding
// principal — and the source's settled state, the target's recast, and every
// cancelled installment are derived from the single transfer row, so the
// operation is reversible by deleting it.
func (srv *Server) TransferLoanBalance(c *gin.Context) {
	sourceID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		validation.RespondError(c, "invalid id", http.StatusBadRequest)
		return
	}
	var req models.LoanTransferRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		validation.RespondBindError(c, err)
		return
	}
	transferDate, err := parseRecurringDate(req.TransferDate)
	if err != nil {
		validation.RespondError(c, err.Error(), http.StatusBadRequest)
		return
	}
	if req.ToLoanAccountID == sourceID {
		validation.RespondError(c, "the target loan must be a different account", http.StatusBadRequest)
		return
	}

	userID := auth.GetUserID(c)
	sourceName, ok := srv.loanAccountGuard(c, userID, sourceID)
	if !ok {
		return
	}

	source, err := srv.loadLoanScheduleDetail(c, userID, sourceID)
	if err != nil {
		slog.Error("TransferLoanBalance (source)", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	if source.Schedule == nil {
		validation.RespondError(c, "the source loan has no amortization schedule — set its terms first", http.StatusBadRequest)
		return
	}
	if source.SettledOn != nil {
		validation.RespondError(c, "this loan is already settled by a balance transfer", http.StatusBadRequest)
		return
	}
	// What moves is the payoff, not just the principal: settling mid-period also
	// clears the interest accrued since the source's last EMI payment.
	quote := loanPayoffFor(source, transferDate)
	amount := quote.Payoff
	if amount <= 0 {
		validation.RespondError(c, "the source loan has nothing left to transfer on that date", http.StatusBadRequest)
		return
	}

	// The target must be the caller's own open Loan / EMI account; a closed one
	// should not take on new debt.
	var targetOwner uuid.UUID
	var targetName, targetType string
	var targetClosed bool
	err = srv.db.QueryRow(c,
		"SELECT user_id, name, account_type_id, closed FROM accounts WHERE id = $1",
		req.ToLoanAccountID).Scan(&targetOwner, &targetName, &targetType, &targetClosed)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		validation.RespondError(c, "target loan account not found", http.StatusNotFound)
		return
	case err != nil:
		slog.Error("TransferLoanBalance (target)", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	case targetOwner != userID:
		validation.RespondError(c, "forbidden", http.StatusForbidden)
		return
	case targetType != loanAccountTypeID:
		validation.RespondError(c, "the target account is not a Loan / EMI account", http.StatusBadRequest)
		return
	case targetClosed:
		validation.RespondError(c, "the target loan account is closed", http.StatusBadRequest)
		return
	}

	target, err := srv.loadLoanScheduleDetail(c, userID, req.ToLoanAccountID)
	if err != nil {
		slog.Error("TransferLoanBalance (target detail)", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	// What the transfer does to the target: an explicit mode, or the automatic
	// one — recast an existing table, or start one when the target has no terms
	// of its own.
	mode := req.Mode
	if mode == "" {
		mode = models.LoanTransferRecast
		if target.Schedule == nil {
			mode = models.LoanTransferOpens
		}
	}

	var targetTerms *loanTerms
	switch mode {
	case models.LoanTransferOpens:
		if target.Schedule != nil {
			validation.RespondError(c, "the target loan already has a schedule — recast it or take the balance over instead", http.StatusBadRequest)
			return
		}
		terms, err := transferTargetTerms(req, amount)
		if err != nil {
			validation.RespondError(c, err.Error(), http.StatusBadRequest)
			return
		}
		targetTerms = terms
	case models.LoanTransferRecast:
		if target.Schedule == nil {
			validation.RespondError(c, "the target loan has no schedule to recast — set its terms first", http.StatusBadRequest)
			return
		}
		if !hasInstallmentsAfter(target.Entries, transferDate) {
			// The balance has to land on an installment that is still owed,
			// which is what forces the recast; a target whose remaining dues are
			// already paid (or already cancelled) has nowhere to put it.
			validation.RespondError(c, "the target loan has no unpaid installment due after the transfer date", http.StatusBadRequest)
			return
		}
	case models.LoanTransferTakeover:
		if target.Schedule == nil {
			validation.RespondError(c, "the target loan has no schedule to take the balance out of — set its terms first", http.StatusBadRequest)
			return
		}
		// A takeover pays the source out of the target's own proceeds, so the
		// target has to have released at least that much.
		if target.Disbursement == nil || target.Disbursement.Net < amount {
			validation.RespondError(c, "the target loan's net disbursement cannot cover the transferred balance", http.StatusBadRequest)
			return
		}
	default:
		validation.RespondError(c, "mode must be recast, opens or takeover", http.StatusBadRequest)
		return
	}

	// The write is one transaction: the transfer row, plus the target's schedule
	// when the transfer opens it. Both tables are derived from the row, so an
	// interrupted request cannot leave one side moved and the other not.
	var transfer models.LoanPrincipalTransfer
	err = db.WithTx(c, srv.db, func(tx pgx.Tx) error {
		transfer = models.LoanPrincipalTransfer{
			ID:                  uuid.New(),
			FromLoanAccountID:   sourceID,
			FromLoanAccountName: sourceName,
			ToLoanAccountID:     req.ToLoanAccountID,
			ToLoanAccountName:   targetName,
			Amount:              amount,
			Principal:           quote.OutstandingPrincipal,
			AccruedInterest:     quote.AccruedInterest,
			TransferDate:        transferDate,
			Mode:                mode,
		}
		if err := tx.QueryRow(c,
			`INSERT INTO loan_transfers (id, user_id, from_loan_account_id, to_loan_account_id, amount, principal, transfer_date, mode)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
			 RETURNING created_at`,
			transfer.ID, userID, sourceID, req.ToLoanAccountID, amount, quote.OutstandingPrincipal,
			transferDate, transfer.Mode).Scan(&transfer.CreatedAt); err != nil {
			return err
		}
		if targetTerms == nil {
			return nil
		}
		_, err := tx.Exec(c,
			`INSERT INTO loan_schedules (loan_account_id, user_id, principal, processing_fee, annual_rate_bps, tenure_months, start_date)
			 SELECT a.id, $2, $3, 0, $4, $5, $6
			 FROM accounts a
			 WHERE a.id = $1 AND a.user_id = $2 AND a.account_type_id = 'loan'`,
			req.ToLoanAccountID, userID, amount, targetTerms.annualRateBps,
			targetTerms.tenureMonths, targetTerms.firstDueDate)
		return err
	})
	if err != nil {
		// Race guard: a concurrent transfer of the same source hits the unique
		// constraint on the source loan.
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			validation.RespondError(c, "this loan is already settled by a balance transfer", http.StatusConflict)
			return
		}
		slog.Error("TransferLoanBalance", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	res := models.LoanTransferResult{Transfer: transfer}
	if res.Source, err = srv.loadLoanScheduleDetail(c, userID, sourceID); err != nil {
		slog.Error("TransferLoanBalance (source reload)", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	res.Source.LoanAccountName = sourceName
	if res.Target, err = srv.loadLoanScheduleDetail(c, userID, req.ToLoanAccountID); err != nil {
		slog.Error("TransferLoanBalance (target reload)", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	res.Target.LoanAccountName = targetName
	c.JSON(http.StatusOK, res)
}

// transferTargetTerms builds the schedule a balance transfer starts on a target
// loan that has none: the transferred amount is its principal, and the rate,
// tenure and first installment date come from the request. All three are
// required, because a defaulted rate would silently start an interest-free loan
// on the amount that just moved.
func transferTargetTerms(req models.LoanTransferRequest, amount money.Amount) (*loanTerms, error) {
	if req.TargetAnnualRateBps == nil {
		return nil, errors.New("the target loan has no schedule — provide targetAnnualRateBps")
	}
	if req.TargetTenureMonths == nil {
		return nil, errors.New("the target loan has no schedule — provide targetTenureMonths")
	}
	if strings.TrimSpace(req.TargetStartDate) == "" {
		return nil, errors.New("the target loan has no schedule — provide targetStartDate")
	}
	if *req.TargetAnnualRateBps < 0 {
		return nil, errors.New("targetAnnualRateBps must not be negative")
	}
	if *req.TargetTenureMonths < 1 || *req.TargetTenureMonths > maxLoanTenureMonths {
		return nil, fmt.Errorf("targetTenureMonths must be between 1 and %d", maxLoanTenureMonths)
	}
	start, err := parseRecurringDate(req.TargetStartDate)
	if err != nil {
		return nil, err
	}
	return &loanTerms{
		principal:     amount,
		annualRateBps: *req.TargetAnnualRateBps,
		tenureMonths:  *req.TargetTenureMonths,
		firstDueDate:  start,
	}, nil
}

// hasInstallmentsAfter reports whether the loan still owes an installment due
// after date — the ones a transferred balance can be spread over. Already paid
// and already cancelled installments do not count: neither can be recast.
func hasInstallmentsAfter(entries []models.LoanScheduleEntry, date time.Time) bool {
	for _, e := range entries {
		if !e.Paid && !e.Cancelled && e.DueDate.After(date) {
			return true
		}
	}
	return false
}

// DeleteLoanTransfer removes a recorded balance transfer, which reverts both
// loans: the target's recast installments, the source's cancelled ones, and the
// target's disbursement are all derived from the row, so nothing else has to be
// undone.
//
// One thing is not derived: when the transfer opened the target's schedule (the
// target had no terms of its own), that schedule is deleted too — it holds the
// moved amount as its principal, so leaving it behind would keep the balance on
// a loan the transfer invented. A schedule the target already had is left alone,
// and so is one the user has since replaced: the schedule is removed only while
// it still carries the transferred amount as its principal and no other transfer
// has landed on the target, otherwise the request answers 409 rather than
// deleting terms the user authored. Both writes are one transaction so the two
// loans can never revert half way.
//
// The route names the transfer's source account, so a delete cannot be aimed at
// the same transfer through an unrelated account. It is idempotent, reporting
// `deleted: 0` when no such transfer exists.
func (srv *Server) DeleteLoanTransfer(c *gin.Context) {
	accountID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		validation.RespondError(c, "invalid id", http.StatusBadRequest)
		return
	}
	transferID, err := uuid.Parse(c.Param("transferId"))
	if err != nil {
		validation.RespondError(c, "invalid transfer id", http.StatusBadRequest)
		return
	}

	userID := auth.GetUserID(c)
	deleted := int64(0)
	err = db.WithTx(c, srv.db, func(tx pgx.Tx) error {
		var targetID uuid.UUID
		var mode string
		var amount money.Amount
		err := tx.QueryRow(c,
			`DELETE FROM loan_transfers WHERE id = $1 AND from_loan_account_id = $2 AND user_id = $3
			 RETURNING to_loan_account_id, mode, amount`,
			transferID, accountID, userID).Scan(&targetID, &mode, &amount)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		deleted = 1
		if mode != models.LoanTransferOpens {
			return nil
		}
		// The target's schedule is only the transfer's to remove while it still
		// *is* the one the transfer created: its principal is still the
		// transferred amount and no other transfer landed on the target. Once
		// the user has replaced the terms (UpsertLoanSchedule overwrites them
		// in place), deleting that schedule would destroy terms the user
		// authored, so the delete is refused instead — and with it the whole
		// revert, because a half-reverted pair of loans is worse.
		var scheduleID uuid.UUID
		err = tx.QueryRow(c,
			`DELETE FROM loan_schedules
			 WHERE loan_account_id = $1 AND user_id = $2 AND principal = $3
			   AND NOT EXISTS (
			       SELECT 1 FROM loan_transfers lt
			       WHERE lt.user_id = $2 AND lt.to_loan_account_id = $1
			   )
			 RETURNING id`,
			targetID, userID, amount).Scan(&scheduleID)
		if errors.Is(err, pgx.ErrNoRows) {
			// Nothing matched. The target no longer has a schedule at all when
			// the user deleted the terms themselves — then the transfer has
			// nothing left to undo there and the revert may proceed. A schedule
			// that is still there is simply not the transfer's any more.
			var scheduleExists bool
			if err := tx.QueryRow(c,
				"SELECT EXISTS (SELECT 1 FROM loan_schedules WHERE loan_account_id = $1 AND user_id = $2)",
				targetID, userID).Scan(&scheduleExists); err != nil {
				return err
			}
			if scheduleExists {
				return errLoanTransferTargetChanged
			}
			return nil
		}
		return err
	})
	if errors.Is(err, errLoanTransferTargetChanged) {
		validation.RespondError(c, "the target loan's terms have changed since this transfer — the schedule it created is no longer the transfer's to remove", http.StatusConflict)
		return
	}
	if err != nil {
		slog.Error("DeleteLoanTransfer", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	c.JSON(http.StatusOK, models.DeleteLoanTransferResult{Deleted: deleted})
}

// LinkLoanDisbursement records the bank credit that released a loan, so the
// disbursement the schedule implies (sanctioned principal less the lender's fee
// and less any takeover it funded) can be reconciled against what actually
// arrived. The comparison is reported, never enforced: a loan whose money
// arrived in a different amount is exactly what the user needs to see.
//
// The credit must be money received on one of the user's own accounts, and it
// cannot be a transaction that is already an EMI payment or another loan's
// disbursement. Linking a second credit to the same loan replaces the first.
func (srv *Server) LinkLoanDisbursement(c *gin.Context) {
	accountID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		validation.RespondError(c, "invalid id", http.StatusBadRequest)
		return
	}
	var req models.LoanDisbursementRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		validation.RespondBindError(c, err)
		return
	}
	userID := auth.GetUserID(c)
	name, ok := srv.loanAccountGuard(c, userID, accountID)
	if !ok {
		return
	}

	// The breakdown this reconciles is derived from the schedule, so there has
	// to be one.
	var hasSchedule bool
	if err := srv.db.QueryRow(c,
		"SELECT EXISTS (SELECT 1 FROM loan_schedules WHERE loan_account_id = $1 AND user_id = $2)",
		accountID, userID).Scan(&hasSchedule); err != nil {
		slog.Error("LinkLoanDisbursement (schedule check)", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	if !hasSchedule {
		validation.RespondError(c, "the loan has no amortization schedule — set its terms first", http.StatusBadRequest)
		return
	}

	var txnOwner uuid.UUID
	var txnType, accountType string
	err = srv.db.QueryRow(c,
		`SELECT t.user_id, t.type, a.account_type_id
		 FROM transactions t
		 JOIN accounts a ON a.id = t.account_id
		 WHERE t.id = $1`,
		req.TransactionID).Scan(&txnOwner, &txnType, &accountType)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		validation.RespondError(c, "transaction not found", http.StatusNotFound)
		return
	case err != nil:
		slog.Error("LinkLoanDisbursement (transaction)", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	case txnOwner != userID:
		validation.RespondError(c, "forbidden", http.StatusForbidden)
		return
	case accountType == loanAccountTypeID:
		validation.RespondError(c, "a loan account holds no transactions, so it cannot be the disbursement credit", http.StatusBadRequest)
		return
	case txnType != "credit":
		validation.RespondError(c, "the disbursement is money received, so the credit must be a credit transaction", http.StatusBadRequest)
		return
	}

	var linkedTo uuid.UUID
	err = srv.db.QueryRow(c,
		"SELECT loan_account_id FROM loan_disbursements WHERE transaction_id = $1 AND user_id = $2",
		req.TransactionID, userID).Scan(&linkedTo)
	switch {
	case err == nil && linkedTo != accountID:
		validation.RespondError(c, "that transaction is already the disbursement credit of another loan", http.StatusConflict)
		return
	case err != nil && !errors.Is(err, pgx.ErrNoRows):
		slog.Error("LinkLoanDisbursement (disbursement check)", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	// A transaction is either an EMI payment or a loan's disbursement, never
	// both: counting the money that released the loan as a repayment would
	// corrupt the progress figures. The write enforces that itself — it inserts
	// only while the transaction is not attached as an EMI payment, so a
	// concurrent attach cannot slip between a check and this insert — and a
	// zero row count is the refusal.
	res, err := srv.db.Exec(c,
		`INSERT INTO loan_disbursements (loan_account_id, transaction_id, user_id)
		 SELECT $1, $2, $3
		 WHERE NOT EXISTS (
		     SELECT 1 FROM loan_attachments la
		     WHERE la.transaction_id = $2 AND la.user_id = $3
		 )
		 ON CONFLICT (user_id, loan_account_id) DO UPDATE
		 SET transaction_id = EXCLUDED.transaction_id`,
		accountID, req.TransactionID, userID)
	if err != nil {
		// Race guard: a concurrent link of the same transaction.
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			validation.RespondError(c, "that transaction is already linked to a loan", http.StatusConflict)
			return
		}
		slog.Error("LinkLoanDisbursement", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	if res.RowsAffected() == 0 {
		validation.RespondError(c, "that transaction is already an EMI payment on a loan", http.StatusConflict)
		return
	}

	detail, err := srv.loadLoanScheduleDetail(c, userID, accountID)
	if err != nil {
		slog.Error("LinkLoanDisbursement (reload)", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	detail.LoanAccountName = name
	c.JSON(http.StatusOK, detail)
}

// GetLoanPayoff quotes what settling a loan on a date costs: the principal it
// still owes plus the interest accrued since its last EMI payment. It is the
// figure a balance transfer moves, and the number a lender's payoff quote should
// match — which is why the quote and the transfer share one computation.
//
// The date is required rather than defaulted to today, so the same request
// always answers the same thing.
func (srv *Server) GetLoanPayoff(c *gin.Context) {
	accountID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		validation.RespondError(c, "invalid id", http.StatusBadRequest)
		return
	}
	asOf, err := parseRecurringDate(c.Query("date"))
	if err != nil {
		validation.RespondError(c, "invalid date (expected YYYY-MM-DD)", http.StatusBadRequest)
		return
	}
	userID := auth.GetUserID(c)
	name, ok := srv.loanAccountGuard(c, userID, accountID)
	if !ok {
		return
	}

	detail, err := srv.loadLoanScheduleDetail(c, userID, accountID)
	if err != nil {
		slog.Error("GetLoanPayoff", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	if detail.Schedule == nil {
		validation.RespondError(c, "the loan has no amortization schedule — set its terms first", http.StatusBadRequest)
		return
	}
	if detail.SettledOn != nil {
		validation.RespondError(c, "this loan is already settled by a balance transfer", http.StatusBadRequest)
		return
	}

	quote := loanPayoffFor(detail, asOf)
	quote.LoanAccountName = name
	c.JSON(http.StatusOK, quote)
}

// UnlinkLoanDisbursement forgets which credit released the loan. Idempotent:
// unlinking a loan that has none reports `deleted: 0`.
func (srv *Server) UnlinkLoanDisbursement(c *gin.Context) {
	accountID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		validation.RespondError(c, "invalid id", http.StatusBadRequest)
		return
	}
	userID := auth.GetUserID(c)
	if _, ok := srv.loanAccountGuard(c, userID, accountID); !ok {
		return
	}

	res, err := srv.db.Exec(c,
		"DELETE FROM loan_disbursements WHERE loan_account_id = $1 AND user_id = $2", accountID, userID)
	if err != nil {
		slog.Error("UnlinkLoanDisbursement", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	c.JSON(http.StatusOK, models.DeleteLoanDisbursementResult{Deleted: res.RowsAffected()})
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

// loadLoanScheduleDetail reads the loan's schedule (if any), the balance
// transfers it took part in, and the EMI transactions attached to it, then
// generates the amortization table and the progress figures. Attached payments
// are matched to installments in date order: the Nth EMI payment pays the Nth
// installment, the way a lender numbers them, so paying early or late does not
// misalign the table.
//
// Every derived figure comes from the terms plus the transfer rows, so a loan
// that received a balance transfer shows a recast table and a loan that gave one
// away shows its unpaid installments void (Cancelled) with nothing outstanding.
func (srv *Server) loadLoanScheduleDetail(ctx context.Context, userID, loanAccountID uuid.UUID) (models.LoanScheduleDetail, error) {
	detail := models.LoanScheduleDetail{
		Entries:   []models.LoanScheduleEntry{},
		Transfers: []models.LoanPrincipalTransfer{},
	}

	var sched models.LoanSchedule
	err := srv.db.QueryRow(ctx,
		`SELECT id, loan_account_id, principal, processing_fee, annual_rate_bps, tenure_months, start_date, disbursal_date, created_at, updated_at
		 FROM loan_schedules WHERE loan_account_id = $1 AND user_id = $2`,
		loanAccountID, userID,
	).Scan(&sched.ID, &sched.LoanAccountID, &sched.Principal, &sched.ProcessingFee, &sched.AnnualRateBps,
		&sched.TenureMonths, &sched.StartDate, &sched.DisbursalDate, &sched.CreatedAt, &sched.UpdatedAt)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return detail, nil
	case err != nil:
		return detail, err
	}
	detail.Schedule = &sched

	transfers, err := srv.loadLoanTransfers(ctx, userID, loanAccountID)
	if err != nil {
		return detail, err
	}
	detail.Transfers = transfers

	terms := loanTerms{
		principal:     sched.Principal,
		processingFee: sched.ProcessingFee,
		annualRateBps: sched.AnnualRateBps,
		tenureMonths:  sched.TenureMonths,
		firstDueDate:  sched.StartDate,
		disbursalDate: sched.DisbursalDate,
	}
	// A transfer into this loan either recasts it from its date on or takes cash
	// out of its disbursement; a transfer out of it settled it. The unique
	// constraint on the source loan allows at most one of the latter, so the last
	// one read is the only one.
	var adjustments []principalAdjustment
	var paidOut money.Amount
	for _, t := range transfers {
		switch {
		case t.ToLoanAccountID != loanAccountID:
			settledOn := t.TransferDate
			detail.SettledOn = &settledOn
		case t.Mode == models.LoanTransferRecast:
			adjustments = append(adjustments, principalAdjustment{date: t.TransferDate, amount: t.Amount})
		case t.Mode == models.LoanTransferTakeover:
			// The target's own table stands: the amount is cash it paid out of
			// its disbursement to settle the source loan.
			paidOut += t.Amount
		}
		// LoanTransferOpens needs nothing here: the amount already is this
		// loan's principal.
	}

	// What the loan released, and the bank credit it is reconciled against.
	disbursement := &models.LoanDisbursement{
		Sanctioned:    sched.Principal,
		ProcessingFee: sched.ProcessingFee,
		PaidOut:       paidOut,
	}
	disbursement.Net = disbursement.Sanctioned - disbursement.ProcessingFee - disbursement.PaidOut
	if err := srv.loadLoanDisbursementCredit(ctx, userID, loanAccountID, disbursement); err != nil {
		return detail, err
	}
	detail.Disbursement = disbursement

	emi, entries := loanAmortization(terms, adjustments)
	detail.EMI = emi
	detail.Entries = entries

	payments, err := srv.loadLoanPayments(ctx, userID, loanAccountID)
	if err != nil {
		return detail, err
	}
	for _, p := range payments {
		if p.credit {
			// A refund against an EMI reduces what was actually paid but does
			// not cover another installment.
			detail.PaidAmount -= p.amount
			continue
		}
		detail.PaidAmount += p.amount
		next := nextUnpaidEntry(detail.Entries)
		if next == nil {
			// Every installment is already covered: the payment is still money
			// paid against the loan, but it settles no further one.
			continue
		}
		txnID := p.id
		next.Paid = true
		next.TransactionID = &txnID
		// Interest is cleared when an installment is paid, not when it falls
		// due, so the last payment date is what a later settlement accrues from.
		paidOn := dateOnly(p.date)
		detail.LastPaidDate = &paidOn
	}

	// A transfer settles everything the borrower had not yet repaid, so the
	// installments it covered are void: they are excluded from the totals, the
	// progress figures, and the next due date.
	for i := range detail.Entries {
		entry := &detail.Entries[i]
		entry.Cancelled = detail.SettledOn != nil && !entry.Paid
		if entry.Cancelled {
			continue
		}
		if entry.Paid {
			detail.PaidInstallments++
			detail.PrincipalPaid += entry.Principal
			detail.InterestPaid += entry.Interest
		}
		detail.TotalInterest += entry.Interest
		detail.TotalPayable += entry.Amount
	}

	// The outstanding figure is what the borrower still owes, so it starts from
	// the whole principal and counts transferred-in principal too: a recast loan
	// owes the balances it absorbed.
	outstanding := detail.Schedule.Principal
	for _, adj := range adjustments {
		outstanding += adj.amount
	}
	detail.OutstandingPrincipal = outstanding - detail.PrincipalPaid
	if detail.SettledOn != nil || detail.OutstandingPrincipal < 0 {
		detail.OutstandingPrincipal = 0
	}

	owed := 0
	for _, e := range detail.Entries {
		if !e.Cancelled {
			owed++
		}
	}
	detail.Completed = detail.PaidInstallments >= owed
	if !detail.Completed {
		if next := nextUnpaidEntry(detail.Entries); next != nil {
			due := next.DueDate
			detail.NextDueDate = &due
		}
	}
	return detail, nil
}

// nextUnpaidEntry returns the first installment the borrower still owes: not
// covered by a payment and not voided by a balance transfer.
func nextUnpaidEntry(entries []models.LoanScheduleEntry) *models.LoanScheduleEntry {
	for i := range entries {
		if !entries[i].Cancelled && !entries[i].Paid {
			return &entries[i]
		}
	}
	return nil
}

// loadLoanTransfers reads every balance transfer the loan took part in, in the
// order they take effect (by date, then creation, so two same-day transfers keep
// a stable order), with both accounts' names attached for display.
func (srv *Server) loadLoanTransfers(ctx context.Context, userID, loanAccountID uuid.UUID) ([]models.LoanPrincipalTransfer, error) {
	rows, err := srv.db.Query(ctx,
		`SELECT t.id, t.from_loan_account_id, fa.name, t.to_loan_account_id, ta.name,
		        t.amount, t.principal, t.transfer_date, t.mode, t.created_at
		 FROM loan_transfers t
		 JOIN accounts fa ON fa.id = t.from_loan_account_id
		 JOIN accounts ta ON ta.id = t.to_loan_account_id
		 WHERE t.user_id = $2
		   AND (t.from_loan_account_id = $1 OR t.to_loan_account_id = $1)
		 ORDER BY t.transfer_date, t.created_at, t.id`,
		loanAccountID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	// Always a non-nil slice: the response renders the transfers as a list and
	// would have to null-check a missing one otherwise.
	out := []models.LoanPrincipalTransfer{}
	for rows.Next() {
		var t models.LoanPrincipalTransfer
		if err := rows.Scan(&t.ID, &t.FromLoanAccountID, &t.FromLoanAccountName,
			&t.ToLoanAccountID, &t.ToLoanAccountName, &t.Amount, &t.Principal,
			&t.TransferDate, &t.Mode, &t.CreatedAt); err != nil {
			return nil, err
		}
		// The interest is what the payoff added on top of the principal, so it is
		// derived rather than stored a second time.
		t.AccruedInterest = t.Amount - t.Principal
		out = append(out, t)
	}
	return out, rows.Err()
}

// loadLoanDisbursementCredit attaches the bank credit linked to the loan, if
// any, and reconciles it against the disbursement the schedule implies: a
// mismatch is what the verification is for, so it is reported rather than
// rejected.
func (srv *Server) loadLoanDisbursementCredit(ctx context.Context, userID, loanAccountID uuid.UUID, disbursement *models.LoanDisbursement) error {
	var creditID uuid.UUID
	var creditAmount money.Amount
	err := srv.db.QueryRow(ctx,
		`SELECT d.transaction_id, t.amount
		 FROM loan_disbursements d
		 JOIN transactions t ON t.id = d.transaction_id
		 WHERE d.loan_account_id = $1 AND d.user_id = $2`,
		loanAccountID, userID).Scan(&creditID, &creditAmount)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return nil
	case err != nil:
		return err
	}
	disbursement.CreditTransactionID = &creditID
	disbursement.CreditAmount = &creditAmount
	disbursement.Difference = creditAmount - disbursement.Net
	disbursement.Verified = creditAmount == disbursement.Net
	return nil
}

// loanPayment is one EMI transaction attached to a loan, in the order it counts
// toward the installments.
type loanPayment struct {
	id     uuid.UUID
	amount money.Amount
	date   time.Time
	credit bool
}

// loadLoanPayments reads the loan's attached EMI transactions, oldest first.
// created_at breaks a same-day tie so the numbering is stable, with the id last
// for the case created_at itself ties (every row of one bulk import shares a
// single now()).
func (srv *Server) loadLoanPayments(ctx context.Context, userID, loanAccountID uuid.UUID) ([]loanPayment, error) {
	rows, err := srv.db.Query(ctx,
		`SELECT t.id, t.amount, t.date, t.type
		 FROM loan_attachments la
		 JOIN transactions t ON t.id = la.transaction_id
		 WHERE la.loan_account_id = $1 AND la.user_id = $2
		 ORDER BY t.date, t.created_at, t.id`,
		loanAccountID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []loanPayment
	for rows.Next() {
		var p loanPayment
		var txnType string
		if err := rows.Scan(&p.id, &p.amount, &p.date, &txnType); err != nil {
			return nil, err
		}
		p.credit = txnType == "credit"
		out = append(out, p)
	}
	return out, rows.Err()
}

// loanTerms are the schedule terms an amortization table is generated from.
// ProcessingFee is what the lender charged and is recorded for reference only:
// it is never amortized, so the table repays Principal in full whatever the fee
// was. DisbursalDate (nil when unknown) is what makes a first period that is not
// a whole month a stub.
type loanTerms struct {
	principal     money.Amount
	processingFee money.Amount
	annualRateBps int
	tenureMonths  int
	firstDueDate  time.Time
	disbursalDate *time.Time
}

// principalAdjustment is principal moved into a loan on a date by a balance
// transfer. The installments due after it are recast over the larger balance.
type principalAdjustment struct {
	date   time.Time
	amount money.Amount
}

// loanAmortization generates the amortization table for a schedule: every
// installment split into principal and interest, with its remaining balance.
// Monthly due dates come from the shared anchored-month helper the recurring
// engine uses, so a schedule starting on the 31st clamps to month length instead
// of drifting.
//
// Two things make a loan's installments non-uniform:
//
//   - When the disbursal date makes the first period a broken month, that period
//     is charged actual-days/30 of a month's interest and the EMI is solved to
//     still clear the loan in the full tenure, so every installment stays level
//     (the first one's split simply leans to interest) instead of the difference
//     piling into the last installment.
//   - Each transfer in adjustments recasts the installments still due after it
//     over the larger balance, so the EMI changes from that installment on
//     (Recast marks those entries).
//
// The processing fee is not part of this: it is recorded on the schedule for
// reference and changes nothing here, so the table always repays the whole
// principal.
//
// The EMI is rounded up to the whole rupee, the way a lender quotes it, and the
// final installment clears whatever principal is left, so the table repays the
// loan exactly despite that rounding; once the surplus has retired the balance
// the remaining installments are zero rather than negative, because the surplus
// is never charged. The returned EMI is the one in force for the last segment,
// which is the installment the borrower pays next. The rate factor is a ratio,
// not money: it is the one place float64 legitimately appears, and every amount
// derived from it is rounded to the nearest minor unit before further
// arithmetic.
func loanAmortization(terms loanTerms, adjustments []principalAdjustment) (money.Amount, []models.LoanScheduleEntry) {
	remaining := terms.principal
	if terms.tenureMonths < 1 {
		return remaining, []models.LoanScheduleEntry{}
	}
	monthlyRate := float64(terms.annualRateBps) / 120000.0

	anchor := dateOnly(terms.firstDueDate)
	stubDays, stub := firstPeriodStub(terms.disbursalDate, anchor)

	emi := loanEMI(remaining, monthlyRate, terms.tenureMonths)
	if stub {
		// A broken first period belongs inside the EMI, not on top of it: the
		// level installment is solved so that tenureMonths installments still
		// clear the loan while the first one also carries those days of
		// interest. Keeping the whole-month EMI instead would leave the
		// difference to pile up into the final installment.
		emi = loanEMIWithStub(remaining, monthlyRate, terms.tenureMonths,
			brokenPeriodInterest(remaining, terms.annualRateBps, stubDays))
	}
	emi = rupeeCeil(emi)

	entries := make([]models.LoanScheduleEntry, 0, terms.tenureMonths)
	recast := false
	next := 0
	for i := 1; i <= terms.tenureMonths; i++ {
		due := addMonthsAnchored(anchor, i-1)

		// A transfer dated strictly before this due date is already owed by
		// then, so this and every later installment is recast over it. One
		// dated on a due date leaves that installment as the lender had it.
		applied := false
		for next < len(adjustments) && adjustments[next].date.Before(due) {
			remaining += adjustments[next].amount
			next++
			applied = true
		}
		if applied {
			recast = true
			emi = rupeeCeil(loanEMI(remaining, monthlyRate, terms.tenureMonths-i+1))
			if i == 1 && stub {
				// The recast covers the broken first period too, so it is solved
				// the same way the original table was.
				emi = rupeeCeil(loanEMIWithStub(remaining, monthlyRate, terms.tenureMonths,
					brokenPeriodInterest(remaining, terms.annualRateBps, stubDays)))
			}
		}

		var interest money.Amount
		if i == 1 && stub {
			interest = brokenPeriodInterest(remaining, terms.annualRateBps, stubDays)
		} else {
			interest = money.Amount(math.Round(float64(remaining) * monthlyRate))
		}

		var principalPart money.Amount
		switch {
		case i == terms.tenureMonths:
			// The final installment clears whatever principal is left — which
			// is nothing once the rounded-up EMI has already retired it.
			if remaining > 0 {
				principalPart = remaining
			}
		case emi > interest:
			principalPart = emi - interest
			// The EMI is rounded up to the whole rupee, so it eventually
			// exceeds what the loan still owes. Never let the principal part run
			// past the balance: that would drive the balance, the interest and
			// the installment negative, and a negative installment matched as
			// "paid" would subtract from the loan. The surplus is simply not
			// charged, so the table repays the loan by (at the latest) the last
			// installment.
			if principalPart > remaining {
				principalPart = remaining
			}
		default:
			// A broken first period can accrue more interest than a whole
			// month's installment covers. That installment then pays interest
			// only rather than negative principal; the final installment
			// absorbs the balance it leaves behind.
			principalPart = 0
		}
		remaining -= principalPart
		entries = append(entries, models.LoanScheduleEntry{
			Number:    i,
			DueDate:   due,
			Amount:    principalPart + interest,
			Principal: principalPart,
			Interest:  interest,
			Balance:   remaining,
			Recast:    recast,
		})
	}
	return emi, entries
}

// loanPayoffFor derives a loan's settlement quote: what it still owes in
// principal plus the interest accrued since its last EMI payment, prorated the
// way a broken period is. The transfer endpoint moves exactly this amount, so a
// quote and the transfer it precedes can never disagree.
func loanPayoffFor(detail models.LoanScheduleDetail, asOf time.Time) models.LoanPayoff {
	asOfDate := dateOnly(asOf)
	quote := models.LoanPayoff{
		AsOf:                 asOfDate,
		OutstandingPrincipal: detail.OutstandingPrincipal,
		FromDate:             payoffAnchor(detail, asOfDate),
	}
	if !quote.FromDate.Before(quote.AsOf) || detail.Schedule == nil {
		// Nothing has accrued: a settlement before the last payment date (or one
		// with no terms to prorate by) moves principal only.
		quote.Payoff = quote.OutstandingPrincipal
		return quote
	}
	quote.Days = int(quote.AsOf.Sub(quote.FromDate).Hours() / 24)
	quote.AccruedInterest = brokenPeriodInterest(quote.OutstandingPrincipal, detail.Schedule.AnnualRateBps, quote.Days)
	quote.Payoff = quote.OutstandingPrincipal + quote.AccruedInterest
	return quote
}

// payoffAnchor is the date settlement interest accrues from: the last EMI
// payment's date, or — before anything is paid — when the money was released.
// With no disbursal date and nothing paid, the first period is assumed to start
// the month before the first installment, the same assumption the table makes.
func payoffAnchor(detail models.LoanScheduleDetail, asOf time.Time) time.Time {
	if detail.LastPaidDate != nil {
		return dateOnly(*detail.LastPaidDate)
	}
	if detail.Schedule == nil {
		return asOf
	}
	if detail.Schedule.DisbursalDate != nil {
		return dateOnly(*detail.Schedule.DisbursalDate)
	}
	return addMonthsAnchored(dateOnly(detail.Schedule.StartDate), -1)
}

// firstPeriodStub reports the length in days of a loan's first period — the
// disbursal date to the first installment date — and whether that period is a
// broken month. A missing disbursal date, a period that is exactly one anchored
// month, and a nonsensical one (disbursal on or after the first due date) are all
// not stubs: those are the normal month the engine has always billed.
func firstPeriodStub(disbursal *time.Time, firstDue time.Time) (int, bool) {
	if disbursal == nil {
		return 0, false
	}
	d := dateOnly(*disbursal)
	if !d.Before(firstDue) || addMonthsAnchored(d, 1).Equal(firstDue) {
		return 0, false
	}
	return int(firstDue.Sub(d).Hours() / 24), true
}

// brokenPeriodInterest is the interest for a period that is not a whole month —
// a loan's broken first period, or the days between a settlement and the last
// EMI payment it follows. Lenders prorate it by the actual days over a 30-day
// month, which is the same as the annual rate over a 360-day year and is
// consistent with the monthly rate the rest of the table is built on.
func brokenPeriodInterest(balance money.Amount, annualRateBps, days int) money.Amount {
	monthly := float64(balance) * float64(annualRateBps) / 120000.0
	return money.Amount(math.Round(monthly * float64(days) / 30.0))
}

// loanEMIWithStub returns the level installment that clears balance over months
// installments when the first of them also carries firstInterest: the present
// value of the level payments must cover the balance plus that interest.
func loanEMIWithStub(balance money.Amount, monthlyRate float64, months int, firstInterest money.Amount) money.Amount {
	if months <= 1 {
		return balance + firstInterest
	}
	factor := 1 + annuityFactor(monthlyRate, months-1)
	if factor <= 0 {
		return balance + firstInterest
	}
	return money.Amount(math.Round(float64(balance+firstInterest) / factor))
}

// annuityFactor is the present value of n monthly payments of one unit, used to
// solve an installment from a balance.
func annuityFactor(monthlyRate float64, months int) float64 {
	switch {
	case months <= 0:
		return 0
	case monthlyRate <= 0:
		return float64(months)
	}
	return (1 - math.Pow(1+monthlyRate, -float64(months))) / monthlyRate
}

// rupeeCeil rounds an installment up to the whole rupee. Lenders quote a
// whole-rupee EMI — the exact annuity figure is an intermediate — so the quoted
// installment is what the borrower pays, and the surplus it carries leaves the
// final installment clearing the remainder.
func rupeeCeil(a money.Amount) money.Amount {
	const rupee = 100
	if a <= 0 {
		return a
	}
	return (a + rupee - 1) / rupee * rupee
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
