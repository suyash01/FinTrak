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
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// RequestIDKey is the gin context key under which the per-request ID is stored.
const RequestIDKey = "requestID"

// sensitiveKeyRe matches JSON/form/query keys whose values must never appear
// in logs (passwords, auth tokens, secrets, payment card numbers, ...). It is
// deliberately unanchored: keys are often composed (paperlessToken, setupToken,
// passwordHash, access_token, X-Api-Key), and over-redacting a harmless value
// in a log line is acceptable — under-redacting a credential is not.
var sensitiveKeyRe = regexp.MustCompile(`(?i)(password|passwd|secret|token|jwt|authorization|api[_-]?key|access[_-]?token|refresh[_-]?token|cvv|cvv2|pin|otp)`)

// responseWriter wraps gin.ResponseWriter to capture the response body so it
// can be logged at debug level. Capturing stops after limit bytes while the
// underlying writer still receives the full payload.
type responseWriter struct {
	gin.ResponseWriter
	buf       bytes.Buffer
	limit     int
	truncated bool
}

// Write captures the written bytes (up to limit) while still streaming the
// real response to the client. The capture buffer gets a bounded prefix of b;
// the underlying writer always receives the full payload.
func (w *responseWriter) Write(b []byte) (int, error) {
	if w.limit > 0 {
		if remaining := w.limit - w.buf.Len(); remaining > 0 {
			if len(b) > remaining {
				w.buf.Write(b[:remaining])
				w.truncated = true
			} else {
				w.buf.Write(b)
			}
		} else if len(b) > 0 {
			w.truncated = true
		}
	}
	return w.ResponseWriter.Write(b)
}

// WriteString routes through Write so responses written with WriteString
// (e.g. gin's c.String) are captured too; embedding alone would bypass the
// capture by calling the inner writer directly.
func (w *responseWriter) WriteString(s string) (int, error) {
	return w.Write([]byte(s))
}

// multiReadCloser replays a body after a bounded prefix has been consumed for
// logging while preserving the original Close.
type multiReadCloser struct {
	io.Reader
	io.Closer
}

// capturePrefix reads at most limit+1 bytes from body and returns a reader that
// replays those bytes followed by the unread remainder, so the full payload is
// still available to the caller. Reading limit+1 bytes (rather than the whole
// body) bounds the memory used for logging and lets callers detect truncation
// (len(prefix) == limit+1). A non-positive limit disables capture entirely and
// returns the body untouched.
func capturePrefix(body io.ReadCloser, limit int) ([]byte, io.ReadCloser) {
	if body == nil || limit <= 0 {
		return nil, body
	}
	prefix, err := io.ReadAll(io.LimitReader(body, int64(limit)+1))
	if err != nil {
		return nil, body
	}
	if len(prefix) == 0 {
		return nil, body
	}
	return prefix, multiReadCloser{
		Reader: io.MultiReader(bytes.NewReader(prefix), body),
		Closer: body,
	}
}

// trimPartialRune drops a trailing incomplete multi-byte sequence, so a capture
// cut off mid-rune (the body was longer than the cap) is not mistaken for a
// binary payload. A valid encoding of U+FFFD decodes with size > 1 and is kept.
func trimPartialRune(b []byte) []byte {
	for i := 0; i < utf8.UTFMax && len(b) > 0; i++ {
		if r, size := utf8.DecodeLastRune(b); r != utf8.RuneError || size > 1 {
			return b
		}
		b = b[:len(b)-1]
	}
	return b
}

// RequestLogger returns a gin middleware that logs every HTTP request with its
// method, path, status, latency, and client metadata. When the logger runs at
// debug level (development), it additionally captures and logs the request and
// response bodies, redacting sensitive fields and skipping binary payloads
// such as multipart uploads, PDFs, and images. bodyLimit caps how many bytes of
// each body are logged; a value <= 0 disables body capture entirely (so the
// default config, 0, logs no bodies).
func RequestLogger(l *slog.Logger, bodyLimit int) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		debug := l.Enabled(c.Request.Context(), slog.LevelDebug)

		reqID := c.GetHeader("X-Request-ID")
		if reqID == "" {
			reqID = uuid.NewString()
		}
		c.Set(RequestIDKey, reqID)
		c.Writer.Header().Set("X-Request-ID", reqID)

		captureBodies := debug && bodyLimit > 0
		var reqCapture []byte
		if captureBodies {
			// Capture a bounded request-body prefix before the handler consumes
			// it, and replace the body with a replayable reader so handlers are
			// unaffected. The read is capped at bodyLimit+1 bytes rather than
			// the whole payload.
			// The declared Content-Type is a client claim, so the prefix must
			// also be valid UTF-8 before it is logged: a binary payload sent as
			// text/plain would otherwise land in the log sink.
			if isTextual(c.Request.Header.Get("Content-Type")) {
				var captured []byte
				captured, c.Request.Body = capturePrefix(c.Request.Body, bodyLimit)
				if utf8.Valid(trimPartialRune(captured)) {
					reqCapture = captured
				}
			}
			c.Writer = &responseWriter{ResponseWriter: c.Writer, limit: bodyLimit}
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
			if captureBodies {
				attrs = append(attrs, logBodyAttrs("request_body", reqCapture, bodyLimit)...)
				if rw, ok := c.Writer.(*responseWriter); ok &&
					rw.buf.Len() > 0 &&
					isTextual(c.Writer.Header().Get("Content-Type")) {
					resp, truncated := truncate(redact(rw.buf.Bytes()), bodyLimit)
					attrs = append(attrs, slog.String("response_body", resp))
					if truncated || rw.truncated {
						attrs = append(attrs, slog.Bool("response_body_truncated", true))
					}
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

// truncate limits a logged body to limit bytes and reports whether it was cut
// off. A non-positive cap means no truncation.
func truncate(s string, limit int) (string, bool) {
	if limit > 0 && len(s) > limit {
		return s[:limit], true
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
