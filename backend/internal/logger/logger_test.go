package logger

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testBodyLimit is the default body cap used by the logger tests.
const testBodyLimit = 8192

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
	r.Use(RequestLogger(slog.New(h), testBodyLimit))
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
	r.Use(RequestLogger(slog.New(h), testBodyLimit))
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
	r.Use(RequestLogger(slog.New(h), testBodyLimit))
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
	r.Use(RequestLogger(slog.New(h), testBodyLimit))
	r.POST("/upload", func(c *gin.Context) { c.Status(http.StatusNoContent) })

	req := httptest.NewRequest(http.MethodPost, "/upload", bytes.NewBufferString("%PDF-1.4 binary"))
	req.Header.Set("Content-Type", "application/pdf")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Len(t, h.records, 1)
	_, ok := recordAttr(t, h.records[0], "request_body")
	assert.False(t, ok)
}

// isTextual trusts the client's Content-Type, so the bytes decide: a payload
// that is not valid UTF-8 must not reach the log however it is declared.
func TestRequestLoggerSkipsBinaryBodiesDeclaredAsText(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &collectHandler{level: slog.LevelDebug}
	r := gin.New()
	r.Use(RequestLogger(slog.New(h), testBodyLimit))
	var received []byte
	r.POST("/upload", func(c *gin.Context) {
		received, _ = io.ReadAll(c.Request.Body)
		c.Status(http.StatusNoContent)
	})

	// A PDF header followed by bytes that cannot be UTF-8.
	payload := append([]byte("%PDF-1.4"), 0xff, 0xfe, 0x00, 0x80)
	req := httptest.NewRequest(http.MethodPost, "/upload", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "text/plain")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusNoContent, w.Code)
	assert.Equal(t, payload, received, "the handler must still receive the body")
	require.Len(t, h.records, 1)
	_, ok := recordAttr(t, h.records[0], "request_body")
	assert.False(t, ok, "invalid UTF-8 must not be logged as text")
}

// A capture cut off mid-rune is not a binary payload: the truncated tail must
// not disqualify an otherwise textual body.
func TestRequestLoggerLogsTextCutMidRune(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &collectHandler{level: slog.LevelDebug}
	const limit = 4 // the capture takes 5 bytes: one 3-byte rune plus 2 of the next
	r := gin.New()
	r.Use(RequestLogger(slog.New(h), limit))
	r.POST("/echo", func(c *gin.Context) { c.Status(http.StatusNoContent) })

	req := httptest.NewRequest(http.MethodPost, "/echo", bytes.NewBufferString("€€€"))
	req.Header.Set("Content-Type", "text/plain")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Len(t, h.records, 1)
	reqBody, ok := recordAttr(t, h.records[0], "request_body")
	require.True(t, ok, "a body cut mid-rune is still text")
	assert.Contains(t, reqBody, "€")
}

