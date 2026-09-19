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
	"github.com/pashagolub/pgxmock/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// linkDetailRow builds one raw cross-account link as the report query returns
// it: both endpoints debit/credit, so direction is derived the same way as the
// Sankey's.
func linkDetailRow(linkType, fromType, toType string, from, to flowAccountEnd, amount float64) flowLinkDetailRow {
	return flowLinkDetailRow{
		linkType: linkType,
		flowAcctLinkRow: flowAcctLinkRow{
			fromType: fromType, toType: toType,
			fromAcctID: from.id, fromAcctName: from.name, fromAcctColor: from.color,
			toAcctID: to.id, toAcctName: to.name, toAcctColor: to.color,
			amount: money.FromFloat(amount),
		},
	}
}

func findOneSidedFlow(report models.LinkCycleReport, from, to string) *models.LinkOneSidedFlow {
	for i := range report.OneSidedFlows {
		if report.OneSidedFlows[i].FromAccountID == from && report.OneSidedFlows[i].ToAccountID == to {
			return &report.OneSidedFlows[i]
		}
	}
	return nil
}

func TestBuildLinkCycleReportReciprocalPair(t *testing.T) {
	a := flowAccountEnd{id: uuid.NewString(), name: "Checking", color: "#111"}
	b := flowAccountEnd{id: uuid.NewString(), name: "Savings", color: "#222"}

	report := buildLinkCycleReport([]flowLinkDetailRow{
		linkDetailRow("transfer", "debit", "credit", a, b, 1000),
		linkDetailRow("transfer", "debit", "credit", b, a, 300),
	})

	require.Len(t, report.Cycles, 1)
	cycle := report.Cycles[0]
	assert.Equal(t, "reciprocal", cycle.Kind)
	require.Len(t, cycle.Accounts, 2)
	require.Len(t, cycle.Legs, 2)
	// Gross is both directions; Net is what actually completes the loop.
	assert.Equal(t, money.FromFloat(1300), cycle.Gross)
	assert.Equal(t, money.FromFloat(300), cycle.Net)
	assert.Equal(t, money.FromFloat(300), report.TotalCircular)
	assert.Equal(t, 2, cycle.Transactions)
	legAmounts := []money.Amount{cycle.Legs[0].Amount, cycle.Legs[1].Amount}
	sort.Slice(legAmounts, func(i, j int) bool { return legAmounts[i] < legAmounts[j] })
	assert.Equal(t, []money.Amount{money.FromFloat(300), money.FromFloat(1000)}, legAmounts)
	for _, leg := range cycle.Legs {
		require.Len(t, leg.Types, 1)
		assert.Equal(t, "transfer", leg.Types[0].Type)
	}

	// Both directions exist, so neither is reported as one-sided.
	assert.Empty(t, report.OneSidedFlows)
}

func TestBuildLinkCycleReportLongerCycle(t *testing.T) {
	a := flowAccountEnd{id: uuid.NewString(), name: "A"}
	b := flowAccountEnd{id: uuid.NewString(), name: "B"}
	c := flowAccountEnd{id: uuid.NewString(), name: "C"}

	report := buildLinkCycleReport([]flowLinkDetailRow{
		linkDetailRow("transfer", "debit", "credit", a, b, 500),
		linkDetailRow("bill_payment", "debit", "credit", b, c, 400),
		linkDetailRow("cashback", "debit", "credit", c, a, 900),
	})

	require.Len(t, report.Cycles, 1)
	cycle := report.Cycles[0]
	assert.Equal(t, "cycle", cycle.Kind)
	assert.Len(t, cycle.Accounts, 3)
	assert.Len(t, cycle.Legs, 3)
	assert.Equal(t, money.FromFloat(1800), cycle.Gross)
	// The smallest leg is what can circulate all the way around.
	assert.Equal(t, money.FromFloat(400), cycle.Net)
	assert.Equal(t, money.FromFloat(400), report.TotalCircular)

	// A cycle's legs are each other's counterpart, so they are not also
	// reported as one-sided flows.
	assert.Empty(t, report.OneSidedFlows)
}

