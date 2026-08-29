package logger

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"regexp"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// maxBodyLog caps how many bytes of a request/response body are written to the
// log. Sensitive fields are redacted before anything is emitted.
const maxBodyLog = 8192

// RequestIDKey is the gin context key under which the per-request ID is stored.
const RequestIDKey = "requestID"

// sensitiveKeyRe matches JSON keys whose values must never appear in logs
// (passwords, auth tokens, secrets, payment card numbers, ...).
var sensitiveKeyRe = regexp.MustCompile(`(?i)^(password|passwd|token|jwt|secret|authorization|apikey|api_key|access_token|refresh_token|cvv|cvv2|pin|otp)$`)

// responseWriter wraps gin.ResponseWriter to capture the response body so it
// can be logged at debug level. Capturing stops after maxBodyLog bytes.
type responseWriter struct {
	gin.ResponseWriter
	buf bytes.Buffer
}

// Write captures the written bytes (up to maxBodyLog) while still streaming the
// real response to the client.
func (w *responseWriter) Write(b []byte) (int, error) {
	if remaining := maxBodyLog - w.buf.Len(); remaining > 0 {
		if len(b) > remaining {
			b = b[:remaining]
		}
		w.buf.Write(b)
	}
	return w.ResponseWriter.Write(b)
}

// RequestLogger returns a gin middleware that logs every HTTP request with its
// method, path, status, latency, and client metadata. When the logger runs at
// debug level (development), it additionally captures and logs the request and
// response bodies, redacting sensitive fields and skipping binary payloads
// such as multipart uploads, PDFs, and images.
func RequestLogger(l *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		debug := l.Enabled(c.Request.Context(), slog.LevelDebug)

		reqID := c.GetHeader("X-Request-ID")
		if reqID == "" {
			reqID = uuid.NewString()
		}
		c.Set(RequestIDKey, reqID)
		c.Writer.Header().Set("X-Request-ID", reqID)

		var reqBody string
		if debug {
			// Capture the request body before the handler consumes it and
			// restore it unchanged so handlers are unaffected.
			if isTextual(c.Request.Header.Get("Content-Type")) {
				if body, err := io.ReadAll(c.Request.Body); err == nil {
					c.Request.Body = io.NopCloser(bytes.NewReader(body))
					reqBody, _ = truncate(redact(body))
				}
			}
			c.Writer = &responseWriter{ResponseWriter: c.Writer}
		}

		c.Next()

		attrs := []slog.Attr{
			slog.String("request_id", reqID),
			slog.String("method", c.Request.Method),
			slog.String("path", c.Request.URL.Path),
			slog.Int("status", c.Writer.Status()),
			slog.Duration("latency", time.Since(start)),
			slog.String("client_ip", c.ClientIP()),
			slog.String("user_agent", c.Request.UserAgent()),
		}

		level := slog.LevelInfo
		if debug {
			level = slog.LevelDebug
			if reqBody != "" {
				attrs = append(attrs, slog.String("request_body", reqBody))
			}
			if rw, ok := c.Writer.(*responseWriter); ok &&
				rw.buf.Len() > 0 &&
				isTextual(c.Writer.Header().Get("Content-Type")) {
				resp, truncated := truncate(redact(rw.buf.Bytes()))
				attrs = append(attrs, slog.String("response_body", resp))
				if truncated {
					attrs = append(attrs, slog.Bool("response_body_truncated", true))
				}
			}
		}

		l.LogAttrs(c.Request.Context(), level, "http_request", attrs...)
	}
}

// isTextual reports whether a Content-Type header value is safe to log as text.
// Binary payloads (uploads, PDFs, images, archives) are excluded.
func isTextual(contentType string) bool {
	ct := strings.ToLower(strings.TrimSpace(strings.SplitN(contentType, ";", 2)[0]))
	if ct == "" {
		return false
	}
	return strings.HasPrefix(ct, "text/") ||
		strings.Contains(ct, "json") ||
		strings.Contains(ct, "xml") ||
		strings.Contains(ct, "x-www-form-urlencoded")
}

// truncate limits a logged body to maxBodyLog bytes and reports whether it was
// cut off.
func truncate(s string) (string, bool) {
	if len(s) > maxBodyLog {
		return s[:maxBodyLog], true
	}
	return s, false
}

// redact replaces the value of every JSON key matched by sensitiveKeyRe with
// "[REDACTED]". Non-JSON input is returned unchanged.
func redact(body []byte) string {
	var v any
	if err := json.Unmarshal(body, &v); err != nil {
		return string(body)
	}
	redactValue(v)
	out, err := json.Marshal(v)
	if err != nil {
		return string(body)
	}
	return string(out)
}

func redactValue(v any) {
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			if sensitiveKeyRe.MatchString(k) {
				t[k] = "[REDACTED]"
			} else {
				redactValue(val)
			}
		}
	case []any:
		for _, item := range t {
			redactValue(item)
		}
	}
}