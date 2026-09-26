package query

import "testing"

// TestCompileRefusesATagItCannotBindSafely is the defence-in-depth test for the
// one place the compiler puts user text into a statement instead of binding it.
//
// emitTags builds ARRAY['a','b']::text[] rather than binding a []string, because
// a bound slice makes the emitted SQL unreadable in a log. The parser refuses a
// tag containing a quote, so a tag reaching the compiler through Parse can never
// break out of the literal. But Compile is exported and takes an Expr: a caller
// that builds a Term directly would otherwise get an injected literal, so the
// check is repeated at the point of interpolation rather than trusted.
func TestCompileRefusesATagItCannotBindSafely(t *testing.T) {
	expr := Expr{Terms: []Term{{
		Field:  "tag",
		Op:     OpEq,
		Values: []string{`x'] OR 1=1 --`},
		Raw:    `tag:x'] OR 1=1 --`,
	}}}
	sink := newRecSink()
	out := Compile(expr, sink)

	if len(out) != 1 || out[0].Code != CodeUnresolved {
		t.Fatalf("want one unresolved_value diagnostic, got %+v", out)
	}
	if len(sink.clauses) != 0 {
		t.Errorf("nothing may be emitted, got %v", sink.clauses)
	}
}

// TestCompileRefusesATagWithAQuoteOnAnyValue: one bad item in a CSV must not be
// silently interpolated alongside the good ones.
func TestCompileRefusesATagWithAQuoteOnAnyValue(t *testing.T) {
	expr := Expr{Terms: []Term{{
		Field:  "tag",
		Op:     OpEq,
		Values: []string{"food", "it's"},
		Raw:    "tag:food,it's",
	}}}
	sink := newRecSink()
	out := Compile(expr, sink)
	if len(out) != 1 {
		t.Fatalf("want one diagnostic, got %+v", out)
	}
	if len(sink.clauses) != 0 {
		t.Errorf("nothing may be emitted, got %v", sink.clauses)
	}
}
