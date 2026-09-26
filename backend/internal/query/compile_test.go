package query

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
)

// recSink records what the compiler emitted, standing in for txnFilter.
//
// Its numbering mirrors the real builder exactly: args[0] is pre-seeded with the
// user id, so the first value the compiler appends is $2. The corpus records
// only what the query itself contributed, so comparisons use args[1:].
type recSink struct {
	clauses []string
	args    []any
}

func newRecSink() *recSink { return &recSink{args: []any{"user-id"}} }

// bound returns the arguments the query contributed, without the seeded user id.
func (s *recSink) bound() []any { return s.args[1:] }

func (s *recSink) Param(clause string, value any) {
	s.args = append(s.args, value)
	s.clauses = append(s.clauses, fmt.Sprintf(clause, len(s.args)))
}

func (s *recSink) Clause(format string, value any) string {
	s.args = append(s.args, value)
	return fmt.Sprintf(format, len(s.args))
}

func (s *recSink) AnyOf(clauses []string) {
	if len(clauses) == 0 {
		return
	}
	s.clauses = append(s.clauses, "("+strings.Join(clauses, " OR ")+")")
}

func (s *recSink) Params(clause string, values ...any) {
	first := len(s.args) + 1
	s.args = append(s.args, values...)
	placeholders := make([]any, len(values))
	for i := range values {
		placeholders[i] = first + i
	}
	s.clauses = append(s.clauses, fmt.Sprintf(clause, placeholders...))
}

func (s *recSink) Raw(clause string) {
	s.clauses = append(s.clauses, clause)
}

// compile is the whole pipeline for a well-formed query.
func compile(t *testing.T, q string) (*recSink, []Diagnostic) {
	t.Helper()
	expr, diags := Parse(q)
	if len(diags) != 0 {
		t.Fatalf("Parse(%q) diagnostics: %+v", q, diags)
	}
	sink := newRecSink()
	return sink, append(diags, Compile(expr, sink)...)
}

// TestCompileMajorUnitsBecomeMinorUnits is the money test: `amt>50` is fifty
// dollars and must bind 5000, never 50. A query for $50 that matched a $0.50
// transaction would be silently and invisibly wrong.
// TestCompileNotOnAPlainTermNegates is the regression test for a term that
// silently compiled to its own opposite. `not coffee` used to emit exactly the
// same predicate as `coffee`, so a user asking for everything EXCEPT coffee got
// only coffee, with no diagnostic and a plausible-looking row count. The grammar
// sheet advertises `not`, so the compiler has to honour it.
func TestCompileNotOnAPlainTermNegates(t *testing.T) {
	plain, _ := compile(t, "coffee")
	negated, _ := compile(t, "not coffee")

	if len(negated.clauses) != 1 {
		t.Fatalf("got %d clauses, want one", len(negated.clauses))
	}
	if !strings.HasPrefix(negated.clauses[0], "NOT (") {
		t.Errorf("clause %q is not negated", negated.clauses[0])
	}
	if plain.clauses[0] == negated.clauses[0] {
		t.Error("`not coffee` compiled to the same predicate as `coffee`")
	}
}

// TestCompileNotOnAMultiWordPlainTermNegatesTheWholeTerm: `not coffee shop` is
// "not (coffee AND shop)", so the NOT must wrap the conjunction once. Wrapping
// each word separately would be "neither coffee nor shop", which is a different
// and much broader request.
func TestCompileNotOnAMultiWordPlainTermNegatesTheWholeTerm(t *testing.T) {
	sink, _ := compile(t, "not coffee shop")
	if len(sink.clauses) != 1 {
		t.Fatalf("got %d clauses, want the whole term as one", len(sink.clauses))
	}
	if !strings.HasPrefix(sink.clauses[0], "NOT (") {
		t.Errorf("clause %q is not negated", sink.clauses[0])
	}
	if !strings.Contains(sink.clauses[0], " AND ") {
		t.Errorf("clause %q should keep the words AND-ed inside the NOT", sink.clauses[0])
	}
}

