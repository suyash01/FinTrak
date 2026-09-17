package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

// moneyFlowRows wires the four expected queries for a money-flow request.
func expectMoneyFlowQueries(mock pgxmock.PgxPoolIface, args []any, income, acctCat, catPayee, links *pgxmock.Rows) {
	mock.ExpectBeginTx(pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	mock.ExpectQuery("t.type = 'credit'").WithArgs(args...).WillReturnRows(income)
	mock.ExpectQuery("SELECT a.id::text, a.name, a.color").WithArgs(args...).WillReturnRows(acctCat)
	mock.ExpectQuery("payees p ON t.payee_id").WithArgs(args...).WillReturnRows(catPayee)
	mock.ExpectQuery("FROM links l").WithArgs(args...).WillReturnRows(links)
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
	payeeID := uuid.New()

	income := pgxmock.NewRows([]string{"cat_id", "cat_name", "cat_color", "group_id", "group_color", "acct_id", "acct_name", "acct_color", "total"}).
		AddRow(catID.String(), "Salary", "#22c55e", "income", "#22c55e", acctID.String(), "Checking", "#3b82f6", 50000.00)
	acctCat := pgxmock.NewRows([]string{"acct_id", "acct_name", "acct_color", "cat_id", "cat_name", "cat_color", "group_id", "group_color", "total"}).
		AddRow(acctID.String(), "Checking", "#3b82f6", catID.String(), "Food", "#f97316", "expense", "#f97316", 12000.00)
	catPayee := pgxmock.NewRows([]string{"cat_id", "cat_name", "cat_color", "group_id", "group_color", "payee_id", "payee_name", "total"}).
		AddRow(catID.String(), "Food", "#f97316", "expense", "#f97316", payeeID.String(), "Zomato", 12000.00)
	links := pgxmock.NewRows([]string{"type", "count", "total"}).
		AddRow("transfer", 2, 30000.00)

	expectMoneyFlowQueries(mock, []any{userID}, income, acctCat, catPayee, links)

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

	assert.Len(t, graph.Links, 3)
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

	mock.ExpectBeginTx(pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	// The regexes deliberately include the "AND" that joins the base predicate
	// to the filter, so a missing separator (which the DB would reject as a
	// syntax error) fails the test.
	mock.ExpectQuery("t.type = 'credit' AND t.date >=").WithArgs(simpleArgs...).WillReturnRows(income)
	mock.ExpectQuery(`(?s)SELECT a\.id::text.*t.type = 'debit' AND t.date >=`).WithArgs(simpleArgs...).WillReturnRows(acctCat)
	mock.ExpectQuery(`(?s)payees p ON t.payee_id.*t.type = 'debit' AND t.date >=`).WithArgs(simpleArgs...).WillReturnRows(catPayee)
	mock.ExpectQuery("FROM links l").WithArgs(linkArgs...).WillReturnRows(links)
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
	}, rows, nil, 1)

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
