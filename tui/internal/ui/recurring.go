package ui

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/fintrak/client/api"
)

func init() {
	registerScreen(90, func(ctx *Ctx) Screen { return NewRecurring(ctx) })
}

// recurPane selects one of the screen's sub-views. Terms, suggestions and the
// attached-transaction list are full-width sub-views rather than App modals
// because each of them is a list the user picks from and then has to come back
// to — the App gives an open modal every key, and a confirmation opened over a
// modal would replace it, dropping the user's place in the list.
type recurPane int

// The screen's sub-views, in the order they are reached.
const (
	recurPaneSeries recurPane = iota
	recurPaneTerms
	recurPaneSuggestions
	recurPaneAttached
)

// Recurring manages recurring series: templates for a repeating charge or
// income.
//
// A series never creates a transaction and never links one by itself. The screen
// therefore offers the three views the API supports around that fact — the
// forecast projects what the template would produce, the suggestion list
// proposes the real transactions that look like an occurrence, and the attached
// list shows which ones the user has actually confirmed. Ranges are never sent:
// the API records an amount or account change on an existing series as a new
// effective-dated range, so the edit form only sends the fields the user really
// changed and lets the server keep the earlier ranges in force.
type Recurring struct {
	ctx   *Ctx
	table Table
	sub   Table

	series      []api.RecurringSeries
	terms       []api.RecurringSeriesTerm
	suggestions []api.RecurringSuggestion
	attached    []api.Transaction

	// pane is the sub-view currently shown; the main table keeps its cursor
	// while a sub-view is open, so returning to the list is free.
	pane recurPane
	// subjectID and subjectName identify the series the open sub-view (or the
	// forecast opened from it) belongs to. They are captured when the sub-view
	// opens so a reload after a mutation still targets that series even if the
	// cursor has moved on.
	subjectID   string
	subjectName string
	// suggestLimit remembers the window the user asked for, so attaching one
	// transaction refreshes the same list rather than resetting the limit.
	suggestLimit int

	keys recurKeys
}

// recurKeys are the screen's bindings, grouped by the sub-view they belong to.
type recurKeys struct {
	New         key.Binding
	Edit        key.Binding
	Delete      key.Binding
	Terms       key.Binding
	Forecast    key.Binding
	Suggestions key.Binding
	Attached    key.Binding

	AddTerm    key.Binding
	EditTerm   key.Binding
	DeleteTerm key.Binding
	Attach     key.Binding
	Detach     key.Binding
	Back       key.Binding
}

func newRecurKeys() recurKeys {
	return recurKeys{
		New:         key.NewBinding(key.WithKeys("n"), key.WithHelp("n", "new series")),
		Edit:        key.NewBinding(key.WithKeys("e"), key.WithHelp("e", "edit")),
		Delete:      key.NewBinding(key.WithKeys("d"), key.WithHelp("d", "delete")),
		Terms:       key.NewBinding(key.WithKeys("t"), key.WithHelp("t", "terms")),
		Forecast:    key.NewBinding(key.WithKeys("f"), key.WithHelp("f", "forecast")),
		Suggestions: key.NewBinding(key.WithKeys("s"), key.WithHelp("s", "suggestions")),
		Attached:    key.NewBinding(key.WithKeys("T"), key.WithHelp("T", "attached")),
		AddTerm:     key.NewBinding(key.WithKeys("a"), key.WithHelp("a", "add term")),
		EditTerm:    key.NewBinding(key.WithKeys("E"), key.WithHelp("E", "edit term")),
		DeleteTerm:  key.NewBinding(key.WithKeys("D"), key.WithHelp("D", "delete term")),
		Attach:      key.NewBinding(key.WithKeys("y"), key.WithHelp("y", "attach transaction")),
		Detach:      key.NewBinding(key.WithKeys("u"), key.WithHelp("u", "detach transaction")),
		Back:        key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "back")),
	}
}

// NewRecurring builds the recurring-series screen.
func NewRecurring(ctx *Ctx) *Recurring {
	r := &Recurring{ctx: ctx, keys: newRecurKeys(), suggestLimit: 100}
	r.table.SetColumns(
		Column{Title: "Name"},
		Column{Title: "Type", Width: 6},
		Column{Title: "Every", Width: 12},
		Column{Title: "Amount", Width: 12, Align: AlignRight},
		Column{Title: "Account", Width: 13},
		Column{Title: "Monthly", Width: 12, Align: AlignRight},
		Column{Title: "Next due", Width: 11},
		Column{Title: "Att", Width: 3, Align: AlignRight},
		Column{Title: "On", Width: 3},
	)
	return r
}

// Title implements Screen.
func (r *Recurring) Title() string { return "Recurring" }

