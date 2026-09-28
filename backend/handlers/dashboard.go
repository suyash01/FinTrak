package handlers

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/fintrak/backend/auth"
	"github.com/fintrak/backend/db"
	currencysql "github.com/fintrak/backend/internal/currency"
	"github.com/fintrak/backend/internal/money"
	"github.com/fintrak/backend/internal/validation"
	"github.com/fintrak/backend/models"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const (
	billingCycleGroupBy  = "billing_cycle"
	defaultBillingCycles = 12
	maxBillingCycles     = 60
	// trendCurrencyParam is the placeholder the billing-cycle trend's currency
	// predicate binds. That query numbers its own arguments - account, user,
	// window start, window end, currency - so the fifth is fixed rather than
	// taken from the shared fragment's counter.
	trendCurrencyParam = 5
)

// accountCurrency is the expression every query in this file reads an account's
// currency through — the projection, the ROW_NUMBER partition, the GROUP BY and
// the ?currency= predicate all name it, so they cannot disagree about what an
// unset currency is. It is spliced into the query literals below rather than
// written into them; see internal/currency for why the NULLIF is load-bearing.
var accountCurrency = currencysql.Column("a.currency")

// currencyPredicate returns the account-currency filter for a placeholder. It
// names the same expression accountCurrency does — through the same shared
// definition — so it cannot drift from the projections it filters.
func currencyPredicate(placeholder int) string {
	return currencysql.Predicate("a.currency", placeholder)
}

// trendCurrencyFilter is currencyPredicate's counterpart for the billing-cycle
// trend, which cannot use the shared fragment because it is driven by
// billing_cycles rather than by transactions. It carries the same expression for
// the same reason: the account-level filter has to narrow the trend bars too, or
// a ?currency= response would show totals in one currency and bars in another.
func trendCurrencyFilter(currency string) string {
	if currency == "" {
		return ""
	}
	return " AND " + currencyPredicate(trendCurrencyParam)
}

