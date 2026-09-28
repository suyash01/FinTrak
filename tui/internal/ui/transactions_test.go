package ui

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/fintrak/client/api"
)

// filterTestCtx is a context for the filter-form tests, carrying one loan account
// so the Loan select has an option to move to. testCtx's two accounts are a bank
// and nothing else, so LoanAccountOptions would be empty and the field untestable.
// AccountsByID is populated the way the reference load populates it, because the
// header line resolves an account id to its name through it and would otherwise
// print the raw id.
func filterTestCtx() *Ctx {
	accounts := []api.Account{
		{ID: "a1", Name: "Everyday", AccountTypeID: "bank", AccountTypeName: "Bank"},
		{ID: "loan1", Name: "Car loan", AccountTypeID: "loan", AccountTypeName: "Loan"},
	}
	byID := make(map[string]api.Account, len(accounts))
	for _, a := range accounts {
		byID[a.ID] = a
	}
	return &Ctx{
		Ref:         &RefData{Accounts: accounts, AccountsByID: byID},
		Theme:       DefaultTheme(),
		Notify:      func(Level, string, ...any) {},
		Open:        func(Modal) {},
	}
}

// filterForm opens the transaction filter form and returns it with the screen, so
// a test can drive the real form the f key opens.
func filterForm(t *testing.T, s *Transactions) *Form {
	t.Helper()
	var opened Modal
	s.ctx.Open = func(m Modal) { opened = m }
	s.openFilterForm()
	form, ok := opened.(*Form)
	if !ok {
		t.Fatalf("the filter key opened a %T, want a *Form", opened)
	}
	return form
}

// focusField moves a form's focus onto the named field of the given kind, by
// label rather than by position, so a field inserted above it does not silently
// turn a test into a test of some other field.
func focusField(t *testing.T, f *Form, kind FieldKind, label string) {
	t.Helper()
	for range len(f.fields) + 1 {
		if f.fields[f.index].Kind == kind && f.fields[f.index].Label == label {
			return
		}
		f.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	}
	t.Fatalf("the form has no %v field labelled %q", kind, label)
}

// selectValue drives a select field to the named value, cycling as many steps as
// that takes rather than assuming the option's position — the order of a
// picker's entries is not something a test should depend on, and one wrong
// Right press would otherwise assert against a value the field never held.
func selectValue(t *testing.T, f *Form, label, want string) {
	t.Helper()
	focusField(t, f, FieldSelect, label)
	for range len(f.fields) + 2 {
		if f.Value(label) == want {
			return
		}
		f.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	}
	t.Fatalf("the %q select never offered %q (it holds %q)", label, want, f.Value(label))
}

// toggleBool flips a yes/no field and checks it landed, so a caller can assert
// on the screen's filter afterwards knowing the form was actually set.
func toggleBool(t *testing.T, f *Form, label string, want bool) {
	t.Helper()
	focusField(t, f, FieldBool, label)
	if f.BoolValue(label) == want {
		t.Fatalf("the %q field is already %t; the test cannot tell a set from an unset one", label, want)
	}
	f.Update(tea.KeyPressMsg{Code: tea.KeySpace})
	if f.BoolValue(label) != want {
		t.Fatalf("the %q field did not toggle to %t", label, want)
	}
}

// run executes a command and feeds its message back to the screen, so a test can
// drive the real load/mutate cycle the App would drive.
func run(t *testing.T, s Screen, cmd tea.Cmd) {
	t.Helper()
	if cmd == nil {
		return
	}
	msg := cmd()
	if msg == nil {
		return
	}
	if next := s.Update(msg); next != nil {
		if follow := next(); follow != nil {
			s.Update(follow)
		}
	}
}