// Keys implements Screen.
func (r *Recurring) Keys() []key.Binding {
	switch r.pane {
	case recurPaneTerms:
		return []key.Binding{r.keys.AddTerm, r.keys.EditTerm, r.keys.DeleteTerm, r.keys.Back}
	case recurPaneSuggestions:
		return []key.Binding{r.keys.Attach, r.keys.Back}
	case recurPaneAttached:
		return []key.Binding{r.keys.Detach, r.keys.Back}
	}
	return []key.Binding{
		r.keys.New, r.keys.Edit, r.keys.Delete, r.keys.Terms,
		r.keys.Forecast, r.keys.Suggestions, r.keys.Attached,
	}
}

// Refresh implements Screen. An open sub-view is reloaded alongside the series
// list, so `r` can never leave a stale pane on screen.
func (r *Recurring) Refresh() tea.Cmd {
	return tea.Batch(r.reloadSeries(), r.reloadPane())
}

// reloadSeries fetches every series with its derived view (next due date,
// normalized monthly amount, attached count).
func (r *Recurring) reloadSeries() tea.Cmd {
	return load("recurring.list", func(ctx context.Context) ([]api.RecurringSeries, error) {
		return r.ctx.Client.ListRecurring(ctx)
	})
}

// reloadPane refetches whatever the open sub-view is showing. It is a no-op for
// the series list, which reloadSeries covers.
func (r *Recurring) reloadPane() tea.Cmd {
	switch r.pane {
	case recurPaneTerms:
		return r.reloadTerms(r.subjectID)
	case recurPaneSuggestions:
		return r.reloadSuggestions(r.subjectID, r.suggestLimit)
	case recurPaneAttached:
		return r.reloadAttached(r.subjectID)
	}
	return nil
}

// reloadTerms fetches the series' effective-dated terms.
func (r *Recurring) reloadTerms(id string) tea.Cmd {
	return load("recurring.terms", func(ctx context.Context) ([]api.RecurringSeriesTerm, error) {
		return r.ctx.Client.ListRecurringTerms(ctx, id)
	})
}

// reloadSuggestions fetches the proposed matches for the series. The call is
// read-only: the server links nothing.
func (r *Recurring) reloadSuggestions(id string, limit int) tea.Cmd {
	return load("recurring.suggestions", func(ctx context.Context) ([]api.RecurringSuggestion, error) {
		return r.ctx.Client.RecurringSuggestions(ctx, id, limit)
	})
}

// reloadAttached fetches the transactions confirmed against the series.
func (r *Recurring) reloadAttached(id string) tea.Cmd {
	return load("recurring.transactions", func(ctx context.Context) ([]api.Transaction, error) {
		return r.ctx.Client.RecurringTransactions(ctx, id)
	})
}

// Update implements Screen.
func (r *Recurring) Update(msg tea.Msg) tea.Cmd {
	switch m := msg.(type) {
	case loaded[[]api.RecurringSeries]:
		if m.tag != "recurring.list" {
			break
		}
		if m.err != nil {
			r.ctx.Notify(LevelError, "%s", m.err)
			return nil
		}
		r.series = m.data
		r.applySeriesRows()
		return nil

	case loaded[[]api.RecurringSeriesTerm]:
		if m.tag != "recurring.terms" {
			break
		}
		if m.err != nil {
			r.ctx.Notify(LevelError, "%s", m.err)
			return nil
		}
		r.terms = m.data
		r.applyTermRows()
		return nil

	case loaded[[]api.RecurringSuggestion]:
		if m.tag != "recurring.suggestions" {
			break
		}
		if m.err != nil {
			r.ctx.Notify(LevelError, "%s", m.err)
			return nil
		}
		r.suggestions = m.data
		r.applySuggestionRows()
		return nil

	case loaded[[]api.Transaction]:
		if m.tag != "recurring.transactions" {
			break
		}
		if m.err != nil {
			r.ctx.Notify(LevelError, "%s", m.err)
			return nil
		}
		r.attached = m.data
		r.applyAttachedRows()
		return nil

	case loaded[[]api.RecurringForecastItem]:
		if m.tag != "recurring.forecast" {
			break
		}
		if m.err != nil {
			r.ctx.Notify(LevelError, "%s", m.err)
			return nil
		}
		r.ctx.Open(NewInfo("Forecast — "+defaultTo(r.subjectName, "series"), recurForecastBody(m.data)).
			WithFooter("matched means an attached transaction already covers that occurrence. Nothing is created and nothing is linked by this view."))
		return nil

	case done:
		if !strings.HasPrefix(m.tag, "recurring.") {
			break
		}
		if m.err != nil {
			// The App already put the failure on the status line. In particular
			// a 409 from a rejected attachment lands here: the transaction
			// already belongs to another series.
			return nil
		}
		if m.tag == "recurring.delete" {
			// The series is gone, so any sub-view about it is meaningless.
			r.pane = recurPaneSeries
			r.subjectID, r.subjectName = "", ""
			return r.reloadSeries()
		}
		return tea.Batch(r.reloadSeries(), r.reloadPane())

	case tea.KeyMsg:
		return r.handleKey(m)
	}
	return nil
}

