package ui

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/fintrak/client/api"
)

func init() {
	registerScreen(20, func(ctx *Ctx) Screen { return NewTransactions(ctx) })
}

// Transactions is the transaction ledger: a filtered, paged list with inline
// create/edit/delete, multi-select bulk operations, CSV export, and a detail
// overlay that follows the link graph.
type Transactions struct {
	ctx   *Ctx
	table Table

	rows   []api.Transaction
	info   api.TransactionPage
	filter api.TransactionFilter

	selected  map[string]bool
	searching bool
	search    textinput.Model

	keys txKeys
}

// txKeys are the screen's bindings.
type txKeys struct {
	Filter     key.Binding
	New        key.Binding
	Edit       key.Binding
	Delete     key.Binding
	Detail     key.Binding
	Select     key.Binding
	SelectAll  key.Binding
	Clear      key.Binding
	Sort       key.Binding
	SortDir    key.Binding
	PrevPage   key.Binding
	NextPage   key.Binding
	Export     key.Binding
	BulkCat    key.Binding
	BulkPayee  key.Binding
	BulkCycle  key.Binding
	BulkTags   key.Binding
	BulkLoan   key.Binding
	BulkDetach key.Binding
}

func newTxKeys() txKeys {
	return txKeys{
		Filter:     key.NewBinding(key.WithKeys("f"), key.WithHelp("f", "filter")),
		New:        key.NewBinding(key.WithKeys("n"), key.WithHelp("n", "new")),
		Edit:       key.NewBinding(key.WithKeys("e"), key.WithHelp("e", "edit")),
		Delete:     key.NewBinding(key.WithKeys("d"), key.WithHelp("d", "delete")),
		Detail:     key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "detail + links")),
		Select:     key.NewBinding(key.WithKeys(" "), key.WithHelp("space", "select")),
		SelectAll:  key.NewBinding(key.WithKeys("a"), key.WithHelp("a", "select all/none")),
		Clear:      key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "clear selection")),
		Sort:       key.NewBinding(key.WithKeys("s"), key.WithHelp("s", "sort field")),
		SortDir:    key.NewBinding(key.WithKeys("S"), key.WithHelp("S", "sort direction")),
		PrevPage:   key.NewBinding(key.WithKeys(","), key.WithHelp(",", "prev page")),
		NextPage:   key.NewBinding(key.WithKeys("."), key.WithHelp(".", "next page")),
		Export:     key.NewBinding(key.WithKeys("x"), key.WithHelp("x", "export csv")),
		BulkCat:    key.NewBinding(key.WithKeys("c"), key.WithHelp("c", "categorize")),
		BulkPayee:  key.NewBinding(key.WithKeys("p"), key.WithHelp("p", "payee")),
		BulkCycle:  key.NewBinding(key.WithKeys("B"), key.WithHelp("B", "billing cycle")),
		BulkTags:   key.NewBinding(key.WithKeys("t"), key.WithHelp("t", "tags")),
		BulkLoan:   key.NewBinding(key.WithKeys("l"), key.WithHelp("l", "attach loan")),
		BulkDetach: key.NewBinding(key.WithKeys("L"), key.WithHelp("L", "detach loan")),
	}
}

// NewTransactions builds the ledger screen.
func NewTransactions(ctx *Ctx) *Transactions {
	t := &Transactions{
		ctx:      ctx,
		filter:   api.TransactionFilter{Limit: 50, SortBy: "date", SortOrder: "DESC"},
		selected: map[string]bool{},
		keys:     newTxKeys(),
	}
	t.table.SetColumns(
		Column{Title: "Date", Width: 11},
		Column{Title: "Type", Width: 4},
		Column{Title: "Description"},
		Column{Title: "Payee", Width: 18},
		Column{Title: "Category", Width: 16},
		Column{Title: "Account", Width: 14},
		Column{Title: "Amount", Width: 14, Align: AlignRight},
	)
	return t
}

// Title implements Screen.
func (t *Transactions) Title() string { return "Transactions" }

// Keys implements Screen.
func (t *Transactions) Keys() []key.Binding {
	if len(t.selected) > 0 {
		return []key.Binding{
			t.keys.BulkCat, t.keys.BulkPayee, t.keys.BulkCycle, t.keys.BulkTags,
			t.keys.BulkLoan, t.keys.BulkDetach, t.keys.Delete, t.keys.Clear,
		}
	}
	return []key.Binding{
		t.keys.Filter, t.keys.New, t.keys.Edit, t.keys.Detail, t.keys.Select,
		t.keys.SelectAll, t.keys.Sort, t.keys.SortDir, t.keys.Export,
	}
}

