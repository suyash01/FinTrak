package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"testing"

	"github.com/fintrak/backend/internal/money"
	"github.com/fintrak/backend/models"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/pashagolub/pgxmock/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newMoneyFlowTestRouter(srv *Server) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.Default()
	r.Use(testAuthMiddleware())
	r.GET("/dashboard/money-flow", srv.GetMoneyFlow)
	return r
}

// The account-currency expression, as it has to appear in the SELECT, the
// GROUP BY and the ?currency= predicate of every query that reads it. Each
// const below is a regexp for one of those three sites, and the expectations use
// them rather than a loose "a.currency" because pgxmock compares arguments with
// reflect.DeepEqual: a respelled expression binds identically and would sail
// through a loose regexp while disagreeing with the projection the same response
// was folded from.
const (
	flowCurrencyRegex     = `COALESCE\(NULLIF\(a\.currency, ''\), 'INR'\)`
	flowCurrencyProjRegex = flowCurrencyRegex + ` AS currency`
	flowCurrencyPredRegex = flowCurrencyRegex + ` = \$2`
	// A link's currency is the currency of the account the amount CASE picked,
	// which is not either alias's column on its own.
	flowLinkCurrencyRegex = `COALESCE\(NULLIF\(CASE WHEN ft\.type = 'debit' THEN fa\.currency ELSE ta\.currency END, ''\), 'INR'\)`
)

// The five stage queries, each split at the point where the ?currency= predicate
// lands: a head that ends at the statement's base WHERE, a tail that covers the
// GROUP BY, and the predicate spliced between them exactly where the handler
// splices it. The unfiltered expectations reuse the same pieces with no
// predicate, so the two sets cannot drift into describing different statements.
var (
	moneyFlowIncomeHead    = `(?s)SELECT COALESCE\(c\.id::text, ''\), COALESCE\(c\.name, 'Uncategorized'\), COALESCE\(c\.color, ''\), COALESCE\(cg\.id, ''\), COALESCE\(cg\.color, ''\), a\.id::text, a\.name, a\.color, ` + flowCurrencyProjRegex + `, COALESCE\(SUM\(t\.amount\), 0\).*t\.type = 'credit'`
	moneyFlowAcctCatHead   = `(?s)SELECT a\.id::text, a\.name, a\.color, COALESCE\(c\.id::text, ''\), COALESCE\(c\.name, 'Uncategorized'\), COALESCE\(c\.color, ''\), COALESCE\(cg\.id, ''\), COALESCE\(cg\.color, ''\), ` + flowCurrencyProjRegex + `, COALESCE\(SUM\(t\.amount\), 0\).*t\.type = 'debit'`
	moneyFlowCatPayeeHead  = `(?s)SELECT COALESCE\(c\.id::text, ''\), COALESCE\(c\.name, 'Uncategorized'\), COALESCE\(c\.color, ''\), COALESCE\(cg\.id, ''\), COALESCE\(cg\.color, ''\), COALESCE\(p\.id::text, ''\), COALESCE\(p\.name, 'No payee'\), ` + flowCurrencyProjRegex + `, COALESCE\(SUM\(t\.amount\), 0\).*t\.type = 'debit'`
	moneyFlowStageTail     = `[\s\S]*GROUP BY [\s\S]*` + flowCurrencyRegex
	moneyFlowLinksHead     = `(?s)SELECT l\.type, COUNT\(\*\), ` + flowLinkCurrencyRegex + ` AS currency, COALESCE\(SUM\(CASE WHEN ft\.type = 'debit'[\s\S]*WHERE l\.user_id = \$1`
	moneyFlowLinksTail     = `[\s\S]*GROUP BY l\.type, ` + flowLinkCurrencyRegex
	moneyFlowAcctLinksHead = `(?s)SELECT ft\.type, tt\.type, fa\.id::text, fa\.name, fa\.color, ta\.id::text, ta\.name, ta\.color, ` + flowLinkCurrencyRegex + ` AS currency, CASE WHEN ft\.type = 'debit' THEN ft\.amount ELSE tt\.amount END[\s\S]*WHERE l\.user_id = \$1`
	moneyFlowAcctLinksTail = `[\s\S]*fa\.id <> ta\.id`
)

// moneyFlowScopeRegex is the shared currencyScope statement, which supplies the
// three headline totals and the accounts behind them.
const moneyFlowScopeRegex = `FROM accounts a\s+LEFT JOIN transactions t`

// flowStageRegex assembles one stage expectation: head, the ?currency= predicate
// if the request carried one, then tail.
func flowStageRegex(head, tail, predicate string) string {
	if predicate == "" {
		return head + `[\s\S]*` + tail
	}
	return head + ` AND ` + predicate + tail
}

