package ui

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/fintrak/tui/internal/api"
)

func init() {
	registerScreen(30, func(ctx *Ctx) Screen { return NewAccounts(ctx) })
}

// Accounts is the account manager: every account with its type, bank, currency,
// billing day, balance and closed marker, with inline create/edit/delete,
// billing-cycle and loan-schedule inspection, and per-account CSV export.
//
// Two API behaviours shape this screen. An account's balance is computed by the
// server — from the account's transaction sums, or from the EMI payments
// attached to a loan account — so the screen only ever prints the number it was
// handed. And creating, renaming or deleting an account also creates, renames
// or deletes the account-linked payee, which is why every successful save
// invalidates the shared reference data instead of merely reloading this list.
type Accounts struct {
	ctx   *Ctx
	table Table

	accounts []api.Account

	// detailAccount, cyclesAccount and scheduleAccount record which account
	// asked for the data now in flight, so a slow response opens the overlay for
	// that account even if the cursor has moved meanwhile. transferAccount is
	// the loan that gave a balance away, whose recast table is shown once the
	// transfer reports back.
	detailAccount   api.Account
	cyclesAccount   api.Account
	scheduleAccount api.Account
	transferAccount api.Account

	// deletedTransactions carries DeleteAccount's count back from the mutation
	// goroutine: the number only exists once the call has returned, and a
	// channel hands it to the event loop without the data race a plain field
	// would introduce.
	deletedTransactions chan int64

	keys accountsKeys
}

// accountsKeys are the screen's bindings. Refresh is listed although the App
// consumes `r` before any screen sees it: the binding is what documents the key
// on the status bar and in the help overlay.
type accountsKeys struct {
	Refresh    key.Binding
	New        key.Binding
	Edit       key.Binding
	Delete     key.Binding
	Detail     key.Binding
	Cycles     key.Binding
	Export     key.Binding
	Loan       key.Binding
	SetLoan    key.Binding
	DeleteLoan key.Binding
	Transfer   key.Binding
}

func newAccountsKeys() accountsKeys {
	return accountsKeys{
		Refresh:    key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "refresh")),
		New:        key.NewBinding(key.WithKeys("n"), key.WithHelp("n", "new")),
		Edit:       key.NewBinding(key.WithKeys("e"), key.WithHelp("e", "edit")),
		Delete:     key.NewBinding(key.WithKeys("d"), key.WithHelp("d", "delete")),
		Detail:     key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "detail")),
		Cycles:     key.NewBinding(key.WithKeys("b"), key.WithHelp("b", "billing cycles")),
		Export:     key.NewBinding(key.WithKeys("x"), key.WithHelp("x", "export csv")),
		Loan:       key.NewBinding(key.WithKeys("l"), key.WithHelp("l", "loan schedule")),
		SetLoan:    key.NewBinding(key.WithKeys("L"), key.WithHelp("L", "set schedule")),
		DeleteLoan: key.NewBinding(key.WithKeys("X"), key.WithHelp("X", "delete schedule")),
		Transfer:   key.NewBinding(key.WithKeys("t"), key.WithHelp("t", "transfer balance")),
	}
}

// NewAccounts builds the account screen.
func NewAccounts(ctx *Ctx) *Accounts {
	a := &Accounts{
		ctx:                 ctx,
		deletedTransactions: make(chan int64, 1),
		keys:                newAccountsKeys(),
	}
	a.table.SetColumns(
		Column{Title: "Name"},
		Column{Title: "Type", Width: 14},
		Column{Title: "Bank", Width: 16},
		Column{Title: "Currency", Width: 9},
		Column{Title: "Billing", Width: 8, Align: AlignRight},
		Column{Title: "Balance", Width: 15, Align: AlignRight},
	)
	return a
}

// Title implements Screen.
func (a *Accounts) Title() string { return "Accounts" }

// Keys implements Screen.
func (a *Accounts) Keys() []key.Binding {
	return []key.Binding{
		a.keys.Refresh, a.keys.New, a.keys.Edit, a.keys.Delete, a.keys.Detail,
		a.keys.Cycles, a.keys.Export, a.keys.Loan, a.keys.SetLoan, a.keys.DeleteLoan,
		a.keys.Transfer,
	}
}

// Refresh implements Screen.
func (a *Accounts) Refresh() tea.Cmd { return a.reload() }

// reload fetches the account list. It is safe to call repeatedly: the response
// replaces the rows wholesale and the table clamps the cursor itself.
func (a *Accounts) reload() tea.Cmd {
	return load("accounts.list", func(ctx context.Context) ([]api.Account, error) {
		return a.ctx.Client.ListAccounts(ctx)
	})
}