func TestRequestLoggerDoesNotTruncateLargeResponses(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &collectHandler{level: slog.LevelDebug}
	r := gin.New()
	r.Use(RequestLogger(slog.New(h), testBodyLimit))
	r.GET("/large", func(c *gin.Context) {
		payload := strings.Repeat("x", testBodyLimit) // > 8 KB, single Write call
		c.JSON(http.StatusOK, gin.H{"documents": strings.Split(payload, "")})
	})

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/large", nil))

	require.Equal(t, http.StatusOK, w.Code)
	var body struct {
		Documents []string `json:"documents"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Len(t, body.Documents, testBodyLimit)
}

func TestRequestLoggerNonPositiveLimitDisablesBodyCapture(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &collectHandler{level: slog.LevelDebug}
	r := gin.New()
	// A non-positive limit captures nothing at all.
	r.Use(RequestLogger(slog.New(h), 0))
	payload := strings.Repeat("z", 10_000)
	r.GET("/big", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"data": payload})
	})

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/big", nil))

	require.Equal(t, http.StatusOK, w.Code)
	// The full response still reaches the client even though it is not logged.
	assert.Greater(t, w.Body.Len(), 0)
	require.Len(t, h.records, 1)
	_, ok := recordAttr(t, h.records[0], "response_body")
	assert.False(t, ok, "a non-positive limit must not capture response bodies")
}

func TestRequestLoggerCapsCapturedBodies(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &collectHandler{level: slog.LevelDebug}
	const limit = 32
	r := gin.New()
	r.Use(RequestLogger(slog.New(h), limit))
	payload := strings.Repeat("z", 10_000)
	r.GET("/big", func(c *gin.Context) {
		c.Writer.Header().Set("Content-Type", "application/json")
		_, _ = c.Writer.WriteString(`{"data":"` + payload + `"}`)
	})

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/big", nil))

	require.Equal(t, http.StatusOK, w.Code)
	assert.Greater(t, w.Body.Len(), limit, "client must receive the full response")
	require.Len(t, h.records, 1)
	respBody := mustAttr(t, h.records[0], "response_body")
	assert.LessOrEqual(t, len(respBody), limit)
	assert.Equal(t, "true", mustAttr(t, h.records[0], "response_body_truncated"))
}

func TestRequestLoggerCapsCapturedRequestAndPreservesBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &collectHandler{level: slog.LevelDebug}
	const limit = 16
	r := gin.New()
	r.Use(RequestLogger(slog.New(h), limit))
	r.POST("/echo", func(c *gin.Context) {
		body, _ := io.ReadAll(c.Request.Body)
		c.String(http.StatusOK, string(body))
	})

	body := strings.Repeat("a", 100)
	req := httptest.NewRequest(http.MethodPost, "/echo", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "text/plain")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, body, w.Body.String(), "handler must still receive the full request body")
	require.Len(t, h.records, 1)
	reqBody := mustAttr(t, h.records[0], "request_body")
	assert.LessOrEqual(t, len(reqBody), limit)
	assert.Equal(t, "true", mustAttr(t, h.records[0], "request_body_truncated"))
}

func TestRequestLoggerLogsQueryParams(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &collectHandler{level: slog.LevelInfo}
	r := gin.New()
	r.Use(RequestLogger(slog.New(h), testBodyLimit))
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
	r.Use(RequestLogger(slog.New(h), testBodyLimit))
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

	rt := LoggingRoundTripper(base, l, testBodyLimit)
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

	rt := LoggingRoundTripper(base, l, testBodyLimit)
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

	rt := LoggingRoundTripper(base, l, testBodyLimit)
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
	r.Use(RequestLogger(slog.New(h), testBodyLimit))
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
	r.Use(RequestLogger(slog.New(h), testBodyLimit))
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
	r.Use(RequestLogger(slog.New(h), testBodyLimit))
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

func TestParseLevel(t *testing.T) {
	tests := []struct {
		level string
		env   string
		want  slog.Level
	}{
		{"debug", "production", slog.LevelDebug},
		{"info", "development", slog.LevelInfo},
		{"WARN", "", slog.LevelWarn},
		{" error ", "", slog.LevelError},
		{"", "production", slog.LevelInfo},
		{"", "development", slog.LevelDebug},
		{"bogus", "production", slog.LevelInfo},
		{"bogus", "development", slog.LevelDebug},
	}
	for _, tt := range tests {
		t.Run(tt.level+"_"+tt.env, func(t *testing.T) {
			assert.Equal(t, tt.want, parseLevel(tt.level, tt.env))
		})
	}
}

func TestNewConfiguresDefaultLoggerAndBridge(t *testing.T) {
	oldDefault := slog.Default()
	oldOutput := log.Writer()
	t.Cleanup(func() {
		slog.SetDefault(oldDefault)
		log.SetOutput(oldOutput)
	})

	require.NotNil(t, New("production", ""))
	_, ok := log.Writer().(logBridge)
	require.True(t, ok, "New should route the stdlib logger through the slog bridge")

	h := &collectHandler{level: slog.LevelDebug}
	slog.SetDefault(slog.New(h))

	n, err := logBridge{}.Write([]byte("hello\n"))
	require.NoError(t, err)
	assert.Equal(t, len("hello\n"), n)
	require.Len(t, h.records, 1)
	assert.Equal(t, "hello", h.records[0].Message)
}