// handleKey routes a key press to the open sub-view or to the series list.
func (r *Recurring) handleKey(msg tea.KeyMsg) tea.Cmd {
	if r.pane != recurPaneSeries {
		return r.handlePaneKey(msg)
	}

	switch {
	case keyMatches(r.keys.New, msg):
		r.openForm(nil)
		return nil
	case keyMatches(r.keys.Edit, msg):
		if row, ok := r.current(); ok {
			r.openForm(&row)
		}
		return nil
	case keyMatches(r.keys.Delete, msg):
		r.confirmDelete()
		return nil
	case keyMatches(r.keys.Terms, msg):
		return r.openTerms()
	case keyMatches(r.keys.Forecast, msg):
		r.openForecastForm()
		return nil
	case keyMatches(r.keys.Suggestions, msg):
		r.openSuggestionsForm()
		return nil
	case keyMatches(r.keys.Attached, msg):
		return r.openAttached()
	}
	r.moveTable(&r.table, msg)
	return nil
}

// handlePaneKey routes a key press inside a sub-view: esc always returns to the
// series list, and the rest depends on which view is open.
func (r *Recurring) handlePaneKey(msg tea.KeyMsg) tea.Cmd {
	if keyMatches(r.keys.Back, msg) {
		r.pane = recurPaneSeries
		return nil
	}
	switch r.pane {
	case recurPaneTerms:
		switch {
		case keyMatches(r.keys.AddTerm, msg):
			r.openTermForm(nil)
			return nil
		case keyMatches(r.keys.EditTerm, msg):
			if term, ok := r.currentTerm(); ok {
				r.openTermForm(&term)
			}
			return nil
		case keyMatches(r.keys.DeleteTerm, msg):
			r.confirmDeleteTerm()
			return nil
		}
	case recurPaneSuggestions:
		if keyMatches(r.keys.Attach, msg) {
			return r.attachCurrent()
		}
	case recurPaneAttached:
		if keyMatches(r.keys.Detach, msg) {
			r.confirmDetach()
			return nil
		}
	}
	r.moveTable(&r.sub, msg)
	return nil
}

// moveTable applies the shared cursor keys to a table.
func (r *Recurring) moveTable(table *Table, msg tea.KeyMsg) {
	switch msg.String() {
	case "up", "k":
		table.Move(-1)
	case "down", "j":
		table.Move(1)
	case "pgup", "ctrl+b":
		table.Page(-1, 20)
	case "pgdown", "ctrl+f":
		table.Page(1, 20)
	case "home":
		table.Home()
	case "end":
		table.End()
	}
}

// current returns the selected series.
func (r *Recurring) current() (api.RecurringSeries, bool) {
	index := r.table.Cursor()
	if index < 0 || index >= len(r.series) {
		return api.RecurringSeries{}, false
	}
	return r.series[index], true
}

// currentTerm returns the selected term. Rows are built one-to-one from the
// fetched terms, so the cursor is the index.
func (r *Recurring) currentTerm() (api.RecurringSeriesTerm, bool) {
	index := r.sub.Cursor()
	if index < 0 || index >= len(r.terms) {
		return api.RecurringSeriesTerm{}, false
	}
	return r.terms[index], true
}

// applySeriesRows rebuilds the main table. The monthly figure is the API's own
// normalization and is only ever displayed, never recomputed here.
func (r *Recurring) applySeriesRows() {
	rows := make([][]Cell, 0, len(r.series))
	for _, series := range r.series {
		state := Cell{Text: "on", Role: RolePositive}
		if !series.Active {
			state = Muted("off")
		}
		rows = append(rows, []Cell{
			Text(series.Name),
			Muted(defaultTo(series.Type, "—")),
			Text(recurCadence(series.Frequency, series.Interval)),
			Cell{Text: signedAmount(series.Amount, series.Type), Role: amountRole(series.Type)},
			Text(defaultTo(series.AccountName, r.ctx.Ref.AccountName(series.AccountID))),
			Money(series.MonthlyAmount.Display()),
			Text(formatDatePtr(series.NextDueDate)),
			Text(strconv.Itoa(series.AttachedCount)),
			state,
		})
	}
	r.table.SetRows(rows)
}

