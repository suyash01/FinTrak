// Package ui implements the FinTrak terminal client: a bubbletea program that
// talks to the same REST API as the web frontend.
//
// Structure: App is the root model. It owns the session, the shared
// reference-data cache, the sidebar navigation and any open modal, and it
// forwards messages to the screens. A Screen is one navigable view; screens
// mutate in place (the App keeps a stable pointer per screen) so their cursors,
// filters and loaded rows survive tab switches.
//
// Async work follows one pattern: a screen returns a tea.Cmd built by load or
// act, and matches the resulting loaded[T] / done message on its own tag. Tags
// are per-screen string constants such as "txn.list", so a slow response for a
// screen the user has navigated away from can never be mistaken for someone
// else's data.
package ui

import (
	"context"
	"sort"
	"strings"
	"sync"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/fintrak/client/api"
)

// Screen is one navigable view.
type Screen interface {
	// Title is the sidebar label.
	Title() string
	// Refresh returns a command that reloads the screen's data. The App issues
	// it on first activation, on `r`, and after a mutation reports invalidate.
	Refresh() tea.Cmd
	// Update handles one message and returns an optional command. Screens
	// ignore messages carrying another screen's tag.
	Update(tea.Msg) tea.Cmd
	// View renders the screen into a content area of the given size.
	View(w, h int) string
	// Keys lists the screen's bindings for the status bar and help overlay.
	Keys() []key.Binding
	// CapturesText reports whether the screen is currently reading raw text from
	// the keyboard — an inline search or filter box. The App skips its global
	// bindings while it does, so a character the user types reaches the input
	// instead of navigating: `r` refreshes the screen, `g` opens the go-to
	// picker, `[`/`]` and the digits jump between screens, and none of them may
	// steal a search term.
	CapturesText() bool
}

// Level classifies a status-line message.
type Level int

// Status levels, in increasing severity.
const (
	LevelInfo Level = iota
	LevelSuccess
	LevelError
)

// Ctx is everything a screen may reach: the API client, the shared
// reference-data cache, and the App's hooks. It is handed to each screen once at
// construction.
type Ctx struct {
	Client *api.Client
	// Ref is the shared, read-mostly cache of accounts, account types,
	// categories, groups, payees and tags. The App refreshes it after any
	// mutation a screen marks as invalidating.
	Ref *RefData
	// Notify writes a transient message to the status line.
	Notify func(level Level, format string, args ...any)
	// Open shows a modal overlay (form, confirm, picker, info). The App owns
	// it and closes it once it reports itself closed.
	Open func(m Modal)
	// User is the signed-in user; Role is "admin" for operators, which is what
	// gates the admin catalog screen.
	User api.User
	// Theme is the palette and styles every screen renders with.
	Theme Theme
}

// loaded carries the result of one API call. Tag scopes it to the screen and
// widget that asked.
type loaded[T any] struct {
	tag  string
	data T
	err  error
}

// done carries the result of a mutation. Invalidate asks the App to refresh the
// shared reference data (set it after creating an account, category, payee, or
// renaming a tag).
type done struct {
	tag        string
	note       string
	err        error
	invalidate bool
}

// load runs fn off the main loop and delivers its result as loaded[T].
func load[T any](tag string, fn func(context.Context) (T, error)) tea.Cmd {
	return func() tea.Msg {
		data, err := fn(context.Background())
		return loaded[T]{tag: tag, data: data, err: err}
	}
}

// act runs a mutation off the main loop and delivers done. A note is shown on
// success; the error is shown instead when the call fails.
func act(tag, note string, invalidate bool, fn func(context.Context) error) tea.Cmd {
	return func() tea.Msg {
		err := fn(context.Background())
		return done{tag: tag, note: note, err: err, invalidate: invalidate}
	}
}

// RefData is the shared lookup cache. It is only mutated on the event loop, so
// screens may read it without locking.
type RefData struct {
	// Me is the authenticated user, resolved from the API. The SSH door starts a
	// session with tokens already in hand, so the client cannot take the user
	// from a sign-in form.
	Me           api.User
	Accounts     []api.Account
	AccountTypes []api.AccountType
	Groups       []api.CategoryGroup
	Categories   []api.Category
	Payees       []api.Payee
	Tags         []api.TagCount

	// Recurring is OPTIONAL reference data: it backs the transaction filter's
	// series picker and nothing else, so a failure to fetch it degrades one
	// dropdown rather than the session. RecurringErr is the reason it is empty,
	// and it is kept precisely so that "no series exist" and "the series could not
	// be loaded" are not the same silence - see recurringOptions.
	Recurring    []api.RecurringSeries
	RecurringErr error

	AccountsByID    map[string]api.Account
	AccountTypeByID map[string]api.AccountType
	GroupsByID      map[string]api.CategoryGroup
	CategoriesByID  map[string]api.Category
	PayeesByID      map[string]api.Payee
}

// emptyRefData reports whether the cache has never been loaded, so a screen can
// tell "no accounts exist" apart from "not fetched yet".
func (r *RefData) Loaded() bool { return r != nil && r.AccountsByID != nil }

