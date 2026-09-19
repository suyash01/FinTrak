package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/pashagolub/pgxmock/v5"
	"github.com/stretchr/testify/assert"
)

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
	mock.ExpectExec("UPDATE transactions").
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

	mock.ExpectExec("UPDATE transactions").
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
