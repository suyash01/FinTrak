package handlers

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newStatementTestRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.Default()
	r.Use(testAuthMiddleware())
	r.POST("/statements/parse", ParseStatement)
	r.GET("/statements/extractors", ListStatementExtractors)
	return r
}

// startFakeParser spins up a throwaway HTTP server that mimics the
// statement-parser REST API and returns the provided JSON/status.
func startFakeParser(t *testing.T, status int, body string) (*httptest.Server, func()) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// sanity: ensure we actually received a file part
		if _, _, err := r.FormFile("file"); err != nil {
			http.Error(w, `{"error":"no file"}`, http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		io.WriteString(w, body)
	}))
	return srv, func() { srv.Close() }
}

// startFakeExtractorParser captures the extractor query param the backend
// forwards so we can assert it was passed through correctly.
func startFakeExtractorParser(t *testing.T, status int, body string, gotExtractor *string) (*httptest.Server, func()) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*gotExtractor = r.URL.Query().Get("extractor")
		if r.URL.Path == "/api/extractors" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			io.WriteString(w, body)
			return
		}
		if _, _, err := r.FormFile("file"); err != nil {
			http.Error(w, `{"error":"no file"}`, http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		io.WriteString(w, body)
	}))
	return srv, func() { srv.Close() }
}

func multipartUpload(t *testing.T, password string) (*bytes.Buffer, string) {
	t.Helper()
	return multipartUploadWithExtractor(t, password, "")
}

func multipartUploadWithExtractor(t *testing.T, password, extractor string) (*bytes.Buffer, string) {
	t.Helper()
	buf := &bytes.Buffer{}
	w := multipart.NewWriter(buf)
	fw, err := w.CreateFormFile("file", "statement.pdf")
	require.NoError(t, err)
	fw.Write([]byte("%PDF-1.4 fake"))
	if password != "" {
		w.WriteField("password", password)
	}
	if extractor != "" {
		w.WriteField("extractor", extractor)
	}
	w.Close()
	return buf, w.FormDataContentType()
}

func TestParseStatementForwardsExtractor(t *testing.T) {
	old := statementParserURL
	defer func() { statementParserURL = old }()

	var gotExtractor string
	srv, closeSrv := startFakeExtractorParser(t, http.StatusOK, `{"transactions":[],"page_count":1,"transaction_count":0}`, &gotExtractor)
	defer closeSrv()
	statementParserURL = srv.URL

	r := newStatementTestRouter()
	body, ct := multipartUploadWithExtractor(t, "", "hdfc_cc")

	req := httptest.NewRequest(http.MethodPost, "/statements/parse", body)
	req.Header.Set("Content-Type", ct)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "hdfc_cc", gotExtractor)
}

func TestParseStatementDefaultsExtractor(t *testing.T) {
	old := statementParserURL
	defer func() { statementParserURL = old }()

	var gotExtractor string
	srv, closeSrv := startFakeExtractorParser(t, http.StatusOK, `{"transactions":[],"page_count":1,"transaction_count":0}`, &gotExtractor)
	defer closeSrv()
	statementParserURL = srv.URL

	r := newStatementTestRouter()
	body, ct := multipartUpload(t, "")

	req := httptest.NewRequest(http.MethodPost, "/statements/parse", body)
	req.Header.Set("Content-Type", ct)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "sbi_cc", gotExtractor)
}