// TestTransactionsReloadsAfterAWrite is a regression test for the reported bug:
// editing a transaction left the list showing its old values, because the
// screen's done handler cleared the selection and never refetched. The stub
// server returns one row before the write and two after, so only a real reload
// can produce two.
func TestTransactionsReloadsAfterAWrite(t *testing.T) {
	var gets int32
	row := func(id, description string) string {
		return fmt.Sprintf(`{"id":%q,"accountId":"acct-1","date":"2026-09-01T00:00:00Z",`+
			`"description":%q,"amount":10.5,"type":"debit","tags":[]}`, id, description)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/transactions" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		n := atomic.AddInt32(&gets, 1)
		body := "[" + row("t1", "one") + "]"
		if n > 1 {
			body = "[" + row("t1", "one") + "," + row("t2", "two") + "]"
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"data":%s,"total":%d,"page":1,"limit":50,"pages":1}`, body, n)
	}))
	defer srv.Close()

	client, err := api.New(srv.URL + "/api/v1")
	if err != nil {
		t.Fatalf("api.New: %v", err)
	}
	ctx := &Ctx{Client: client, Ref: &RefData{}, Theme: DefaultTheme(), Notify: func(Level, string, ...any) {}}
	screen := NewTransactions(ctx)

	run(t, screen, screen.Refresh())
	if len(screen.rows) != 1 {
		t.Fatalf("initial rows = %d, want 1", len(screen.rows))
	}

	run(t, screen, screen.Update(done{tag: "txn.save", note: "transaction updated"}))

	if len(screen.rows) != 2 {
		t.Errorf("after a successful write the list holds %d rows, want 2 — it did not reload", len(screen.rows))
	}
}

// TestTransactionsKeepsTheListOnAFailedWrite makes sure a rejected save does not
// discard what the screen already had.
func TestTransactionsKeepsTheListOnAFailedWrite(t *testing.T) {
	var gets int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&gets, 1)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data":[{"id":"t1","accountId":"a","date":"2026-09-01T00:00:00Z","description":"one","amount":1,"type":"debit","tags":[]}],"total":1,"page":1,"limit":50,"pages":1}`)
	}))
	defer srv.Close()

	client, err := api.New(srv.URL + "/api/v1")
	if err != nil {
		t.Fatalf("api.New: %v", err)
	}
	ctx := &Ctx{Client: client, Ref: &RefData{}, Theme: DefaultTheme(), Notify: func(Level, string, ...any) {}}
	screen := NewTransactions(ctx)
	run(t, screen, screen.Refresh())

	before := atomic.LoadInt32(&gets)
	run(t, screen, screen.Update(done{tag: "txn.save", err: errors.New("amount: must be positive")}))

	if len(screen.rows) != 1 {
		t.Errorf("a failed write changed the list: %d rows", len(screen.rows))
	}
	if atomic.LoadInt32(&gets) != before {
		t.Error("a failed write should not refetch")
	}
}

