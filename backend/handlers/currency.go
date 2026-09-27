package handlers

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/fintrak/backend/internal/money"
	"github.com/fintrak/backend/internal/validation"
	"github.com/fintrak/backend/models"
)

// scopeSQL is the one query behind every reporting endpoint's per-currency
// totals and its currencyScope block. It reads accounts, not transactions,
// because the currency lives on the account and a transaction has none: the
// answer to "which currencies does this report span" is a question about the
// accounts in the window.
//
// The two filter fragments are spliced into different clauses on purpose. Date
// bounds belong in the JOIN's ON, so an account with no transactions in the
// window still appears in the scope — moving them to the WHERE would drop it,
// and the caller would be told a currency is absent when the account holding it
// is simply quiet this month.
const scopeSQL = `SELECT a.id, a.name, COALESCE(NULLIF(a.currency, ''), 'INR') AS currency,
	  COALESCE(SUM(t.amount) FILTER (WHERE t.type = 'credit'), 0) AS income,
	  COALESCE(SUM(t.amount) FILTER (WHERE t.type = 'debit'), 0) AS expense
	FROM accounts a
	LEFT JOIN transactions t ON t.account_id = a.id AND t.user_id = $1%s
	WHERE a.user_id = $1%s
	GROUP BY a.id, a.name, COALESCE(NULLIF(a.currency, ''), 'INR')
	ORDER BY a.name, a.id`

// defaultCurrency reads both NULL and the empty string out of
// accounts.currency. The column is `VARCHAR(3) DEFAULT 'INR'` with no NOT NULL
// (migration 000001), so a row can genuinely hold NULL, and NULLIF additionally
// covers a restored bundle carrying "". The API cannot create either —
// CreateAccount normalises "" to "INR" and UpdateAccount keeps the existing
// value through COALESCE(NULLIF(...), currency) — so this is defence at the one
// edge that bypasses both write paths. It is the same idiom account.go already
// uses, and without it a blank code would become a "" map key that every
// consumer would then have to defend against.
const defaultCurrency = "INR"

// scopeOptions is the window one aggregate covers. It is the reporting
// endpoints' query parameters, in the shape the shared query wants.
type scopeOptions struct {
	DateFrom  string
	DateTo    string
	AccountID string
	// Currency is already normalised by parseCurrency: upper case, or empty.
	Currency string
}

// scopeResult is what one currencyScope query yields: the scope the client is
// told about, and the two headline totals folded from the same rows. They travel
// together on purpose — a total that could describe a scope the response does not
// name would be exactly the silent gap this change exists to close.
type scopeResult struct {
	Scope   models.CurrencyScope
	Income  models.CurrencyAmounts
	Expense models.CurrencyAmounts
}

// Net is the per-currency difference, computed here so no client ever subtracts
// two maps by hand. A currency with expenses but no income still gets a key,
// because Sub counts a missing operand as zero.
func (r scopeResult) Net() models.CurrencyAmounts { return r.Income.Sub(r.Expense) }

// scopeQueryer is the pool surface currencyScope needs, so a test can hand it a
// pgxmock pool directly.
type scopeQueryer interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// parseCurrency reads the `currency` query parameter and normalises it to the
// upper-case three-letter form the accounts.currency column stores. Case is
// folded rather than compared case-sensitively because the failure is invisible:
// "usd" would bind against 'USD', match nothing, and render a report of zeros
// with no error anywhere. A code that is not three ASCII letters is a 400,
// naming the shape, because silently ignoring it would be the same silence.
func parseCurrency(c *gin.Context) (string, bool) {
	raw := strings.TrimSpace(c.Query("currency"))
	if raw == "" {
		return "", true
	}
	code := strings.ToUpper(raw)
	if len(code) != 3 {
		validation.RespondError(c, "invalid currency", 400)
		return "", false
	}
	for i := range len(code) {
		if code[i] < 'A' || code[i] > 'Z' {
			validation.RespondError(c, "invalid currency", 400)
			return "", false
		}
	}
	return code, true
}

// currencyScope runs scopeSQL once and returns the response's currency scope
// alongside the per-currency income and expense it folds from the same rows.
func (s *Server) currencyScope(ctx context.Context, q scopeQueryer, userID uuid.UUID, opts scopeOptions) (scopeResult, error) {
	var (
		args                  []any
		joinConds, whereConds []string
		param                 = 2
	)
	// add appends one already-formatted condition and its bound value. The
	// caller builds the $n placeholder from param rather than letting add do
	// it, because the currency predicate has to splice the column default into
	// the fragment as well and one fmt.Sprintf cannot fill both a %s and a %d
	// from different sources.
	add := func(conds *[]string, fragment string, value any) {
		*conds = append(*conds, fragment)
		args = append(args, value)
		param++
	}
	if opts.DateFrom != "" {
		add(&joinConds, fmt.Sprintf(" AND t.date >= $%d", param), opts.DateFrom)
	}
	if opts.DateTo != "" {
		add(&joinConds, fmt.Sprintf(" AND t.date <= $%d", param), opts.DateTo)
	}
	if opts.AccountID != "" {
		add(&whereConds, fmt.Sprintf(" AND a.id = $%d", param), opts.AccountID)
	}
	if opts.Currency != "" {
		// Compared against the same COALESCE the SELECT projects, so filtering
		// by INR finds the accounts whose currency is genuinely unset rather
		// than silently excluding them.
		add(&whereConds, fmt.Sprintf(" AND COALESCE(NULLIF(a.currency, ''), '%s') = $%d", defaultCurrency, param), opts.Currency)
	}

	stmt := fmt.Sprintf(scopeSQL, strings.Join(joinConds, ""), strings.Join(whereConds, ""))
	rows, err := q.Query(ctx, stmt, append([]any{userID}, args...)...)
	if err != nil {
		return scopeResult{}, err
	}
	defer rows.Close()

	res := scopeResult{Income: models.NewCurrencyAmounts(), Expense: models.NewCurrencyAmounts()}
	res.Scope.Accounts = []models.ScopedAccount{}
	seen := map[string]bool{}

	for rows.Next() {
		var (
			id              uuid.UUID
			name, code      string
			income, expense money.Amount
		)
		if err := rows.Scan(&id, &name, &code, &income, &expense); err != nil {
			return scopeResult{}, err
		}
		res.Scope.Accounts = append(res.Scope.Accounts, models.ScopedAccount{
			ID: id, Name: name, Currency: code,
			Income:  models.NewCurrencyAmounts().Add(code, income),
			Expense: models.NewCurrencyAmounts().Add(code, expense),
		})
		res.Income.Add(code, income)
		res.Expense.Add(code, expense)
		seen[code] = true
	}
	if err := rows.Err(); err != nil {
		return scopeResult{}, err
	}

	res.Scope.Currencies = make([]string, 0, len(seen))
	for code := range seen {
		res.Scope.Currencies = append(res.Scope.Currencies, code)
	}
	sort.Strings(res.Scope.Currencies)
	return res, nil
}
