package handlers

import (
	"context"
	"encoding/json"
	"errors"
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

// linkDetailRow builds one raw cross-account link as the report query returns
// it: both endpoints debit/credit, so direction is derived the same way as the
// Sankey's. currency is the denomination of the amount the query's CASE picked,
// which is not necessarily the currency of the account the money flows out of.
func linkDetailRow(linkType, fromType, toType, currency string, from, to flowAccountEnd, amount float64) flowLinkDetailRow {
	return flowLinkDetailRow{
		linkType: linkType,
		flowAcctLinkRow: flowAcctLinkRow{
			fromType: fromType, toType: toType,
			fromAcctID: from.id, fromAcctName: from.name, fromAcctColor: from.color,
			toAcctID: to.id, toAcctName: to.name, toAcctColor: to.color,
			currency: currency,
			amount:   money.FromFloat(amount),
		},
	}
}

// The link query's own regexes. The projected currency is the shared
// linkCurrencyColumn expression - the CASE over the endpoint whose transaction
// supplied the amount - wrapped in the same COALESCE/NULLIF every other site
// uses, and it appears both in the SELECT projection and in the ?currency=
// predicate. flowLinkCurrencyRegex is the spelling money_flow_test.go pins; the
// argument alone cannot catch a respelling, because pgxmock compares arguments
// with reflect.DeepEqual and a bare COALESCE(CASE ...) binds the same "USD".
const (
	// The scope read is the shared currencyScope statement - one query behind
	// every reporting endpoint's currencyScope block - so the regex is
	// money_flow_test.go's rather than a fourth spelling of it.
	linkCyclesScopeRegex  = moneyFlowScopeRegex
	linkCyclesDetailsHead = `(?s)SELECT l\.type, ft\.type, tt\.type, fa\.id::text, fa\.name, fa\.color, ` +
		`ta\.id::text, ta\.name, ta\.color, ` + flowLinkCurrencyRegex + ` AS currency, ` +
		`CASE WHEN ft\.type = 'debit' THEN ft\.amount ELSE tt\.amount END`
	// The cross-account guard is pinned because it is what keeps a same-account
	// refund or cashback out of an account-to-account report, and a loose
	// "SELECT l.type" would not notice its loss.
	linkCyclesDetailsRegex = linkCyclesDetailsHead + `[\s\S]*AND fa\.id <> ta\.id`
)

// linkCycleDetailRows is the column set queryAccountLinkDetails scans, in the
// order it scans them.
func linkCycleDetailRows() *pgxmock.Rows {
	return pgxmock.NewRows([]string{
		"link_type", "from_type", "to_type",
		"fa_id", "fa_name", "fa_color", "ta_id", "ta_name", "ta_color",
		"currency", "amount",
	})
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
		linkDetailRow("transfer", "debit", "credit", "INR", a, b, 1000),
		linkDetailRow("transfer", "debit", "credit", "INR", b, a, 300),
	})

	require.Len(t, report.Cycles, 1)
	cycle := report.Cycles[0]
	assert.Equal(t, "reciprocal", cycle.Kind)
	require.Len(t, cycle.Accounts, 2)
	require.Len(t, cycle.Legs, 2)
	// Gross is both directions; Net is what actually completes the loop. A cycle
	// whose legs are all in one currency is unchanged by this: one key, and the
	// old figure is still readable through Single().
	assert.Equal(t, models.CurrencyAmounts{"INR": money.FromFloat(1300)}, cycle.Gross)
	assert.Equal(t, models.CurrencyAmounts{"INR": money.FromFloat(300)}, cycle.Net)
	assert.Equal(t, models.CurrencyAmounts{"INR": money.FromFloat(300)}, report.TotalCircular)
	code, amount, ok := cycle.Net.Single()
	assert.True(t, ok, "a single-currency cycle net did not report as a single amount")
	assert.Equal(t, "INR", code)
	assert.Equal(t, money.FromFloat(300), amount)
	assert.Equal(t, 2, cycle.Transactions)
	// Leg order follows the sorted account ids, so it is compared as a set:
	// pinning it positionally would test uuid ordering, not the report.
	legAmounts := []models.CurrencyAmounts{cycle.Legs[0].Amount, cycle.Legs[1].Amount}
	sort.Slice(legAmounts, func(i, j int) bool { return legAmounts[i]["INR"] < legAmounts[j]["INR"] })
	assert.Equal(t, []models.CurrencyAmounts{
		{"INR": money.FromFloat(300)},
		{"INR": money.FromFloat(1000)},
	}, legAmounts)
	for _, leg := range cycle.Legs {
		require.Len(t, leg.Types, 1)
		assert.Equal(t, "transfer", leg.Types[0].Type)
		assert.Equal(t, models.CurrencyAmounts{"INR": leg.Amount["INR"]}, leg.Types[0].Total)
	}

	// Both directions exist, so neither is reported as one-sided.
	assert.Empty(t, report.OneSidedFlows)
}

