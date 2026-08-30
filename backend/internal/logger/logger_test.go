package logger

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// collectHandler records slog records in memory so tests can assert on them.
type collectHandler struct {
	level   slog.Level
	records []slog.Record
}

func (h *collectHandler) Enabled(_ context.Context, l slog.Level) bool {
	return l >= h.level
}

func (h *collectHandler) Handle(_ context.Context, r slog.Record) error {
	h.records = append(h.records, r)
	return nil
}

func (h *collectHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *collectHandler) WithGroup(string) slog.Handler      { return h }

func recordAttr(t *testing.T, r slog.Record, key string) (string, bool) {
	t.Helper()
	var found string
	ok := false
	r.Attrs(func(a slog.Attr) bool {
		if a.Key == key {
			found = a.Value.String()
			ok = true
			return false
		}
		return true
	})
	return found, ok
}

func TestRequestLoggerDebugCapturesBodiesAndRedacts(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &collectHandler{level: slog.LevelDebug}
	r := gin.New()
	r.Use(RequestLogger(slog.New(h)))
	r.POST("/echo", func(c *gin.Context) {
		body, _ := io.ReadAll(c.Request.Body)
		c.JSON(http.StatusOK, gin.H{"received": string(body)})
	})

	req := httptest.NewRequest(http.MethodPost, "/echo",
		bytes.NewBufferString(`{"email":"a@b.com","password":"supersecret"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	require.Len(t, h.records, 1)
	rec := h.records[0]

	assert.Equal(t, slog.LevelDebug, rec.Level)
	assert.NotEmpty(t, w.Header().Get("X-Request-ID"))
	reqID, ok := recordAttr(t, rec, "request_id")
	require.True(t, ok)
	assert.NotEmpty(t, reqID)
	assert.Equal(t, "POST", mustAttr(t, rec, "method"))
	assert.Equal(t, "/echo", mustAttr(t, rec, "path"))
	assert.Equal(t, "200", mustAttr(t, rec, "status"))

	reqBody, ok := recordAttr(t, rec, "request_body")
	require.True(t, ok)
	assert.NotContains(t, reqBody, "supersecret")
	assert.Contains(t, reqBody, "[REDACTED]")
	assert.Contains(t, reqBody, "a@b.com")

	respBody, ok := recordAttr(t, rec, "response_body")
	require.True(t, ok)
	assert.Contains(t, respBody, "received")
	assert.Contains(t, respBody, "supersecret")
}

func TestRequestLoggerDebugRedactsNestedResponseTokens(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &collectHandler{level: slog.LevelDebug}
	r := gin.New()
	r.Use(RequestLogger(slog.New(h)))
	r.GET("/login", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"token": "abc123", "user": gin.H{"name": "alice"}})
	})

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/login", nil))

	require.Len(t, h.records, 1)
	respBody, ok := recordAttr(t, h.records[0], "response_body")
	require.True(t, ok)
	assert.NotContains(t, respBody, "abc123")
	assert.Contains(t, respBody, "[REDACTED]")
	assert.Contains(t, respBody, "alice")
}

func TestRequestLoggerInfoSkipsBodies(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &collectHandler{level: slog.LevelInfo}
	r := gin.New()
	r.Use(RequestLogger(slog.New(h)))
	r.GET("/ping", func(c *gin.Context) { c.String(http.StatusOK, "pong") })

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/ping", nil))

	require.Len(t, h.records, 1)
	rec := h.records[0]
	assert.Equal(t, slog.LevelInfo, rec.Level)
	assert.Equal(t, "GET", mustAttr(t, rec, "method"))
	_, ok := recordAttr(t, rec, "request_body")
	assert.False(t, ok)
	_, ok = recordAttr(t, rec, "response_body")
	assert.False(t, ok)
}

func TestRequestLoggerSkipsBinaryPayloads(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &collectHandler{level: slog.LevelDebug}
	r := gin.New()
	r.Use(RequestLogger(slog.New(h)))
	r.POST("/upload", func(c *gin.Context) { c.Status(http.StatusNoContent) })

	req := httptest.NewRequest(http.MethodPost, "/upload", bytes.NewBufferString("%PDF-1.4 binary"))
	req.Header.Set("Content-Type", "application/pdf")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Len(t, h.records, 1)
	_, ok := recordAttr(t, h.records[0], "request_body")
	assert.False(t, ok)
}

func TestRequestLoggerDoesNotTruncateLargeResponses(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &collectHandler{level: slog.LevelDebug}
	r := gin.New()
	r.Use(RequestLogger(slog.New(h)))
	r.GET("/large", func(c *gin.Context) {
		payload := strings.Repeat("x", maxBodyLog) // > 8 KB, single Write call
		c.JSON(http.StatusOK, gin.H{"documents": strings.Split(payload, "")})
	})

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/large", nil))

	require.Equal(t, http.StatusOK, w.Code)
	var body struct {
		Documents []string `json:"documents"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Len(t, body.Documents, maxBodyLog)
}

func TestRequestLoggerLogsFullBodyWhenUnlimited(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &collectHandler{level: slog.LevelDebug}
	r := gin.New()
	r.Use(RequestLogger(slog.New(h)))
	payload := strings.Repeat("z", 10_000)
	r.GET("/big", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"data": payload})
	})

	original := maxBodyLog
	SetMaxBodyLog(0)
	t.Cleanup(func() { maxBodyLog = original })

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/big", nil))

	require.Equal(t, http.StatusOK, w.Code)
	require.Len(t, h.records, 1)
	respBody, ok := recordAttr(t, h.records[0], "response_body")
	require.True(t, ok)
	assert.Contains(t, respBody, payload)
	_, truncated := recordAttr(t, h.records[0], "response_body_truncated")
	assert.False(t, truncated)
}