// Refresh implements Screen.
func (t *Transactions) Refresh() tea.Cmd { return t.reload() }

// reload fetches the current page.
func (t *Transactions) reload() tea.Cmd {
	filter := t.filter
	return load("txn.list", func(ctx context.Context) (api.TransactionPage, error) {
		return t.ctx.Client.ListTransactions(ctx, filter)
	})
}

// Update implements Screen.
func (t *Transactions) Update(msg tea.Msg) tea.Cmd {
	switch m := msg.(type) {
	case loaded[api.TransactionPage]:
		if m.tag != "txn.list" {
			break
		}
		if m.err != nil {
			t.ctx.Notify(LevelError, "%s", m.err)
			return nil
		}
		t.info = m.data
		t.rows = m.data.Data
		t.applyRows()
		t.pruneSelection()
		return nil

	case loaded[[]api.Link]:
		if m.tag != "txn.links" {
			break
		}
		if m.err != nil {
			t.ctx.Notify(LevelError, "%s", m.err)
			return nil
		}
		row, ok := t.current()
		if !ok {
			return nil
		}
		t.ctx.Open(NewInfo("Transaction", txnDetail(row, m.data, t.ctx.Ref)))
		return nil

	case loaded[[]api.BillingCycle]:
		if m.tag != "txn.cycles" {
			break
		}
		if m.err != nil {
			t.ctx.Notify(LevelError, "%s", m.err)
			return nil
		}
		t.openBulkCycleForm(m.data)
		return nil

	case done:
		if !strings.HasPrefix(m.tag, "txn.") {
			break
		}
		if m.err != nil {
			return nil
		}
		// The list is server state, so a write invalidates it: without this the
		// edited row kept showing its old values until the user pressed r.
		if m.tag == "txn.export" {
			return nil // an export changes nothing
		}
		t.clearSelection()
		return t.reload()

	case tea.KeyMsg:
		return t.handleKey(m)
	}
	return nil
}

// handleKey routes a key press.
func (t *Transactions) handleKey(msg tea.KeyMsg) tea.Cmd {
	if t.searching {
		switch msg.String() {
		case "enter":
			t.searching = false
			t.filter.Search = t.search.Value()
			t.filter.Page = 1
			return t.reload()
		case "esc":
			t.searching = false
			return nil
		}
		var cmd tea.Cmd
		t.search, cmd = t.search.Update(msg)
		return cmd
	}

	switch {
	case keyMatches(t.keys.Filter, msg):
		t.openFilterForm()
		return nil
	case keyMatches(t.keys.New, msg):
		t.openTxnForm(nil)
		return nil
	case keyMatches(t.keys.Edit, msg):
		if row, ok := t.current(); ok {
			t.openTxnForm(&row)
		}
		return nil
	case keyMatches(t.keys.Detail, msg):
		row, ok := t.current()
		if !ok {
			return nil
		}
		txnID := row.ID
		return load("txn.links", func(ctx context.Context) ([]api.Link, error) {
			return t.ctx.Client.ListLinks(ctx, "", txnID)
		})
	case keyMatches(t.keys.Select, msg):
		t.toggleSelection()
		return nil
	case keyMatches(t.keys.SelectAll, msg):
		t.toggleAll()
		return nil
	case keyMatches(t.keys.Clear, msg):
		t.clearSelection()
		return nil
	case keyMatches(t.keys.Sort, msg):
		t.cycleSort()
		return t.reload()
	case keyMatches(t.keys.SortDir, msg):
		if strings.EqualFold(t.filter.SortOrder, "ASC") {
			t.filter.SortOrder = "DESC"
		} else {
			t.filter.SortOrder = "ASC"
		}
		return t.reload()
	case keyMatches(t.keys.PrevPage, msg):
		if t.filter.Page > 1 {
			t.filter.Page--
			return t.reload()
		}
		return nil
	case keyMatches(t.keys.NextPage, msg):
		if t.filter.Page < t.info.Pages {
			t.filter.Page++
			return t.reload()
		}
		return nil
	case keyMatches(t.keys.Export, msg):
		t.openExportForm()
		return nil
	case keyMatches(t.keys.BulkCat, msg):
		t.openBulkCategoryForm()
		return nil
	case keyMatches(t.keys.BulkPayee, msg):
		t.openBulkPayeeForm()
		return nil
	case keyMatches(t.keys.BulkCycle, msg):
		return t.startBulkCycle()
	case keyMatches(t.keys.BulkTags, msg):
		t.openBulkTagsForm()
		return nil
	case keyMatches(t.keys.BulkLoan, msg):
		t.openBulkLoanForm()
		return nil
	case keyMatches(t.keys.BulkDetach, msg):
		t.confirmBulkDetachLoan()
		return nil
	case keyMatches(t.keys.Delete, msg):
		t.confirmDelete()
		return nil
	}

	switch msg.String() {
	case "up", "k":
		t.table.Move(-1)
	case "down", "j":
		t.table.Move(1)
	case "pgup", "ctrl+b":
		t.table.Page(-1, 20)
	case "pgdown", "ctrl+f":
		t.table.Page(1, 20)
	case "home", "g":
		t.table.Home()
	case "end", "G":
		t.table.End()
	case "/":
		t.searching = true
		t.search = textinput.New()
		t.search.Placeholder = "search description, notes, payee, tags"
		t.search.Width = 48
		t.search.SetValue(t.filter.Search)
		t.search.Focus()
		return textinput.Blink
	}
	return nil
}