func TestBuildLinkCycleReportLongerCycle(t *testing.T) {
	a := flowAccountEnd{id: uuid.NewString(), name: "A"}
	b := flowAccountEnd{id: uuid.NewString(), name: "B"}
	c := flowAccountEnd{id: uuid.NewString(), name: "C"}

	report := buildLinkCycleReport([]flowLinkDetailRow{
		linkDetailRow("transfer", "debit", "credit", "INR", a, b, 500),
		linkDetailRow("bill_payment", "debit", "credit", "INR", b, c, 400),
		linkDetailRow("cashback", "debit", "credit", "INR", c, a, 900),
	})

	require.Len(t, report.Cycles, 1)
	cycle := report.Cycles[0]
	assert.Equal(t, "cycle", cycle.Kind)
	assert.Len(t, cycle.Accounts, 3)
	assert.Len(t, cycle.Legs, 3)
	assert.Equal(t, models.CurrencyAmounts{"INR": money.FromFloat(1800)}, cycle.Gross)
	// The smallest leg is what can circulate all the way around.
	assert.Equal(t, models.CurrencyAmounts{"INR": money.FromFloat(400)}, cycle.Net)
	assert.Equal(t, models.CurrencyAmounts{"INR": money.FromFloat(400)}, report.TotalCircular)

	// A cycle's legs are each other's counterpart, so they are not also
	// reported as one-sided flows.
	assert.Empty(t, report.OneSidedFlows)
}

