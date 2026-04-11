package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/fintrak/backend/db"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
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
	r.POST("/rules/apply", ApplyRules)

	ruleCatID := uuid.New()
	txnID := uuid.New()
	uncategorizedID := uuid.New()

	// 1. Get all rules
	mock.ExpectQuery("SELECT pattern, match_type, category_id, payee_id FROM rules").
		WillReturnRows(pgxmock.NewRows([]string{"pattern", "match_type", "category_id", "payee_id"}).
			AddRow("Zomato", "contains", ruleCatID, nil))

	// 2. Find "Uncategorized" category ID
	mock.ExpectQuery("SELECT id FROM categories WHERE name = 'Uncategorized'").
		WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow(uncategorizedID))

	// 3. Get uncategorized transactions
	mock.ExpectQuery("SELECT id, description FROM transactions WHERE category_id IS NULL").
		WithArgs(uncategorizedID).
		WillReturnRows(pgxmock.NewRows([]string{"id", "description"}).
			AddRow(txnID, "Zomato Order #999"))

	// 4. Batch update transaction
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE transactions SET category_id =").
		WithArgs(ruleCatID, txnID).
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
