package handlers

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/fintrak/backend/internal/money"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newStatementTestRouter(srv *Server) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.Default()
	r.Use(testAuthMiddleware())
	r.POST("/statements/parse", srv.ParseStatement)
	r.GET("/statements/extractors", srv.ListStatementExtractors)
	return r
}

// startFakeParser spins up a throwaway HTTP server that mimics the
// statement-parser REST API and returns the provided JSON/status.
func startFakeParser(t *testing.T, status int, body string) (*httptest.Server, func()) {
	t.Helper()
	parser := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// sanity: ensure we actually received a file part
		if _, _, err := r.FormFile("file"); err != nil {
			http.Error(w, `{"error":"no file"}`, http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		io.WriteString(w, body)
	}))
	return parser, func() { parser.Close() }
}

// startFakeExtractorParser captures the extractor query param the backend
// forwards so we can assert it was passed through correctly.
func startFakeExtractorParser(t *testing.T, status int, body string, gotExtractor *string) (*httptest.Server, func()) {
	t.Helper()
	parser := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	return parser, func() { parser.Close() }
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

// multipartUploadOfSize builds an upload whose file part is `size` bytes, so a
// test can drive the backend past its request-body cap without a real PDF.
func multipartUploadOfSize(t *testing.T, size int) (*bytes.Buffer, string) {
	t.Helper()
	buf := &bytes.Buffer{}
	w := multipart.NewWriter(buf)
	fw, err := w.CreateFormFile("file", "statement.pdf")
	require.NoError(t, err)
	fw.Write([]byte("%PDF-1.4 fake"))
	fw.Write(make([]byte, size))
	w.Close()
	return buf, w.FormDataContentType()
}

func TestParseStatementForwardsExtractor(t *testing.T) {
	var gotExtractor string
	parser, closeParser := startFakeExtractorParser(t, http.StatusOK, `{"transactions":[],"page_count":1,"transaction_count":0}`, &gotExtractor)
	defer closeParser()

	r := newStatementTestRouter(NewServer(nil, parser.URL, 0))
	body, ct := multipartUploadWithExtractor(t, "", "hdfc_cc")

	req := httptest.NewRequest(http.MethodPost, "/statements/parse", body)
	req.Header.Set("Content-Type", ct)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "hdfc_cc", gotExtractor)
}

func TestParseStatementDefaultsExtractor(t *testing.T) {
	var gotExtractor string
	parser, closeParser := startFakeExtractorParser(t, http.StatusOK, `{"transactions":[],"page_count":1,"transaction_count":0}`, &gotExtractor)
	defer closeParser()

	r := newStatementTestRouter(NewServer(nil, parser.URL, 0))
	body, ct := multipartUpload(t, "")

	req := httptest.NewRequest(http.MethodPost, "/statements/parse", body)
	req.Header.Set("Content-Type", ct)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "sbi_cc", gotExtractor)
}

func TestListStatementExtractors(t *testing.T) {
	var gotExtractor string
	parser, closeParser := startFakeExtractorParser(t, http.StatusOK, `{"extractors":[{"name":"sbi_cc","display_name":"SBI Credit Card"},{"name":"hdfc_cc","display_name":"HDFC Credit Card"}]}`, &gotExtractor)
	defer closeParser()

	r := newStatementTestRouter(NewServer(nil, parser.URL, 0))
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
	r := newStatementTestRouter(NewServer(nil, "http://127.0.0.1:1", 0))
	req := httptest.NewRequest(http.MethodGet, "/statements/extractors", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadGateway, w.Code)
}

func TestParseStatementSuccess(t *testing.T) {
	parser, closeParser := startFakeParser(t, http.StatusOK, `{
		"transactions": [
			{"date":"18 May 26","description":"UPI-SUYASH MITTAL","amount":310.0,"type":"Credit"},
			{"date":"04 May 26","description":"TATA AIG INSURANCE","amount":31939.99,"type":"Debit"},
			{"date":"12 Dec 2024","description":"AMAZON PAY","amount":12.5,"type":"Debit"}
		],
		"summary":{"credit_limit":"2,29,000.00"},
		"page_count":7,
		"transaction_count":3
	}`)
	defer closeParser()

	r := newStatementTestRouter(NewServer(nil, parser.URL, 0))
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
	assert.Equal(t, money.FromFloat(310.0), res.Transactions[0].Amount)
	assert.Equal(t, "2026-05-04", res.Transactions[1].Date)
	assert.Equal(t, "debit", res.Transactions[1].Type)
	assert.Equal(t, "2024-12-12", res.Transactions[2].Date)
	assert.Equal(t, "debit", res.Transactions[2].Type)
	assert.Equal(t, 7, res.PageCount)
	assert.Equal(t, 3, res.TxnCount)
	assert.Equal(t, "2,29,000.00", res.Summary["credit_limit"])
	assert.Empty(t, res.ValidationErrors)

	// The parser reported no warnings, but the field must still serialize as an
	// array so the frontend never has to null-check it.
	var envelope map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &envelope))
	assert.JSONEq(t, `[]`, string(envelope["validationErrors"]))
}

