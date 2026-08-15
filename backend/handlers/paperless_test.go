package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/fintrak/backend/db"
	"github.com/gin-gonic/gin"
	"github.com/pashagolub/pgxmock/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newPaperlessTestRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.Default()
	r.Use(testAuthMiddleware())
	r.GET("/paperless/settings", GetPaperlessSettings)
	r.PUT("/paperless/settings", UpdatePaperlessSettings)
	r.GET("/paperless/documents", ListPaperlessDocuments)
	r.GET("/paperless/documents/:id/file", GetPaperlessDocumentFile)
	r.POST("/paperless/import", ImportPaperlessDocument)
	return r
}

// setupPaperlessMock swaps the DB pool for a pgxmock and preloads the user's
// Paperless settings row so paperlessConfig resolves.
func setupPaperlessMock(t *testing.T, url, token string) pgxmock.PgxPoolIface {
	t.Helper()
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	t.Cleanup(mock.Close)
	oldPool := db.Pool
	db.Pool = mock
	t.Cleanup(func() { db.Pool = oldPool })
	return mock
}

func expectPaperlessConfigQuery(mock pgxmock.PgxPoolIface, url, token string) {
	mock.ExpectQuery("SELECT paperless_url, paperless_token FROM users").
		WithArgs(testUserID()).
		WillReturnRows(pgxmock.NewRows([]string{"paperless_url", "paperless_token"}).AddRow(url, token))
}

