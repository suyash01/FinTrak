package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"
	"time"

	"github.com/fintrak/backend/internal/money"
	"github.com/fintrak/backend/models"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/pashagolub/pgxmock/v5"
	"github.com/stretchr/testify/assert"
)

func TestCalculateTransferScore(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name      string
		debitTxn  models.Transaction
		creditTxn models.Transaction
		minScore  float64
		maxScore  float64
	}{
		{
			name: "perfect match",
			debitTxn: models.Transaction{
				Amount:      money.FromFloat(1000),
				Date:        now,
				Description: "Transfer to Savings",
			},
			creditTxn: models.Transaction{
				Amount:      money.FromFloat(1000),
				Date:        now,
				Description: "Transfer from Checking",
			},
			minScore: 95,
			maxScore: 100,
		},
		{
			name: "date difference",
			debitTxn: models.Transaction{
				Amount:      money.FromFloat(1000),
				Date:        now,
				Description: "Rent",
			},
			creditTxn: models.Transaction{
				Amount:      money.FromFloat(1000),
				Date:        now.Add(48 * time.Hour), // 2 days later
				Description: "Rent",
			},
			minScore: 80,
			maxScore: 95,
		},
		{
			name: "keyword boost",
			debitTxn: models.Transaction{
				Amount:      money.FromFloat(500),
				Date:        now,
				Description: "UPI-PAY-123",
			},
			creditTxn: models.Transaction{
				Amount:      money.FromFloat(500),
				Date:        now,
				Description: "UPI-REC-123",
			},
			minScore: 90,
			maxScore: 100,
		},
		{
			name: "amount mismatch",
			debitTxn: models.Transaction{
				Amount: money.FromFloat(1000),
				Date:   now,
			},
			creditTxn: models.Transaction{
				Amount: money.FromFloat(1050), // 50 diff
				Date:   now,
			},
			minScore: 0,
			maxScore: 60,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			score := calculateTransferScore(tt.debitTxn, tt.creditTxn)
			assert.GreaterOrEqual(t, score, tt.minScore)
			assert.LessOrEqual(t, score, tt.maxScore)
		})
	}
}