// Update implements Screen.
func (a *Accounts) Update(msg tea.Msg) tea.Cmd {
	switch m := msg.(type) {
	case loaded[[]api.Account]:
		if m.tag != "accounts.list" {
			break
		}
		if m.err != nil {
			a.ctx.Notify(LevelError, "%s", m.err)
			return nil
		}
		a.accounts = m.data
		a.applyRows()
		return nil

	case loaded[[]api.BillingCycle]:
		if m.tag != "accounts.detail" && m.tag != "accounts.cycles" {
			break
		}
		if m.err != nil {
			a.ctx.Notify(LevelError, "%s", m.err)
			return nil
		}
		if m.tag == "accounts.detail" {
			a.ctx.Open(NewInfo("Account · "+a.detailAccount.Name, accountsDetail(a.detailAccount, m.data)))
			return nil
		}
		a.ctx.Open(NewInfo("Billing cycles · "+a.cyclesAccount.Name, accountsCyclesText(a.cyclesAccount, m.data)))
		return nil

	case loaded[api.LoanScheduleDetail]:
		if m.tag != "accounts.schedule" {
			break
		}
		if m.err != nil {
			a.ctx.Notify(LevelError, "%s", m.err)
			return nil
		}
		title := "Loan schedule · " + defaultTo(m.data.LoanAccountName, a.scheduleAccount.Name)
		modal := NewInfo(title, accountsLoanScheduleText(m.data))
		if m.data.Schedule == nil {
			// The API answers 200 with a null schedule for a loan that has no
			// terms yet, so this is a normal state, not a failure.
			modal = modal.WithFooter("close with esc, then press L to set the terms")
		}
		a.ctx.Open(modal)
		return nil

	case done:
		if !strings.HasPrefix(m.tag, "accounts.") {
			break
		}
		if m.err != nil {
			// A form keeps what the user typed and shows the API's message
			// itself; the App has already put it on the status line.
			return nil
		}
		return a.afterMutation(m.tag)

	case tea.KeyMsg:
		return a.handleKey(m)
	}
	return nil
}

// afterMutation reacts to a successful accounts.* mutation. The account list is
// always refetched because the App's reference-data refresh only targets the
// visible screen, which this one may not be when the mutation completes.
func (a *Accounts) afterMutation(tag string) tea.Cmd {
	switch tag {
	case "accounts.export", "accounts.schedule.delete":
		// Neither touched an account: the listing on screen is still accurate.
		return nil
	case "accounts.schedule.save":
		// Show the table the API generated from the accepted terms.
		return a.loadSchedule(a.scheduleAccount)
	case "accounts.transfer":
		// The transfer regenerated both loans' tables; the source's shows the
		// cancelled installments and the transfer row, and the balances the
		// list prints moved with it.
		return tea.Batch(a.reload(), a.loadSchedule(a.transferAccount))
	case "accounts.delete":
		a.ctx.Notify(LevelSuccess, "%s", a.deletionNote())
	}
	return a.reload()
}

// handleKey routes a key press. Refresh is deliberately absent: the App handles
// `r` globally and calls Refresh.
func (a *Accounts) handleKey(msg tea.KeyMsg) tea.Cmd {
	switch {
	case keyMatches(a.keys.New, msg):
		a.openForm(nil)
		return nil
	case keyMatches(a.keys.Edit, msg):
		if account, ok := a.current(); ok {
			a.openForm(&account)
		}
		return nil
	case keyMatches(a.keys.Delete, msg):
		if account, ok := a.current(); ok {
			a.confirmDelete(account)
		}
		return nil
	case keyMatches(a.keys.Detail, msg):
		return a.openDetail()
	case keyMatches(a.keys.Cycles, msg):
		return a.openCycles()
	case keyMatches(a.keys.Export, msg):
		if account, ok := a.current(); ok {
			a.openExportForm(account)
		}
		return nil
	case keyMatches(a.keys.Loan, msg):
		return a.openLoanSchedule()
	case keyMatches(a.keys.SetLoan, msg):
		if account, ok := a.current(); ok {
			a.openScheduleForm(account)
		}
		return nil
	case keyMatches(a.keys.DeleteLoan, msg):
		if account, ok := a.current(); ok {
			a.confirmDeleteSchedule(account)
		}
		return nil
	case keyMatches(a.keys.Transfer, msg):
		if account, ok := a.current(); ok {
			a.openTransferForm(account)
		}
		return nil
	}

	switch msg.String() {
	case "up", "k":
		a.table.Move(-1)
	case "down", "j":
		a.table.Move(1)
	case "pgup", "ctrl+b":
		a.table.Page(-1, 20)
	case "pgdown", "ctrl+f":
		a.table.Page(1, 20)
	case "home":
		a.table.Home()
	case "end", "G":
		a.table.End()
	}
	return nil
}