// TestCompileBooleanCSVIsAnOr is the regression test for a query that answered
// "nothing" instead of "either". A comma-separated value is an OR everywhere
// else, and the openapi prose promises it for every field - but the two boolean
// fields emit one Raw fragment per value and Raw fragments are AND-ed, so
// `linked:true,false` compiled to EXISTS(...) AND NOT EXISTS(...), which is
// always false, with no diagnostic.
func TestCompileBooleanCSVIsAnOr(t *testing.T) {
	sink, diags := compile(t, "linked:true,false")
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics: %+v", diags)
	}
	if len(sink.clauses) != 1 {
		t.Fatalf("got %d clauses, want one OR group", len(sink.clauses))
	}
	if !strings.Contains(sink.clauses[0], " OR ") {
		t.Errorf("clause %q should OR the two senses", sink.clauses[0])
	}
	if strings.Contains(sink.clauses[0], ") AND ") {
		t.Errorf("clause %q ANDs two contradictory EXISTS fragments, so it can never match", sink.clauses[0])
	}
}

// The same for the recurring field, whose two senses are its own.
func TestCompileRecurringCSVIsAnOr(t *testing.T) {
	sink, _ := compile(t, "recurring:linked,unlinked")
	if len(sink.clauses) != 1 || !strings.Contains(sink.clauses[0], " OR ") {
		t.Errorf("clauses = %v, want one OR group", sink.clauses)
	}
}

func TestCompileMajorUnitsBecomeMinorUnits(t *testing.T) {
	sink, diags := compile(t, "amt>50")
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics: %+v", diags)
	}
	if !reflect.DeepEqual(sink.bound(), []any{int64(5000)}) {
		t.Errorf("args = %#v, want []any{int64(5000)}", sink.bound())
	}
	if !reflect.DeepEqual(sink.clauses, []string{"(t.amount > $2)"}) {
		t.Errorf("clauses = %v, want [(t.amount > $2)] - every group is OR-wrapped, as txnQueryFilter's anyOf already does", sink.clauses)
	}
}

// TestCompileFractionalAmountIsExact guards the other half: 50.75 is 5075, and
// no float ever touches the value.
func TestCompileFractionalAmountIsExact(t *testing.T) {
	sink, _ := compile(t, "amt>=50.75")
	if !reflect.DeepEqual(sink.bound(), []any{int64(5075)}) {
		t.Errorf("args = %#v, want []any{int64(5075)}", sink.bound())
	}
}

func TestCompileCSVIsOredAndNegationWrapsTheGroup(t *testing.T) {
	sink, _ := compile(t, "not cat:"+uuidA+","+uuidB)
	want := "NOT (t.category_id = $2 OR t.category_id = $3)"
	if !reflect.DeepEqual(sink.clauses, []string{want}) {
		t.Errorf("clauses = %v, want [%s]", sink.clauses, want)
	}
}

// TestCompileNotEqualsIsNegation pins that `!=` means the same as `not ...`.
// A compiler that only looked at the not-prefix would turn `desc!=rent` into a
// contains, which is the opposite of what was asked.
func TestCompileNotEqualsIsNegation(t *testing.T) {
	sink, _ := compile(t, "desc!=rent")
	want := "NOT (LOWER(t.description) LIKE LOWER($2))"
	if !reflect.DeepEqual(sink.clauses, []string{want}) {
		t.Errorf("clauses = %v, want [%s]", sink.clauses, want)
	}
}

func TestCompilePlainSearchIsTheExistingFourWayOr(t *testing.T) {
	sink, _ := compile(t, "coffee")
	if len(sink.clauses) != 1 {
		t.Fatalf("clauses = %v, want one clause", sink.clauses)
	}
	for _, want := range []string{
		"LOWER(t.description) LIKE LOWER($2)",
		"LOWER(COALESCE(t.notes, '')) LIKE LOWER($3)",
		"payees sp",
		"unnest(t.tags)",
	} {
		if !strings.Contains(sink.clauses[0], want) {
			t.Errorf("clause %q is missing %q", sink.clauses[0], want)
		}
	}
}

