package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"

	"github.com/google/uuid"
	"github.com/pashagolub/pgxmock/v5"
	"github.com/stretchr/testify/assert"
)

// wantClosedAccountGuard is the predicate every transaction write must carry:
// closing an account freezes its transactions, so a write touching such a row
// is a no-op. The handler tests match it verbatim, so dropping or moving it
// fails instead of silently rewriting frozen rows.
const wantClosedAccountGuard = "NOT EXISTS (SELECT 1 FROM accounts closed_acct WHERE closed_acct.id = transactions.account_id AND closed_acct.closed)"

func TestGetTags(t *testing.T) {
	r, srv, mock := newAccountTestRouter(t)
	r.GET("/tags", srv.GetTags)

	mock.ExpectQuery("SELECT tag, COUNT").
		WithArgs(testUserID()).
		WillReturnRows(pgxmock.NewRows([]string{"tag", "count"}).
			AddRow("work", 3).
			AddRow("reimbursable", 1))

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/tags", nil)
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var body struct {
		Data []struct {
			Name  string `json:"name"`
			Count int    `json:"count"`
		} `json:"data"`
	}
	assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Len(t, body.Data, 2)
	assert.Equal(t, "work", body.Data[0].Name)
	assert.Equal(t, 3, body.Data[0].Count)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestBulkUpdateTags(t *testing.T) {
	r, srv, mock := newAccountTestRouter(t)
	r.POST("/transactions/bulk-tags", srv.BulkUpdateTags)

	id1 := uuid.New()
	id2 := uuid.New()
	// pgxmock collapses whitespace on both sides, so the statement is pinned
	// here in its single-line form, closed-account guard included.
	mock.ExpectExec(regexp.QuoteMeta(
		"UPDATE transactions SET tags = COALESCE(( SELECT array_agg(DISTINCT x ORDER BY x) "+
			"FROM unnest(tags || $2::text[]) AS x WHERE x <> ALL($3::text[]) ), '{}') "+
			"WHERE user_id = $1 AND id = ANY($4::uuid[]) AND "+wantClosedAccountGuard)).
		WithArgs(testUserID(), []string{"trip"}, []string{"draft"}, []uuid.UUID{id1, id2}).
		WillReturnResult(pgxmock.NewResult("UPDATE", 2))

	body, _ := json.Marshal(map[string]any{
		"transactionIds": []string{id1.String(), id2.String()},
		"add":            []string{"trip"},
		"remove":         []string{"draft"},
	})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/transactions/bulk-tags", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"updated":2`)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// A batch whose rows all sit on closed accounts changes nothing: the guard
// makes every frozen transaction unmatchable, so the count reports zero.
func TestBulkUpdateTagsSkipsClosedAccounts(t *testing.T) {
	r, srv, mock := newAccountTestRouter(t)
	r.POST("/transactions/bulk-tags", srv.BulkUpdateTags)

	closedID := uuid.New()
	mock.ExpectExec(regexp.QuoteMeta(
		"UPDATE transactions SET tags = COALESCE(( SELECT array_agg(DISTINCT x ORDER BY x) "+
			"FROM unnest(tags || $2::text[]) AS x WHERE x <> ALL($3::text[]) ), '{}') "+
			"WHERE user_id = $1 AND id = ANY($4::uuid[]) AND "+wantClosedAccountGuard)).
		WithArgs(testUserID(), []string{"trip"}, []string{}, []uuid.UUID{closedID}).
		WillReturnResult(pgxmock.NewResult("UPDATE", 0))

	body, _ := json.Marshal(map[string]any{
		"transactionIds": []string{closedID.String()},
		"add":            []string{"trip"},
	})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/transactions/bulk-tags", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"updated":0`)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestBulkUpdateTagsRequiresChange(t *testing.T) {
	r, srv, _ := newAccountTestRouter(t)
	r.POST("/transactions/bulk-tags", srv.BulkUpdateTags)

	body, _ := json.Marshal(map[string]any{
		"transactionIds": []string{uuid.New().String()},
	})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/transactions/bulk-tags", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestBulkUpdateTagsRejectsLongTag(t *testing.T) {
	r, srv, _ := newAccountTestRouter(t)
	r.POST("/transactions/bulk-tags", srv.BulkUpdateTags)

	long := make([]byte, maxTagLength+1)
	for i := range long {
		long[i] = 'a'
	}
	body, _ := json.Marshal(map[string]any{
		"transactionIds": []string{uuid.New().String()},
		"add":            []string{string(long)},
	})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/transactions/bulk-tags", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestRenameTag(t *testing.T) {
	r, srv, mock := newAccountTestRouter(t)
	r.POST("/tags/rename", srv.RenameTag)

	mock.ExpectExec(regexp.QuoteMeta(
		"UPDATE transactions SET tags = COALESCE(( SELECT array_agg(DISTINCT new_tag ORDER BY new_tag) "+
			"FROM ( SELECT CASE WHEN x = $2 THEN $3 ELSE x END AS new_tag FROM unnest(tags) AS x ) mapped "+
			"), '{}') WHERE user_id = $1 AND $2 = ANY(tags) AND "+wantClosedAccountGuard)).
		WithArgs(testUserID(), "old", "new").
		WillReturnResult(pgxmock.NewResult("UPDATE", 4))

	body, _ := json.Marshal(map[string]string{"from": "old", "to": "new"})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/tags/rename", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"updated":4`)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// A rename is a global rewrite, so the guard is what keeps it from reaching a
// frozen transaction: nothing on a closed account matches, and the response
// reports zero rows rewritten.
func TestRenameTagSkipsClosedAccounts(t *testing.T) {
	r, srv, mock := newAccountTestRouter(t)
	r.POST("/tags/rename", srv.RenameTag)

	mock.ExpectExec(regexp.QuoteMeta(
		"UPDATE transactions SET tags = COALESCE(( SELECT array_agg(DISTINCT new_tag ORDER BY new_tag) "+
			"FROM ( SELECT CASE WHEN x = $2 THEN $3 ELSE x END AS new_tag FROM unnest(tags) AS x ) mapped "+
			"), '{}') WHERE user_id = $1 AND $2 = ANY(tags) AND "+wantClosedAccountGuard)).
		WithArgs(testUserID(), "old", "new").
		WillReturnResult(pgxmock.NewResult("UPDATE", 0))

	body, _ := json.Marshal(map[string]string{"from": "old", "to": "new"})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/tags/rename", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"updated":0`)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestRenameTagNoopWhenSame(t *testing.T) {
	r, srv, _ := newAccountTestRouter(t)
	r.POST("/tags/rename", srv.RenameTag)

	body, _ := json.Marshal(map[string]string{"from": "same", "to": "same"})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/tags/rename", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"updated":0`)
}