// applyTermRows rebuilds the terms table.
func (r *Recurring) applyTermRows() {
	rows := make([][]Cell, 0, len(r.terms))
	for _, term := range r.terms {
		rows = append(rows, []Cell{
			Text(formatDate(term.StartDate)),
			Text(formatDatePtr(term.EndDate)),
			Cell{Text: term.Amount.Display(), Role: RoleMoney},
			Text(defaultTo(term.AccountName, r.ctx.Ref.AccountName(term.AccountID))),
		})
	}
	r.sub.SetRows(rows)
}

// applySuggestionRows rebuilds the suggestion table.
func (r *Recurring) applySuggestionRows() {
	rows := make([][]Cell, 0, len(r.suggestions))
	for _, suggestion := range r.suggestions {
		txn := suggestion.Txn
		rows = append(rows, []Cell{
			Text(formatDate(txn.Date)),
			Text(txn.Description),
			Text(defaultTo(txn.AccountName, r.ctx.Ref.AccountName(txn.AccountID))),
			Cell{Text: signedAmount(txn.Amount, txn.Type), Role: amountRole(txn.Type)},
			Text(fmt.Sprintf("%.2f", suggestion.Score)),
			Text(formatDate(suggestion.OccurrenceDate)),
			Text(fmt.Sprintf("%.1f", suggestion.DaysOff)),
		})
	}
	r.sub.SetRows(rows)
}

// applyAttachedRows rebuilds the attached-transaction table.
func (r *Recurring) applyAttachedRows() {
	rows := make([][]Cell, 0, len(r.attached))
	for _, txn := range r.attached {
		rows = append(rows, []Cell{
			Text(formatDate(txn.Date)),
			Text(txn.Description),
			Text(defaultTo(txn.AccountName, r.ctx.Ref.AccountName(txn.AccountID))),
			Cell{Text: signedAmount(txn.Amount, txn.Type), Role: amountRole(txn.Type)},
			Text(truncateID(txn.ID)),
		})
	}
	r.sub.SetRows(rows)
}

// setSubject records which series a sub-view belongs to.
func (r *Recurring) setSubject(series api.RecurringSeries) {
	r.subjectID = series.ID
	r.subjectName = series.Name
}

// openTerms shows the series' effective-dated terms, which override the
// series' own account and amount for the range each one covers.
func (r *Recurring) openTerms() tea.Cmd {
	series, ok := r.current()
	if !ok {
		r.ctx.Notify(LevelError, "select a series first")
		return nil
	}
	r.setSubject(series)
	r.pane = recurPaneTerms
	r.terms = nil
	r.sub.SetColumns(
		Column{Title: "Start", Width: 12},
		Column{Title: "End (exclusive)"},
		Column{Title: "Amount", Width: 14, Align: AlignRight},
		Column{Title: "Account", Width: 18},
	)
	r.sub.SetRows(nil)
	return r.reloadTerms(series.ID)
}

// openAttached shows the transactions already linked to the series.
func (r *Recurring) openAttached() tea.Cmd {
	series, ok := r.current()
	if !ok {
		r.ctx.Notify(LevelError, "select a series first")
		return nil
	}
	r.setSubject(series)
	r.pane = recurPaneAttached
	r.attached = nil
	r.sub.SetColumns(
		Column{Title: "Date", Width: 11},
		Column{Title: "Description"},
		Column{Title: "Account", Width: 14},
		Column{Title: "Amount", Width: 12, Align: AlignRight},
		Column{Title: "Id", Width: 10},
	)
	r.sub.SetRows(nil)
	return r.reloadAttached(series.ID)
}

