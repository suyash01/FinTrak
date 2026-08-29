package logger

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// roundTripperFunc adapts a function to the http.RoundTripper interface.
type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

// LoggingRoundTripper wraps base (defaulting to http.DefaultTransport) so every
// outbound HTTP call the backend makes — Paperless-ngx, the statement parser —
// shows up in the structured log. Info level records the method, the full URL
// including its query string, response status, and latency. At debug level the
// redacted request/response bodies are appended too. Sensitive headers
// (Authorization, Cookie, ...) and query values are never written; they are
// replaced with "[REDACTED]".
func LoggingRoundTripper(base http.RoundTripper, l *slog.Logger) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	return roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		start := time.Now()
		debug := l.Enabled(req.Context(), slog.LevelDebug)

		// Snapshot a textual request body so it can be logged, then restore it
		// unchanged so the actual request is unaffected. Binary/multipart
		// payloads (PDF uploads) are skipped entirely.
		var reqBody []byte
		if debug && req.Body != nil && isTextual(req.Header.Get("Content-Type")) {
			reqBody, _ = io.ReadAll(req.Body)
			req.Body = io.NopCloser(bytes.NewReader(reqBody))
		}

		resp, err := base.RoundTrip(req)

		level := slog.LevelInfo
		attrs := []slog.Attr{
			slog.String("method", req.Method),
			slog.String("url", redactURL(req.URL)),
			slog.Int("status", statusOf(resp)),
			slog.Duration("latency", time.Since(start)),
		}
		if err != nil {
			level = slog.LevelWarn
			attrs = append(attrs, slog.String("error", err.Error()))
		}

		// Snapshot a textual response body for logging and restore it so the
		// caller reads the full payload.
		var respBody []byte
		if resp != nil && debug && isTextual(resp.Header.Get("Content-Type")) {
			if b, rerr := io.ReadAll(resp.Body); rerr == nil {
				respBody = b
				resp.Body.Close()
				resp.Body = io.NopCloser(bytes.NewReader(b))
			}
		}

		if debug {
			level = slog.LevelDebug
			for k := range req.Header {
				if redactHeaderValue(k) {
					attrs = append(attrs, slog.String("redacted_header", strings.ToLower(k)))
				}
			}
			attrs = append(attrs, logBodyAttrs("request_body", reqBody)...)
			attrs = append(attrs, logBodyAttrs("response_body", respBody)...)
		}

		l.LogAttrs(req.Context(), level, "outbound_request", attrs...)
		return resp, err
	})
}

// statusOf returns the response status code, or 0 when there is no response.
func statusOf(resp *http.Response) int {
	if resp == nil {
		return 0
	}
	return resp.StatusCode
}

// redactURL returns the URL string with embedded credentials removed and any
// query value whose key matches a sensitive field replaced.
func redactURL(u *url.URL) string {
	if u == nil {
		return ""
	}
	if u.User != nil {
		u2 := *u
		u2.User = nil
		u = &u2
	}
	q := u.Query()
	changed := false
	for k := range q {
		if sensitiveKeyRe.MatchString(k) {
			q.Set(k, "[REDACTED]")
			changed = true
		}
	}
	if changed {
		u2 := *u
		u2.RawQuery = q.Encode()
		u = &u2
	}
	return u.String()
}

// redactHeaderValue reports whether a header's value must never be logged.
func redactHeaderValue(k string) bool {
	return sensitiveKeyRe.MatchString(k) || strings.EqualFold(k, "cookie")
}

// logBodyAttrs builds the request/response body log attributes, capped by
// maxBodyLog and with sensitive JSON fields redacted.
func logBodyAttrs(name string, body []byte) []slog.Attr {
	if len(body) == 0 {
		return nil
	}
	s, truncated := truncate(redact(body))
	attrs := []slog.Attr{slog.String(name, s)}
	if truncated {
		attrs = append(attrs, slog.Bool(name+"_truncated", true))
	}
	return attrs
}