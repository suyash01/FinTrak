package validation

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestCheckTransactionDate(t *testing.T) {
	now := time.Date(2026, 3, 15, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name    string
		value   string
		want    string
		wantMsg string
	}{
		{name: "today", value: "2026-03-15", want: "2026-03-15"},
		{name: "past", value: "2024-01-15", want: "2024-01-15"},
		{name: "the earliest accepted day", value: TransactionDateMin, want: TransactionDateMin},
		{name: "one year ahead", value: "2027-03-15", want: "2027-03-15"},

		{name: "malformed", value: "15/01/2024", wantMsg: "invalid date (expected YYYY-MM-DD)"},
		{name: "empty", value: "", wantMsg: "invalid date (expected YYYY-MM-DD)"},
		{name: "one day before the window", value: "1899-12-31", wantMsg: "date out of range (1900-01-01 to 2027-03-15)"},
		// A typo'd or OCR'd year: the whole point of the bound, because a cycle
		// is generated per month between the earliest transaction and today.
		{name: "an impossible year", value: "1024-01-01", wantMsg: "date out of range (1900-01-01 to 2027-03-15)"},
		{name: "one day past the window", value: "2027-03-16", wantMsg: "date out of range (1900-01-01 to 2027-03-15)"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, msg := CheckTransactionDate(tt.value, now)
			assert.Equal(t, tt.wantMsg, msg)
			if tt.wantMsg == "" {
				assert.Equal(t, tt.want, got.Format("2006-01-02"))
				return
			}
			assert.True(t, got.IsZero(), "a rejected date must not be returned")
		})
	}
}

// The window must follow "today", not a fixed constant: an entry dated next
// year is accepted while the calendar has not reached it yet, and the same
// entry is rejected once it falls outside the window.
func TestTransactionDateMaxFollowsToday(t *testing.T) {
	now := time.Date(2026, 12, 31, 23, 30, 0, 0, time.UTC)
	assert.Equal(t, "2027-12-31", TransactionDateMax(now).Format("2006-01-02"))

	_, msg := CheckTransactionDate("2027-12-31", now)
	assert.Empty(t, msg)
	_, msg = CheckTransactionDate("2028-01-01", now)
	assert.NotEmpty(t, msg)
}