// expectMoneyFlowQueries wires the six expected queries for an unfiltered
// money-flow request: the currency scope and the five stages.
func expectMoneyFlowQueries(mock pgxmock.PgxPoolIface, args []any, scope, income, acctCat, catPayee, links, acctLinks *pgxmock.Rows) {
	mock.ExpectBeginTx(pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	mock.ExpectQuery(moneyFlowScopeRegex).WithArgs(args...).WillReturnRows(scope)
	mock.ExpectQuery(flowStageRegex(moneyFlowIncomeHead, moneyFlowStageTail, "")).WithArgs(args...).WillReturnRows(income)
	mock.ExpectQuery(flowStageRegex(moneyFlowAcctCatHead, moneyFlowStageTail, "")).WithArgs(args...).WillReturnRows(acctCat)
	mock.ExpectQuery(flowStageRegex(moneyFlowCatPayeeHead, moneyFlowStageTail, "")).WithArgs(args...).WillReturnRows(catPayee)
	mock.ExpectQuery(flowStageRegex(moneyFlowLinksHead, moneyFlowLinksTail, "")).WithArgs(args...).WillReturnRows(links)
	mock.ExpectQuery(flowStageRegex(moneyFlowAcctLinksHead, moneyFlowAcctLinksTail, "")).WithArgs(args...).WillReturnRows(acctLinks)
	mock.ExpectCommit()
}

func findFlowNode(nodes []models.MoneyFlowNode, id string) *models.MoneyFlowNode {
	for i := range nodes {
		if nodes[i].ID == id {
			return &nodes[i]
		}
	}
	return nil
}

func findFlowEdge(edges []models.MoneyFlowEdge, source, target string) *models.MoneyFlowEdge {
	for i := range edges {
		if edges[i].Source == source && edges[i].Target == target {
			return &edges[i]
		}
	}
	return nil
}

func TestGetMoneyFlow(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()
	srv := newTestServer(mock)
	r := newMoneyFlowTestRouter(srv)
	userID := testUserID()

	catID := uuid.New()
	acctID := uuid.New()
	acct2ID := uuid.New()
	payeeID := uuid.New()

	scope := pgxmock.NewRows([]string{"id", "name", "currency", "income", "expense"}).
		AddRow(acctID, "Checking", "INR", money.FromFloat(50000.00), money.FromFloat(12000.00))
	income := pgxmock.NewRows([]string{"cat_id", "cat_name", "cat_color", "group_id", "group_color", "acct_id", "acct_name", "acct_color", "currency", "total"}).
		AddRow(catID.String(), "Salary", "#22c55e", "income", "#22c55e", acctID.String(), "Checking", "#3b82f6", "INR", money.FromFloat(50000.00))
	acctCat := pgxmock.NewRows([]string{"acct_id", "acct_name", "acct_color", "cat_id", "cat_name", "cat_color", "group_id", "group_color", "currency", "total"}).
		AddRow(acctID.String(), "Checking", "#3b82f6", catID.String(), "Food", "#f97316", "expense", "#f97316", "INR", money.FromFloat(12000.00))
	catPayee := pgxmock.NewRows([]string{"cat_id", "cat_name", "cat_color", "group_id", "group_color", "payee_id", "payee_name", "currency", "total"}).
		AddRow(catID.String(), "Food", "#f97316", "expense", "#f97316", payeeID.String(), "Zomato", "INR", money.FromFloat(12000.00))
	links := pgxmock.NewRows([]string{"type", "count", "currency", "total"}).
		AddRow("transfer", 2, "INR", money.FromFloat(30000.00))
	// A cross-account transfer: Checking (debit) -> Savings (credit).
	acctLinks := pgxmock.NewRows([]string{"from_type", "to_type", "fa_id", "fa_name", "fa_color", "ta_id", "ta_name", "ta_color", "currency", "amount"}).
		AddRow("debit", "credit", acctID.String(), "Checking", "#3b82f6", acct2ID.String(), "Savings", "#22c55e", "INR", money.FromFloat(30000.00))

	expectMoneyFlowQueries(mock, []any{userID}, scope, income, acctCat, catPayee, links, acctLinks)

	req, _ := http.NewRequest(http.MethodGet, "/dashboard/money-flow", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	var graph models.MoneyFlowGraph
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &graph))

	assert.Equal(t, models.CurrencyAmounts{"INR": money.FromFloat(50000.00)}, graph.TotalIncome)
	assert.Equal(t, models.CurrencyAmounts{"INR": money.FromFloat(12000.00)}, graph.TotalExpense)
	assert.Equal(t, models.CurrencyAmounts{"INR": money.FromFloat(38000.00)}, graph.TotalNet)
	assert.Equal(t, []string{"INR"}, graph.CurrencyScope.Currencies)

	incomeNode := findFlowNode(graph.Nodes, "income:"+catID.String())
	require.NotNil(t, incomeNode)
	assert.Equal(t, "Salary", incomeNode.Name)
	assert.Equal(t, "#22c55e", incomeNode.Color)
	assert.Equal(t, models.CurrencyAmounts{"INR": money.FromFloat(50000.00)}, incomeNode.Total)

	acctNode := findFlowNode(graph.Nodes, "account:"+acctID.String())
	require.NotNil(t, acctNode)
	// The account carries the larger of its inflow (50000) and outflow (12000).
	assert.Equal(t, models.CurrencyAmounts{"INR": money.FromFloat(50000.00)}, acctNode.Total)

	catNode := findFlowNode(graph.Nodes, "category:"+catID.String())
	require.NotNil(t, catNode)
	// Category nodes are painted with the base-group color.
	assert.Equal(t, "#f97316", catNode.Color)
	assert.Equal(t, "expense", catNode.Group)

	payeeNode := findFlowNode(graph.Nodes, "payee:"+payeeID.String())
	require.NotNil(t, payeeNode)
	assert.Equal(t, "Zomato", payeeNode.Name)

	// The cross-account transfer is drawn as a real account-to-account edge,
	// and the destination account (which has no other activity) gets a node.
	savings := findFlowNode(graph.Nodes, "account:"+acct2ID.String())
	require.NotNil(t, savings)
	assert.Equal(t, "Savings", savings.Name)
	assert.Equal(t, models.CurrencyAmounts{"INR": money.FromFloat(30000.00)}, savings.Total)
	require.Len(t, graph.Links, 4)
	acctEdge := findFlowEdge(graph.Links, "account:"+acctID.String(), "account:"+acct2ID.String())
	require.NotNil(t, acctEdge)
	assert.Equal(t, models.CurrencyAmounts{"INR": money.FromFloat(30000.00)}, acctEdge.Value)

	assert.Len(t, graph.LinkSummary, 1)
	assert.Equal(t, "transfer", graph.LinkSummary[0].Type)
	assert.Equal(t, 2, graph.LinkSummary[0].Count)
	assert.Equal(t, models.CurrencyAmounts{"INR": money.FromFloat(30000.00)}, graph.LinkSummary[0].Total)

	assert.NoError(t, mock.ExpectationsWereMet())
}