// TestTransactionsDropsASupersededPage is a regression test for a race: the list
// load carried only its tag, so two overlapping loads were indistinguishable and
// whichever answered last won — including an older page overwriting the newer one
// that had already landed. The header, the rows and the next page request then
// described different queries.
func TestTransactionsDropsASupersededPage(t *testing.T) {
	var gets int32
	row := func(id, description string) string {
		return fmt.Sprintf(`{"id":%q,"accountId":"acct-1","date":"2026-09-01T00:00:00Z",`+
			`"description":%q,"amount":10.5,"type":"debit","tags":[]}`, id, description)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// The first request is answered with two rows and the second with one, so
		// the test can tell which response the screen ends up showing: they are
		// executed out of order, the newer request answering first.
		n := atomic.AddInt32(&gets, 1)
		body, total := "["+row("t1", "one")+","+row("t2", "two")+"]", 2
		if n > 1 {
			body, total = "["+row("t3", "three")+"]", 1
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"data":%s,"total":%d,"page":1,"limit":50,"pages":1}`, body, total)
	}))
	defer srv.Close()

	client, err := api.New(srv.URL + "/api/v1")
	if err != nil {
		t.Fatalf("api.New: %v", err)
	}
	ctx := &Ctx{Client: client, Ref: &RefData{}, Theme: DefaultTheme(), Notify: func(Level, string, ...any) {}}
	screen := NewTransactions(ctx)

	superseded := screen.Refresh()
	current := screen.Refresh()

	// The newer request answers first...
	run(t, screen, current)
	if len(screen.rows) != 2 {
		t.Fatalf("rows = %d, want the 2 the newer request asked for", len(screen.rows))
	}
	// ...and the request it superseded must not overwrite it when it lands late.
	run(t, screen, superseded)
	if len(screen.rows) != 2 {
		t.Errorf("a superseded page overwrote the current one: %d rows, want 2", len(screen.rows))
	}
	if screen.info.Total != 2 {
		t.Errorf("the paging was taken from a superseded page: total = %d, want 2", screen.info.Total)
	}
}

// TestTransactionsFilterOffersTheFilingFilters is the presence half of #39: the
// terminal client could not ask the questions the API answers, and these four are
// the ones it had no control for at all. Asserted per field and by kind, because a
// label on a field of the wrong kind would still satisfy a substring check.
func TestTransactionsFilterOffersTheFilingFilters(t *testing.T) {
	screen := NewTransactions(filterTestCtx())
	form := filterForm(t, screen)

	want := []struct {
		label string
		kind  FieldKind
	}{
		{"Loan", FieldSelect},
		{"Recurring", FieldSelect},
		{"Uncategorized", FieldBool},
		{"Exclude attached", FieldBool},
	}
	for _, w := range want {
		found := false
		for _, field := range form.fields {
			if field.Label == w.label {
				found = true
				if field.Kind != w.kind {
					t.Errorf("the %q field is a %v, want a %v", w.label, field.Kind, w.kind)
				}
			}
		}
		if !found {
			t.Errorf("the filter form has no %q field", w.label)
		}
	}
}

// TestTransactionsFilterCarriesTheFilingFiltersToTheRequest is the half that
// matters. Adding a control to the form is easy and worthless on its own: the
// submit closure rebuilds api.TransactionFilter field by field, so a field added
// to the form and forgotten there is dropped on every apply — the user sets it,
// confirms, and the list comes back unfiltered with no error anywhere. That is
// the same silent-wrong-answer shape as the bug this issue is about, so the
// assertion is on the screen's filter, not on the form's contents.
func TestTransactionsFilterCarriesTheFilingFiltersToTheRequest(t *testing.T) {
	screen := NewTransactions(filterTestCtx())
	form := filterForm(t, screen)

	selectValue(t, form, "Loan", "loan1")
	selectValue(t, form, "Recurring", "unlinked")
	toggleBool(t, form, "Uncategorized", true)
	toggleBool(t, form, "Exclude attached", true)

	// ctrl+s submits. The closure returns a reload command that the test does not
	// run, so no request is made — what is under test is the filter the screen
	// now holds, which is what any next page or export would be built from.
	form.Update(ctrlPress('s'))

	f := screen.filter
	if f.LoanAccountID != "loan1" {
		t.Errorf("loanAccountId = %q, want loan1", f.LoanAccountID)
	}
	if f.Recurring != "unlinked" {
		t.Errorf("recurring = %q, want unlinked", f.Recurring)
	}
	if !f.Uncategorized {
		t.Error("uncategorized = false, want true")
	}
	if !f.ExcludeAttached {
		t.Error("excludeAttached = false, want true")
	}
}

// TestTransactionsFilterKeepsTheFilingFiltersAcrossAReapply is the failure mode
// the test above cannot see on its own. It sets a value and reads it back in the
// same submit, so a field wired up one way round still passes. Re-opening the
// form pre-fills it from the filter and submitting it unchanged must give the
// same filter back — which fails the moment a field is seeded into the form but
// left out of the reassignment, because the value is dropped on the way through.
func TestTransactionsFilterKeepsTheFilingFiltersAcrossAReapply(t *testing.T) {
	screen := NewTransactions(filterTestCtx())
	screen.filter = api.TransactionFilter{
		Limit:           50,
		SortBy:          "date",
		SortOrder:       "DESC",
		LoanAccountID:   "loan1",
		Recurring:       "linked",
		Uncategorized:   true,
		ExcludeAttached: true,
	}

	form := filterForm(t, screen)
	// The form was seeded from the filter, so a round trip through it unchanged
	// is the identity it has to satisfy.
	if got := form.Value("Loan"); got != "loan1" {
		t.Errorf("the re-opened form shows Loan = %q, want the filter's loan1", got)
	}
	if !form.BoolValue("Uncategorized") {
		t.Error("the re-opened form shows Uncategorized as false, want the filter's true")
	}

	form.Update(ctrlPress('s'))

	f := screen.filter
	if f.LoanAccountID != "loan1" {
		t.Errorf("loanAccountId = %q after an unchanged re-apply, want loan1", f.LoanAccountID)
	}
	if f.Recurring != "linked" {
		t.Errorf("recurring = %q after an unchanged re-apply, want linked", f.Recurring)
	}
	if !f.Uncategorized || !f.ExcludeAttached {
		t.Errorf("the yes/no filters were lost across an unchanged re-apply: uncategorized=%t excludeAttached=%t",
			f.Uncategorized, f.ExcludeAttached)
	}
}

// TestTransactionsFilterNamesTheFilingFilters is why the header line is in scope.
// A filter the user set and cannot see is the defect in a different place: the
// form closes, the rows change, and nothing on the list says which of four new
// controls is doing the narrowing.
func TestTransactionsFilterNamesTheFilingFilters(t *testing.T) {
	screen := NewTransactions(filterTestCtx())
	screen.filter = api.TransactionFilter{
		LoanAccountID:   "loan1",
		Recurring:       "unlinked",
		Uncategorized:   true,
		ExcludeAttached: true,
	}

	line := screen.headerLine(200)
	for _, want := range []string{"loan=Car loan", "recurring=unlinked", "uncategorized", "not attached"} {
		if !strings.Contains(line, want) {
			t.Errorf("the header line %q does not name %q", line, want)
		}
	}
}

// TestTransactionsFilterSaysNothingWhenTheFilingFiltersAreOff guards the other
// direction: an unset filter must not print empty or placeholder tokens, or the
// header is noise on every list the user ever opens.
func TestTransactionsFilterSaysNothingWhenTheFilingFiltersAreOff(t *testing.T) {
	screen := NewTransactions(filterTestCtx())
	line := screen.headerLine(200)
	for _, unwanted := range []string{"loan=", "recurring=", "uncategorized", "not attached"} {
		if strings.Contains(line, unwanted) {
			t.Errorf("the header line %q mentions %q with no such filter set", line, unwanted)
		}
	}
}