func TestRequestLoggerLogsQueryParams(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &collectHandler{level: slog.LevelInfo}
	r := gin.New()
	r.Use(RequestLogger(slog.New(h)))
	r.GET("/list", func(c *gin.Context) { c.String(http.StatusOK, "ok") })

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/list?page=2&category_id=5", nil))

	require.Len(t, h.records, 1)
	assert.Equal(t, "/list", mustAttr(t, h.records[0], "path"))
	assert.Equal(t, "page=2&category_id=5", mustAttr(t, h.records[0], "query"))
}

func TestRequestLoggerOmitsQueryWhenAbsent(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &collectHandler{level: slog.LevelInfo}
	r := gin.New()
	r.Use(RequestLogger(slog.New(h)))
	r.GET("/ping", func(c *gin.Context) { c.String(http.StatusOK, "pong") })

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/ping", nil))

	require.Len(t, h.records, 1)
	_, ok := recordAttr(t, h.records[0], "query")
	assert.False(t, ok)
}

func TestLoggingRoundTripperLogsURLWithQueryAndRedactsToken(t *testing.T) {
	h := &collectHandler{level: slog.LevelDebug}
	l := slog.New(h)

	base := roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(bytes.NewBufferString(`{"documents":[{"id":1}]}`)),
		}, nil
	})

	rt := LoggingRoundTripper(base, l)
	req := httptest.NewRequest(http.MethodGet,
		"http://paperless.local/api/documents/?page_size=100&ordering=-created", nil)
	req.Header.Set("Authorization", "Token supersecret-token")

	resp, err := rt.RoundTrip(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	got, _ := io.ReadAll(resp.Body)
	assert.JSONEq(t, `{"documents":[{"id":1}]}`, string(got))

	require.Len(t, h.records, 1)
	rec := h.records[0]
	assert.Equal(t, "outbound_request", rec.Message)
	assert.Equal(t, "GET", mustAttr(t, rec, "method"))
	assert.Equal(t, "200", mustAttr(t, rec, "status"))
	urlAttr := mustAttr(t, rec, "url")
	assert.Contains(t, urlAttr, "page_size=100")
	assert.Contains(t, urlAttr, "ordering=-created")
	assert.Contains(t, urlAttr, "/api/documents/")

	assert.Equal(t, "authorization", mustAttr(t, rec, "redacted_header"))
	assert.False(t, recordHasAttrValue(t, rec, "supersecret-token"))
	respBody := mustAttr(t, rec, "response_body")
	assert.Contains(t, respBody, "documents")
}

func TestLoggingRoundTripperPreservesRequestBody(t *testing.T) {
	h := &collectHandler{level: slog.LevelDebug}
	l := slog.New(h)

	base := roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		_, _ = io.ReadAll(req.Body)
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(bytes.NewBufferString(`{"id":42}`)),
			Request:    req,
		}, nil
	})

	rt := LoggingRoundTripper(base, l)
	payload := `{"name":"fintrak","color":"#06b6d4"}`
	req := httptest.NewRequest(http.MethodPost, "http://paperless.local/api/tags/",
		bytes.NewBufferString(payload))
	req.Header.Set("Content-Type", "application/json")

	resp, err := rt.RoundTrip(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	got, _ := io.ReadAll(resp.Body)
	assert.JSONEq(t, `{"id":42}`, string(got))

	require.Len(t, h.records, 1)
	assert.Contains(t, mustAttr(t, h.records[0], "request_body"), "fintrak")
}