// applyRows rebuilds the table rows from the fetched transactions.
func (t *Transactions) applyRows() {
	rows := make([][]Cell, 0, len(t.rows))
	for _, row := range t.rows {
		marker := "  "
		if t.selected[row.ID] {
			marker = "▌ "
		}
		if row.IsSummary {
			rows = append(rows, []Cell{
				Muted(row.Date.Format(api.DateLayout)),
				Muted(""),
				Muted(marker + row.Description),
				Muted(""),
				Muted(""),
				Muted(row.AccountName),
				Cell{Text: row.Amount.Display(), Role: RoleMuted},
			})
			continue
		}
		typeLabel := "dr"
		role := RoleNegative
		if row.Type == "credit" {
			typeLabel, role = "cr", RolePositive
		}
		rows = append(rows, []Cell{
			Text(row.Date.Format(api.DateLayout)),
			Muted(typeLabel),
			Text(marker + row.Description),
			Text(payeeOr(row.Payee, row.PayeeID)),
			Text(categoryOr(row.CategoryName, row.CategoryID)),
			Text(row.AccountName),
			Cell{Text: signedAmount(row.Amount, row.Type), Role: role},
		})
	}
	t.table.SetRows(rows)
}

// current returns the selected transaction, skipping synthetic summary rows
// which cannot be edited or linked.
func (t *Transactions) current() (api.Transaction, bool) {
	index := t.table.Cursor()
	if index < 0 || index >= len(t.rows) {
		return api.Transaction{}, false
	}
	if t.rows[index].IsSummary {
		return api.Transaction{}, false
	}
	return t.rows[index], true
}

// pruneSelection drops selections that are no longer on screen, so a bulk action
// can never touch a row the user cannot see.
func (t *Transactions) pruneSelection() {
	visible := make(map[string]bool, len(t.rows))
	for _, row := range t.rows {
		visible[row.ID] = true
	}
	for id := range t.selected {
		if !visible[id] {
			delete(t.selected, id)
		}
	}
}

// toggleSelection flips the cursor row's selection.
func (t *Transactions) toggleSelection() {
	row, ok := t.current()
	if !ok {
		return
	}
	if t.selected[row.ID] {
		delete(t.selected, row.ID)
	} else {
		t.selected[row.ID] = true
	}
	t.applyRows()
}

// toggleAll selects every listed transaction, or clears when all are selected.
func (t *Transactions) toggleAll() {
	all := true
	for _, row := range t.rows {
		if row.IsSummary {
			continue
		}
		if !t.selected[row.ID] {
			all = false
			break
		}
	}
	t.selected = map[string]bool{}
	if !all {
		for _, row := range t.rows {
			if !row.IsSummary {
				t.selected[row.ID] = true
			}
		}
	}
	t.applyRows()
}

