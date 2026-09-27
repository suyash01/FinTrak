package handlers

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/fintrak/backend/auth"
	"github.com/fintrak/backend/db"
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
)

// GetDashboardSummary aggregates the user's financial overview in one response:
// account and transaction counts, income/expense totals, per-category spend and
// income (top 15 each), a monthly income/expense trend, and the 10 most recent
// transactions. An optional date range, account and currency filter apply to
// every transaction-backed section.
//
// Every amount is per-currency. With no account filter the window can span an
// INR account and a USD one, and no single figure can represent that, so the
// response carries one amount per currency alongside the scope that produced it.
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

	// Billing-cycle view: the whole summary is framed around the statement
	// periods of a single account that has a billing day set.
	if c.Query("groupBy") == billingCycleGroupBy {
		srv.getDashboardSummaryBillingCycle(c)
		return
	}

	var (
		totalAccounts     int
		totalTransactions int
		byCategory        []models.CategorySpend
		incomeByCategory  []models.CategorySpend
		monthlyTrend      []models.MonthlyData
		recent            []models.Transaction
	)

	// Build transaction filters (date range + account). plainFilter is used by
	// queries without a table alias; catFilter prefixes columns with t. for
	// queries that join/alias the transactions table. Both number their
	// placeholders identically, so a query can switch between them without
	// touching its arguments.
	plainFilter := ""
	catFilter := ""
	args := []any{userID}
	paramIdx := 2
	addCond := func(col, op string, val any) {
		plainFilter += fmt.Sprintf(" AND %s %s $%d", col, op, paramIdx)
		catFilter += fmt.Sprintf(" AND t.%s %s $%d", col, op, paramIdx)
		args = append(args, val)
		paramIdx++
	}
	if dateFrom != "" {
		addCond("date", ">=", dateFrom)
	}
	if dateTo != "" {
		addCond("date", "<=", dateTo)
	}
	if accountID != "" {
		addCond("account_id", "=", accountID)
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

	// Transaction count. A count has no currency, so it stays a plain COUNT
	// rather than riding along with the income and expense sums - those come
	// from the currency scope below, which keeps them apart per currency.
	countQuery := `SELECT COUNT(*) FROM transactions WHERE user_id = $1` + plainFilter
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
	// window's ORDER BY at this level. HAVING runs before it, so a category with
	// no matching transaction - whose SUM is NULL and whose currency is NULL
	// because the join found nothing - is already gone and cannot occupy a slot
	// in the NULL-currency partition. And the chained LEFT JOIN accounts touches
	// neither the t.date nor the t.account_id predicates catFilter carries, so
	// the same fragment still numbers its placeholders the same way.
	//
	// The outer ORDER BY is display order over rows that are already capped, not
	// a ranking across currencies: nothing is compared, so nothing is added up.
	catQuery := `SELECT id, name, color, icon, currency, total, count FROM (
				 SELECT c.id, c.name, c.color, c.icon, COALESCE(NULLIF(a.currency, ''), 'INR') AS currency,
				        COALESCE(SUM(t.amount), 0) as total, COUNT(t.id) as count,
				        ROW_NUMBER() OVER (PARTITION BY COALESCE(NULLIF(a.currency, ''), 'INR') ORDER BY SUM(t.amount) DESC) AS rn
				 FROM categories c
				 LEFT JOIN transactions t ON t.category_id = c.id AND t.type = 'debit' AND t.user_id = $1
				 LEFT JOIN accounts a ON a.id = t.account_id AND a.user_id = $1` + catFilter + `
				 WHERE (c.user_id = $1 OR c.user_id IS NULL)
				 GROUP BY c.id, c.name, c.color, c.icon, COALESCE(NULLIF(a.currency, ''), 'INR')
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
				 SELECT c.id, c.name, c.color, c.icon, COALESCE(NULLIF(a.currency, ''), 'INR') AS currency,
				        COALESCE(SUM(t.amount), 0) as total, COUNT(t.id) as count,
				        ROW_NUMBER() OVER (PARTITION BY COALESCE(NULLIF(a.currency, ''), 'INR') ORDER BY SUM(t.amount) DESC) AS rn
				 FROM categories c
				 LEFT JOIN transactions t ON t.category_id = c.id AND t.type = 'credit' AND t.user_id = $1
				 LEFT JOIN accounts a ON a.id = t.account_id AND a.user_id = $1` + catFilter + `
				 WHERE (c.user_id = $1 OR c.user_id IS NULL)
				 GROUP BY c.id, c.name, c.color, c.icon, COALESCE(NULLIF(a.currency, ''), 'INR')
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
	// currency. The filter is catFilter because the columns are now qualified.
	monthlyQuery := `SELECT TO_CHAR(t.date, 'YYYY-MM') as month,
					 COALESCE(NULLIF(a.currency, ''), 'INR') as currency,
					 COALESCE(SUM(CASE WHEN t.type = 'credit' THEN t.amount ELSE 0 END), 0) as income,
					 COALESCE(SUM(CASE WHEN t.type = 'debit' THEN t.amount ELSE 0 END), 0) as expense
					 FROM transactions t
					 JOIN accounts a ON t.account_id = a.id
					 WHERE t.user_id = $1` + catFilter + `
					 GROUP BY TO_CHAR(t.date, 'YYYY-MM'), COALESCE(NULLIF(a.currency, ''), 'INR')
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
func (srv *Server) getDashboardSummaryBillingCycle(c *gin.Context) {
	ctx := c
	userID := auth.GetUserID(c)

	accountID, err := uuid.Parse(c.Query("accountId"))
	if err != nil {
		validation.RespondError(c, "accountId is required for billing cycle view", http.StatusBadRequest)
		return
	}

	// One account holds exactly one currency, so this filter cannot make the
	// response multi-currency; it is still honoured so a caller asking for a
	// currency the account does not have gets an empty report rather than one in
	// the wrong currency.
	currency, ok := parseCurrency(c)
	if !ok {
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

	// Stat-card totals across every displayed cycle. The amounts come from the
	// currency scope, windowed by the same cycle dates as everything else below,
	// so the headline figures and the trend chart cannot describe different
	// periods. The transaction count is separate because a count has no currency.
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

	cycleCountQuery := `SELECT COUNT(t.id)
			 FROM transactions t
			 JOIN billing_cycles bc ON t.billing_cycle_id = bc.id
			 WHERE t.user_id = $1 AND t.account_id = $2
			   AND bc.end_date >= $3 AND bc.end_date <= $4`
	if err := q.QueryRow(ctx, cycleCountQuery, userID, accountID, windowStart, windowEnd).
		Scan(&summary.TotalTransactions); err != nil {
		slog.Error("GetDashboardSummary (cycle window count)", slog.String("error", err.Error()))
		validation.RespondError(c, "internal server error", http.StatusInternalServerError)
		return
	}

	// Per-cycle trend over the window. The account is joined on for its currency,
	// so the trend folds per currency like every other amount in this response.
	trendQuery := `SELECT bc.label, bc.start_date, bc.end_date,
			 COALESCE(NULLIF(a.currency, ''), 'INR') AS currency,
			 COALESCE(SUM(CASE WHEN t.type = 'credit' THEN t.amount ELSE 0 END), 0) as income,
			 COALESCE(SUM(CASE WHEN t.type = 'debit' THEN t.amount ELSE 0 END), 0) as expense
			 FROM billing_cycles bc
			 LEFT JOIN transactions t ON t.billing_cycle_id = bc.id
			 LEFT JOIN accounts a ON a.id = bc.account_id
			 WHERE bc.account_id = $1 AND bc.user_id = $2
			   AND bc.end_date >= $3 AND bc.end_date <= $4
			 GROUP BY bc.id, bc.label, bc.start_date, bc.end_date, COALESCE(NULLIF(a.currency, ''), 'INR')
			 ORDER BY bc.start_date ASC`
	trendRows, err := q.Query(ctx, trendQuery, accountID, userID, windowStart, windowEnd)
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

	// Category breakdowns over the cycle window.
	catFilter := ""
	catArgs := []any{userID}
	paramIdx := 2
	addCond := func(col, op string, val any) {
		catFilter += fmt.Sprintf(" AND t.%s %s $%d", col, op, paramIdx)
		catArgs = append(catArgs, val)
		paramIdx++
	}
	addCond("date", ">=", windowStart)
	addCond("date", "<=", windowEnd)
	addCond("account_id", "=", accountID)

	// Category breakdowns over the cycle window; same name/id tiebreak as the
	// unbounded summary so the top 15 are stable. The window is one account, so
	// there is a single currency and the per-currency ROW_NUMBER the unbounded
	// query needs would be a no-op partition here - a plain LIMIT 15 says the
	// same thing.
	catQuery := `SELECT c.id, c.name, c.color, c.icon, COALESCE(NULLIF(a.currency, ''), 'INR') AS currency,
				 COALESCE(SUM(t.amount), 0) as total, COUNT(t.id)
				 FROM categories c
				 LEFT JOIN transactions t ON t.category_id = c.id AND t.type = 'debit' AND t.user_id = $1
				 LEFT JOIN accounts a ON a.id = t.account_id AND a.user_id = $1` + catFilter + `
				 WHERE (c.user_id = $1 OR c.user_id IS NULL)
				 GROUP BY c.id, c.name, c.color, c.icon, COALESCE(NULLIF(a.currency, ''), 'INR')
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

	incomeCatQuery := `SELECT c.id, c.name, c.color, c.icon, COALESCE(NULLIF(a.currency, ''), 'INR') AS currency,
				 COALESCE(SUM(t.amount), 0) as total, COUNT(t.id)
				 FROM categories c
				 LEFT JOIN transactions t ON t.category_id = c.id AND t.type = 'credit' AND t.user_id = $1
				 LEFT JOIN accounts a ON a.id = t.account_id AND a.user_id = $1` + catFilter + `
				 WHERE (c.user_id = $1 OR c.user_id IS NULL)
				 GROUP BY c.id, c.name, c.color, c.icon, COALESCE(NULLIF(a.currency, ''), 'INR')
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
					WHERE t.user_id = $1 AND t.account_id = $2
					  AND t.date >= $3 AND t.date <= $4
					ORDER BY ` + txnOrderByDate(false) + `
					LIMIT 10`
	recentRows, err := q.Query(ctx, recentQuery, userID, accountID, windowStart, windowEnd)
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
