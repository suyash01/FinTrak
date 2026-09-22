package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/fintrak/backend/models"
	"github.com/google/uuid"
	"github.com/pashagolub/pgxmock/v5"
	"github.com/stretchr/testify/assert"
)

func TestGetLinksQueryError(t *testing.T) {
	r, srv, mock := newLinkTestRouter(t)
	r.GET("/links", srv.GetLinks)

	mock.ExpectQuery("SELECT l.id, l.type, l.from_txn_id").
		WithArgs(testUserID()).
		WillReturnError(assert.AnError)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/links", nil))

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetLinksScanError(t *testing.T) {
	r, srv, mock := newLinkTestRouter(t)
	r.GET("/links", srv.GetLinks)

	mock.ExpectQuery("SELECT l.id, l.type, l.from_txn_id").
		WithArgs(testUserID()).
		WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow(uuid.New()))

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/links", nil))

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateLinkBeginError(t *testing.T) {
	r, srv, mock := newLinkTestRouter(t)
	r.POST("/links", srv.CreateLink)

	mock.ExpectBegin().WillReturnError(assert.AnError)

	body, _ := json.Marshal(models.CreateLinkRequest{
		Type: "transfer", FromTxnID: uuid.New(), ToTxnID: uuid.New(),
	})
	req, _ := http.NewRequest(http.MethodPost, "/links", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateLinkQueryErrors(t *testing.T) {
	fromID := uuid.New()
	toID := uuid.New()
	base := models.CreateLinkRequest{Type: "transfer", FromTxnID: fromID, ToTxnID: toID}

	t.Run("ownership check", func(t *testing.T) {
		r, srv, mock := newLinkTestRouter(t)
		r.POST("/links", srv.CreateLink)

		mock.ExpectBegin()
		mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM transactions WHERE id = ANY").
			WithArgs([]uuid.UUID{fromID, toID}, testUserID()).
			WillReturnError(assert.AnError)

		body, _ := json.Marshal(base)
		req, _ := http.NewRequest(http.MethodPost, "/links", bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("insert", func(t *testing.T) {
		r, srv, mock := newLinkTestRouter(t)
		r.POST("/links", srv.CreateLink)

		mock.ExpectBegin()
		mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM transactions WHERE id = ANY").
			WithArgs([]uuid.UUID{fromID, toID}, testUserID()).
			WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(2))
		mock.ExpectQuery("INSERT INTO links").
			WithArgs(testUserID(), "transfer", fromID, toID, "").
			WillReturnError(assert.AnError)

		body, _ := json.Marshal(base)
		req, _ := http.NewRequest(http.MethodPost, "/links", bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestCreateLinkTransferUpdateErrors(t *testing.T) {
	fromID := uuid.New()
	toID := uuid.New()
	base := models.CreateLinkRequest{Type: "transfer", FromTxnID: fromID, ToTxnID: toID}

	expectInsert := func(mock pgxmock.PgxPoolIface) {
		mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM transactions WHERE id = ANY").
			WithArgs([]uuid.UUID{fromID, toID}, testUserID()).
			WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(2))
		mock.ExpectQuery("INSERT INTO links").
			WithArgs(testUserID(), "transfer", fromID, toID, "").
			WillReturnRows(pgxmock.NewRows([]string{"id", "type", "from_txn_id", "to_txn_id", "notes", "created_at"}).
				AddRow(uuid.New(), "transfer", fromID, toID, "", time.Now()))
	}

	t.Run("category update", func(t *testing.T) {
		r, srv, mock := newLinkTestRouter(t)
		r.POST("/links", srv.CreateLink)

		mock.ExpectBegin()
		expectInsert(mock)
		mock.ExpectExec("UPDATE transactions SET category_id").
			WithArgs(fromID, toID, testUserID()).
			WillReturnError(assert.AnError)

		body, _ := json.Marshal(base)
		req, _ := http.NewRequest(http.MethodPost, "/links", bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("from payee update", func(t *testing.T) {
		r, srv, mock := newLinkTestRouter(t)
		r.POST("/links", srv.CreateLink)

		mock.ExpectBegin()
		expectInsert(mock)
		mock.ExpectExec("UPDATE transactions SET category_id").
			WithArgs(fromID, toID, testUserID()).
			WillReturnResult(pgxmock.NewResult("UPDATE", 2))
		mock.ExpectExec("UPDATE transactions t1").
			WithArgs(fromID, toID, testUserID()).
			WillReturnError(assert.AnError)

		body, _ := json.Marshal(base)
		req, _ := http.NewRequest(http.MethodPost, "/links", bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("to payee update", func(t *testing.T) {
		r, srv, mock := newLinkTestRouter(t)
		r.POST("/links", srv.CreateLink)

		mock.ExpectBegin()
		expectInsert(mock)
		mock.ExpectExec("UPDATE transactions SET category_id").
			WithArgs(fromID, toID, testUserID()).
			WillReturnResult(pgxmock.NewResult("UPDATE", 2))
		mock.ExpectExec("UPDATE transactions t1").
			WithArgs(fromID, toID, testUserID()).
			WillReturnResult(pgxmock.NewResult("UPDATE", 1))
		mock.ExpectExec("UPDATE transactions t1").
			WithArgs(toID, fromID, testUserID()).
			WillReturnError(assert.AnError)

		body, _ := json.Marshal(base)
		req, _ := http.NewRequest(http.MethodPost, "/links", bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("commit", func(t *testing.T) {
		r, srv, mock := newLinkTestRouter(t)
		r.POST("/links", srv.CreateLink)

		mock.ExpectBegin()
		expectInsert(mock)
		mock.ExpectExec("UPDATE transactions SET category_id").
			WithArgs(fromID, toID, testUserID()).
			WillReturnResult(pgxmock.NewResult("UPDATE", 2))
		mock.ExpectExec("UPDATE transactions t1").
			WithArgs(fromID, toID, testUserID()).
			WillReturnResult(pgxmock.NewResult("UPDATE", 1))
		mock.ExpectExec("UPDATE transactions t1").
			WithArgs(toID, fromID, testUserID()).
			WillReturnResult(pgxmock.NewResult("UPDATE", 1))
		mock.ExpectCommit().WillReturnError(assert.AnError)

		body, _ := json.Marshal(base)
		req, _ := http.NewRequest(http.MethodPost, "/links", bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestBulkCreateLinksErrors(t *testing.T) {
	one := func(txnType string) models.BulkCreateLinksRequest {
		return models.BulkCreateLinksRequest{
			Links: []models.CreateLinkRequest{{Type: txnType, FromTxnID: uuid.New(), ToTxnID: uuid.New()}},
		}
	}

	t.Run("bad json", func(t *testing.T) {
		r, srv, mock := newLinkTestRouter(t)
		r.POST("/links/bulk", srv.BulkCreateLinks)

		req, _ := http.NewRequest(http.MethodPost, "/links/bulk", bytes.NewBufferString("{"))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("too many links", func(t *testing.T) {
		r, srv, mock := newLinkTestRouter(t)
		r.POST("/links/bulk", srv.BulkCreateLinks)

		links := make([]models.CreateLinkRequest, maxBulkBatch+1)
		for i := range links {
			links[i] = models.CreateLinkRequest{Type: "transfer", FromTxnID: uuid.New(), ToTxnID: uuid.New()}
		}
		body, _ := json.Marshal(models.BulkCreateLinksRequest{Links: links})
		req, _ := http.NewRequest(http.MethodPost, "/links/bulk", bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("begin error", func(t *testing.T) {
		r, srv, mock := newLinkTestRouter(t)
		r.POST("/links/bulk", srv.BulkCreateLinks)

		mock.ExpectBegin().WillReturnError(assert.AnError)

		body, _ := json.Marshal(one("transfer"))
		req, _ := http.NewRequest(http.MethodPost, "/links/bulk", bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("invalid link type", func(t *testing.T) {
		r, srv, mock := newLinkTestRouter(t)
		r.POST("/links/bulk", srv.BulkCreateLinks)

		mock.ExpectBegin()

		body, _ := json.Marshal(one("gift"))
		req, _ := http.NewRequest(http.MethodPost, "/links/bulk", bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("self link", func(t *testing.T) {
		r, srv, mock := newLinkTestRouter(t)
		r.POST("/links/bulk", srv.BulkCreateLinks)

		mock.ExpectBegin()
		txnID := uuid.New()
		body, _ := json.Marshal(models.BulkCreateLinksRequest{
			Links: []models.CreateLinkRequest{{Type: "transfer", FromTxnID: txnID, ToTxnID: txnID}},
		})
		req, _ := http.NewRequest(http.MethodPost, "/links/bulk", bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("ownership error", func(t *testing.T) {
		r, srv, mock := newLinkTestRouter(t)
		r.POST("/links/bulk", srv.BulkCreateLinks)

		mock.ExpectBegin()
		mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM transactions WHERE id = ANY").
			WithArgs(pgxmock.AnyArg(), pgxmock.AnyArg()).
			WillReturnError(assert.AnError)

		body, _ := json.Marshal(one("transfer"))
		req, _ := http.NewRequest(http.MethodPost, "/links/bulk", bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("ownership not found", func(t *testing.T) {
		r, srv, mock := newLinkTestRouter(t)
		r.POST("/links/bulk", srv.BulkCreateLinks)

		mock.ExpectBegin()
		mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM transactions WHERE id = ANY").
			WithArgs(pgxmock.AnyArg(), pgxmock.AnyArg()).
			WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(1))

		body, _ := json.Marshal(one("transfer"))
		req, _ := http.NewRequest(http.MethodPost, "/links/bulk", bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusNotFound, w.Code)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("insert error", func(t *testing.T) {
		r, srv, mock := newLinkTestRouter(t)
		r.POST("/links/bulk", srv.BulkCreateLinks)

		mock.ExpectBegin()
		mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM transactions WHERE id = ANY").
			WithArgs(pgxmock.AnyArg(), pgxmock.AnyArg()).
			WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(2))
		mock.ExpectExec("INSERT INTO links").
			WithArgs(pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg()).
			WillReturnError(assert.AnError)

		body, _ := json.Marshal(one("transfer"))
		req, _ := http.NewRequest(http.MethodPost, "/links/bulk", bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("transfer category error", func(t *testing.T) {
		r, srv, mock := newLinkTestRouter(t)
		r.POST("/links/bulk", srv.BulkCreateLinks)

		mock.ExpectBegin()
		mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM transactions WHERE id = ANY").
			WithArgs(pgxmock.AnyArg(), pgxmock.AnyArg()).
			WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(2))
		mock.ExpectExec("INSERT INTO links").
			WithArgs(pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg()).
			WillReturnResult(pgxmock.NewResult("INSERT", 1))
		mock.ExpectExec("UPDATE transactions SET category_id").
			WithArgs(pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg()).
			WillReturnError(assert.AnError)

		body, _ := json.Marshal(one("transfer"))
		req, _ := http.NewRequest(http.MethodPost, "/links/bulk", bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("transfer payee error", func(t *testing.T) {
		r, srv, mock := newLinkTestRouter(t)
		r.POST("/links/bulk", srv.BulkCreateLinks)

		mock.ExpectBegin()
		mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM transactions WHERE id = ANY").
			WithArgs(pgxmock.AnyArg(), pgxmock.AnyArg()).
			WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(2))
		mock.ExpectExec("INSERT INTO links").
			WithArgs(pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg()).
			WillReturnResult(pgxmock.NewResult("INSERT", 1))
		mock.ExpectExec("UPDATE transactions SET category_id").
			WithArgs(pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg()).
			WillReturnResult(pgxmock.NewResult("UPDATE", 2))
		mock.ExpectExec("UPDATE transactions t1").
			WithArgs(pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg()).
			WillReturnError(assert.AnError)

		body, _ := json.Marshal(one("transfer"))
		req, _ := http.NewRequest(http.MethodPost, "/links/bulk", bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("commit error", func(t *testing.T) {
		r, srv, mock := newLinkTestRouter(t)
		r.POST("/links/bulk", srv.BulkCreateLinks)

		mock.ExpectBegin()
		mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM transactions WHERE id = ANY").
			WithArgs(pgxmock.AnyArg(), pgxmock.AnyArg()).
			WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(2))
		mock.ExpectExec("INSERT INTO links").
			WithArgs(pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg()).
			WillReturnResult(pgxmock.NewResult("INSERT", 1))
		mock.ExpectExec("UPDATE transactions SET category_id").
			WithArgs(pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg()).
			WillReturnResult(pgxmock.NewResult("UPDATE", 2))
		mock.ExpectExec("UPDATE transactions t1").
			WithArgs(pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg()).
			WillReturnResult(pgxmock.NewResult("UPDATE", 1))
		mock.ExpectExec("UPDATE transactions t1").
			WithArgs(pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg()).
			WillReturnResult(pgxmock.NewResult("UPDATE", 1))
		mock.ExpectCommit().WillReturnError(assert.AnError)

		body, _ := json.Marshal(one("transfer"))
		req, _ := http.NewRequest(http.MethodPost, "/links/bulk", bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestDeleteLinkErrors(t *testing.T) {
	t.Run("begin error", func(t *testing.T) {
		r, srv, mock := newLinkTestRouter(t)
		r.DELETE("/links/:id", srv.DeleteLink)

		mock.ExpectBegin().WillReturnError(assert.AnError)

		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/links/"+uuid.New().String(), nil))

		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("lookup error", func(t *testing.T) {
		r, srv, mock := newLinkTestRouter(t)
		r.DELETE("/links/:id", srv.DeleteLink)

		mock.ExpectBegin()
		mock.ExpectQuery("SELECT type, from_txn_id, to_txn_id FROM links").
			WithArgs(pgxmock.AnyArg(), pgxmock.AnyArg()).
			WillReturnError(assert.AnError)

		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/links/"+uuid.New().String(), nil))

		assert.Equal(t, http.StatusNotFound, w.Code)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("delete error", func(t *testing.T) {
		r, srv, mock := newLinkTestRouter(t)
		r.DELETE("/links/:id", srv.DeleteLink)

		linkID := uuid.New()
		mock.ExpectBegin()
		mock.ExpectQuery("SELECT type, from_txn_id, to_txn_id FROM links").
			WithArgs(pgxmock.AnyArg(), pgxmock.AnyArg()).
			WillReturnRows(pgxmock.NewRows([]string{"type", "from_txn_id", "to_txn_id"}).
				AddRow("cashback", uuid.New(), uuid.New()))
		mock.ExpectExec("DELETE FROM links").
			WithArgs(pgxmock.AnyArg(), pgxmock.AnyArg()).
			WillReturnError(assert.AnError)

		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/links/"+linkID.String(), nil))

		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("transfer reset error", func(t *testing.T) {
		r, srv, mock := newLinkTestRouter(t)
		r.DELETE("/links/:id", srv.DeleteLink)

		linkID := uuid.New()
		mock.ExpectBegin()
		mock.ExpectQuery("SELECT type, from_txn_id, to_txn_id FROM links").
			WithArgs(pgxmock.AnyArg(), pgxmock.AnyArg()).
			WillReturnRows(pgxmock.NewRows([]string{"type", "from_txn_id", "to_txn_id"}).
				AddRow("transfer", uuid.New(), uuid.New()))
		mock.ExpectExec("DELETE FROM links").
			WithArgs(pgxmock.AnyArg(), pgxmock.AnyArg()).
			WillReturnResult(pgxmock.NewResult("DELETE", 1))
		mock.ExpectExec("UPDATE transactions").
			WithArgs(pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg()).
			WillReturnError(assert.AnError)

		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/links/"+linkID.String(), nil))

		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("commit error", func(t *testing.T) {
		r, srv, mock := newLinkTestRouter(t)
		r.DELETE("/links/:id", srv.DeleteLink)

		linkID := uuid.New()
		mock.ExpectBegin()
		mock.ExpectQuery("SELECT type, from_txn_id, to_txn_id FROM links").
			WithArgs(pgxmock.AnyArg(), pgxmock.AnyArg()).
			WillReturnRows(pgxmock.NewRows([]string{"type", "from_txn_id", "to_txn_id"}).
				AddRow("cashback", uuid.New(), uuid.New()))
		mock.ExpectExec("DELETE FROM links").
			WithArgs(pgxmock.AnyArg(), pgxmock.AnyArg()).
			WillReturnResult(pgxmock.NewResult("DELETE", 1))
		mock.ExpectCommit().WillReturnError(assert.AnError)

		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/links/"+linkID.String(), nil))

		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestBulkDeleteLinksErrors(t *testing.T) {
	t.Run("bad json", func(t *testing.T) {
		r, srv, mock := newLinkTestRouter(t)
		r.POST("/links/bulk-delete", srv.BulkDeleteLinks)

		req, _ := http.NewRequest(http.MethodPost, "/links/bulk-delete", bytes.NewBufferString("{"))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("begin error", func(t *testing.T) {
		r, srv, mock := newLinkTestRouter(t)
		r.POST("/links/bulk-delete", srv.BulkDeleteLinks)

		mock.ExpectBegin().WillReturnError(assert.AnError)

		body, _ := json.Marshal(models.BulkDeleteLinksRequest{IDs: []uuid.UUID{uuid.New()}})
		req, _ := http.NewRequest(http.MethodPost, "/links/bulk-delete", bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("query error", func(t *testing.T) {
		r, srv, mock := newLinkTestRouter(t)
		r.POST("/links/bulk-delete", srv.BulkDeleteLinks)

		mock.ExpectBegin()
		mock.ExpectQuery("SELECT type, from_txn_id, to_txn_id FROM links").
			WithArgs(pgxmock.AnyArg(), pgxmock.AnyArg()).
			WillReturnError(assert.AnError)

		body, _ := json.Marshal(models.BulkDeleteLinksRequest{IDs: []uuid.UUID{uuid.New()}})
		req, _ := http.NewRequest(http.MethodPost, "/links/bulk-delete", bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("delete error", func(t *testing.T) {
		r, srv, mock := newLinkTestRouter(t)
		r.POST("/links/bulk-delete", srv.BulkDeleteLinks)

		mock.ExpectBegin()
		mock.ExpectQuery("SELECT type, from_txn_id, to_txn_id FROM links").
			WithArgs(pgxmock.AnyArg(), pgxmock.AnyArg()).
			WillReturnRows(pgxmock.NewRows([]string{"type", "from_txn_id", "to_txn_id"}))
		mock.ExpectExec("DELETE FROM links").
			WithArgs(pgxmock.AnyArg(), pgxmock.AnyArg()).
			WillReturnError(assert.AnError)

		body, _ := json.Marshal(models.BulkDeleteLinksRequest{IDs: []uuid.UUID{uuid.New()}})
		req, _ := http.NewRequest(http.MethodPost, "/links/bulk-delete", bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("reset error", func(t *testing.T) {
		r, srv, mock := newLinkTestRouter(t)
		r.POST("/links/bulk-delete", srv.BulkDeleteLinks)

		mock.ExpectBegin()
		mock.ExpectQuery("SELECT type, from_txn_id, to_txn_id FROM links").
			WithArgs(pgxmock.AnyArg(), pgxmock.AnyArg()).
			WillReturnRows(pgxmock.NewRows([]string{"type", "from_txn_id", "to_txn_id"}).
				AddRow("transfer", uuid.New(), uuid.New()))
		mock.ExpectExec("DELETE FROM links").
			WithArgs(pgxmock.AnyArg(), pgxmock.AnyArg()).
			WillReturnResult(pgxmock.NewResult("DELETE", 1))
		mock.ExpectExec("UPDATE transactions").
			WithArgs(pgxmock.AnyArg(), pgxmock.AnyArg()).
			WillReturnError(assert.AnError)

		body, _ := json.Marshal(models.BulkDeleteLinksRequest{IDs: []uuid.UUID{uuid.New()}})
		req, _ := http.NewRequest(http.MethodPost, "/links/bulk-delete", bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("commit error", func(t *testing.T) {
		r, srv, mock := newLinkTestRouter(t)
		r.POST("/links/bulk-delete", srv.BulkDeleteLinks)

		mock.ExpectBegin()
		mock.ExpectQuery("SELECT type, from_txn_id, to_txn_id FROM links").
			WithArgs(pgxmock.AnyArg(), pgxmock.AnyArg()).
			WillReturnRows(pgxmock.NewRows([]string{"type", "from_txn_id", "to_txn_id"}))
		mock.ExpectExec("DELETE FROM links").
			WithArgs(pgxmock.AnyArg(), pgxmock.AnyArg()).
			WillReturnResult(pgxmock.NewResult("DELETE", 1))
		mock.ExpectCommit().WillReturnError(assert.AnError)

		body, _ := json.Marshal(models.BulkDeleteLinksRequest{IDs: []uuid.UUID{uuid.New()}})
		req, _ := http.NewRequest(http.MethodPost, "/links/bulk-delete", bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestGetCashbackSuggestionsErrors(t *testing.T) {
	t.Run("query error", func(t *testing.T) {
		r, srv, mock := newLinkTestRouter(t)
		r.GET("/links/cashback-suggestions", srv.GetCashbackSuggestions)

		mock.ExpectQuery("SELECT cb.id, cb.account_id").
			WithArgs(pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg()).
			WillReturnError(assert.AnError)

		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/links/cashback-suggestions", nil))

		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("scan error", func(t *testing.T) {
		r, srv, mock := newLinkTestRouter(t)
		r.GET("/links/cashback-suggestions", srv.GetCashbackSuggestions)

		mock.ExpectQuery("SELECT cb.id, cb.account_id").
			WithArgs(pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg()).
			WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow(uuid.New()))

		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/links/cashback-suggestions", nil))

		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestGetTransferSuggestionsScanErrorIsSkipped(t *testing.T) {
	r, srv, mock := newLinkTestRouter(t)
	r.GET("/links/transfer-suggestions", srv.GetTransferSuggestions)

	mock.ExpectQuery("SELECT d.id, d.account_id").
		WithArgs(pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg()).
		WillReturnRows(pgxmock.NewRows([]string{"id"}).AddRow(uuid.New()))

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/links/transfer-suggestions", nil))

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"hasMore":false`)
	assert.NoError(t, mock.ExpectationsWereMet())
}