// TestParseStatementRejectsOutOfRangeAmount pins the parser-amount boundary: a
// crafted statement can make the parser emit a finite float far beyond
// money.MaxMinorUnits, and converting it would wrap the int64 into a garbage
// negative amount. It must be rejected, not surfaced in the preview.
func TestParseStatementRejectsOutOfRangeAmount(t *testing.T) {
	parser, closeParser := startFakeParser(t, http.StatusOK, `{
		"transactions": [
			{"date":"18 May 26","description":"=1e300","amount":1e300,"type":"Credit"}
		],
		"page_count":1,
		"transaction_count":1
	}`)
	defer closeParser()

	r := newStatementTestRouter(NewServer(nil, parser.URL, 0))
	body, ct := multipartUpload(t, "")
	req := httptest.NewRequest(http.MethodPost, "/statements/parse", body)
	req.Header.Set("Content-Type", ct)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadGateway, w.Code)
}

// TestParseStatementCarriesValidationErrors verifies the parser's per-page
// consistency warnings reach the client instead of being dropped, so a drifted
// parse is never presented to the user as clean.
func TestParseStatementCarriesValidationErrors(t *testing.T) {
	parser, closeParser := startFakeParser(t, http.StatusOK, `{
		"transactions": [],
		"summary": {},
		"page_count": 2,
		"transaction_count": 0,
		"validation_errors": ["page 2: rebuilt subtotal 1,204.00 does not match printed 1,234.00"]
	}`)
	defer closeParser()

	r := newStatementTestRouter(NewServer(nil, parser.URL, 0))
	body, ct := multipartUpload(t, "")
	req := httptest.NewRequest(http.MethodPost, "/statements/parse", body)
	req.Header.Set("Content-Type", ct)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var res parseStatementResult
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &res))
	assert.Equal(t, []string{"page 2: rebuilt subtotal 1,204.00 does not match printed 1,234.00"}, res.ValidationErrors)
}

// TestParseStatementRejectsOversizedBody verifies the body is capped before gin
// parses the multipart form: an upload past the limit is answered with 413 (not
// the misleading 400 "no file provided") and never reaches the parser.
func TestParseStatementRejectsOversizedBody(t *testing.T) {
	parser, closeParser := startFakeParser(t, http.StatusOK, `{"transactions":[],"page_count":1,"transaction_count":0}`)
	defer closeParser()

	r := newStatementTestRouter(NewServer(nil, parser.URL, 0))
	body, ct := multipartUploadOfSize(t, maxStatementUpload+(1<<20))
	req := httptest.NewRequest(http.MethodPost, "/statements/parse", body)
	req.Header.Set("Content-Type", ct)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusRequestEntityTooLarge, w.Code)
}

func TestParseStatementPasswordRequired(t *testing.T) {
	parser, closeParser := startFakeParser(t, http.StatusUnauthorized, `{"error":"password-protected","password_required":true}`)
	defer closeParser()

	r := newStatementTestRouter(NewServer(nil, parser.URL, 0))
	body, ct := multipartUpload(t, "")
	req := httptest.NewRequest(http.MethodPost, "/statements/parse", body)
	req.Header.Set("Content-Type", ct)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// 422, not 401: a password-protected PDF is not an expired session, and a
	// 401 here made the SPA sign the user out (and the shared client refresh
	// the session and re-upload the whole file) instead of showing the password
	// field the form already has.
	assert.Equal(t, http.StatusUnprocessableEntity, w.Code)
	assert.Contains(t, w.Body.String(), "password required")
}

func TestParseStatementNoFile(t *testing.T) {
	r := newStatementTestRouter(NewServer(nil, testParserURL, 0))
	req := httptest.NewRequest(http.MethodPost, "/statements/parse", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestParseStatementParserUnavailable(t *testing.T) {
	r := newStatementTestRouter(NewServer(nil, "http://127.0.0.1:1", 0))
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

// TestParseStatementReturns429WhenParserBusy verifies the authoritative
// server-side concurrency cap: once maxConcurrentParses forwards are in flight,
// a new request fails fast with 429 instead of piling onto the parser.
func TestParseStatementReturns429WhenParserBusy(t *testing.T) {
	srv := NewServer(nil, "http://parser.invalid", 0)
	for i := 0; i < maxConcurrentParses; i++ {
		srv.parseSem <- struct{}{}
	}

	r := newStatementTestRouter(srv)
	body, ct := multipartUpload(t, "")
	req := httptest.NewRequest(http.MethodPost, "/statements/parse", body)
	req.Header.Set("Content-Type", ct)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusTooManyRequests, w.Code)
	assert.Contains(t, w.Body.String(), "busy")
}

// TestParseStatementReleasesConcurrencySlot guards against a leaked semaphore
// slot: repeated successful parses must all be admitted.
func TestParseStatementReleasesConcurrencySlot(t *testing.T) {
	parser, closeParser := startFakeParser(t, http.StatusOK, `{"transactions":[],"page_count":1,"transaction_count":0}`)
	defer closeParser()

	srv := NewServer(nil, parser.URL, 0)
	r := newStatementTestRouter(srv)

	for i := 0; i < 2; i++ {
		body, ct := multipartUpload(t, "")
		req := httptest.NewRequest(http.MethodPost, "/statements/parse", body)
		req.Header.Set("Content-Type", ct)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
	}
	assert.Len(t, srv.parseSem, 0, "semaphore slots must be released")
}
