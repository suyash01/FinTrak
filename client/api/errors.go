package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// FieldError is one invalid field reported by the API's error envelope.
type FieldError struct {
	Field   string `json:"field,omitempty"`
	Tag     string `json:"tag,omitempty"`
	Message string `json:"message"`
}

// ErrorResponse is the standard envelope every handler returns for 4xx/5xx.
type ErrorResponse struct {
	Errors []FieldError `json:"errors"`
}

// APIError is a non-2xx response. The TUI surfaces Message() in the status line
// and keeps the status code so callers can branch on 401/403/404/409/429.
type APIError struct {
	Status int
	Errors []FieldError
	Body   string
}

// Error renders the API status and the one-line error message.
func (e *APIError) Error() string {
	return fmt.Sprintf("%d %s: %s", e.Status, http.StatusText(e.Status), e.Message())
}

// Message renders the envelope as a single line, joining field errors. Some
// responses (proxied Paperless failures, the statement parser's upstream, a
// plain-text nginx error page) do not use the envelope, so the raw body is the
// fallback.
func (e *APIError) Message() string {
	if len(e.Errors) > 0 {
		parts := make([]string, 0, len(e.Errors))
		for _, fe := range e.Errors {
			if fe.Field != "" {
				parts = append(parts, fe.Field+": "+fe.Message)
				continue
			}
			parts = append(parts, fe.Message)
		}
		return strings.Join(parts, "; ")
	}
	body := strings.TrimSpace(e.Body)
	if body == "" {
		return http.StatusText(e.Status)
	}
	if len(body) > 300 {
		body = body[:300] + "…"
	}
	return strings.Join(strings.Fields(body), " ")
}

// StatusIs reports whether err is directly an *APIError with the given status.
// It does not walk an Unwrap chain; use Unauthorized or inspect the wrapped
// error when callers may have added context.
func StatusIs(err error, status int) bool {
	ae, ok := err.(*APIError)
	return ok && ae.Status == status
}

// Unauthorized reports whether err is (or wraps) a 401.
func Unauthorized(err error) bool {
	for err != nil {
		if StatusIs(err, http.StatusUnauthorized) {
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

// newAPIError builds an APIError from a response, decoding the standard error
// envelope when the body carries one. A panic is recovered by gin's
// CustomRecovery and written as {"error":"…"} (main.go:136), a different key,
// so that shape is recognized too.
func newAPIError(resp *http.Response, body []byte) *APIError {
	e := &APIError{Status: resp.StatusCode, Body: string(body)}
	var envelope ErrorResponse
	if err := json.Unmarshal(body, &envelope); err == nil && len(envelope.Errors) > 0 {
		e.Errors = envelope.Errors
	}
	if e.Errors == nil {
		var plain struct {
			Error string `json:"error"`
		}
		if err := json.Unmarshal(body, &plain); err == nil && plain.Error != "" {
			e.Errors = []FieldError{{Message: plain.Error}}
		}
	}
	if e.Errors == nil && resp.StatusCode == http.StatusTooManyRequests {
		msg := "too many requests — slow down"
		if retry := resp.Header.Get("Retry-After"); retry != "" {
			msg += " (retry in " + retry + "s)"
		}
		e.Errors = []FieldError{{Message: msg}}
	}
	return e
}

// closeBody drains and closes a response body so the connection can be reused.
func closeBody(body io.ReadCloser) {
	if body == nil {
		return
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(body, 1<<16))
	_ = body.Close()
}