// TestCompilePlainSearchBindsOneGroupPerWord: `coffee shop` must require both
// words, as two four-way groups AND-ed inside a single clause. One clause rather
// than two is deliberate: it is what lets a negated term wrap the whole
// conjunction in one NOT.
func TestCompilePlainSearchBindsOneGroupPerWord(t *testing.T) {
	sink, _ := compile(t, "coffee shop")
	if len(sink.clauses) != 1 {
		t.Fatalf("got %d clauses, want the whole term as one", len(sink.clauses))
	}
	if !strings.Contains(sink.clauses[0], " AND ") {
		t.Errorf("clause %q should AND the two words", sink.clauses[0])
	}
	if len(sink.bound()) != 8 {
		t.Errorf("got %d args, want 8 (four per word)", len(sink.bound()))
	}
	for i, want := range []any{"%coffee%", "%coffee%", "%coffee%", "%coffee%", "%shop%", "%shop%", "%shop%", "%shop%"} {
		if sink.bound()[i] != want {
			t.Errorf("arg %d = %#v, want %#v", i, sink.bound()[i], want)
		}
	}
}

func TestCompileEscapesLikeWildcards(t *testing.T) {
	sink, _ := compile(t, `desc~100%`)
	if sink.bound()[0] != `%100\%%` {
		t.Errorf("arg = %#v, want the percent escaped so it matches literally", sink.bound()[0])
	}
}

func TestCompileSentinelMatchesNull(t *testing.T) {
	sink, _ := compile(t, "cat:none")
	if !reflect.DeepEqual(sink.clauses, []string{"(t.category_id IS NULL)"}) {
		t.Errorf("clauses = %v", sink.clauses)
	}
	if len(sink.bound()) != 0 {
		t.Errorf("IS NULL binds nothing, got args %v", sink.bound())
	}
}

func TestCompileBooleanFieldsBindNothing(t *testing.T) {
	sink, _ := compile(t, "linked:true recurring:unlinked")
	// A single value is still OR-wrapped, like every other field's single value.
	want := []string{
		"(EXISTS (SELECT 1 FROM links l WHERE l.from_txn_id = t.id OR l.to_txn_id = t.id))",
		"(NOT EXISTS (SELECT 1 FROM recurring_attachments ra WHERE ra.transaction_id = t.id))",
	}
	if !reflect.DeepEqual(sink.clauses, want) {
		t.Errorf("clauses = %v, want %v", sink.clauses, want)
	}
	if len(sink.bound()) != 0 {
		t.Errorf("the two fixed fragments bind nothing, got args %v", sink.bound())
	}
}

func TestCompileTagListIsAnArrayOverlap(t *testing.T) {
	sink, _ := compile(t, "tag:food,drink")
	if !strings.Contains(sink.clauses[0], "t.tags && ") {
		t.Errorf("clause %q should use array overlap", sink.clauses[0])
	}
	if !strings.Contains(sink.clauses[0], "food") || !strings.Contains(sink.clauses[0], "drink") {
		t.Errorf("clause %q should carry both tag names", sink.clauses[0])
	}
}

// TestCompileAWhollyUnresolvableTermIsANoOp: if every value in a CSV failed to
// resolve the term must emit nothing at all, not a false predicate — the user
// gets the wider result plus a diagnostic saying what was ignored.
func TestCompileAWhollyUnresolvableTermIsANoOp(t *testing.T) {
	expr := Expr{Terms: []Term{{
		Field: "cat", Op: OpEq, Values: []string{}, Raw: "cat:", Position: 0,
	}}}
	sink := newRecSink()
	if out := Compile(expr, sink); len(out) != 0 {
		t.Errorf("expected no compile diagnostic for an empty value list, got %+v", out)
	}
	if len(sink.clauses) != 0 {
		t.Errorf("expected no predicate, got %v", sink.clauses)
	}
}