// applyRows rebuilds the table from the fetched accounts. A closed account is
// muted and says so, because the API still lists it — its balance is simply
// frozen at whatever it held when it was closed.
func (a *Accounts) applyRows() {
	rows := make([][]Cell, 0, len(a.accounts))
	for _, account := range a.accounts {
		name := account.Name
		if account.IsDefault {
			name += " · default"
		}
		if account.Closed {
			name += " · closed"
		}
		nameCell := Text(name)
		if account.Closed {
			nameCell = Muted(name)
		}

		billingDay := "—"
		if account.BillingDay != nil {
			billingDay = strconv.Itoa(*account.BillingDay)
		}

		balance := Cell{Text: account.Balance.Display(), Role: RoleMoney}
		switch {
		case account.Closed:
			balance = Muted(balance.Text)
		case account.Balance.IsNegative():
			balance = Cell{Text: balance.Text, Role: RoleNegative}
		}

		rows = append(rows, []Cell{
			nameCell,
			Text(defaultTo(account.AccountTypeName, account.AccountTypeID)),
			Text(defaultTo(account.Bank, "—")),
			Text(defaultTo(account.Currency, "—")),
			Cell{Text: billingDay, Role: RoleMuted},
			balance,
		})
	}
	a.table.SetRows(rows)
}

// current returns the selected account. Rows map one-to-one onto accounts, so
// the cursor index is the account index.
func (a *Accounts) current() (api.Account, bool) {
	index := a.table.Cursor()
	if index < 0 || index >= len(a.accounts) {
		return api.Account{}, false
	}
	return a.accounts[index], true
}

// openForm opens the create or edit form. A nil row creates.
func (a *Accounts) openForm(row *api.Account) {
	types := a.ctx.Ref.AccountTypeOptions()
	if len(types) == 0 {
		a.ctx.Notify(LevelError, "no account types are available")
		return
	}

	creating := row == nil
	title := "New account"
	account := api.Account{Currency: "INR", Color: "#06b6d4", AccountTypeID: types[0].Value}
	if row != nil {
		title = "Edit account"
		account = *row
	}

	billingDay := ""
	if account.BillingDay != nil {
		billingDay = strconv.Itoa(*account.BillingDay)
	}

	fields := []Field{
		{Label: "Name", Kind: FieldText, Value: account.Name, Width: 32, Validate: required("name")},
		SelectField("Account type", account.AccountTypeID, types, true),
		{Label: "Bank", Kind: FieldText, Value: account.Bank, Width: 24},
		{
			Label: "Currency", Kind: FieldText, Value: defaultTo(account.Currency, "INR"),
			Width: 6, Validate: accountCurrency,
		},
		{
			Label: "Colour", Kind: FieldText, Value: defaultTo(account.Color, "#06b6d4"),
			Width: 10, Validate: accountHexColour, Help: "#rrggbb",
		},
		{
			Label: "Billing day", Kind: FieldText, Value: billingDay, Width: 4,
			Validate: accountBillingDay, Help: "1-31; blank means no statement periods",
		},
		BoolField("Default account", account.IsDefault),
	}
	if !creating {
		fields = append(fields, BoolField("Closed", account.Closed))
	}

	a.ctx.Open(NewForm("accounts.save", title, fields, func(f *Form) tea.Cmd {
		name := f.Value("Name")
		accountTypeID := f.Value("Account type")
		bank := f.Value("Bank")
		currency := f.Value("Currency")
		color := f.Value("Colour")
		isDefault := f.BoolValue("Default account")
		day := strings.TrimSpace(f.Value("Billing day"))

		if creating {
			req := api.CreateAccountRequest{
				Name:          name,
				AccountTypeID: accountTypeID,
				Bank:          bank,
				Currency:      currency,
				Color:         color,
				IsDefault:     isDefault,
			}
			if day != "" {
				// The API stores an omitted day as NULL, so a blank field is
				// left off the body rather than sent as zero.
				value := f.IntValue("Billing day")
				req.BillingDay = &value
			}
			return act("accounts.save", "account created", true, func(ctx context.Context) error {
				_, err := a.ctx.Client.CreateAccount(ctx, req)
				return err
			})
		}

		closed := f.BoolValue("Closed")
		req := api.UpdateAccountRequest{
			Name:          name,
			AccountTypeID: accountTypeID,
			Bank:          bank,
			Currency:      currency,
			Color:         color,
			IsDefault:     &isDefault,
			Closed:        &closed,
		}
		// BillingDay is an OptionalInt: its zero value is not dropped by
		// omitempty (a struct is never "empty" to encoding/json), so the key
		// always reaches the wire, as an explicit null when the field is blank.
		// That clears a day the account had, and is a no-op when the column was
		// already empty; a non-blank field sets it.
		if day != "" {
			req.BillingDay = api.Int(f.IntValue("Billing day"))
		} else {
			req.BillingDay = api.IntNull()
		}
		id := account.ID
		return act("accounts.save", "account updated", true, func(ctx context.Context) error {
			_, err := a.ctx.Client.UpdateAccount(ctx, id, req)
			return err
		})
	}))
}