// clearSelection empties the selection.
func (t *Transactions) clearSelection() {
	if len(t.selected) == 0 {
		return
	}
	t.selected = map[string]bool{}
	t.applyRows()
}

// selectedIDs returns the selected transaction ids.
func (t *Transactions) selectedIDs() []string {
	ids := make([]string, 0, len(t.selected))
	for _, row := range t.rows {
		if t.selected[row.ID] {
			ids = append(ids, row.ID)
		}
	}
	return ids
}

// cycleSort steps through the sortable fields.
func (t *Transactions) cycleSort() {
	switch t.filter.SortBy {
	case "", "date":
		t.filter.SortBy = "amount"
	case "amount":
		t.filter.SortBy = "createdAt"
	default:
		t.filter.SortBy = "date"
	}
}

// openFilterForm opens the filter editor covering the whole query grammar.
func (t *Transactions) openFilterForm() {
	ref := t.ctx.Ref
	filter := t.filter
	fields := []Field{
		{Label: "Search", Kind: FieldText, Value: filter.Search, Width: 40, Help: "description, notes, payee or tags"},
		SelectField("Account", filter.AccountID, ref.AccountOptions(), false),
		SelectField("Category", filter.CategoryID, append([]Option{{Value: api.UncategorizedCategory, Label: "Uncategorized"}}, ref.CategoryOptions()...), false),
		SelectField("Group", filter.GroupID, ref.GroupOptions(), false),
		SelectField("Payee", filter.PayeeID, append([]Option{{Value: api.NoPayee, Label: "(no payee)"}}, ref.PayeeOptions()...), false),
		SelectField("Type", filter.Type, []Option{{Value: "debit", Label: "debit"}, {Value: "credit", Label: "credit"}}, false),
		SelectField("Linked", linkedState(filter.Linked), []Option{{Value: "yes", Label: "linked"}, {Value: "no", Label: "unlinked"}}, false),
		{Label: "Date from", Kind: FieldText, Value: filter.DateFrom, Width: 14, Validate: optionalDate},
		{Label: "Date to", Kind: FieldText, Value: filter.DateTo, Width: 14, Validate: optionalDate},
		{Label: "Tags", Kind: FieldText, Value: strings.Join(filter.Tags, ","), Width: 30, Help: "comma separated, matches any"},
		{Label: "Amount", Kind: FieldText, Value: filter.Amount, Width: 14},
		{Label: "Limit", Kind: FieldText, Value: strconv.Itoa(filter.Limit), Width: 6, Validate: positiveInt},
		{Label: "Page", Kind: FieldText, Value: strconv.Itoa(filter.Page), Width: 6, Validate: positiveInt},
	}
	t.ctx.Open(NewForm("txn.filter", "Filter transactions", fields, func(f *Form) tea.Cmd {
		t.filter = api.TransactionFilter{
			AccountID:  f.Value("Account"),
			CategoryID: f.Value("Category"),
			GroupID:    f.Value("Group"),
			PayeeID:    f.Value("Payee"),
			Search:     f.Value("Search"),
			Type:       f.Value("Type"),
			DateFrom:   f.Value("Date from"),
			DateTo:     f.Value("Date to"),
			Amount:     f.Value("Amount"),
			Tags:       splitList(f.Value("Tags")),
			Linked:     parseLinked(f.Value("Linked")),
			SortBy:     filter.SortBy,
			SortOrder:  filter.SortOrder,
			Limit:      f.IntValue("Limit"),
			Page:       f.IntValue("Page"),
		}
		f.Close()
		t.ctx.Notify(LevelInfo, "filters applied")
		return t.reload()
	}))
}