// openForm opens the create or edit form. A nil series creates one.
func (r *Recurring) openForm(row *api.RecurringSeries) {
	ref := r.ctx.Ref
	creating := row == nil
	title := "New recurring series"
	current := api.RecurringSeries{Type: "debit", Frequency: "monthly", Interval: 1, Active: true}
	if row != nil {
		title = "Edit recurring series"
		current = *row
	}

	accounts := ref.AccountOptions()
	if len(accounts) == 0 {
		r.ctx.Notify(LevelError, "create an account first")
		return
	}
	accountID := current.AccountID
	if accountID == "" {
		accountID = accounts[0].Value
	}
	interval := current.Interval
	if interval < 1 {
		interval = 1
	}
	active := current.Active
	if creating {
		active = true
	}

	amountField := AmountField("Amount", current.Amount.String())
	accountField := SelectField("Account", accountID, accounts, true)
	if !creating {
		// On an existing series the API does not overwrite the stored
		// account/amount: it records a NEW range starting at the effective
		// date, which is what keeps past occurrences on the account they
		// really came from.
		amountField.Help = "a change records a new range from the effective date on; earlier ranges keep their amount"
		accountField.Help = "a change records a new range from the effective date on; earlier ranges keep their account"
	}

	fields := []Field{
		{Label: "Name", Kind: FieldText, Value: current.Name, Width: 36, Validate: required("name")},
		SelectField("Type", defaultTo(current.Type, "debit"),
			[]Option{{Value: "debit", Label: "debit (money out)"}, {Value: "credit", Label: "credit (money in)"}}, true),
		SelectField("Frequency", defaultTo(current.Frequency, "monthly"),
			[]Option{
				{Value: "daily", Label: "daily"},
				{Value: "weekly", Label: "weekly"},
				{Value: "monthly", Label: "monthly"},
				{Value: "yearly", Label: "yearly"},
			}, true),
		{Label: "Interval", Kind: FieldText, Value: strconv.Itoa(interval), Width: 6, Validate: requiredInt,
			Help: "every N periods, e.g. monthly with interval 2 is every second month"},
		amountField,
		accountField,
	}
	if creating {
		fields = append(fields,
			Field{Label: "Start date", Kind: FieldText, Value: nowDate(), Width: 12, Validate: requiredDate,
				Help: "first occurrence; the template creates no transaction by itself"},
			Field{Label: "End date", Kind: FieldText, Width: 12, Validate: optionalDate,
				Help: "stop projecting after this date; empty leaves the series open-ended"},
		)
	} else {
		fields = append(fields, Field{Label: "Effective date", Kind: FieldText, Value: nowDate(), Width: 12, Validate: requiredDate,
			Help: "start of a new amount/account range (inclusive); defaults to today and is ignored when neither changed"})
	}
	fields = append(fields,
		SelectField("Category", derefID(current.CategoryID), ref.CategoryOptions(), false),
		SelectField("Payee", derefID(current.PayeeID), ref.PayeeOptions(), false),
		Field{Label: "Notes", Kind: FieldText, Value: current.Notes, Width: 44},
		BoolField("Active", active),
	)

	r.ctx.Open(NewForm("recurring.save", title, fields, func(f *Form) tea.Cmd {
		amount, err := api.ParseAmount(f.Value("Amount"))
		if err != nil {
			return nil
		}
		active := f.BoolValue("Active")

		if creating {
			account := f.Value("Account")
			var categoryID, payeeID *string
			if value := f.Value("Category"); value != "" {
				categoryID = &value
			}
			if value := f.Value("Payee"); value != "" {
				payeeID = &value
			}
			// Ranges stays nil: a new series is described by this single
			// account/amount/start date, and effective-dated ranges or terms
			// can be layered on afterwards.
			req := api.CreateRecurringSeriesRequest{
				AccountID:  &account,
				Name:       f.Value("Name"),
				Amount:     &amount,
				Type:       f.Value("Type"),
				Frequency:  f.Value("Frequency"),
				Interval:   f.IntValue("Interval"),
				StartDate:  f.Value("Start date"),
				EndDate:    f.Value("End date"),
				CategoryID: categoryID,
				PayeeID:    payeeID,
				Active:     &active,
				Notes:      f.Value("Notes"),
			}
			return act("recurring.save", "recurring series created", false, func(ctx context.Context) error {
				_, err := r.ctx.Client.CreateRecurring(ctx, req)
				return err
			})
		}

		id := current.ID
		name := f.Value("Name")
		seriesType := f.Value("Type")
		frequency := f.Value("Frequency")
		interval := f.IntValue("Interval")
		notes := f.Value("Notes")
		effective := f.Value("Effective date")
		req := api.UpdateRecurringSeriesRequest{
			Name:          &name,
			Type:          &seriesType,
			Frequency:     &frequency,
			Interval:      &interval,
			Notes:         &notes,
			Active:        &active,
			EffectiveDate: &effective,
			// An empty selection is sent as an explicit null, which is what
			// clears the column.
			CategoryID: optionalID(f.Value("Category")),
			PayeeID:    optionalID(f.Value("Payee")),
		}
		// Send an amount or account only when it really changed: the API turns
		// either one into a new range from EffectiveDate, so echoing an
		// unchanged value back would cut a pointless boundary into the series'
		// history. Ranges stays nil, which is exactly what keeps every earlier
		// range in force; the whole list is only replaced by a request that
		// carries one.
		if amount != current.Amount {
			req.Amount = &amount
		}
		if account := f.Value("Account"); account != current.AccountID {
			req.AccountID = &account
		}
		return act("recurring.save", "recurring series updated", false, func(ctx context.Context) error {
			_, err := r.ctx.Client.UpdateRecurring(ctx, id, req)
			return err
		})
	}))
}

