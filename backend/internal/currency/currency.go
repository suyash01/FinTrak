// Package currency owns the one SQL expression that reads an account's currency.
//
// The expression is COALESCE(NULLIF(<col>, ''), 'INR'), and it is written here
// and nowhere else. Every reporting query and the query compiler's `ccy:` term
// build theirs from Column or Predicate, so a future edit to the spelling
// happens in one file instead of drifting across the ones that used to hold
// separate copies of it.
//
// The NULLIF is load-bearing, and it is here rather than incidental.
//
// accounts.currency is `VARCHAR(3) DEFAULT 'INR'` with no NOT NULL, so NULL is
// representable — and so is the empty string, because backup.go's restore scans
// COALESCE(currency, '') and re-inserts it verbatim, so a restored bundle is the
// one write path that can actually store "". A bare COALESCE(col, 'INR') reads
// NULL correctly and "" incorrectly, and the difference is invisible: the
// account becomes a "" key, or disappears from a report that every other part
// of the same response calls INR.
//
// The failure this shape prevents is specifically a *disagreement* between two
// copies of it. Within one statement, a WHERE missing the NULLIF that the
// SELECT has excludes the very account the filter exists to find, while the
// projection beside it still reports that account as INR — a silent drop of one
// row, with no error. Across surfaces it is the same defect wearing a hat: the
// ledger calling an unset currency something the dashboard does not, so
// `?currency=INR` matches on /dashboard/summary and misses on /transactions.
// Two surfaces of one product answering the same question differently is not a
// defect anyone would file, which is why the expression is defined once.
package currency

import "strconv"

// Default is the code an account is read as when its own is unset. It is the
// accounts.currency column default, not a configurable knob: a change here
// redefines what every stored "unset" account means, in every report at once.
const Default = "INR"

// Column renders the expression for one column. col is a SQL column reference
// as the calling query writes it — `a.currency`, `ac.currency` — or an
// expression yielding one, which the link queries need because a link's
// currency is picked by the same CASE that picks its amount.
//
// The column is substituted, not a table alias, because the queries do not
// agree on an alias: the transaction-driven ones join `accounts a`, the query
// compiler's correlated EXISTS reaches it as `ac`, and the two link queries go
// through `fa`/`ta`.
func Column(col string) string {
	return "COALESCE(NULLIF(" + col + ", ''), '" + Default + "')"
}

// Predicate renders Column bound to a placeholder, for the `?currency=` filter.
// It carries the same expression rather than comparing the raw column, because
// a predicate that compared the raw column would exclude every account whose
// currency is merely unset — the accounts a ?currency=INR report is for.
func Predicate(col string, placeholder int) string {
	return Column(col) + " = $" + strconv.Itoa(placeholder)
}
