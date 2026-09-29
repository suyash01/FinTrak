package ui

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/fintrak/client/api"
)

// queryTestCtx is filterTestCtx with the reference data a `q` name has to resolve
// against, which is the whole reason the expression can be offered at all.
func queryTestCtx() *Ctx {
	ctx := filterTestCtx()
	ctx.Ref.Groups = []api.CategoryGroup{{ID: "g1", Name: "Expense"}}
	ctx.Ref.Categories = []api.Category{
		{ID: "c1", Name: "Groceries", GroupID: "g1"},
	}
	ctx.Ref.Payees = []api.Payee{{ID: "p1", Name: "Whole Foods"}}
	return ctx
}

// typeText focuses a text field and types into it, so the assertion is on what
// the form holds rather than on the key presses that got it there.
func typeText(t *testing.T, f *Form, label, text string) {
	t.Helper()
	focusField(t, f, FieldText, label)
	f.Update(tea.KeyPressMsg{Code: tea.KeyBackspace})
	for _, r := range text {
		f.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
}

// TestTransactionsFilterOffersTheQueryField is the presence half of #39's
// remaining scope: the eight named filters landed, and `q` is the one the API
// already answers that the terminal still could not ask.
func TestTransactionsFilterOffersTheQueryField(t *testing.T) {
	screen := NewTransactions(queryTestCtx())
	form := filterForm(t, screen)

	found := false
	for _, field := range form.fields {
		if field.Label == "Query" {
			found = true
			if field.Kind != FieldText {
				t.Errorf("the Query field is a %v, want a %v", field.Kind, FieldText)
			}
			if field.Help == "" {
				t.Error("the Query field has no help text, so the grammar is undiscoverable")
			}
		}
	}
	if !found {
		t.Error("the filter form has no Query field")
	}
}

// TestTransactionsFilterCarriesTheQueryToTheRequest is the half that matters, for
// the same reason the filing-filter test exists: a field added to the form and
// forgotten in the submit closure is dropped on every apply, and the user sets it,
// confirms, and gets an unfiltered list with no error anywhere. The value that
// reaches the filter is the resolved one, which is what the next page or an
// export would be built from.
func TestTransactionsFilterCarriesTheQueryToTheRequest(t *testing.T) {
	screen := NewTransactions(queryTestCtx())
	form := filterForm(t, screen)

	typeText(t, form, "Query", "cat:Groceries")
	form.Update(ctrlPress('s'))

	if got := screen.filter.Query; got != "cat:c1" {
		t.Errorf("query = %q, want the resolved cat:c1", got)
	}
}

// TestTransactionsFilterResolvesNamesBeforeSending is the reason the field is
// worth having. The server resolves no names, so a name left on the wire comes
// back as an empty ledger; the screen resolves it against the reference data it
// already holds before the request is built.
func TestTransactionsFilterResolvesNamesBeforeSending(t *testing.T) {
	screen := NewTransactions(queryTestCtx())
	form := filterForm(t, screen)

	typeText(t, form, "Query", "cat:Groceries")
	form.Update(ctrlPress('s'))

	if got := screen.filter.Query; got != "cat:c1" {
		t.Errorf("query = %q, want the resolved cat:c1", got)
	}
}

// TestTransactionsFilterKeepsTheQueryAcrossAReapply is the failure mode the
// presence test cannot see: re-opening the form pre-fills from what the user
// typed and submitting it unchanged must give the same expression back. A form
// pre-filled from the resolved filter instead would show "cat:c1" where the user
// wrote "cat:Groceries", and re-resolving an id is a second lookup that can
// come back empty — the filter would silently widen to no constraint at all.
func TestTransactionsFilterKeepsTheQueryAcrossAReapply(t *testing.T) {
	screen := NewTransactions(queryTestCtx())
	form := filterForm(t, screen)

	typeText(t, form, "Query", "cat:Groceries")
	form.Update(ctrlPress('s'))
	first := screen.filter.Query

	form2 := filterForm(t, screen)
	if got := form2.Value("Query"); got != "cat:Groceries" {
		t.Errorf("the re-opened form pre-filled %q, want what the user typed (%q)", got, "cat:Groceries")
	}
	form2.Update(ctrlPress('s'))
	if got := screen.filter.Query; got != first {
		t.Errorf("re-applying gave %q, want %q", got, first)
	}
}

// TestTransactionsFilterKeepsAUuidQuery pins the compatibility half: an
// expression that is already in wire form must survive untouched, or a query
// saved from the web app or written by hand against a uuid would change meaning
// the moment it was retyped here.
func TestTransactionsFilterKeepsAUuidQuery(t *testing.T) {
	screen := NewTransactions(queryTestCtx())
	form := filterForm(t, screen)

	const raw = "cat:22222222-2222-4222-8222-222222222222 amt>50"
	typeText(t, form, "Query", raw)
	form.Update(ctrlPress('s'))

	if got := screen.filter.Query; got != raw {
		t.Errorf("query = %q, want it unchanged (%q)", got, raw)
	}
}

// TestTransactionsFilterDropsAnUnresolvableNameAndSaysSo covers the case that
// decides whether the field is safe to offer at all. A name matching nothing is
// dropped rather than sent as a literal — which would bind a string against a
// uuid column and return an empty ledger — and the user is told, because a
// dropped constraint widens the results and silence would read as the answer.
func TestTransactionsFilterDropsAnUnresolvableNameAndSaysSo(t *testing.T) {
	ctx := queryTestCtx()
	var notices []string
	ctx.Notify = func(_ Level, msg string, _ ...any) { notices = append(notices, msg) }

	screen := NewTransactions(ctx)
	form := filterForm(t, screen)

	typeText(t, form, "Query", "cat:Nonexistent amt>10")
	form.Update(ctrlPress('s'))

	if got := screen.filter.Query; got != "amt>10" {
		t.Errorf("query = %q, want the unresolvable term dropped", got)
	}
	if len(screen.localQueryDiags) == 0 {
		t.Error("nothing was reported, so the user cannot tell a term was dropped")
	}
	if screen.localQueryDiags[0].Code != "unresolved_value" {
		t.Errorf("code = %q, want unresolved_value", screen.localQueryDiags[0].Code)
	}
}