// openTermForm opens the add or edit form for one term. A nil term adds one.
// Terms are the API's own way of changing the amount or account a series uses
// over time, which is why they carry the range rule the form spells out.
func (r *Recurring) openTermForm(term *api.RecurringSeriesTerm) {
	creating := term == nil
	title := "Add term"
	current := api.RecurringSeriesTerm{}
	if term != nil {
		title = "Edit term"
		current = *term
	}
	// An empty end date (rather than a dash) is how the form says
	// "open-ended": the field is a date, and blank is the only valid spelling of
	// no value for a series that never ends.
	start := nowDate()
	if !current.StartDate.IsZero() {
		start = current.StartDate.Format(api.DateLayout)
	}
	end := ""
	if current.EndDate != nil {
		end = formatDate(*current.EndDate)
	}

	accounts := r.ctx.Ref.AccountOptions()
	if len(accounts) == 0 {
		r.ctx.Notify(LevelError, "create an account first")
		return
	}
	accountID := current.AccountID
	if accountID == "" {
		accountID = accounts[0].Value
	}

	fields := []Field{
		{Label: "Start date", Kind: FieldText, Value: start, Width: 12, Validate: requiredDate,
			Help: "included in the term — a term covers [start date, end date)"},
		{Label: "End date", Kind: FieldText, Value: end, Width: 12, Validate: optionalDate,
			Help: "EXCLUSIVE: the date is not covered, so the next term may start on it; empty means open-ended"},
		AmountField("Amount", current.Amount.String()),
		SelectField("Account", accountID, accounts, true),
	}
	if !creating {
		fields[0].Help = "included in the term — a term covers [start date, end date); terms of one series must not overlap"
	}

	r.ctx.Open(NewForm("recurring.term.save", title, fields, func(f *Form) tea.Cmd {
		amount, err := api.ParseAmount(f.Value("Amount"))
		if err != nil {
			return nil
		}
		start := f.Value("Start date")
		end := f.Value("End date")
		account := f.Value("Account")
		if creating {
			// A term is created with a PUT that answers 201 with the stored
			// term; an empty end date is simply omitted, which the API reads as
			// open-ended.
			req := api.CreateRecurringSeriesTermRequest{
				StartDate: start,
				EndDate:   end,
				Amount:    amount,
				AccountID: account,
			}
			return act("recurring.term.save", "term added", false, func(ctx context.Context) error {
				_, err := r.ctx.Client.AddRecurringTerm(ctx, r.subjectID, req)
				return err
			})
		}
		// Every field is sent explicitly here: an empty End date is a pointer
		// to "", which is what clears the range end rather than leaving it
		// alone.
		req := api.UpdateRecurringSeriesTermRequest{
			StartDate: &start,
			EndDate:   &end,
			Amount:    &amount,
			AccountID: &account,
		}
		id := current.ID
		return act("recurring.term.save", "term updated", false, func(ctx context.Context) error {
			_, err := r.ctx.Client.UpdateRecurringTerm(ctx, r.subjectID, id, req)
			return err
		})
	}))
}

// confirmDeleteTerm deletes the selected term, which leaves a gap in the
// series' coverage rather than restoring the series' own amount.
func (r *Recurring) confirmDeleteTerm() {
	term, ok := r.currentTerm()
	if !ok {
		return
	}
	r.ctx.Open(NewConfirm("Delete term",
		fmt.Sprintf("Delete the term starting %s?", formatDate(term.StartDate)), true, func() tea.Cmd {
			return act("recurring.term.delete", "term deleted", false, func(ctx context.Context) error {
				return r.ctx.Client.DeleteRecurringTerm(ctx, r.subjectID, term.ID)
			})
		}).WithDetail(
		"The series itself is untouched; the dates this term covered simply stop matching anything.",
		"The range is [start date, end date), so an adjacent term that starts on this term's end date is unaffected.",
	))
}

// confirmDelete deletes the selected series.
func (r *Recurring) confirmDelete() {
	series, ok := r.current()
	if !ok {
		return
	}
	name := series.Name
	r.ctx.Open(NewConfirm("Delete recurring series", fmt.Sprintf("Delete %q?", name), true, func() tea.Cmd {
		return act("recurring.delete", "recurring series deleted", false, func(ctx context.Context) error {
			return r.ctx.Client.DeleteRecurring(ctx, series.ID)
		})
	}).WithDetail(
		"The series is a template: no transaction was ever created from it, so none is deleted.",
		"Its terms and its attached links go with it; the attached transactions are kept and merely unlinked.",
	))
}

