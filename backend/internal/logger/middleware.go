package logger

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// maxBodyLog caps how many bytes of a request/response body are written to the
// log. Sensitive fields are redacted before anything is emitted. A value <= 0
// disables truncation so full bodies are captured; SetMaxBodyLog overrides it
// (wired from the LOG_BODY_LIMIT config).
var maxBodyLog = 8192

// RequestIDKey is the gin context key under which the per-request ID is stored.
const RequestIDKey = "requestID"

// sensitiveKeyRe matches JSON/form/query keys whose values must never appear
// in logs (passwords, auth tokens, secrets, payment card numbers, ...). It is
// deliberately unanchored: keys are often composed (paperlessToken, setupToken,
// passwordHash, access_token, X-Api-Key), and over-redacting a harmless value
// in a log line is acceptable — under-redacting a credential is not.
var sensitiveKeyRe = regexp.MustCompile(`(?i)(password|passwd|secret|token|jwt|authorization|api[_-]?key|access[_-]?token|refresh[_-]?token|cvv|cvv2|pin|otp)`)

// responseWriter wraps gin.ResponseWriter to capture the response body so it
// can be logged at debug level. Capturing stops after maxBodyLog bytes.
type responseWriter struct {
	gin.ResponseWriter
	buf bytes.Buffer
}

// Write captures the written bytes (up to maxBodyLog) while still streaming the
// real response to the client. The capture buffer gets a bounded prefix of b;
// the underlying writer always receives the full payload.
func (w *responseWriter) Write(b []byte) (int, error) {
	if maxBodyLog <= 0 {
		w.buf.Write(b)
	} else if remaining := maxBodyLog - w.buf.Len(); remaining > 0 {
		if len(b) > remaining {
			w.buf.Write(b[:remaining])
		} else {
			w.buf.Write(b)
		}
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
		if q := c.Request.URL.RawQuery; q != "" {
			attrs = append(attrs, slog.String("query", redactQueryString(q)))
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
// cut off. A non-positive cap means no truncation.
func truncate(s string) (string, bool) {
	if maxBodyLog > 0 && len(s) > maxBodyLog {
		return s[:maxBodyLog], true
	}
	return s, false
}

// redact replaces the value of every JSON key matched by sensitiveKeyRe with
// "[REDACTED]". URL-encoded bodies (x-www-form-urlencoded is considered
// textual by isTextual) are handled by key as well, so password=... fields are
// redacted even though they are not JSON. Any other input is returned
// unchanged.
func redact(body []byte) string {
	var v any
	if err := json.Unmarshal(body, &v); err == nil {
		redactValue(v)
		out, err := json.Marshal(v)
		if err == nil {
			return string(out)
		}
	}
	if q, err := url.ParseQuery(string(body)); err == nil {
		if redactValues(q) {
			return q.Encode()
		}
	}
	return string(body)
}

// redactValues replaces the value of every key matched by sensitiveKeyRe and
// reports whether anything was redacted.
func redactValues(q url.Values) bool {
	redacted := false
	for k := range q {
		if sensitiveKeyRe.MatchString(k) {
			q[k] = []string{"[REDACTED]"}
			redacted = true
		}
	}
	return redacted
}

// redactQueryString redacts sensitive request query parameters for the logged
// "query" attribute. Benign queries are returned verbatim (byte-for-byte), so
// their ordering and encoding are preserved; only after a redaction is the
// query re-encoded.
func redactQueryString(raw string) string {
	q, err := url.ParseQuery(raw)
	if err != nil {
		return raw
	}
	if !redactValues(q) {
		return raw
	}
	return q.Encode()
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
