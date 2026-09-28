package currency

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestDefaultIsTheColumnDefault pins the code an account is read as when its
// own is unset. It is the accounts.currency column default, not a configurable
// knob, and every spelling of the read expression below depends on it: change
// it and a stored "unset" account changes meaning across every report at once.
func TestDefaultIsTheColumnDefault(t *testing.T) {
	assert.Equal(t, "INR", Default)
}

// TestColumn pins the rendered expression byte for byte, because a respelling
// that is still valid SQL is the failure this package exists to prevent. A bare
// COALESCE(a.currency, 'INR') compiles, passes every argument-level assertion in
// the suite, and then excludes exactly the accounts whose currency is unset
// from a ?currency=INR report that the rest of the same response calls INR.
func TestColumn(t *testing.T) {
	tests := []struct {
		name string
		col  string
		want string
	}{
		{
			name: "a plain aliased column",
			col:  "a.currency",
			want: "COALESCE(NULLIF(a.currency, ''), 'INR')",
		},
		{
			// The query compiler reaches the account through a correlated EXISTS
			// and therefore has to spell the column under a different alias.
			name: "a different alias",
			col:  "ac.currency",
			want: "COALESCE(NULLIF(ac.currency, ''), 'INR')",
		},
		{
			// A link's currency is picked by the same CASE that picks its amount,
			// so the column argument is an expression rather than a column name.
			name: "an expression",
			col:  "CASE WHEN ft.type = 'debit' THEN fa.currency ELSE ta.currency END",
			want: "COALESCE(NULLIF(CASE WHEN ft.type = 'debit' THEN fa.currency ELSE ta.currency END, ''), 'INR')",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, Column(tt.col))
		})
	}
}

// TestPredicate pins the ?currency= fragment. It has to carry the same
// expression the projection and the GROUP BY carry, or the filter excludes the
// very accounts it exists to find.
func TestPredicate(t *testing.T) {
	assert.Equal(t, "COALESCE(NULLIF(a.currency, ''), 'INR') = $2", Predicate("a.currency", 2))
	assert.Equal(t, "COALESCE(NULLIF(ac.currency, ''), 'INR') = $5", Predicate("ac.currency", 5))
}
