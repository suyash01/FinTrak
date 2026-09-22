package handlers

import (
	"fmt"
	"log/slog"
	"sort"
	"time"

	"github.com/fintrak/backend/db"
	"github.com/fintrak/backend/internal/money"
	"github.com/fintrak/backend/models"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// summaryNamespace seeds the deterministic UUIDs used for synthetic summary rows
// so React keys stay stable across requests.
var summaryNamespace = uuid.MustParse("00000000-0000-0000-0000-00000000f1a7")

// buildAccountSummaryRows looks up the filtered account and returns the
// synthetic summary rows to display. Accounts with a billing day set get
// per-cycle "Total outstanding" rows (regardless of account type); accounts
// without one get month-end "Running balance" rows instead. Both sets are
// synthetic, never persisted, and only meaningful in a date-ordered list.
func (srv *Server) buildAccountSummaryRows(c *gin.Context, userID, accountID uuid.UUID, dateFrom, dateTo string) ([]models.Transaction, []models.Transaction) {
	var acctName string
	var billingDay *int
	err := srv.db.QueryRow(c,
		`SELECT a.name, a.billing_day
		 FROM accounts a
		 WHERE a.id = $1 AND a.user_id = $2`,
		accountID, userID).Scan(&acctName, &billingDay)
	if err != nil {
		slog.Error("buildAccountSummaryRows (account lookup)", slog.String("error", err.Error()))
		return nil, nil
	}
	if billingDay == nil {
		return nil, srv.computeMonthEndBalanceRows(c, userID, accountID, acctName, dateFrom, dateTo)
	}

	// The regeneration detaches and recreates the account's cycles, so it runs
	// in one transaction: an interruption between the detach and the delete
	// would otherwise leave its transactions detached from cycles that still
	// exist.
	if err := db.WithTx(c, srv.db, func(tx pgx.Tx) error {
		return ensureBillingCycles(c, tx, userID, accountID, *billingDay)
	}); err != nil {
		slog.Error("buildAccountSummaryRows (ensure billing cycles)", slog.String("error", err.Error()))
		return nil, nil
	}
	return srv.computeSummaryRows(c, userID, accountID, acctName, dateFrom, dateTo), nil
}

// computeMonthEndBalanceRows builds the synthetic "Running balance" rows for an
// account without a billing day: the account's ledger balance (credits minus
// debits) at the end of every calendar month that has transactions — the same
// shape as the "Total outstanding" rows billing-day accounts get at cycle
// ends. The balance is the account's FULL running balance up to each month
// end, independent of any active view filter, so the same numbers appear on
// every page and filter combination. The month containing the range end is
// still in progress, so it gets its row at the range end carrying the balance
// as of that date (mirroring the in-progress-cycle row).
func (srv *Server) computeMonthEndBalanceRows(c *gin.Context, userID, accountID uuid.UUID, acctName, dateFrom, dateTo string) []models.Transaction {
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

	// Per-month net activity over the account's full ledger, oldest month
	// first. The running sum of these nets is the balance at each month end.
	rows, err := srv.db.Query(c,
		`SELECT date_trunc('month', t.date)::date,
		        COALESCE(SUM(CASE WHEN t.type = 'credit' THEN t.amount WHEN t.type = 'debit' THEN -t.amount ELSE 0 END), 0),
		        COUNT(t.id)
		 FROM transactions t
		 WHERE t.account_id = $1 AND t.user_id = $2
		 GROUP BY 1 ORDER BY 1`,
		accountID, userID)
	if err != nil {
		slog.Error("computeMonthEndBalanceRows (monthly net)", slog.String("error", err.Error()))
		return nil
	}
	type monthNet struct {
		month time.Time // first day of the month
		net   money.Amount
		count int
	}
	nets := []monthNet{}
	for rows.Next() {
		var m time.Time
		var net money.Amount
		var count int
		if err := rows.Scan(&m, &net, &count); err != nil {
			rows.Close()
			slog.Error("computeMonthEndBalanceRows (scan)", slog.String("error", err.Error()))
			return nil
		}
		nets = append(nets, monthNet{month: m, net: net, count: count})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		slog.Error("computeMonthEndBalanceRows (iterate)", slog.String("error", err.Error()))
		return nil
	}
	if len(nets) == 0 {
		return nil
	}

	buildRow := func(date time.Time, balance money.Amount) models.Transaction {
		return models.Transaction{
			ID:          summaryID(accountID.String(), "running-balance", date),
			AccountID:   accountID,
			Date:        date,
			Description: "Running balance",
			Amount:      balance,
			Type:        "credit",
			AccountName: acctName,
			IsSummary:   true,
		}
	}

	currentMonthEnd := time.Date(to.Year(), to.Month()+1, 0, 0, 0, 0, 0, time.UTC)
	inProgress := currentMonthEnd.After(to)

	out := []models.Transaction{}
	balance := money.Amount(0)
	for _, n := range nets {
		balance += n.net
		if n.count == 0 {
			continue
		}
		end := time.Date(n.month.Year(), n.month.Month()+1, 0, 0, 0, 0, 0, time.UTC)
		// The in-progress month (the one containing the range end, whose end
		// date is beyond the range): the row sits at the range end with the
		// balance as of that date instead of the projected month-end balance.
		if inProgress && end.Equal(currentMonthEnd) {
			var total money.Amount
			var count int
			err := srv.db.QueryRow(c,
				`SELECT COALESCE(SUM(CASE WHEN t.type = 'credit' THEN t.amount WHEN t.type = 'debit' THEN -t.amount ELSE 0 END), 0), COUNT(t.id)
				 FROM transactions t WHERE t.account_id = $1 AND t.user_id = $2 AND t.date <= $3`,
				accountID, userID, to).Scan(&total, &count)
			if err != nil {
				slog.Error("computeMonthEndBalanceRows (in-progress month)", slog.String("error", err.Error()))
				return nil
			}
			if count > 0 {
				out = append(out, buildRow(to, total))
			}
			continue
		}
		// Only rows whose month end falls inside the viewed range.
		if (!from.IsZero() && end.Before(from)) || end.After(to) {
			continue
		}
		out = append(out, buildRow(end, balance))
	}
	return out
}

// computeSummaryRows builds the synthetic "Total outstanding" rows
// for an account (any type with a billing day set) from its explicit billing
// cycles. Each cycle that has attached transactions gets a row at its end date
// (the running balance through that date), and the in-progress cycle
// containing the end of the range gets a row at the range end. Cycles are
// expected to already exist (callers run ensureBillingCycles first).
func (srv *Server) computeSummaryRows(c *gin.Context, userID, accountID uuid.UUID, acctName, dateFrom, dateTo string) []models.Transaction {
	cycles, err := listBillingCycles(c, srv.db, userID, accountID)
	if err != nil {
		slog.Error("computeSummaryRows (list cycles)", slog.String("error", err.Error()))
		return nil
	}
	return srv.summaryRowsFromCycles(c, userID, accountID, acctName, dateFrom, dateTo, cycles)
}

// summaryRowsFromCycles builds the synthetic "Total outstanding" rows from an
// already-loaded cycle list, so callers that need the cycles themselves
// (e.g. the cash-flow calendar overlay) can avoid a second query.
func (srv *Server) summaryRowsFromCycles(c *gin.Context, userID, accountID uuid.UUID, acctName, dateFrom, dateTo string, cycles []models.BillingCycle) []models.Transaction {
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

	buildRow := func(kind, description string, date time.Time, amount money.Amount, billingCycleID *uuid.UUID) models.Transaction {
		return models.Transaction{
			ID:             summaryID(accountID.String(), kind, date),
			AccountID:      accountID,
			Date:           date,
			Description:    description,
			Amount:         amount,
			Type:           "credit",
			AccountName:    acctName,
			IsSummary:      true,
			BillingCycleID: billingCycleID,
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
			rows = append(rows, buildRow("outstanding", "Total outstanding", end, bc.TotalOutstanding, &bc.ID))
		}
		// Track the in-progress cycle: the one whose date range contains `to`.
		if !dateOnly(bc.StartDate).After(to) && !end.Before(to) {
			current = bc
		}
	}

	// The in-progress cycle (its end date is beyond the range end) gets a row at
	// the range end with the running balance up to that date: every debit minus
	// every credit (payments, refunds, cashbacks) posted to the account so far.
	if current != nil && dateOnly(current.EndDate).After(to) {
		var total money.Amount
		var count int
		err := srv.db.QueryRow(c,
			`SELECT COALESCE(SUM(CASE WHEN t.type = 'debit' THEN t.amount WHEN t.type = 'credit' THEN -t.amount ELSE 0 END), 0), COUNT(t.id)
			 FROM transactions t WHERE t.account_id = $1 AND t.user_id = $2 AND t.date <= $3`,
			accountID, userID, to).Scan(&total, &count)
		if err != nil {
			slog.Error("computeSummaryRows (current cycle)", slog.String("error", err.Error()))
			return nil
		}
		if count > 0 {
			rows = append(rows, buildRow("outstanding-current", "Total outstanding", to, total, &current.ID))
		}
	}

	return rows
}

// mergeSummaryRows interleaves synthetic summary rows into the already-sorted
// transaction list by date so they appear in the correct chronological position.
// Summary rows only make sense in a date-ordered list; when sorting by another
// column they are hidden entirely.
func mergeSummaryRows(transactions []models.Transaction, rows []models.Transaction, sortBy, sortOrder string) []models.Transaction {
	if len(rows) == 0 || sortBy != "date" {
		return transactions
	}
	// Only include the summary rows whose cycle window overlaps the transactions
	// on the current page, so rows are spread across pages instead of repeating
	// on every page.
	rows = filterSummaryRowsForPage(transactions, rows)
	if len(rows) == 0 {
		return transactions
	}
	return mergeByDate(transactions, rows, sortOrder)
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

// mergeByDate interleaves summary rows into the sorted transaction list by
// date, grouping each page's transactions by billing cycle so a "Total
// outstanding" row stays adjacent to its own cycle's transactions: after them
// in ascending order, before them in descending. Cycle transactions always
// fall on or before their cycle's row date, so the grouped output remains
// date-ordered. Transactions not attached to a cycle that has a kept row are
// preserved too (before the rows when descending, after them when ascending)
// so no transaction is ever dropped.
func mergeByDate(transactions []models.Transaction, rows []models.Transaction, sortOrder string) []models.Transaction {
	asc := sortOrder == "ASC"
	sort.Slice(rows, func(i, j int) bool {
		if asc {
			return rows[i].Date.Before(rows[j].Date)
		}
		return rows[i].Date.After(rows[j].Date)
	})

	// Group the page's transactions by the cycle whose row they belong to;
	// transactions with no matching kept cycle are held aside and re-added
	// below so they are never dropped.
	grouped := make(map[uuid.UUID][]models.Transaction, len(rows))
	rowCycles := make(map[uuid.UUID]bool, len(rows))
	for _, r := range rows {
		rowCycles[*r.BillingCycleID] = true
	}
	var ungrouped []models.Transaction
	for _, t := range transactions {
		if t.BillingCycleID != nil && rowCycles[*t.BillingCycleID] {
			grouped[*t.BillingCycleID] = append(grouped[*t.BillingCycleID], t)
		} else {
			ungrouped = append(ungrouped, t)
		}
	}

	merged := make([]models.Transaction, 0, len(transactions)+len(rows))
	if asc {
		for _, r := range rows {
			merged = append(merged, grouped[*r.BillingCycleID]...)
			merged = append(merged, r)
		}
		merged = append(merged, ungrouped...)
	} else {
		merged = append(merged, ungrouped...)
		for _, r := range rows {
			merged = append(merged, r)
			merged = append(merged, grouped[*r.BillingCycleID]...)
		}
	}

	return merged
}

// mergeMonthEndRows interleaves the month-end "Running balance" rows into the
// date-ordered transaction list. Unlike mergeByDate (which groups transactions
// by billing cycle), month-end rows carry no cycle, so placement is purely
// date-based: in ascending order a row lands right after its month's last
// transaction, in descending order right before it — the balance sits at the
// month's end either way. A transaction dated on the month's final day ties
// with the row: the row comes after it in ascending order and before it in
// descending order.
func mergeMonthEndRows(transactions []models.Transaction, rows []models.Transaction, sortOrder string) []models.Transaction {
	if len(rows) == 0 {
		return transactions
	}
	// Only keep the rows whose month window overlaps the transactions on the
	// current page, so rows are spread across pages instead of repeating.
	rows = filterSummaryRowsForPage(transactions, rows)
	if len(rows) == 0 {
		return transactions
	}

	asc := sortOrder == "ASC"
	sort.Slice(rows, func(i, j int) bool {
		if asc {
			return rows[i].Date.Before(rows[j].Date)
		}
		return rows[i].Date.After(rows[j].Date)
	})

	merged := make([]models.Transaction, 0, len(transactions)+len(rows))
	i, j := 0, 0
	for i < len(transactions) && j < len(rows) {
		td, rd := dateOnly(transactions[i].Date), dateOnly(rows[j].Date)
		if asc {
			// A same-day tie puts the transaction first so the month-end row
			// still follows it (the descending branch below already puts the
			// row first, which is the same order reversed).
			if !td.After(rd) {
				merged = append(merged, transactions[i])
				i++
			} else {
				merged = append(merged, rows[j])
				j++
			}
		} else {
			if td.After(rd) {
				merged = append(merged, transactions[i])
				i++
			} else {
				merged = append(merged, rows[j])
				j++
			}
		}
	}
	merged = append(merged, transactions[i:]...)
	merged = append(merged, rows[j:]...)
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

// billingDateInMonth returns the billing day (clamped to the month length)
// within the month containing t.
func billingDateInMonth(t time.Time, day int) time.Time {
	y, m := t.Year(), t.Month()
	if day > daysInMonth(y, m) {
		day = daysInMonth(y, m)
	}
	return time.Date(y, m, day, 0, 0, 0, 0, time.UTC)
}
