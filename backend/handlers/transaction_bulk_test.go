package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"

	"github.com/fintrak/backend/models"
	"github.com/google/uuid"
	"github.com/pashagolub/pgxmock/v5"
	"github.com/stretchr/testify/assert"
)

// oversizeBulkIDs builds one more id than a bulk endpoint accepts.
func oversizeBulkIDs() []uuid.UUID {
	ids := make([]uuid.UUID, maxBulkBatch+1)
	for i := range ids {
		ids[i] = uuid.New()
	}
	return ids
}

func TestBulkCategorizeInvalidCategoryID(t *testing.T) {
	r, srv, mock := newTransactionTestRouter(t)
	r.POST("/transactions/bulk-categorize", srv.BulkCategorize)

	body, _ := json.Marshal(map[string]any{
		"transactionIds": []uuid.UUID{uuid.New()},
		"categoryId":     "not-a-uuid",
	})
	req, _ := http.NewRequest(http.MethodPost, "/transactions/bulk-categorize", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "invalid category id")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestBulkCategorizeExecError(t *testing.T) {
	r, srv, mock := newTransactionTestRouter(t)
	r.POST("/transactions/bulk-categorize", srv.BulkCategorize)

	catID := uuid.New()
	mock.ExpectExec("UPDATE transactions SET category_id").
		WithArgs(catID, pgxmock.AnyArg(), testUserID()).
		WillReturnError(assert.AnError)

	body, _ := json.Marshal(models.BulkCategorizeRequest{
		TransactionIDs: []uuid.UUID{uuid.New()},
		CategoryID:     catID.String(),
	})
	req, _ := http.NewRequest(http.MethodPost, "/transactions/bulk-categorize", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestBulkUpdatePayeeErrors(t *testing.T) {
	t.Run("invalid json", func(t *testing.T) {
		r, srv, mock := newTransactionTestRouter(t)
		r.POST("/transactions/bulk-payee", srv.BulkUpdatePayee)

		req, _ := http.NewRequest(http.MethodPost, "/transactions/bulk-payee", bytes.NewBufferString("{"))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("too many ids", func(t *testing.T) {
		r, srv, mock := newTransactionTestRouter(t)
		r.POST("/transactions/bulk-payee", srv.BulkUpdatePayee)

		body, _ := json.Marshal(map[string]any{
			"transactionIds": oversizeBulkIDs(),
			"payeeId":        uuid.New(),
		})
		req, _ := http.NewRequest(http.MethodPost, "/transactions/bulk-payee", bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("exec error", func(t *testing.T) {
		r, srv, mock := newTransactionTestRouter(t)
		r.POST("/transactions/bulk-payee", srv.BulkUpdatePayee)

		payeeID := uuid.New()
		mock.ExpectExec("UPDATE transactions SET payee_id").
			WithArgs(payeeID, pgxmock.AnyArg(), testUserID()).
			WillReturnError(assert.AnError)

		body, _ := json.Marshal(models.BulkUpdatePayeeRequest{
			TransactionIDs: []uuid.UUID{uuid.New()},
			PayeeID:        payeeID,
		})
		req, _ := http.NewRequest(http.MethodPost, "/transactions/bulk-payee", bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestBulkUpdateBillingCycleErrors(t *testing.T) {
	t.Run("invalid json", func(t *testing.T) {
		r, srv, mock := newTransactionTestRouter(t)
		r.POST("/transactions/bulk-billing-cycle", srv.BulkUpdateBillingCycle)

		req, _ := http.NewRequest(http.MethodPost, "/transactions/bulk-billing-cycle", bytes.NewBufferString("{"))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("too many ids", func(t *testing.T) {
		r, srv, mock := newTransactionTestRouter(t)
		r.POST("/transactions/bulk-billing-cycle", srv.BulkUpdateBillingCycle)

		body, _ := json.Marshal(map[string]any{
			"transactionIds": oversizeBulkIDs(),
			"billingCycleId": uuid.New(),
		})
		req, _ := http.NewRequest(http.MethodPost, "/transactions/bulk-billing-cycle", bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("exec error", func(t *testing.T) {
		r, srv, mock := newTransactionTestRouter(t)
		r.POST("/transactions/bulk-billing-cycle", srv.BulkUpdateBillingCycle)

		cycleID := uuid.New()
		// A bulk assignment is an assignment: it also clears the
		// billing_cycle_detached flag the "Unassigned" action set.
		mock.ExpectExec(regexp.QuoteMeta("UPDATE transactions SET billing_cycle_id = $1, billing_cycle_detached = FALSE")).
			WithArgs(cycleID, pgxmock.AnyArg(), testUserID()).
			WillReturnError(assert.AnError)

		body, _ := json.Marshal(models.BulkBillingCycleRequest{
			TransactionIDs: []uuid.UUID{uuid.New()},
			BillingCycleID: cycleID,
		})
		req, _ := http.NewRequest(http.MethodPost, "/transactions/bulk-billing-cycle", bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestBulkDeleteTransactionsErrors(t *testing.T) {
	t.Run("invalid json", func(t *testing.T) {
		r, srv, mock := newTransactionTestRouter(t)
		r.POST("/transactions/bulk-delete", srv.BulkDeleteTransactions)

		req, _ := http.NewRequest(http.MethodPost, "/transactions/bulk-delete", bytes.NewBufferString("{"))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("too many ids", func(t *testing.T) {
		r, srv, mock := newTransactionTestRouter(t)
		r.POST("/transactions/bulk-delete", srv.BulkDeleteTransactions)

		body, _ := json.Marshal(map[string]any{"transactionIds": oversizeBulkIDs()})
		req, _ := http.NewRequest(http.MethodPost, "/transactions/bulk-delete", bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("exec error", func(t *testing.T) {
		r, srv, mock := newTransactionTestRouter(t)
		r.POST("/transactions/bulk-delete", srv.BulkDeleteTransactions)

		mock.ExpectExec("DELETE FROM transactions WHERE id = ANY").
			WithArgs(pgxmock.AnyArg(), testUserID()).
			WillReturnError(assert.AnError)

		body, _ := json.Marshal(models.BulkDeleteTransactionsRequest{
			TransactionIDs: []uuid.UUID{uuid.New()},
		})
		req, _ := http.NewRequest(http.MethodPost, "/transactions/bulk-delete", bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}