func TestListStatementExtractors(t *testing.T) {
	old := statementParserURL
	defer func() { statementParserURL = old }()

	var gotExtractor string
	srv, closeSrv := startFakeExtractorParser(t, http.StatusOK, `{"extractors":[{"name":"sbi_cc","display_name":"SBI Credit Card"},{"name":"hdfc_cc","display_name":"HDFC Credit Card"}]}`, &gotExtractor)
	defer closeSrv()
	statementParserURL = srv.URL

	r := newStatementTestRouter()
	req := httptest.NewRequest(http.MethodGet, "/statements/extractors", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var res struct {
		Extractors []struct {
			Name        string `json:"name"`
			DisplayName string `json:"display_name"`
		} `json:"extractors"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &res))
	assert.Equal(t, "sbi_cc", res.Extractors[0].Name)
	assert.Equal(t, "SBI Credit Card", res.Extractors[0].DisplayName)
}

func TestListStatementExtractorsUnavailable(t *testing.T) {
	old := statementParserURL
	defer func() { statementParserURL = old }()
	statementParserURL = "http://127.0.0.1:1"

	r := newStatementTestRouter()
	req := httptest.NewRequest(http.MethodGet, "/statements/extractors", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadGateway, w.Code)
}

func TestParseStatementSuccess(t *testing.T) {
	old := statementParserURL
	defer func() { statementParserURL = old }()

	srv, closeSrv := startFakeParser(t, http.StatusOK, `{
		"transactions": [
			{"date":"18 May 26","description":"UPI-SUYASH MITTAL","amount":310.0,"type":"Credit"},
			{"date":"04 May 26","description":"TATA AIG INSURANCE","amount":31939.99,"type":"Debit"},
			{"date":"12 Dec 2024","description":"AMAZON PAY","amount":12.5,"type":"Debit"}
		],
		"summary":{"credit_limit":"2,29,000.00"},
		"page_count":7,
		"transaction_count":3
	}`)
	defer closeSrv()
	statementParserURL = srv.URL

	r := newStatementTestRouter()
	body, ct := multipartUpload(t, "")
	req := httptest.NewRequest(http.MethodPost, "/statements/parse", body)
	req.Header.Set("Content-Type", ct)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var res parseStatementResult
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &res))
	require.Len(t, res.Transactions, 3)
	assert.Equal(t, "2026-05-18", res.Transactions[0].Date)
	assert.Equal(t, "credit", res.Transactions[0].Type)
	assert.Equal(t, 310.0, res.Transactions[0].Amount)
	assert.Equal(t, "2026-05-04", res.Transactions[1].Date)
	assert.Equal(t, "debit", res.Transactions[1].Type)
	assert.Equal(t, "2024-12-12", res.Transactions[2].Date)
	assert.Equal(t, "debit", res.Transactions[2].Type)
	assert.Equal(t, 7, res.PageCount)
	assert.Equal(t, 3, res.TxnCount)
	assert.Equal(t, "2,29,000.00", res.Summary["credit_limit"])
}

func TestParseStatementPasswordRequired(t *testing.T) {
	old := statementParserURL
	defer func() { statementParserURL = old }()

	srv, closeSrv := startFakeParser(t, http.StatusUnauthorized, `{"error":"password-protected","password_required":true}`)
	defer closeSrv()
	statementParserURL = srv.URL

	r := newStatementTestRouter()
	body, ct := multipartUpload(t, "")
	req := httptest.NewRequest(http.MethodPost, "/statements/parse", body)
	req.Header.Set("Content-Type", ct)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	var respBody map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &respBody))
	assert.Equal(t, true, respBody["passwordRequired"])
}

func TestParseStatementNoFile(t *testing.T) {
	r := newStatementTestRouter()
	req := httptest.NewRequest(http.MethodPost, "/statements/parse", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestParseStatementParserUnavailable(t *testing.T) {
	old := statementParserURL
	defer func() { statementParserURL = old }()
	statementParserURL = "http://127.0.0.1:1"

	r := newStatementTestRouter()
	body, ct := multipartUpload(t, "")
	req := httptest.NewRequest(http.MethodPost, "/statements/parse", body)
	req.Header.Set("Content-Type", ct)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadGateway, w.Code)
}

func TestNormalizeParserDate(t *testing.T) {
	assert.Equal(t, "2026-05-18", normalizeParserDate("18 May 26", ""))
	assert.Equal(t, "2024-12-12", normalizeParserDate("12 Dec 2024", ""))
	assert.Equal(t, "", normalizeParserDate("", ""))
	assert.Equal(t, "unparseable", normalizeParserDate("unparseable", ""))
}

func TestSetStatementParserURL(t *testing.T) {
	old := statementParserURL
	defer func() { statementParserURL = old }()

	SetStatementParserURL("http://parser:8080")
	assert.Equal(t, "http://parser:8080", statementParserURL)

	// An empty URL must not overwrite the configured value.
	SetStatementParserURL("")
	assert.Equal(t, "http://parser:8080", statementParserURL)
}

func TestNormalizeParserDateWithFormat(t *testing.T) {
	assert.Equal(t, "2026-05-18", normalizeParserDate("18/05/2026", "DD/MM/YYYY"))
	assert.Equal(t, "2026-05-18", normalizeParserDate("05/18/2026", "MM/DD/YYYY"))
	assert.Equal(t, "2026-05-18", normalizeParserDate("2026-05-18", "YYYY-MM-DD"))
	assert.Equal(t, "2026-05-18", normalizeParserDate("18/05/26", "DD/MM/YY"))
	assert.Equal(t, "2024-12-12", normalizeParserDate("12 Dec 2024", "DD Mon YYYY"))
	// falls back to auto-detect when the requested format doesn't match
	assert.Equal(t, "2026-05-18", normalizeParserDate("18 May 26", "DD/MM/YYYY"))
}

func TestNormalizeParserType(t *testing.T) {
	assert.Equal(t, "credit", normalizeParserType("Credit"))
	assert.Equal(t, "debit", normalizeParserType("Debit"))
	assert.Equal(t, "debit", normalizeParserType("weird"))
}
