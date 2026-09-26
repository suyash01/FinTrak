package query

import (
	"strings"
	"testing"
)

// TestSentinelsAreRefusedWhereThereIsNoNullColumn is the regression test for a
// 500 reached through q=, which the "nothing is ever rejected" rule forbids.
//
// Every uuid field's validator used to accept the "none" / "uncategorized"
// sentinels, but only cat and payee have a nullable column to match. On acct and
// group the literal string was bound against a uuid column and PostgreSQL
// answered with a type error - a 500, for a query the documentation promised
// would work.
func TestSentinelsAreRefusedWhereThereIsNoNullColumn(t *testing.T) {
	for _, q := range []string{"acct:none", "acct:uncategorized", "group:none", "group:uncategorized"} {
		t.Run(q, func(t *testing.T) {
			expr, diags := Parse(q)
			if len(diags) == 0 {
				t.Fatalf("Parse(%q) accepted a sentinel this field cannot match; got terms %+v", q, expr.Terms)
			}
			if diags[0].Code != CodeUnresolved {
				t.Errorf("code = %q, want %q", diags[0].Code, CodeUnresolved)
			}
			if len(expr.Terms) != 0 {
				t.Errorf("expected no terms, got %+v", expr.Terms)
			}
		})
	}
}

// The two fields that DO have a nullable column keep the sentinel, because the
// uncategorized and no-payee filters depend on it.
func TestSentinelsStillWorkWhereTheColumnIsNullable(t *testing.T) {
	for _, tc := range []struct{ q, clause string }{
		{"cat:none", "(t.category_id IS NULL)"},
		{"payee:none", "(t.payee_id IS NULL)"},
	} {
		t.Run(tc.q, func(t *testing.T) {
			sink, diags := compile(t, tc.q)
			if len(diags) != 0 {
				t.Fatalf("unexpected diagnostics: %+v", diags)
			}
			if len(sink.clauses) != 1 || sink.clauses[0] != tc.clause {
				t.Errorf("clauses = %v, want [%s]", sink.clauses, tc.clause)
			}
		})
	}
}

// The diagnostic has to name what the field actually accepts, or the user is told
// to try a spelling that will never work.
func TestSentinelDiagnosticNamesWhatTheFieldAccepts(t *testing.T) {
	_, diags := Parse("acct:none")
	if len(diags) == 0 {
		t.Fatal("expected a diagnostic")
	}
	if msg := diags[0].Message; !strings.Contains(msg, "acct takes an id") {
		t.Errorf("message = %q, want it to say the field takes an id", msg)
	}
	// The field that does take sentinels keeps being told about them.
	_, diags = Parse("cat:notauuid")
	if len(diags) == 0 {
		t.Fatal("expected a diagnostic")
	}
	if msg := diags[0].Message; !strings.Contains(msg, "none") || !strings.Contains(msg, "uncategorized") {
		t.Errorf("message = %q, want it to list the sentinels cat accepts", msg)
	}
}