// The bug this endpoint had: with no account filter, a window over a USD
// account and an INR account added dollars into the same totals the Sankey
// draws, so every node total and every edge width carried that one wrong number.
// The response has to refuse the total rather than report it.
func TestGetMoneyFlowKeepsCurrenciesApartInTheGraph(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()
	srv := newTestServer(mock)
	r := newMoneyFlowTestRouter(srv)
	userID := testUserID()

	acctINR := uuid.New()
	acctUSD := uuid.New()
	acctINR2 := uuid.New()
	incomeCat, spendCat, payeeID := uuid.NewString(), uuid.NewString(), uuid.NewString()

	scope := pgxmock.NewRows([]string{"id", "name", "currency", "income", "expense"}).
		AddRow(acctINR, "Salary", "INR", money.FromFloat(50000), money.FromFloat(12000)).
		AddRow(acctUSD, "Travel card", "USD", 0, money.FromFloat(300))
	income := pgxmock.NewRows([]string{"cat_id", "cat_name", "cat_color", "group_id", "group_color", "acct_id", "acct_name", "acct_color", "currency", "total"}).
		AddRow(incomeCat, "Consulting", "#22c55e", "income", "#22c55e", acctINR.String(), "Salary", "#3b82f6", "INR", money.FromFloat(50000))
	acctCat := pgxmock.NewRows([]string{"acct_id", "acct_name", "acct_color", "cat_id", "cat_name", "cat_color", "group_id", "group_color", "currency", "total"}).
		AddRow(acctINR.String(), "Salary", "#3b82f6", spendCat, "Travel", "#f97316", "expense", "#f97316", "INR", money.FromFloat(12000)).
		AddRow(acctUSD.String(), "Travel card", "#0ea5e9", spendCat, "Travel", "#f97316", "expense", "#f97316", "USD", money.FromFloat(300))
	// The same category paid from both accounts, and the same payee: the
	// category→payee query is the one that used to merge them into a single row
	// in the database, before any map existed to keep them apart.
	catPayee := pgxmock.NewRows([]string{"cat_id", "cat_name", "cat_color", "group_id", "group_color", "payee_id", "payee_name", "currency", "total"}).
		AddRow(spendCat, "Travel", "#f97316", "expense", "#f97316", payeeID, "Airline", "INR", money.FromFloat(12000)).
		AddRow(spendCat, "Travel", "#f97316", "expense", "#f97316", payeeID, "Airline", "USD", money.FromFloat(300))
	// One transfer type whose two links are in different currencies: the summary
	// is one entry holding both, not two entries.
	links := pgxmock.NewRows([]string{"type", "count", "currency", "total"}).
		AddRow("transfer", 1, "INR", money.FromFloat(500)).
		AddRow("transfer", 1, "USD", money.FromFloat(50))
	// A reciprocal rupee pair between two accounts, netted into one leg.
	acctLinks := pgxmock.NewRows([]string{"from_type", "to_type", "fa_id", "fa_name", "fa_color", "ta_id", "ta_name", "ta_color", "currency", "amount"}).
		AddRow("debit", "credit", acctINR.String(), "Salary", "#3b82f6", acctINR2.String(), "Savings", "#22c55e", "INR", money.FromFloat(500)).
		AddRow("debit", "credit", acctINR2.String(), "Savings", "#22c55e", acctINR.String(), "Salary", "#3b82f6", "INR", money.FromFloat(200))

	expectMoneyFlowQueries(mock, []any{userID}, scope, income, acctCat, catPayee, links, acctLinks)

	req, _ := http.NewRequest(http.MethodGet, "/dashboard/money-flow", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	var graph models.MoneyFlowGraph
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &graph))

	assert.Equal(t, models.CurrencyAmounts{"INR": money.FromFloat(50000)}, graph.TotalIncome)
	assert.Equal(t, models.CurrencyAmounts{"INR": money.FromFloat(12000), "USD": money.FromFloat(300)}, graph.TotalExpense)
	assert.Equal(t, models.CurrencyAmounts{"INR": money.FromFloat(38000), "USD": -money.FromFloat(300)}, graph.TotalNet)
	assert.Equal(t, []string{"INR", "USD"}, graph.CurrencyScope.Currencies)
	assert.Len(t, graph.CurrencyScope.Accounts, 2)

	// The category and the payee are one node each, fed from both accounts, and
	// each holds the two currencies side by side rather than one sum.
	catNode := findFlowNode(graph.Nodes, "category:"+spendCat)
	require.NotNil(t, catNode)
	assert.Equal(t, models.CurrencyAmounts{"INR": money.FromFloat(12000), "USD": money.FromFloat(300)}, catNode.Total)

	payeeNode := findFlowNode(graph.Nodes, "payee:"+payeeID)
	require.NotNil(t, payeeNode)
	assert.Equal(t, models.CurrencyAmounts{"INR": money.FromFloat(12000), "USD": money.FromFloat(300)}, payeeNode.Total)

	// Each account node holds only its own currency: the volume the money that
	// passed through it, in the currency it is denominated in.
	salaryNode := findFlowNode(graph.Nodes, "account:"+acctINR.String())
	require.NotNil(t, salaryNode)
	assert.Equal(t, models.CurrencyAmounts{"INR": money.FromFloat(50000)}, salaryNode.Total)
	cardNode := findFlowNode(graph.Nodes, "account:"+acctUSD.String())
	require.NotNil(t, cardNode)
	assert.Equal(t, models.CurrencyAmounts{"USD": money.FromFloat(300)}, cardNode.Total)
	savingsNode := findFlowNode(graph.Nodes, "account:"+acctINR2.String())
	require.NotNil(t, savingsNode)
	assert.Equal(t, models.CurrencyAmounts{"INR": money.FromFloat(300)}, savingsNode.Total)

	// The category→payee edge is the one the database used to merge: it carries
	// both currencies, each only what its own account contributed.
	catEdge := findFlowEdge(graph.Links, "category:"+spendCat, "payee:"+payeeID)
	require.NotNil(t, catEdge)
	assert.Equal(t, models.CurrencyAmounts{"INR": money.FromFloat(12000), "USD": money.FromFloat(300)}, catEdge.Value)

	// Two account-to-category edges, one income edge, one category→payee edge
	// and the netted transfer.
	require.Len(t, graph.Links, 5)
	transfer := findFlowEdge(graph.Links, "account:"+acctINR.String(), "account:"+acctINR2.String())
	require.NotNil(t, transfer)
	assert.Equal(t, models.CurrencyAmounts{"INR": money.FromFloat(300)}, transfer.Value)

	require.Len(t, graph.LinkSummary, 1)
	assert.Equal(t, "transfer", graph.LinkSummary[0].Type)
	assert.Equal(t, 2, graph.LinkSummary[0].Count)
	assert.Equal(t, models.CurrencyAmounts{"INR": money.FromFloat(500), "USD": money.FromFloat(50)}, graph.LinkSummary[0].Total)

	// Every amount in the body is a currency-keyed object: not a bare number, and
	// not an absent field either, which an "is it a float?" check would wave
	// through while the name quietly stopped being sent.
	var raw map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &raw))
	for _, field := range []string{"totalIncome", "totalExpense", "totalNet"} {
		if amounts, isObject := raw[field].(map[string]any); !isObject || len(amounts) == 0 {
			t.Errorf("%s = %v, want a currency-keyed object", field, raw[field])
		}
	}

	assert.NoError(t, mock.ExpectationsWereMet())
}