// openTxnForm opens the create or edit form. A nil row creates.
func (t *Transactions) openTxnForm(row *api.Transaction) {
	ref := t.ctx.Ref
	creating := row == nil
	title := "New transaction"
	current := api.Transaction{Type: "debit", Date: time.Now()}
	if row != nil {
		title = "Edit transaction"
		current = *row
	}

	accounts := ref.AccountOptions()
	if len(accounts) == 0 {
		t.ctx.Notify(LevelError, "create an account first")
		return
	}
	accountID := current.AccountID
	if accountID == "" {
		accountID = accounts[0].Value
	}

	fields := []Field{
		SelectField("Account", accountID, accounts, true),
		{Label: "Date", Kind: FieldText, Value: current.Date.Format(api.DateLayout), Width: 14, Validate: requiredDate},
		{Label: "Description", Kind: FieldText, Value: current.Description, Width: 44, Validate: required("description")},
		AmountField("Amount", current.Amount.String()),
		SelectField("Type", defaultTo(current.Type, "debit"), []Option{{Value: "debit", Label: "debit"}, {Value: "credit", Label: "credit"}}, true),
		SelectField("Category", derefID(current.CategoryID), ref.CategoryOptions(), false),
		SelectField("Payee", derefID(current.PayeeID), ref.PayeeOptions(), false),
		{Label: "Tags", Kind: FieldText, Value: strings.Join(current.Tags, ","), Width: 30, Help: "comma separated"},
		{Label: "Notes", Kind: FieldText, Value: current.Notes, Width: 44},
	}

	t.ctx.Open(NewForm("txn.save", title, fields, func(f *Form) tea.Cmd {
		amount, err := api.ParseAmount(f.Value("Amount"))
		if err != nil {
			return nil
		}
		values := txnFormValues{
			accountID:   f.Value("Account"),
			date:        f.Value("Date"),
			description: f.Value("Description"),
			amount:      amount,
			txnType:     f.Value("Type"),
			categoryID:  f.Value("Category"),
			payeeID:     f.Value("Payee"),
			tags:        splitList(f.Value("Tags")),
			notes:       f.Value("Notes"),
		}
		if creating {
			return act("txn.save", "transaction created", true, func(ctx context.Context) error {
				_, err := t.ctx.Client.CreateTransaction(ctx, values.create())
				return err
			})
		}
		id := current.ID
		return act("txn.save", "transaction updated", false, func(ctx context.Context) error {
			req := values.update()
			req.CategoryID = optionalID(f.Value("Category"))
			req.PayeeID = optionalID(f.Value("Payee"))
			return t.ctx.Client.UpdateTransaction(ctx, id, req)
		})
	}))
}

// txnFormValues carries the form's contents across the async boundary.
type txnFormValues struct {
	accountID   string
	date        string
	description string
	amount      api.Amount
	txnType     string
	categoryID  string
	payeeID     string
	tags        []string
	notes       string
}

// create builds the POST body.
func (v txnFormValues) create() api.CreateTransactionRequest {
	req := api.CreateTransactionRequest{
		AccountID:   v.accountID,
		Date:        v.date,
		Description: v.description,
		Amount:      v.amount,
		Type:        v.txnType,
		Tags:        v.tags,
		Notes:       v.notes,
	}
	if v.categoryID != "" {
		req.CategoryID = &v.categoryID
	}
	if v.payeeID != "" {
		req.PayeeID = &v.payeeID
	}
	return req
}

// update builds the PATCH body. An empty category or payee is sent as an
// explicit null, which is what clears the column.
func (v txnFormValues) update() api.UpdateTransactionRequest {
	return api.UpdateTransactionRequest{
		Date:        &v.date,
		Description: &v.description,
		Amount:      &v.amount,
		Type:        &v.txnType,
		AccountID:   &v.accountID,
		Tags:        &v.tags,
		Notes:       &v.notes,
	}
}

// confirmDelete deletes the selection, or the cursor row when nothing is
// selected.
func (t *Transactions) confirmDelete() {
	ids := t.selectedIDs()
	if len(ids) == 0 {
		row, ok := t.current()
		if !ok {
			return
		}
		ids = []string{row.ID}
	}
	body := fmt.Sprintf("Delete %d transaction(s)?", len(ids))
	detail := "This cannot be undone. Transactions on closed accounts are skipped by the API."
	t.ctx.Open(NewConfirm("Delete transactions", body, true, func() tea.Cmd {
		return act("txn.delete", fmt.Sprintf("deleted %d transaction(s)", len(ids)), false, func(ctx context.Context) error {
			if len(ids) == 1 {
				return t.ctx.Client.DeleteTransaction(ctx, ids[0])
			}
			_, err := t.ctx.Client.BulkDelete(ctx, ids)
			return err
		})
	}).WithDetail(detail))
}

