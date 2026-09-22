package handlers

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/fintrak/backend/internal/crypto"
	"github.com/fintrak/backend/models"
	"github.com/gin-gonic/gin"
	"github.com/pashagolub/pgxmock/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newPaperlessTestRouter(srv *Server) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.Default()
	r.Use(testAuthMiddleware())
	r.GET("/paperless/settings", srv.GetPaperlessSettings)
	r.PUT("/paperless/settings", srv.UpdatePaperlessSettings)
	r.GET("/paperless/documents", srv.ListPaperlessDocuments)
	r.GET("/paperless/documents/:id/file", srv.GetPaperlessDocumentFile)
	r.POST("/paperless/import", srv.ImportPaperlessDocument)
	return r
}

// setupPaperlessMock creates a mock pool, a Server over it, and preloads the
// user's Paperless settings row so paperlessConfig resolves.
func setupPaperlessMock(t *testing.T, url, token string) (*Server, pgxmock.PgxPoolIface) {
	t.Helper()
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	t.Cleanup(mock.Close)
	return newTestServer(mock), mock
}

func expectPaperlessConfigQuery(mock pgxmock.PgxPoolIface, url, token, tag string) {
	mock.ExpectQuery("SELECT paperless_url, paperless_token, paperless_tag, page_size FROM users").
		WithArgs(testUserID()).
		WillReturnRows(pgxmock.NewRows([]string{"paperless_url", "paperless_token", "paperless_tag", "page_size"}).AddRow(url, token, tag, nil))
}

func TestFetchNameMapsPaginates(t *testing.T) {
	var pages []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page := r.URL.Query().Get("page")
		pages = append(pages, page)
		results := make([]map[string]any, 0, nameMapPageSize)
		if page == "1" {
			// A full first page: forces the pagination loop to continue.
			for i := 0; i < nameMapPageSize; i++ {
				results = append(results, map[string]any{"id": i, "name": fmt.Sprintf("c%d", i)})
			}
		} else {
			// A short second page ends the loop.
			for i := 0; i < 2; i++ {
				results = append(results, map[string]any{"id": nameMapPageSize + i, "name": fmt.Sprintf("c%d", nameMapPageSize+i)})
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"results": results})
	}))
	defer server.Close()

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)

	maps := fetchNameMaps(c.Request.Context(), &http.Client{}, server.URL, "tok")

	// 1000 entries from page 1 + 2 from page 2 — beyond the old single-page
	// single-fetch limit of 1000.
	assert.Len(t, maps.correspondents, nameMapPageSize+2)
	assert.Equal(t, "c0", maps.correspondents[0])
	assert.Equal(t, fmt.Sprintf("c%d", nameMapPageSize+1), maps.correspondents[nameMapPageSize+1])
	assert.Contains(t, pages, "2")
}