// openForecastForm asks how many occurrences to project, then shows the
// projection in a read-only overlay.
func (r *Recurring) openForecastForm() {
	series, ok := r.current()
	if !ok {
		r.ctx.Notify(LevelError, "select a series first")
		return
	}
	r.setSubject(series)
	id := series.ID
	fields := []Field{
		{Label: "Occurrences", Kind: FieldText, Value: "12", Width: 6, Validate: recurCountValidator(1, 60),
			Help: "how many future occurrences to project (1-60; the API caps the count at 60)"},
	}
	r.ctx.Open(NewForm("recurring.forecast.form", "Forecast "+series.Name, fields, func(f *Form) tea.Cmd {
		count := f.IntValue("Occurrences")
		// Nothing was written, so the form closes at once and the projection
		// arrives as an overlay of its own.
		f.Close()
		return load("recurring.forecast", func(ctx context.Context) ([]api.RecurringForecastItem, error) {
			return r.ctx.Client.RecurringForecast(ctx, id, count)
		})
	}))
}

// openSuggestionsForm asks for the suggestion window, then opens the read-only
// suggestion sub-view where `y` links one transaction.
func (r *Recurring) openSuggestionsForm() {
	series, ok := r.current()
	if !ok {
		r.ctx.Notify(LevelError, "select a series first")
		return
	}
	r.setSubject(series)
	id := series.ID
	fields := []Field{
		{Label: "Limit", Kind: FieldText, Value: strconv.Itoa(r.suggestLimit), Width: 6, Validate: recurCountValidator(1, 500),
			Help: "how many candidate transactions to consider (1-500; the API caps the limit at 500)"},
	}
	r.ctx.Open(NewForm("recurring.suggestions.form", "Suggestions for "+series.Name, fields, func(f *Form) tea.Cmd {
		limit := f.IntValue("Limit")
		f.Close()
		r.suggestLimit = limit
		r.pane = recurPaneSuggestions
		r.suggestions = nil
		r.sub.SetColumns(
			Column{Title: "Date", Width: 11},
			Column{Title: "Description"},
			Column{Title: "Account", Width: 14},
			Column{Title: "Amount", Width: 12, Align: AlignRight},
			Column{Title: "Score", Width: 6, Align: AlignRight},
			Column{Title: "Occurrence", Width: 11},
			Column{Title: "Days off", Width: 8, Align: AlignRight},
		)
		r.sub.SetRows(nil)
		return r.reloadSuggestions(id, limit)
	}))
}

// attachCurrent links the highlighted suggestion to the series. This is the only
// write the suggestion view performs, and it happens per transaction on purpose:
// the API answers 409 for a transaction that already belongs to another series,
// so asking for one at a time keeps that refusal attributable to a row.
func (r *Recurring) attachCurrent() tea.Cmd {
	index := r.sub.Cursor()
	if index < 0 || index >= len(r.suggestions) {
		return nil
	}
	txnID := r.suggestions[index].Txn.ID
	return act("recurring.attach", "transaction attached", false, func(ctx context.Context) error {
		_, err := r.ctx.Client.AttachRecurring(ctx, r.subjectID, []string{txnID})
		return err
	})
}

// confirmDetach unlinks the highlighted attached transaction.
func (r *Recurring) confirmDetach() {
	index := r.sub.Cursor()
	if index < 0 || index >= len(r.attached) {
		return
	}
	txn := r.attached[index]
	r.ctx.Open(NewConfirm("Detach transaction",
		fmt.Sprintf("Detach %q from this series?", txn.Description), false, func() tea.Cmd {
			return act("recurring.detach", "transaction detached", false, func(ctx context.Context) error {
				_, err := r.ctx.Client.DetachRecurring(ctx, []string{txn.ID})
				return err
			})
		}).WithDetail(
		"The transaction is kept — it simply stops counting as a covered occurrence.",
		"A transaction belongs to at most one series, so it can be attached elsewhere afterwards.",
	))
}

// View implements Screen.
func (r *Recurring) View(width, height int) string {
	th := r.ctx.Theme
	body := max(2, height-5)
	parts := []string{r.headerLine(width)}

	switch r.pane {
	case recurPaneTerms:
		parts = append(parts,
			r.sub.View(th, width, body, "no terms — the series' own amount and account stay in force"),
			r.paneNote(),
			"a add · E edit · D delete · esc back",
		)
	case recurPaneSuggestions:
		parts = append(parts,
			r.sub.View(th, width, body, "no suggestions — nothing looks like an occurrence of this series yet"),
			r.paneNote(),
			"y attach highlighted · esc back",
		)
	case recurPaneAttached:
		parts = append(parts,
			r.sub.View(th, width, body, "nothing attached yet — review the suggestions with s"),
			r.paneNote(),
			"u detach highlighted · esc back",
		)
	default:
		parts = append(parts, r.table.View(th, width, body, "no recurring series yet — n adds one"))
		if detail := r.detailLine(width); detail != "" {
			parts = append(parts, detail)
		}
		parts = append(parts, "n new · e edit · d delete · t terms · f forecast · s suggestions · T attached")
	}
	return trimToBox(strings.Join(parts, "\n"), width, height)
}