// GetDashboardSummary aggregates the user's financial overview in one response:
// account and transaction counts, income/expense totals, per-category spend and
// income (top 15 each), a monthly income/expense trend, and the 10 most recent
// transactions. An optional date range, account and currency filter apply to
// every transaction-backed section - the currency filter narrows the whole
// response, so the totals, the breakdowns, the trend and the transaction list
// never describe different sets of transactions.
//
// Every amount is per-currency. With no account filter the window can span an
// INR account and a USD one, and no single figure can represent that, so the
// response carries one amount per currency alongside the scope that produced it.
//
// An `asOf=YYYY-MM-DD` names an instant — the ledger at the END of that day —
// rather than a day to exclude, so it becomes the window's upper bound and every
// transaction-backed section above is narrowed by it together. It also adds
// `asOf` and `balances`: each account's balance at that instant, including the
// accounts holding nothing. Both are omitted entirely when no instant was asked
// for.
func (srv *Server) GetDashboardSummary(c *gin.Context) {
	ctx := c
	userID := auth.GetUserID(c)
	dateFrom := c.Query("dateFrom")
	dateTo := c.Query("dateTo")
	accountID := c.Query("accountId")

	// The date bounds are compared against a date column and the account id
	// against a uuid column, so reject malformed filters up front instead of
	// letting them surface as 500s.
	dateFrom, ok := parseQueryDate(c, "dateFrom", dateFrom)
	if !ok {
		return
	}
	dateTo, ok = parseQueryDate(c, "dateTo", dateTo)
	if !ok {
		return
	}
	if accountID != "" {
		if _, err := uuid.Parse(accountID); err != nil {
			validation.RespondError(c, "invalid accountId", http.StatusBadRequest)
			return
		}
	}
	currency, ok := parseCurrency(c)
	if !ok {
		return
	}

	// asOf reports the ledger at the END of a named day, so it resolves against
	// the window just parsed above — the validated values, not a second read of
	// the request — and the clamp parseAsOf applies lands in dateTo below. It is
	// resolved here, before the billing-cycle branch, so groupBy=billing_cycle
	// gets the same 400s for a malformed or out-of-window instant rather than
	// silently ignoring the parameter.
	asOf, ok := parseAsOf(c, dateFrom, dateTo)
	if !ok {
		return
	}
	// The resolved instant BECOMES the window's end rather than a clause of its
	// own, which is the shape txnQueryFilter already established. It is the same
	// predicate and the same inclusive comparison, and parseAsOf has already
	// pulled the instant back to dateTo, so a second `t.date <=` would bind a
	// duplicate argument for a bound that says nothing the first does not. Every
	// later reader in this handler — the filter fragment below, the currency
	// scope, the echoed asOf — then reads one value, and none of them can answer
	// about a different instant than the one the response reports.
	if asOf != "" {
		dateTo = asOf
	}

	// Billing-cycle view: the whole summary is framed around the statement
	// periods of a single account that has a billing day set. The currency is
	// parsed above and passed down rather than read again, so both views filter
	// on the same value.
	if c.Query("groupBy") == billingCycleGroupBy {
		srv.getDashboardSummaryBillingCycle(c, currency)
		return
	}

	var (
		totalAccounts     int
		totalTransactions int
		byCategory        []models.CategorySpend
		incomeByCategory  []models.CategorySpend
		monthlyTrend      []models.MonthlyData
		recent            []models.Transaction
		// balances is the as-of block, and is nil unless asOf was asked for. It
		// is declared here so the pointer is taken once, beside the summary it
		// belongs to, rather than inside the query's branch.
		balances []models.AccountBalance
	)

	// One filter fragment serves every transaction-backed query below, all of
	// which alias the transactions table as t and join accounts as a. It is
	// numbered once, so no query can renumber another's placeholders.
	//
	// The currency predicate belongs here rather than in currencyScope alone: a
	// filter that reached only the stat cards would leave the trend and the
	// category breakdowns describing a wider window than the totals beside them,
	// which is the silent-wrong-number case this change exists to remove. It
	// reads the account and carries accountCurrency, the same expression the
	// scope query uses in its projection, its GROUP BY and its own predicate,
	// because all of them have to be the same expression.
	//
	// The predicate sits in the WHERE, which for these queries is the same place
	// as the join's ON clause: every one of them is driven by transactions and
	// inner-joins accounts, so a row that fails the predicate is one with no
	// account in scope. It is the account-driven scope query that has to keep
	// quiet accounts visible, and that guard lives in currencyScope, not here.
	//
	// Two closers build that fragment, and neither can do the other's job.
	// addCond owns the placeholder and appends it to an operator, which is what
	// the three column comparisons need. addFragment takes a condition that
	// already carries its own and binds only the value, which is what a currency
	// predicate needs: currencyPredicate renders a whole comparison, so handing
	// it to addCond produced `= $4 $4` and PostgreSQL rejected the statement.
	// That is the same shape currency.go's add has always had — a pre-formatted
	// fragment plus a bound value, with the caller building the $n — and it was
	// right there and wrong here only because addCond predates any
	// self-contained predicate. Two closures, one job each, is cheaper than
	// teaching one of them to guess which kind of condition it was handed.
	catFilter := ""
	args := []any{userID}
	paramIdx := 2
	addCond := func(cond string, val any) {
		catFilter += fmt.Sprintf(" AND %s $%d", cond, paramIdx)
		args = append(args, val)
		paramIdx++
	}
	addFragment := func(fragment string, val any) {
		catFilter += " AND " + fragment
		args = append(args, val)
		paramIdx++
	}
	if dateFrom != "" {
		addCond("t.date >=", dateFrom)
	}
	if dateTo != "" {
		addCond("t.date <=", dateTo)
	}
	if accountID != "" {
		addCond("t.account_id =", accountID)
	}
	if currency != "" {
		addFragment(currencyPredicate(paramIdx), currency)
	}

	// Run every read in a single read-only, repeatable-read transaction so the
	// stats reflect one consistent snapshot (concurrent writes can't partially
	// land between queries) and share a single connection.
	tx, err := srv.db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		slog.Error("GetDashboardSummary (begin)", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	defer tx.Rollback(ctx)
	q := tx

	// Total accounts
	if err := q.QueryRow(ctx, "SELECT COUNT(*) FROM accounts WHERE user_id = $1", userID).Scan(&totalAccounts); err != nil {
		slog.Error("GetDashboardSummary (total accounts)", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	// Transaction count. A count has no currency of its own, but it is still a
	// transaction-backed section, so it counts what the rest of the response
	// describes: accounts is joined for the currency filter, and the join is
	// inner so a transaction whose account row is missing is left out rather
	// than filed under the default currency.
	countQuery := `SELECT COUNT(*) FROM transactions t
				 JOIN accounts a ON a.id = t.account_id AND a.user_id = $1
				 WHERE t.user_id = $1` + catFilter
	if err := q.QueryRow(ctx, countQuery, args...).Scan(&totalTransactions); err != nil {
		slog.Error("GetDashboardSummary (transaction count)", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	// Income and expense per currency, and the accounts each currency came from.
	scope, err := srv.currencyScope(ctx, q, userID, scopeOptions{
		DateFrom:  dateFrom,
		DateTo:    dateTo,
		AccountID: accountID,
		Currency:  currency,
	})
	if err != nil {
		slog.Error("GetDashboardSummary (currency scope)", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	// By category (expenses only). The top 15 are cut per currency, so "top 15
	// by spend" keeps meaning the 15 biggest categories *of one currency* rather
	// than 15 rows drawn from an ordering that compares rupees with dollars.
	//
	// Three things make the window function legal and correct. Postgres
	// evaluates windows after grouping, so SUM(t.amount) is available in the
	// window's ORDER BY at this level, as are the grouped c.name and c.id beside
	// it. HAVING runs before it, and the inner join to accounts means a category
	// with no matching transaction never produces a row at all, so the
	// NULL-currency partition cannot exist and no unspend category can take a
	// slot. And the filter fragment is spliced into the WHERE, which is where the
	// inner join makes it equivalent to the ON clause.
	//
	// The window's ORDER BY ends in name and id on purpose: without them, rn
	// among equal totals is arbitrary and which 15 categories survive could
	// differ between two identical requests. The old LIMIT 15 carried the same
	// tiebreak and it is what kept the list from swapping in and out.
	//
	// The outer ORDER BY is display order over rows that are already capped, not
	// a ranking across currencies: nothing is compared, so nothing is added up.
	catQuery := `SELECT id, name, color, icon, currency, total, count FROM (
				 SELECT c.id, c.name, c.color, c.icon, ` + accountCurrency + ` AS currency,
				        COALESCE(SUM(t.amount), 0) as total, COUNT(t.id) as count,
				        ROW_NUMBER() OVER (PARTITION BY ` + accountCurrency + ` ORDER BY SUM(t.amount) DESC, c.name, c.id) AS rn
				 FROM categories c
				 LEFT JOIN transactions t ON t.category_id = c.id AND t.type = 'debit' AND t.user_id = $1
				 JOIN accounts a ON a.id = t.account_id AND a.user_id = $1` + catFilter + `
				 WHERE (c.user_id = $1 OR c.user_id IS NULL)
				 GROUP BY c.id, c.name, c.color, c.icon, ` + accountCurrency + `
				 HAVING COALESCE(SUM(t.amount), 0) > 0
				 ) ranked WHERE rn <= 15
				 ORDER BY total DESC, name, id`

	catRows, err := q.Query(ctx, catQuery, args...)
	if err != nil {
		slog.Error("GetDashboardSummary (by category)", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	defer catRows.Close()
	// One category can be spent in more than one currency, so the query returns a
	// row per (category, currency) pair and the fold puts them back together into
	// one entry per category, in the order the query returned them.
	totals := map[string]*models.CategorySpend{}
	order := []string{}
	for catRows.Next() {
		var (
			id, name, color, icon, code string
			total                       money.Amount
			count                       int
		)
		if err := catRows.Scan(&id, &name, &color, &icon, &code, &total, &count); err != nil {
			slog.Error("GetDashboardSummary scan (by category)", slog.String("error", err.Error()))
			validation.RespondError(c, "internal server error", http.StatusInternalServerError)
			return
		}
		entry, ok := totals[id]
		if !ok {
			entry = &models.CategorySpend{
				CategoryID: id, CategoryName: name, CategoryColor: color,
				CategoryIcon: icon, Total: models.NewCurrencyAmounts(),
			}
			totals[id] = entry
			order = append(order, id)
		}
		entry.Total.Add(code, total)
		entry.Count += count
	}
	if err := catRows.Err(); err != nil {
		slog.Error("GetDashboardSummary (by category rows)", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	for _, id := range order {
		byCategory = append(byCategory, *totals[id])
	}

	// By category (income only)
	incomeCatQuery := `SELECT id, name, color, icon, currency, total, count FROM (
				 SELECT c.id, c.name, c.color, c.icon, ` + accountCurrency + ` AS currency,
				        COALESCE(SUM(t.amount), 0) as total, COUNT(t.id) as count,
				        ROW_NUMBER() OVER (PARTITION BY ` + accountCurrency + ` ORDER BY SUM(t.amount) DESC, c.name, c.id) AS rn
				 FROM categories c
				 LEFT JOIN transactions t ON t.category_id = c.id AND t.type = 'credit' AND t.user_id = $1
				 JOIN accounts a ON a.id = t.account_id AND a.user_id = $1` + catFilter + `
				 WHERE (c.user_id = $1 OR c.user_id IS NULL)
				 GROUP BY c.id, c.name, c.color, c.icon, ` + accountCurrency + `
				 HAVING COALESCE(SUM(t.amount), 0) > 0
				 ) ranked WHERE rn <= 15
				 ORDER BY total DESC, name, id`

	incomeCatRows, err := q.Query(ctx, incomeCatQuery, args...)
	if err != nil {
		slog.Error("GetDashboardSummary (income by category)", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	defer incomeCatRows.Close()
	incomeTotals := map[string]*models.CategorySpend{}
	incomeOrder := []string{}
	for incomeCatRows.Next() {
		var (
			id, name, color, icon, code string
			total                       money.Amount
			count                       int
		)
		if err := incomeCatRows.Scan(&id, &name, &color, &icon, &code, &total, &count); err != nil {
			slog.Error("GetDashboardSummary scan (income by category)", slog.String("error", err.Error()))
			validation.RespondError(c, "internal server error", http.StatusInternalServerError)
			return
		}
		entry, ok := incomeTotals[id]
		if !ok {
			entry = &models.CategorySpend{
				CategoryID: id, CategoryName: name, CategoryColor: color,
				CategoryIcon: icon, Total: models.NewCurrencyAmounts(),
			}
			incomeTotals[id] = entry
			incomeOrder = append(incomeOrder, id)
		}
		entry.Total.Add(code, total)
		entry.Count += count
	}
	if err := incomeCatRows.Err(); err != nil {
		slog.Error("GetDashboardSummary (income by category rows)", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	for _, id := range incomeOrder {
		incomeByCategory = append(incomeByCategory, *incomeTotals[id])
	}

	// Monthly trend. accounts is joined so a month can be split by currency: one
	// month is one entry holding one amount per currency, not one entry per
	// currency. The join is inner, matching the category queries, so a
	// transaction with no account row is left out of both rather than filed
	// under the default currency in one and dropped from the other.
	monthlyQuery := `SELECT TO_CHAR(t.date, 'YYYY-MM') as month,
					 ` + accountCurrency + ` as currency,
					 COALESCE(SUM(CASE WHEN t.type = 'credit' THEN t.amount ELSE 0 END), 0) as income,
					 COALESCE(SUM(CASE WHEN t.type = 'debit' THEN t.amount ELSE 0 END), 0) as expense
					 FROM transactions t
					 JOIN accounts a ON t.account_id = a.id
					 WHERE t.user_id = $1` + catFilter + `
					 GROUP BY TO_CHAR(t.date, 'YYYY-MM'), ` + accountCurrency + `
					 ORDER BY month`

	monthRows, err := q.Query(ctx, monthlyQuery, args...)
	if err != nil {
		slog.Error("GetDashboardSummary (monthly trend)", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	defer monthRows.Close()
	trend := map[string]*models.MonthlyData{}
	trendOrder := []string{}
	for monthRows.Next() {
		var month, code string
		var income, expense money.Amount
		if err := monthRows.Scan(&month, &code, &income, &expense); err != nil {
			slog.Error("GetDashboardSummary scan (monthly trend)", slog.String("error", err.Error()))
			validation.RespondError(c, "internal server error", http.StatusInternalServerError)
			return
		}
		entry, ok := trend[month]
		if !ok {
			entry = &models.MonthlyData{Month: month, Income: models.NewCurrencyAmounts(), Expense: models.NewCurrencyAmounts()}
			trend[month] = entry
			trendOrder = append(trendOrder, month)
		}
		entry.Income.Add(code, income)
		entry.Expense.Add(code, expense)
	}
	if err := monthRows.Err(); err != nil {
		slog.Error("GetDashboardSummary (monthly trend rows)", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	for _, month := range trendOrder {
		monthlyTrend = append(monthlyTrend, *trend[month])
	}

	// Recent transactions
	recentQuery := `SELECT t.id, t.account_id, t.date, t.description, t.amount, t.type,
					t.category_id, COALESCE(t.tags, '{}') as tags, t.notes, t.payee_id, COALESCE(p.name, '') as payee, t.created_at,
					a.name as account_name,
					COALESCE(c.name, '') as category_name,
					COALESCE(c.icon, '') as category_icon,
					COALESCE(c.color, '') as category_color
					FROM transactions t
					JOIN accounts a ON t.account_id = a.id
					LEFT JOIN categories c ON t.category_id = c.id
					LEFT JOIN payees p ON t.payee_id = p.id
					WHERE t.user_id = $1` + catFilter + `
					ORDER BY ` + txnOrderByDate(false) + `
					LIMIT 10`

	recentRows, err := q.Query(ctx, recentQuery, args...)
	if err != nil {
		slog.Error("GetDashboardSummary (recent transactions)", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	defer recentRows.Close()
	for recentRows.Next() {
		var t models.Transaction
		if err := recentRows.Scan(&t.ID, &t.AccountID, &t.Date, &t.Description, &t.Amount, &t.Type,
			&t.CategoryID, &t.Tags, &t.Notes, &t.PayeeID, &t.Payee, &t.CreatedAt,
			&t.AccountName, &t.CategoryName, &t.CategoryIcon, &t.CategoryColor); err != nil {
			slog.Error("GetDashboardSummary scan (recent transactions)", slog.String("error", err.Error()))
			validation.RespondError(c, "internal server error", http.StatusInternalServerError)
			return
		}
		recent = append(recent, t)
	}
	if err := recentRows.Err(); err != nil {
		slog.Error("GetDashboardSummary (recent transactions rows)", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	// The as-of balances, on the same read-only snapshot as everything above —
	// a balance computed from a different instant than the trend beside it would
	// be a response describing two different ledgers.
	//
	// Only when an instant was asked for. A summary is the most-polled endpoint
	// in the app, and this is a correlated subquery per account; running it for
	// callers who never asked would tax every request for a field they will
	// never see.
	if asOf != "" {
		balances, err = srv.accountBalancesAsOf(ctx, q, userID, asOf)
		if err != nil {
			slog.Error("accountBalancesAsOf", slog.String("error", err.Error()))
			validation.RespondError(c, "internal server error", http.StatusInternalServerError)
			return
		}
	}

	// Empty slices rather than nil, so the body carries [] and a client does not
	// have to tell an absent list from a null one.
	if byCategory == nil {
		byCategory = []models.CategorySpend{}
	}
	if incomeByCategory == nil {
		incomeByCategory = []models.CategorySpend{}
	}
	if monthlyTrend == nil {
		monthlyTrend = []models.MonthlyData{}
	}
	if recent == nil {
		recent = []models.Transaction{}
	}

	// TotalNet is the server's per-currency difference, not a subtraction the
	// client performs: income minus expense is only defined inside one currency,
	// and only the server knows which currencies are in scope.
	summary := models.DashboardSummary{
		TotalAccounts:      totalAccounts,
		TotalTransactions:  totalTransactions,
		TotalIncome:        scope.Income,
		TotalExpense:       scope.Expense,
		TotalNet:           scope.Net(),
		ByCategory:         byCategory,
		IncomeByCategory:   incomeByCategory,
		MonthlyTrend:       monthlyTrend,
		RecentTransactions: recent,
		CurrencyScope:      scope.Scope,
	}

	// Both as-of fields are POINTERS, and nil is what omits them: a response
	// for a request that never asked for an instant is byte-for-byte what it was
	// before this feature.
	//
	// Taking the address of `balances` here is safe only because
	// accountBalancesAsOf allocated it. A non-nil pointer to a nil slice is
	// neither "not asked" nor "no accounts" — it serialises as
	// `"balances": null`, and a client reading that sees a server that lost the
	// value rather than a user with no accounts.
	if asOf != "" {
		summary.AsOf = &asOf
		summary.Balances = &balances
	}

	if err := tx.Commit(ctx); err != nil {
		slog.Error("GetDashboardSummary (commit)", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	c.JSON(http.StatusOK, summary)
}

// getDashboardSummaryBillingCycle returns a statement-period framed summary for
// a single account that has a billing day set. The stat-card totals reflect the
// current (in-progress) cycle, the trend is one bar per billing cycle for the
// last `cycles` cycles, the category breakdowns span that cycle window, and the
// recent transactions come from the current cycle. Date-range filters are
// ignored in this mode; the window is defined by billing cycles instead.
// `currency` is the already-normalised ?currency filter, parsed by the caller
// so both views narrow on the same value.
func (srv *Server) getDashboardSummaryBillingCycle(c *gin.Context, currency string) {
	ctx := c
	userID := auth.GetUserID(c)

	accountID, err := uuid.Parse(c.Query("accountId"))
	if err != nil {
		validation.RespondError(c, "accountId is required for billing cycle view", http.StatusBadRequest)
		return
	}

	// The account must exist, belong to the user, and have a billing day set.
	var billingDay *int
	err = srv.db.QueryRow(ctx,
		`SELECT a.billing_day
		 FROM accounts a WHERE a.id = $1 AND a.user_id = $2`,
		accountID, userID).Scan(&billingDay)
	if errors.Is(err, pgx.ErrNoRows) {
		validation.RespondError(c, "account not found", http.StatusNotFound)
		return
	}
	if err != nil {
		slog.Error("GetDashboardSummary (billing cycle account lookup)", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	if billingDay == nil {
		validation.RespondError(c, "account has no billing day", http.StatusBadRequest)
		return
	}

	// The regeneration detaches and recreates the account's cycles, so it runs
	// in one transaction: an interruption between the detach and the delete
	// would otherwise leave its transactions detached from cycles that still
	// exist.
	if err := db.WithTx(ctx, srv.db, func(tx pgx.Tx) error {
		return ensureBillingCycles(ctx, tx, userID, accountID, *billingDay)
	}); err != nil {
		slog.Error("GetDashboardSummary (ensure billing cycles)", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	// Read the summary inside a single read-only snapshot once cycle generation
	// (which writes) has finished.
	tx, err := srv.db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		slog.Error("GetDashboardSummary (begin)", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	defer tx.Rollback(ctx)
	q := tx

	cycles, err := listBillingCycles(ctx, q, userID, accountID)
	if err != nil {
		slog.Error("GetDashboardSummary (list billing cycles)", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	// The amount maps start empty rather than nil: this response is built before
	// the window exists, and a nil map marshals as null, which would be a
	// contract break on the early return below.
	summary := models.DashboardSummary{
		TotalIncome:        models.NewCurrencyAmounts(),
		TotalExpense:       models.NewCurrencyAmounts(),
		TotalNet:           models.NewCurrencyAmounts(),
		CurrencyScope:      models.CurrencyScope{Currencies: []string{}, Accounts: []models.ScopedAccount{}},
		ByCategory:         []models.CategorySpend{},
		IncomeByCategory:   []models.CategorySpend{},
		MonthlyTrend:       []models.MonthlyData{},
		BillingCycleTrend:  []models.BillingCycleTrendItem{},
		RecentTransactions: []models.Transaction{},
	}

	// Total accounts
	if err := q.QueryRow(ctx, "SELECT COUNT(*) FROM accounts WHERE user_id = $1", userID).Scan(&summary.TotalAccounts); err != nil {
		slog.Error("GetDashboardSummary (total accounts)", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	if len(cycles) == 0 {
		// No cycle means no window, so the summary stays the empty one built
		// above: the response covers no currency rather than an unnamed one.
		c.JSON(http.StatusOK, summary)
		return
	}

	// Window: the last `cycles` billing cycles, ending with the current one.
	numCycles := defaultBillingCycles
	if raw := c.Query("cycles"); raw != "" {
		if n, perr := strconv.Atoi(raw); perr == nil && n > 0 {
			if n > maxBillingCycles {
				n = maxBillingCycles
			}
			numCycles = n
		}
	}
	window := cycles
	if len(cycles) > numCycles {
		window = cycles[len(cycles)-numCycles:]
	}
	current := window[len(window)-1]
	windowStart := window[0].StartDate.Format("2006-01-02")
	windowEnd := window[len(window)-1].EndDate.Format("2006-01-02")

	summary.CurrentCycle = &models.CurrentCycleInfo{
		ID:        current.ID,
		StartDate: current.StartDate,
		EndDate:   current.EndDate,
		Label:     current.Label,
	}

	// Stat-card totals across every displayed cycle, from the currency scope.
	// Every other section below is filtered by the same window, account and
	// currency, so the headline figures, the trend and the count all describe the
	// same transactions. A count has no currency of its own but it is still
	// transaction-backed, so it takes the same filter.
	scope, err := srv.currencyScope(ctx, q, userID, scopeOptions{
		DateFrom:  windowStart,
		DateTo:    windowEnd,
		AccountID: accountID.String(),
		Currency:  currency,
	})
	if err != nil {
		slog.Error("GetDashboardSummary (currency scope)", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	summary.TotalIncome = scope.Income
	summary.TotalExpense = scope.Expense
	summary.TotalNet = scope.Net()
	summary.CurrencyScope = scope.Scope

	// One filter fragment for the transaction-backed queries below, numbered once
	// so no query can renumber another's placeholders. The account's own
	// currency filter rides along so a caller asking for a currency this account
	// does not hold gets an empty report rather than one in the wrong currency.
	//
	// Two closers, for the reason given at the month view's copy: addCond owns
	// the placeholder and takes an operator, addFragment takes a condition that
	// already carries one. A currency predicate is the second kind, and routing
	// it through the first is what made this endpoint answer 500 before.
	catFilter := ""
	catArgs := []any{userID}
	paramIdx := 2
	addCond := func(cond string, val any) {
		catFilter += fmt.Sprintf(" AND %s $%d", cond, paramIdx)
		catArgs = append(catArgs, val)
		paramIdx++
	}
	addFragment := func(fragment string, val any) {
		catFilter += " AND " + fragment
		catArgs = append(catArgs, val)
		paramIdx++
	}
	addCond("t.date >=", windowStart)
	addCond("t.date <=", windowEnd)
	addCond("t.account_id =", accountID)
	if currency != "" {
		addFragment(currencyPredicate(paramIdx), currency)
	}

	cycleCountQuery := `SELECT COUNT(*) FROM transactions t
			 JOIN accounts a ON a.id = t.account_id AND a.user_id = $1
			 WHERE t.user_id = $1` + catFilter
	if err := q.QueryRow(ctx, cycleCountQuery, catArgs...).
		Scan(&summary.TotalTransactions); err != nil {
		slog.Error("GetDashboardSummary (cycle window count)", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	// Per-cycle trend over the window. The account is joined for its currency, so
	// the trend folds per currency like every other amount here, and the
	// transactions are joined by the cycle's own date range rather than by
	// t.billing_cycle_id.
	//
	// That join is the same set the stat cards count. Joining on
	// billing_cycle_id instead would be a cheaper plan, but a transaction whose
	// cycle assignment is detached or NULL would then be in the headline totals
	// and in no bar at all, which is the disagreement this comment exists to
	// prevent. Do not "optimise" the join back to the cycle id.
	trendQuery := `SELECT bc.label, bc.start_date, bc.end_date,
			 ` + accountCurrency + ` AS currency,
			 COALESCE(SUM(CASE WHEN t.type = 'credit' THEN t.amount ELSE 0 END), 0) as income,
			 COALESCE(SUM(CASE WHEN t.type = 'debit' THEN t.amount ELSE 0 END), 0) as expense
			 FROM billing_cycles bc
			 JOIN accounts a ON a.id = bc.account_id AND a.user_id = $2
			 LEFT JOIN transactions t ON t.account_id = bc.account_id AND t.user_id = $2
			   AND t.date >= bc.start_date AND t.date <= bc.end_date
			 WHERE bc.account_id = $1 AND bc.user_id = $2
			   AND bc.end_date >= $3 AND bc.end_date <= $4` + trendCurrencyFilter(currency) + `
			 GROUP BY bc.id, bc.label, bc.start_date, bc.end_date, ` + accountCurrency + `
			 ORDER BY bc.start_date ASC`
	trendArgs := []any{accountID, userID, windowStart, windowEnd}
	if currency != "" {
		trendArgs = append(trendArgs, currency)
	}
	trendRows, err := q.Query(ctx, trendQuery, trendArgs...)
	if err != nil {
		slog.Error("GetDashboardSummary (billing cycle trend)", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	defer trendRows.Close()
	// Folded by label because one cycle can carry more than one currency row; an
	// account's cycle labels are unique, so the key identifies the cycle.
	trend := map[string]*models.BillingCycleTrendItem{}
	trendOrder := []string{}
	for trendRows.Next() {
		var item models.BillingCycleTrendItem
		var code string
		var income, expense money.Amount
		if err := trendRows.Scan(&item.Label, &item.StartDate, &item.EndDate, &code, &income, &expense); err != nil {
			slog.Error("GetDashboardSummary scan (billing cycle trend)", slog.String("error", err.Error()))
			validation.RespondError(c, "internal server error", http.StatusInternalServerError)
			return
		}
		entry, ok := trend[item.Label]
		if !ok {
			entry = &models.BillingCycleTrendItem{
				Label: item.Label, StartDate: item.StartDate, EndDate: item.EndDate,
				Income: models.NewCurrencyAmounts(), Expense: models.NewCurrencyAmounts(),
			}
			trend[item.Label] = entry
			trendOrder = append(trendOrder, item.Label)
		}
		entry.Income.Add(code, income)
		entry.Expense.Add(code, expense)
	}
	if err := trendRows.Err(); err != nil {
		slog.Error("GetDashboardSummary (billing cycle trend rows)", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	for _, label := range trendOrder {
		summary.BillingCycleTrend = append(summary.BillingCycleTrend, *trend[label])
	}

	// Category breakdowns over the cycle window; same name/id tiebreak as the
	// unbounded summary so the top 15 are stable. The window is one account, so
	// there is a single currency and the per-currency ROW_NUMBER the unbounded
	// query needs would be a no-op partition here - a plain LIMIT 15 says the
	// same thing. accounts is inner-joined for the same reason as there: a
	// transaction with no account row is not this account's money.
	catQuery := `SELECT c.id, c.name, c.color, c.icon, ` + accountCurrency + ` AS currency,
				 COALESCE(SUM(t.amount), 0) as total, COUNT(t.id)
				 FROM categories c
				 LEFT JOIN transactions t ON t.category_id = c.id AND t.type = 'debit' AND t.user_id = $1
				 JOIN accounts a ON a.id = t.account_id AND a.user_id = $1` + catFilter + `
				 WHERE (c.user_id = $1 OR c.user_id IS NULL)
				 GROUP BY c.id, c.name, c.color, c.icon, ` + accountCurrency + `
				 HAVING COALESCE(SUM(t.amount), 0) > 0
				 ORDER BY total DESC, c.name, c.id
				 LIMIT 15`
	catRows, err := q.Query(ctx, catQuery, catArgs...)
	if err != nil {
		slog.Error("GetDashboardSummary (by category)", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	defer catRows.Close()
	totals := map[string]*models.CategorySpend{}
	order := []string{}
	for catRows.Next() {
		var (
			id, name, color, icon, code string
			total                       money.Amount
			count                       int
		)
		if err := catRows.Scan(&id, &name, &color, &icon, &code, &total, &count); err != nil {
			slog.Error("GetDashboardSummary scan (by category)", slog.String("error", err.Error()))
			validation.RespondError(c, "internal server error", http.StatusInternalServerError)
			return
		}
		entry, ok := totals[id]
		if !ok {
			entry = &models.CategorySpend{
				CategoryID: id, CategoryName: name, CategoryColor: color,
				CategoryIcon: icon, Total: models.NewCurrencyAmounts(),
			}
			totals[id] = entry
			order = append(order, id)
		}
		entry.Total.Add(code, total)
		entry.Count += count
	}
	if err := catRows.Err(); err != nil {
		slog.Error("GetDashboardSummary (by category rows)", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	for _, id := range order {
		summary.ByCategory = append(summary.ByCategory, *totals[id])
	}

	incomeCatQuery := `SELECT c.id, c.name, c.color, c.icon, ` + accountCurrency + ` AS currency,
				 COALESCE(SUM(t.amount), 0) as total, COUNT(t.id)
				 FROM categories c
				 LEFT JOIN transactions t ON t.category_id = c.id AND t.type = 'credit' AND t.user_id = $1
				 JOIN accounts a ON a.id = t.account_id AND a.user_id = $1` + catFilter + `
				 WHERE (c.user_id = $1 OR c.user_id IS NULL)
				 GROUP BY c.id, c.name, c.color, c.icon, ` + accountCurrency + `
				 HAVING COALESCE(SUM(t.amount), 0) > 0
				 ORDER BY total DESC, c.name, c.id
				 LIMIT 15`
	incomeCatRows, err := q.Query(ctx, incomeCatQuery, catArgs...)
	if err != nil {
		slog.Error("GetDashboardSummary (income by category)", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	defer incomeCatRows.Close()
	incomeTotals := map[string]*models.CategorySpend{}
	incomeOrder := []string{}
	for incomeCatRows.Next() {
		var (
			id, name, color, icon, code string
			total                       money.Amount
			count                       int
		)
		if err := incomeCatRows.Scan(&id, &name, &color, &icon, &code, &total, &count); err != nil {
			slog.Error("GetDashboardSummary scan (income by category)", slog.String("error", err.Error()))
			validation.RespondError(c, "internal server error", http.StatusInternalServerError)
			return
		}
		entry, ok := incomeTotals[id]
		if !ok {
			entry = &models.CategorySpend{
				CategoryID: id, CategoryName: name, CategoryColor: color,
				CategoryIcon: icon, Total: models.NewCurrencyAmounts(),
			}
			incomeTotals[id] = entry
			incomeOrder = append(incomeOrder, id)
		}
		entry.Total.Add(code, total)
		entry.Count += count
	}
	if err := incomeCatRows.Err(); err != nil {
		slog.Error("GetDashboardSummary (income by category rows)", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	for _, id := range incomeOrder {
		summary.IncomeByCategory = append(summary.IncomeByCategory, *incomeTotals[id])
	}

	// Recent transactions across the displayed cycle window (so the list is
	// non-empty even while the current in-progress cycle has no activity yet).
	recentQuery := `SELECT t.id, t.account_id, t.date, t.description, t.amount, t.type,
					t.category_id, COALESCE(t.tags, '{}') as tags, t.notes, t.payee_id, COALESCE(p.name, '') as payee, t.created_at,
					a.name as account_name,
					COALESCE(c.name, '') as category_name,
					COALESCE(c.icon, '') as category_icon,
					COALESCE(c.color, '') as category_color
					FROM transactions t
					JOIN accounts a ON t.account_id = a.id
					LEFT JOIN categories c ON t.category_id = c.id
					LEFT JOIN payees p ON t.payee_id = p.id
					WHERE t.user_id = $1` + catFilter + `
					ORDER BY ` + txnOrderByDate(false) + `
					LIMIT 10`
	recentRows, err := q.Query(ctx, recentQuery, catArgs...)
	if err != nil {
		slog.Error("GetDashboardSummary (recent transactions)", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}
	defer recentRows.Close()
	for recentRows.Next() {
		var t models.Transaction
		if err := recentRows.Scan(&t.ID, &t.AccountID, &t.Date, &t.Description, &t.Amount, &t.Type,
			&t.CategoryID, &t.Tags, &t.Notes, &t.PayeeID, &t.Payee, &t.CreatedAt,
			&t.AccountName, &t.CategoryName, &t.CategoryIcon, &t.CategoryColor); err != nil {
			slog.Error("GetDashboardSummary scan (recent transactions)", slog.String("error", err.Error()))
			validation.RespondError(c, "internal server error", http.StatusInternalServerError)
			return
		}
		summary.RecentTransactions = append(summary.RecentTransactions, t)
	}
	if err := recentRows.Err(); err != nil {
		slog.Error("GetDashboardSummary (recent transactions rows)", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	if err := tx.Commit(ctx); err != nil {
		slog.Error("GetDashboardSummary (commit)", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	c.JSON(http.StatusOK, summary)
}