func TestGetPaperlessSettings(t *testing.T) {
	srv, mock := setupPaperlessMock(t, "http://paperless.local", "tok123")
	expectPaperlessConfigQuery(mock, "http://paperless.local", "tok123", "")

	r := newPaperlessTestRouter(srv)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/paperless/settings", nil))

	assert.Equal(t, http.StatusOK, w.Code)
	var res map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &res))
	assert.Equal(t, "http://paperless.local", res["paperlessUrl"])
	assert.Equal(t, true, res["hasToken"])
	// The token must never be returned.
	assert.NotContains(t, res, "paperlessToken")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetPaperlessSettingsNoToken(t *testing.T) {
	srv, mock := setupPaperlessMock(t, "http://paperless.local", "")
	expectPaperlessConfigQuery(mock, "http://paperless.local", "", "")

	r := newPaperlessTestRouter(srv)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/paperless/settings", nil))

	assert.Equal(t, http.StatusOK, w.Code)
	var res map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &res))
	assert.Equal(t, false, res["hasToken"])
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdatePaperlessSettings(t *testing.T) {
	srv, mock := setupPaperlessMock(t, "", "")
	mock.ExpectExec("UPDATE users SET paperless_url = \\$1, paperless_token = \\$2 WHERE id = \\$3").
		WithArgs("http://paperless.local", pgxmock.AnyArg(), testUserID()).
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))

	r := newPaperlessTestRouter(srv)
	body := bytes.NewBufferString(`{"paperlessUrl":"  http://paperless.local  ","paperlessToken":"tok456"}`)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPut, "/paperless/settings", body))

	assert.Equal(t, http.StatusOK, w.Code)
	var res map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &res))
	assert.Equal(t, true, res["hasToken"])
	assert.NotContains(t, res, "paperlessToken")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdatePaperlessSettingsInvalidURL(t *testing.T) {
	srv, mock := setupPaperlessMock(t, "", "")

	r := newPaperlessTestRouter(srv)
	for _, bad := range []string{"not-a-url", "ftp://paperless", "http://", "http://user:pass@example.com"} {
		body := bytes.NewBufferString(`{"paperlessUrl":"` + bad + `"}`)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodPut, "/paperless/settings", body))
		assert.Equal(t, http.StatusBadRequest, w.Code, "url %q should be rejected", bad)
	}
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdatePaperlessSettingsWithPageSize(t *testing.T) {
	srv, mock := setupPaperlessMock(t, "", "")
	pageSize := 100
	mock.ExpectExec("UPDATE users SET page_size = \\$1 WHERE id = \\$2").
		WithArgs(&pageSize, testUserID()).
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))

	r := newPaperlessTestRouter(srv)
	body := bytes.NewBufferString(`{"pageSize":100}`)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPut, "/paperless/settings", body))

	assert.Equal(t, http.StatusOK, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdatePaperlessSettingsClearsPageSize(t *testing.T) {
	srv, mock := setupPaperlessMock(t, "", "")
	mock.ExpectExec("UPDATE users SET page_size = \\$1 WHERE id = \\$2").
		WithArgs((*int)(nil), testUserID()).
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))

	r := newPaperlessTestRouter(srv)
	body := bytes.NewBufferString(`{"pageSize":null}`)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPut, "/paperless/settings", body))

	assert.Equal(t, http.StatusOK, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdatePaperlessSettingsNoFields(t *testing.T) {
	srv, mock := setupPaperlessMock(t, "", "")
	r := newPaperlessTestRouter(srv)
	body := bytes.NewBufferString(`{}`)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPut, "/paperless/settings", body))

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestListPaperlessDocumentsUnconfigured(t *testing.T) {
	srv, mock := setupPaperlessMock(t, "", "")
	expectPaperlessConfigQuery(mock, "", "", "")

	r := newPaperlessTestRouter(srv)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/paperless/documents", nil))

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestListPaperlessDocumentsSuccess(t *testing.T) {
	srv, mock := setupPaperlessMock(t, "", "")

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
			w.Write([]byte(`{"count":1,"results":[
				{"id":42,"title":"SBI Statement March","correspondent":7,"document_type":3,"added":"2026-03-01T10:00:00Z","created":"2026-03-01T10:00:00Z","tags":[9]}
			]}`))
		}
	}))
	defer paperless.Close()
	// point paperlessConfig at the fake via the settings row value
	expectPaperlessConfigQuery(mock, paperless.URL, "tok", "")

	r := newPaperlessTestRouter(srv)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/paperless/documents", nil))

	assert.Equal(t, http.StatusOK, w.Code)
	var res struct {
		Documents []struct {
			ID            int      `json:"id"`
			Title         string   `json:"title"`
			Correspondent string   `json:"correspondent"`
			DocumentType  string   `json:"documentType"`
			Created       string   `json:"created"`
			Tags          []string `json:"tags"`
		} `json:"documents"`
		Page           int      `json:"page"`
		PageSize       int      `json:"pageSize"`
		TotalCount     int      `json:"totalCount"`
		TotalPages     int      `json:"totalPages"`
		Correspondents []string `json:"correspondents"`
		DocumentTypes  []string `json:"documentTypes"`
		Tags           []string `json:"tags"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &res))
	require.Len(t, res.Documents, 1)
	assert.Equal(t, 42, res.Documents[0].ID)
	assert.Equal(t, "SBI", res.Documents[0].Correspondent)
	assert.Equal(t, "credit-card", res.Documents[0].Tags[0])
	assert.Equal(t, "2026-03-01T10:00:00Z", res.Documents[0].Created)
	assert.Equal(t, 1, res.Page)
	assert.Equal(t, 25, res.PageSize)
	assert.Equal(t, 1, res.TotalCount)
	assert.Equal(t, 1, res.TotalPages)
	// The lookup tables are returned so the UI can render filter dropdowns.
	assert.Equal(t, []string{"SBI"}, res.Correspondents)
	assert.Equal(t, []string{"Statement"}, res.DocumentTypes)
	assert.Equal(t, []string{"credit-card"}, res.Tags)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestImportPaperlessDocumentSuccess(t *testing.T) {
	srv, mock := setupPaperlessMock(t, "", "")

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
		w.Write([]byte(`{"transactions":[{"date":"18 May 26","description":"UPI-SUYASH MITTAL","amount":310.0,"type":"Credit"}],"summary":{},"page_count":1,"transaction_count":1,"validation_errors":["page 1: rebuilt subtotal does not match printed total"]}`))
	}))
	defer parser.Close()

	srv.parserURL = parser.URL

	// need fresh config query pointing at paperless.URL (not the fake parser)
	expectPaperlessConfigQuery(mock, paperless.URL, "tok", "")

	r := newPaperlessTestRouter(srv)
	body := bytes.NewBufferString(`{"documentId":42,"extractor":"sbi_cc"}`)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/paperless/import", body))

	assert.Equal(t, http.StatusOK, w.Code)
	var res parseStatementResult
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &res))
	require.Len(t, res.Transactions, 1)
	assert.Equal(t, "2026-05-18", res.Transactions[0].Date)
	assert.Equal(t, "credit", res.Transactions[0].Type)
	// The Paperless path shares the manual path's result shape, so parser
	// warnings must survive it too.
	assert.Equal(t, []string{"page 1: rebuilt subtotal does not match printed total"}, res.ValidationErrors)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestImportPaperlessDocumentDoesNotTagOnParse(t *testing.T) {
	srv, mock := setupPaperlessMock(t, "", "")

	// Fake Paperless serving the original file. Any tag-related call is made to
	// fail, proving the import endpoint no longer tags during parsing.
	paperless := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Token tok", r.Header.Get("Authorization"))
		if strings.Contains(r.URL.Path, "/api/tags") || strings.HasSuffix(r.URL.Path, "/api/documents/42/") {
			http.Error(w, "tagging should not happen during parse", http.StatusInternalServerError)
			return
		}
		w.Write([]byte("%PDF-1.4 fake"))
	}))
	defer paperless.Close()

	parser := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"transactions":[{"date":"18 May 26","description":"UPI-SUYASH MITTAL","amount":310.0,"type":"Credit"}],"summary":{},"page_count":1,"transaction_count":1}`))
	}))
	defer parser.Close()

	srv.parserURL = parser.URL

	expectPaperlessConfigQuery(mock, paperless.URL, "tok", "fintrak")

	r := newPaperlessTestRouter(srv)
	body := bytes.NewBufferString(`{"documentId":42,"extractor":"sbi_cc"}`)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/paperless/import", body))

	assert.Equal(t, http.StatusOK, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestTagPaperlessDocuments(t *testing.T) {
	srv, mock := setupPaperlessMock(t, "", "")

	// Fake Paperless serving tag lookups, tag creation, and the document
	// fetch/patch used to append the tag.
	paperless := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Token tok", r.Header.Get("Authorization"))
		switch {
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/api/tags"):
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"results":[]}`))
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/api/tags/"):
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"id":55,"name":"fintrak"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/documents/42/":
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"id":42,"tags":[9]}`))
		case r.Method == http.MethodPatch && r.URL.Path == "/api/documents/42/":
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"id":42,"tags":[9,55]}`))
		default:
			http.Error(w, "unexpected", http.StatusNotFound)
		}
	}))
	defer paperless.Close()

	expectPaperlessConfigQuery(mock, paperless.URL, "tok", "fintrak")

	srv.tagPaperlessDocuments(context.Background(), testUserID(), []int{42}, "test-key", "development")

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestTagPaperlessDocumentsNoIDs(t *testing.T) {
	srv, mock := setupPaperlessMock(t, "", "")

	// No document IDs means no settings lookup and no Paperless calls.
	srv.tagPaperlessDocuments(context.Background(), testUserID(), nil, "test-key", "development")

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestTagPaperlessDocumentsUnconfigured(t *testing.T) {
	srv, mock := setupPaperlessMock(t, "", "")
	expectPaperlessConfigQuery(mock, "", "", "")

	srv.tagPaperlessDocuments(context.Background(), testUserID(), []int{42}, "test-key", "development")

	assert.NoError(t, mock.ExpectationsWereMet())
}
func TestImportPaperlessDocumentUnconfigured(t *testing.T) {
	srv, mock := setupPaperlessMock(t, "", "")
	expectPaperlessConfigQuery(mock, "", "", "")

	r := newPaperlessTestRouter(srv)
	body := bytes.NewBufferString(`{"documentId":1,"extractor":"sbi_cc"}`)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/paperless/import", body))

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetPaperlessDocumentFileSuccess(t *testing.T) {
	srv, mock := setupPaperlessMock(t, "", "")

	// A compromised Paperless instance could serve HTML with attacker script;
	// the proxy must pin the content type and harden the response.
	paperless := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Token tok", r.Header.Get("Authorization"))
		assert.Equal(t, "/api/documents/42/download/", r.URL.Path)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte("%PDF-1.4 fake content"))
	}))
	defer paperless.Close()
	expectPaperlessConfigQuery(mock, paperless.URL, "tok", "")

	r := newPaperlessTestRouter(srv)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/paperless/documents/42/file", nil))

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "application/pdf", w.Header().Get("Content-Type"))
	assert.Equal(t, "nosniff", w.Header().Get("X-Content-Type-Options"))
	assert.Equal(t, `inline; filename="42.pdf"`, w.Header().Get("Content-Disposition"))
	assert.Equal(t, "%PDF-1.4 fake content", w.Body.String())
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetPaperlessDocumentFileInvalidID(t *testing.T) {
	srv, mock := setupPaperlessMock(t, "", "")
	expectPaperlessConfigQuery(mock, "http://paperless.local", "tok", "")

	r := newPaperlessTestRouter(srv)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/paperless/documents/abc/file", nil))

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestListPaperlessDocumentsPagination(t *testing.T) {
	srv, mock := setupPaperlessMock(t, "", "")

	// Paperless must receive the requested page/page_size directly and the
	// handler must return its count as pagination metadata.
	paperless := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Token tok", r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, "/api/correspondents"),
			strings.Contains(r.URL.Path, "/api/document_types"),
			strings.Contains(r.URL.Path, "/api/tags"):
			w.Write([]byte(`{"results":[]}`))
		default:
			assert.Equal(t, "2", r.URL.Query().Get("page"))
			assert.Equal(t, "25", r.URL.Query().Get("page_size"))
			assert.Equal(t, "id,title,correspondent,document_type,created,tags", r.URL.Query().Get("fields"))
			w.Write([]byte(`{"count":57,"results":[{"id":2,"title":"Doc Two"}]}`))
		}
	}))
	defer paperless.Close()
	expectPaperlessConfigQuery(mock, paperless.URL, "tok", "")

	r := newPaperlessTestRouter(srv)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/paperless/documents?page=2&pageSize=25", nil))

	assert.Equal(t, http.StatusOK, w.Code)
	var res struct {
		Documents []struct {
			ID    int    `json:"id"`
			Title string `json:"title"`
		} `json:"documents"`
		Page       int `json:"page"`
		PageSize   int `json:"pageSize"`
		TotalCount int `json:"totalCount"`
		TotalPages int `json:"totalPages"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &res))
	require.Len(t, res.Documents, 1)
	assert.Equal(t, 2, res.Documents[0].ID)
	assert.Equal(t, 2, res.Page)
	assert.Equal(t, 25, res.PageSize)
	assert.Equal(t, 57, res.TotalCount)
	assert.Equal(t, 3, res.TotalPages)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestListPaperlessDocumentsFilters(t *testing.T) {
	srv, mock := setupPaperlessMock(t, "", "")

	// The name-based UI filters must be translated into Paperless ID filters and
	// forwarded, so filtering happens server-side rather than in the backend.
	paperless := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Token tok", r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, "/api/correspondents"):
			w.Write([]byte(`{"results":[{"id":7,"name":"SBI"},{"id":8,"name":"HDFC"}]}`))
		case strings.Contains(r.URL.Path, "/api/document_types"):
			w.Write([]byte(`{"results":[{"id":3,"name":"Statement"}]}`))
		case strings.Contains(r.URL.Path, "/api/tags"):
			w.Write([]byte(`{"results":[{"id":9,"name":"credit-card"}]}`))
		default:
			assert.Equal(t, "SBI", r.URL.Query().Get("title_search"))
			// The legacy ORM filter is not forwarded: AND-ed with title_search it
			// narrows multi-word queries to literal contiguous substrings and can
			// veto valid Tantivy hits.
			assert.Empty(t, r.URL.Query().Get("title__icontains"))
			assert.Equal(t, "id,title,correspondent,document_type,created,tags", r.URL.Query().Get("fields"))
			assert.Equal(t, "7,8", r.URL.Query().Get("correspondent__id__in"))
			assert.Equal(t, "8", r.URL.Query().Get("correspondent__id__none"))
			assert.Equal(t, "3", r.URL.Query().Get("document_type__id__in"))
			assert.Equal(t, "9", r.URL.Query().Get("tags__id__any"))
			w.Write([]byte(`{"count":1,"results":[{"id":42,"title":"Doc","correspondent":7,"document_type":3,"tags":[9]}]}`))
		}
	}))
	defer paperless.Close()
	expectPaperlessConfigQuery(mock, paperless.URL, "tok", "")

	r := newPaperlessTestRouter(srv)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet,
		"/paperless/documents?search=SBI&correspondentInc=SBI&correspondentInc=HDFC&correspondentExc=HDFC&documentTypeInc=Statement&tagInc=credit-card", nil))

	assert.Equal(t, http.StatusOK, w.Code)
	var res struct {
		Documents []struct {
			ID int `json:"id"`
		} `json:"documents"`
		TotalCount int `json:"totalCount"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &res))
	require.Len(t, res.Documents, 1)
	assert.Equal(t, 42, res.Documents[0].ID)
	assert.Equal(t, 1, res.TotalCount)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestListPaperlessDocumentsMultiWordTitleSearch(t *testing.T) {
	srv, mock := setupPaperlessMock(t, "", "")

	// Multi-word `title_search` is broken on paperless-ngx 3.0.x ("credit card"
	// returns nothing even when titles contain both words), so multi-word
	// searches are forwarded as an explicit fielded Tantivy query AND-ing each
	// word on the title field.
	paperless := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, "/api/correspondents"),
			strings.Contains(r.URL.Path, "/api/document_types"),
			strings.Contains(r.URL.Path, "/api/tags"):
			w.Write([]byte(`{"results":[]}`))
		default:
			assert.Equal(t, `title:"Credit" AND title:"Card"`, r.URL.Query().Get("query"))
			assert.Empty(t, r.URL.Query().Get("title_search"))
			assert.Empty(t, r.URL.Query().Get("title__icontains"))
			w.Write([]byte(`{"count":1,"results":[{"id":42,"title":"Credit_Card_Statement"}]}`))
		}
	}))
	defer paperless.Close()
	expectPaperlessConfigQuery(mock, paperless.URL, "tok", "")

	r := newPaperlessTestRouter(srv)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/paperless/documents?search=Credit+Card", nil))

	assert.Equal(t, http.StatusOK, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestListPaperlessDocumentsRejectsCrossOriginRedirect(t *testing.T) {
	srv, mock := setupPaperlessMock(t, "", "")

	paperless := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/api/correspondents"),
			strings.Contains(r.URL.Path, "/api/document_types"),
			strings.Contains(r.URL.Path, "/api/tags"):
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"results":[]}`))
		default:
			http.Redirect(w, r, "http://evil.example.com/steal", http.StatusFound)
		}
	}))
	defer paperless.Close()
	expectPaperlessConfigQuery(mock, paperless.URL, "tok", "")

	r := newPaperlessTestRouter(srv)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/paperless/documents", nil))

	assert.Equal(t, http.StatusBadGateway, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetPaperlessDocumentFileRejectsOversized(t *testing.T) {
	srv, mock := setupPaperlessMock(t, "", "")

	paperless := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/pdf")
		w.Write(bytes.Repeat([]byte("x"), maxPaperlessDocument+1))
	}))
	defer paperless.Close()
	expectPaperlessConfigQuery(mock, paperless.URL, "tok", "")

	r := newPaperlessTestRouter(srv)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/paperless/documents/42/file", nil))

	assert.Equal(t, http.StatusRequestEntityTooLarge, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestImportPaperlessDocumentRejectsOversized(t *testing.T) {
	srv, mock := setupPaperlessMock(t, "", "")

	paperless := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(bytes.Repeat([]byte("x"), maxPaperlessDocument+1))
	}))
	defer paperless.Close()
	expectPaperlessConfigQuery(mock, paperless.URL, "tok", "")

	r := newPaperlessTestRouter(srv)
	body := bytes.NewBufferString(`{"documentId":42,"extractor":"sbi_cc"}`)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/paperless/import", body))

	assert.Equal(t, http.StatusRequestEntityTooLarge, w.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestValidatePaperlessURL(t *testing.T) {
	for _, u := range []string{
		"http://paperless.local",
		"https://paperless.example.com/",
		"https://sub.example.com/paperless",
	} {
		assert.NoError(t, validatePaperlessURL(u), "url %q should be valid", u)
	}
	for _, u := range []string{"", "not a url", "ftp://x", "http://", "http://user:pass@example.com"} {
		assert.Error(t, validatePaperlessURL(u), "url %q should be rejected", u)
	}
}

func TestPaperlessOrigin(t *testing.T) {
	o, err := paperlessOrigin(models.UserSettings{PaperlessURL: "https://paperless.example.com/paperless"})
	assert.NoError(t, err)
	assert.Equal(t, "https://paperless.example.com", o)
}

func TestValidatePaperlessHost(t *testing.T) {
	dev := models.UserSettings{PaperlessURL: "http://localhost:8000"}
	assert.NoError(t, validatePaperlessHost(context.Background(), dev, "development"))

	// In production, plain http and private/loopback hosts are rejected.
	err := validatePaperlessHost(context.Background(), models.UserSettings{PaperlessURL: "http://paperless.example.com"}, "production")
	assert.Error(t, err)
	err = validatePaperlessHost(context.Background(), models.UserSettings{PaperlessURL: "https://localhost:8000"}, "production")
	assert.Error(t, err)
}

func TestValidatePaperlessHostRejectsNonStandardPort(t *testing.T) {
	err := validatePaperlessHost(context.Background(), models.UserSettings{PaperlessURL: "https://paperless.example.com:8000"}, "production")
	assert.Error(t, err)
}

func TestPaperlessAllowedPort(t *testing.T) {
	assert.True(t, paperlessAllowedPort("80"))
	assert.True(t, paperlessAllowedPort("443"))
	assert.False(t, paperlessAllowedPort("8000"))
	assert.False(t, paperlessAllowedPort("5432"))
}

func TestIsDisallowedPaperlessIP(t *testing.T) {
	cases := []struct {
		name         string
		ip           string
		allowPrivate bool
		disallowed   bool
	}{
		{"loopback allowed in dev", "127.0.0.1", true, false},
		{"loopback blocked in prod", "127.0.0.1", false, true},
		{"private allowed in dev", "10.0.0.5", true, false},
		{"private blocked in prod", "10.0.0.5", false, true},
		{"ipv6 loopback allowed in dev", "::1", true, false},
		{"ipv4-mapped loopback blocked in prod", "::ffff:127.0.0.1", false, true},
		{"cloud metadata always blocked", "169.254.169.254", true, true},
		{"cgnat always blocked", "100.64.0.1", true, true},
		{"unspecified always blocked", "0.0.0.0", true, true},
		{"public allowed", "8.8.8.8", false, false},
		{"public allowed in dev", "8.8.8.8", true, false},
		{"ula blocked in prod", "fd00::1", false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.disallowed, isDisallowedPaperlessIP(net.ParseIP(tc.ip), tc.allowPrivate))
		})
	}
}