func newLinkTestRouter(t *testing.T) (*gin.Engine, *Server, pgxmock.PgxPoolIface) {
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

func TestGetLinks(t *testing.T) {
	r, srv, mock := newLinkTestRouter(t)
	r.GET("/links", srv.GetLinks)

	userID := testUserID()
	linkID := uuid.New()
	fromID := uuid.New()
	toID := uuid.New()
	now := time.Now()

	rows := pgxmock.NewRows([]string{"id", "type", "from_txn_id", "to_txn_id", "notes", "created_at",
		"ft_date", "ft_description", "ft_amount", "ft_type", "fa_name",
		"tt_date", "tt_description", "tt_amount", "tt_type", "ta_name"}).
		AddRow(linkID, "transfer", fromID, toID, "note", now,
			now, "Debit desc", 100.0, "debit", "Checking",
			now, "Credit desc", 100.0, "credit", "Savings")

	mock.ExpectQuery("SELECT l.id, l.type, l.from_txn_id").
		WithArgs(userID).
		WillReturnRows(rows)

	req, _ := http.NewRequest("GET", "/links", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var links []models.Link
	assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &links))
	assert.Len(t, links, 1)
	assert.Equal(t, "transfer", links[0].Type)
	assert.Equal(t, fromID, links[0].FromTxnID)
	assert.NotNil(t, links[0].FromTxn)
	assert.Equal(t, "Debit desc", links[0].FromTxn.Description)
	assert.Equal(t, "Checking", links[0].FromTxn.AccountName)
	assert.Equal(t, "Credit desc", links[0].ToTxn.Description)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetLinksWithFilters(t *testing.T) {
	r, srv, mock := newLinkTestRouter(t)
	r.GET("/links", srv.GetLinks)

	userID := testUserID()
	txnID := uuid.New()
	now := time.Now()

	rows := pgxmock.NewRows([]string{"id", "type", "from_txn_id", "to_txn_id", "notes", "created_at",
		"ft_date", "ft_description", "ft_amount", "ft_type", "fa_name",
		"tt_date", "tt_description", "tt_amount", "tt_type", "ta_name"}).
		AddRow(uuid.New(), "cashback", txnID, uuid.New(), "", now,
			now, "Purchase", 500.0, "debit", "Savings",
			now, "Cashback", 50.0, "credit", "Savings")

	mock.ExpectQuery("SELECT l.id, l.type, l.from_txn_id").
		WithArgs(userID, "cashback", txnID.String(), txnID.String()).
		WillReturnRows(rows)

	req, _ := http.NewRequest("GET", "/links?type=cashback&txnId="+txnID.String(), nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateLinkTransfer(t *testing.T) {
	r, srv, mock := newLinkTestRouter(t)
	r.POST("/links", srv.CreateLink)

	userID := testUserID()
	fromID := uuid.New()
	toID := uuid.New()
	linkID := uuid.New()
	now := time.Now()

	reqBody := models.CreateLinkRequest{
		Type:      "transfer",
		FromTxnID: fromID,
		ToTxnID:   toID,
		Notes:     "self transfer",
	}

	mock.ExpectBegin()

	// Ownership check: both transactions belong to the user.
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM transactions WHERE id = ANY").
		WithArgs([]uuid.UUID{fromID, toID}, userID).
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(2))

	// Duplicate check.
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM links WHERE user_id").
		WithArgs(userID, "transfer", fromID, toID).
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(0))

	// Insert link.
	mock.ExpectQuery("INSERT INTO links").
		WithArgs(userID, "transfer", fromID, toID, "self transfer").
		WillReturnRows(pgxmock.NewRows([]string{"id", "type", "from_txn_id", "to_txn_id", "notes", "created_at"}).
			AddRow(linkID, "transfer", fromID, toID, "self transfer", now))

	// Transfer auto-categorization + payee updates.
	mock.ExpectExec("UPDATE transactions SET category_id").
		WithArgs(fromID, toID, userID).
		WillReturnResult(pgxmock.NewResult("UPDATE", 2))
	mock.ExpectExec("UPDATE transactions t1").
		WithArgs(fromID, toID, userID).
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectExec("UPDATE transactions t1").
		WithArgs(toID, fromID, userID).
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))

	mock.ExpectCommit()

	body, _ := json.Marshal(reqBody)
	req, _ := http.NewRequest("POST", "/links", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)

	var link models.Link
	assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &link))
	assert.Equal(t, linkID, link.ID)
	assert.Equal(t, "transfer", link.Type)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateLinkCashbackSkipsTransferUpdates(t *testing.T) {
	r, srv, mock := newLinkTestRouter(t)
	r.POST("/links", srv.CreateLink)

	userID := testUserID()
	fromID := uuid.New()
	toID := uuid.New()
	linkID := uuid.New()
	now := time.Now()

	reqBody := models.CreateLinkRequest{
		Type:      "cashback",
		FromTxnID: fromID,
		ToTxnID:   toID,
	}

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM transactions WHERE id = ANY").
		WithArgs([]uuid.UUID{fromID, toID}, userID).
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(2))
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM links WHERE user_id").
		WithArgs(userID, "cashback", fromID, toID).
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectQuery("INSERT INTO links").
		WithArgs(userID, "cashback", fromID, toID, "").
		WillReturnRows(pgxmock.NewRows([]string{"id", "type", "from_txn_id", "to_txn_id", "notes", "created_at"}).
			AddRow(linkID, "cashback", fromID, toID, "", now))
	mock.ExpectCommit()

	body, _ := json.Marshal(reqBody)
	req, _ := http.NewRequest("POST", "/links", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateLinkBillPaymentSkipsTransferUpdates(t *testing.T) {
	r, srv, mock := newLinkTestRouter(t)
	r.POST("/links", srv.CreateLink)

	userID := testUserID()
	fromID := uuid.New()
	toID := uuid.New()
	linkID := uuid.New()
	now := time.Now()

	reqBody := models.CreateLinkRequest{
		Type:      "bill_payment",
		FromTxnID: fromID,
		ToTxnID:   toID,
	}

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM transactions WHERE id = ANY").
		WithArgs([]uuid.UUID{fromID, toID}, userID).
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(2))
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM links WHERE user_id").
		WithArgs(userID, "bill_payment", fromID, toID).
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectQuery("INSERT INTO links").
		WithArgs(userID, "bill_payment", fromID, toID, "").
		WillReturnRows(pgxmock.NewRows([]string{"id", "type", "from_txn_id", "to_txn_id", "notes", "created_at"}).
			AddRow(linkID, "bill_payment", fromID, toID, "", now))
	mock.ExpectCommit()

	body, _ := json.Marshal(reqBody)
	req, _ := http.NewRequest("POST", "/links", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)
	var link models.Link
	assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &link))
	assert.Equal(t, "bill_payment", link.Type)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateLinkInvalidType(t *testing.T) {
	r, srv, mock := newLinkTestRouter(t)
	r.POST("/links", srv.CreateLink)

	reqBody := models.CreateLinkRequest{
		Type:      "gift",
		FromTxnID: uuid.New(),
		ToTxnID:   uuid.New(),
	}

	mock.ExpectBegin()

	body, _ := json.Marshal(reqBody)
	req, _ := http.NewRequest("POST", "/links", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateLinkRejectsSelfLink(t *testing.T) {
	r, srv, mock := newLinkTestRouter(t)
	r.POST("/links", srv.CreateLink)

	txnID := uuid.New()
	reqBody := models.CreateLinkRequest{
		Type:      "transfer",
		FromTxnID: txnID,
		ToTxnID:   txnID,
	}

	mock.ExpectBegin()

	body, _ := json.Marshal(reqBody)
	req, _ := http.NewRequest("POST", "/links", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "itself")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateLinkOwnershipNotFound(t *testing.T) {
	r, srv, mock := newLinkTestRouter(t)
	r.POST("/links", srv.CreateLink)

	userID := testUserID()
	fromID := uuid.New()
	toID := uuid.New()

	reqBody := models.CreateLinkRequest{
		Type:      "transfer",
		FromTxnID: fromID,
		ToTxnID:   toID,
	}

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM transactions WHERE id = ANY").
		WithArgs([]uuid.UUID{fromID, toID}, userID).
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(1))

	body, _ := json.Marshal(reqBody)
	req, _ := http.NewRequest("POST", "/links", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateLinkDuplicate(t *testing.T) {
	r, srv, mock := newLinkTestRouter(t)
	r.POST("/links", srv.CreateLink)

	userID := testUserID()
	fromID := uuid.New()
	toID := uuid.New()

	reqBody := models.CreateLinkRequest{
		Type:      "transfer",
		FromTxnID: fromID,
		ToTxnID:   toID,
	}

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM transactions WHERE id = ANY").
		WithArgs([]uuid.UUID{fromID, toID}, userID).
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(2))
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM links WHERE user_id").
		WithArgs(userID, "transfer", fromID, toID).
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(1))

	body, _ := json.Marshal(reqBody)
	req, _ := http.NewRequest("POST", "/links", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusConflict, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateLinkBadJSON(t *testing.T) {
	r, srv, mock := newLinkTestRouter(t)
	r.POST("/links", srv.CreateLink)

	req, _ := http.NewRequest("POST", "/links", bytes.NewBufferString("{not json"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestBulkCreateLinks(t *testing.T) {
	r, srv, mock := newLinkTestRouter(t)
	r.POST("/links/bulk", srv.BulkCreateLinks)

	userID := testUserID()
	fromID := uuid.New()
	toID := uuid.New()

	reqBody := models.BulkCreateLinksRequest{
		Links: []models.CreateLinkRequest{
			{Type: "transfer", FromTxnID: fromID, ToTxnID: toID, Notes: "n1"},
			{Type: "cashback", FromTxnID: toID, ToTxnID: fromID, Notes: "n2"},
			{Type: "bill_payment", FromTxnID: fromID, ToTxnID: toID, Notes: "n3"},
		},
	}

	mock.ExpectBegin()

	// Link 1: transfer
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM transactions WHERE id = ANY").
		WithArgs([]uuid.UUID{fromID, toID}, userID).
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(2))
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM links WHERE user_id").
		WithArgs(userID, "transfer", fromID, toID).
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectExec("INSERT INTO links").
		WithArgs(userID, "transfer", fromID, toID, "n1").
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectExec("UPDATE transactions SET category_id").
		WithArgs(fromID, toID, userID).
		WillReturnResult(pgxmock.NewResult("UPDATE", 2))
	mock.ExpectExec("UPDATE transactions t1").
		WithArgs(fromID, toID, userID).
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	mock.ExpectExec("UPDATE transactions t1").
		WithArgs(toID, fromID, userID).
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))

	// Link 2: cashback (no transfer updates)
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM transactions WHERE id = ANY").
		WithArgs([]uuid.UUID{toID, fromID}, userID).
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(2))
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM links WHERE user_id").
		WithArgs(userID, "cashback", toID, fromID).
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectExec("INSERT INTO links").
		WithArgs(userID, "cashback", toID, fromID, "n2").
		WillReturnResult(pgxmock.NewResult("INSERT", 1))

	// Link 3: bill payment (no transfer updates)
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM transactions WHERE id = ANY").
		WithArgs([]uuid.UUID{fromID, toID}, userID).
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(2))
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM links WHERE user_id").
		WithArgs(userID, "bill_payment", fromID, toID).
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(0))
	mock.ExpectExec("INSERT INTO links").
		WithArgs(userID, "bill_payment", fromID, toID, "n3").
		WillReturnResult(pgxmock.NewResult("INSERT", 1))

	mock.ExpectCommit()

	body, _ := json.Marshal(reqBody)
	req, _ := http.NewRequest("POST", "/links/bulk", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"createdCount":3`)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestBulkCreateLinksSkipsDuplicates(t *testing.T) {
	r, srv, mock := newLinkTestRouter(t)
	r.POST("/links/bulk", srv.BulkCreateLinks)

	userID := testUserID()
	fromID := uuid.New()
	toID := uuid.New()

	reqBody := models.BulkCreateLinksRequest{
		Links: []models.CreateLinkRequest{
			{Type: "cashback", FromTxnID: fromID, ToTxnID: toID},
		},
	}

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM transactions WHERE id = ANY").
		WithArgs([]uuid.UUID{fromID, toID}, userID).
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(2))
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM links WHERE user_id").
		WithArgs(userID, "cashback", fromID, toID).
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(1))
	mock.ExpectCommit()

	body, _ := json.Marshal(reqBody)
	req, _ := http.NewRequest("POST", "/links/bulk", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"createdCount":0`)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestBulkCreateLinksRejectsSelfLink(t *testing.T) {
	r, srv, mock := newLinkTestRouter(t)
	r.POST("/links/bulk", srv.BulkCreateLinks)

	txnID := uuid.New()
	reqBody := models.BulkCreateLinksRequest{
		Links: []models.CreateLinkRequest{
			{Type: "transfer", FromTxnID: txnID, ToTxnID: txnID},
		},
	}

	mock.ExpectBegin()

	body, _ := json.Marshal(reqBody)
	req, _ := http.NewRequest("POST", "/links/bulk", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "itself")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestDeleteLink(t *testing.T) {
	r, srv, mock := newLinkTestRouter(t)
	r.DELETE("/links/:id", srv.DeleteLink)

	userID := testUserID()
	linkID := uuid.New()
	fromID := uuid.New()
	toID := uuid.New()

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT type, from_txn_id, to_txn_id FROM links WHERE id").
		WithArgs(linkID, userID).
		WillReturnRows(pgxmock.NewRows([]string{"type", "from_txn_id", "to_txn_id"}).AddRow("transfer", fromID, toID))
	mock.ExpectExec("DELETE FROM links WHERE id").
		WithArgs(linkID, userID).
		WillReturnResult(pgxmock.NewResult("DELETE", 1))
	mock.ExpectExec("UPDATE transactions").
		WithArgs([]uuid.UUID{fromID, toID}, userID, linkID).
		WillReturnResult(pgxmock.NewResult("UPDATE", 2))
	mock.ExpectCommit()

	req, _ := http.NewRequest("DELETE", "/links/"+linkID.String(), nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestDeleteLinkNonTransferKeepsCategory(t *testing.T) {
	r, srv, mock := newLinkTestRouter(t)
	r.DELETE("/links/:id", srv.DeleteLink)

	userID := testUserID()
	linkID := uuid.New()
	fromID := uuid.New()
	toID := uuid.New()

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT type, from_txn_id, to_txn_id FROM links WHERE id").
		WithArgs(linkID, userID).
		WillReturnRows(pgxmock.NewRows([]string{"type", "from_txn_id", "to_txn_id"}).AddRow("cashback", fromID, toID))
	mock.ExpectExec("DELETE FROM links WHERE id").
		WithArgs(linkID, userID).
		WillReturnResult(pgxmock.NewResult("DELETE", 1))
	// No UPDATE expected: non-transfer links leave category/payee intact.
	mock.ExpectCommit()

	req, _ := http.NewRequest("DELETE", "/links/"+linkID.String(), nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestDeleteLinkNotFound(t *testing.T) {
	r, srv, mock := newLinkTestRouter(t)
	r.DELETE("/links/:id", srv.DeleteLink)

	userID := testUserID()
	linkID := uuid.New()

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT type, from_txn_id, to_txn_id FROM links WHERE id").
		WithArgs(linkID, userID).
		WillReturnError(pgx.ErrNoRows)

	req, _ := http.NewRequest("DELETE", "/links/"+linkID.String(), nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestDeleteLinkInvalidID(t *testing.T) {
	r, srv, mock := newLinkTestRouter(t)
	r.DELETE("/links/:id", srv.DeleteLink)

	req, _ := http.NewRequest("DELETE", "/links/not-a-uuid", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestBulkDeleteLinks(t *testing.T) {
	r, srv, mock := newLinkTestRouter(t)
	r.POST("/links/bulk-delete", srv.BulkDeleteLinks)

	userID := testUserID()
	linkID1 := uuid.New()
	linkID2 := uuid.New()
	fromID := uuid.New()
	toID := uuid.New()

	reqBody := models.BulkDeleteLinksRequest{IDs: []uuid.UUID{linkID1, linkID2}}

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT type, from_txn_id, to_txn_id FROM links WHERE id = ANY").
		WithArgs([]uuid.UUID{linkID1, linkID2}, userID).
		WillReturnRows(pgxmock.NewRows([]string{"type", "from_txn_id", "to_txn_id"}).
			AddRow("transfer", fromID, toID))
	mock.ExpectExec("DELETE FROM links WHERE id = ANY").
		WithArgs([]uuid.UUID{linkID1, linkID2}, userID).
		WillReturnResult(pgxmock.NewResult("DELETE", 2))
	mock.ExpectExec("UPDATE transactions SET category_id = NULL").
		WithArgs([]uuid.UUID{fromID, toID}, userID).
		WillReturnResult(pgxmock.NewResult("UPDATE", 2))
	mock.ExpectCommit()

	body, _ := json.Marshal(reqBody)
	req, _ := http.NewRequest("POST", "/links/bulk-delete", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"deletedCount":2`)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestBulkDeleteLinksNonTransferKeepsCategory(t *testing.T) {
	r, srv, mock := newLinkTestRouter(t)
	r.POST("/links/bulk-delete", srv.BulkDeleteLinks)

	userID := testUserID()
	linkID1 := uuid.New()
	linkID2 := uuid.New()
	fromID := uuid.New()
	toID := uuid.New()

	reqBody := models.BulkDeleteLinksRequest{IDs: []uuid.UUID{linkID1, linkID2}}

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT type, from_txn_id, to_txn_id FROM links WHERE id = ANY").
		WithArgs([]uuid.UUID{linkID1, linkID2}, userID).
		WillReturnRows(pgxmock.NewRows([]string{"type", "from_txn_id", "to_txn_id"}).
			AddRow("refund", fromID, toID))
	mock.ExpectExec("DELETE FROM links WHERE id = ANY").
		WithArgs([]uuid.UUID{linkID1, linkID2}, userID).
		WillReturnResult(pgxmock.NewResult("DELETE", 2))
	// No UPDATE expected: non-transfer links leave category/payee intact.
	mock.ExpectCommit()

	body, _ := json.Marshal(reqBody)
	req, _ := http.NewRequest("POST", "/links/bulk-delete", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"deletedCount":2`)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestBulkDeleteLinksTooManyIDs(t *testing.T) {
	r, srv, mock := newLinkTestRouter(t)
	r.POST("/links/bulk-delete", srv.BulkDeleteLinks)

	ids := make([]uuid.UUID, maxBulkBatch+1)
	for i := range ids {
		ids[i] = uuid.New()
	}
	body, _ := json.Marshal(models.BulkDeleteLinksRequest{IDs: ids})
	req, _ := http.NewRequest("POST", "/links/bulk-delete", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "too many link ids")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestBulkDeleteLinksEmpty(t *testing.T) {
	r, srv, mock := newLinkTestRouter(t)
	r.POST("/links/bulk-delete", srv.BulkDeleteLinks)

	reqBody := models.BulkDeleteLinksRequest{IDs: []uuid.UUID{}}
	body, _ := json.Marshal(reqBody)
	req, _ := http.NewRequest("POST", "/links/bulk-delete", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"deletedCount":0`)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetTransferSuggestionsExcludesLinkedCredits(t *testing.T) {
	r, srv, mock := newLinkTestRouter(t)
	r.GET("/links/transfer-suggestions", srv.GetTransferSuggestions)

	// The lateral must never suggest a credit transaction that is already on
	// either side of a link — the strict matcher pins the exact clause so a
	// future revert to the debit-side-only check fails this test.
	mock.ExpectQuery(regexp.QuoteMeta("AND NOT EXISTS (SELECT 1 FROM links WHERE (from_txn_id = t.id OR to_txn_id = t.id))")).
		WithArgs(testUserID(), defaultSuggestionLimit+1, 0).
		WillReturnRows(pgxmock.NewRows([]string{"d_id", "d_account_id", "d_date", "d_description", "d_amount", "d_type", "d_account",
			"c_id", "c_account_id", "c_date", "c_description", "c_amount", "c_type", "c_account"}))

	req, _ := http.NewRequest(http.MethodGet, "/links/transfer-suggestions", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetTransferSuggestions(t *testing.T) {
	r, srv, mock := newLinkTestRouter(t)
	r.GET("/links/transfer-suggestions", srv.GetTransferSuggestions)

	userID := testUserID()
	now := time.Now()

	rows := pgxmock.NewRows([]string{"d_id", "d_account_id", "d_date", "d_description", "d_amount", "d_type", "d_account",
		"c_id", "c_account_id", "c_date", "c_description", "c_amount", "c_type", "c_account"}).
		AddRow(uuid.New(), uuid.New(), now, "UPI Transfer", 100.0, "debit", "Checking",
			uuid.New(), uuid.New(), now, "UPI Received", 100.0, "credit", "Savings")

	mock.ExpectQuery("SELECT d.id, d.account_id").
		WithArgs(userID, defaultSuggestionLimit+1, 0).
		WillReturnRows(rows)

	req, _ := http.NewRequest("GET", "/links/transfer-suggestions", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp struct {
		Data    []models.TransferSuggestion `json:"data"`
		Page    int                         `json:"page"`
		Limit   int                         `json:"limit"`
		HasMore bool                        `json:"hasMore"`
	}
	assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Len(t, resp.Data, 1)
	assert.Equal(t, 1, resp.Page)
	assert.Equal(t, defaultSuggestionLimit, resp.Limit)
	assert.False(t, resp.HasMore)
	assert.Equal(t, "UPI Transfer", resp.Data[0].DebitTxn.Description)
	assert.Equal(t, "UPI Received", resp.Data[0].CreditTxn.Description)
	assert.Greater(t, resp.Data[0].Score, 0.0)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetTransferSuggestionsPagination(t *testing.T) {
	r, srv, mock := newLinkTestRouter(t)
	r.GET("/links/transfer-suggestions", srv.GetTransferSuggestions)

	userID := testUserID()

	// page=2, limit=10 -> offset 10 and an 11-row fetch for hasMore.
	mock.ExpectQuery("SELECT d.id, d.account_id").
		WithArgs(userID, 11, 10).
		WillReturnRows(pgxmock.NewRows([]string{"d_id", "d_account_id", "d_date", "d_description", "d_amount", "d_type", "d_account",
			"c_id", "c_account_id", "c_date", "c_description", "c_amount", "c_type", "c_account"}))

	req, _ := http.NewRequest("GET", "/links/transfer-suggestions?page=2&limit=10", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"page":2`)
	assert.Contains(t, w.Body.String(), `"limit":10`)
	assert.Contains(t, w.Body.String(), `"hasMore":false`)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetTransferSuggestionsClampsLimit(t *testing.T) {
	r, srv, mock := newLinkTestRouter(t)
	r.GET("/links/transfer-suggestions", srv.GetTransferSuggestions)

	userID := testUserID()

	// limit=1000 is clamped to maxSuggestionLimit; the extra row keeps hasMore.
	mock.ExpectQuery("SELECT d.id, d.account_id").
		WithArgs(userID, maxSuggestionLimit+1, 0).
		WillReturnRows(pgxmock.NewRows([]string{"d_id", "d_account_id", "d_date", "d_description", "d_amount", "d_type", "d_account",
			"c_id", "c_account_id", "c_date", "c_description", "c_amount", "c_type", "c_account"}))

	req, _ := http.NewRequest("GET", "/links/transfer-suggestions?limit=1000", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"limit":100`)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetTransferSuggestionsHasMore(t *testing.T) {
	r, srv, mock := newLinkTestRouter(t)
	r.GET("/links/transfer-suggestions", srv.GetTransferSuggestions)

	userID := testUserID()
	now := time.Now()

	// limit=1 but two rows returned: the extra row signals another page and is
	// dropped from the payload.
	rows := pgxmock.NewRows([]string{"d_id", "d_account_id", "d_date", "d_description", "d_amount", "d_type", "d_account",
		"c_id", "c_account_id", "c_date", "c_description", "c_amount", "c_type", "c_account"}).
		AddRow(uuid.New(), uuid.New(), now, "A", 100.0, "debit", "Checking",
			uuid.New(), uuid.New(), now, "A credit", 100.0, "credit", "Savings").
		AddRow(uuid.New(), uuid.New(), now, "B", 200.0, "debit", "Checking",
			uuid.New(), uuid.New(), now, "B credit", 200.0, "credit", "Savings")

	mock.ExpectQuery("SELECT d.id, d.account_id").
		WithArgs(userID, 2, 0).
		WillReturnRows(rows)

	req, _ := http.NewRequest("GET", "/links/transfer-suggestions?limit=1", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp struct {
		Data    []models.TransferSuggestion `json:"data"`
		HasMore bool                        `json:"hasMore"`
	}
	assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Len(t, resp.Data, 1)
	assert.True(t, resp.HasMore)
	assert.Equal(t, "A", resp.Data[0].DebitTxn.Description)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetCashbackSuggestions(t *testing.T) {
	r, srv, mock := newLinkTestRouter(t)
	r.GET("/links/cashback-suggestions", srv.GetCashbackSuggestions)

	userID := testUserID()
	now := time.Now()

	rows := pgxmock.NewRows([]string{"cb_id", "cb_account_id", "cb_date", "cb_description", "cb_amount", "cb_type", "ca_name",
		"o_id", "o_account_id", "o_date", "o_description", "o_amount", "o_type", "oa_name"}).
		AddRow(uuid.New(), uuid.New(), now, "Cashback reward", 50.0, "credit", "Savings",
			uuid.New(), uuid.New(), now, "Purchase", 500.0, "debit", "Savings")

	mock.ExpectQuery("SELECT cb.id, cb.account_id").
		WithArgs(userID, defaultSuggestionLimit+1, 0).
		WillReturnRows(rows)

	req, _ := http.NewRequest("GET", "/links/cashback-suggestions", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp struct {
		Data    []models.TransferSuggestion `json:"data"`
		Page    int                         `json:"page"`
		Limit   int                         `json:"limit"`
		HasMore bool                        `json:"hasMore"`
	}
	assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Len(t, resp.Data, 1)
	assert.Equal(t, 1, resp.Page)
	assert.Equal(t, defaultSuggestionLimit, resp.Limit)
	assert.False(t, resp.HasMore)
	assert.Equal(t, "Cashback reward", resp.Data[0].CreditTxn.Description)
	assert.Equal(t, 70.0, resp.Data[0].Score)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetCashbackSuggestionsPagination(t *testing.T) {
	r, srv, mock := newLinkTestRouter(t)
	r.GET("/links/cashback-suggestions", srv.GetCashbackSuggestions)

	userID := testUserID()

	mock.ExpectQuery("SELECT cb.id, cb.account_id").
		WithArgs(userID, 6, 5).
		WillReturnRows(pgxmock.NewRows([]string{"cb_id", "cb_account_id", "cb_date", "cb_description", "cb_amount", "cb_type", "ca_name",
			"o_id", "o_account_id", "o_date", "o_description", "o_amount", "o_type", "oa_name"}))

	req, _ := http.NewRequest("GET", "/links/cashback-suggestions?page=2&limit=5", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"page":2`)
	assert.Contains(t, w.Body.String(), `"limit":5`)
	assert.NoError(t, mock.ExpectationsWereMet())
}