// Account returns the account with the given id.
func (r *RefData) Account(id string) (api.Account, bool) {
	a, ok := r.AccountsByID[id]
	return a, ok
}

// AccountName resolves an id for display, falling back to a dash so a deleted
// or filtered-out row still renders.
func (r *RefData) AccountName(id string) string {
	if a, ok := r.AccountsByID[id]; ok {
		return a.Name
	}
	if id == "" {
		return "—"
	}
	return id
}

// RecurringName resolves a recurring series id for display, falling back to the
// id when it is not among the loaded series - which is also what happens when the
// optional load failed, so the summary names an id rather than a wrong name.
func (r *RefData) RecurringName(id string) string {
	if id == "" {
		return "—"
	}
	for _, s := range r.Recurring {
		if s.ID == id {
			return s.Name
		}
	}
	return id
}

// CategoryName resolves a category id for display.
func (r *RefData) CategoryName(id string) string {
	if id == "" {
		return "Uncategorized"
	}
	if c, ok := r.CategoriesByID[id]; ok {
		return c.Name
	}
	return id
}

// PayeeName resolves a payee id for display.
func (r *RefData) PayeeName(id string) string {
	if id == "" {
		return "—"
	}
	if p, ok := r.PayeesByID[id]; ok {
		return p.Name
	}
	return id
}

// GroupName resolves a category group id for display.
func (r *RefData) GroupName(id string) string {
	if g, ok := r.GroupsByID[id]; ok {
		return g.Name
	}
	return id
}

// LoanAccounts returns the loan/EMI accounts, which are the valid targets of a
// loan attachment and the only accounts with an amortization schedule.
func (r *RefData) LoanAccounts() []api.Account {
	out := make([]api.Account, 0, len(r.Accounts))
	for _, a := range r.Accounts {
		if a.AccountTypeID == "loan" {
			out = append(out, a)
		}
	}
	return out
}

// LoanAccountOptions returns picker options for the loan/EMI accounts only, by
// bare name — a loan is already identified as one by the field asking for it, so
// the account-type suffix AccountOptions appends would be noise here.
//
// It exists because two callers need this list and neither is AccountOptions: the
// bulk-attach form, and the transaction filter's loanAccountId. An empty result
// is a legitimate answer (a user with no loans), so it is the caller's job to
// decide whether that is worth reporting.
func (r *RefData) LoanAccountOptions() []Option {
	loans := r.LoanAccounts()
	out := make([]Option, 0, len(loans))
	for _, loan := range loans {
		out = append(out, Option{Value: loan.ID, Label: loan.Name})
	}
	return out
}

// RecurringSeriesField builds the transaction filter's series picker, with the
// help line that explains an empty one.
//
// The help is not decoration. An empty picker is ambiguous on its own: a user
// with no series and a user whose series failed to load both produce zero
// options, and telling them the same thing is the silent-wrong-answer shape this
// codebase keeps guarding against. So a failure is reported where the person
// editing the filter will read it, rather than left to be inferred from a list
// that is simply not there.
//
// The alternative — letting a series failure fail the whole reference load — is
// worse: it stops the TUI starting at all, because every screen is created after
// the reference load. A filter that cannot narrow by series is a smaller loss than
// a client that will not open.
func (r *RefData) RecurringSeriesField(current string) Field {
	field := SelectField("Series", current, nil, false)
	switch {
	case r.RecurringErr != nil:
		field.Help = "could not load your series: " + r.RecurringErr.Error()
	case len(r.Recurring) == 0:
		field.Help = "you have no recurring series"
	default:
		field.Options = make([]Option, 0, len(r.Recurring))
		for _, s := range r.Recurring {
			field.Options = append(field.Options, Option{Value: s.ID, Label: s.Name})
		}
	}
	return field
}

// AccountOptions returns picker options for every account, with the loan, bank
// and credit-card names differentiated.
func (r *RefData) AccountOptions() []Option {
	out := make([]Option, 0, len(r.Accounts))
	for _, a := range r.Accounts {
		label := a.Name
		if a.AccountTypeName != "" {
			label += " (" + a.AccountTypeName + ")"
		}
		if a.Closed {
			label += " · closed"
		}
		out = append(out, Option{Value: a.ID, Label: label})
	}
	return out
}

// CurrencyOptions returns picker options for every currency the user's accounts
// hold, in code order, with the unfiltered entry a currency selection needs.
// A screen cannot know which currencies a *window* spans before it asks, so the
// picker is built from the accounts it already holds; the response's own
// currencyScope is what reports a window that turned out to span more, and that
// is what currencyScopeLabel prints beside the selection.
func (r *RefData) CurrencyOptions() []Option {
	seen := map[string]bool{}
	codes := make([]string, 0, len(r.Accounts))
	for _, a := range r.Accounts {
		code := strings.ToUpper(defaultTo(a.Currency, ""))
		if code == "" || seen[code] {
			continue
		}
		seen[code] = true
		codes = append(codes, code)
	}
	sort.Strings(codes)

	out := make([]Option, 0, len(codes))
	for _, code := range codes {
		out = append(out, Option{Value: code, Label: code})
	}
	return out
}