// openBulkCategoryForm reassigns one category across the selection.
func (t *Transactions) openBulkCategoryForm() {
	ids := t.selectedIDs()
	options := append([]Option{{Value: api.UncategorizedCategory, Label: "Uncategorized (clear)"}}, t.ctx.Ref.CategoryOptions()...)
	fields := []Field{SelectField("Category", "", options, true)}
	t.ctx.Open(NewForm("txn.bulk", "Categorize selection", fields, func(f *Form) tea.Cmd {
		categoryID := f.Value("Category")
		return act("txn.bulk", fmt.Sprintf("categorized %d transaction(s)", len(ids)), true, func(ctx context.Context) error {
			_, err := t.ctx.Client.BulkCategorize(ctx, ids, categoryID)
			return err
		})
	}))
}

// openBulkPayeeForm reassigns one payee across the selection.
func (t *Transactions) openBulkPayeeForm() {
	ids := t.selectedIDs()
	fields := []Field{SelectField("Payee", "", t.ctx.Ref.PayeeOptions(), true)}
	t.ctx.Open(NewForm("txn.bulk", "Set payee on selection", fields, func(f *Form) tea.Cmd {
		payeeID := f.Value("Payee")
		return act("txn.bulk", fmt.Sprintf("updated %d transaction(s)", len(ids)), true, func(ctx context.Context) error {
			_, err := t.ctx.Client.BulkPayee(ctx, ids, payeeID)
			return err
		})
	}))
}

// startBulkCycle loads the billing cycles when the selection shares one account,
// because a cycle only belongs to the account it was generated for.
func (t *Transactions) startBulkCycle() tea.Cmd {
	accountID, ok := t.selectionAccount()
	if !ok {
		t.ctx.Notify(LevelError, "select transactions from a single account to set a billing cycle")
		return nil
	}
	return load("txn.cycles", func(ctx context.Context) ([]api.BillingCycle, error) {
		return t.ctx.Client.ListBillingCycles(ctx, accountID)
	})
}

// openBulkCycleForm presents the cycles fetched for the selection's account.
func (t *Transactions) openBulkCycleForm(cycles []api.BillingCycle) {
	if len(cycles) == 0 {
		t.ctx.Notify(LevelError, "that account has no billing cycles (set a billing day first)")
		return
	}
	options := make([]Option, 0, len(cycles))
	for _, c := range cycles {
		options = append(options, Option{Value: c.ID, Label: fmt.Sprintf("%s (%s … %s)", c.Label, c.StartDate.Format(api.DateLayout), c.EndDate.Format(api.DateLayout))})
	}
	ids := t.selectedIDs()
	fields := []Field{SelectField("Billing cycle", "", options, true)}
	t.ctx.Open(NewForm("txn.bulk", "Attach billing cycle", fields, func(f *Form) tea.Cmd {
		cycleID := f.Value("Billing cycle")
		return act("txn.bulk", fmt.Sprintf("attached %d transaction(s)", len(ids)), true, func(ctx context.Context) error {
			_, err := t.ctx.Client.BulkBillingCycle(ctx, ids, cycleID)
			return err
		})
	}))
}

// openBulkTagsForm adds and/or removes tags across the selection.
func (t *Transactions) openBulkTagsForm() {
	ids := t.selectedIDs()
	allTags := make([]Option, 0, len(t.ctx.Ref.Tags))
	for _, tc := range t.ctx.Ref.Tags {
		allTags = append(allTags, Option{Value: tc.Name, Label: fmt.Sprintf("%s (%d)", tc.Name, tc.Count)})
	}
	fields := []Field{
		SelectField("Add existing", "", allTags, false),
		SelectField("Remove existing", "", allTags, false),
		{Label: "Add new tags", Kind: FieldText, Width: 30, Help: "comma separated, added before any removal"},
	}
	t.ctx.Open(NewForm("txn.bulk", "Tags on selection", fields, func(f *Form) tea.Cmd {
		add := splitList(f.Value("Add new tags"))
		if value := f.Value("Add existing"); value != "" {
			add = append(add, value)
		}
		remove := splitList(f.Value("Remove existing"))
		if len(add) == 0 && len(remove) == 0 {
			t.ctx.Notify(LevelError, "nothing to add or remove")
			f.Close()
			return nil
		}
		return act("txn.bulk", fmt.Sprintf("updated %d transaction(s)", len(ids)), true, func(ctx context.Context) error {
			_, err := t.ctx.Client.BulkTags(ctx, ids, add, remove)
			return err
		})
	}))
}

