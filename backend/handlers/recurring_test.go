package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/fintrak/backend/internal/money"
	"github.com/fintrak/backend/models"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/pashagolub/pgxmock/v5"
	"github.com/stretchr/testify/assert"
)

// recurringSeriesColumnsForTest mirrors scanRecurringSeries' scan order.
var recurringSeriesColumnsForTest = []string{
	"id", "account_id", "name", "description", "amount", "type", "frequency",
	"interval", "start_date", "end_date", "category_id", "payee_id", "active",
	"notes", "created_at",
}

// anyArgs builds n pgxmock.AnyArg() values. pgxmock expects exactly as many
// arguments as the query binds unless WithArgs is given, so the helpers below
// pin the arity while leaving values free.
func anyArgs(n int) []any {
	args := make([]any, n)
	for i := range args {
		args[i] = pgxmock.AnyArg()
	}
	return args
}

func expectQueryAny(mock pgxmock.PgxPoolIface, query string, nArgs int) *pgxmock.ExpectedQuery {
	return mock.ExpectQuery(query).WithArgs(anyArgs(nArgs)...)
}

func expectExecAny(mock pgxmock.PgxPoolIface, query string, nArgs int) *pgxmock.ExpectedExec {
	return mock.ExpectExec(query).WithArgs(anyArgs(nArgs)...)
}

func recurringTestRouter(t *testing.T) (*gin.Engine, *Server, pgxmock.PgxPoolIface) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	srv := newTestServer(mock)
	t.Cleanup(mock.Close)

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(testAuthMiddleware())
	r.GET("/recurring", srv.GetRecurringSeries)
	r.POST("/recurring", srv.CreateRecurringSeries)
	r.PUT("/recurring/:id", srv.UpdateRecurringSeries)
	r.DELETE("/recurring/:id", srv.DeleteRecurringSeries)
	r.GET("/recurring/:id/forecast", srv.GetRecurringForecast)
	r.GET("/recurring/:id/suggestions", srv.GetRecurringSuggestions)
	r.GET("/recurring/:id/transactions", srv.GetRecurringTransactions)
	r.GET("/recurring/:id/terms", srv.GetRecurringTerms)
	r.PUT("/recurring/:id/terms", srv.CreateRecurringTerm)
	r.PUT("/recurring/:id/terms/:termId", srv.UpdateRecurringTerm)
	r.DELETE("/recurring/:id/terms/:termId", srv.DeleteRecurringTerm)
	r.POST("/recurring/attach", srv.AttachRecurring)
	r.POST("/recurring/detach", srv.DetachRecurring)
	return r, srv, mock
}

// testRecurringSeries builds a series anchored on a fixed start date.
func testRecurringSeries(freq string, interval int, start string) models.RecurringSeries {
	st, _ := time.Parse("2006-01-02", start)
	return models.RecurringSeries{
		ID:        uuid.New(),
		AccountID: uuid.New(),
		Name:      "Netflix",
		Amount:    money.Amount(1599),
		Type:      "debit",
		Frequency: freq,
		Interval:  interval,
		StartDate: st,
		Active:    true,
	}
}

// recurringSeriesRow builds a mock row in scanRecurringSeries order.
func recurringSeriesRow(id, accountID uuid.UUID, name string, amount int64, freq string, interval int, start time.Time, end *time.Time) []interface{} {
	return []interface{}{
		id, accountID, name, "", amount, "debit", freq, interval,
		start, end, nil, nil, true, "", time.Now(),
	}
}

// recurringTermLoadCols is the column set returned by loadRecurringTerms (the
// joined account name is included).
var recurringTermLoadCols = []string{
	"id", "series_id", "start_date", "end_date", "amount", "account_id", "account_name", "created_at",
}

// recurringTermRow builds a loadRecurringTerms row with an open-ended range.
func recurringTermRow(termID, seriesID uuid.UUID, start time.Time, amount int64, accountID uuid.UUID) []interface{} {
	return []interface{}{termID, seriesID, start, nil, amount, accountID, "Checking", time.Now()}
}

// recurringTermRowEnd builds a loadRecurringTerms row with a closed range.
func recurringTermRowEnd(termID, seriesID uuid.UUID, start, end time.Time, amount int64, accountID uuid.UUID) []interface{} {
	e := end
	return []interface{}{termID, seriesID, start, &e, amount, accountID, "Checking", time.Now()}
}

// recurringTermReturnCols is the column set returned by the term INSERT/UPDATE.
var recurringTermReturnCols = []string{
	"id", "series_id", "start_date", "end_date", "amount", "account_id", "created_at",
}

func mustDate(s string) time.Time {
	d, _ := time.Parse("2006-01-02", s)
	return d
}

func ptrDate(s string) *time.Time {
	d := mustDate(s)
	return &d
}

// ---------------------------------------------------------------------------
// Pure helpers: date math, ranges, forecast, scoring
// ---------------------------------------------------------------------------

func TestAddMonthsAnchored(t *testing.T) {
	anchor, _ := time.Parse("2006-01-02", "2026-01-31")
	assert.Equal(t, "2026-02-28", addMonthsAnchored(anchor, 1).Format("2006-01-02"))
	assert.Equal(t, "2026-03-31", addMonthsAnchored(anchor, 2).Format("2006-01-02"))
	assert.Equal(t, "2026-04-30", addMonthsAnchored(anchor, 3).Format("2006-01-02"))
	// A leap year clamps February to the 29th.
	leap, _ := time.Parse("2006-01-02", "2028-01-31")
	assert.Equal(t, "2028-02-29", addMonthsAnchored(leap, 1).Format("2006-01-02"))
}

func TestRecurringOccurrenceAt(t *testing.T) {
	anchor, _ := time.Parse("2006-01-02", "2026-01-01")
	assert.Equal(t, "2026-01-03", recurringOccurrenceAt(anchor, recurringFreqDaily, 2, 1).Format("2006-01-02"))
	assert.Equal(t, "2026-01-15", recurringOccurrenceAt(anchor, recurringFreqWeekly, 2, 1).Format("2006-01-02"))
	assert.Equal(t, "2026-03-01", recurringOccurrenceAt(anchor, recurringFreqMonthly, 2, 1).Format("2006-01-02"))
	assert.Equal(t, "2028-01-01", recurringOccurrenceAt(anchor, recurringFreqYearly, 2, 1).Format("2006-01-02"))
}

func TestRecurringUpcoming(t *testing.T) {
	s := testRecurringSeries(recurringFreqMonthly, 1, "2026-01-15")
	from, _ := time.Parse("2006-01-02", "2026-03-01")
	dates := recurringUpcoming(s, from, 3)
	got := []string{}
	for _, d := range dates {
		got = append(got, d.Format("2006-01-02"))
	}
	assert.Equal(t, []string{"2026-03-15", "2026-04-15", "2026-05-15"}, got)
}

func TestRecurringUpcomingRespectsEndDate(t *testing.T) {
	s := testRecurringSeries(recurringFreqMonthly, 1, "2026-01-15")
	end, _ := time.Parse("2006-01-02", "2026-03-01")
	s.EndDate = &end
	from, _ := time.Parse("2006-01-02", "2026-01-01")
	dates := recurringUpcoming(s, from, 12)
	assert.Len(t, dates, 2) // Jan 15, Feb 15
}

func TestNextRecurringOccurrenceEnded(t *testing.T) {
	s := testRecurringSeries(recurringFreqMonthly, 1, "2020-01-15")
	end, _ := time.Parse("2006-01-02", "2020-06-15")
	s.EndDate = &end
	from, _ := time.Parse("2006-01-02", "2026-01-01")
	assert.Nil(t, nextRecurringOccurrence(s, from))

	s.EndDate = nil
	next := nextRecurringOccurrence(s, from)
	assert.NotNil(t, next)
	assert.Equal(t, "2026-01-15", next.Format("2006-01-02"))
}

func TestRecurringMonthlyAmount(t *testing.T) {
	cases := []struct {
		freq     string
		interval int
		cents    int64
		want     int64
	}{
		{recurringFreqMonthly, 1, 1599, 1599},
		{recurringFreqMonthly, 3, 1599, 533},
		{recurringFreqYearly, 1, 12000, 1000},
		{recurringFreqWeekly, 1, 1000, 4333},
		{recurringFreqDaily, 1, 100, 3041},
	}
	for _, tc := range cases {
		s := testRecurringSeries(tc.freq, tc.interval, "2026-01-01")
		s.Amount = money.Amount(tc.cents)
		assert.Equal(t, tc.want, recurringMonthlyAmount(s).Cents(), "%s/%d", tc.freq, tc.interval)
	}
}

