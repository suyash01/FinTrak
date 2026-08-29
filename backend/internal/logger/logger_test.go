package logger

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
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

func mustAttr(t *testing.T, r slog.Record, key string) string {
	t.Helper()
	val, ok := recordAttr(t, r, key)
	require.True(t, ok, "expected attr %q", key)
	return val
}