package validation

import (
	"fmt"
	"time"
)

// TransactionDateMin is the earliest date a transaction may carry. It bounds
// billing-cycle generation: an account gets one cycle per month between its
// earliest transaction and today, so a single transaction carrying a typo'd or
// OCR'd year (0001-01-01, 1024-01-01) would otherwise make every read of that
// account emit tens of thousands of INSERTs before it can answer.
const TransactionDateMin = "1900-01-01"

// transactionDateMinValue is TransactionDateMin parsed once.
var transactionDateMinValue = time.Date(1900, 1, 1, 0, 0, 0, 0, time.UTC)

// TransactionDateMax returns the latest date a transaction may carry: one year
// beyond today. The slack accepts an entry dated slightly ahead (a charge the
// user pre-recorded) and a client whose clock runs fast, while still keeping
// the generated cycle span bounded in both directions.
func TransactionDateMax(now time.Time) time.Time {
	return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).AddDate(1, 0, 0)
}

// CheckTransactionDate parses a YYYY-MM-DD transaction date and enforces the
// ledger's supported window [1900-01-01, today+1y]. It returns the parsed date
// and a user-facing message — empty when the date is accepted, otherwise a
// sentence the caller writes as its 400 (optionally prefixed with the row it
// came from, e.g. "transaction 3: ...").
//
// Every write edge that accepts a transaction date goes through it (create,
// update, import, validate), so the window cannot drift between them; the
// cycle generator clamps independently, because rows written before the rule
// existed are still in the table.
func CheckTransactionDate(value string, now time.Time) (time.Time, string) {
	d, err := time.Parse("2006-01-02", value)
	if err != nil {
		return time.Time{}, "invalid date (expected YYYY-MM-DD)"
	}
	if max := TransactionDateMax(now); d.Before(transactionDateMinValue) || d.After(max) {
		return time.Time{}, fmt.Sprintf("date out of range (%s to %s)", TransactionDateMin, max.Format("2006-01-02"))
	}
	return d, ""
}