// A link between accounts in different currencies carries the currency of the
// account its amount was taken from — the only one of the two that is true of
// that amount — rather than converting it. The consequence is visible on the
// receiving account's node: it holds the arriving amount under the sender's
// currency alongside its own, because that is what the ledger recorded. The point
// is that the two are never added: there is no key that holds both.
func TestBuildMoneyFlowGraphCarriesACrossCurrencyLinkInTheAmountsCurrency(t *testing.T) {
	acctINR, acctUSD := uuid.NewString(), uuid.NewString()

	graph := buildMoneyFlowGraph(nil, []flowAcctCatRow{
		{acctID: acctUSD, acctName: "Travel card", currency: "USD", total: money.FromFloat(300)},
	}, nil, nil, []flowAcctLinkRow{{
		fromType: "debit", toType: "credit",
		fromAcctID: acctINR, fromAcctName: "Salary",
		toAcctID: acctUSD, toAcctName: "Travel card",
		currency: "INR", amount: money.FromFloat(500),
	}}, 12)

	leg := findFlowEdge(graph.Links, "account:"+acctINR, "account:"+acctUSD)
	require.NotNil(t, leg)
	assert.Equal(t, models.CurrencyAmounts{"INR": money.FromFloat(500)}, leg.Value)

	receiver := findFlowNode(graph.Nodes, "account:"+acctUSD)
	require.NotNil(t, receiver)
	assert.Equal(t, models.CurrencyAmounts{"INR": money.FromFloat(500), "USD": money.FromFloat(300)}, receiver.Total)
}

