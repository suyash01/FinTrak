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
		// answers 200 leaves it empty so an unexpected error envelope fails
		// too: the two 200 cases assert on the return value, and any 400 they
		// somehow produced would have to be invisible in the body.
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
			query:  "asOf=2026-03-01&dateTo=2026-02-15",
			dateTo: "2026-02-15",
			want:   "2026-02-15",
		},
		{
			name:   "a later dateTo does not widen the instant",
			query:  "asOf=2026-03-01&dateTo=2026-06-30",
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
			query:    "asOf=2026-03-01&dateFrom=2026-04-01",
			dateFrom: "2026-04-01",
			want:     "",
			wantCode: http.StatusBadRequest,
			wantBody: []string{"dateFrom 2026-04-01 is after asOf 2026-03-01"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			// The query is the only thing that varies between cases: the window
			// reaches parseAsOf as arguments, which the caller has already
			// validated, while `asOf` is read straight off the request.
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