// TestCompileOnlyReferencesTheTransactionsTable is the invariant that keeps the
// list query and the COUNT(*) query in agreement: txnQueryFilter runs the same
// predicate with no joins, so a fragment naming a joined table would make the
// count fail while the page rendered fine.
func TestCompileOnlyReferencesTheTransactionsTable(t *testing.T) {
	queries := []string{
		"coffee", "coffee shop", "desc:rent", "note:memo", "cat:none", "cat:" + uuidA,
		"group:" + uuidA, "acct:" + uuidA, "payee:none", "payee:" + uuidA,
		"tag:food", "tag:food,drink", "type:debit", "amt>50", "date>=2026-01-01",
		"linked:true", "linked:false", "recurring:unlinked", "not linked:true",
	}
	for _, q := range queries {
		t.Run(q, func(t *testing.T) {
			expr, diags := Parse(q)
			if len(diags) != 0 {
				t.Skipf("query does not parse: %+v", diags)
			}
			sink := newRecSink()
			Compile(expr, sink)
			for _, c := range sink.clauses {
				if strings.Contains(strings.ToUpper(c), " JOIN ") {
					t.Errorf("clause %q contains a JOIN; the count query has none", c)
				}
				// Every table reference outside a correlated EXISTS must be the
				// aliased transactions table.
				if strings.Contains(c, " FROM transactions") && !strings.Contains(c, "FROM transactions t") {
					t.Errorf("clause %q must read FROM transactions t", c)
				}
			}
		})
	}
}

type corpusSQLCase struct {
	Name      string `json:"name"`
	Surface   string `json:"surface"`
	Canonical string `json:"canonical"`
	SQL       *struct {
		Clauses []string `json:"clauses"`
		Args    []any    `json:"args"`
		Kind    string   `json:"kind"`
	} `json:"sql"`
}

// readCorpusSQL decodes the corpus with UseNumber so an integer argument in the
// fixture stays an exact integer instead of becoming a float64 — a float
// round-trip would defeat the point of a test that exists to catch a money
// conversion going wrong.
func readCorpusSQL(t *testing.T) []corpusSQLCase {
	t.Helper()
	raw, err := os.ReadFile("testdata/corpus.json")
	if err != nil {
		t.Fatalf("read corpus: %v", err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var corpus struct {
		Cases []corpusSQLCase `json:"cases"`
	}
	if err := dec.Decode(&corpus); err != nil {
		t.Fatalf("parse corpus: %v", err)
	}
	if len(corpus.Cases) == 0 {
		t.Fatal("corpus is empty; it is the cross-language drift guard")
	}
	return corpus.Cases
}

func normalizeArg(v any) any {
	n, ok := v.(json.Number)
	if !ok {
		return v
	}
	i, err := n.Int64()
	if err != nil {
		return v
	}
	return i
}

func TestCorpusSQLContract(t *testing.T) {
	for _, tc := range readCorpusSQL(t) {
		if tc.SQL == nil {
			continue
		}
		t.Run(tc.Name, func(t *testing.T) {
			if tc.SQL.Kind == "plain" {
				// The free-text shape is a four-way group with four placeholders
				// per word, asserted directly by
				// TestCompilePlainSearchIsTheExistingFourWayOr.
				return
			}
			// Compile the CANONICAL form, not the surface. The canonical is what
			// the frontend actually puts on the wire, so compiling the surface
			// would leave the whole frontend-to-server path unverified: an
			// `amt` term is the one case where they differ, and a double
			// conversion there once made every amount filter 100x too large
			// without a single test noticing.
			surface := tc.Canonical
			if surface == "" {
				surface = tc.Surface
			}
			sink, diags := compile(t, surface)
			if len(diags) != 0 {
				t.Fatalf("Compile(%q) diagnostics: %+v", surface, diags)
			}
			if !reflect.DeepEqual(sink.clauses, tc.SQL.Clauses) {
				t.Errorf("clauses = %v, want %v", sink.clauses, tc.SQL.Clauses)
			}
			want := make([]any, len(tc.SQL.Args))
			for i, a := range tc.SQL.Args {
				want[i] = normalizeArg(a)
			}
			if !reflect.DeepEqual(sink.bound(), want) {
				t.Errorf("args = %#v, want %#v", sink.bound(), want)
			}
		})
	}
}