// The bug this endpoint has and the other four had not: a link has a
// from-account and a to-account, so unlike every transaction-driven report it
// sums two accounts' transactions by construction, and those two need not share a
// currency. Leg amounts are per-currency, a cycle's net is each currency's own
// smallest leg rather than one number, and the honest answer to "what
// circulates this loop" is that a loop spanning currencies has no such figure -
// which is what the shape reports and Single() refuses to flatten.
func TestBuildLinkCycleReportRefusesACrossCurrencyTotal(t *testing.T) {
	inr := flowAccountEnd{id: uuid.NewString(), name: "Savings", color: "#111"}
	usd := flowAccountEnd{id: uuid.NewString(), name: "Travel card", color: "#222"}

	report := buildLinkCycleReport([]flowLinkDetailRow{
		// A rupee leg and a dollar leg, in opposite directions: the shape a
		// transfer between a local account and a foreign one has.
		linkDetailRow("transfer", "debit", "credit", "INR", inr, usd, 1000),
		linkDetailRow("transfer", "debit", "credit", "USD", usd, inr, 12),
	})

	require.Len(t, report.Cycles, 1)
	cycle := report.Cycles[0]
	assert.Equal(t, "reciprocal", cycle.Kind)

	// Each leg is denominated in the currency of the account the amount came
	// from, so each is one currency, and neither is added to the other.
	require.Len(t, cycle.Legs, 2)
	byPair := map[string]models.CurrencyAmounts{}
	for _, leg := range cycle.Legs {
		byPair[leg.FromAccountID+"->"+leg.ToAccountID] = leg.Amount
	}
	assert.Equal(t, models.CurrencyAmounts{"INR": money.FromFloat(1000)}, byPair[inr.id+"->"+usd.id])
	assert.Equal(t, models.CurrencyAmounts{"USD": money.FromFloat(12)}, byPair[usd.id+"->"+inr.id])

	assert.Equal(t, models.CurrencyAmounts{
		"INR": money.FromFloat(1000),
		"USD": money.FromFloat(12),
	}, cycle.Gross)
	// Each currency's own smallest leg. The old code compared the two legs as
	// one number and would have reported 1200.00 - twelve dollars plus a thousand
	// rupees - as the amount circulating the loop.
	assert.Equal(t, models.CurrencyAmounts{
		"INR": money.FromFloat(1000),
		"USD": money.FromFloat(12),
	}, cycle.Net)
	assert.Equal(t, cycle.Net, report.TotalCircular)

	// Nothing collapses the two keys, and the shape says so: a caller asking
	// "what circulates this loop" gets ok=false and knows to report that the
	// loop moves two currencies rather than to sum them.
	if _, _, ok := cycle.Net.Single(); ok {
		t.Errorf("a two-currency cycle net reported as a single amount: %v", cycle.Net)
	}

	// The one-sided rollup is per-currency for the same reason: a one-way pair
	// can move money in two currencies too.
	oneSided := buildLinkCycleReport([]flowLinkDetailRow{
		linkDetailRow("refund", "debit", "credit", "USD", usd, inr, 12),
		linkDetailRow("cashback", "debit", "credit", "INR", usd, inr, 40),
	})
	require.Len(t, oneSided.OneSidedFlows, 1)
	assert.Equal(t, models.CurrencyAmounts{"USD": money.FromFloat(12), "INR": money.FromFloat(40)},
		oneSided.OneSidedFlows[0].Total)
	require.Len(t, oneSided.OneSidedFlows[0].Types, 2)
	// Largest flow first, and "largest" is compareFlowTotals' total order over
	// the sorted union of the two key sets - never a comparison between a rupee
	// and a dollar. Here the INR leg leads because INR is the first code and the
	// USD-only rollup reads zero in it; the order is total and stable, which is
	// all a display order has to be.
	assert.Equal(t, "cashback", oneSided.OneSidedFlows[0].Types[0].Type)
	assert.Equal(t, models.CurrencyAmounts{"INR": money.FromFloat(40)}, oneSided.OneSidedFlows[0].Types[0].Total)
	assert.Equal(t, "refund", oneSided.OneSidedFlows[0].Types[1].Type)
}

// "Smallest" is decided inside one currency, never across the key set - which is
// the obvious wrong reading of a per-currency minimum, because the code looks as
// though comparing two maps needs no care. The smallest USD leg (50) is the
// smallest *number* here and must not become the INR answer: the INR minimum is
// its own smallest INR leg (400), so a client can say the loop moves 400 rupees
// and 50 dollars.
func TestBuildLinkCycleReportTakesEachCurrencysOwnSmallestLeg(t *testing.T) {
	a := flowAccountEnd{id: uuid.NewString(), name: "A"}
	b := flowAccountEnd{id: uuid.NewString(), name: "B"}
	c := flowAccountEnd{id: uuid.NewString(), name: "C"}

	report := buildLinkCycleReport([]flowLinkDetailRow{
		linkDetailRow("transfer", "debit", "credit", "INR", a, b, 1000),
		linkDetailRow("transfer", "debit", "credit", "USD", b, c, 50),
		linkDetailRow("refund", "debit", "credit", "INR", c, a, 400),
	})

	require.Len(t, report.Cycles, 1)
	cycle := report.Cycles[0]
	assert.Equal(t, "cycle", cycle.Kind)
	assert.Equal(t, models.CurrencyAmounts{
		"INR": money.FromFloat(400),
		"USD": money.FromFloat(50),
	}, cycle.Net)
	assert.Equal(t, models.CurrencyAmounts{
		"INR": money.FromFloat(1400),
		"USD": money.FromFloat(50),
	}, cycle.Gross)
	assert.Equal(t, cycle.Net, report.TotalCircular)
}