func TestGetPaperlessSettings(t *testing.T) {
	mock := setupPaperlessMock(t, "http://paperless.local", "tok123")
	expectPaperlessConfigQuery(mock, "http://paperless.local", "tok123")

	r := newPaperlessTestRouter()
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/paperless/settings", nil))

	assert.Equal(t, http.StatusOK, w.Code)
	var res map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &res))
	assert.Equal(t, "http://paperless.local", res["paperlessUrl"])
	assert.Equal(t, "tok123", res["paperlessToken"])
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdatePaperlessSettings(t *testing.T) {
	mock := setupPaperlessMock(t, "", "")
	mock.ExpectExec("UPDATE users SET paperless_url = \\$1, paperless_token = \\$2").
		WithArgs("http://paperless.local", "tok456", testUserID()).
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))

	r := newPaperlessTestRouter()
	body := bytes.NewBufferString(`{"paperlessUrl":"  http://paperless.local  ","paperlessToken":"tok456"}`)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPut, "/paperless/settings", body))

	assert.Equal(t, http.StatusOK, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestListPaperlessDocumentsUnconfigured(t *testing.T) {
	mock := setupPaperlessMock(t, "", "")
	expectPaperlessConfigQuery(mock, "", "")

	r := newPaperlessTestRouter()
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/paperless/documents", nil))

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestListPaperlessDocumentsSuccess(t *testing.T) {
	mock := setupPaperlessMock(t, "", "")

	// Fake Paperless-ngx returning a document list (IDs for FK-like fields) plus
	// the lookup tables used to humanize names.
	paperless := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Token tok", r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, "/api/correspondents"):
			w.Write([]byte(`{"results":[{"id":7,"name":"SBI"}]}`))
		case strings.Contains(r.URL.Path, "/api/document_types"):
			w.Write([]byte(`{"results":[{"id":3,"name":"Statement"}]}`))
		case strings.Contains(r.URL.Path, "/api/tags"):
			w.Write([]byte(`{"results":[{"id":9,"name":"credit-card"}]}`))
		default:
			w.Write([]byte(`{"results":[
				{"id":42,"title":"SBI Statement March","correspondent":7,"document_type":3,"added":"2026-03-01T10:00:00Z","tags":[9]}
			]}`))
		}
	}))
	defer paperless.Close()
	// point paperlessConfig at the fake via the settings row value
	expectPaperlessConfigQuery(mock, paperless.URL, "tok")

	r := newPaperlessTestRouter()
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/paperless/documents", nil))

	assert.Equal(t, http.StatusOK, w.Code)
	var res struct {
		Documents []struct {
			ID            int      `json:"id"`
			Title         string   `json:"title"`
			Correspondent string   `json:"correspondent"`
			DocumentType  string   `json:"documentType"`
			Tags          []string `json:"tags"`
		} `json:"documents"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &res))
	require.Len(t, res.Documents, 1)
	assert.Equal(t, 42, res.Documents[0].ID)
	assert.Equal(t, "SBI", res.Documents[0].Correspondent)
	assert.Equal(t, "credit-card", res.Documents[0].Tags[0])
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestImportPaperlessDocumentSuccess(t *testing.T) {
	mock := setupPaperlessMock(t, "", "")

	// Fake Paperless serving the original file download.
	paperless := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Token tok", r.Header.Get("Authorization"))
		assert.Equal(t, "/api/documents/42/download/", r.URL.Path)
		w.Write([]byte("%PDF-1.4 fake"))
	}))
	defer paperless.Close()

	// Fake statement parser.
	parser := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, _, err := r.FormFile("file"); err != nil {
			http.Error(w, `{"error":"no file"}`, http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"transactions":[{"date":"18 May 26","description":"UPI-SUYASH MITTAL","amount":310.0,"type":"Credit"}],"summary":{},"page_count":1,"transaction_count":1}`))
	}))
	defer parser.Close()

	old := statementParserURL
	statementParserURL = parser.URL
	defer func() { statementParserURL = old }()

	// need fresh config query pointing at paperless.URL (not the fake parser)
	expectPaperlessConfigQuery(mock, paperless.URL, "tok")

	r := newPaperlessTestRouter()
	body := bytes.NewBufferString(`{"documentId":42,"extractor":"sbi_cc"}`)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/paperless/import", body))

	assert.Equal(t, http.StatusOK, w.Code)
	var res parseStatementResult
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &res))
	require.Len(t, res.Transactions, 1)
	assert.Equal(t, "2026-05-18", res.Transactions[0].Date)
	assert.Equal(t, "credit", res.Transactions[0].Type)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestImportPaperlessDocumentUnconfigured(t *testing.T) {
	mock := setupPaperlessMock(t, "", "")
	expectPaperlessConfigQuery(mock, "", "")

	r := newPaperlessTestRouter()
	body := bytes.NewBufferString(`{"documentId":1,"extractor":"sbi_cc"}`)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/paperless/import", body))

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetPaperlessDocumentFileSuccess(t *testing.T) {
	mock := setupPaperlessMock(t, "", "")

	paperless := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Token tok", r.Header.Get("Authorization"))
		assert.Equal(t, "/api/documents/42/download/", r.URL.Path)
		w.Header().Set("Content-Type", "application/pdf")
		w.Write([]byte("%PDF-1.4 fake content"))
	}))
	defer paperless.Close()
	expectPaperlessConfigQuery(mock, paperless.URL, "tok")

	r := newPaperlessTestRouter()
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/paperless/documents/42/file", nil))

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "application/pdf", w.Header().Get("Content-Type"))
	assert.Equal(t, "%PDF-1.4 fake content", w.Body.String())
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetPaperlessDocumentFileInvalidID(t *testing.T) {
	mock := setupPaperlessMock(t, "", "")
	expectPaperlessConfigQuery(mock, "http://paperless.local", "tok")

	r := newPaperlessTestRouter()
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/paperless/documents/abc/file", nil))

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestListPaperlessDocumentsPagination(t *testing.T) {
	mock := setupPaperlessMock(t, "", "")

	// Paperless returns two pages; the first points to a `next` link.
	// The server URL is captured via a holder so the handler can reference it.
	var baseURL string
	paperless := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Token tok", r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, "/api/correspondents"):
			w.Write([]byte(`{"results":[]}`))
		case strings.Contains(r.URL.Path, "/api/document_types"):
			w.Write([]byte(`{"results":[]}`))
		case strings.Contains(r.URL.Path, "/api/tags"):
			w.Write([]byte(`{"results":[]}`))
		case r.URL.RawQuery == "page=2":
			w.Write([]byte(`{"results":[{"id":2,"title":"Doc Two"}],"next":null}`))
		default:
			w.Write([]byte(`{"results":[{"id":1,"title":"Doc One"}],"next":"` + baseURL + `/api/documents/?page=2"}`))
		}
	}))
	baseURL = paperless.URL
	defer paperless.Close()
	expectPaperlessConfigQuery(mock, paperless.URL, "tok")

	r := newPaperlessTestRouter()
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/paperless/documents", nil))

	assert.Equal(t, http.StatusOK, w.Code)
	var res struct {
		Documents []struct {
			ID    int    `json:"id"`
			Title string `json:"title"`
		} `json:"documents"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &res))
	require.Len(t, res.Documents, 2)
	assert.Equal(t, 1, res.Documents[0].ID)
	assert.Equal(t, 2, res.Documents[1].ID)
	assert.NoError(t, mock.ExpectationsWereMet())
}