func TestNearestRecurringOccurrence(t *testing.T) {
	s := testRecurringSeries(recurringFreqMonthly, 1, "2026-01-15")
	d, _ := time.Parse("2006-01-02", "2026-03-10")
	occ, daysOff, ok := nearestRecurringOccurrence(s, d)
	assert.True(t, ok)
	assert.Equal(t, "2026-03-15", occ.Format("2006-01-02"))
	assert.InDelta(t, 5, daysOff, 0.001)

	// An ended series still yields its closest (last) occurrence.
	end, _ := time.Parse("2006-01-02", "2026-02-15")
	s.EndDate = &end
	occ, _, ok = nearestRecurringOccurrence(s, d)
	assert.True(t, ok)
	assert.Equal(t, "2026-02-15", occ.Format("2006-01-02"))
}

func TestCalculateRecurringScore(t *testing.T) {
	s := testRecurringSeries(recurringFreqMonthly, 1, "2026-01-01")

	// No day offset -> perfect score.
	txn := models.Transaction{Amount: money.Amount(1599), Description: "ACME"}
	assert.Equal(t, 100.0, calculateRecurringScore(s, txn, 0))

	// One day off the expected occurrence (-8).
	assert.InDelta(t, 92, calculateRecurringScore(s, txn, 1), 0.001)

	// Five days off (-40).
	assert.InDelta(t, 60, calculateRecurringScore(s, txn, 5), 0.001)

	// Description / category bonuses cap at 100.
	catID := uuid.New()
	s.CategoryID = &catID
	txn = models.Transaction{Amount: money.Amount(1599), Description: "Netflix subscription", CategoryID: &catID}
	assert.Equal(t, 100.0, calculateRecurringScore(s, txn, 0))
}

func TestRecurringDescriptionMatches(t *testing.T) {
	s := testRecurringSeries(recurringFreqMonthly, 1, "2026-01-01")
	s.Name = "Netflix India"
	assert.True(t, recurringDescriptionMatches(s, "Payment to NETFLIX"))
	// Words shorter than 3 chars are ignored.
	s.Name = "Go"
	assert.False(t, recurringDescriptionMatches(s, "payment"))
}

func TestRecurringTermContainingAndAt(t *testing.T) {
	terms := []models.RecurringSeriesTerm{
		{StartDate: mustDate("2026-01-01"), EndDate: ptrDate("2026-05-31"), Amount: money.Amount(1000)},
		{StartDate: mustDate("2026-07-01"), Amount: money.Amount(2000)},
	}
	// Containment: a date inside a range matches that range.
	assert.Equal(t, int64(1000), recurringTermContaining(terms, mustDate("2026-03-15")).Amount.Cents())
	assert.Equal(t, int64(2000), recurringTermContaining(terms, mustDate("2026-09-01")).Amount.Cents())
	// A date in the gap (June) matches no range.
	assert.Nil(t, recurringTermContaining(terms, mustDate("2026-06-15")))
	assert.Nil(t, recurringTermContaining(nil, mustDate("2026-06-15")))

	// The display resolver carries the previous range forward across a gap and
	// before the first range.
	assert.Equal(t, int64(1000), recurringTermAt(terms, mustDate("2026-06-15")).Amount.Cents())
	assert.Equal(t, int64(1000), recurringTermAt(terms, mustDate("2025-01-01")).Amount.Cents())
	assert.Nil(t, recurringTermAt(nil, mustDate("2026-06-15")))
}

func TestRecurringTermsOverlap(t *testing.T) {
	terms := []models.RecurringSeriesTerm{
		{ID: uuid.New(), StartDate: mustDate("2026-01-01"), EndDate: ptrDate("2026-05-31")},
	}
	// Ranges that intersect.
	assert.True(t, recurringTermsOverlap(terms, mustDate("2026-05-01"), nil, uuid.Nil))
	assert.True(t, recurringTermsOverlap(terms, mustDate("2025-12-01"), ptrDate("2026-01-02"), uuid.Nil))
	// Adjacent ranges (the new end is the existing start, or vice versa) do
	// not overlap because ends are exclusive.
	assert.False(t, recurringTermsOverlap(terms, mustDate("2025-12-01"), ptrDate("2026-01-01"), uuid.Nil))
	assert.False(t, recurringTermsOverlap(terms, mustDate("2026-05-31"), nil, uuid.Nil))
	// Excluding the same term makes it non-conflicting.
	assert.False(t, recurringTermsOverlap(terms, mustDate("2026-02-01"), nil, terms[0].ID))
}

// ---------------------------------------------------------------------------
// GetRecurringSeries
// ---------------------------------------------------------------------------