// A currency that appears in only one leg is that leg's own value: there is no
// other candidate to compare it against, and inventing one would put a number
// in the map that no leg holds. So the answer is the union of the legs' keys,
// the same rule Sub and maxFlowAmounts already follow.
func TestBuildLinkCycleReportNetIsTheUnionOfItsLegsCurrencies(t *testing.T) {
	a := flowAccountEnd{id: uuid.NewString(), name: "A"}
	b := flowAccountEnd{id: uuid.NewString(), name: "B"}

	report := buildLinkCycleReport([]flowLinkDetailRow{
		linkDetailRow("transfer", "debit", "credit", "INR", a, b, 700),
		linkDetailRow("transfer", "debit", "credit", "USD", b, a, 30),
	})

	require.Len(t, report.Cycles, 1)
	assert.Equal(t, models.CurrencyAmounts{"INR": money.FromFloat(700), "USD": money.FromFloat(30)},
		report.Cycles[0].Net)
}

// A cycle whose smallest leg is zero still has a key, because a present zero and
// an absent key are the same value to every reader but only a present one keeps
// the currency named in the response. The empty map is the {} the contract
// promises, never a null.
func TestBuildLinkCycleReportEmptyAmountsAreObjectsNotNulls(t *testing.T) {
	report := buildLinkCycleReport(nil)

	assert.Equal(t, models.CurrencyAmounts{}, report.TotalCircular)
	assert.Empty(t, report.Cycles)
	assert.Empty(t, report.OneSidedFlows)

	a := flowAccountEnd{id: uuid.NewString(), name: "A"}
	b := flowAccountEnd{id: uuid.NewString(), name: "B"}
	withPair := buildLinkCycleReport([]flowLinkDetailRow{
		linkDetailRow("transfer", "debit", "credit", "INR", a, b, 0),
	})
	require.Len(t, withPair.OneSidedFlows, 1)
	assert.Equal(t, models.CurrencyAmounts{}, withPair.OneSidedFlows[0].Total)
	require.Len(t, withPair.OneSidedFlows[0].Types, 1)
	assert.Equal(t, models.CurrencyAmounts{}, withPair.OneSidedFlows[0].Types[0].Total)
}

func TestBuildLinkCycleReportOneSidedFlows(t *testing.T) {
	a := flowAccountEnd{id: uuid.NewString(), name: "Checking", color: "#111"}
	b := flowAccountEnd{id: uuid.NewString(), name: "Card", color: "#222"}
	c := flowAccountEnd{id: uuid.NewString(), name: "Savings", color: "#333"}

	report := buildLinkCycleReport([]flowLinkDetailRow{
		// A -> B with two different link types, no flow back.
		linkDetailRow("transfer", "debit", "credit", "INR", a, b, 60),
		linkDetailRow("bill_payment", "debit", "credit", "INR", a, b, 40),
		// Same-account link: no account-to-account flow at all.
		linkDetailRow("refund", "debit", "credit", "INR", a, a, 25),
		// An unrelated one-way pair.
		linkDetailRow("cashback", "debit", "credit", "INR", b, c, 15),
	})

	assert.Empty(t, report.Cycles)
	require.Len(t, report.OneSidedFlows, 2)

	flow := findOneSidedFlow(report, a.id, b.id)
	require.NotNil(t, flow)
	assert.Equal(t, models.CurrencyAmounts{"INR": money.FromFloat(100)}, flow.Total)
	assert.Equal(t, 2, flow.Count)
	require.Len(t, flow.Types, 2)
	// Largest flow first.
	assert.Equal(t, "transfer", flow.Types[0].Type)
	assert.Equal(t, models.CurrencyAmounts{"INR": money.FromFloat(60)}, flow.Types[0].Total)
	assert.Equal(t, "bill_payment", flow.Types[1].Type)

	other := findOneSidedFlow(report, b.id, c.id)
	require.NotNil(t, other)
	assert.Equal(t, models.CurrencyAmounts{"INR": money.FromFloat(15)}, other.Total)

	// The same-account link is not a flow in either direction.
	assert.Nil(t, findOneSidedFlow(report, a.id, a.id))
	assert.Equal(t, models.CurrencyAmounts{}, report.TotalCircular)
}

func newLinkCyclesTestRouter(srv *Server) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.Default()
	r.Use(testAuthMiddleware())
	r.GET("/links/cycles", srv.GetLinkCycles)
	return r
}