// headerLine summarises the open view and its subject.
func (r *Recurring) headerLine(width int) string {
	th := r.ctx.Theme
	var summary string
	switch r.pane {
	case recurPaneTerms:
		summary = fmt.Sprintf("Terms · %s · %s", defaultTo(r.subjectName, "series"), pluralise(len(r.terms), "term", "terms"))
	case recurPaneSuggestions:
		summary = fmt.Sprintf("Suggestions · %s · %s proposed, nothing linked yet",
			defaultTo(r.subjectName, "series"), pluralise(len(r.suggestions), "transaction", "transactions"))
	case recurPaneAttached:
		summary = fmt.Sprintf("Attached · %s · %s",
			defaultTo(r.subjectName, "series"), pluralise(len(r.attached), "transaction", "transactions"))
	default:
		active, attached := 0, 0
		for _, series := range r.series {
			if series.Active {
				active++
			}
			attached += series.AttachedCount
		}
		summary = fmt.Sprintf("%s · %d active · %d attached occurrence(s)",
			pluralise(len(r.series), "series", "series"), active, attached)
	}
	return th.Title.Render(truncate(summary, width))
}

// detailLine describes the cursor series: the dates it runs between, what it is
// filed under, and any note left on it.
func (r *Recurring) detailLine(width int) string {
	series, ok := r.current()
	if !ok {
		return ""
	}
	parts := []string{"id " + truncateID(series.ID)}
	parts = append(parts, "starts "+formatDate(series.StartDate))
	if series.EndDate == nil {
		parts = append(parts, "open-ended")
	} else {
		parts = append(parts, "ends "+formatDate(*series.EndDate))
	}
	parts = append(parts,
		"category "+categoryOr(series.CategoryName, series.CategoryID),
		"payee "+payeeOr(series.Payee, series.PayeeID),
	)
	if series.Notes != "" {
		parts = append(parts, "notes "+series.Notes)
	}
	return r.ctx.Theme.Subtle.Render(truncate(strings.Join(parts, " · "), width))
}

// paneNote is the one-line explanation of the open sub-view's rules.
func (r *Recurring) paneNote() string {
	switch r.pane {
	case recurPaneTerms:
		return r.ctx.Theme.Subtle.Render("a term covers [start date, end date): the end is EXCLUSIVE, so adjacent terms may share a boundary and a gap matches nothing")
	case recurPaneSuggestions:
		return r.ctx.Theme.Subtle.Render("read-only: nothing is linked yet · y links the highlighted transaction, and a transaction may belong to at most one series (the API answers 409 otherwise)")
	case recurPaneAttached:
		return r.ctx.Theme.Subtle.Render("these transactions already cover an occurrence of the series · u unlinks the highlighted one, keeping the transaction itself")
	}
	return ""
}

// recurCadence renders a frequency and interval the way the web UI does, e.g.
// "monthly ×2".
func recurCadence(frequency string, interval int) string {
	if frequency == "" {
		return "—"
	}
	if interval <= 1 {
		return frequency
	}
	return fmt.Sprintf("%s ×%d", frequency, interval)
}

// recurCountValidator bounds a count field. The API clamps these server-side
// (60 forecasts, 500 suggestions), so the form reports the limit rather than
// silently asking for a window the server would shrink.
func recurCountValidator(low, high int) func(string) error {
	return func(value string) error {
		value = strings.TrimSpace(value)
		if value == "" {
			return errText("required")
		}
		n, err := strconv.Atoi(value)
		if err != nil || n < low || n > high {
			return errText(fmt.Sprintf("expected a whole number between %d and %d", low, high))
		}
		return nil
	}
}

// recurForecastBody renders the projected occurrences. The amounts come from the
// API's own projection, so this only lays them out.
func recurForecastBody(items []api.RecurringForecastItem) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%-12s %16s  %s\n", "Date", "Amount", "Matched")
	if len(items) == 0 {
		b.WriteString("\n  nothing to project — the series has no future occurrence (check its end date)\n")
		return b.String()
	}
	for _, item := range items {
		matched := "no"
		if item.Matched {
			matched = "yes"
		}
		fmt.Fprintf(&b, "%-12s %16s  %s\n", formatDate(item.Date), signedAmount(item.Amount, item.Type), matched)
	}
	return b.String()
}
