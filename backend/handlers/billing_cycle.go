package handlers

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"net/http"
	"time"

	"github.com/fintrak/backend/auth"
	"github.com/fintrak/backend/db"
	"github.com/fintrak/backend/internal/validation"
	"github.com/fintrak/backend/models"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// cycleQueryer is the minimal query surface shared by *pgxpool.Pool (via
// db.DBPool) and pgx.Tx so the billing-cycle helpers can run against either.
type cycleQueryer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// GetBillingCycles lists the billing cycles for a credit-card account,
// auto-generating any missing cycles first. Non-credit-card accounts return an
// empty list. Each cycle carries its total outstanding (sum of attached debit
// transactions) and transaction count.
func GetBillingCycles(c *gin.Context) {
	userID := auth.GetUserID(c)
	accountID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		validation.RespondError(c, "invalid id", http.StatusBadRequest)
		return
	}

	// The account must exist and belong to the authenticated user.
	var acctTypeID string
	err = db.Pool.QueryRow(c,
		`SELECT a.account_type_id
		 FROM accounts a WHERE a.id = $1 AND a.user_id = $2`,
		accountID, userID).Scan(&acctTypeID)
	if errors.Is(err, pgx.ErrNoRows) {
		validation.RespondError(c, "account not found", http.StatusNotFound)
		return
	}
	if err != nil {
		log.Printf("Error in GetBillingCycles (account lookup): %v\n", err)
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	if acctTypeID != "credit_card" {
		c.JSON(http.StatusOK, gin.H{"data": []models.BillingCycle{}})
		return
	}

	if err := ensureBillingCycles(c, db.Pool, userID, accountID); err != nil {
		log.Printf("Error in GetBillingCycles (ensure cycles): %v\n", err)
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	cycles, err := listBillingCycles(c, db.Pool, userID, accountID)
	if err != nil {
		log.Printf("Error in GetBillingCycles (list cycles): %v\n", err)
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": cycles})
}

// ensureBillingCycles creates any missing billing cycles for a credit-card
// account (one per month, ending on the 1st) and then back-fills the suggested
// default: every unassigned transaction is attached to the cycle whose date
// range contains its transaction date. It is idempotent and safe to call on
// every request.
func ensureBillingCycles(ctx context.Context, q cycleQueryer, userID, accountID uuid.UUID) error {
	// Cycles always end on the 1st of each month (the billing day is no longer
	// configurable per account).
	billingDay := 1

	// Earliest transaction date for the account (fall back to today).
	var earliest time.Time
	err := q.QueryRow(ctx,
		"SELECT MIN(date) FROM transactions WHERE account_id = $1 AND user_id = $2",
		accountID, userID).Scan(&earliest)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if earliest.IsZero() {
		earliest = dateOnly(time.Now())
	}

	// Latest existing cycle end date (invalid when no cycles exist yet).
	var latestEnd sql.NullTime
	err = q.QueryRow(ctx,
		"SELECT MAX(end_date) FROM billing_cycles WHERE account_id = $1 AND user_id = $2",
		accountID, userID).Scan(&latestEnd)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}

	today := dateOnly(time.Now())
	for _, ms := range billingCycleMonths(earliest, today, billingDay) {
		// Skip months already covered by an existing cycle.
		if latestEnd.Valid && !ms.After(time.Date(latestEnd.Time.Year(), latestEnd.Time.Month(), 1, 0, 0, 0, 0, time.UTC)) {
			continue
		}
		start, end := cycleDates(ms, billingDay)
		if _, err := q.Exec(ctx,
			`INSERT INTO billing_cycles (account_id, user_id, start_date, end_date, label)
			 VALUES ($1, $2, $3, $4, $5)
			 ON CONFLICT (account_id, start_date) DO NOTHING`,
			accountID, userID, start, end, end.Format("Jan 2006")); err != nil {
			return err
		}
	}

	// Suggested default: attach every unassigned transaction to the cycle whose
	// date range contains its transaction date.
	_, err = q.Exec(ctx,
		`UPDATE transactions t SET billing_cycle_id = bc.id
		 FROM billing_cycles bc
		 WHERE t.account_id = $1 AND t.user_id = $2
		   AND t.billing_cycle_id IS NULL
		   AND t.date >= bc.start_date AND t.date <= bc.end_date`,
		accountID, userID)
	return err
}

// billingCycleMonths returns the months for which billing cycles should exist:
// starting one month before the earliest transaction (so the first partial
// cycle is covered) and ending with the cycle that contains today.
func billingCycleMonths(earliest, today time.Time, billingDay int) []time.Time {
	if billingDay <= 0 {
		billingDay = 1
	}
	first := time.Date(earliest.Year(), earliest.Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, -1, 0)
	last := time.Date(today.Year(), today.Month(), 1, 0, 0, 0, 0, time.UTC)
	if today.After(billingDateInMonth(last, billingDay)) {
		last = last.AddDate(0, 1, 0)
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

// listBillingCycles returns the account's billing cycles ordered by start date,
// each with the sum of its attached debit transactions and its transaction
// count.
func listBillingCycles(ctx context.Context, q cycleQueryer, userID, accountID uuid.UUID) ([]models.BillingCycle, error) {
	rows, err := q.Query(ctx,
		`SELECT bc.id, bc.start_date, bc.end_date, bc.label,
		        COALESCE(SUM(CASE WHEN t.type = 'debit' THEN t.amount ELSE 0 END), 0) AS total_outstanding,
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
	for rows.Next() {
		var bc models.BillingCycle
		if err := rows.Scan(&bc.ID, &bc.StartDate, &bc.EndDate, &bc.Label, &bc.TotalOutstanding, &bc.TransactionCount); err != nil {
			return nil, err
		}
		cycles = append(cycles, bc)
	}
	return cycles, rows.Err()
}