// A ?currency= filter that reached only the headline totals would leave the
// nodes and edges describing a wider window than the figures beside them: the
// client draws a rupee-width band beside a dollar figure and neither is wrong on
// its own. So the filter has to reach every stage query, and this pins that it
// does — in the exact expression the projection uses, not merely as a bound
// argument, because pgxmock's arg comparison is by value.
//
// The row values are what a filtered database returns: a mock matches the
// statement, it does not execute it. Drop the predicate from any one stage and
// its expectation stops matching, the handler answers 500, and this fails.
func TestGetMoneyFlowNarrowsEveryStageToTheCurrencyFilter(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()
	srv := newTestServer(mock)
	r := newMoneyFlowTestRouter(srv)
	userID := testUserID()
	acctUSD := uuid.NewString()
	catID, payeeID := uuid.NewString(), uuid.NewString()

	empty := func(cols ...string) *pgxmock.Rows { return pgxmock.NewRows(cols) }

	mock.ExpectBeginTx(pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	// 1. The scope, carrying the same expression as its own WHERE.
	mock.ExpectQuery(moneyFlowScopeRegex+`[\s\S]*`+flowCurrencyPredRegex).
		WithArgs(userID, "USD").
		WillReturnRows(pgxmock.NewRows([]string{"id", "name", "currency", "income", "expense"}).
			AddRow(acctUSD, "Travel card", "USD", money.FromFloat(120), money.FromFloat(80)))
	// 2. The income stage.
	mock.ExpectQuery(flowStageRegex(moneyFlowIncomeHead, moneyFlowStageTail, flowCurrencyPredRegex)).
		WithArgs(userID, "USD").
		WillReturnRows(pgxmock.NewRows([]string{"cat_id", "cat_name", "cat_color", "group_id", "group_color", "acct_id", "acct_name", "acct_color", "currency", "total"}).
			AddRow(catID, "Consulting", "#22c55e", "income", "#22c55e", acctUSD, "Travel card", "#0ea5e9", "USD", money.FromFloat(120)))
	// 3. The account→category stage.
	mock.ExpectQuery(flowStageRegex(moneyFlowAcctCatHead, moneyFlowStageTail, flowCurrencyPredRegex)).
		WithArgs(userID, "USD").
		WillReturnRows(pgxmock.NewRows([]string{"acct_id", "acct_name", "acct_color", "cat_id", "cat_name", "cat_color", "group_id", "group_color", "currency", "total"}).
			AddRow(acctUSD, "Travel card", "#0ea5e9", catID, "Travel", "#f97316", "expense", "#f97316", "USD", money.FromFloat(80)))
	// 4. The category→payee stage.
	mock.ExpectQuery(flowStageRegex(moneyFlowCatPayeeHead, moneyFlowStageTail, flowCurrencyPredRegex)).
		WithArgs(userID, "USD").
		WillReturnRows(pgxmock.NewRows([]string{"cat_id", "cat_name", "cat_color", "group_id", "group_color", "payee_id", "payee_name", "currency", "total"}).
			AddRow(catID, "Travel", "#f97316", "expense", "#f97316", payeeID, "Airline", "USD", money.FromFloat(80)))
	// 5/6. Both link queries narrow on the currency of the link's value, which is
	// the account the amount CASE picked and not either endpoint's column.
	mock.ExpectQuery(flowStageRegex(moneyFlowLinksHead, moneyFlowLinksTail, flowLinkCurrencyRegex+` = \$2`)).
		WithArgs(userID, "USD").
		WillReturnRows(pgxmock.NewRows([]string{"type", "count", "currency", "total"}).
			AddRow("transfer", 1, "USD", money.FromFloat(40)))
	mock.ExpectQuery(flowStageRegex(moneyFlowAcctLinksHead, moneyFlowAcctLinksTail, flowLinkCurrencyRegex+` = \$2`)).
		WithArgs(userID, "USD").
		WillReturnRows(empty("from_type", "to_type", "fa_id", "fa_name", "fa_color", "ta_id", "ta_name", "ta_color", "currency", "amount"))
	mock.ExpectCommit()

	// Lower case on purpose: the bound argument is the normalised "USD", so a
	// regression that forwarded the raw parameter would match nothing and report
	// a quiet month with no error anywhere.
	req, _ := http.NewRequest(http.MethodGet, "/dashboard/money-flow?currency=usd", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	var graph models.MoneyFlowGraph
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &graph))

	assert.Equal(t, models.CurrencyAmounts{"USD": money.FromFloat(120)}, graph.TotalIncome)
	assert.Equal(t, models.CurrencyAmounts{"USD": money.FromFloat(80)}, graph.TotalExpense)
	assert.Equal(t, models.CurrencyAmounts{"USD": money.FromFloat(40)}, graph.TotalNet)
	assert.Equal(t, []string{"USD"}, graph.CurrencyScope.Currencies)

	// Every amount in the response is single-currency, so a client can read all
	// of them as plain numbers without ever combining two of them.
	amounts := map[string]models.CurrencyAmounts{
		"totalIncome":  graph.TotalIncome,
		"totalExpense": graph.TotalExpense,
		"totalNet":     graph.TotalNet,
	}
	for _, n := range graph.Nodes {
		amounts["node "+n.ID] = n.Total
	}
	for _, e := range graph.Links {
		amounts["edge "+e.Source+"->"+e.Target] = e.Value
	}
	for _, s := range graph.LinkSummary {
		amounts["linkSummary "+s.Type] = s.Total
	}
	for name, got := range amounts {
		if code, _, ok := got.Single(); !ok || code != "USD" {
			t.Errorf("%s = %v, want a single USD amount", name, got)
		}
	}

	assert.NoError(t, mock.ExpectationsWereMet())
}