// openBulkLoanForm attaches the selection to a loan/EMI account.
func (t *Transactions) openBulkLoanForm() {
	loans := t.ctx.Ref.LoanAccounts()
	if len(loans) == 0 {
		t.ctx.Notify(LevelError, "no loan/EMI accounts exist")
		return
	}
	options := make([]Option, 0, len(loans))
	for _, loan := range loans {
		options = append(options, Option{Value: loan.ID, Label: loan.Name})
	}
	ids := t.selectedIDs()
	fields := []Field{SelectField("Loan account", "", options, true)}
	t.ctx.Open(NewForm("txn.bulk", "Attach to loan / EMI", fields, func(f *Form) tea.Cmd {
		loanID := f.Value("Loan account")
		return act("txn.bulk", fmt.Sprintf("attached %d transaction(s)", len(ids)), true, func(ctx context.Context) error {
			_, err := t.ctx.Client.BulkAttachLoan(ctx, ids, loanID)
			return err
		})
	}))
}

// confirmBulkDetachLoan detaches the selection from its loan attachment.
func (t *Transactions) confirmBulkDetachLoan() {
	ids := t.selectedIDs()
	t.ctx.Open(NewConfirm("Detach from loan", fmt.Sprintf("Detach %d transaction(s) from their loan account?", len(ids)), false, func() tea.Cmd {
		return act("txn.bulk", fmt.Sprintf("detached %d transaction(s)", len(ids)), true, func(ctx context.Context) error {
			_, err := t.ctx.Client.BulkDetachLoan(ctx, ids)
			return err
		})
	}).WithDetail("Payees are left unchanged."))
}

// openExportForm streams the current filter to a CSV file.
func (t *Transactions) openExportForm() {
	filter := t.filter
	fields := []Field{
		{Label: "File", Kind: FieldText, Value: "fintrak-transactions.csv", Width: 44, Validate: required("file path")},
	}
	t.ctx.Open(NewForm("txn.export", "Export transactions (current filters)", fields, func(f *Form) tea.Cmd {
		path := f.Value("File")
		return act("txn.export", "exported to "+path, false, func(ctx context.Context) error {
			file, err := os.Create(path)
			if err != nil {
				return err
			}
			defer file.Close()
			_, err = t.ctx.Client.ExportTransactionsCSV(ctx, filter, file)
			return err
		})
	}))
}

// selectionAccount reports the shared account of the selection.
func (t *Transactions) selectionAccount() (string, bool) {
	account := ""
	for _, row := range t.rows {
		if !t.selected[row.ID] {
			continue
		}
		if account == "" {
			account = row.AccountID
			continue
		}
		if account != row.AccountID {
			return "", false
		}
	}
	return account, account != ""
}

// View implements Screen.
func (t *Transactions) View(width, height int) string {
	header := t.headerLine(width)
	if t.searching {
		header = t.ctx.Theme.Title.Render("/") + t.search.View()
	}
	body := t.table.View(t.ctx.Theme, width, listRows(height), "no transactions match these filters")
	parts := []string{header, body, ""}
	if detail := t.detailLine(width); detail != "" {
		parts = append(parts, detail)
	}
	parts = append(parts, "space select · f filter · n new · enter detail · x export")
	return trimToBox(strings.Join(parts, "\n"), width, height)
}

// headerLine summarises the active filters and paging.
func (t *Transactions) headerLine(width int) string {
	th := t.ctx.Theme
	var bits []string
	if t.filter.Search != "" {
		bits = append(bits, "search="+t.filter.Search)
	}
	if t.filter.AccountID != "" {
		bits = append(bits, "account="+t.ctx.Ref.AccountName(t.filter.AccountID))
	}
	if t.filter.CategoryID != "" {
		bits = append(bits, "category="+t.ctx.Ref.CategoryName(t.filter.CategoryID))
	}
	if t.filter.GroupID != "" {
		bits = append(bits, "group="+t.ctx.Ref.GroupName(t.filter.GroupID))
	}
	if t.filter.PayeeID != "" {
		bits = append(bits, "payee="+t.ctx.Ref.PayeeName(t.filter.PayeeID))
	}
	if t.filter.Type != "" {
		bits = append(bits, "type="+t.filter.Type)
	}
	if t.filter.Linked != nil {
		bits = append(bits, fmt.Sprintf("linked=%t", *t.filter.Linked))
	}
	if t.filter.DateFrom != "" || t.filter.DateTo != "" {
		bits = append(bits, "dates="+defaultTo(t.filter.DateFrom, "…")+"…"+defaultTo(t.filter.DateTo, "…"))
	}
	if len(t.filter.Tags) > 0 {
		bits = append(bits, "tags="+strings.Join(t.filter.Tags, ","))
	}
	summary := fmt.Sprintf("%d transaction(s) · page %d/%d · sort %s %s",
		t.info.Total, max(1, t.info.Page), max(1, t.info.Pages), defaultTo(t.filter.SortBy, "date"), t.filter.SortOrder)
	if len(t.selected) > 0 {
		summary += fmt.Sprintf(" · %d selected", len(t.selected))
	}
	line := th.Title.Render(summary)
	if len(bits) > 0 {
		line += "  " + th.Subtle.Render(truncate(strings.Join(bits, " · "), max(10, width-len(summary)-4)))
	}
	return line
}