func TestGetLinkCycles(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()

	userID := testUserID()
	checking, card := uuid.New(), uuid.New()

	// The scope and the links are read in one snapshot, so a cycle can never be
	// reported beside a currency scope describing a different ledger; the begin
	// and the commit are what pin that, and dropping either fails here.
	mock.ExpectBeginTx(pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	mock.ExpectQuery(linkCyclesScopeRegex).
		WithArgs(userID).
		WillReturnRows(pgxmock.NewRows([]string{"id", "name", "currency", "income", "expense"}).
			AddRow(checking, "Checking", "INR", money.FromFloat(20000), money.FromFloat(9000)))
	// Checking (debit) -> Card (credit) on the transfer, and back the other way.
	mock.ExpectQuery(linkCyclesDetailsRegex).
		WithArgs(userID).
		WillReturnRows(linkCycleDetailRows().
			AddRow("transfer", "debit", "credit", checking.String(), "Checking", "#111", card.String(), "Card", "#222", "INR", 5000.00).
			AddRow("transfer", "debit", "credit", card.String(), "Card", "#222", checking.String(), "Checking", "#111", "INR", 1200.00))
	mock.ExpectCommit()

	req, _ := http.NewRequest(http.MethodGet, "/links/cycles", nil)
	w := httptest.NewRecorder()
	newLinkCyclesTestRouter(newTestServer(mock)).ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	var report models.LinkCycleReport
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &report))
	require.Len(t, report.Cycles, 1)
	assert.Equal(t, "reciprocal", report.Cycles[0].Kind)
	assert.Equal(t, models.CurrencyAmounts{"INR": money.FromFloat(1200)}, report.Cycles[0].Net)
	assert.Equal(t, models.CurrencyAmounts{"INR": money.FromFloat(1200)}, report.TotalCircular)
	assert.Empty(t, report.OneSidedFlows)

	// The scope names the currency the single-currency figures are in, so a
	// client can read the report without knowing which account it covers.
	assert.Equal(t, []string{"INR"}, report.CurrencyScope.Currencies)
	require.Len(t, report.CurrencyScope.Accounts, 1)
	assert.Equal(t, "Checking", report.CurrencyScope.Accounts[0].Name)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// A link has two accounts, so this report is the one aggregate that sums two
// accounts' transactions by construction - and the two need not share a
// currency. With no account filter a reciprocal pair here has an INR leg and a
// USD leg, and neither figure is addable to the other: the old report's single
// "net" was exactly the twelve dollars plus a thousand rupees.
func TestGetLinkCyclesRefusesACrossCurrencyTotal(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()

	userID := testUserID()
	savings, card := uuid.New(), uuid.New()

	mock.ExpectBeginTx(pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	mock.ExpectQuery(linkCyclesScopeRegex).
		WithArgs(userID).
		WillReturnRows(pgxmock.NewRows([]string{"id", "name", "currency", "income", "expense"}).
			AddRow(savings, "Savings", "INR", money.FromFloat(20000), money.FromFloat(9000)).
			AddRow(card, "Travel card", "USD", money.FromFloat(300), money.FromFloat(50)))
	mock.ExpectQuery(linkCyclesDetailsRegex).
		WithArgs(userID).
		WillReturnRows(linkCycleDetailRows().
			AddRow("transfer", "debit", "credit", savings.String(), "Savings", "#111", card.String(), "Travel card", "#222", "INR", 1000.00).
			AddRow("transfer", "debit", "credit", card.String(), "Travel card", "#222", savings.String(), "Savings", "#111", "USD", 12.00))
	mock.ExpectCommit()

	req, _ := http.NewRequest(http.MethodGet, "/links/cycles", nil)
	w := httptest.NewRecorder()
	newLinkCyclesTestRouter(newTestServer(mock)).ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	var report models.LinkCycleReport
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &report))
	require.Len(t, report.Cycles, 1)
	cycle := report.Cycles[0]
	assert.Equal(t, models.CurrencyAmounts{"INR": money.FromFloat(1000), "USD": money.FromFloat(12)}, cycle.Net)
	assert.Equal(t, models.CurrencyAmounts{"INR": money.FromFloat(1000), "USD": money.FromFloat(12)}, report.TotalCircular)
	if _, _, ok := cycle.Net.Single(); ok {
		t.Error("a two-currency cycle net reported as a single amount")
	}
	assert.Equal(t, []string{"INR", "USD"}, report.CurrencyScope.Currencies)

	// On the wire each leg and each net is an object keyed by currency. A bare
	// number anywhere here is the cross-currency total this change removes.
	assert.Contains(t, w.Body.String(), `"net":{"INR":1000.00,"USD":12.00}`)
	assert.Contains(t, w.Body.String(), `"amount":{"INR":1000.00}`)
	assert.Contains(t, w.Body.String(), `"amount":{"USD":12.00}`)
	assert.NotContains(t, w.Body.String(), `"net":12`)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// The projected currency is the same expression the money-flow link queries use
