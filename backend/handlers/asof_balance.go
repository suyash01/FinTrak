package handlers

import (
	"context"

	"github.com/fintrak/backend/internal/money"
	"github.com/fintrak/backend/models"
	"github.com/google/uuid"
)

// accountBalancesAsOfSQL is the statement behind the summary's `balances`
// block, and it is a var rather than a literal inside the function so a test can
// assert on the statement itself rather than only on the values it returned.
//
// THE LOAN BRANCH MUST STAY IDENTICAL TO GetAccounts (account.go). The
// expression below is copied from that query verbatim, with `AND t.date <= $2`
// added to each branch. They have to stay alike because a loan's balance comes
// from somewhere a reader would not guess:
//
//   - A loan account holds no transactions of its own. That is a schema
//     decision, not an accident — see the comment on accounts in
//     db/migrations/000001_initial_schema.up.sql. What it holds is
//     loan_attachments: the EMI payments made against it from other accounts.
//   - So a loan's balance is the sum over those ATTACHED transactions, and it
//     is always debit-positive: an EMI leaves the payer's account as a debit
//     and contributes +amount here, so the figure is the TOTAL PAID TO DATE —
//     positive, and growing with every payment. It is the same number
//     /accounts reports for that account, and the same one the loan schedule
//     calls PaidAmount. It is NOT what the borrower still owes: that is
//     LoanScheduleDetail.OutstandingPrincipal, a different figure over a
//     different question, and this balance must not be read as it.
//   - Every other account type sums its OWN transactions, signed by the type's
//     positive_txn_type — a credit-positive account (a bank, a card: both
//     seeded 'credit') balances negative on a debit, and a debit-positive
//     account balances positive on one.
//
// An as-of balance that omits the loan branch is the silent wrong number this
// whole feature was reworked to remove, and it is silent in the worst way: the
// statement still returns one row per account, still returns the right columns,
// and still answers 200. A car loan comes back at roughly zero, which reads as
// "you owed nothing" rather than as a bug. Nothing downstream can notice. So
// change one copy and you must change the other, and the tests pin the table
// name for exactly that reason.
//
// The two further differences from GetAccounts are both required by asking for
// a point in time rather than for now:
//
//   - `t.date <= $2` in BOTH branches, bound to the instant. A loan's bound is
//     the attached transaction's date, so "how much had I paid off by March" is
//     answered from the payments actually made by then, not from the loan's
//     account rows (of which there are none).
//   - The subqueries are correlated on a.id rather than grouped, so every
//     account returns a row. A GROUP BY over the transactions would drop an
//     account that held nothing at the instant, and an account holding nothing
//     on the day asked about is an answer, not an absence — a report of zeros,
//     never an empty list. The outer COALESCE(..., 0) keeps the column
//     non-null for the same reason.
var accountBalancesAsOfSQL = `
		SELECT a.id, a.name, ` + accountCurrency + ` as currency,
		COALESCE(CASE
			WHEN at.id = 'loan' THEN (
				SELECT SUM(CASE WHEN t.type = 'debit' THEN t.amount ELSE -t.amount END)
				FROM loan_attachments la JOIN transactions t ON t.id = la.transaction_id
				WHERE la.loan_account_id = a.id AND la.user_id = $1 AND t.date <= $2
			)
			ELSE (
				SELECT SUM(CASE
					WHEN at.positive_txn_type = 'credit' THEN (CASE WHEN t.type = 'credit' THEN t.amount ELSE -t.amount END)
					WHEN at.positive_txn_type = 'debit' THEN (CASE WHEN t.type = 'debit' THEN t.amount ELSE -t.amount END)
					ELSE 0 END)
				FROM transactions t WHERE t.account_id = a.id AND t.user_id = $1 AND t.date <= $2
			)
		END, 0) as balance
		FROM accounts a
		JOIN account_types at ON a.account_type_id = at.id
		WHERE a.user_id = $1
		ORDER BY a.created_at DESC`

// accountBalancesAsOf returns every one of the user's accounts with its balance
// at asOf, including the ones holding nothing. The returned slice is non-nil
// whenever the query succeeds, so an empty list means "no accounts", never
// "no money" — the caller takes its address, and a non-nil pointer to a nil
// slice would serialise as `"balances": null`, which is neither of the three
// states models.DashboardSummary.Balances is there to distinguish.
//
// The account's currency comes from accountCurrency, the same expression the
// summary's projections, its GROUP BYs and its ?currency= predicate all read
// through, so an account whose currency column is empty is filed as INR here
// exactly as it is everywhere else. It is spliced rather than written out for
// that reason alone: this repository has exactly one spelling of the default
// currency, and this query must not become a second one.
//
// The map is built with Add, which skips a zero contribution, so a key exists
// only where money actually was. That is what makes len(Balance) count the
// accounts that held something, and it is why a quiet account carries a named
// Currency and an empty Balance rather than a fabricated zero under a key.
//
// The order matches GetAccounts (`a.created_at DESC`) so the same account is not
// in a different place in the two responses.
//
// q is the handler's read-only transaction, so these balances are read from the
// same snapshot as the rest of the summary and cannot disagree with it.
func (srv *Server) accountBalancesAsOf(ctx context.Context, q scopeQueryer, userID uuid.UUID, asOf string) ([]models.AccountBalance, error) {
	rows, err := q.Query(ctx, accountBalancesAsOfSQL, userID, asOf)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	balances := make([]models.AccountBalance, 0)
	for rows.Next() {
		var b models.AccountBalance
		// money.Amount scans straight out of the BIGINT cents column, the same
		// way GetAccounts reads its balance, so no conversion happens here and
		// no money is ever in a float.
		var balance money.Amount
		if err := rows.Scan(&b.ID, &b.Name, &b.Currency, &balance); err != nil {
			return nil, err
		}
		b.Balance = models.NewCurrencyAmounts().Add(b.Currency, balance)
		balances = append(balances, b)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return balances, nil
}
