package query

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// Real UUIDs, because the server resolves no names: a cat/payee/acct/group
// value must be an id the database could hold, the sentinel "none", or
// "uncategorized". The frontend is what turns a name into one of these.
const (
	uuidA = "11111111-1111-4111-8111-111111111111"
	uuidB = "22222222-2222-4222-8222-222222222222"
	uuidC = "33333333-3333-4333-8333-333333333333"
)

func TestParseAcceptsTheDocumentedForms(t *testing.T) {
	cases := []struct {
		in    string
		terms []Term
	}{
		{"cat:" + uuidA, []Term{{Field: "cat", Op: OpEq, Values: []string{uuidA}}}},
		{"amt>50", []Term{{Field: "amt", Op: OpGt, Values: []string{"50"}}}},
		{"amt>=50", []Term{{Field: "amt", Op: OpGe, Values: []string{"50"}}}},
		{"not cat:" + uuidA + "," + uuidB, []Term{{Field: "cat", Op: OpEq, Values: []string{uuidA, uuidB}, Negated: true}}},
		// A quoted value with a space, on a field where a spaced value is valid.
		// `payee:"Whole Foods"` would be *rejected* here, correctly: the server
		// resolves no names, so the frontend is what turns a name into a uuid.
		{`tag:"Whole Foods"`, []Term{{Field: "tag", Op: OpEq, Values: []string{"Whole Foods"}}}},
		{"coffee", []Term{{Field: FieldPlain, Op: OpLike, Values: []string{"coffee"}}}},
		{"coffee shop", []Term{{Field: FieldPlain, Op: OpLike, Values: []string{"coffee", "shop"}}}},
		{"desc:coffee shop", []Term{
			{Field: "desc", Op: OpEq, Values: []string{"coffee"}},
			{Field: FieldPlain, Op: OpLike, Values: []string{"shop"}},
		}},
		{"cat:" + uuidA + " desc:rent", []Term{
			{Field: "cat", Op: OpEq, Values: []string{uuidA}},
			{Field: "desc", Op: OpEq, Values: []string{"rent"}},
		}},
	}
	for _, tc := range cases {
		expr, diags := Parse(tc.in)
		if len(diags) != 0 {
			t.Errorf("Parse(%q) diagnostics = %+v, want none", tc.in, diags)
		}
		if len(expr.Terms) != len(tc.terms) {
			t.Errorf("Parse(%q) got %d terms, want %d (%+v)", tc.in, len(expr.Terms), len(tc.terms), expr.Terms)
			continue
		}
		for i, want := range tc.terms {
			got := expr.Terms[i]
			if got.Field != want.Field || got.Op != want.Op || got.Negated != want.Negated {
				t.Errorf("Parse(%q) term %d = %+v, want field=%q op=%q neg=%v",
					tc.in, i, got, want.Field, want.Op, want.Negated)
			}
			if len(got.Values) != len(want.Values) {
				t.Errorf("Parse(%q) term %d values = %v, want %v", tc.in, i, got.Values, want.Values)
				continue
			}
			for j := range want.Values {
				if got.Values[j] != want.Values[j] {
					t.Errorf("Parse(%q) term %d value %d = %q, want %q", tc.in, i, j, got.Values[j], want.Values[j])
				}
			}
		}
	}
}

func TestParseDropsAndReportsInsteadOfFailing(t *testing.T) {
	expr, diags := Parse("catgory:food amt>>50 cat:" + uuidA)
	if len(expr.Terms) != 1 || expr.Terms[0].Field != "cat" {
		t.Fatalf("expected only the valid term to survive, got %+v", expr.Terms)
	}
	if len(diags) != 2 {
		t.Fatalf("got %d diagnostics, want 2: %+v", len(diags), diags)
	}
	if diags[0].Code != CodeUnknownField || diags[0].Term != "catgory:food" {
		t.Errorf("first diagnostic = %+v, want unknown_field for catgory:food", diags[0])
	}
	if diags[1].Code != CodeBadOperator {
		t.Errorf("second diagnostic = %+v, want bad_operator", diags[1])
	}
}

func TestParseReportsMissingValue(t *testing.T) {
	_, diags := Parse("cat:")
	if len(diags) != 1 || diags[0].Code != CodeMissingValue {
		t.Fatalf("got %+v, want one missing_value diagnostic", diags)
	}
}

func TestParseEnforcesCaps(t *testing.T) {
	// Space-separated: without a delimiter every run would be one value.
	long := strings.TrimSpace(strings.Repeat("desc:a ", 40))
	expr, diags := Parse(long)
	if len(expr.Terms) != MaxTerms {
		t.Errorf("got %d terms, want the cap of %d", len(expr.Terms), MaxTerms)
	}
	found := false
	for _, d := range diags {
		if d.Code == CodeTooLong {
			found = true
		}
	}
	if !found {
		t.Errorf("want a too_long diagnostic, got %+v", diags)
	}
}