// - the CASE over the endpoint whose transaction supplied the amount, wrapped in
// the shared COALESCE/NULLIF - and it has to be that expression in the SELECT
// projection and in the ?currency= predicate. Pinning the bound argument is not
// enough: pgxmock compares arguments with reflect.DeepEqual, so respelling the
// projection to a bare COALESCE(CASE ...) would bind the same "USD" and pass a
// loose expectation while disagreeing with the scope query this response's
// currencyScope was folded from, and would put a "" key in the leg's amounts
// for an account whose currency is only unset.
func TestGetLinkCyclesPinsTheProjectedCurrency(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()

	userID := testUserID()
	card, wallet := uuid.New(), uuid.New()
	const predAt2 = ` = \$2`

	mock.ExpectBeginTx(pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	mock.ExpectQuery(linkCyclesScopeRegex + `[\s\S]*` + flowCurrencyRegex + predAt2).
		WithArgs(userID, "USD").
		WillReturnRows(pgxmock.NewRows([]string{"id", "name", "currency", "income", "expense"}).
			AddRow(card, "Travel card", "USD", money.FromFloat(300), money.FromFloat(50)))
	mock.ExpectQuery(linkCyclesDetailsHead + `[\s\S]*` + flowLinkCurrencyRegex + predAt2 + `[\s\S]*AND fa\.id <> ta\.id`).
		WithArgs(userID, "USD").
		WillReturnRows(linkCycleDetailRows().
			AddRow("refund", "debit", "credit", card.String(), "Travel card", "#222", wallet.String(), "Wallet", "#333", "USD", 25.00))
	mock.ExpectCommit()

	req, _ := http.NewRequest(http.MethodGet, "/links/cycles?currency=usd", nil)
	w := httptest.NewRecorder()
	newLinkCyclesTestRouter(newTestServer(mock)).ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	var report models.LinkCycleReport
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &report))
	// parseCurrency folded ?currency=usd to "USD" before either query saw it, so
	// the filter finds the USD account instead of matching nothing.
	assert.Equal(t, []string{"USD"}, report.CurrencyScope.Currencies)
	require.Len(t, report.OneSidedFlows, 1)
	assert.Equal(t, models.CurrencyAmounts{"USD": money.FromFloat(25)}, report.OneSidedFlows[0].Total)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetLinkCyclesInvalidAccount(t *testing.T) {
	srv, _ := newMockServer(t)

	req, _ := http.NewRequest(http.MethodGet, "/links/cycles?accountId=not-a-uuid", nil)
	w := httptest.NewRecorder()
	newLinkCyclesTestRouter(srv).ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// The ?currency= parameter is read by the shared parser, so a code that is not
// three letters is the same 400 every reporting endpoint returns - never a
// silently ignored filter and a report of zeroes.
func TestGetLinkCyclesRejectsMalformedCurrency(t *testing.T) {
	srv, _ := newMockServer(t)

	req, _ := http.NewRequest(http.MethodGet, "/links/cycles?currency=US", nil)
	w := httptest.NewRecorder()
	newLinkCyclesTestRouter(srv).ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "invalid currency")
}

// The cycle report shares its window filter with the money-flow endpoints, so a
// malformed date must answer the same 400 they do rather than a 500.
func TestGetLinkCyclesRejectsMalformedDates(t *testing.T) {
	srv, _ := newMockServer(t)
	r := newLinkCyclesTestRouter(srv)

	for _, query := range []string{"dateFrom=oops", "dateTo=2024-1-5"} {
		t.Run(query, func(t *testing.T) {
			req, _ := http.NewRequest(http.MethodGet, "/links/cycles?"+query, nil)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			assert.Equal(t, http.StatusBadRequest, w.Code)
			assert.Contains(t, w.Body.String(), "must be YYYY-MM-DD")
		})
	}
}