func TestBuildLinkCycleReportOneSidedFlows(t *testing.T) {
	a := flowAccountEnd{id: uuid.NewString(), name: "Checking", color: "#111"}
	b := flowAccountEnd{id: uuid.NewString(), name: "Card", color: "#222"}
	c := flowAccountEnd{id: uuid.NewString(), name: "Savings", color: "#333"}

	report := buildLinkCycleReport([]flowLinkDetailRow{
		// A -> B with two different link types, no flow back.
		linkDetailRow("transfer", "debit", "credit", a, b, 60),
		linkDetailRow("bill_payment", "debit", "credit", a, b, 40),
		// Same-account link: no account-to-account flow at all.
		linkDetailRow("refund", "debit", "credit", a, a, 25),
		// An unrelated one-way pair.
		linkDetailRow("cashback", "debit", "credit", b, c, 15),
	})

	assert.Empty(t, report.Cycles)
	require.Len(t, report.OneSidedFlows, 2)

	flow := findOneSidedFlow(report, a.id, b.id)
	require.NotNil(t, flow)
	assert.Equal(t, money.FromFloat(100), flow.Total)
	assert.Equal(t, 2, flow.Count)
	require.Len(t, flow.Types, 2)
	// Largest flow first.
	assert.Equal(t, "transfer", flow.Types[0].Type)
	assert.Equal(t, money.FromFloat(60), flow.Types[0].Total)
	assert.Equal(t, "bill_payment", flow.Types[1].Type)

	other := findOneSidedFlow(report, b.id, c.id)
	require.NotNil(t, other)
	assert.Equal(t, money.FromFloat(15), other.Total)

	// The same-account link is not a flow in either direction.
	assert.Nil(t, findOneSidedFlow(report, a.id, a.id))
	assert.Zero(t, report.TotalCircular)
}

func TestGetLinkCycles(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()
	srv := newTestServer(mock)

	gin.SetMode(gin.TestMode)
	r := gin.Default()
	r.Use(testAuthMiddleware())
	r.GET("/links/cycles", srv.GetLinkCycles)

	userID := testUserID()
	checking, card := uuid.New(), uuid.New()

	// Checking (debit) -> Card (credit) on the transfer, and back the other way.
	mock.ExpectQuery("SELECT l.type, ft.type, tt.type").
		WithArgs(userID).
		WillReturnRows(pgxmock.NewRows([]string{"link_type", "from_type", "to_type", "fa_id", "fa_name", "fa_color", "ta_id", "ta_name", "ta_color", "amount"}).
			AddRow("transfer", "debit", "credit", checking.String(), "Checking", "#111", card.String(), "Card", "#222", 5000.00).
			AddRow("transfer", "debit", "credit", card.String(), "Card", "#222", checking.String(), "Checking", "#111", 1200.00))

	req, _ := http.NewRequest(http.MethodGet, "/links/cycles", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	var report models.LinkCycleReport
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &report))
	require.Len(t, report.Cycles, 1)
	assert.Equal(t, "reciprocal", report.Cycles[0].Kind)
	assert.Equal(t, money.FromFloat(1200), report.Cycles[0].Net)
	assert.Equal(t, money.FromFloat(1200), report.TotalCircular)
	assert.Empty(t, report.OneSidedFlows)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetLinkCyclesInvalidAccount(t *testing.T) {
	srv, _ := newMockServer(t)

	gin.SetMode(gin.TestMode)
	r := gin.Default()
	r.Use(testAuthMiddleware())
	r.GET("/links/cycles", srv.GetLinkCycles)

	req, _ := http.NewRequest(http.MethodGet, "/links/cycles?accountId=not-a-uuid", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestGetLinkCyclesQueryError(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()
	srv := newTestServer(mock)

	gin.SetMode(gin.TestMode)
	r := gin.Default()
	r.Use(testAuthMiddleware())
	r.GET("/links/cycles", srv.GetLinkCycles)

	mock.ExpectQuery("SELECT l.type, ft.type, tt.type").
		WithArgs(testUserID()).
		WillReturnError(assert.AnError)

	req, _ := http.NewRequest(http.MethodGet, "/links/cycles", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}
