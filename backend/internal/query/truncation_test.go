package query

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// TestParseTruncatesOnARuneBoundary is the regression test for a 500 reachable
// through q=, which the "nothing is ever rejected" rule forbids.
//
// The cap sliced q[:2000] on BYTES. A query whose 2000th byte lands inside a
// multi-byte rune produced invalid UTF-8, which went into a bound argument and
// came back from the database as an encoding error.
func TestParseTruncatesOnARuneBoundary(t *testing.T) {
	// A Japanese tag makes every character three bytes, so byte 2000 falls inside
	// one of them.
	q := strings.Repeat("日", 700) // 2100 bytes
	if len(q) <= MaxQueryChars {
		t.Fatalf("fixture is %d bytes, needs to exceed the %d cap", len(q), MaxQueryChars)
	}

	expr, diags := Parse(q)
	if len(expr.Terms) == 0 {
		t.Fatalf("expected the truncated remainder as a term, got %+v (%+v)", expr.Terms, diags)
	}
	for _, term := range expr.Terms {
		for _, v := range term.Values {
			if !utf8.ValidString(v) {
				t.Fatalf("term value is not valid UTF-8: %q", v)
			}
		}
		if !utf8.ValidString(term.Raw) {
			t.Fatalf("term Raw is not valid UTF-8: %q", term.Raw)
		}
	}
	for _, d := range diags {
		if !utf8.ValidString(d.Term) {
			t.Fatalf("diagnostic Term is not valid UTF-8: %q", d.Term)
		}
	}
}

// Positions are rune offsets, not byte offsets, so a diagnostic after a
// multi-byte character still points at the right place. The TypeScript parser
// indexes UTF-16 code units, so the two agree only for ASCII - which the corpus
// stays within, and which this test pins.
func TestPositionCountsCharactersNotBytes(t *testing.T) {
	// Three 3-byte characters, then a bad field: the term starts at character 3.
	q := "日日本 catgory:food"
	_, diags := Parse(q)
	if len(diags) != 1 {
		t.Fatalf("got %d diagnostics, want 1: %+v", len(diags), diags)
	}
	if diags[0].Position != 4 {
		t.Errorf("Position = %d, want 4 (a character offset, not a byte offset of %d)",
			diags[0].Position, strings.Index(q, "catgory"))
	}
}