// detailLine describes the cursor row.
func (t *Transactions) detailLine(width int) string {
	row, ok := t.current()
	if !ok {
		return ""
	}
	th := t.ctx.Theme
	parts := []string{"id " + truncate(row.ID, 8)}
	if row.IsLinked {
		parts = append(parts, "linked")
	}
	if row.LoanAccountName != "" {
		parts = append(parts, "loan "+row.LoanAccountName)
	}
	if row.RecurringSeriesNm != "" {
		parts = append(parts, "series "+row.RecurringSeriesNm)
	}
	if row.BillingCycleLabel != "" {
		parts = append(parts, "cycle "+row.BillingCycleLabel)
	}
	if len(row.Tags) > 0 {
		parts = append(parts, "tags "+strings.Join(row.Tags, ","))
	}
	return th.Subtle.Render(truncate(strings.Join(parts, " · "), width))
}

// txnDetail renders the detail overlay, including the link graph around the
// transaction.
func txnDetail(row api.Transaction, links []api.Link, ref *RefData) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Date         %s\n", row.Date.Format(api.DateLayout))
	fmt.Fprintf(&b, "Description  %s\n", row.Description)
	fmt.Fprintf(&b, "Amount       %s (%s)\n", row.Amount.Display(), row.Type)
	fmt.Fprintf(&b, "Account      %s\n", defaultTo(row.AccountName, ref.AccountName(row.AccountID)))
	fmt.Fprintf(&b, "Category     %s\n", categoryOr(row.CategoryName, row.CategoryID))
	fmt.Fprintf(&b, "Payee        %s\n", payeeOr(row.Payee, row.PayeeID))
	if row.BillingCycleLabel != "" {
		fmt.Fprintf(&b, "Billing cycle %s\n", row.BillingCycleLabel)
	}
	if row.LoanAccountName != "" {
		fmt.Fprintf(&b, "Loan         %s\n", row.LoanAccountName)
	}
	if row.RecurringSeriesNm != "" {
		fmt.Fprintf(&b, "Series       %s\n", row.RecurringSeriesNm)
	}
	if len(row.Tags) > 0 {
		fmt.Fprintf(&b, "Tags         %s\n", strings.Join(row.Tags, ", "))
	}
	if row.Notes != "" {
		fmt.Fprintf(&b, "Notes        %s\n", row.Notes)
	}
	fmt.Fprintf(&b, "Id           %s\n", row.ID)

	b.WriteString("\nLinks\n")
	if len(links) == 0 {
		b.WriteString("  (none — no transfer, refund, cashback or bill payment is recorded against this transaction)\n")
		return b.String()
	}
	for _, link := range links {
		other := link.ToTxn
		direction := "→"
		if link.ToTxnID == row.ID {
			other, direction = link.FromTxn, "←"
		}
		name := "?"
		amount := ""
		date := ""
		if other != nil {
			name = defaultTo(other.Description, other.ID)
			amount = other.Amount.Display()
			date = other.Date.Format(api.DateLayout)
			if other.AccountName != "" {
				name += " @" + other.AccountName
			}
		}
		fmt.Fprintf(&b, "  %s %-12s %s %s  %s\n", direction, link.Type, date, amount, name)
		if link.Notes != "" {
			fmt.Fprintf(&b, "      note: %s\n", link.Notes)
		}
	}
	return b.String()
}