// Every read is inside a transaction the handler also commits, so a row-stream
// failure and a commit failure are the same 500 at the router and a fold that
// dropped its rows.Err() check would pass a router-level test. Each branch is
// pinned here, because "the report came back 500" is not the same claim as "this
// statement failed" and a regression in one of them should be visible.
func TestGetLinkCyclesErrors(t *testing.T) {
	userID := testUserID()

	t.Run("begin", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		require.NoError(t, err)
		defer mock.Close()

		mock.ExpectBeginTx(pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}).
			WillReturnError(assert.AnError)

		w := httptest.NewRecorder()
		newLinkCyclesTestRouter(newTestServer(mock)).ServeHTTP(w,
			httptest.NewRequest(http.MethodGet, "/links/cycles", nil))
		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("currency scope", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		require.NoError(t, err)
		defer mock.Close()

		mock.ExpectBeginTx(pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
		mock.ExpectQuery(linkCyclesScopeRegex).WithArgs(userID).WillReturnError(assert.AnError)

		w := httptest.NewRecorder()
		newLinkCyclesTestRouter(newTestServer(mock)).ServeHTTP(w,
			httptest.NewRequest(http.MethodGet, "/links/cycles", nil))
		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("details", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		require.NoError(t, err)
		defer mock.Close()

		mock.ExpectBeginTx(pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
		mock.ExpectQuery(linkCyclesScopeRegex).
			WithArgs(userID).
			WillReturnRows(pgxmock.NewRows([]string{"id", "name", "currency", "income", "expense"}))
		mock.ExpectQuery(linkCyclesDetailsRegex).WithArgs(userID).WillReturnError(assert.AnError)

		w := httptest.NewRecorder()
		newLinkCyclesTestRouter(newTestServer(mock)).ServeHTTP(w,
			httptest.NewRequest(http.MethodGet, "/links/cycles", nil))
		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	// A failed commit means the snapshot was not the one the response describes,
	// so the response is withheld rather than sent from a transaction Postgres
	// has already discarded.
	t.Run("commit", func(t *testing.T) {
		mock, err := pgxmock.NewPool()
		require.NoError(t, err)
		defer mock.Close()

		mock.ExpectBeginTx(pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
		mock.ExpectQuery(linkCyclesScopeRegex).
			WithArgs(userID).
			WillReturnRows(pgxmock.NewRows([]string{"id", "name", "currency", "income", "expense"}))
		mock.ExpectQuery(linkCyclesDetailsRegex).
			WithArgs(userID).
			WillReturnRows(linkCycleDetailRows())
		mock.ExpectCommit().WillReturnError(assert.AnError)

		w := httptest.NewRecorder()
		newLinkCyclesTestRouter(newTestServer(mock)).ServeHTTP(w,
			httptest.NewRequest(http.MethodGet, "/links/cycles", nil))
		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

// A stream that fails after a row is the case worth pinning: the fold already
// holds links, and returning them would answer 200 with a report quietly missing
// the rest of the window - read as a quiet account rather than a failed read.
// Through the handler a failed stream and a failed commit are both a 500, so the
// query is driven directly.
func TestQueryAccountLinkDetailsPropagatesARowError(t *testing.T) {
	boom := errors.New("connection reset mid-fold")

	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()

	mock.ExpectQuery(linkCyclesDetailsRegex).
		WithArgs(testUserID()).
		WillReturnRows(linkCycleDetailRows().
			AddRow("transfer", "debit", "credit", uuid.NewString(), "A", "#111", uuid.NewString(), "B", "#222", "INR", 100.00).
			RowError(1, boom))

	rows, err := queryAccountLinkDetails(context.Background(), mock, testUserID(), "", "", "", "")
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want %v", err, boom)
	}
	if len(rows) != 0 {
		t.Errorf("rows = %v on a row error, want none", rows)
	}
	assert.NoError(t, mock.ExpectationsWereMet())
}