// A code that is not three ASCII letters would bind against a currency no
// account holds and report a silent, empty month. It is a 400 instead, and the
// handler must reject it before it opens a transaction.
func TestGetMoneyFlowRejectsMalformedCurrency(t *testing.T) {
	srv, _ := newMockServer(t)
	r := newMoneyFlowTestRouter(srv)

	for _, currency := range []string{"US", "usdd", "12"} {
		t.Run(currency, func(t *testing.T) {
			req, _ := http.NewRequest(http.MethodGet, "/dashboard/money-flow?currency="+currency, nil)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			assert.Equal(t, http.StatusBadRequest, w.Code)
			assert.Contains(t, w.Body.String(), "invalid currency")
		})
	}
}

func TestGetMoneyFlowWithFilters(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()
	srv := newTestServer(mock)
	r := newMoneyFlowTestRouter(srv)
	userID := testUserID()

	accountID := uuid.New()
	dateFrom, dateTo := "2026-07-01", "2026-07-31"
	// The first three queries bind the filter once each.
	simpleArgs := []any{userID, dateFrom, dateTo, accountID.String()}
	// The link query binds the filter twice, once per endpoint.
	linkArgs := []any{userID, dateFrom, dateTo, accountID.String(), dateFrom, dateTo, accountID.String()}

	empty := func(cols ...string) *pgxmock.Rows { return pgxmock.NewRows(cols) }
	scope := empty("id", "name", "currency", "income", "expense")
	income := empty("cat_id", "cat_name", "cat_color", "group_id", "group_color", "acct_id", "acct_name", "acct_color", "currency", "total")
	acctCat := empty("acct_id", "acct_name", "acct_color", "cat_id", "cat_name", "cat_color", "group_id", "group_color", "currency", "total")
	catPayee := empty("cat_id", "cat_name", "cat_color", "group_id", "group_color", "payee_id", "payee_name", "currency", "total")
	links := empty("type", "count", "currency", "total")
	acctLinks := empty("from_type", "to_type", "fa_id", "fa_name", "fa_color", "ta_id", "ta_name", "ta_color", "currency", "amount")

	mock.ExpectBeginTx(pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	// The stage regexes deliberately include the "AND" that joins the base
	// predicate to the filter, so a missing separator (which the DB would reject
	// as a syntax error) fails the test.
	mock.ExpectQuery(`FROM accounts a\s+LEFT JOIN transactions t[\s\S]*a\.id = \$4`).
		WithArgs(simpleArgs...).WillReturnRows(scope)
	mock.ExpectQuery(`(?s)t\.type = 'credit' AND t\.date >=`).WithArgs(simpleArgs...).WillReturnRows(income)
	mock.ExpectQuery(`(?s)SELECT a\.id::text.*t\.type = 'debit' AND t\.date >=`).WithArgs(simpleArgs...).WillReturnRows(acctCat)
	mock.ExpectQuery(`(?s)payees p ON t\.payee_id.*t\.type = 'debit' AND t\.date >=`).WithArgs(simpleArgs...).WillReturnRows(catPayee)
	mock.ExpectQuery("SELECT l.type, COUNT").WithArgs(linkArgs...).WillReturnRows(links)
	mock.ExpectQuery("fa.id <> ta.id").WithArgs(linkArgs...).WillReturnRows(acctLinks)
	mock.ExpectCommit()

	req, _ := http.NewRequest(http.MethodGet,
		"/dashboard/money-flow?dateFrom="+dateFrom+"&dateTo="+dateTo+"&accountId="+accountID.String(), nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetMoneyFlowInvalidAccount(t *testing.T) {
	srv, _ := newMockServer(t)
	r := newMoneyFlowTestRouter(srv)

	req, _ := http.NewRequest(http.MethodGet, "/dashboard/money-flow?accountId=not-a-uuid", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestGetMoneyFlowQueryError(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()
	srv := newTestServer(mock)
	r := newMoneyFlowTestRouter(srv)

	mock.ExpectBeginTx(pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	mock.ExpectQuery(moneyFlowScopeRegex).WithArgs(testUserID()).WillReturnError(assert.AnError)

	req, _ := http.NewRequest(http.MethodGet, "/dashboard/money-flow", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// The stage queries run after the scope query, so a failure in one of them is
// still a 500 rather than a partial graph.
func TestGetMoneyFlowStageQueryError(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()
	srv := newTestServer(mock)
	r := newMoneyFlowTestRouter(srv)

	mock.ExpectBeginTx(pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	mock.ExpectQuery(moneyFlowScopeRegex).WithArgs(testUserID()).WillReturnRows(
		pgxmock.NewRows([]string{"id", "name", "currency", "income", "expense"}))
	mock.ExpectQuery(flowStageRegex(moneyFlowIncomeHead, moneyFlowStageTail, "")).WithArgs(testUserID()).WillReturnError(assert.AnError)

	req, _ := http.NewRequest(http.MethodGet, "/dashboard/money-flow", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestBuildMoneyFlowGraphRollup checks that stages over the node cap collapse
// their smallest nodes into one "Other" node and that the edges are remapped
// through the same rollup. The tail spans two currencies, so the rollup has to
// carry both rather than the single number the stage used to sum to.
func TestBuildMoneyFlowGraphRollup(t *testing.T) {
	catA, catB, catC := uuid.NewString(), uuid.NewString(), uuid.NewString()
	acctID, payeeID := uuid.NewString(), uuid.NewString()

	rows := []flowCatPayeeRow{
		{catID: catA, catName: "A", groupID: "expense", groupColor: "#111111", payeeID: payeeID, payeeName: "P", currency: "INR", total: money.FromFloat(100)},
		{catID: catB, catName: "B", groupID: "expense", groupColor: "#222222", payeeID: payeeID, payeeName: "P", currency: "INR", total: money.FromFloat(50)},
		{catID: catC, catName: "C", groupID: "expense", groupColor: "#333333", payeeID: payeeID, payeeName: "P", currency: "USD", total: money.FromFloat(25)},
	}

	graph := buildMoneyFlowGraph(nil, []flowAcctCatRow{
		{acctID: acctID, acctName: "Checking", catID: catA, groupID: "expense", currency: "INR", total: money.FromFloat(100)},
		{acctID: acctID, acctName: "Checking", catID: catB, groupID: "expense", currency: "INR", total: money.FromFloat(50)},
		{acctID: acctID, acctName: "Checking", catID: catC, groupID: "expense", currency: "USD", total: money.FromFloat(25)},
	}, rows, nil, nil, 1)

	other := findFlowNode(graph.Nodes, "category:other")
	require.NotNil(t, other)
	assert.Equal(t, models.CurrencyAmounts{"INR": money.FromFloat(50), "USD": money.FromFloat(25)}, other.Total)
	assert.Equal(t, "Other categories", other.Name)

	assert.NotNil(t, findFlowNode(graph.Nodes, "category:"+catA))
	assert.Nil(t, findFlowNode(graph.Nodes, "category:"+catB))

	// Every category flows into the single payee: two account->category edges
	// and two category->payee edges (the kept category plus the rolled-up one).
	assert.Len(t, graph.Links, 4)
	otherEdge := findFlowEdge(graph.Links, "category:other", "payee:"+payeeID)
	require.NotNil(t, otherEdge)
	assert.Equal(t, models.CurrencyAmounts{"INR": money.FromFloat(50), "USD": money.FromFloat(25)}, otherEdge.Value)

	// The account spent from two currencies: its node holds both, each the
	// larger of that currency's own inflow and outflow.
	acctNode := findFlowNode(graph.Nodes, "account:"+acctID)
	require.NotNil(t, acctNode)
	assert.Equal(t, models.CurrencyAmounts{"INR": money.FromFloat(150), "USD": money.FromFloat(25)}, acctNode.Total)
}

func TestAccountFlowEdgesDerivesDirectionAndSkipsSameAccount(t *testing.T) {
	a, b := uuid.NewString(), uuid.NewString()

	edges := accountFlowEdges([]flowAcctLinkRow{
		// Stored credit -> debit: money still flows debit (B) -> credit (A).
		{fromType: "credit", toType: "debit",
			fromAcctID: a, fromAcctName: "A", fromAcctColor: "#111",
			toAcctID: b, toAcctName: "B", toAcctColor: "#222",
			currency: "INR", amount: money.FromFloat(50)},
		// Same account on both ends: not an account-to-account flow.
		{fromType: "debit", toType: "credit",
			fromAcctID: a, fromAcctName: "A", toAcctID: a, toAcctName: "A", amount: money.FromFloat(10)},
	})

	require.Len(t, edges, 1)
	assert.Equal(t, b, edges[0].srcID)
	assert.Equal(t, a, edges[0].dstID)
	assert.Equal(t, models.CurrencyAmounts{"INR": money.FromFloat(50)}, edges[0].value)
}

func TestAccountFlowEdgesNetsReciprocalPairs(t *testing.T) {
	a, b := uuid.NewString(), uuid.NewString()

	edges := accountFlowEdges([]flowAcctLinkRow{
		{fromType: "debit", toType: "credit", fromAcctID: a, fromAcctName: "A", toAcctID: b, toAcctName: "B", currency: "INR", amount: money.FromFloat(100)},
		{fromType: "debit", toType: "credit", fromAcctID: b, fromAcctName: "B", toAcctID: a, toAcctName: "A", currency: "INR", amount: money.FromFloat(40)},
	})

	require.Len(t, edges, 1)
	assert.Equal(t, a, edges[0].srcID)
	assert.Equal(t, b, edges[0].dstID)
	assert.Equal(t, models.CurrencyAmounts{"INR": money.FromFloat(60)}, edges[0].value)
}

// A reciprocal pair in two different currencies cannot be netted into one edge:
// which side is dominant is not a question a rupee and a dollar can answer. So
// netting cancels each currency separately, and whatever it cannot cancel is left
// as a two-way flow — which the cycle-breaking pass then hides, because a
// Sankey cannot draw a cycle. Both facts have to be visible: the graph shows one
// leg, and the cycles report carries the pair with its gross, per-currency legs.
func TestAccountFlowEdgesKeepsReciprocalPairsInDifferentCurrenciesApart(t *testing.T) {
	a, b := uuid.NewString(), uuid.NewString()
	// Force a < b so which side survives the cycle-breaking is stable: the DFS
	// starts at the lexicographically first account.
	ids := []string{a, b}
	sort.Strings(ids)
	a, b = ids[0], ids[1]

	edges, cycles := analyzeAccountFlows([]flowAcctLinkRow{
		{fromType: "debit", toType: "credit", fromAcctID: a, fromAcctName: "A", toAcctID: b, toAcctName: "B", currency: "INR", amount: money.FromFloat(100)},
		{fromType: "debit", toType: "credit", fromAcctID: b, fromAcctName: "B", toAcctID: a, toAcctName: "A", currency: "INR", amount: money.FromFloat(40)},
		{fromType: "debit", toType: "credit", fromAcctID: b, fromAcctName: "B", toAcctID: a, toAcctName: "A", currency: "USD", amount: money.FromFloat(70)},
	})

	// The rupee leg is netted down to its dominant side; the dollar leg cannot
	// cancel anything, and what is left of the pair is a cycle, so only one
	// direction is drawn.
	require.Len(t, edges, 1)
	assert.Equal(t, a, edges[0].srcID)
	assert.Equal(t, b, edges[0].dstID)
	assert.Equal(t, models.CurrencyAmounts{"INR": money.FromFloat(60)}, edges[0].value)

	// The pair is reported once, with the gross legs netting left untouched and
	// each in its own currency.
	require.Len(t, cycles, 1)
	assert.Equal(t, "reciprocal", cycles[0].kind)
	require.Len(t, cycles[0].legs, 2)
	assert.Equal(t, models.CurrencyAmounts{"INR": money.FromFloat(100)}, cycles[0].legs[0].value)
	assert.Equal(t, models.CurrencyAmounts{"INR": money.FromFloat(40), "USD": money.FromFloat(70)}, cycles[0].legs[1].value)
}

func TestAccountFlowEdgesBreaksCyclesDeterministically(t *testing.T) {
	a, b, c := uuid.NewString(), uuid.NewString(), uuid.NewString()
	// Force a < b < c ordering so the expected kept edges are stable.
	ids := []string{a, b, c}
	sort.Strings(ids)
	a, b, c = ids[0], ids[1], ids[2]

	edges := accountFlowEdges([]flowAcctLinkRow{
		{fromType: "debit", toType: "credit", fromAcctID: a, fromAcctName: "A", toAcctID: b, toAcctName: "B", currency: "INR", amount: money.FromFloat(100)},
		{fromType: "debit", toType: "credit", fromAcctID: b, fromAcctName: "B", toAcctID: c, toAcctName: "C", currency: "INR", amount: money.FromFloat(100)},
		{fromType: "debit", toType: "credit", fromAcctID: c, fromAcctName: "C", toAcctID: a, toAcctName: "A", currency: "INR", amount: money.FromFloat(100)},
	})

	// The 3-cycle loses exactly one (back) edge, leaving a 2-edge DAG.
	require.Len(t, edges, 2)
	assert.Equal(t, a, edges[0].srcID)
	assert.Equal(t, b, edges[0].dstID)
	assert.Equal(t, models.CurrencyAmounts{"INR": money.FromFloat(100)}, edges[0].value)
	assert.Equal(t, b, edges[1].srcID)
	assert.Equal(t, c, edges[1].dstID)
}

// The date window is compared against a date column, so a malformed bound must
// be a 400 rather than a Postgres parse error surfacing as a 500.
func TestGetMoneyFlowRejectsMalformedDates(t *testing.T) {
	srv, _ := newMockServer(t)
	r := newMoneyFlowTestRouter(srv)

	for _, query := range []string{"dateFrom=2024-1-5", "dateTo=not-a-date"} {
		t.Run(query, func(t *testing.T) {
			req, _ := http.NewRequest(http.MethodGet, "/dashboard/money-flow?"+query, nil)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			assert.Equal(t, http.StatusBadRequest, w.Code)
			assert.Contains(t, w.Body.String(), "must be YYYY-MM-DD")
		})
	}
}