func TestGetRecurringSeries(t *testing.T) {
	r, _, mock := recurringTestRouter(t)
	accountID := uuid.New()
	start, _ := time.Parse("2006-01-02", "2099-01-15")

	cols := append(append([]string{}, recurringSeriesColumnsForTest...),
		"account_name", "category_name", "category_icon", "category_color", "payee", "attached_count")
	rows := pgxmock.NewRows(cols).
		AddRow(append(recurringSeriesRow(uuid.New(), accountID, "Rent", 50000, recurringFreqMonthly, 1, start, nil),
			"HDFC", "Rent", "home", "#fff", "Landlord", 3)...).
		AddRow(append(recurringSeriesRow(uuid.New(), accountID, "Salary", 100000, recurringFreqMonthly, 1, start, nil),
			"HDFC", "Income", "cash", "#0f0", "Employer", 0)...)

	expectQueryAny(mock, "SELECT rs.id, rs.account_id", 1).WillReturnRows(rows)

	req, _ := http.NewRequest(http.MethodGet, "/recurring", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var res struct {
		Data []models.RecurringSeries `json:"data"`
	}
	assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &res))
	assert.Len(t, res.Data, 2)
	assert.Equal(t, "Rent", res.Data[0].Name)
	assert.Equal(t, 3, res.Data[0].AttachedCount)
	assert.NotNil(t, res.Data[0].NextDueDate)
	assert.Equal(t, "2099-01-15", res.Data[0].NextDueDate.Format("2006-01-02"))
	assert.Equal(t, int64(50000), res.Data[0].MonthlyAmount.Cents())
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetRecurringSeriesError(t *testing.T) {
	r, _, mock := recurringTestRouter(t)
	expectQueryAny(mock, "SELECT rs.id, rs.account_id", 1).WillReturnError(assert.AnError)

	req, _ := http.NewRequest(http.MethodGet, "/recurring", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// ---------------------------------------------------------------------------
// CreateRecurringSeries
// ---------------------------------------------------------------------------

func TestCreateRecurringSeries(t *testing.T) {
	r, _, mock := recurringTestRouter(t)
	id := uuid.New()
	accountID := uuid.New()
	start, _ := time.Parse("2006-01-02", "2099-01-15")
	mock.ExpectBegin()
	expectQueryAny(mock, "INSERT INTO recurring_series", 14).
		WillReturnRows(pgxmock.NewRows(recurringSeriesColumnsForTest).
			AddRow(recurringSeriesRow(id, accountID, "Rent", 50000, recurringFreqMonthly, 1, start, nil)...))
	expectExecAny(mock, "INSERT INTO recurring_series_terms", 6).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectCommit()

	body := `{"accountId":"` + accountID.String() + `","name":"Rent","amount":500,"type":"debit","frequency":"monthly","startDate":"2099-01-15"}`
	req, _ := http.NewRequest(http.MethodPost, "/recurring", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)
	var s models.RecurringSeries
	assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &s))
	assert.Equal(t, id, s.ID)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateRecurringSeriesWithRanges(t *testing.T) {
	r, _, mock := recurringTestRouter(t)
	id, accountA, accountB := uuid.New(), uuid.New(), uuid.New()
	start, _ := time.Parse("2006-01-02", "2099-01-15")

	mock.ExpectBegin()
	expectQueryAny(mock, "INSERT INTO recurring_series", 14).
		WillReturnRows(pgxmock.NewRows(recurringSeriesColumnsForTest).
			AddRow(recurringSeriesRow(id, accountA, "Rent", 1000, recurringFreqMonthly, 1, start, nil)...))
	expectExecAny(mock, "INSERT INTO recurring_series_terms", 6).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	expectExecAny(mock, "INSERT INTO recurring_series_terms", 6).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectCommit()

	body := `{"name":"Rent","type":"debit","frequency":"monthly","startDate":"2099-01-15","ranges":[` +
		`{"startDate":"2099-01-15","endDate":"2099-06-01","amount":10,"accountId":"` + accountA.String() + `"},` +
		`{"startDate":"2099-06-01","amount":12,"accountId":"` + accountB.String() + `"}]}`
	req, _ := http.NewRequest(http.MethodPost, "/recurring", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateRecurringSeriesRangesSameStart(t *testing.T) {
	r, _, mock := recurringTestRouter(t)
	accountA := uuid.New()
	body := `{"name":"Rent","type":"debit","frequency":"monthly","ranges":[` +
		`{"startDate":"2099-05-01","amount":10,"accountId":"` + accountA.String() + `"},` +
		`{"startDate":"2099-05-01","amount":12,"accountId":"` + accountA.String() + `"}]}`
	req, _ := http.NewRequest(http.MethodPost, "/recurring", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestCreateRecurringSeriesRangesWithGap verifies a subscription can be
// discontinued and resumed: ranges may leave gaps, and the series period is
// derived from the earliest start and latest end.
func TestCreateRecurringSeriesRangesWithGap(t *testing.T) {
	r, _, mock := recurringTestRouter(t)
	id, accountA, accountB := uuid.New(), uuid.New(), uuid.New()
	// Supplied out of order; a gap between March and June is allowed.
	mock.ExpectBegin()
	expectQueryAny(mock, "INSERT INTO recurring_series", 14).
		WillReturnRows(pgxmock.NewRows(recurringSeriesColumnsForTest).
			AddRow(recurringSeriesRow(id, accountA, "Rent", 1000, recurringFreqMonthly, 1, mustDate("2099-01-15"), nil)...))
	expectExecAny(mock, "INSERT INTO recurring_series_terms", 6).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	expectExecAny(mock, "INSERT INTO recurring_series_terms", 6).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectCommit()

	body := `{"name":"Rent","type":"debit","frequency":"monthly","ranges":[` +
		`{"startDate":"2099-06-01","amount":12,"accountId":"` + accountB.String() + `"},` +
		`{"startDate":"2099-01-15","endDate":"2099-03-01","amount":10,"accountId":"` + accountA.String() + `"}]}`
	req, _ := http.NewRequest(http.MethodPost, "/recurring", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateRecurringSeriesReferencedNotFound(t *testing.T) {
	r, _, mock := recurringTestRouter(t)
	mock.ExpectBegin()
	expectQueryAny(mock, "INSERT INTO recurring_series", 14).WillReturnError(pgx.ErrNoRows)
	mock.ExpectRollback()

	body := `{"accountId":"` + uuid.New().String() + `","name":"Rent","amount":500,"type":"debit","frequency":"monthly","startDate":"2099-01-15"}`
	req, _ := http.NewRequest(http.MethodPost, "/recurring", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateRecurringSeriesValidation(t *testing.T) {
	cases := map[string]string{
		"bad type":      `{"accountId":"` + uuid.New().String() + `","name":"x","amount":5,"type":"nope","frequency":"monthly","startDate":"2099-01-15"}`,
		"bad amount":    `{"accountId":"` + uuid.New().String() + `","name":"x","amount":0,"type":"debit","frequency":"monthly","startDate":"2099-01-15"}`,
		"bad frequency": `{"accountId":"` + uuid.New().String() + `","name":"x","amount":5,"type":"debit","frequency":"hourly","startDate":"2099-01-15"}`,
		"bad interval":  `{"accountId":"` + uuid.New().String() + `","name":"x","amount":5,"type":"debit","frequency":"monthly","interval":400,"startDate":"2099-01-15"}`,
		"bad date":      `{"accountId":"` + uuid.New().String() + `","name":"x","amount":5,"type":"debit","frequency":"monthly","startDate":"not-a-date"}`,
		"bad end date":  `{"accountId":"` + uuid.New().String() + `","name":"x","amount":5,"type":"debit","frequency":"monthly","startDate":"2099-01-15","endDate":"2098-01-15"}`,
		"missing value": `{"name":"x","type":"debit","frequency":"monthly","startDate":"2099-01-15"}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			r, _, mock := recurringTestRouter(t)
			req, _ := http.NewRequest(http.MethodPost, "/recurring", bytes.NewBufferString(body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			assert.Equal(t, http.StatusBadRequest, w.Code, body)
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

// ---------------------------------------------------------------------------
// UpdateRecurringSeries
// ---------------------------------------------------------------------------

func TestUpdateRecurringSeries(t *testing.T) {
	r, _, mock := recurringTestRouter(t)
	id := uuid.New()
	accountID := uuid.New()
	start, _ := time.Parse("2006-01-02", "2099-01-15")

	expectQueryAny(mock, "FROM recurring_series WHERE id", 2).
		WillReturnRows(pgxmock.NewRows(recurringSeriesColumnsForTest).
			AddRow(recurringSeriesRow(id, accountID, "Old", 1000, recurringFreqMonthly, 1, start, nil)...))
	expectQueryAny(mock, "FROM recurring_series_terms t JOIN accounts a", 2).
		WillReturnRows(pgxmock.NewRows(recurringTermLoadCols).
			AddRow(recurringTermRow(uuid.New(), id, start, 1000, accountID)...))
	// The amount changes at the series start, so the covering range is edited
	// in place, then the cache and series row are refreshed.
	mock.ExpectBegin()
	expectExecAny(mock, "UPDATE recurring_series_terms t SET", 4).
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	expectExecAny(mock, "UPDATE recurring_series rs SET", 2).
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	expectQueryAny(mock, "UPDATE recurring_series SET", 13).
		WillReturnRows(pgxmock.NewRows(recurringSeriesColumnsForTest).
			AddRow(recurringSeriesRow(id, accountID, "New", 2000, recurringFreqWeekly, 2, start, nil)...))
	mock.ExpectCommit()

	body := `{"name":"New","amount":20,"frequency":"weekly","interval":2}`
	req, _ := http.NewRequest(http.MethodPut, "/recurring/"+id.String(), bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var s models.RecurringSeries
	assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &s))
	assert.Equal(t, "New", s.Name)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateRecurringSeriesClearsEndDate(t *testing.T) {
	r, _, mock := recurringTestRouter(t)
	id := uuid.New()
	accountID := uuid.New()
	start, _ := time.Parse("2006-01-02", "2099-01-15")
	end, _ := time.Parse("2006-01-02", "2099-06-15")

	expectQueryAny(mock, "FROM recurring_series WHERE id", 2).
		WillReturnRows(pgxmock.NewRows(recurringSeriesColumnsForTest).
			AddRow(recurringSeriesRow(id, accountID, "Old", 1000, recurringFreqMonthly, 1, start, &end)...))
	expectQueryAny(mock, "FROM recurring_series_terms t JOIN accounts a", 2).
		WillReturnRows(pgxmock.NewRows(recurringTermLoadCols).
			AddRow(recurringTermRow(uuid.New(), id, start, 1000, accountID)...))
	// Only the end date changes, so no range is written.
	mock.ExpectBegin()
	expectExecAny(mock, "UPDATE recurring_series rs SET", 2).
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	expectQueryAny(mock, "UPDATE recurring_series SET", 13).
		WillReturnRows(pgxmock.NewRows(recurringSeriesColumnsForTest).
			AddRow(recurringSeriesRow(id, accountID, "Old", 1000, recurringFreqMonthly, 1, start, nil)...))
	mock.ExpectCommit()

	req, _ := http.NewRequest(http.MethodPut, "/recurring/"+id.String(), bytes.NewBufferString(`{"endDate":""}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateRecurringSeriesNotFound(t *testing.T) {
	r, _, mock := recurringTestRouter(t)
	expectQueryAny(mock, "FROM recurring_series WHERE id", 2).WillReturnError(pgx.ErrNoRows)

	req, _ := http.NewRequest(http.MethodPut, "/recurring/"+uuid.New().String(), bytes.NewBufferString(`{"name":"x"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateRecurringSeriesValidation(t *testing.T) {
	cases := map[string]string{
		"empty name":    `{"name":"   "}`,
		"bad amount":    `{"amount":0}`,
		"bad type":      `{"type":"x"}`,
		"bad frequency": `{"frequency":"hourly"}`,
		"bad interval":  `{"interval":0}`,
		"bad date":      `{"startDate":"nope"}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			r, _, mock := recurringTestRouter(t)
			id := uuid.New()
			start, _ := time.Parse("2006-01-02", "2099-01-15")
			expectQueryAny(mock, "FROM recurring_series WHERE id", 2).
				WillReturnRows(pgxmock.NewRows(recurringSeriesColumnsForTest).
					AddRow(recurringSeriesRow(id, uuid.New(), "Old", 1000, recurringFreqMonthly, 1, start, nil)...))

			req, _ := http.NewRequest(http.MethodPut, "/recurring/"+id.String(), bytes.NewBufferString(body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			assert.Equal(t, http.StatusBadRequest, w.Code, body)
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestUpdateRecurringSeriesReplacesRanges(t *testing.T) {
	r, _, mock := recurringTestRouter(t)
	id, accountA, accountB := uuid.New(), uuid.New(), uuid.New()
	start, _ := time.Parse("2006-01-02", "2099-01-15")

	expectQueryAny(mock, "FROM recurring_series WHERE id", 2).
		WillReturnRows(pgxmock.NewRows(recurringSeriesColumnsForTest).
			AddRow(recurringSeriesRow(id, accountA, "Rent", 1000, recurringFreqMonthly, 1, start, nil)...))
	expectQueryAny(mock, "FROM recurring_series_terms t JOIN accounts a", 2).
		WillReturnRows(pgxmock.NewRows(recurringTermLoadCols).
			AddRow(recurringTermRow(uuid.New(), id, start, 1000, accountA)...))
	mock.ExpectBegin()
	expectExecAny(mock, "DELETE FROM recurring_series_terms", 2).
		WillReturnResult(pgxmock.NewResult("DELETE", 1))
	expectExecAny(mock, "INSERT INTO recurring_series_terms", 6).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	expectExecAny(mock, "INSERT INTO recurring_series_terms", 6).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	expectExecAny(mock, "UPDATE recurring_series rs SET", 2).
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	expectQueryAny(mock, "UPDATE recurring_series SET", 13).
		WillReturnRows(pgxmock.NewRows(recurringSeriesColumnsForTest).
			AddRow(recurringSeriesRow(id, accountA, "Rent", 1000, recurringFreqMonthly, 1, start, nil)...))
	mock.ExpectCommit()

	body := `{"ranges":[` +
		`{"startDate":"2099-01-15","endDate":"2099-06-01","amount":10,"accountId":"` + accountA.String() + `"},` +
		`{"startDate":"2099-06-01","amount":12,"accountId":"` + accountB.String() + `"}]}`
	req, _ := http.NewRequest(http.MethodPut, "/recurring/"+id.String(), bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateRecurringSeriesRangesSameStart(t *testing.T) {
	r, _, mock := recurringTestRouter(t)
	id, accountA := uuid.New(), uuid.New()
	start, _ := time.Parse("2006-01-02", "2099-01-15")
	expectQueryAny(mock, "FROM recurring_series WHERE id", 2).
		WillReturnRows(pgxmock.NewRows(recurringSeriesColumnsForTest).
			AddRow(recurringSeriesRow(id, accountA, "Rent", 1000, recurringFreqMonthly, 1, start, nil)...))

	body := `{"ranges":[` +
		`{"startDate":"2099-01-15","amount":10,"accountId":"` + accountA.String() + `"},` +
		`{"startDate":"2099-01-15","amount":12,"accountId":"` + accountA.String() + `"}]}`
	req, _ := http.NewRequest(http.MethodPut, "/recurring/"+id.String(), bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// ---------------------------------------------------------------------------
// DeleteRecurringSeries
// ---------------------------------------------------------------------------
func TestDeleteRecurringSeries(t *testing.T) {
	r, _, mock := recurringTestRouter(t)
	expectExecAny(mock, "DELETE FROM recurring_series WHERE id", 2).
		WillReturnResult(pgxmock.NewResult("DELETE", 1))

	req, _ := http.NewRequest(http.MethodDelete, "/recurring/"+uuid.New().String(), nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestDeleteRecurringSeriesNotFound(t *testing.T) {
	r, _, mock := recurringTestRouter(t)
	expectExecAny(mock, "DELETE FROM recurring_series WHERE id", 2).
		WillReturnResult(pgxmock.NewResult("DELETE", 0))

	req, _ := http.NewRequest(http.MethodDelete, "/recurring/"+uuid.New().String(), nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// ---------------------------------------------------------------------------
// GetRecurringForecast
// ---------------------------------------------------------------------------

func TestGetRecurringForecast(t *testing.T) {
	r, _, mock := recurringTestRouter(t)
	id := uuid.New()
	accountID := uuid.New()
	start, _ := time.Parse("2006-01-02", "2099-01-15")
	change, _ := time.Parse("2006-01-02", "2099-02-15")
	matchedDate, _ := time.Parse("2006-01-02", "2099-01-15")

	expectQueryAny(mock, "FROM recurring_series WHERE id", 2).
		WillReturnRows(pgxmock.NewRows(recurringSeriesColumnsForTest).
			AddRow(recurringSeriesRow(id, accountID, "Rent", 50000, recurringFreqMonthly, 1, start, nil)...))
	expectQueryAny(mock, "SELECT t.date FROM recurring_attachments ra", 2).
		WillReturnRows(pgxmock.NewRows([]string{"date"}).AddRow(matchedDate))
	// A price change effective Feb means later occurrences use the new amount.
	expectQueryAny(mock, "FROM recurring_series_terms t JOIN accounts a", 2).
		WillReturnRows(pgxmock.NewRows(recurringTermLoadCols).
			AddRow(recurringTermRowEnd(uuid.New(), id, start, mustDate("2099-02-14"), 50000, accountID)...).
			AddRow(recurringTermRow(uuid.New(), id, change, 60000, accountID)...))

	req, _ := http.NewRequest(http.MethodGet, "/recurring/"+id.String()+"/forecast?count=3", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var res struct {
		Data []models.RecurringForecastItem `json:"data"`
	}
	assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &res))
	assert.Len(t, res.Data, 3)
	assert.True(t, res.Data[0].Matched)
	assert.False(t, res.Data[1].Matched)
	assert.Equal(t, int64(50000), res.Data[0].Amount.Cents())
	assert.Equal(t, int64(60000), res.Data[1].Amount.Cents())
	assert.Equal(t, int64(60000), res.Data[2].Amount.Cents())
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetRecurringForecastNotFound(t *testing.T) {
	r, _, mock := recurringTestRouter(t)
	expectQueryAny(mock, "FROM recurring_series WHERE id", 2).WillReturnError(pgx.ErrNoRows)

	req, _ := http.NewRequest(http.MethodGet, "/recurring/"+uuid.New().String()+"/forecast", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// ---------------------------------------------------------------------------
// GetRecurringSuggestions
// ---------------------------------------------------------------------------

func TestGetRecurringSuggestions(t *testing.T) {
	r, _, mock := recurringTestRouter(t)
	id := uuid.New()
	accountID := uuid.New()
	start, _ := time.Parse("2006-01-02", "2099-01-15")
	candDate, _ := time.Parse("2006-01-02", "2099-01-16")

	expectQueryAny(mock, "FROM recurring_series WHERE id", 2).
		WillReturnRows(pgxmock.NewRows(recurringSeriesColumnsForTest).
			AddRow(recurringSeriesRow(id, accountID, "Rent", 50000, recurringFreqMonthly, 1, start, nil)...))
	expectQueryAny(mock, "FROM recurring_series_terms t JOIN accounts a", 2).
		WillReturnRows(pgxmock.NewRows(recurringTermLoadCols).
			AddRow(recurringTermRow(uuid.New(), id, start, 50000, accountID)...))
	expectQueryAny(mock, "AND t.amount = ", 7).
		WillReturnRows(pgxmock.NewRows([]string{
			"id", "account_id", "date", "description", "amount", "type", "category_id", "payee_id", "account_name",
		}).AddRow(uuid.New(), accountID, candDate, "RENT PAYMENT", int64(50000), "debit", nil, nil, "HDFC"))

	req, _ := http.NewRequest(http.MethodGet, "/recurring/"+id.String()+"/suggestions", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var res struct {
		Data []models.RecurringSuggestion `json:"data"`
	}
	assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &res))
	assert.Len(t, res.Data, 1)
	assert.Equal(t, "RENT PAYMENT", res.Data[0].Txn.Description)
	assert.Equal(t, "2099-01-15", res.Data[0].OccurrenceDate.Format("2006-01-02"))
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetRecurringSuggestionsSkipsOutsideRange(t *testing.T) {
	r, _, mock := recurringTestRouter(t)
	id := uuid.New()
	accountID := uuid.New()
	start, _ := time.Parse("2006-01-02", "2099-01-15")
	// The candidate falls after the only (closed) range ends.
	expectQueryAny(mock, "FROM recurring_series WHERE id", 2).
		WillReturnRows(pgxmock.NewRows(recurringSeriesColumnsForTest).
			AddRow(recurringSeriesRow(id, accountID, "Rent", 50000, recurringFreqMonthly, 1, start, nil)...))
	expectQueryAny(mock, "FROM recurring_series_terms t JOIN accounts a", 2).
		WillReturnRows(pgxmock.NewRows(recurringTermLoadCols).
			AddRow(recurringTermRowEnd(uuid.New(), id, start, mustDate("2099-01-31"), 50000, accountID)...))
	expectQueryAny(mock, "AND t.amount = ", 7).
		WillReturnRows(pgxmock.NewRows([]string{
			"id", "account_id", "date", "description", "amount", "type", "category_id", "payee_id", "account_name",
		}).AddRow(uuid.New(), accountID, mustDate("2099-03-15"), "RENT PAYMENT", int64(50000), "debit", nil, nil, "HDFC"))

	req, _ := http.NewRequest(http.MethodGet, "/recurring/"+id.String()+"/suggestions", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var res struct {
		Data []models.RecurringSuggestion `json:"data"`
	}
	assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &res))
	assert.Empty(t, res.Data)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestGetRecurringSuggestionsSpansRanges guards the regression where the
// candidate query was capped at the newest rows, so older ranges produced no
// suggestions. The query must span the earliest range start, and a candidate
// from an older range must be returned.
func TestGetRecurringSuggestionsSpansRanges(t *testing.T) {
	r, _, mock := recurringTestRouter(t)
	id := uuid.New()
	accountA, accountB := uuid.New(), uuid.New()
	startA, _ := time.Parse("2006-01-02", "2024-01-01")
	startB, _ := time.Parse("2006-01-02", "2024-07-01")

	expectQueryAny(mock, "FROM recurring_series WHERE id", 2).
		WillReturnRows(pgxmock.NewRows(recurringSeriesColumnsForTest).
			AddRow(recurringSeriesRow(id, accountB, "Rent", 2000, recurringFreqMonthly, 1, startA, nil)...))
	expectQueryAny(mock, "FROM recurring_series_terms t JOIN accounts a", 2).
		WillReturnRows(pgxmock.NewRows(recurringTermLoadCols).
			AddRow(recurringTermRowEnd(uuid.New(), id, startA, startB, 1000, accountA)...).
			AddRow(recurringTermRow(uuid.New(), id, startB, 2000, accountB)...))

	// The date-spanning query starts at the earliest range start (2024-01-01),
	// has no upper bound (the last range is open), and uses the large safety cap
	// rather than a recency-limited one.
	mock.ExpectQuery("AND t.amount = ").WithArgs(
		testUserID(), pgxmock.AnyArg(), "debit", pgxmock.AnyArg(),
		startA, (*time.Time)(nil), maxRecurringCandidates,
	).WillReturnRows(pgxmock.NewRows([]string{
		"id", "account_id", "date", "description", "amount", "type", "category_id", "payee_id", "account_name",
	}).AddRow(uuid.New(), accountA, mustDate("2024-02-15"), "OLD RENT", int64(1000), "debit", nil, nil, "HDFC"))

	req, _ := http.NewRequest(http.MethodGet, "/recurring/"+id.String()+"/suggestions", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var res struct {
		Data []models.RecurringSuggestion `json:"data"`
	}
	assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &res))
	assert.Len(t, res.Data, 1)
	assert.Equal(t, "OLD RENT", res.Data[0].Txn.Description)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestGetRecurringSuggestionsKeepsEveryRange guards the regression where an
// older amount range produced no suggestions because the newer range's many
// candidates filled the result page. The page must include the best candidate
// from every range.
func TestGetRecurringSuggestionsKeepsEveryRange(t *testing.T) {
	r, _, mock := recurringTestRouter(t)
	id := uuid.New()
	accountOld, accountNew := uuid.New(), uuid.New()
	startOld, _ := time.Parse("2006-01-02", "2024-01-15")
	startNew, _ := time.Parse("2006-01-02", "2024-04-15")
	termOld, termNew := uuid.New(), uuid.New()

	expectQueryAny(mock, "FROM recurring_series WHERE id", 2).
		WillReturnRows(pgxmock.NewRows(recurringSeriesColumnsForTest).
			AddRow(recurringSeriesRow(id, accountNew, "Rent", 20000, recurringFreqMonthly, 1, startOld, nil)...))
	expectQueryAny(mock, "FROM recurring_series_terms t JOIN accounts a", 2).
		WillReturnRows(pgxmock.NewRows(recurringTermLoadCols).
			AddRow(recurringTermRowEnd(termOld, id, startOld, startNew, 18900, accountOld)...).
			AddRow(recurringTermRow(termNew, id, startNew, 20000, accountNew)...))

	candidateCols := []string{
		"id", "account_id", "date", "description", "amount", "type", "category_id", "payee_id", "account_name",
	}
	// One older-range candidate and several newer-range ones (all on occurrence
	// dates, so all tie at the top score).
	expectQueryAny(mock, "AND t.amount = ", 7).
		WillReturnRows(pgxmock.NewRows(candidateCols).
			AddRow(uuid.New(), accountNew, mustDate("2024-06-15"), "NEW A", int64(20000), "debit", nil, nil, "HDFC").
			AddRow(uuid.New(), accountNew, mustDate("2024-05-15"), "NEW B", int64(20000), "debit", nil, nil, "HDFC").
			AddRow(uuid.New(), accountNew, mustDate("2024-04-15"), "NEW C", int64(20000), "debit", nil, nil, "HDFC").
			AddRow(uuid.New(), accountOld, mustDate("2024-02-15"), "OLD 189", int64(18900), "debit", nil, nil, "HDFC"))

	req, _ := http.NewRequest(http.MethodGet, "/recurring/"+id.String()+"/suggestions?limit=2", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var res struct {
		Data []models.RecurringSuggestion `json:"data"`
	}
	assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &res))
	assert.Len(t, res.Data, 2)
	amounts := []int64{res.Data[0].Txn.Amount.Cents(), res.Data[1].Txn.Amount.Cents()}
	assert.Contains(t, amounts, int64(18900), "the older range must be represented")
	assert.Contains(t, amounts, int64(20000))
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetRecurringSuggestionsNotFound(t *testing.T) {
	r, _, mock := recurringTestRouter(t)
	expectQueryAny(mock, "FROM recurring_series WHERE id", 2).WillReturnError(pgx.ErrNoRows)

	req, _ := http.NewRequest(http.MethodGet, "/recurring/"+uuid.New().String()+"/suggestions", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetRecurringSuggestionsQueryError(t *testing.T) {
	r, _, mock := recurringTestRouter(t)
	id := uuid.New()
	start, _ := time.Parse("2006-01-02", "2099-01-15")
	expectQueryAny(mock, "FROM recurring_series WHERE id", 2).
		WillReturnRows(pgxmock.NewRows(recurringSeriesColumnsForTest).
			AddRow(recurringSeriesRow(id, uuid.New(), "Rent", 50000, recurringFreqMonthly, 1, start, nil)...))
	expectQueryAny(mock, "FROM recurring_series_terms t JOIN accounts a", 2).
		WillReturnRows(pgxmock.NewRows(recurringTermLoadCols).
			AddRow(recurringTermRow(uuid.New(), id, start, 50000, uuid.New())...))
	expectQueryAny(mock, "AND t.amount = ", 7).WillReturnError(assert.AnError)

	req, _ := http.NewRequest(http.MethodGet, "/recurring/"+id.String()+"/suggestions", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// ---------------------------------------------------------------------------
// GetRecurringTransactions
// ---------------------------------------------------------------------------

func TestGetRecurringTransactions(t *testing.T) {
	r, _, mock := recurringTestRouter(t)
	id := uuid.New()
	start, _ := time.Parse("2006-01-02", "2099-01-15")
	now := time.Now()

	expectQueryAny(mock, "FROM recurring_series WHERE id", 2).
		WillReturnRows(pgxmock.NewRows(recurringSeriesColumnsForTest).
			AddRow(recurringSeriesRow(id, uuid.New(), "Rent", 50000, recurringFreqMonthly, 1, start, nil)...))
	expectQueryAny(mock, "SELECT t.id, t.account_id, t.date, t.description", 2).
		WillReturnRows(pgxmock.NewRows([]string{
			"id", "account_id", "date", "description", "amount", "type", "category_id",
			"tags", "notes", "payee_id", "payee", "created_at", "account_name",
			"category_name", "category_icon", "category_color", "billing_cycle_id", "billing_cycle_label",
		}).AddRow(uuid.New(), uuid.New(), now, "RENT", int64(50000), "debit", nil,
			[]string{}, "", nil, "", now, "HDFC", "", "", "", nil, ""))

	req, _ := http.NewRequest(http.MethodGet, "/recurring/"+id.String()+"/transactions", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var res struct {
		Data []models.Transaction `json:"data"`
	}
	assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &res))
	assert.Len(t, res.Data, 1)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetRecurringTransactionsNotFound(t *testing.T) {
	r, _, mock := recurringTestRouter(t)
	expectQueryAny(mock, "FROM recurring_series WHERE id", 2).WillReturnError(pgx.ErrNoRows)

	req, _ := http.NewRequest(http.MethodGet, "/recurring/"+uuid.New().String()+"/transactions", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// ---------------------------------------------------------------------------
// AttachRecurring / DetachRecurring
// ---------------------------------------------------------------------------

func TestAttachRecurring(t *testing.T) {
	r, _, mock := recurringTestRouter(t)
	seriesID := uuid.New()
	ids := []uuid.UUID{uuid.New(), uuid.New()}

	expectQueryAny(mock, "SELECT EXISTS", 2).
		WillReturnRows(pgxmock.NewRows([]string{"exists"}).AddRow(true))
	expectQueryAny(mock, "FROM transactions WHERE id = ANY", 2).
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(2))
	expectQueryAny(mock, "FROM recurring_attachments WHERE transaction_id = ANY", 2).
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(0))
	expectExecAny(mock, "INSERT INTO recurring_attachments", 3).
		WillReturnResult(pgxmock.NewResult("INSERT", 2))

	body, _ := json.Marshal(models.RecurringAttachRequest{SeriesID: seriesID, TransactionIDs: ids})
	req, _ := http.NewRequest(http.MethodPost, "/recurring/attach", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var res struct {
		Attached int64 `json:"attached"`
	}
	assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &res))
	assert.Equal(t, int64(2), res.Attached)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestAttachRecurringNoIDs(t *testing.T) {
	r, _, mock := recurringTestRouter(t)
	req, _ := http.NewRequest(http.MethodPost, "/recurring/attach", bytes.NewBufferString(`{"transactionIds":[]}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestAttachRecurringTooManyIDs(t *testing.T) {
	r, _, mock := recurringTestRouter(t)
	ids := make([]uuid.UUID, maxBulkBatch+1)
	for i := range ids {
		ids[i] = uuid.New()
	}
	body, _ := json.Marshal(models.RecurringAttachRequest{SeriesID: uuid.New(), TransactionIDs: ids})
	req, _ := http.NewRequest(http.MethodPost, "/recurring/attach", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestAttachRecurringSeriesNotFound(t *testing.T) {
	r, _, mock := recurringTestRouter(t)
	expectQueryAny(mock, "SELECT EXISTS", 2).
		WillReturnRows(pgxmock.NewRows([]string{"exists"}).AddRow(false))

	body, _ := json.Marshal(models.RecurringAttachRequest{SeriesID: uuid.New(), TransactionIDs: []uuid.UUID{uuid.New()}})
	req, _ := http.NewRequest(http.MethodPost, "/recurring/attach", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestAttachRecurringTransactionsNotFound(t *testing.T) {
	r, _, mock := recurringTestRouter(t)
	expectQueryAny(mock, "SELECT EXISTS", 2).
		WillReturnRows(pgxmock.NewRows([]string{"exists"}).AddRow(true))
	expectQueryAny(mock, "FROM transactions WHERE id = ANY", 2).
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(0))

	body, _ := json.Marshal(models.RecurringAttachRequest{SeriesID: uuid.New(), TransactionIDs: []uuid.UUID{uuid.New()}})
	req, _ := http.NewRequest(http.MethodPost, "/recurring/attach", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestAttachRecurringAlreadyAttached(t *testing.T) {
	r, _, mock := recurringTestRouter(t)
	expectQueryAny(mock, "SELECT EXISTS", 2).
		WillReturnRows(pgxmock.NewRows([]string{"exists"}).AddRow(true))
	expectQueryAny(mock, "FROM transactions WHERE id = ANY", 2).
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(1))
	expectQueryAny(mock, "FROM recurring_attachments WHERE transaction_id = ANY", 2).
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(1))

	body, _ := json.Marshal(models.RecurringAttachRequest{SeriesID: uuid.New(), TransactionIDs: []uuid.UUID{uuid.New()}})
	req, _ := http.NewRequest(http.MethodPost, "/recurring/attach", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusConflict, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestAttachRecurringUniqueViolation(t *testing.T) {
	r, _, mock := recurringTestRouter(t)
	expectQueryAny(mock, "SELECT EXISTS", 2).
		WillReturnRows(pgxmock.NewRows([]string{"exists"}).AddRow(true))
	expectQueryAny(mock, "FROM transactions WHERE id = ANY", 2).
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(1))
	expectQueryAny(mock, "FROM recurring_attachments WHERE transaction_id = ANY", 2).
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(0))
	expectExecAny(mock, "INSERT INTO recurring_attachments", 3).
		WillReturnError(&pgconn.PgError{Code: "23505"})

	body, _ := json.Marshal(models.RecurringAttachRequest{SeriesID: uuid.New(), TransactionIDs: []uuid.UUID{uuid.New()}})
	req, _ := http.NewRequest(http.MethodPost, "/recurring/attach", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusConflict, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestAttachRecurringInsertError(t *testing.T) {
	r, _, mock := recurringTestRouter(t)
	expectQueryAny(mock, "SELECT EXISTS", 2).
		WillReturnRows(pgxmock.NewRows([]string{"exists"}).AddRow(true))
	expectQueryAny(mock, "FROM transactions WHERE id = ANY", 2).
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(1))
	expectQueryAny(mock, "FROM recurring_attachments WHERE transaction_id = ANY", 2).
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(0))
	expectExecAny(mock, "INSERT INTO recurring_attachments", 3).WillReturnError(assert.AnError)

	body, _ := json.Marshal(models.RecurringAttachRequest{SeriesID: uuid.New(), TransactionIDs: []uuid.UUID{uuid.New()}})
	req, _ := http.NewRequest(http.MethodPost, "/recurring/attach", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestDetachRecurring(t *testing.T) {
	r, _, mock := recurringTestRouter(t)
	ids := []uuid.UUID{uuid.New(), uuid.New()}
	expectExecAny(mock, "DELETE FROM recurring_attachments WHERE", 2).
		WillReturnResult(pgxmock.NewResult("DELETE", 2))

	body, _ := json.Marshal(models.RecurringDetachRequest{TransactionIDs: ids})
	req, _ := http.NewRequest(http.MethodPost, "/recurring/detach", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var res struct {
		Detached int64 `json:"detached"`
	}
	assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &res))
	assert.Equal(t, int64(2), res.Detached)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestDetachRecurringNoIDs(t *testing.T) {
	r, _, mock := recurringTestRouter(t)
	req, _ := http.NewRequest(http.MethodPost, "/recurring/detach", bytes.NewBufferString(`{"transactionIds":[]}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestDetachRecurringError(t *testing.T) {
	r, _, mock := recurringTestRouter(t)
	expectExecAny(mock, "DELETE FROM recurring_attachments WHERE", 2).WillReturnError(assert.AnError)

	body, _ := json.Marshal(models.RecurringDetachRequest{TransactionIDs: []uuid.UUID{uuid.New()}})
	req, _ := http.NewRequest(http.MethodPost, "/recurring/detach", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// ---------------------------------------------------------------------------
// Recurring series terms (date ranges)
// ---------------------------------------------------------------------------

func TestGetRecurringTerms(t *testing.T) {
	r, _, mock := recurringTestRouter(t)
	id := uuid.New()
	accountID := uuid.New()
	start, _ := time.Parse("2006-01-02", "2099-01-15")

	expectQueryAny(mock, "FROM recurring_series WHERE id", 2).
		WillReturnRows(pgxmock.NewRows(recurringSeriesColumnsForTest).
			AddRow(recurringSeriesRow(id, accountID, "Rent", 1000, recurringFreqMonthly, 1, start, nil)...))
	expectQueryAny(mock, "FROM recurring_series_terms t JOIN accounts a", 2).
		WillReturnRows(pgxmock.NewRows(recurringTermLoadCols).
			AddRow(recurringTermRow(uuid.New(), id, start, 1000, accountID)...))

	req, _ := http.NewRequest(http.MethodGet, "/recurring/"+id.String()+"/terms", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var res struct {
		Data []models.RecurringSeriesTerm `json:"data"`
	}
	assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &res))
	assert.Len(t, res.Data, 1)
	assert.Equal(t, "Checking", res.Data[0].AccountName)
	assert.Nil(t, res.Data[0].EndDate)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetRecurringTermsNotFound(t *testing.T) {
	r, _, mock := recurringTestRouter(t)
	expectQueryAny(mock, "FROM recurring_series WHERE id", 2).WillReturnError(pgx.ErrNoRows)

	req, _ := http.NewRequest(http.MethodGet, "/recurring/"+uuid.New().String()+"/terms", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateRecurringTerm(t *testing.T) {
	r, _, mock := recurringTestRouter(t)
	id, accountID := uuid.New(), uuid.New()
	start, _ := time.Parse("2006-01-02", "2099-01-15")
	rangeStart, _ := time.Parse("2006-01-02", "2099-06-01")
	rangeEnd, _ := time.Parse("2006-01-02", "2099-12-31")

	expectQueryAny(mock, "FROM recurring_series WHERE id", 2).
		WillReturnRows(pgxmock.NewRows(recurringSeriesColumnsForTest).
			AddRow(recurringSeriesRow(id, accountID, "Rent", 1000, recurringFreqMonthly, 1, start, nil)...))
	expectQueryAny(mock, "FROM recurring_series_terms t JOIN accounts a", 2).
		WillReturnRows(pgxmock.NewRows(recurringTermLoadCols).
			AddRow(recurringTermRowEnd(uuid.New(), id, start, start, 1000, accountID)...))
	mock.ExpectBegin()
	expectQueryAny(mock, "INSERT INTO recurring_series_terms", 6).
		WillReturnRows(pgxmock.NewRows(recurringTermReturnCols).
			AddRow(uuid.New(), id, rangeStart, &rangeEnd, int64(2000), accountID, time.Now()))
	expectExecAny(mock, "UPDATE recurring_series rs SET", 2).
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectCommit()

	body := `{"startDate":"2099-06-01","endDate":"2099-12-31","amount":20,"accountId":"` + accountID.String() + `"}`
	req, _ := http.NewRequest(http.MethodPut, "/recurring/"+id.String()+"/terms", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)
	var term models.RecurringSeriesTerm
	assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &term))
	assert.Equal(t, int64(2000), term.Amount.Cents())
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateRecurringTermValidation(t *testing.T) {
	cases := map[string]string{
		"bad amount":       `{"startDate":"2099-06-01","amount":0,"accountId":"` + uuid.New().String() + `"}`,
		"bad date":         `{"startDate":"nope","amount":10,"accountId":"` + uuid.New().String() + `"}`,
		"end before start": `{"startDate":"2099-06-01","endDate":"2099-01-01","amount":10,"accountId":"` + uuid.New().String() + `"}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			r, _, mock := recurringTestRouter(t)
			req, _ := http.NewRequest(http.MethodPut, "/recurring/"+uuid.New().String()+"/terms", bytes.NewBufferString(body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			assert.Equal(t, http.StatusBadRequest, w.Code, body)
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestCreateRecurringTermOverlap(t *testing.T) {
	r, _, mock := recurringTestRouter(t)
	id, accountID := uuid.New(), uuid.New()
	start, _ := time.Parse("2006-01-02", "2099-01-15")

	expectQueryAny(mock, "FROM recurring_series WHERE id", 2).
		WillReturnRows(pgxmock.NewRows(recurringSeriesColumnsForTest).
			AddRow(recurringSeriesRow(id, accountID, "Rent", 1000, recurringFreqMonthly, 1, start, nil)...))
	// The existing entry is open-ended, so the new range overlaps it.
	expectQueryAny(mock, "FROM recurring_series_terms t JOIN accounts a", 2).
		WillReturnRows(pgxmock.NewRows(recurringTermLoadCols).
			AddRow(recurringTermRow(uuid.New(), id, start, 1000, accountID)...))

	body := `{"startDate":"2099-06-01","amount":10,"accountId":"` + accountID.String() + `"}`
	req, _ := http.NewRequest(http.MethodPut, "/recurring/"+id.String()+"/terms", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateRecurringTermSeriesNotFound(t *testing.T) {
	r, _, mock := recurringTestRouter(t)
	expectQueryAny(mock, "FROM recurring_series WHERE id", 2).WillReturnError(pgx.ErrNoRows)

	body := `{"startDate":"2099-06-01","amount":10,"accountId":"` + uuid.New().String() + `"}`
	req, _ := http.NewRequest(http.MethodPut, "/recurring/"+uuid.New().String()+"/terms", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateRecurringTermAccountNotFound(t *testing.T) {
	r, _, mock := recurringTestRouter(t)
	id, accountID := uuid.New(), uuid.New()
	start, _ := time.Parse("2006-01-02", "2099-01-15")

	expectQueryAny(mock, "FROM recurring_series WHERE id", 2).
		WillReturnRows(pgxmock.NewRows(recurringSeriesColumnsForTest).
			AddRow(recurringSeriesRow(id, accountID, "Rent", 1000, recurringFreqMonthly, 1, start, nil)...))
	expectQueryAny(mock, "FROM recurring_series_terms t JOIN accounts a", 2).
		WillReturnRows(pgxmock.NewRows(recurringTermLoadCols).
			AddRow(recurringTermRowEnd(uuid.New(), id, start, start, 1000, accountID)...))
	mock.ExpectBegin()
	expectQueryAny(mock, "INSERT INTO recurring_series_terms", 6).WillReturnError(pgx.ErrNoRows)
	mock.ExpectRollback()

	body := `{"startDate":"2099-06-01","amount":10,"accountId":"` + uuid.New().String() + `"}`
	req, _ := http.NewRequest(http.MethodPut, "/recurring/"+id.String()+"/terms", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateRecurringTerm(t *testing.T) {
	r, _, mock := recurringTestRouter(t)
	id, accountID, termID := uuid.New(), uuid.New(), uuid.New()
	start, _ := time.Parse("2006-01-02", "2099-01-15")
	newStart, _ := time.Parse("2006-01-02", "2099-02-01")

	expectQueryAny(mock, "FROM recurring_series WHERE id", 2).
		WillReturnRows(pgxmock.NewRows(recurringSeriesColumnsForTest).
			AddRow(recurringSeriesRow(id, accountID, "Rent", 1000, recurringFreqMonthly, 1, start, nil)...))
	expectQueryAny(mock, "FROM recurring_series_terms t JOIN accounts a", 2).
		WillReturnRows(pgxmock.NewRows(recurringTermLoadCols).
			AddRow(recurringTermRow(termID, id, start, 1000, accountID)...))
	mock.ExpectBegin()
	expectQueryAny(mock, "UPDATE recurring_series_terms t SET", 7).
		WillReturnRows(pgxmock.NewRows(recurringTermReturnCols).
			AddRow(termID, id, newStart, nil, int64(1500), accountID, time.Now()))
	expectExecAny(mock, "UPDATE recurring_series rs SET", 2).
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectCommit()

	body := `{"startDate":"2099-02-01","amount":15}`
	req, _ := http.NewRequest(http.MethodPut, "/recurring/"+id.String()+"/terms/"+termID.String(), bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var term models.RecurringSeriesTerm
	assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &term))
	assert.Equal(t, int64(1500), term.Amount.Cents())
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateRecurringTermNotFound(t *testing.T) {
	r, _, mock := recurringTestRouter(t)
	id, accountID := uuid.New(), uuid.New()
	start, _ := time.Parse("2006-01-02", "2099-01-15")

	expectQueryAny(mock, "FROM recurring_series WHERE id", 2).
		WillReturnRows(pgxmock.NewRows(recurringSeriesColumnsForTest).
			AddRow(recurringSeriesRow(id, accountID, "Rent", 1000, recurringFreqMonthly, 1, start, nil)...))
	expectQueryAny(mock, "FROM recurring_series_terms t JOIN accounts a", 2).
		WillReturnRows(pgxmock.NewRows(recurringTermLoadCols).
			AddRow(recurringTermRow(uuid.New(), id, start, 1000, accountID)...))

	body := `{"amount":15}`
	req, _ := http.NewRequest(http.MethodPut, "/recurring/"+id.String()+"/terms/"+uuid.New().String(), bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateRecurringTermOverlap(t *testing.T) {
	r, _, mock := recurringTestRouter(t)
	id, accountID := uuid.New(), uuid.New()
	start, _ := time.Parse("2006-01-02", "2099-01-15")
	term1, term2 := uuid.New(), uuid.New()

	expectQueryAny(mock, "FROM recurring_series WHERE id", 2).
		WillReturnRows(pgxmock.NewRows(recurringSeriesColumnsForTest).
			AddRow(recurringSeriesRow(id, accountID, "Rent", 1000, recurringFreqMonthly, 1, start, nil)...))
	expectQueryAny(mock, "FROM recurring_series_terms t JOIN accounts a", 2).
		WillReturnRows(pgxmock.NewRows(recurringTermLoadCols).
			AddRow(recurringTermRowEnd(term1, id, start, mustDate("2099-03-31"), 1000, accountID)...).
			AddRow(recurringTermRow(term2, id, mustDate("2099-06-01"), 2000, accountID)...))

	// Moving term2 to start inside term1's range overlaps.
	body := `{"startDate":"2099-02-01"}`
	req, _ := http.NewRequest(http.MethodPut, "/recurring/"+id.String()+"/terms/"+term2.String(), bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateRecurringTermAccountNotFound(t *testing.T) {
	r, _, mock := recurringTestRouter(t)
	id, accountID, termID := uuid.New(), uuid.New(), uuid.New()
	start, _ := time.Parse("2006-01-02", "2099-01-15")

	expectQueryAny(mock, "FROM recurring_series WHERE id", 2).
		WillReturnRows(pgxmock.NewRows(recurringSeriesColumnsForTest).
			AddRow(recurringSeriesRow(id, accountID, "Rent", 1000, recurringFreqMonthly, 1, start, nil)...))
	expectQueryAny(mock, "FROM recurring_series_terms t JOIN accounts a", 2).
		WillReturnRows(pgxmock.NewRows(recurringTermLoadCols).
			AddRow(recurringTermRow(termID, id, start, 1000, accountID)...))
	mock.ExpectBegin()
	expectQueryAny(mock, "UPDATE recurring_series_terms t SET", 7).WillReturnError(pgx.ErrNoRows)
	mock.ExpectRollback()

	body := `{"accountId":"` + uuid.New().String() + `"}`
	req, _ := http.NewRequest(http.MethodPut, "/recurring/"+id.String()+"/terms/"+termID.String(), bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestDeleteRecurringTerm(t *testing.T) {
	r, _, mock := recurringTestRouter(t)
	id, accountID := uuid.New(), uuid.New()
	start, _ := time.Parse("2006-01-02", "2099-01-15")
	termID := uuid.New()

	expectQueryAny(mock, "FROM recurring_series WHERE id", 2).
		WillReturnRows(pgxmock.NewRows(recurringSeriesColumnsForTest).
			AddRow(recurringSeriesRow(id, accountID, "Rent", 1000, recurringFreqMonthly, 1, start, nil)...))
	expectQueryAny(mock, "FROM recurring_series_terms t JOIN accounts a", 2).
		WillReturnRows(pgxmock.NewRows(recurringTermLoadCols).
			AddRow(recurringTermRow(termID, id, start, 1000, accountID)...))
	mock.ExpectBegin()
	expectExecAny(mock, "DELETE FROM recurring_series_terms", 3).
		WillReturnResult(pgxmock.NewResult("DELETE", 1))
	expectExecAny(mock, "UPDATE recurring_series rs SET", 2).
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectCommit()

	req, _ := http.NewRequest(http.MethodDelete, "/recurring/"+id.String()+"/terms/"+termID.String(), nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestDeleteRecurringTermNotFound(t *testing.T) {
	r, _, mock := recurringTestRouter(t)
	id, accountID := uuid.New(), uuid.New()
	start, _ := time.Parse("2006-01-02", "2099-01-15")

	expectQueryAny(mock, "FROM recurring_series WHERE id", 2).
		WillReturnRows(pgxmock.NewRows(recurringSeriesColumnsForTest).
			AddRow(recurringSeriesRow(id, accountID, "Rent", 1000, recurringFreqMonthly, 1, start, nil)...))
	expectQueryAny(mock, "FROM recurring_series_terms t JOIN accounts a", 2).
		WillReturnRows(pgxmock.NewRows(recurringTermLoadCols).
			AddRow(recurringTermRow(uuid.New(), id, start, 1000, accountID)...))

	req, _ := http.NewRequest(http.MethodDelete, "/recurring/"+id.String()+"/terms/"+uuid.New().String(), nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestDeleteRecurringTermSeriesNotFound(t *testing.T) {
	r, _, mock := recurringTestRouter(t)
	expectQueryAny(mock, "FROM recurring_series WHERE id", 2).WillReturnError(pgx.ErrNoRows)

	req, _ := http.NewRequest(http.MethodDelete, "/recurring/"+uuid.New().String()+"/terms/"+uuid.New().String(), nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestCreateRecurringSeriesClosed verifies an entry can carry its own end date
// (a subscription that ends).
func TestCreateRecurringSeriesClosed(t *testing.T) {
	r, _, mock := recurringTestRouter(t)
	id, accountA := uuid.New(), uuid.New()

	mock.ExpectBegin()
	expectQueryAny(mock, "INSERT INTO recurring_series", 14).
		WillReturnRows(pgxmock.NewRows(recurringSeriesColumnsForTest).
			AddRow(recurringSeriesRow(id, accountA, "Rent", 1000, recurringFreqMonthly, 1, mustDate("2099-01-15"), ptrDate("2099-12-31"))...))
	mock.ExpectExec("INSERT INTO recurring_series_terms").
		WithArgs(id, testUserID(), mustDate("2099-01-15"), ptrDate("2099-12-31"), money.Amount(1000), accountA).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectCommit()

	body := `{"name":"Rent","type":"debit","frequency":"monthly","ranges":[` +
		`{"startDate":"2099-01-15","endDate":"2099-12-31","amount":10,"accountId":"` + accountA.String() + `"}]}`
	req, _ := http.NewRequest(http.MethodPost, "/recurring", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestCreateRecurringSeriesRangeEndBeforeStart rejects a range whose end is not
// after its start.
func TestCreateRecurringSeriesRangeEndBeforeStart(t *testing.T) {
	r, _, mock := recurringTestRouter(t)
	accountA := uuid.New()
	body := `{"name":"Rent","type":"debit","frequency":"monthly","ranges":[` +
		`{"startDate":"2099-06-01","endDate":"2099-01-01","amount":10,"accountId":"` + accountA.String() + `"}]}`
	req, _ := http.NewRequest(http.MethodPost, "/recurring", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}
