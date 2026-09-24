package handlers

import (
	"context"
	"errors"
	"log/slog"
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

// cycleQueryer is the minimal query surface shared by *pgxpool.Pool (via
// db.DBPool) and pgx.Tx so the billing-cycle readers can run against either.
// The regeneration below is not a reader: it takes a pgx.Tx, so a caller cannot
// run its multi-statement detach-and-recreate without a transaction.
type cycleQueryer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// GetBillingCycles lists the billing cycles for an account, auto-generating
// any missing cycles first. Accounts without a billing day return an empty
// list. Each cycle carries its total outstanding — the account's running
// balance at the cycle end date (all debits minus all credits — purchases,
// payments, refunds, cashbacks — posted up to that date) — and its
// transaction count.
func (srv *Server) GetBillingCycles(c *gin.Context) {
	userID := auth.GetUserID(c)
	accountID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		validation.RespondError(c, "invalid id", http.StatusBadRequest)
		return
	}

	// The account must exist and belong to the authenticated user.
	var billingDay *int
	err = srv.db.QueryRow(c,
		`SELECT a.billing_day
		 FROM accounts a WHERE a.id = $1 AND a.user_id = $2`,
		accountID, userID).Scan(&billingDay)
	if errors.Is(err, pgx.ErrNoRows) {
		validation.RespondError(c, "account not found", http.StatusNotFound)
		return
	}
	if err != nil {
		slog.Error("GetBillingCycles (account lookup)", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	if billingDay == nil {
		c.JSON(http.StatusOK, gin.H{"data": []models.BillingCycle{}})
		return
	}

	// The regeneration detaches and recreates the account's cycles, so it runs
	// in one transaction: a cancellation or failure between the detach and the
	// delete would otherwise leave the account's transactions detached from
	// cycles that still exist.
	if err := db.WithTx(c, srv.db, func(tx pgx.Tx) error {
		return ensureBillingCycles(c, tx, userID, accountID, *billingDay)
	}); err != nil {
		slog.Error("GetBillingCycles (ensure cycles)", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	cycles, err := listBillingCycles(c, srv.db, userID, accountID)
	if err != nil {
		slog.Error("GetBillingCycles (list cycles)", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": cycles})
}

// ensureBillingCycles creates any missing billing cycles for an account that
// has a billing day set (one per month, ending on the account's billing day)
// and then back-fills the suggested default: every unassigned transaction is
// attached to the cycle whose date range contains its transaction date. Gaps
// are filled in BOTH directions: months after the newest cycle (new activity)
// and months before the oldest cycle (backdated/late imports). It is
// idempotent and safe to call on every request. If existing cycles no longer
// end on the billing day (e.g. the day was changed), they are dropped first so
// they can be regenerated.
//
// tx is the caller's transaction, which is what makes the regeneration atomic:
// the drop path detaches transactions from their cycles before deleting and
// recreating them, so an interrupted run without a transaction would leave the
// account's cycles in place with every transaction detached from them (and any
// manually assigned cycle replaced by the date-based default on the next read).
func ensureBillingCycles(ctx context.Context, tx pgx.Tx, userID, accountID uuid.UUID, billingDay int) error {
	// Billing days out of range fall back to the 1st of the month.
	if billingDay <= 0 || billingDay > 31 {
		billingDay = 1
	}

	// If the billing day changed, drop the stale cycles so they can be
	// regenerated on the new day.
	if err := dropMisalignedCycles(ctx, tx, userID, accountID, billingDay); err != nil {
		return err
	}

	// Earliest transaction date for the account, falling back to today when the
	// account has no transactions yet. COALESCE avoids a NULL scan for an empty
	// account (MIN returns a single NULL row, not ErrNoRows).
	var firstDate time.Time
	err := tx.QueryRow(ctx,
		"SELECT COALESCE(MIN(date), CURRENT_DATE) FROM transactions WHERE account_id = $1 AND user_id = $2",
		accountID, userID).Scan(&firstDate)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	firstDate = dateOnly(firstDate)

	// Months that already have an existing cycle, keyed by the month the cycle
	// ends in. Missing months are generated below — including months OLDER than
	// the first existing cycle, so late or backdated imports can still be
	// attached to the cycle matching their transaction date.
	coveredMonths := map[time.Time]bool{}
	rows, err := tx.Query(ctx,
		"SELECT end_date FROM billing_cycles WHERE account_id = $1 AND user_id = $2",
		accountID, userID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var end time.Time
		if err := rows.Scan(&end); err != nil {
			rows.Close()
			return err
		}
		coveredMonths[time.Date(end.Year(), end.Month(), 1, 0, 0, 0, 0, time.UTC)] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	today := dateOnly(time.Now())
	months := billingCycleMonths(firstDate, today, billingDay)

	// If every desired month already has a cycle we can skip the per-month
	// INSERT loop entirely — the common case on every read.
	allCovered := true
	for _, ms := range months {
		if !coveredMonths[ms] {
			allCovered = false
			break
		}
	}

	// The back-fill UPDATE scans the account's transactions, so only run it when
	// an unassigned transaction actually exists. Combined with allCovered this
	// keeps the steady-state read path to a handful of index lookups instead of
	// a full scan plus one INSERT per month. A row the user detached on purpose
	// is not unassigned: it is explicitly excluded, so it cannot keep this
	// branch alive on its own either.
	var hasUnassigned bool
	if err := tx.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM transactions WHERE account_id = $1 AND user_id = $2
		   AND billing_cycle_id IS NULL AND NOT billing_cycle_detached)`,
		accountID, userID).Scan(&hasUnassigned); err != nil {
		return err
	}

	if allCovered && !hasUnassigned {
		return nil
	}

	for _, ms := range months {
		// Skip months that already have a cycle.
		if coveredMonths[ms] {
			continue
		}
		start, end := cycleDates(ms, billingDay)
		if _, err := tx.Exec(ctx,
			`INSERT INTO billing_cycles (account_id, user_id, start_date, end_date, label)
			 VALUES ($1, $2, $3, $4, $5)
			 ON CONFLICT (user_id, account_id, start_date) DO NOTHING`,
			accountID, userID, start, end, end.Format("Jan 2006")); err != nil {
			return err
		}
	}

	if !hasUnassigned {
		return nil
	}

	// Suggested default: attach every unassigned transaction to the cycle whose
	// date range contains its transaction date. The cycle is scoped to the
	// transaction's OWN account and user, so an import can never land on
	// another account's cycle (which would corrupt that account's totals).
	//
	// A row the user detached by hand (billing_cycle_detached) is skipped: NULL
	// alone cannot tell "never assigned" from "the user chose Unassigned", and
	// silently re-attaching would undo the correction the user just made.
	_, err = tx.Exec(ctx,
		`UPDATE transactions t SET billing_cycle_id = bc.id
		 FROM billing_cycles bc
		 WHERE t.account_id = $1 AND t.user_id = $2
		   AND bc.account_id = t.account_id AND bc.user_id = t.user_id
		   AND t.billing_cycle_id IS NULL
		   AND NOT t.billing_cycle_detached
		   AND t.date >= bc.start_date AND t.date <= bc.end_date`,
		accountID, userID)
	return err
}

// dropMisalignedCycles deletes any billing cycles whose end date no longer
// matches the account's billing day (e.g. the day was changed), detaching their
// transactions first so ensureBillingCycles can regenerate the cycles on the
// new day. It is a no-op when all existing cycles are aligned, and it runs in
// the caller's transaction so the detach cannot outlive a failed delete.
func dropMisalignedCycles(ctx context.Context, tx pgx.Tx, userID, accountID uuid.UUID, billingDay int) error {
	rows, err := tx.Query(ctx,
		`SELECT end_date FROM billing_cycles WHERE account_id = $1 AND user_id = $2`,
		accountID, userID)
	if err != nil {
		return err
	}
	misaligned := false
	for rows.Next() {
		var end time.Time
		if err := rows.Scan(&end); err != nil {
			rows.Close()
			return err
		}
		if !dateOnly(end).Equal(billingDateInMonth(dateOnly(end), billingDay)) {
			misaligned = true
			break
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if !misaligned {
		return nil
	}

	// Detach the transactions so the date-based default can re-attach them to
	// the regenerated cycles, then drop the stale cycles. The detach flag is
	// cleared too: the cycles the user was detaching from no longer exist, so
	// the re-derived assignment on the new billing day is the only one left.
	if _, err := tx.Exec(ctx,
		`UPDATE transactions SET billing_cycle_id = NULL, billing_cycle_detached = FALSE
		 WHERE user_id = $1 AND billing_cycle_id IN (SELECT id FROM billing_cycles WHERE account_id = $2 AND user_id = $1)`,
		userID, accountID); err != nil {
		return err
	}
	_, err = tx.Exec(ctx,
		`DELETE FROM billing_cycles WHERE account_id = $1 AND user_id = $2`,
		accountID, userID)
	return err
}

// maxBillingCycleMonths bounds how many cycles a single call may generate. It
// is a backstop, not the rule: the write edges reject a transaction date outside
// [1900-01-01, today+1y] (validation.CheckTransactionDate), which is what keeps
// the span sane. Rows written before that rule existed — or by a future caller
// that bypasses the handler — must still not be able to turn one read into tens
// of thousands of INSERTs, so the generator admits at most 50 years of months
// and keeps the newest ones (the oldest are where a bad year lands, and their
// transactions simply stay unassigned instead of wedging the account).
const maxBillingCycleMonths = 600

// billingCycleMonths returns the months for which billing cycles should exist:
// starting one month before the earliest transaction (so the first partial
// cycle is covered) and ending with the cycle that contains today. The span is
// clamped to maxBillingCycleMonths, counted back from the last month, so the
// number of cycles one call can create is bounded regardless of the stored data.
func billingCycleMonths(earliest, today time.Time, billingDay int) []time.Time {
	if billingDay <= 0 {
		billingDay = 1
	}
	first := time.Date(earliest.Year(), earliest.Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, -1, 0)
	last := time.Date(today.Year(), today.Month(), 1, 0, 0, 0, 0, time.UTC)
	if today.After(billingDateInMonth(last, billingDay)) {
		last = last.AddDate(0, 1, 0)
	}
	if floor := last.AddDate(0, -(maxBillingCycleMonths - 1), 0); first.Before(floor) {
		first = floor
	}
	months := []time.Time{}
	for ms := first; !ms.After(last); ms = ms.AddDate(0, 1, 0) {
		months = append(months, ms)
	}
	return months
}

// cycleDates returns the start and end dates of the billing cycle ending in the
// month containing ms, given the billing day. The cycle runs from the day after
// the previous billing date through the current billing date.
func cycleDates(ms time.Time, billingDay int) (time.Time, time.Time) {
	end := billingDateInMonth(ms, billingDay)
	start := billingDateInMonth(ms.AddDate(0, -1, 0), billingDay).AddDate(0, 0, 1)
	return start, end
}

// listBillingCycles returns the account's billing cycles ordered by start date.
// Each row carries the net activity of its attached transactions (debits minus
// credits) and the transaction count; TotalOutstanding is the running
// debit-minus-credit total through each cycle end. It is not necessarily the
// same sign/value as GET /accounts, whose balance sign follows the account
// type's positiveTxnType.
func listBillingCycles(ctx context.Context, q cycleQueryer, userID, accountID uuid.UUID) ([]models.BillingCycle, error) {
	rows, err := q.Query(ctx,
		`SELECT bc.id, bc.start_date, bc.end_date, bc.label,
		        COALESCE(SUM(CASE WHEN t.type = 'debit' THEN t.amount WHEN t.type = 'credit' THEN -t.amount ELSE 0 END), 0) AS net_activity,
		        COUNT(t.id) AS txn_count
		 FROM billing_cycles bc
		 LEFT JOIN transactions t ON t.billing_cycle_id = bc.id
		 WHERE bc.account_id = $1 AND bc.user_id = $2
		 GROUP BY bc.id, bc.start_date, bc.end_date, bc.label
		 ORDER BY bc.start_date ASC`,
		accountID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	cycles := []models.BillingCycle{}
	runningBalance := money.Amount(0)
	for rows.Next() {
		var bc models.BillingCycle
		var net money.Amount
		if err := rows.Scan(&bc.ID, &bc.StartDate, &bc.EndDate, &bc.Label, &net, &bc.TransactionCount); err != nil {
			return nil, err
		}
		// Every row selected here is filtered by `bc.account_id = $1`, so the
		// cycle belongs to the queried account: assigning it directly keeps the
		// contract's accountId populated (models.BillingCycle.AccountID would
		// otherwise be the nil UUID for every cycle) without selecting a column
		// that can only ever repeat the argument.
		bc.AccountID = accountID
		runningBalance += net
		bc.TotalOutstanding = runningBalance
		cycles = append(cycles, bc)
	}
	return cycles, rows.Err()
}