// confirmDelete deletes the account. The transaction count only arrives with
// the response, so the dialog warns that the account's transactions go with it
// and the count is reported on the status line once it has happened.
func (a *Accounts) confirmDelete(account api.Account) {
	id := account.ID
	body := fmt.Sprintf("Delete %q and the transactions filed under it?", account.Name)
	detail := "The account's transactions, billing cycles and loan schedule are removed with it, " +
		"as is its linked payee, and the number of deleted transactions is reported afterwards. " +
		"This cannot be undone."
	a.ctx.Open(NewConfirm("Delete account", body, true, func() tea.Cmd {
		return act("accounts.delete", "account deleted", true, func(ctx context.Context) error {
			result, err := a.ctx.Client.DeleteAccount(ctx, id)
			if err != nil {
				return err
			}
			select {
			case a.deletedTransactions <- result.TransactionsDeleted:
			default:
			}
			return nil
		})
	}).WithDetail(detail))
}

// deletionNote reports what the last successful delete removed. The channel is
// only written on success, so an empty read means the mutation failed and the
// status line already carries its error.
func (a *Accounts) deletionNote() string {
	select {
	case count := <-a.deletedTransactions:
		if count <= 0 {
			return "account deleted"
		}
		return fmt.Sprintf("account deleted · %s removed with it",
			pluralise(int(count), "transaction", "transactions"))
	default:
		return "account deleted"
	}
}

// openDetail shows the account's fields and, for an account with a billing day,
// its billing cycles. The cycles are a second call, so the overlay opens when
// that response lands.
func (a *Accounts) openDetail() tea.Cmd {
	account, ok := a.current()
	if !ok {
		return nil
	}
	a.detailAccount = account
	if account.BillingDay == nil {
		a.ctx.Open(NewInfo("Account · "+account.Name, accountsDetail(account, nil)))
		return nil
	}
	return load("accounts.detail", func(ctx context.Context) ([]api.BillingCycle, error) {
		return a.ctx.Client.ListBillingCycles(ctx, account.ID)
	})
}

// openCycles lists the cursor account's billing cycles. An account without a
// billing day legitimately has none — the API answers with an empty list rather
// than an error, and the overlay says so instead of reporting a failure.
func (a *Accounts) openCycles() tea.Cmd {
	account, ok := a.current()
	if !ok {
		return nil
	}
	a.cyclesAccount = account
	return load("accounts.cycles", func(ctx context.Context) ([]api.BillingCycle, error) {
		return a.ctx.Client.ListBillingCycles(ctx, account.ID)
	})
}

// openExportForm streams the cursor account's transactions into a CSV file. The
// default filename is derived from the account so two exports do not overwrite
// each other.
func (a *Accounts) openExportForm(account api.Account) {
	id := account.ID
	fields := []Field{
		{Label: "File", Kind: FieldText, Value: accountExportName(account), Width: 44, Validate: required("file path")},
	}
	a.ctx.Open(NewForm("accounts.export", "Export "+account.Name+" as CSV", fields, func(f *Form) tea.Cmd {
		path := f.Value("File")
		return act("accounts.export", "exported to "+path, false, func(ctx context.Context) error {
			file, err := os.Create(path)
			if err != nil {
				return err
			}
			defer file.Close()
			_, err = a.ctx.Client.ExportAccountCSV(ctx, id, file)
			return err
		})
	}))
}

// openLoanSchedule shows the cursor account's amortization table. Only a
// loan/EMI account has one, so any other type is refused with a hint rather
// than an empty overlay.
func (a *Accounts) openLoanSchedule() tea.Cmd {
	account, ok := a.current()
	if !ok {
		return nil
	}
	if account.AccountTypeID != "loan" {
		a.ctx.Notify(LevelError, "%s is not a loan/EMI account", account.Name)
		return nil
	}
	return a.loadSchedule(account)
}

// loadSchedule fetches the amortization table. A loan without terms answers 200
// with a null schedule, which the overlay reports as "no schedule yet" instead
// of an error.
func (a *Accounts) loadSchedule(account api.Account) tea.Cmd {
	a.scheduleAccount = account
	return load("accounts.schedule", func(ctx context.Context) (api.LoanScheduleDetail, error) {
		return a.ctx.Client.LoanSchedule(ctx, account.ID)
	})
}

