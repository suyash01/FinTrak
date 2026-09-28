package api

import "testing"

// The server resolves no names, so a caller that wants `cat:Groceries` to mean
// something has to turn it into a uuid first. This is that step, and it is the
// same one the web app performs in frontend/src/lib/query/resolve.ts — which is
// why the cases here and in resolve.test.ts are kept to the same grammar.

func resolveSource() RefSource {
	return RefSource{
		Accounts: []Account{
			{ID: "a1", Name: "HDFC Savings"},
			{ID: "a2", Name: "Cash"},
		},
		Categories: []Category{
			{ID: "c1", Name: "Groceries", GroupID: "g1"},
			{ID: "c2", Name: "Rent", GroupID: "g1"},
			// A second "Groceries" in another group: the ambiguity case.
			{ID: "c3", Name: "Groceries", GroupID: "g2"},
		},
		Groups: []CategoryGroup{
			{ID: "g1", Name: "Expense"},
			{ID: "g2", Name: "Income"},
		},
		Payees: []Payee{
			{ID: "p1", Name: "Whole Foods"},
		},
	}
}

// TestResolveQueryTurnsANameIntoAnID is the whole point: a person types a name,
// and what goes on the wire is a uuid the server can act on. "Rent" is
// deliberately unambiguous in this source; the ambiguous case has its own test.
func TestResolveQueryTurnsANameIntoAnID(t *testing.T) {
	got, diags := ResolveQuery(`cat:Rent`, resolveSource())
	if diags != nil {
		t.Fatalf("diagnostics = %+v, want none", diags)
	}
	if got != "cat:c2" {
		t.Errorf("q = %q, want cat:c2", got)
	}
}

// TestResolveQueryIsCaseInsensitive is the reason a person can type "whole
// foods" for "Whole Foods" rather than having to reproduce the exact spelling.
// A value containing a space is quoted, because a bare value run stops at
// whitespace — that is the grammar, and the resolver must not invent a wider one.
func TestResolveQueryIsCaseInsensitive(t *testing.T) {
	got, diags := ResolveQuery(`payee:"whole foods"`, resolveSource())
	if diags != nil {
		t.Fatalf("diagnostics = %+v, want none", diags)
	}
	if got != "payee:p1" {
		t.Errorf("q = %q, want payee:p1", got)
	}
}

// TestResolveQueryKeepsAUuid pins the compatibility half: a value that is
// already an id must pass through untouched, or every q the MCP server and the
// web app already send would change meaning.
func TestResolveQueryKeepsAUuid(t *testing.T) {
	const raw = "cat:22222222-2222-4222-8222-222222222222"
	got, diags := ResolveQuery(raw, resolveSource())
	if diags != nil {
		t.Fatalf("diagnostics = %+v, want none", diags)
	}
	if got != raw {
		t.Errorf("q = %q, want it unchanged (%q)", got, raw)
	}
}

// TestResolveQueryKeepsTheSentinels covers the two words that stand for an
// absent value. They are not names and must never be looked up.
func TestResolveQueryKeepsTheSentinels(t *testing.T) {
	got, _ := ResolveQuery(`cat:uncategorized payee:none`, resolveSource())
	if got != "cat:uncategorized payee:none" {
		t.Errorf("q = %q, want the sentinels untouched", got)
	}
}

// TestResolveQueryDropsANameThatMatchesNothing is the load-bearing behaviour
// and the reason this is a drop-and-report rather than a refuse. A name passed
// on as a literal would bind the string against a uuid column server-side and
// come back as an empty ledger; the server would drop it and only it could say
// why. The same rule the web app applies at resolve.ts:147.
func TestResolveQueryDropsANameThatMatchesNothing(t *testing.T) {
	got, diags := ResolveQuery(`cat:Nonexistent`, resolveSource())
	if got != "" {
		t.Errorf("q = %q, want the unresolvable term dropped entirely", got)
	}
	if len(diags) != 1 {
		t.Fatalf("got %d diagnostics, want 1", len(diags))
	}
	if diags[0].Code != "unresolved_value" {
		t.Errorf("code = %q, want unresolved_value", diags[0].Code)
	}
	if diags[0].Message == "" {
		t.Error("the message is empty, so the user is told nothing")
	}
}

// TestResolveQueryKeepsAnAmbiguousNameAndSaysSo pins the other half. Narrowing
// to one of two "Groceries" would be a guess, and the user would read the
// narrower result as the answer; keeping both widens it visibly instead.
func TestResolveQueryKeepsAnAmbiguousNameAndSaysSo(t *testing.T) {
	got, diags := ResolveQuery(`cat:Groceries`, resolveSource())
	if got != "cat:c1,c3" {
		t.Errorf("q = %q, want both ids kept", got)
	}
	if len(diags) != 1 {
		t.Fatalf("got %d diagnostics, want 1 naming the ambiguity", len(diags))
	}
	if diags[0].Code != "ambiguous_value" {
		t.Errorf("code = %q, want ambiguous_value", diags[0].Code)
	}
}

// TestResolveQueryDisambiguatesACategoryByGroup is the Group/Name spelling the
// autocomplete inserts, and the only fix-it advice the ambiguity message offers
// for cat.
func TestResolveQueryDisambiguatesACategoryByGroup(t *testing.T) {
	got, diags := ResolveQuery(`cat:Income/Groceries`, resolveSource())
	if diags != nil {
		t.Fatalf("diagnostics = %+v, want none", diags)
	}
	if got != "cat:c3" {
		t.Errorf("q = %q, want cat:c3 (the Income one)", got)
	}
}

// TestResolveQueryResolvesTheOtherThreeIDFields checks the remaining fields
// resolve the same way, so the TUI's grammar is not half-usable.
func TestResolveQueryResolvesTheOtherThreeIDFields(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{`group:Expense`, "group:g1"},
		{`acct:Cash`, "acct:a2"},
		{`payee:"Whole Foods"`, "payee:p1"},
	} {
		got, diags := ResolveQuery(tc.in, resolveSource())
		if diags != nil {
			t.Errorf("%s: diagnostics = %+v, want none", tc.in, diags)
			continue
		}
		if got != tc.want {
			t.Errorf("%s resolved to %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestResolveQueryLeavesNonIDTermsAlone is the negative case that keeps the
// resolver from touching what it has no business touching: every other field is
// already in its final wire form, including the amount, which is deliberately
// left in major units for the server to convert.
func TestResolveQueryLeavesNonIDTermsAlone(t *testing.T) {
	for _, raw := range []string{
		`amt>50.75`, `date>=2026-01-01`, `tag:vacation`, `ccy:usd`,
		`type:debit`, `linked:true`, `coffee`, `not cat:`,
	} {
		got, _ := ResolveQuery(raw, resolveSource())
		if got != raw {
			t.Errorf("%s became %q, want it unchanged", raw, got)
		}
	}
}

// TestResolveQueryResolvesOnlyTheTermsThatNeedIt guards the mixed case: one
// good name and one bad name in the same expression must not lose the good one.
func TestResolveQueryResolvesOnlyTheTermsThatNeedIt(t *testing.T) {
	got, diags := ResolveQuery(`cat:Nonexistent payee:"Whole Foods" amt>10`, resolveSource())
	if got != "payee:p1 amt>10" {
		t.Errorf("q = %q, want the resolvable terms kept", got)
	}
	if len(diags) != 1 {
		t.Errorf("got %d diagnostics, want 1", len(diags))
	}
}