func TestReadAllLimited(t *testing.T) {
	small, err := readAllLimited(strings.NewReader("hello"), 10)
	assert.NoError(t, err)
	assert.Equal(t, "hello", string(small))

	_, err = readAllLimited(strings.NewReader(strings.Repeat("x", 11)), 10)
	assert.Error(t, err)
}

func TestPaperlessToken(t *testing.T) {
	enc, err := crypto.Encrypt("secret", "test-key")
	require.NoError(t, err)
	dec, err := paperlessToken(context.Background(), models.UserSettings{PaperlessToken: enc}, "test-key")
	assert.NoError(t, err)
	assert.Equal(t, "secret", dec)

	// Legacy plaintext tokens pass through unchanged.
	plain, err := paperlessToken(context.Background(), models.UserSettings{PaperlessToken: "legacy"}, "test-key")
	assert.NoError(t, err)
	assert.Equal(t, "legacy", plain)
}

// TestPaperlessConfigUpgradesLegacyToken verifies a v1-format token is
// transparently re-sealed under the current v2 derivation when read.
func TestPaperlessConfigUpgradesLegacyToken(t *testing.T) {
	prevKey := tokenEncryptionKey
	tokenEncryptionKey = "upgrade-key"
	t.Cleanup(func() { tokenEncryptionKey = prevKey })

	srv, mock := setupPaperlessMock(t, "http://paperless.local", "")
	legacy := legacyV1Token(t, "secret-token", tokenEncryptionKey)

	expectPaperlessConfigQuery(mock, "http://paperless.local", legacy, "")
	mock.ExpectExec("UPDATE users SET paperless_token = \\$1 WHERE id = \\$2").
		WithArgs(pgxmock.AnyArg(), testUserID()).
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))

	settings, err := srv.paperlessConfig(context.Background(), testUserID())
	require.NoError(t, err)
	require.False(t, crypto.IsLegacy(settings.PaperlessToken))
	require.True(t, strings.HasPrefix(settings.PaperlessToken, crypto.PrefixV2))
	assert.NoError(t, mock.ExpectationsWereMet())

	plain, err := crypto.Decrypt(settings.PaperlessToken, tokenEncryptionKey)
	require.NoError(t, err)
	assert.Equal(t, "secret-token", plain)
}

// legacyV1Token builds a pre-HKDF ciphertext (bare SHA-256 key, nonce||sealed),
// matching the format old versions wrote.
func legacyV1Token(t *testing.T, plaintext, key string) string {
	t.Helper()
	sum := sha256.Sum256([]byte(key))
	block, err := aes.NewCipher(sum[:])
	require.NoError(t, err)
	gcm, err := cipher.NewGCM(block)
	require.NoError(t, err)
	nonce := make([]byte, gcm.NonceSize())
	_, err = io.ReadFull(rand.Reader, nonce)
	require.NoError(t, err)
	sealed := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return crypto.Prefix + base64.StdEncoding.EncodeToString(sealed)
}