// openScheduleForm sets or replaces the loan's terms. The API generates the
// amortization table from them, so the form collects principal, the processing
// fee withheld from it, rate, tenure and the start and disbursal dates, and
// nothing else.
func (a *Accounts) openScheduleForm(account api.Account) {
	if account.AccountTypeID != "loan" {
		a.ctx.Notify(LevelError, "%s is not a loan/EMI account", account.Name)
		return
	}
	a.scheduleAccount = account
	principal := AmountField("Principal", "")
	principal.Validate = accountPositiveAmount
	fee := AmountField("Processing fee", "")
	fee.Validate = accountFee
	fee.Help = "deducted from the principal before amortization; blank means no fee"
	fields := []Field{
		principal,
		fee,
		{
			Label: "Annual rate (bps)", Kind: FieldText, Value: "", Width: 8,
			Validate: requiredInt, Help: "basis points: 950 = 9.50%",
		},
		{
			Label: "Tenure (months)", Kind: FieldText, Value: "", Width: 8,
			Validate: accountTenure, Help: "whole months, 1-600",
		},
		{Label: "Start date", Kind: FieldText, Value: nowDate(), Width: 14, Validate: requiredDate},
		{
			Label: "Disbursal date", Kind: FieldText, Value: "", Width: 14,
			Validate: optionalDate, Help: "YYYY-MM-DD; blank bills the first period as a whole month",
		},
	}
	a.ctx.Open(NewForm("accounts.schedule.save", "Loan terms · "+account.Name, fields, func(f *Form) tea.Cmd {
		amount, err := api.ParseAmount(f.Value("Principal"))
		if err != nil {
			// The field validator normally catches this before submit; keeping
			// the form open is what preserves the typed terms.
			f.SetError(err)
			return nil
		}
		fee, err := accountOptionalAmount(f.Value("Processing fee"))
		if err != nil {
			f.SetError(err)
			return nil
		}
		req := api.LoanScheduleRequest{
			Principal:     amount,
			ProcessingFee: fee,
			AnnualRateBps: f.IntValue("Annual rate (bps)"),
			TenureMonths:  f.IntValue("Tenure (months)"),
			StartDate:     f.Value("Start date"),
			DisbursalDate: strings.TrimSpace(f.Value("Disbursal date")),
		}
		return act("accounts.schedule.save", "loan schedule saved", true, func(ctx context.Context) error {
			_, err := a.ctx.Client.SetLoanSchedule(ctx, account.ID, req)
			return err
		})
	}))
}

// confirmDeleteSchedule removes the loan's terms and the table generated from
// them. The endpoint is idempotent, so a repeat is harmless.
func (a *Accounts) confirmDeleteSchedule(account api.Account) {
	if account.AccountTypeID != "loan" {
		a.ctx.Notify(LevelError, "%s is not a loan/EMI account", account.Name)
		return
	}
	id := account.ID
	body := fmt.Sprintf("Remove the amortization schedule of %q?", account.Name)
	a.ctx.Open(NewConfirm("Remove loan schedule", body, true, func() tea.Cmd {
		return act("accounts.schedule.delete", "loan schedule removed", false, func(ctx context.Context) error {
			_, err := a.ctx.Client.DeleteLoanSchedule(ctx, id)
			return err
		})
	}).WithDetail("The account and its transactions are untouched; only the terms are forgotten."))
}

