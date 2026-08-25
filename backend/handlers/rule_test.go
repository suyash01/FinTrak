package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/fintrak/backend/db"
	"github.com/fintrak/backend/models"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/pashagolub/pgxmock/v3"
	"github.com/stretchr/testify/assert"
)

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

	oldPool := db.Pool
	db.Pool = mock
	defer func() { db.Pool = oldPool }()

	gin.SetMode(gin.TestMode)
	r := gin.Default()
	r.Use(testAuthMiddleware())
	r.POST("/rules/apply", ApplyRules)

	ruleCatID := uuid.New()
	txnID := uuid.New()
	uncategorizedID := uuid.New()
	userID := testUserID()

	// 1. Get all rules
	mock.ExpectQuery("SELECT pattern, match_type, category_id, payee_id FROM rules").
		WithArgs(userID).
		WillReturnRows(pgxmock.NewRows([]string{"pattern", "match_type", "category_id", "payee_id"}).
			AddRow("Zomato", "contains", ruleCatID, nil))

	// 2. Find "Uncategorized" category ID
	mock.ExpectQuery("SELECT id FROM categories WHERE name = 'Uncategorized'").
		WithArgs(userID).
		WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow(uncategorizedID))

	// 3. Get uncategorized transactions
	mock.ExpectQuery("SELECT id, description FROM transactions WHERE category_id IS NULL").
		WithArgs(userID, uncategorizedID).
		WillReturnRows(pgxmock.NewRows([]string{"id", "description"}).
			AddRow(txnID, "Zomato Order #999"))

	// 4. Batch update transaction
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE transactions SET category_id =").
		WithArgs(ruleCatID, txnID, userID).
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectCommit()

	// Perform request
	req, _ := http.NewRequest("POST", "/rules/apply", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// Assertions
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"updated":1`)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func newRuleTestRouter(t *testing.T) (*gin.Engine, pgxmock.PgxPoolIface) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	oldPool := db.Pool
	db.Pool = mock
	t.Cleanup(func() {
		db.Pool = oldPool
		mock.Close()
	})

	gin.SetMode(gin.TestMode)
	r := gin.Default()
	r.Use(testAuthMiddleware())
	return r, mock
}

func TestGetRules(t *testing.T) {
	r, mock := newRuleTestRouter(t)
	r.GET("/rules", GetRules)

	userID := testUserID()
	ruleID := uuid.New()
	catID := uuid.New()

	rows := pgxmock.NewRows([]string{"id", "pattern", "match_type", "category_id", "payee_id", "payee", "priority", "category_name"}).
		AddRow(ruleID, "Zomato", "contains", catID, nil, "Zomato", 10, "Food")

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
	r, mock := newRuleTestRouter(t)
	r.POST("/rules", CreateRule)

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
		WithArgs(userID, "Swiggy", "contains", catID, &payeeID, 5).
		WillReturnRows(pgxmock.NewRows([]string{"id", "pattern", "match_type", "category_id", "payee_id", "priority"}).
			AddRow(ruleID, "Swiggy", "contains", catID, payeeID, 5))

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
	r, mock := newRuleTestRouter(t)
	r.POST("/rules", CreateRule)

	userID := testUserID()
	ruleID := uuid.New()
	catID := uuid.New()

	reqBody := models.CreateRuleRequest{
		Pattern:    "Rent",
		CategoryID: catID,
	}

	// Empty match type defaults to "contains".
	mock.ExpectQuery("INSERT INTO rules").
		WithArgs(userID, "Rent", "contains", catID, (*uuid.UUID)(nil), 0).
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
	r, mock := newRuleTestRouter(t)
	r.POST("/rules", CreateRule)

	req, _ := http.NewRequest("POST", "/rules", bytes.NewBufferString("{not json"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestDeleteRule(t *testing.T) {
	r, mock := newRuleTestRouter(t)
	r.DELETE("/rules/:id", DeleteRule)

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
	r, mock := newRuleTestRouter(t)
	r.DELETE("/rules/:id", DeleteRule)

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
	r, mock := newRuleTestRouter(t)
	r.DELETE("/rules/:id", DeleteRule)

	req, _ := http.NewRequest("DELETE", "/rules/not-a-uuid", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateRule(t *testing.T) {
	r, mock := newRuleTestRouter(t)
	r.PUT("/rules/:id", UpdateRule)

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
		WithArgs("Netflix", "starts_with", catID, (*uuid.UUID)(nil), 3, ruleID, userID).
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
	r, mock := newRuleTestRouter(t)
	r.PUT("/rules/:id", UpdateRule)

	ruleID := uuid.New()
	catID := uuid.New()

	reqBody := models.UpdateRuleRequest{
		Pattern:    "Netflix",
		CategoryID: catID,
	}

	mock.ExpectQuery("UPDATE rules SET pattern").
		WithArgs("Netflix", "", catID, (*uuid.UUID)(nil), 0, ruleID, testUserID()).
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
	r, mock := newRuleTestRouter(t)
	r.PUT("/rules/:id", UpdateRule)

	req, _ := http.NewRequest("PUT", "/rules/"+uuid.New().String(), bytes.NewBufferString("{not json"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateRuleCategoryNotOwned(t *testing.T) {
	r, mock := newRuleTestRouter(t)
	r.POST("/rules", CreateRule)

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
		WithArgs(userID, "Swiggy", "contains", otherCatID, (*uuid.UUID)(nil), 5).
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
	r, mock := newRuleTestRouter(t)
	r.POST("/rules", CreateRule)

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
		WithArgs(userID, "Swiggy", "contains", catID, &otherPayeeID, 5).
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
	r, mock := newRuleTestRouter(t)
	r.PUT("/rules/:id", UpdateRule)

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
		WithArgs("Netflix", "starts_with", otherCatID, (*uuid.UUID)(nil), 3, ruleID, userID).
		WillReturnError(pgx.ErrNoRows)

	body, _ := json.Marshal(reqBody)
	req, _ := http.NewRequest("PUT", "/rules/"+ruleID.String(), bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}
