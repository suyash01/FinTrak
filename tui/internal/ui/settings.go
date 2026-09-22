package ui

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/fintrak/client/api"
)

func init() {
	registerScreen(130, func(ctx *Ctx) Screen { return NewSettings(ctx) })
}

// settingsPane is the region of this screen the cursor keys belong to. The
// screen needs the distinction because `e` means two different things: editing
// the Paperless settings in the overview, and editing the selected account type
// in the catalog. Making the pane explicit keeps `e` unambiguous instead of
// guessing from what happens to be on screen.
type settingsPane int

// Panes of the settings screen.
const (
	settingsOverview settingsPane = iota
	settingsCatalog
)

// settingsPageSizeClear is the select value that sends an explicit null for the
// Paperless page size, which is what restores the server's default. It is the
// only way to clear that setting from here: the client's OptionalInt always
// marshals a value — its zero value is written as null, exactly like IntNull()
// — so "not provided" cannot be put on the wire, and every other choice has to
// name the size it wants.
const settingsPageSizeClear = "clear"

// settingsTypeIDPattern mirrors the server's slug rule for a custom account
// type id (backend/handlers/account_type.go). The API answers 400 otherwise, so
// the create form applies the same rule locally.
var settingsTypeIDPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{1,29}$`)

// Settings is the integration and administration screen: the Paperless-ngx
// connection, backup export and restore, a summary of the signed-in session,
// and — for an admin — the account-type catalog.
//
// The catalog is the only data this screen mutates directly; the Paperless
// settings are refetched after every save, because the update response echoes
// back just the fields that were sent and is therefore never a complete view of
// what is stored.
type Settings struct {
	ctx  *Ctx
	keys settingsKeys

	admin bool

	paperless       api.PaperlessSettingsResponse
	paperlessLoaded bool

	types       []api.AccountType
	typesLoaded bool
	table       Table
	pane        settingsPane

	// restore and restorePath carry the report of the last successful
	// ImportBackup from the worker goroutine back to the event loop. The act
	// closure writes them off the loop and only the done branch reads them; the
	// done message is delivered over a channel after the write, which orders the
	// two, so no other goroutine ever observes a half-written report.
	restore     api.BackupImportResult
	restorePath string
}

// settingsKeys are the screen's bindings. EditPaperless and EditType share `e`
// because only one of them is ever live: the catalog pane owns `e` while it is
// open, and the overview owns it otherwise.
type settingsKeys struct {
	EditPaperless key.Binding
	Export        key.Binding
	Import        key.Binding
	Catalog       key.Binding
	NewType       key.Binding
	EditType      key.Binding
	DeleteType    key.Binding
	Back          key.Binding
}

func newSettingsKeys() settingsKeys {
	return settingsKeys{
		EditPaperless: key.NewBinding(key.WithKeys("e"), key.WithHelp("e", "paperless settings")),
		Export:        key.NewBinding(key.WithKeys("x"), key.WithHelp("x", "export backup")),
		Import:        key.NewBinding(key.WithKeys("i"), key.WithHelp("i", "restore backup")),
		Catalog:       key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "account types")),
		NewType:       key.NewBinding(key.WithKeys("n"), key.WithHelp("n", "new account type")),
		EditType:      key.NewBinding(key.WithKeys("e"), key.WithHelp("e", "edit type")),
		DeleteType:    key.NewBinding(key.WithKeys("d"), key.WithHelp("d", "delete type")),
		Back:          key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "back")),
	}
}

// NewSettings builds the settings screen. The role is captured at construction
// because the admin gate never changes within a session.
func NewSettings(ctx *Ctx) *Settings {
	s := &Settings{
		ctx:   ctx,
		keys:  newSettingsKeys(),
		admin: ctx.User.Role == "admin",
	}
	s.table.SetColumns(
		Column{Title: "Id", Width: 16},
		Column{Title: "Name"},
		Column{Title: "Positive txn type", Width: 20},
	)
	return s
}

// Title implements Screen.
func (s *Settings) Title() string { return "Settings" }

// Keys implements Screen. The account-type catalog is admin-only, so a member
// never sees its keys — the API would refuse them with 403 anyway.
func (s *Settings) Keys() []key.Binding {
	if !s.admin {
		return []key.Binding{s.keys.EditPaperless, s.keys.Export, s.keys.Import}
	}
	if s.pane == settingsCatalog {
		return []key.Binding{s.keys.NewType, s.keys.EditType, s.keys.DeleteType, s.keys.Back}
	}
	return []key.Binding{s.keys.EditPaperless, s.keys.Export, s.keys.Import, s.keys.NewType, s.keys.Catalog}
}

// CapturesText implements Screen: this screen has no inline text input.
func (s *Settings) CapturesText() bool { return false }

// Refresh implements Screen.
func (s *Settings) Refresh() tea.Cmd {
	return tea.Batch(s.loadPaperless(), s.loadTypes())
}

// loadPaperless fetches the Paperless-ngx settings. The response never carries
// the stored API token: HasToken says whether one exists.
func (s *Settings) loadPaperless() tea.Cmd {
	return load("settings.paperless.list", func(ctx context.Context) (api.PaperlessSettingsResponse, error) {
		return s.ctx.Client.PaperlessSettings(ctx)
	})
}

// loadTypes fetches the account-type catalog. A member has no use for it and
// the App hides the section, so the call is skipped rather than made and
// discarded.
func (s *Settings) loadTypes() tea.Cmd {
	if !s.admin {
		return nil
	}
	return load("settings.types.list", func(ctx context.Context) ([]api.AccountType, error) {
		return s.ctx.Client.ListAccountTypes(ctx)
	})
}

// Update implements Screen.
func (s *Settings) Update(msg tea.Msg) tea.Cmd {
	switch m := msg.(type) {
	case loaded[api.PaperlessSettingsResponse]:
		if m.tag != "settings.paperless.list" {
			break
		}
		if m.err != nil {
			s.ctx.Notify(LevelError, "%s", m.err)
			return nil
		}
		s.paperless, s.paperlessLoaded = m.data, true
		return nil

	case loaded[[]api.AccountType]:
		if m.tag != "settings.types.list" {
			break
		}
		if m.err != nil {
			s.ctx.Notify(LevelError, "%s", m.err)
			return nil
		}
		s.types, s.typesLoaded = m.data, true
		s.applyTypes()
		return nil

	case done:
		if !strings.HasPrefix(m.tag, "settings.") {
			break
		}
		if m.err != nil {
			// The App has already shown the failure — inside the form for a
			// form-owned tag, on the status line otherwise.
			return nil
		}
		switch m.tag {
		case "settings.paperless":
			// The save response carries only the fields that were sent, so the
			// stored settings are re-read instead of merged from it.
			return s.loadPaperless()
		case "settings.types":
			return s.loadTypes()
		case "settings.import":
			s.ctx.Open(NewInfo("Restore complete", settingsRestoreReport(s.restorePath, s.restore)))
			return nil
		}
		return nil

	case tea.KeyMsg:
		return s.handleKey(m)
	}
	return nil
}

// handleKey routes a key press.
func (s *Settings) handleKey(msg tea.KeyMsg) tea.Cmd {
	if s.pane == settingsCatalog {
		return s.handleCatalogKey(msg)
	}

	switch {
	case keyMatches(s.keys.EditPaperless, msg):
		s.openPaperlessForm()
		return nil
	case keyMatches(s.keys.Export, msg):
		s.openExportForm()
		return nil
	case keyMatches(s.keys.Import, msg):
		s.openImportForm()
		return nil
	case s.admin && keyMatches(s.keys.NewType, msg):
		s.openCreateTypeForm()
		return nil
	case s.admin && keyMatches(s.keys.Catalog, msg):
		if !s.typesLoaded || len(s.types) == 0 {
			s.ctx.Notify(LevelError, "no account types to show")
			return nil
		}
		s.pane = settingsCatalog
		return nil
	}
	return nil
}

// handleCatalogKey routes a key press while the account-type catalog is open.
func (s *Settings) handleCatalogKey(msg tea.KeyMsg) tea.Cmd {
	switch {
	case keyMatches(s.keys.Back, msg):
		s.pane = settingsOverview
		return nil
	case keyMatches(s.keys.NewType, msg):
		s.openCreateTypeForm()
		return nil
	case keyMatches(s.keys.EditType, msg):
		s.openEditTypeForm()
		return nil
	case keyMatches(s.keys.DeleteType, msg):
		s.confirmDeleteType()
		return nil
	}

	switch msg.String() {
	case "up", "k":
		s.table.Move(-1)
	case "down", "j":
		s.table.Move(1)
	case "pgup", "ctrl+b":
		s.table.Page(-1, 10)
	case "pgdown", "ctrl+f":
		s.table.Page(1, 10)
	case "home":
		s.table.Home()
	case "end":
		s.table.End()
	}
	return nil
}

// applyTypes rebuilds the catalog table. Rows keep their order so the cursor
// index and the fetched slice stay aligned.
func (s *Settings) applyTypes() {
	rows := make([][]Cell, 0, len(s.types))
	for _, t := range s.types {
		rows = append(rows, []Cell{
			Muted(t.ID),
			Text(t.Name),
			Muted(t.PositiveTxnType),
		})
	}
	s.table.SetRows(rows)
}

// currentType returns the account type under the cursor.
func (s *Settings) currentType() (api.AccountType, bool) {
	index := s.table.Cursor()
	if index < 0 || index >= len(s.types) {
		return api.AccountType{}, false
	}
	return s.types[index], true
}

// openPaperlessForm edits the Paperless-ngx connection. The update is partial:
// each field maps to a pointer in the request, and a field the user left alone
// goes out as null — which the API reads as a nil pointer and skips, leaving
// the stored value alone. That is why a blank token cannot wipe an existing
// one, and why clearing one takes an explicit whitespace-only value.
func (s *Settings) openPaperlessForm() {
	current := s.paperless
	fields := []Field{
		{
			Label: "Paperless URL", Kind: FieldText, Value: current.PaperlessURL, Width: 48,
			Help: "blank leaves the stored URL unchanged; the API rejects an empty URL, so this field can be changed but not cleared",
		},
		{
			Label: "API token", Kind: FieldPassword, Width: 48,
			Help: "always starts empty because the API never returns the token: blank keeps the stored one, whitespace clears it",
		},
		{
			Label: "Tag", Kind: FieldText, Value: current.PaperlessTag, Width: 32,
			Help: "applied to a Paperless document once its import is committed; blank clears the stored tag",
		},
		settingsPageSizeField(current.PageSize),
	}

	s.ctx.Open(NewForm("settings.paperless", "Paperless-ngx settings", fields, func(f *Form) tea.Cmd {
		req := api.UpdateUserSettingsRequest{PageSize: settingsPageSizeValue(f.Value("Page size"), current.PageSize)}
		if url := strings.TrimSpace(f.Value("Paperless URL")); url != "" {
			req.PaperlessURL = &url
		}
		if raw := f.Value("API token"); raw != "" {
			// Sent as typed and trimmed: whitespace becomes the empty string,
			// which the API stores verbatim and therefore clears the token.
			token := strings.TrimSpace(raw)
			req.PaperlessToken = &token
		}
		tag := strings.TrimSpace(f.Value("Tag"))
		req.PaperlessTag = &tag

		return act("settings.paperless", "paperless settings saved", false, func(ctx context.Context) error {
			_, err := s.ctx.Client.UpdatePaperlessSettings(ctx, req)
			return err
		})
	}))
}

// settingsPageSizeField builds the page-size select: "leave unchanged" keeps the
// stored size, "clear" returns to the server default, and a number sets one.
func settingsPageSizeField(current *int) Field {
	options := []Option{
		{Value: settingsPageSizeClear, Label: "clear (server default: 25, capped at 100)"},
		{Value: "25", Label: "25"},
		{Value: "50", Label: "50"},
		{Value: "100", Label: "100 (Paperless maximum)"},
	}
	value := ""
	if current != nil {
		value = strconv.Itoa(*current)
	}
	if value != "" && !settingsHasOption(options, value) {
		// Sizes set outside this screen (the web UI, an older bundle) stay
		// selectable, so opening the form cannot silently change the value.
		options = append(options, Option{Value: value, Label: value})
	}
	return Field{
		Label: "Page size", Kind: FieldSelect, Value: value, Options: options,
		AllowClear: true, ClearLabel: "leave unchanged",
		Help: "documents requested per page from Paperless; a number sets it, clear restores the server default, leave unchanged keeps the stored size",
	}
}

// settingsHasOption reports whether value is already among the options.
func settingsHasOption(options []Option, value string) bool {
	for _, o := range options {
		if o.Value == value {
			return true
		}
	}
	return false
}

// settingsPageSizeValue maps the page-size select onto the request's
// OptionalInt.
//
// "leave unchanged" resends the stored size instead of omitting the key,
// because omitting is not expressible here: the client writes the zero
// OptionalInt as null, and the API reads a null pageSize as "clear". Resending
// the stored value is therefore the no-op that actually leaves it alone — and
// when nothing is stored, null is itself the no-op.
func settingsPageSizeValue(value string, current *int) api.OptionalInt {
	if value == settingsPageSizeClear {
		return api.IntNull()
	}
	if value != "" {
		if n, err := strconv.Atoi(value); err == nil && n > 0 {
			return api.Int(n)
		}
	}
	if current == nil {
		return api.IntNull()
	}
	return api.Int(*current)
}

// openExportForm writes the whole graph to a JSON bundle. ExportBackup returns
// the server's suggested filename (fintrak-backup-<date>.json) only after it has
// streamed the entire bundle, so the default replicates that documented name
// rather than exporting everything twice just to learn it.
func (s *Settings) openExportForm() {
	fields := []Field{{
		Label: "File", Kind: FieldText, Value: "fintrak-backup-" + nowDate() + ".json", Width: 48,
		Validate: required("file path"),
		Help:     "written on this machine; the bundle is JSON and does not contain the Paperless API token",
	}}

	s.ctx.Open(NewForm("settings.export", "Export backup", fields, func(f *Form) tea.Cmd {
		path := strings.TrimSpace(f.Value("File"))
		return act("settings.export", "exported to "+path, false, func(ctx context.Context) error {
			file, err := os.Create(path)
			if err != nil {
				return err
			}
			// Close explicitly and report its error: a backup that failed to
			// flush is a truncated backup, so the deferred-Close idiom would
			// hide the one failure that matters here.
			if _, err := s.ctx.Client.ExportBackup(ctx, file); err != nil {
				_ = file.Close()
				return err
			}
			return file.Close()
		})
	}))
}

// openImportForm asks for the bundle path. Reading the file is deferred to the
// confirm step so nothing is touched before the contract is on screen.
func (s *Settings) openImportForm() {
	fields := []Field{{
		Label: "Bundle file", Kind: FieldText, Value: "fintrak-backup-" + nowDate() + ".json", Width: 48,
		Validate: required("bundle path"),
		Help:     "read locally, then POSTed verbatim; the restore refuses with 409 when this account already has accounts",
	}}

	s.ctx.Open(NewForm("settings.import.path", "Restore backup", fields, func(f *Form) tea.Cmd {
		path := strings.TrimSpace(f.Value("Bundle file"))
		f.Close()
		s.confirmImport(path)
		return nil
	}))
}

// confirmImport spells out the restore contract before a single byte is read.
// A restore is the most destructive thing this screen can do: it refuses to
// merge into a used account (409), and it rewrites a bundle's whole id space.
func (s *Settings) confirmImport(path string) {
	s.ctx.Open(NewConfirm("Restore backup", fmt.Sprintf("Restore %s into this account?", path), true, func() tea.Cmd {
		return act("settings.import", "backup restored", true, func(ctx context.Context) error {
			bundle, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			report, err := s.ctx.Client.ImportBackup(ctx, bundle)
			if err != nil {
				return err
			}
			s.restore, s.restorePath = report, path
			return nil
		})
	}).WithDetail(
		"All-or-nothing: the whole bundle is restored in one transaction, so an error part-way through writes nothing at all.",
		"Refused with 409 Conflict once this account has accounts: merging a foreign bundle into existing data is ambiguous, so a restore is only for a fresh account.",
		"Every row is inserted under a freshly minted id with its references remapped, so a bundle can never collide with existing rows.",
		"A bundle is portable between users and instances — but it carries no credentials, so the Paperless token is not restored.",
	))
}

// settingsRestoreReport renders what a restore created, plus the rows the API
// skipped. Warnings are not failures: they list rows referencing something the
// bundle did not carry, which the restore drops instead of refusing.
func settingsRestoreReport(path string, r api.BackupImportResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "From %s\n\n", path)

	counts := []struct {
		name string
		n    int
	}{
		{"Accounts", r.Accounts},
		{"Category groups", r.CategoryGroups},
		{"Categories", r.Categories},
		{"Payees", r.Payees},
		{"Billing cycles", r.BillingCycles},
		{"Transactions", r.Transactions},
		{"Links", r.Links},
		{"Loan attachments", r.LoanAttachments},
		{"Loan schedules", r.LoanSchedules},
		{"Loan transfers", r.LoanTransfers},
		{"Loan disbursements", r.LoanDisbursements},
		{"Recurring series", r.RecurringSeries},
		{"Recurring terms", r.RecurringTerms},
		{"Recurring attachments", r.RecurringAttachments},
		{"Rules", r.Rules},
	}
	for _, c := range counts {
		fmt.Fprintf(&b, "  %-21s %d\n", c.name, c.n)
	}

	b.WriteString("\nWarnings\n")
	if len(r.Warnings) == 0 {
		b.WriteString("  none — every row in the bundle was written\n")
		return b.String()
	}
	for _, w := range r.Warnings {
		b.WriteString("  · " + w + "\n")
	}
	return b.String()
}

// openCreateTypeForm creates an account type. The id is immutable once stored
// on an account, so the form states the slug rule up front rather than letting
// the API's 400 teach it.
func (s *Settings) openCreateTypeForm() {
	fields := []Field{
		{
			Label: "Id", Kind: FieldText, Width: 24, Validate: validateSettingsTypeID,
			Help: "lowercase slug: a letter then 1-29 of a-z, 0-9, _; stored on every account using the type and cannot change later",
		},
		{Label: "Name", Kind: FieldText, Width: 32, Validate: required("name")},
		settingsPositiveTypeField(""),
	}

	s.ctx.Open(NewForm("settings.types", "New account type", fields, func(f *Form) tea.Cmd {
		req := api.CreateAccountTypeRequest{
			ID:              strings.TrimSpace(f.Value("Id")),
			Name:            strings.TrimSpace(f.Value("Name")),
			PositiveTxnType: f.Value("Positive txn type"),
		}
		return act("settings.types", "account type "+req.ID+" created", true, func(ctx context.Context) error {
			_, err := s.ctx.Client.CreateAccountType(ctx, req)
			return err
		})
	}))
}

// openEditTypeForm edits an existing account type. Only the name and the
// positive side are mutable; the API refuses the built-in types with 403 and
// that refusal is surfaced in the form rather than pre-blocked here.
func (s *Settings) openEditTypeForm() {
	current, ok := s.currentType()
	if !ok {
		s.ctx.Notify(LevelError, "no account type selected")
		return
	}
	fields := []Field{
		{Label: "Name", Kind: FieldText, Value: current.Name, Width: 32, Validate: required("name")},
		settingsPositiveTypeField(current.PositiveTxnType),
	}

	s.ctx.Open(NewForm("settings.types", "Edit account type "+current.ID, fields, func(f *Form) tea.Cmd {
		id := current.ID
		req := api.UpdateAccountTypeRequest{
			Name:            strings.TrimSpace(f.Value("Name")),
			PositiveTxnType: f.Value("Positive txn type"),
		}
		return act("settings.types", "account type "+id+" updated", true, func(ctx context.Context) error {
			_, err := s.ctx.Client.UpdateAccountType(ctx, id, req)
			return err
		})
	}))
}

// confirmDeleteType deletes the selected account type. The API is the
// authority on what may go: the built-in types answer 403 and a type still used
// by an account answers 409, and both messages reach the status line as-is.
func (s *Settings) confirmDeleteType() {
	current, ok := s.currentType()
	if !ok {
		s.ctx.Notify(LevelError, "no account type selected")
		return
	}
	s.ctx.Open(NewConfirm(
		"Delete account type",
		fmt.Sprintf("Delete account type %s (%s)?", current.ID, current.Name),
		true,
		func() tea.Cmd {
			id := current.ID
			return act("settings.types", "account type "+id+" deleted", true, func(ctx context.Context) error {
				return s.ctx.Client.DeleteAccountType(ctx, id)
			})
		},
	).WithDetail(
		"Built-in types (bank, credit_card, loan) are refused with 403, and a type still in use by an account with 409; those refusals are reported rather than prevented.",
	))
}

// settingsPositiveTypeField is the credit/debit choice shared by both catalog
// forms. It names the side that increases the balance, which is what makes a
// bank account "credit" and a credit card "debit".
func settingsPositiveTypeField(value string) Field {
	options := []Option{
		{Value: "credit", Label: "credit (money in increases the balance)"},
		{Value: "debit", Label: "debit (money out increases the balance)"},
	}
	return SelectField("Positive txn type", defaultTo(value, "credit"), options, true)
}

// validateSettingsTypeID applies the API's slug rule locally.
func validateSettingsTypeID(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return errText("required")
	}
	if !settingsTypeIDPattern.MatchString(value) {
		return errText("expected a lowercase slug: a letter then 1-29 of a-z, 0-9 or _")
	}
	return nil
}

// View implements Screen.
func (s *Settings) View(width, height int) string {
	th := s.ctx.Theme
	// The long explanations are worth three paragraphs on a tall terminal and
	// worth nothing in a short one, where they would push the catalog off the
	// screen entirely. Every one of them is repeated where it is needed: in the
	// form help, or in the restore confirmation.
	verbose := height >= 34

	lines := []string{th.Title.Render("Settings"), ""}
	lines = append(lines, s.sessionSection(width, verbose)...)
	lines = append(lines, "")
	lines = append(lines, s.paperlessSection(width, verbose)...)
	lines = append(lines, "")
	lines = append(lines, s.backupSection(width, verbose)...)

	if s.admin {
		lines = append(lines, "")
		lines = append(lines, s.catalogHeader())
		// Give the table whatever is left, minus its own hint line.
		tableHeight := height - len(lines) - 1
		lines = append(lines, s.table.View(th, width, max(2, tableHeight), s.catalogEmpty()))
		lines = append(lines, s.catalogHint(width))
	}
	return trimToBox(strings.Join(lines, "\n"), width, height)
}

// sessionSection summarises who is signed in and where the client points.
func (s *Settings) sessionSection(width int, verbose bool) []string {
	th := s.ctx.Theme
	user := s.ctx.User
	lines := []string{
		th.Header.Render("Session"),
		settingsLine("Email", defaultTo(user.Email, "—"), width),
		settingsLine("Role", defaultTo(user.Role, "member"), width),
		settingsLine("API", s.ctx.Client.BaseURL(), width),
	}
	if verbose {
		lines = append(lines, settingsParagraph(
			"The terminal signs in with the same email and password as the web UI: the browser's session cookie is httpOnly and cannot be read from here, so this client keeps its own token and the same 30-day deadline. ctrl+o signs out.",
			width)...)
	}
	return lines
}

// paperlessSection shows the Paperless-ngx connection. The token is rendered as
// a presence flag, never as a value: the API never returns it.
func (s *Settings) paperlessSection(width int, verbose bool) []string {
	th := s.ctx.Theme
	lines := []string{th.Header.Render("Paperless-ngx")}
	if !s.paperlessLoaded {
		return append(lines, th.Subtle.Render("  loading…"))
	}

	token := th.WarnText.Render("not configured")
	if s.paperless.HasToken {
		token = th.SuccessText.Render("configured")
	}
	pageSize := "server default (25, capped at 100)"
	if s.paperless.PageSize != nil {
		pageSize = strconv.Itoa(*s.paperless.PageSize)
	}

	lines = append(lines,
		settingsLine("URL", defaultTo(s.paperless.PaperlessURL, "(not configured)"), width),
		settingsLine("API token", token+" (the stored token is never displayed)", width),
		settingsLine("Tag", defaultTo(s.paperless.PaperlessTag, "(none)"), width),
		settingsLine("Page size", pageSize, width),
	)
	if verbose {
		lines = append(lines, settingsParagraph(
			"The token is encrypted at rest with the server's TOKEN_ENCRYPTION_KEY. Rotating that key leaves the stored ciphertext unreadable, so the token has to be entered again after a rotation — which is why e opens an empty token field instead of a masked stored value.",
			width)...)
	}
	return lines
}

// backupSection describes the two backup actions.
func (s *Settings) backupSection(width int, verbose bool) []string {
	th := s.ctx.Theme
	lines := []string{
		th.Header.Render("Backup"),
		"  " + th.Key.Render("x") + th.KeyDesc.Render("  export the whole graph as a JSON bundle (accounts, transactions, links, rules, settings)"),
		"  " + th.Key.Render("i") + th.KeyDesc.Render("  restore a bundle: one all-or-nothing transaction, refused with 409 once you have accounts"),
	}
	if verbose {
		lines = append(lines, settingsParagraph(
			"A restore is only for a fresh account, because the API answers 409 as soon as the account already has accounts: merging a foreign bundle into existing data is ambiguous. Within that one transaction every row is inserted under a fresh id with its references remapped, which is what makes a bundle portable between users and instances.",
			width)...)
	}
	return lines
}

// catalogHeader titles the admin catalog, marking it when it owns the keys.
func (s *Settings) catalogHeader() string {
	th := s.ctx.Theme
	if s.pane == settingsCatalog {
		return th.Title.Render("▸ Account types") + th.Subtle.Render("   admin catalog")
	}
	return th.Header.Render("Account types") + th.Subtle.Render("   admin catalog")
}

// catalogHint is the hint line under the catalog.
func (s *Settings) catalogHint(width int) string {
	th := s.ctx.Theme
	if s.pane == settingsCatalog {
		return th.Subtle.Render(truncate("  ↑/↓ choose · n new · e edit · d delete · esc back to settings", max(10, width)))
	}
	return th.Subtle.Render(truncate("  enter to open the catalog", max(10, width)))
}

// catalogEmpty is the table's empty message.
func (s *Settings) catalogEmpty() string {
	if !s.typesLoaded {
		return "loading…"
	}
	return "no account types"
}

// settingsLine renders an "  Label   value" row clipped to the box.
func settingsLine(label, value string, width int) string {
	return truncate(fmt.Sprintf("  %-12s %s", label, value), max(10, width))
}

// settingsParagraph wraps a note to the box and indents it.
func settingsParagraph(text string, width int) []string {
	wrapped := strings.Split(wrapText(text, max(20, width-4)), "\n")
	out := make([]string, 0, len(wrapped))
	for _, line := range wrapped {
		out = append(out, "  "+line)
	}
	return out
}