// openTransferForm moves the cursor loan's remaining principal to another loan:
// the source is settled at its outstanding balance on the transfer date and the
// target's remaining installments are recast over that amount. The date
// defaults to today, which is when the source's balance is measured.
//
// The target terms are only used when the target has no schedule of its own —
// one that has a schedule recasts it — so they are checked against the target's
// fetched schedule when the transfer is submitted rather than guessed here.
func (a *Accounts) openTransferForm(source api.Account) {
	if source.AccountTypeID != "loan" {
		a.ctx.Notify(LevelError, "%s is not a loan/EMI account", source.Name)
		return
	}
	targets := a.transferTargets(source)
	if len(targets) == 0 {
		a.ctx.Notify(LevelError, "no other loan/EMI account to transfer the balance to")
		return
	}
	fields := []Field{
		SelectField("Target account", targets[0].Value, targets, true),
		{Label: "Transfer date", Kind: FieldText, Value: nowDate(), Width: 14, Validate: requiredDate},
		{
			Label: "Target rate (bps)", Kind: FieldText, Value: "", Width: 8,
			Validate: positiveInt, Help: "only when the target has no schedule: 950 = 9.50%",
		},
		{
			Label: "Target tenure (months)", Kind: FieldText, Value: "", Width: 8,
			Validate: optionalTenure, Help: "only when the target has no schedule: 1-600",
		},
		{
			Label: "Target start date", Kind: FieldText, Value: "", Width: 14,
			Validate: optionalDate, Help: "the target's first installment date; only without a schedule",
		},
	}
	a.transferAccount = source
	sourceID := source.ID
	a.ctx.Open(NewForm("accounts.transfer", "Balance transfer · "+source.Name, fields, func(f *Form) tea.Cmd {
		// The terms are read here, on the event loop: the lookup below runs off
		// it, where the form must not be touched.
		rateText := strings.TrimSpace(f.Value("Target rate (bps)"))
		tenureText := strings.TrimSpace(f.Value("Target tenure (months)"))
		startDate := strings.TrimSpace(f.Value("Target start date"))
		rate := f.IntValue("Target rate (bps)")
		tenure := f.IntValue("Target tenure (months)")
		req := api.LoanTransferRequest{
			ToLoanAccountID: f.Value("Target account"),
			TransferDate:    f.Value("Transfer date"),
		}
		return act("accounts.transfer", "balance transferred", true, func(ctx context.Context) error {
			// The target's own schedule decides whether its terms are used at
			// all: the API recasts a table that exists and ignores them, and
			// requires them when there is nothing to recast.
			detail, err := a.ctx.Client.LoanSchedule(ctx, req.ToLoanAccountID)
			if err != nil {
				return err
			}
			if detail.Schedule == nil {
				if rateText == "" || tenureText == "" || startDate == "" {
					return errText("the target loan has no schedule — give its rate, tenure and first installment date")
				}
				req.TargetAnnualRateBps = api.Int(rate)
				req.TargetTenureMonths = api.Int(tenure)
				req.TargetStartDate = startDate
			}
			_, err = a.ctx.Client.TransferLoanBalance(ctx, sourceID, req)
			return err
		})
	}))
}

// transferTargets lists the accounts a balance transfer may name: every other
// Loan / EMI account, because a loan cannot absorb its own balance. A closed one
// is offered but marked, since the API refuses to put new debt on it.
func (a *Accounts) transferTargets(source api.Account) []Option {
	options := make([]Option, 0, len(a.ctx.Ref.Accounts))
	for _, account := range a.ctx.Ref.Accounts {
		if account.ID == source.ID || account.AccountTypeID != "loan" {
			continue
		}
		label := account.Name
		if account.Closed {
			label += " · closed"
		}
		options = append(options, Option{Value: account.ID, Label: label})
	}
	return options
}

// accountCurrency accepts a blank currency (the API stores INR) or a
// three-character code. The column is VARCHAR(3), so a longer code would fail
// at the database rather than surface as a validation error.
func accountCurrency(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	if len(value) != 3 {
		return errText("expected a 3-character code, e.g. INR")
	}
	return nil
}

// accountHexColour accepts a blank colour (the API stores #06b6d4) or a #rrggbb
// hex triplet, which is what the colour column holds.
func accountHexColour(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	if len(value) != 7 || value[0] != '#' {
		return errText("expected #rrggbb")
	}
	for _, r := range value[1:] {
		if !strings.ContainsRune("0123456789abcdefABCDEF", r) {
			return errText("expected #rrggbb")
		}
	}
	return nil
}

// accountBillingDay accepts a blank day (which leaves the account without
// statement periods) or 1-31, the range the API enforces on create and update.
func accountBillingDay(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	day, err := strconv.Atoi(value)
	if err != nil || day < 1 || day > 31 {
		return errText("expected a day between 1 and 31")
	}
	return nil
}

// accountPositiveAmount accepts the API's amount grammar and rejects anything
// that is not above zero, which is what loan terms require.
func accountPositiveAmount(value string) error {
	amount, err := api.ParseAmount(value)
	if err != nil {
		return err
	}
	if amount.IsZero() || amount.IsNegative() {
		return errText("expected an amount above zero")
	}
	return nil
}

// accountFee validates the processing fee, which is optional: a blank field is
// a loan without a fee. The API refuses a negative fee and one that is not
// below the principal; only the sign can be judged here.
func accountFee(value string) error {
	_, err := accountOptionalAmount(value)
	return err
}

// accountOptionalAmount parses an optional amount, where blank means "none"
// rather than a missing value.
func accountOptionalAmount(value string) (api.Amount, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	amount, err := api.ParseAmount(value)
	if err != nil {
		return "", err
	}
	if amount.IsNegative() {
		return "", errText("expected an amount of zero or more")
	}
	return amount, nil
}

// accountTenure accepts the 1-600 month range the loan terms allow.
func accountTenure(value string) error {
	if err := requiredInt(value); err != nil {
		return err
	}
	months, _ := strconv.Atoi(strings.TrimSpace(value))
	if months < 1 || months > 600 {
		return errText("expected a tenure between 1 and 600 months")
	}
	return nil
}

