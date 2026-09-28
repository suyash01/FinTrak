package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestParseAsOf(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cases := []struct {
		name     string
		query    string
		dateFrom string
		dateTo   string
		want     string
		wantCode int
		// wantBody, when set, must appear in the recorded body. A case that
		// answers 200 leaves it empty, and the run asserts the body is empty
		// too: a 400 that somehow accompanied an ok=true result would
		// otherwise be invisible to a return-value-only assertion.
		wantBody []string
	}{
		{
			name:  "absent",
			query: "",
			want:  "",
		},
		{
			name:  "valid",
			query: "asOf=2026-03-01",
			want:  "2026-03-01",
		},
		{
			name:   "clamped by dateTo",
			query:  "asOf=2026-03-01",
			dateTo: "2026-02-15",
			want:   "2026-02-15",
		},
		{
			name:   "a later dateTo does not widen the instant",
			query:  "asOf=2026-03-01",
			dateTo: "2026-06-30",
			want:   "2026-03-01",
		},
		{
			name:     "malformed",
			query:    "asOf=01/03/2026",
			want:     "",
			wantCode: http.StatusBadRequest,
			wantBody: []string{"asOf must be YYYY-MM-DD"},
		},
		{
			name:     "out of the ledger window",
			query:    "asOf=1800-01-01",
			want:     "",
			wantCode: http.StatusBadRequest,
			wantBody: []string{"date out of range"},
		},
		{
			name:     "dateFrom after asOf",
			query:    "asOf=2026-03-01",
			dateFrom: "2026-04-01",
			want:     "",
			wantCode: http.StatusBadRequest,
			wantBody: []string{"dateFrom 2026-04-01 is after asOf 2026-03-01"},
		},
		// The comparison is `>` and not `>=`, so an equal dateFrom is a legal
		// one-day window. This is the only row that pins that boundary: every
		// other row either leaves dateFrom empty or puts a month between the
		// two, so flipping `>` to `>=` would pass them all and turn this
		// request into a 400.
		{
			name:     "dateFrom equal to asOf is a one-day window",
			query:    "asOf=2026-03-01",
			dateFrom: "2026-03-01",
			want:     "2026-03-01",
		},
		// The clamp runs before the dateFrom check, so this pair is judged
		// against the pulled-back instant (2026-02-15), not the requested one
		// (2026-03-01) — which is the whole reason for the ordering. It is
		// still a 400 either way, so the row earns its place through the
		// message it pins, not through the status code.
		{
			name:     "dateFrom after the clamped asOf names the clamped value",
			query:    "asOf=2026-03-01",
			dateTo:   "2026-02-15",
			dateFrom: "2026-02-20",
			want:     "",
			wantCode: http.StatusBadRequest,
			wantBody: []string{"dateFrom 2026-02-20 is after asOf 2026-02-15"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			// parseAsOf reads only `asOf` off the request; the window arrives as
			// arguments, already validated by the caller. So `dateTo=` and
			// `dateFrom=` are deliberately absent from these query strings even
			// where a case uses them: repeating the value in both places would
			// let a rewrite that read the window from the request instead of
			// from its arguments pass unnoticed.
			c.Request = httptest.NewRequest(http.MethodGet, "/?"+tc.query, nil)

			got, ok := parseAsOf(c, tc.dateFrom, tc.dateTo)

			if tc.wantCode != http.StatusBadRequest {
				if w.Body.Len() != 0 {
					t.Errorf("body = %q, want no error envelope", w.Body.String())
				}
				if !ok || got != tc.want {
					t.Errorf("parseAsOf = %q, %t; want %q, true", got, ok, tc.want)
				}
				return
			}

			if ok || got != "" {
				t.Errorf("parseAsOf = %q, %t; want \"\", false", got, ok)
			}
			if w.Code != tc.wantCode {
				t.Errorf("answered %d, want %d", w.Code, tc.wantCode)
			}
			for _, want := range tc.wantBody {
				if !strings.Contains(w.Body.String(), want) {
					t.Errorf("body %q does not contain %q", w.Body.String(), want)
				}
			}
		})
	}
}