func TestLoggingRoundTripperSkipsBinaryRequestBody(t *testing.T) {
	h := &collectHandler{level: slog.LevelDebug}
	l := slog.New(h)

	base := roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		_, _ = io.ReadAll(req.Body)
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(bytes.NewBufferString("{}")),
			Request:    req,
		}, nil
	})

	rt := LoggingRoundTripper(base, l)
	req := httptest.NewRequest(http.MethodPost, "http://parser.local/api/extract",
		bytes.NewBufferString("%PDF-1.4 binary payload"))
	req.Header.Set("Content-Type", "multipart/form-data; boundary=xyz")

	resp, err := rt.RoundTrip(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Len(t, h.records, 1)
	_, ok := recordAttr(t, h.records[0], "request_body")
	assert.False(t, ok)
}

func recordHasAttrValue(t *testing.T, rec slog.Record, substr string) bool {
	t.Helper()
	found := false
	rec.Attrs(func(a slog.Attr) bool {
		if strings.Contains(a.Value.String(), substr) {
			found = true
			return false
		}
		return true
	})
	return found
}

func TestRequestLoggerPreservesHandlerRequestBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &collectHandler{level: slog.LevelDebug}
	r := gin.New()
	r.Use(RequestLogger(slog.New(h)))
	r.POST("/echo", func(c *gin.Context) {
		body, _ := io.ReadAll(c.Request.Body)
		c.JSON(http.StatusOK, gin.H{"received": string(body)})
	})

	body := `{"name":"alice","password":"hunter2"}`
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/echo", bytes.NewBufferString(body)))

	require.Equal(t, http.StatusOK, w.Code)
	assert.JSONEq(t, `{"received":"{\"name\":\"alice\",\"password\":\"hunter2\"}"}`, w.Body.String())
}

func TestRedactComposedSensitiveKeys(t *testing.T) {
	// The regex is unanchored, so composed camelCase/underscore keys that the
	// old anchored ^...$ pattern missed are redacted too.
	got := redact([]byte(`{"paperlessToken":"tok-123","nested":{"passwordHash":"hash-abc"},"name":"alice"}`))
	assert.NotContains(t, got, "tok-123")
	assert.NotContains(t, got, "hash-abc")
	assert.Contains(t, got, "[REDACTED]")
	assert.Contains(t, got, "alice")
}

func TestRedactURLEncodedForm(t *testing.T) {
	// x-www-form-urlencoded bodies are textual for logging but not JSON — the
	// key-based redaction must apply to them as well.
	got := redact([]byte("password=hunter2&name=alice"))
	assert.NotContains(t, got, "hunter2")
	assert.Contains(t, got, "alice")
	assert.Contains(t, got, "REDACTED")
}

func TestRedactQueryStringPreservesBenignQueries(t *testing.T) {
	assert.Equal(t, "page=2&category_id=5", redactQueryString("page=2&category_id=5"))
	assert.Equal(t, "", redactQueryString(""))
}

func TestRedactQueryStringRedactsSensitiveParams(t *testing.T) {
	got := redactQueryString("accessToken=xyz&page=2")
	assert.NotContains(t, got, "xyz")
	assert.Contains(t, got, "REDACTED")
}

func TestRequestLoggerRedactsComposedKeyBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &collectHandler{level: slog.LevelDebug}
	r := gin.New()
	r.Use(RequestLogger(slog.New(h)))
	r.POST("/settings", func(c *gin.Context) { c.Status(http.StatusNoContent) })

	req := httptest.NewRequest(http.MethodPost, "/settings",
		bytes.NewBufferString(`{"paperlessToken":"leaky-token-123","paperlessUrl":"http://pl.local"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Len(t, h.records, 1)
	reqBody, ok := recordAttr(t, h.records[0], "request_body")
	require.True(t, ok)
	assert.NotContains(t, reqBody, "leaky-token-123")
	assert.Contains(t, reqBody, "[REDACTED]")
	assert.Contains(t, reqBody, "pl.local")
}

func TestRequestLoggerRedactsSensitiveQueryParam(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &collectHandler{level: slog.LevelInfo}
	r := gin.New()
	r.Use(RequestLogger(slog.New(h)))
	r.GET("/list", func(c *gin.Context) { c.String(http.StatusOK, "ok") })

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/list?token=query-secret&page=2", nil))

	require.Len(t, h.records, 1)
	query, ok := recordAttr(t, h.records[0], "query")
	require.True(t, ok)
	assert.NotContains(t, query, "query-secret")
	assert.Contains(t, query, "REDACTED")
}

func mustAttr(t *testing.T, r slog.Record, key string) string {
	t.Helper()
	val, ok := recordAttr(t, r, key)
	require.True(t, ok, "expected attr %q", key)
	return val
}