// optionalTenure accepts a blank tenure — the form asks for one only when the
// target loan has no schedule to recast — or the range the terms allow.
func optionalTenure(value string) error {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return accountTenure(value)
}

// accountExportName derives a default CSV filename from the account name. Only
// characters that survive a filesystem are kept, so an account named "HDFC / OD"
// cannot suggest a path through a directory that does not exist.
func accountExportName(account api.Account) string {
	var b strings.Builder
	for _, r := range strings.ToLower(account.Name) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == ' ', r == '-', r == '_':
			b.WriteByte('-')
		}
	}
	name := strings.Trim(b.String(), "-")
	if name == "" {
		return "account.csv"
	}
	return "account-" + name + ".csv"
}

// accountsDetail renders the detail overlay: the account's fields followed by
// its billing cycles.
func accountsDetail(account api.Account, cycles []api.BillingCycle) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Type         %s\n", defaultTo(account.AccountTypeName, account.AccountTypeID))
	fmt.Fprintf(&b, "Bank         %s\n", defaultTo(account.Bank, "—"))
	fmt.Fprintf(&b, "Currency     %s\n", defaultTo(account.Currency, "—"))
	fmt.Fprintf(&b, "Colour       %s\n", defaultTo(account.Color, "—"))
	fmt.Fprintf(&b, "Balance      %s\n", account.Balance.Display())
	fmt.Fprintf(&b, "Default      %t\n", account.IsDefault)
	fmt.Fprintf(&b, "Closed       %t\n", account.Closed)
	if account.BillingDay != nil {
		fmt.Fprintf(&b, "Billing day  %d\n", *account.BillingDay)
	} else {
		b.WriteString("Billing day  — (no statement periods)\n")
	}
	fmt.Fprintf(&b, "Created      %s\n", formatDate(account.CreatedAt))
	fmt.Fprintf(&b, "Id           %s\n", account.ID)
	b.WriteString("\n")
	b.WriteString(accountsCyclesText(account, cycles))
	return b.String()
}

// accountsCyclesText renders an account's statement periods. Cycles are
// generated from the account's billing day, so an account without one has none
// by definition and the text says exactly that rather than looking broken.
func accountsCyclesText(account api.Account, cycles []api.BillingCycle) string {
	var b strings.Builder
	b.WriteString("Billing cycles\n")
	switch {
	case account.BillingDay == nil:
		b.WriteString("  none: this account has no billing day, so the API generates no statement periods for it\n")
		return b.String()
	case len(cycles) == 0:
		b.WriteString("  none yet\n")
		return b.String()
	}
	b.WriteString("  period                    label                 txns   outstanding\n")
	for _, cycle := range cycles {
		fmt.Fprintf(&b, "  %-24s  %-20s  %4d   %s\n",
			formatRange(cycle.StartDate, cycle.EndDate), truncate(cycle.Label, 20),
			cycle.TransactionCount, cycle.TotalOutstanding.Display())
	}
	return b.String()
}

// accountsLoanScheduleText renders the amortization table. Every figure is the
// server's: the table is generated from the terms, so nobody here derives an
// installment, a split or an outstanding balance.
func accountsLoanScheduleText(detail api.LoanScheduleDetail) string {
	if detail.Schedule == nil {
		return "This loan has no schedule yet.\n\n" +
			"The terms (principal, processing fee, annual rate, tenure, start and\n" +
			"disbursal dates) are stored per loan and the API generates the\n" +
			"amortization table from them."
	}
	schedule := detail.Schedule
	var b strings.Builder
	fmt.Fprintf(&b, "Principal          %s\n", schedule.Principal.Display())
	fmt.Fprintf(&b, "Processing fee     %s (reference only)\n", schedule.ProcessingFee.Display())
	fmt.Fprintf(&b, "Annual rate        %d bps\n", schedule.AnnualRateBps)
	fmt.Fprintf(&b, "Tenure             %s\n", pluralise(schedule.TenureMonths, "month", "months"))
	fmt.Fprintf(&b, "Start date         %s\n", formatDate(schedule.StartDate))
	fmt.Fprintf(&b, "Disbursal date     %s\n", formatDate(schedule.DisbursalDate))
	b.WriteString("\n")
	fmt.Fprintf(&b, "EMI                %s\n", detail.EMI.Display())
	fmt.Fprintf(&b, "Total interest     %s\n", detail.TotalInterest.Display())
	fmt.Fprintf(&b, "Total payable      %s\n", detail.TotalPayable.Display())
	fmt.Fprintf(&b, "Paid installments  %d of %d\n", detail.PaidInstallments, len(detail.Entries))
	fmt.Fprintf(&b, "Paid so far        %s (principal %s · interest %s)\n",
		detail.PaidAmount.Display(), detail.PrincipalPaid.Display(), detail.InterestPaid.Display())
	fmt.Fprintf(&b, "Outstanding        %s\n", detail.OutstandingPrincipal.Display())
	fmt.Fprintf(&b, "Next due           %s\n", formatDatePtr(detail.NextDueDate))
	fmt.Fprintf(&b, "Completed          %t\n", detail.Completed)
	fmt.Fprintf(&b, "Settled on         %s\n", formatDatePtr(detail.SettledOn))
	b.WriteString("\n")
	b.WriteString(accountsTransfersText(detail))
	b.WriteString("\nInstallments\n")
	if len(detail.Entries) == 0 {
		b.WriteString("  none\n")
		return b.String()
	}
	b.WriteString("    #  due         amount         principal      interest       balance        state\n")
	for _, entry := range detail.Entries {
		fmt.Fprintf(&b, "  %3d  %-10s  %-13s  %-13s  %-13s  %-13s  %s\n",
			entry.Number, formatDate(entry.DueDate), entry.Amount.Display(), entry.Principal.Display(),
			entry.Interest.Display(), entry.Balance.Display(), loanEntryState(entry))
	}
	return b.String()
}

