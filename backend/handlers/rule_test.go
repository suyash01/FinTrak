package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"

	"github.com/fintrak/backend/internal/money"
	"github.com/fintrak/backend/models"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/pashagolub/pgxmock/v5"
	"github.com/stretchr/testify/assert"
)

// ruleEntryRows is the column set loadRules returns (16 columns).
func ruleEntryRows() *pgxmock.Rows {
	return pgxmock.NewRows([]string{
		"pattern", "match_type", "category_id", "payee_id",
		"account_id", "filter_category_id", "filter_payee_id", "min_amount", "max_amount", "txn_type",
		"date_from", "date_to", "is_linked", "is_recurring", "add_tags", "notes",
	})
}

// ruleListRows is the column set GetRules returns (23 columns).
func ruleListRows() *pgxmock.Rows {
	return pgxmock.NewRows([]string{
		"id", "pattern", "match_type", "category_id", "payee_id", "payee", "priority", "category_name",
		"account_id", "account_name", "filter_category_id", "filter_category_name",
		"filter_payee_id", "filter_payee_name", "min_amount", "max_amount", "txn_type",
		"date_from", "date_to", "is_linked", "is_recurring", "add_tags", "notes",
	})
}

func TestMatchRule(t *testing.T) {
	tests := []struct {
		name      string
		desc      string
		pattern   string
		matchType string
		expected  bool
	}{
		{
			name:      "contains match",
			desc:      "Zomato Order #1234",
			pattern:   "Zomato",
			matchType: "contains",
			expected:  true,
		},
		{
			name:      "contains mismatch",
			desc:      "Swiggy Order #1234",
			pattern:   "Zomato",
			matchType: "contains",
			expected:  false,
		},
		{
			name:      "case insensitive contains",
			desc:      "zomato order #1234",
			pattern:   "ZOMATO",
			matchType: "contains",
			expected:  true,
		},
		{
			name:      "starts_with match",
			desc:      "UPI-Transfer to Friend",
			pattern:   "UPI-",
			matchType: "starts_with",
			expected:  true,
		},
		{
			name:      "starts_with mismatch",
			desc:      "Friend UPI-Transfer",
			pattern:   "UPI-",
			matchType: "starts_with",
			expected:  false,
		},
		{
			name:      "exact match",
			desc:      "RENT",
			pattern:   "rent",
			matchType: "exact",
			expected:  true,
		},
		{
			name:      "exact mismatch",
			desc:      "MONTHLY RENT",
			pattern:   "rent",
			matchType: "exact",
			expected:  false,
		},
		{
			name:      "unknown match type",
			desc:      "Any",
			pattern:   "Any",
			matchType: "regex",
			expected:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := matchRule(tt.desc, tt.pattern, tt.matchType)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestApplyRules(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()
	srv := newTestServer(mock)

	gin.SetMode(gin.TestMode)
	r := gin.Default()
	r.Use(testAuthMiddleware())
	r.POST("/rules/apply", srv.ApplyRules)

	userID := testUserID()
	cat1 := uuid.New()
	cat2 := uuid.New()
	payeeID := uuid.New()

	// 1. Get all rules (priority order): contains rule without payee, then
	// starts_with rule with a payee. pgxmock can only scan pointer mock
	// values into *uuid.UUID destinations (account_test.go's intPtr pattern).
	mock.ExpectQuery("SELECT pattern, match_type, category_id, payee_id").
		WithArgs(userID).
		WillReturnRows(ruleEntryRows().
			AddRow("Zomato", "contains", cat1, nil, nil, nil, nil, nil, nil, "", nil, nil, nil, nil, []string{}, "").
			AddRow("Netflix", "starts_with", cat2, &payeeID, nil, nil, nil, nil, nil, "", nil, nil, nil, nil, []string{}, ""))

	// 2. One set-based UPDATE per rule (no per-transaction N+1 loop). Each
	// statement carries the closed-account guard, so a frozen transaction is
	// never categorized by a rule run.
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta(
		"UPDATE transactions SET category_id = $2 WHERE user_id = $1 AND category_id IS NULL AND " +
			wantClosedAccountGuard + ` AND LOWER(description) LIKE LOWER($3) ESCAPE '\'`)).
		WithArgs(userID, cat1, "%Zomato%").
		WillReturnResult(pgxmock.NewResult("UPDATE", 2))
	mock.ExpectExec(regexp.QuoteMeta(
		"UPDATE transactions SET category_id = $2, payee_id = $3 WHERE user_id = $1 AND category_id IS NULL AND " +
			wantClosedAccountGuard + ` AND LOWER(description) LIKE LOWER($4) ESCAPE '\'`)).
		WithArgs(userID, cat2, payeeID, "Netflix%").
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectCommit()

	// Perform request
	req, _ := http.NewRequest("POST", "/rules/apply", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// Assertions
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"updated":3`)

	assert.NoError(t, mock.ExpectationsWereMet())
}

// Uncategorized is not the same as writable: a rule can match a transaction on
// a closed account, and the guard is what keeps the apply from rewriting it.
func TestApplyRulesSkipsClosedAccounts(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()
	srv := newTestServer(mock)

	gin.SetMode(gin.TestMode)
	r := gin.Default()
	r.Use(testAuthMiddleware())
	r.POST("/rules/apply", srv.ApplyRules)

	userID := testUserID()
	catID := uuid.New()

	mock.ExpectQuery("SELECT pattern, match_type, category_id, payee_id").
		WithArgs(userID).
		WillReturnRows(ruleEntryRows().
			AddRow("Zomato", "contains", catID, nil, nil, nil, nil, nil, nil, "", nil, nil, nil, nil, []string{}, ""))

	// Every row the rule matches sits on a closed account, so the guarded
	// UPDATE matches nothing.
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta(
		"UPDATE transactions SET category_id = $2 WHERE user_id = $1 AND category_id IS NULL AND " +
			wantClosedAccountGuard + ` AND LOWER(description) LIKE LOWER($3) ESCAPE '\'`)).
		WithArgs(userID, catID, "%Zomato%").
		WillReturnResult(pgxmock.NewResult("UPDATE", 0))
	mock.ExpectCommit()

	req, _ := http.NewRequest("POST", "/rules/apply", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"updated":0`)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestApplyRulesFailureRollsBack(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()
	srv := newTestServer(mock)

	gin.SetMode(gin.TestMode)
	r := gin.Default()
	r.Use(testAuthMiddleware())
	r.POST("/rules/apply", srv.ApplyRules)

	userID := testUserID()
	cat1 := uuid.New()

	mock.ExpectQuery("SELECT pattern, match_type, category_id, payee_id").
		WithArgs(userID).
		WillReturnRows(ruleEntryRows().
			AddRow("Zomato", "contains", cat1, nil, nil, nil, nil, nil, nil, "", nil, nil, nil, nil, []string{}, "").
			AddRow("Netflix", "contains", cat1, nil, nil, nil, nil, nil, nil, "", nil, nil, nil, nil, []string{}, ""))

	// First rule's UPDATE succeeds, the second fails...
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta(
		"UPDATE transactions SET category_id = $2 WHERE user_id = $1 AND category_id IS NULL AND " +
			wantClosedAccountGuard + ` AND LOWER(description) LIKE LOWER($3) ESCAPE '\'`)).
		WithArgs(userID, cat1, "%Zomato%").
		WillReturnResult(pgxmock.NewResult("UPDATE", 5))
	mock.ExpectExec(regexp.QuoteMeta(
		"UPDATE transactions SET category_id = $2 WHERE user_id = $1 AND category_id IS NULL AND " +
			wantClosedAccountGuard + ` AND LOWER(description) LIKE LOWER($3) ESCAPE '\'`)).
		WithArgs(userID, cat1, "%Netflix%").
		WillReturnError(pgx.ErrTxClosed)
	// ...so the transaction is rolled back (never committed): the apply is
	// all-or-nothing, unlike the old per-row loop that committed partial work.

	req, _ := http.NewRequest("POST", "/rules/apply", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestEscapeLikePattern(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"100%", `100\%`},
		{"a_b", `a\_b`},
		{`C:\dir`, `C:\\dir`},
		{"plain", "plain"},
		{"100% of a_b \\ cases", `100\% of a\_b \\ cases`},
	}
	for _, tc := range tests {
		assert.Equal(t, tc.want, escapeLikePattern(tc.in), "escapeLikePattern(%q)", tc.in)
	}
}

func TestRuleMatchSQL(t *testing.T) {
	expr, arg, ok := ruleMatchSQL("contains", "100%", 3)
	assert.True(t, ok)
	assert.Contains(t, expr, "LIKE LOWER($3)")
	assert.Contains(t, expr, "ESCAPE")
	assert.Equal(t, `%100\%%`, arg)

	expr, arg, ok = ruleMatchSQL("starts_with", "Netflix", 4)
	assert.True(t, ok)
	assert.Contains(t, expr, "LIKE LOWER($4)")
	assert.Equal(t, "Netflix%", arg)

	expr, arg, ok = ruleMatchSQL("exact", "Salary", 3)
	assert.True(t, ok)
	assert.Contains(t, expr, "= LOWER($3)")
	assert.Equal(t, "Salary", arg)

	// match types matchRule never fires for are skipped, not errors.
	_, _, ok = ruleMatchSQL("regex", "x", 3)
	assert.False(t, ok)
}

func newRuleTestRouter(t *testing.T) (*gin.Engine, *Server, pgxmock.PgxPoolIface) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	srv := newTestServer(mock)
	t.Cleanup(mock.Close)

	gin.SetMode(gin.TestMode)
	r := gin.Default()
	r.Use(testAuthMiddleware())
	return r, srv, mock
}

func TestPreviewRule(t *testing.T) {
	r, srv, mock := newRuleTestRouter(t)
	r.POST("/rules/preview", srv.PreviewRule)

	catID := uuid.New()
	acctID := uuid.New()

	// The preview renders the same clause list ApplyRules builds — same
	// uncategorized guard, same closed-account guard, same rule predicate — so
	// the count cannot overstate what a real apply would update.
	mock.ExpectQuery(regexp.QuoteMeta(
		"SELECT COUNT(*) FROM transactions WHERE user_id = $1 AND category_id IS NULL AND " +
			wantClosedAccountGuard + ` AND LOWER(description) LIKE LOWER($2) ESCAPE '\' AND account_id = $3`)).
		WithArgs(testUserID(), "zomato%", acctID).
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(4))

	body, _ := json.Marshal(models.CreateRuleRequest{
		Pattern:    "zomato",
		MatchType:  "starts_with",
		CategoryID: catID,
		AccountID:  &acctID,
	})
	req, _ := http.NewRequest("POST", "/rules/preview", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"matched":4`)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPreviewRuleInvalidTxnType(t *testing.T) {
	r, srv, _ := newRuleTestRouter(t)
	r.POST("/rules/preview", srv.PreviewRule)

	body, _ := json.Marshal(models.CreateRuleRequest{
		Pattern:    "x",
		CategoryID: uuid.New(),
		TxnType:    "refund",
	})
	req, _ := http.NewRequest("POST", "/rules/preview", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestCreateRuleWithConditions(t *testing.T) {
	r, srv, mock := newRuleTestRouter(t)
	r.POST("/rules", srv.CreateRule)

	userID := testUserID()
	ruleID := uuid.New()
	catID := uuid.New()
	acctID := uuid.New()
	minAmount := money.FromFloat(100)

	mock.ExpectQuery("INSERT INTO rules").
		WithArgs(userID, "Zomato", "contains", catID, (*uuid.UUID)(nil), 0,
			&acctID, (*uuid.UUID)(nil), (*uuid.UUID)(nil), &minAmount, (*money.Amount)(nil), "debit",
			(*string)(nil), (*string)(nil), (*bool)(nil), (*bool)(nil), []string{"food"}, "note").
		WillReturnRows(pgxmock.NewRows([]string{"id", "pattern", "match_type", "category_id", "payee_id", "priority"}).
			AddRow(ruleID, "Zomato", "contains", catID, nil, 0))

	body, _ := json.Marshal(models.CreateRuleRequest{
		Pattern:    "Zomato",
		CategoryID: catID,
		AccountID:  &acctID,
		MinAmount:  &minAmount,
		TxnType:    "debit",
		AddTags:    []string{"food"},
		Notes:      "note",
	})
	req, _ := http.NewRequest("POST", "/rules", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateRuleInvalidMatchType(t *testing.T) {
	r, srv, _ := newRuleTestRouter(t)
	r.POST("/rules", srv.CreateRule)

	body, _ := json.Marshal(models.CreateRuleRequest{
		Pattern:    "x",
		MatchType:  "regex",
		CategoryID: uuid.New(),
	})
	req, _ := http.NewRequest("POST", "/rules", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestGetRules(t *testing.T) {
	r, srv, mock := newRuleTestRouter(t)
	r.GET("/rules", srv.GetRules)

	userID := testUserID()
	ruleID := uuid.New()
	catID := uuid.New()

	rows := ruleListRows().
		AddRow(ruleID, "Zomato", "contains", catID, nil, "Zomato", 10, "Food",
			nil, "", nil, "", nil, "", nil, nil, "", nil, nil, nil, nil, []string{}, "")

	mock.ExpectQuery("SELECT r.id, r.pattern, r.match_type").
		WithArgs(userID).
		WillReturnRows(rows)

	req, _ := http.NewRequest("GET", "/rules", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var rules []models.Rule
	assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &rules))
	assert.Len(t, rules, 1)
	assert.Equal(t, "Zomato", rules[0].Pattern)
	assert.Equal(t, "contains", rules[0].MatchType)
	assert.Equal(t, "Food", rules[0].CategoryName)
	assert.Equal(t, "Zomato", rules[0].Payee)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateRule(t *testing.T) {
	r, srv, mock := newRuleTestRouter(t)
	r.POST("/rules", srv.CreateRule)

	userID := testUserID()
	ruleID := uuid.New()
	catID := uuid.New()
	payeeID := uuid.New()

	reqBody := models.CreateRuleRequest{
		Pattern:    "Swiggy",
		MatchType:  "contains",
		CategoryID: catID,
		PayeeID:    &payeeID,
		Priority:   5,
	}

	mock.ExpectQuery("INSERT INTO rules").
		WithArgs(userID, "Swiggy", "contains", catID, &payeeID, 5,
			(*uuid.UUID)(nil), (*uuid.UUID)(nil), (*uuid.UUID)(nil), (*money.Amount)(nil), (*money.Amount)(nil), nil,
			(*string)(nil), (*string)(nil), (*bool)(nil), (*bool)(nil), ([]string)(nil), "").
		WillReturnRows(pgxmock.NewRows([]string{"id", "pattern", "match_type", "category_id", "payee_id", "priority"}).
			AddRow(ruleID, "Swiggy", "contains", catID, &payeeID, 5))

	body, _ := json.Marshal(reqBody)
	req, _ := http.NewRequest("POST", "/rules", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)

	var rule models.Rule
	assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &rule))
	assert.Equal(t, ruleID, rule.ID)
	assert.Equal(t, "Swiggy", rule.Pattern)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateRuleDefaultsMatchType(t *testing.T) {
	r, srv, mock := newRuleTestRouter(t)
	r.POST("/rules", srv.CreateRule)

	userID := testUserID()
	ruleID := uuid.New()
	catID := uuid.New()

	reqBody := models.CreateRuleRequest{
		Pattern:    "Rent",
		CategoryID: catID,
	}

	// Empty match type defaults to "contains".
	mock.ExpectQuery("INSERT INTO rules").
		WithArgs(userID, "Rent", "contains", catID, (*uuid.UUID)(nil), 0,
			(*uuid.UUID)(nil), (*uuid.UUID)(nil), (*uuid.UUID)(nil), (*money.Amount)(nil), (*money.Amount)(nil), nil,
			(*string)(nil), (*string)(nil), (*bool)(nil), (*bool)(nil), ([]string)(nil), "").
		WillReturnRows(pgxmock.NewRows([]string{"id", "pattern", "match_type", "category_id", "payee_id", "priority"}).
			AddRow(ruleID, "Rent", "contains", catID, nil, 0))

	body, _ := json.Marshal(reqBody)
	req, _ := http.NewRequest("POST", "/rules", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateRuleBadJSON(t *testing.T) {
	r, srv, mock := newRuleTestRouter(t)
	r.POST("/rules", srv.CreateRule)

	req, _ := http.NewRequest("POST", "/rules", bytes.NewBufferString("{not json"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestDeleteRule(t *testing.T) {
	r, srv, mock := newRuleTestRouter(t)
	r.DELETE("/rules/:id", srv.DeleteRule)

	userID := testUserID()
	ruleID := uuid.New()

	mock.ExpectExec("DELETE FROM rules WHERE id").
		WithArgs(ruleID, userID).
		WillReturnResult(pgxmock.NewResult("DELETE", 1))

	req, _ := http.NewRequest("DELETE", "/rules/"+ruleID.String(), nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestDeleteRuleNotFound(t *testing.T) {
	r, srv, mock := newRuleTestRouter(t)
	r.DELETE("/rules/:id", srv.DeleteRule)

	userID := testUserID()
	ruleID := uuid.New()

	mock.ExpectExec("DELETE FROM rules WHERE id").
		WithArgs(ruleID, userID).
		WillReturnResult(pgxmock.NewResult("DELETE", 0))

	req, _ := http.NewRequest("DELETE", "/rules/"+ruleID.String(), nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestDeleteRuleInvalidID(t *testing.T) {
	r, srv, mock := newRuleTestRouter(t)
	r.DELETE("/rules/:id", srv.DeleteRule)

	req, _ := http.NewRequest("DELETE", "/rules/not-a-uuid", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateRule(t *testing.T) {
	r, srv, mock := newRuleTestRouter(t)
	r.PUT("/rules/:id", srv.UpdateRule)

	userID := testUserID()
	ruleID := uuid.New()
	catID := uuid.New()

	reqBody := models.UpdateRuleRequest{
		Pattern:    "Netflix",
		MatchType:  "starts_with",
		CategoryID: catID,
		Priority:   3,
	}

	mock.ExpectQuery("UPDATE rules SET pattern").
		WithArgs("Netflix", "starts_with", catID, (*uuid.UUID)(nil), 3,
			(*uuid.UUID)(nil), (*uuid.UUID)(nil), (*uuid.UUID)(nil), (*money.Amount)(nil), (*money.Amount)(nil), nil,
			(*string)(nil), (*string)(nil), (*bool)(nil), (*bool)(nil), ([]string)(nil), "", ruleID, userID).
		WillReturnRows(pgxmock.NewRows([]string{"id", "pattern", "match_type", "category_id", "payee_id", "priority"}).
			AddRow(ruleID, "Netflix", "starts_with", catID, nil, 3))

	body, _ := json.Marshal(reqBody)
	req, _ := http.NewRequest("PUT", "/rules/"+ruleID.String(), bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var rule models.Rule
	assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &rule))
	assert.Equal(t, "Netflix", rule.Pattern)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateRuleNotFound(t *testing.T) {
	r, srv, mock := newRuleTestRouter(t)
	r.PUT("/rules/:id", srv.UpdateRule)

	ruleID := uuid.New()
	catID := uuid.New()

	reqBody := models.UpdateRuleRequest{
		Pattern:    "Netflix",
		CategoryID: catID,
	}

	mock.ExpectQuery("UPDATE rules SET pattern").
		WithArgs("Netflix", "contains", catID, (*uuid.UUID)(nil), 0,
			(*uuid.UUID)(nil), (*uuid.UUID)(nil), (*uuid.UUID)(nil), (*money.Amount)(nil), (*money.Amount)(nil), nil,
			(*string)(nil), (*string)(nil), (*bool)(nil), (*bool)(nil), ([]string)(nil), "", ruleID, testUserID()).
		WillReturnError(pgx.ErrNoRows)

	body, _ := json.Marshal(reqBody)
	req, _ := http.NewRequest("PUT", "/rules/"+ruleID.String(), bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateRuleBadJSON(t *testing.T) {
	r, srv, mock := newRuleTestRouter(t)
	r.PUT("/rules/:id", srv.UpdateRule)

	req, _ := http.NewRequest("PUT", "/rules/"+uuid.New().String(), bytes.NewBufferString("{not json"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateRuleCategoryNotOwned(t *testing.T) {
	r, srv, mock := newRuleTestRouter(t)
	r.POST("/rules", srv.CreateRule)

	userID := testUserID()
	otherCatID := uuid.New()

	reqBody := models.CreateRuleRequest{
		Pattern:    "Swiggy",
		MatchType:  "contains",
		CategoryID: otherCatID,
		Priority:   5,
	}

	// Category belongs to another user -> INSERT...SELECT matches no rows.
	mock.ExpectQuery("INSERT INTO rules").
		WithArgs(userID, "Swiggy", "contains", otherCatID, (*uuid.UUID)(nil), 5,
			(*uuid.UUID)(nil), (*uuid.UUID)(nil), (*uuid.UUID)(nil), (*money.Amount)(nil), (*money.Amount)(nil), nil,
			(*string)(nil), (*string)(nil), (*bool)(nil), (*bool)(nil), ([]string)(nil), "").
		WillReturnError(pgx.ErrNoRows)

	body, _ := json.Marshal(reqBody)
	req, _ := http.NewRequest("POST", "/rules", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateRulePayeeNotOwned(t *testing.T) {
	r, srv, mock := newRuleTestRouter(t)
	r.POST("/rules", srv.CreateRule)

	userID := testUserID()
	catID := uuid.New()
	otherPayeeID := uuid.New()

	reqBody := models.CreateRuleRequest{
		Pattern:    "Swiggy",
		MatchType:  "contains",
		CategoryID: catID,
		PayeeID:    &otherPayeeID,
		Priority:   5,
	}

	mock.ExpectQuery("INSERT INTO rules").
		WithArgs(userID, "Swiggy", "contains", catID, &otherPayeeID, 5,
			(*uuid.UUID)(nil), (*uuid.UUID)(nil), (*uuid.UUID)(nil), (*money.Amount)(nil), (*money.Amount)(nil), nil,
			(*string)(nil), (*string)(nil), (*bool)(nil), (*bool)(nil), ([]string)(nil), "").
		WillReturnError(pgx.ErrNoRows)

	body, _ := json.Marshal(reqBody)
	req, _ := http.NewRequest("POST", "/rules", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateRuleCategoryNotOwned(t *testing.T) {
	r, srv, mock := newRuleTestRouter(t)
	r.PUT("/rules/:id", srv.UpdateRule)

	userID := testUserID()
	ruleID := uuid.New()
	otherCatID := uuid.New()

	reqBody := models.UpdateRuleRequest{
		Pattern:    "Netflix",
		MatchType:  "starts_with",
		CategoryID: otherCatID,
		Priority:   3,
	}

	// Category belongs to another user -> ownership predicate fails -> no rows.
	mock.ExpectQuery("UPDATE rules SET pattern").
		WithArgs("Netflix", "starts_with", otherCatID, (*uuid.UUID)(nil), 3,
			(*uuid.UUID)(nil), (*uuid.UUID)(nil), (*uuid.UUID)(nil), (*money.Amount)(nil), (*money.Amount)(nil), nil,
			(*string)(nil), (*string)(nil), (*bool)(nil), (*bool)(nil), ([]string)(nil), "", ruleID, userID).
		WillReturnError(pgx.ErrNoRows)

	body, _ := json.Marshal(reqBody)
	req, _ := http.NewRequest("PUT", "/rules/"+ruleID.String(), bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateRuleWithGlobalCategory(t *testing.T) {
	r, srv, mock := newRuleTestRouter(t)
	r.POST("/rules", srv.CreateRule)

	userID := testUserID()
	globalCatID := uuid.New()
	ruleID := uuid.New()

	reqBody := models.CreateRuleRequest{
		Pattern:    "ZOMATO",
		CategoryID: globalCatID,
		Priority:   10,
	}

	// The ownership guard must admit global categories (user_id IS NULL).
	mock.ExpectQuery(regexp.QuoteMeta("WHERE EXISTS (SELECT 1 FROM categories c WHERE c.id = $4 AND (c.user_id = $1 OR c.user_id IS NULL))")).
		WithArgs(userID, reqBody.Pattern, "contains", globalCatID, (*uuid.UUID)(nil), 10,
			(*uuid.UUID)(nil), (*uuid.UUID)(nil), (*uuid.UUID)(nil), (*money.Amount)(nil), (*money.Amount)(nil), nil,
			(*string)(nil), (*string)(nil), (*bool)(nil), (*bool)(nil), ([]string)(nil), "").
		WillReturnRows(pgxmock.NewRows([]string{"id", "pattern", "match_type", "category_id", "payee_id", "priority"}).
			AddRow(ruleID, reqBody.Pattern, "contains", globalCatID, nil, 10))

	body, _ := json.Marshal(reqBody)
	req, _ := http.NewRequest("POST", "/rules", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}
