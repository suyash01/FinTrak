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

// moneyFlowRows wires the five expected queries for a money-flow request.
func expectMoneyFlowQueries(mock pgxmock.PgxPoolIface, args []any, income, acctCat, catPayee, links, acctLinks *pgxmock.Rows) {
	mock.ExpectBeginTx(pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	mock.ExpectQuery("t.type = 'credit'").WithArgs(args...).WillReturnRows(income)
	mock.ExpectQuery("SELECT a.id::text, a.name, a.color").WithArgs(args...).WillReturnRows(acctCat)
	mock.ExpectQuery("payees p ON t.payee_id").WithArgs(args...).WillReturnRows(catPayee)
	mock.ExpectQuery("SELECT l.type, COUNT").WithArgs(args...).WillReturnRows(links)
	mock.ExpectQuery("fa.id <> ta.id").WithArgs(args...).WillReturnRows(acctLinks)
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

	income := pgxmock.NewRows([]string{"cat_id", "cat_name", "cat_color", "group_id", "group_color", "acct_id", "acct_name", "acct_color", "total"}).
		AddRow(catID.String(), "Salary", "#22c55e", "income", "#22c55e", acctID.String(), "Checking", "#3b82f6", 50000.00)
	acctCat := pgxmock.NewRows([]string{"acct_id", "acct_name", "acct_color", "cat_id", "cat_name", "cat_color", "group_id", "group_color", "total"}).
		AddRow(acctID.String(), "Checking", "#3b82f6", catID.String(), "Food", "#f97316", "expense", "#f97316", 12000.00)
	catPayee := pgxmock.NewRows([]string{"cat_id", "cat_name", "cat_color", "group_id", "group_color", "payee_id", "payee_name", "total"}).
		AddRow(catID.String(), "Food", "#f97316", "expense", "#f97316", payeeID.String(), "Zomato", 12000.00)
	links := pgxmock.NewRows([]string{"type", "count", "total"}).
		AddRow("transfer", 2, 30000.00)
	// A cross-account transfer: Checking (debit) -> Savings (credit).
	acctLinks := pgxmock.NewRows([]string{"from_type", "to_type", "fa_id", "fa_name", "fa_color", "ta_id", "ta_name", "ta_color", "amount"}).
		AddRow("debit", "credit", acctID.String(), "Checking", "#3b82f6", acct2ID.String(), "Savings", "#22c55e", 30000.00)

	expectMoneyFlowQueries(mock, []any{userID}, income, acctCat, catPayee, links, acctLinks)

	req, _ := http.NewRequest(http.MethodGet, "/dashboard/money-flow", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	var graph models.MoneyFlowGraph
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &graph))

	assert.Equal(t, money.FromFloat(50000.00), graph.TotalIncome)
	assert.Equal(t, money.FromFloat(12000.00), graph.TotalExpense)

	incomeNode := findFlowNode(graph.Nodes, "income:"+catID.String())
	require.NotNil(t, incomeNode)
	assert.Equal(t, "Salary", incomeNode.Name)
	assert.Equal(t, "#22c55e", incomeNode.Color)
	assert.Equal(t, money.FromFloat(50000.00), incomeNode.Total)

	acctNode := findFlowNode(graph.Nodes, "account:"+acctID.String())
	require.NotNil(t, acctNode)
	// The account carries the larger of its inflow (50000) and outflow (12000).
	assert.Equal(t, money.FromFloat(50000.00), acctNode.Total)

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
	assert.Equal(t, money.FromFloat(30000.00), savings.Total)
	require.Len(t, graph.Links, 4)
	var acctEdge *models.MoneyFlowEdge
	for i := range graph.Links {
		if graph.Links[i].Source == "account:"+acctID.String() && graph.Links[i].Target == "account:"+acct2ID.String() {
			acctEdge = &graph.Links[i]
		}
	}
	require.NotNil(t, acctEdge)
	assert.Equal(t, money.FromFloat(30000.00), acctEdge.Value)

	assert.Len(t, graph.LinkSummary, 1)
	assert.Equal(t, "transfer", graph.LinkSummary[0].Type)
	assert.Equal(t, 2, graph.LinkSummary[0].Count)

	assert.NoError(t, mock.ExpectationsWereMet())
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
	income := empty("cat_id", "cat_name", "cat_color", "group_id", "group_color", "acct_id", "acct_name", "acct_color", "total")
	acctCat := empty("acct_id", "acct_name", "acct_color", "cat_id", "cat_name", "cat_color", "group_id", "group_color", "total")
	catPayee := empty("cat_id", "cat_name", "cat_color", "group_id", "group_color", "payee_id", "payee_name", "total")
	links := empty("type", "count", "total")
	acctLinks := empty("from_type", "to_type", "fa_id", "fa_name", "fa_color", "ta_id", "ta_name", "ta_color", "amount")

	mock.ExpectBeginTx(pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	// The regexes deliberately include the "AND" that joins the base predicate
	// to the filter, so a missing separator (which the DB would reject as a
	// syntax error) fails the test.
	mock.ExpectQuery("t.type = 'credit' AND t.date >=").WithArgs(simpleArgs...).WillReturnRows(income)
	mock.ExpectQuery(`(?s)SELECT a\.id::text.*t.type = 'debit' AND t.date >=`).WithArgs(simpleArgs...).WillReturnRows(acctCat)
	mock.ExpectQuery(`(?s)payees p ON t.payee_id.*t.type = 'debit' AND t.date >=`).WithArgs(simpleArgs...).WillReturnRows(catPayee)
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
	mock.ExpectQuery("t.type = 'credit'").WithArgs(testUserID()).WillReturnError(assert.AnError)

	req, _ := http.NewRequest(http.MethodGet, "/dashboard/money-flow", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestBuildMoneyFlowGraphRollup checks that stages over the node cap collapse
// their smallest nodes into one "Other" node and that the edges are remapped
// through the same rollup.
func TestBuildMoneyFlowGraphRollup(t *testing.T) {
	catA, catB, catC := uuid.NewString(), uuid.NewString(), uuid.NewString()
	acctID, payeeID := uuid.NewString(), uuid.NewString()

	rows := []flowCatPayeeRow{
		{catID: catA, catName: "A", groupID: "expense", groupColor: "#111111", payeeID: payeeID, payeeName: "P", total: money.FromFloat(100)},
		{catID: catB, catName: "B", groupID: "expense", groupColor: "#222222", payeeID: payeeID, payeeName: "P", total: money.FromFloat(50)},
		{catID: catC, catName: "C", groupID: "expense", groupColor: "#333333", payeeID: payeeID, payeeName: "P", total: money.FromFloat(25)},
	}

	graph := buildMoneyFlowGraph(nil, []flowAcctCatRow{
		{acctID: acctID, acctName: "Checking", catID: catA, groupID: "expense", total: money.FromFloat(100)},
		{acctID: acctID, acctName: "Checking", catID: catB, groupID: "expense", total: money.FromFloat(50)},
		{acctID: acctID, acctName: "Checking", catID: catC, groupID: "expense", total: money.FromFloat(25)},
	}, rows, nil, nil, 1)

	other := findFlowNode(graph.Nodes, "category:other")
	require.NotNil(t, other)
	assert.Equal(t, money.FromFloat(75), other.Total)
	assert.Equal(t, "Other categories", other.Name)

	assert.NotNil(t, findFlowNode(graph.Nodes, "category:"+catA))
	assert.Nil(t, findFlowNode(graph.Nodes, "category:"+catB))

	// Every category flows into the single payee: two account->category edges
	// and two category->payee edges (the kept category plus the rolled-up one).
	assert.Len(t, graph.Links, 4)
	var otherEdge *models.MoneyFlowEdge
	for i := range graph.Links {
		if graph.Links[i].Source == "category:other" {
			otherEdge = &graph.Links[i]
		}
	}
	require.NotNil(t, otherEdge)
	assert.Equal(t, money.FromFloat(75), otherEdge.Value)
	assert.Equal(t, "payee:"+payeeID, otherEdge.Target)
}

func TestAccountFlowEdgesDerivesDirectionAndSkipsSameAccount(t *testing.T) {
	a, b := uuid.NewString(), uuid.NewString()

	edges := accountFlowEdges([]flowAcctLinkRow{
		// Stored credit -> debit: money still flows debit (B) -> credit (A).
		{fromType: "credit", toType: "debit",
			fromAcctID: a, fromAcctName: "A", fromAcctColor: "#111",
			toAcctID: b, toAcctName: "B", toAcctColor: "#222", amount: money.FromFloat(50)},
		// Same account on both ends: not an account-to-account flow.
		{fromType: "debit", toType: "credit",
			fromAcctID: a, fromAcctName: "A", toAcctID: a, toAcctName: "A", amount: money.FromFloat(10)},
	})

	require.Len(t, edges, 1)
	assert.Equal(t, b, edges[0].srcID)
	assert.Equal(t, a, edges[0].dstID)
	assert.Equal(t, money.FromFloat(50), edges[0].value)
}

func TestAccountFlowEdgesNetsReciprocalPairs(t *testing.T) {
	a, b := uuid.NewString(), uuid.NewString()

	edges := accountFlowEdges([]flowAcctLinkRow{
		{fromType: "debit", toType: "credit", fromAcctID: a, fromAcctName: "A", toAcctID: b, toAcctName: "B", amount: money.FromFloat(100)},
		{fromType: "debit", toType: "credit", fromAcctID: b, fromAcctName: "B", toAcctID: a, toAcctName: "A", amount: money.FromFloat(40)},
	})

	require.Len(t, edges, 1)
	assert.Equal(t, a, edges[0].srcID)
	assert.Equal(t, b, edges[0].dstID)
	assert.Equal(t, money.FromFloat(60), edges[0].value)
}

func TestAccountFlowEdgesBreaksCyclesDeterministically(t *testing.T) {
	a, b, c := uuid.NewString(), uuid.NewString(), uuid.NewString()
	// Force a < b < c ordering so the expected kept edges are stable.
	ids := []string{a, b, c}
	sort.Strings(ids)
	a, b, c = ids[0], ids[1], ids[2]

	edges := accountFlowEdges([]flowAcctLinkRow{
		{fromType: "debit", toType: "credit", fromAcctID: a, fromAcctName: "A", toAcctID: b, toAcctName: "B", amount: money.FromFloat(100)},
		{fromType: "debit", toType: "credit", fromAcctID: b, fromAcctName: "B", toAcctID: c, toAcctName: "C", amount: money.FromFloat(100)},
		{fromType: "debit", toType: "credit", fromAcctID: c, fromAcctName: "C", toAcctID: a, toAcctName: "A", amount: money.FromFloat(100)},
	})

	// The 3-cycle loses exactly one (back) edge, leaving a 2-edge DAG.
	require.Len(t, edges, 2)
	assert.Equal(t, a, edges[0].srcID)
	assert.Equal(t, b, edges[0].dstID)
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