// accountsTransfersText renders the balance transfers a loan took part in from
// its own point of view: "out" is one that settled this loan, "in" one whose
// amount it absorbed. The counterparty is the other side of the transfer.
func accountsTransfersText(detail api.LoanScheduleDetail) string {
	var b strings.Builder
	b.WriteString("Transfers\n")
	if len(detail.Transfers) == 0 {
		b.WriteString("  none\n")
		return b.String()
	}
	loanID := detail.Schedule.LoanAccountID
	b.WriteString("  dir  date        counterparty                    amount\n")
	for _, transfer := range detail.Transfers {
		direction, counterparty := "in ", defaultTo(transfer.FromLoanAccountName, transfer.FromLoanAccountID)
		if transfer.FromLoanAccountID == loanID {
			direction, counterparty = "out", defaultTo(transfer.ToLoanAccountName, transfer.ToLoanAccountID)
		}
		fmt.Fprintf(&b, "  %s  %-10s  %-28s  %s\n",
			direction, formatDate(transfer.TransferDate), truncate(counterparty, 28), transfer.Amount.Display())
	}
	return b.String()
}

// loanEntryState names an installment's state. A transfer-voided installment is
// cancelled and can no longer be paid; a recast one was regenerated over a
// transferred balance, and either can also be covered by a payment.
func loanEntryState(entry api.LoanScheduleEntry) string {
	if entry.Cancelled {
		return "cancelled"
	}
	switch {
	case entry.Recast && entry.Paid:
		return "recast · paid"
	case entry.Recast:
		return "recast"
	case entry.Paid:
		return "paid"
	}
	return "due"
}

// headerLine summarises the list: how many accounts there are, how many are
// closed, and which one is the default.
func (a *Accounts) headerLine(width int) string {
	open, closed := 0, 0
	defaultName := "—"
	for _, account := range a.accounts {
		if account.Closed {
			closed++
		} else {
			open++
		}
		if account.IsDefault {
			defaultName = account.Name
		}
	}
	summary := pluralise(open, "open account", "open accounts")
	if closed > 0 {
		summary += fmt.Sprintf(" · %d closed", closed)
	}
	hint := truncate("default "+defaultName, max(10, width-len(summary)-4))
	return a.ctx.Theme.Title.Render(summary) + "  " + a.ctx.Theme.Subtle.Render(hint)
}

// detailLine describes the cursor account, so the facts that fit on one line do
// not need a modal.
func (a *Accounts) detailLine(width int) string {
	account, ok := a.current()
	if !ok {
		return ""
	}
	parts := []string{"id " + truncateID(account.ID)}
	if account.BillingDay != nil {
		parts = append(parts, fmt.Sprintf("billing day %d", *account.BillingDay))
	} else {
		parts = append(parts, "no billing day")
	}
	parts = append(parts, "colour "+defaultTo(account.Color, "—"))
	if account.AccountTypeID == "loan" {
		parts = append(parts, "loan/EMI — l for the schedule, t to transfer")
	}
	return a.ctx.Theme.Subtle.Render(truncate(strings.Join(parts, " · "), width))
}

// View implements Screen.
func (a *Accounts) View(width, height int) string {
	body := a.table.View(a.ctx.Theme, width, listRows(height), "no accounts yet — press n to create one")
	parts := []string{a.headerLine(width), body, ""}
	if detail := a.detailLine(width); detail != "" {
		parts = append(parts, detail)
	}
	parts = append(parts, "n new · e edit · d delete · enter detail · b cycles · x export · l loan schedule · t transfer")
	return trimToBox(strings.Join(parts, "\n"), width, height)
}