// CategoryOptions returns picker options for every visible category, each
// carrying its group so a picker renders them under group headings. The API
// returns categories in group order, so the headings appear once per group.
func (r *RefData) CategoryOptions() []Option {
	out := make([]Option, 0, len(r.Categories))
	for _, c := range r.Categories {
		out = append(out, Option{
			Value: c.ID,
			Label: c.Name,
			Group: defaultTo(c.GroupName, r.GroupName(c.GroupID)),
		})
	}
	return out
}

// PayeeOptions returns picker options for every payee.
func (r *RefData) PayeeOptions() []Option {
	out := make([]Option, 0, len(r.Payees))
	for _, p := range r.Payees {
		out = append(out, Option{Value: p.ID, Label: p.Name})
	}
	return out
}

// GroupOptions returns picker options for the category groups a user may file a
// category under.
func (r *RefData) GroupOptions() []Option {
	out := make([]Option, 0, len(r.Groups))
	for _, g := range r.Groups {
		label := g.Name
		if g.IsBase {
			label += " · base"
		}
		out = append(out, Option{Value: g.ID, Label: label})
	}
	return out
}

// AccountTypeOptions returns picker options for the account types.
func (r *RefData) AccountTypeOptions() []Option {
	out := make([]Option, 0, len(r.AccountTypes))
	for _, t := range r.AccountTypes {
		out = append(out, Option{Value: t.ID, Label: t.Name})
	}
	return out
}

// TagOptions returns picker options for the user's tag vocabulary.
func (r *RefData) TagOptions() []Option {
	out := make([]Option, 0, len(r.Tags))
	for _, t := range r.Tags {
		out = append(out, Option{Value: t.Name, Label: t.Name})
	}
	return out
}

// fetchRefData loads every reference table. The calls are independent, so they
// run concurrently; the first error is reported and the rest are dropped.
//
// Recurring series are loaded here too, but through a second sink: they back one
// picker, so a failure must not fail the whole load and stop the TUI starting.
// The reason is kept on the RefData rather than discarded, because a silently
// empty picker is indistinguishable from a user with no series.
func fetchRefData(ctx context.Context, c *api.Client) (*RefData, error) {
	var (
		wg           sync.WaitGroup
		ref          = &RefData{}
		mu           sync.Mutex
		fail         error
		optionalFail error
	)

	record := func(err error) {
		if err == nil {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		if fail == nil {
			fail = err
		}
	}

	// recordOptional records a failure that must NOT abort the load. It does not
	// overwrite an earlier one, for the same reason record keeps the first: the
	// report names one cause, and the first is the one that happened.
	recordOptional := func(err error) {
		if err == nil {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		if optionalFail == nil {
			optionalFail = err
		}
	}

	wg.Add(8)
	go func() { defer wg.Done(); v, err := c.Me(ctx); record(err); ref.Me = v }()
	go func() { defer wg.Done(); v, err := c.ListAccounts(ctx); record(err); ref.Accounts = v }()
	go func() { defer wg.Done(); v, err := c.ListAccountTypes(ctx); record(err); ref.AccountTypes = v }()
	go func() { defer wg.Done(); v, err := c.ListGroups(ctx); record(err); ref.Groups = v }()
	go func() { defer wg.Done(); v, err := c.ListCategories(ctx); record(err); ref.Categories = v }()
	go func() { defer wg.Done(); v, err := c.ListPayees(ctx); record(err); ref.Payees = v }()
	go func() { defer wg.Done(); v, err := c.ListTags(ctx); record(err); ref.Tags = v }()
	go func() {
		defer wg.Done()
		v, err := c.ListRecurring(ctx)
		recordOptional(err)
		ref.Recurring = v
	}()
	wg.Wait()

	if fail != nil {
		return nil, fail
	}
	ref.RecurringErr = optionalFail

	ref.AccountsByID = make(map[string]api.Account, len(ref.Accounts))
	for _, a := range ref.Accounts {
		ref.AccountsByID[a.ID] = a
	}
	ref.AccountTypeByID = make(map[string]api.AccountType, len(ref.AccountTypes))
	for _, t := range ref.AccountTypes {
		ref.AccountTypeByID[t.ID] = t
	}
	ref.GroupsByID = make(map[string]api.CategoryGroup, len(ref.Groups))
	for _, g := range ref.Groups {
		ref.GroupsByID[g.ID] = g
	}
	ref.CategoriesByID = make(map[string]api.Category, len(ref.Categories))
	for _, cat := range ref.Categories {
		ref.CategoriesByID[cat.ID] = cat
	}
	ref.PayeesByID = make(map[string]api.Payee, len(ref.Payees))
	for _, p := range ref.Payees {
		ref.PayeesByID[p.ID] = p
	}
	return ref, nil
}