func TestParseTruncatesAnOverlongQuery(t *testing.T) {
	expr, diags := Parse(strings.Repeat("a", MaxQueryChars+50))
	if len(diags) != 1 || diags[0].Code != CodeTooLong {
		t.Fatalf("got %+v, want one too_long diagnostic", diags)
	}
	if len(expr.Terms) != 1 {
		t.Errorf("got %d terms, want the truncated remainder as one plain term", len(expr.Terms))
	}
}

func TestParseQuotedValueKeepsCommas(t *testing.T) {
	expr, diags := Parse(`tag:"food, drink"`)
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics: %+v", diags)
	}
	if len(expr.Terms[0].Values) != 1 || expr.Terms[0].Values[0] != "food, drink" {
		t.Fatalf("got %v, want the comma kept inside the quotes", expr.Terms[0].Values)
	}
}

// TestParseReportsUnresolvedNames is the cost of frontend resolution, pinned:
// a name the server cannot resolve is dropped and named, never treated as an
// id. The frontend is what turns "groceries" into a uuid.
func TestParseReportsUnresolvedNames(t *testing.T) {
	_, diags := Parse("cat:groceries")
	if len(diags) != 1 || diags[0].Code != CodeUnresolved {
		t.Fatalf("got %+v, want one unresolved_value diagnostic", diags)
	}
	if !strings.Contains(diags[0].Message, "groceries") {
		t.Errorf("message %q should quote the offending value", diags[0].Message)
	}
}

func TestParseAcceptsTheExistingSentinels(t *testing.T) {
	expr, diags := Parse("cat:none payee:none cat:uncategorized")
	if len(diags) != 0 {
		t.Fatalf("the sentinels the existing filters use must stay valid: %+v", diags)
	}
	if len(expr.Terms) != 3 {
		t.Fatalf("got %d terms, want 3", len(expr.Terms))
	}
}

func TestParseRejectsATagWithAQuote(t *testing.T) {
	// A tag is bound as a literal, so a quote in one would end the literal.
	_, diags := Parse(`tag:it's`)
	if len(diags) != 1 || diags[0].Code != CodeUnresolved {
		t.Fatalf("got %+v, want one unresolved_value diagnostic", diags)
	}
}

type corpusCase struct {
	Name        string       `json:"name"`
	Surface     string       `json:"surface"`
	Canonical   string       `json:"canonical"`
	Terms       []Term       `json:"terms"`
	Diagnostics []Diagnostic `json:"diagnostics"`
}

// TestCorpusGrammarContract runs the shared corpus. The frontend's vitest
// suite reads this same file (frontend/src/lib/query/parse.test.ts), so a field
// or operator added on one side without the other fails both suites.
//
// Diagnostics are compared on term, code and position but never on message: the
// Go and TypeScript validators are two implementations that are free to word a
// message differently, and comparing prose would couple them on cosmetics.
func TestCorpusGrammarContract(t *testing.T) {
	raw, err := os.ReadFile("testdata/corpus.json")
	if err != nil {
		t.Fatalf("read corpus: %v", err)
	}
	var corpus struct {
		Cases []corpusCase `json:"cases"`
	}
	if err := json.Unmarshal(raw, &corpus); err != nil {
		t.Fatalf("parse corpus: %v", err)
	}
	if len(corpus.Cases) == 0 {
		t.Fatal("corpus is empty; it is the cross-language drift guard")
	}
	for _, tc := range corpus.Cases {
		t.Run(tc.Name, func(t *testing.T) {
			expr, diags := Parse(tc.Surface)
			if len(expr.Terms) != len(tc.Terms) {
				t.Fatalf("Parse(%q) got %d terms, want %d (%+v)", tc.Surface, len(expr.Terms), len(tc.Terms), expr.Terms)
			}
			for i, want := range tc.Terms {
				got := expr.Terms[i]
				if got.Field != want.Field || got.Op != want.Op || got.Negated != want.Negated || got.Position != want.Position {
					t.Errorf("term %d = %+v, want field=%q op=%q neg=%v pos=%d",
						i, got, want.Field, want.Op, want.Negated, want.Position)
				}
				if strings.Join(got.Values, ",") != strings.Join(want.Values, ",") {
					t.Errorf("term %d values = %v, want %v", i, got.Values, want.Values)
				}
			}
			if len(diags) != len(tc.Diagnostics) {
				t.Fatalf("Parse(%q) got %d diagnostics, want %d (%+v)", tc.Surface, len(diags), len(tc.Diagnostics), diags)
			}
			for i, want := range tc.Diagnostics {
				if diags[i].Code != want.Code || diags[i].Term != want.Term || diags[i].Position != want.Position {
					t.Errorf("diagnostic %d = %+v, want term=%q code=%q pos=%d",
						i, diags[i], want.Term, want.Code, want.Position)
				}
			}
		})
	}
}
