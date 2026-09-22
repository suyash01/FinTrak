package ui

import (
	"bytes"
	"context"
	"encoding/csv"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/fintrak/client/api"
)

func init() {
	registerScreen(120, func(ctx *Ctx) Screen { return NewImport(ctx) })
}

// importPane is one of the three sources the screen can import from.
type importPane int

// The panes, in cycle order.
const (
	paneStatement importPane = iota
	panePaperless
	paneCSV
)

// importPaneNames are the pane labels, indexed by importPane.
var importPaneNames = []string{"Statement", "Paperless", "CSV"}

// maxImportUpload caps what the client reads before it asks the API to parse.
// The statement endpoint refuses anything over 20 MB with 413, so checking here
// saves uploading a file the server has already said it will not take, and keeps
// a very large file from being held in memory on the way.
const maxImportUpload = 20 << 20

// importKeys are the screen's bindings.
type importKeys struct {
	Pane     key.Binding
	Parse    key.Binding
	Open     key.Binding
	Map      key.Binding
	Import   key.Binding
	Search   key.Binding
	Filters  key.Binding
	PrevPage key.Binding
	NextPage key.Binding
	Discard  key.Binding
}

func newImportKeys() importKeys {
	return importKeys{
		// `tab` is the App's sidebar/content toggle and never reaches a screen,
		// so the panes cycle on `p` instead.
		Pane:     key.NewBinding(key.WithKeys("p"), key.WithHelp("p", "source pane")),
		Parse:    key.NewBinding(key.WithKeys("n"), key.WithHelp("n", "parse pdf")),
		Open:     key.NewBinding(key.WithKeys("o"), key.WithHelp("o", "open csv")),
		Map:      key.NewBinding(key.WithKeys("m"), key.WithHelp("m", "map columns")),
		Import:   key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "parse / import")),
		Search:   key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "search")),
		Filters:  key.NewBinding(key.WithKeys("f"), key.WithHelp("f", "paperless filters")),
		PrevPage: key.NewBinding(key.WithKeys(","), key.WithHelp(",", "prev page")),
		NextPage: key.NewBinding(key.WithKeys("."), key.WithHelp(".", "next page")),
		Discard:  key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "discard preview")),
	}
}

// importBatch is one parsed batch waiting to be committed. Every source ends
// here: the parse endpoints only normalize and report, so the screen holds the
// rows until the user has seen them and chosen an account.
type importBatch struct {
	// source describes where the rows came from, for the preview and the report.
	source string
	rows   []api.ImportTransaction
	// summary, pageCount and transactionCount are the parser's own report.
	summary          map[string]string
	pageCount        int
	transactionCount int
	// errors holds the parser's per-page reconciliation failures. A non-empty
	// list means the rows are suspect, so the preview leads with it rather than
	// burying it under the table.
	errors []string
	// documents are the Paperless documents the import tags once it commits.
	documents []int
	// skipped counts CSV rows the mapping could not resolve.
	skipped int
}

// importTarget is what the commit steps collect before anything is written.
type importTarget struct {
	accountID string
	action    string
	cycleID   string
}

// importReport is the durable result of the last commit, kept on screen after
// the modal that announced it is dismissed.
type importReport struct {
	source  string
	account string
	result  api.ImportResult
}

// Import brings transactions in from a bank statement PDF, a Paperless-ngx
// document or a CSV file.
//
// The three sources are panes of one screen (p cycles them) because they all end
// in the same two steps: a parse, which never writes, and a commit, which is the
// only call that changes data. Holding the parsed batch here is what lets the
// user read the parser's own reconciliation errors before the rows reach the
// ledger.
type Import struct {
	ctx  *Ctx
	keys importKeys
	pane importPane

	extractors      []api.StatementExtractor
	extractorsReady bool
	extractorsErr   error

	settings      api.PaperlessSettingsResponse
	settingsReady bool
	settingsErr   error
	// docs is the current document page; the lookup tables it carries are what
	// the filter form offers as names.
	docs      api.PaperlessDocumentsResponse
	docsReady bool
	docsErr   error
	query     api.PaperlessQuery

	docTable  Table
	searching bool
	search    textinput.Model

	csvPath    string
	csvHeader  []string
	csvRows    [][]string
	csvMapping csvMapping

	// parseForm and csvForm are the forms whose submit fetches rather than
	// saves. They are kept so a failed fetch is reported inside the form: a 401
	// on a password-protected PDF has to be fixable without retyping the path.
	parseForm *Form
	csvForm   *Form
	// docForm is the document the open parse form was opened for. It survives a
	// failed parse so a retry still carries the id that gets tagged once the
	// import commits.
	docForm *api.PaperlessDocument

	pending *importBatch
	preview Table
	target  importTarget
	check   api.ValidateTransactionsResponse
	cycles  []api.BillingCycle
	report  *importReport
}

// NewImport builds the import screen.
func NewImport(ctx *Ctx) *Import {
	i := &Import{
		ctx:        ctx,
		keys:       newImportKeys(),
		query:      api.PaperlessQuery{Page: 1},
		csvMapping: csvMapping{amountMode: "signed", positive: "credit", dateFormat: "auto"},
	}
	i.preview.SetColumns(
		Column{Title: "Date", Width: 11},
		Column{Title: "Description"},
		Column{Title: "Amount", Width: 14, Align: AlignRight},
		Column{Title: "Type", Width: 7},
	)
	i.docTable.SetColumns(
		Column{Title: "Id", Width: 7, Align: AlignRight},
		Column{Title: "Title"},
		Column{Title: "Correspondent", Width: 18},
		Column{Title: "Type", Width: 14},
		Column{Title: "Created", Width: 11},
		Column{Title: "Tags"},
	)
	return i
}

// Title implements Screen.
func (i *Import) Title() string { return "Import" }

// Keys implements Screen.
func (i *Import) Keys() []key.Binding {
	if i.pending != nil {
		return []key.Binding{i.keys.Import, i.keys.Discard}
	}
	switch i.pane {
	case paneStatement:
		return []key.Binding{i.keys.Parse, i.keys.Pane}
	case panePaperless:
		return []key.Binding{
			i.keys.Import, i.keys.Search, i.keys.Filters, i.keys.PrevPage,
			i.keys.NextPage, i.keys.Pane,
		}
	default:
		if len(i.csvHeader) > 0 {
			return []key.Binding{i.keys.Open, i.keys.Map, i.keys.Pane}
		}
		return []key.Binding{i.keys.Open, i.keys.Pane}
	}
}

// CapturesText implements Screen: while the Paperless document search is open,
// every character belongs to the query.
func (i *Import) CapturesText() bool { return i.searching }

// Refresh implements Screen. It always refetches: `r` is the user asking for
// current data, and a stale Paperless page or extractor registry is worse than
// one redundant call.
func (i *Import) Refresh() tea.Cmd {
	switch i.pane {
	case paneStatement:
		return i.loadExtractors()
	case panePaperless:
		return i.loadSettings()
	default:
		// The CSV pane reads a local file only, so there is nothing to refetch.
		return nil
	}
}

// loadExtractors fetches the parser service's extractor registry.
func (i *Import) loadExtractors() tea.Cmd {
	i.extractorsReady = false
	return load("import.extractors", func(ctx context.Context) ([]api.StatementExtractor, error) {
		return i.ctx.Client.ListStatementExtractors(ctx)
	})
}

// loadSettings fetches the Paperless-ngx settings, which decide whether the
// document list may be called at all.
func (i *Import) loadSettings() tea.Cmd {
	i.settingsReady = false
	return load("import.settings", func(ctx context.Context) (api.PaperlessSettingsResponse, error) {
		return i.ctx.Client.PaperlessSettings(ctx)
	})
}

// loadDocuments fetches the current page of Paperless documents.
func (i *Import) loadDocuments() tea.Cmd {
	i.docsReady = false
	query := i.query
	return load("import.docs", func(ctx context.Context) (api.PaperlessDocumentsResponse, error) {
		return i.ctx.Client.ListPaperlessDocuments(ctx, query)
	})
}

// ensurePane fetches the visible pane's data only if it has not been loaded yet,
// so cycling through the panes does not re-query Paperless every time.
func (i *Import) ensurePane() tea.Cmd {
	switch i.pane {
	case paneStatement:
		if !i.extractorsReady {
			return i.loadExtractors()
		}
	case panePaperless:
		if !i.settingsReady {
			return i.loadSettings()
		}
	}
	return nil
}

// Update implements Screen.
func (i *Import) Update(msg tea.Msg) tea.Cmd {
	switch m := msg.(type) {
	case loaded[[]api.StatementExtractor]:
		if m.tag != "import.extractors" {
			break
		}
		i.extractorsReady = true
		i.extractors, i.extractorsErr = m.data, m.err
		if m.err != nil {
			i.ctx.Notify(LevelError, "extractors: %s", m.err)
		}
		return nil

	case loaded[api.PaperlessSettingsResponse]:
		if m.tag != "import.settings" {
			break
		}
		i.settingsReady = true
		i.settings, i.settingsErr = m.data, m.err
		if m.err != nil {
			i.ctx.Notify(LevelError, "paperless: %s", m.err)
			return nil
		}
		if !m.data.HasToken {
			// The document list answers 400 without a token, so it is not
			// called: the pane points at Settings instead of showing that error.
			return nil
		}
		return i.loadDocuments()

	case loaded[api.PaperlessDocumentsResponse]:
		if m.tag != "import.docs" {
			break
		}
		i.docsReady = true
		if m.err != nil {
			i.docsErr = m.err
			i.ctx.Notify(LevelError, "paperless documents: %s", m.err)
			return nil
		}
		i.docs, i.docsErr = m.data, nil
		i.applyDocRows()
		return nil

	case loaded[api.StatementParseResult]:
		// Both parse endpoints answer with the same payload, so the tag decides
		// which pane asked and whether a Paperless document id rides along.
		switch m.tag {
		case "import.parse":
			return i.acceptParse(m.data, m.err, importBatch{source: "statement pdf"})
		case "import.paperless":
			batch := importBatch{source: "paperless document"}
			if i.docForm != nil {
				batch.source = fmt.Sprintf("paperless document %d", i.docForm.ID)
				batch.documents = []int{i.docForm.ID}
			}
			return i.acceptParse(m.data, m.err, batch)
		}
		return nil

	case loaded[csvFile]:
		if m.tag != "import.csv" {
			break
		}
		return i.acceptCSV(m.data, m.err)

	case loaded[[]api.BillingCycle]:
		if m.tag != "import.cycles" {
			break
		}
		return i.acceptCycles(m.data, m.err)

	case loaded[api.ValidateTransactionsResponse]:
		if m.tag != "import.check" {
			break
		}
		if m.err != nil {
			// Nothing has been written: the batch stays staged so the user can
			// retry once the problem is fixed.
			i.ctx.Notify(LevelError, "validate: %s", m.err)
			return nil
		}
		i.check = m.data
		i.openCommitConfirm()
		return nil

	case loaded[api.ImportResult]:
		if m.tag != "import.commit" {
			break
		}
		return i.acceptImport(m.data, m.err)

	case tea.KeyMsg:
		return i.handleKey(m)
	}
	return nil
}

// handleKey routes a key press.
func (i *Import) handleKey(msg tea.KeyMsg) tea.Cmd {
	// A staged batch owns the keyboard: it is the only thing the screen can act
	// on until it is committed or discarded.
	if i.pending != nil {
		switch {
		case keyMatches(i.keys.Import, msg):
			return i.startCommit()
		case keyMatches(i.keys.Discard, msg):
			i.discard()
			return nil
		}
		switch msg.String() {
		case "up", "k":
			i.preview.Move(-1)
		case "down", "j":
			i.preview.Move(1)
		case "pgup", "ctrl+b":
			i.preview.Page(-1, 20)
		case "pgdown", "ctrl+f":
			i.preview.Page(1, 20)
		case "home":
			i.preview.Home()
		case "end":
			i.preview.End()
		}
		return nil
	}

	if i.searching {
		switch msg.String() {
		case "enter":
			i.searching = false
			i.query.Search = i.search.Value()
			i.query.Page = 1
			return i.loadDocuments()
		case "esc":
			i.searching = false
			return nil
		}
		var cmd tea.Cmd
		i.search, cmd = i.search.Update(msg)
		return cmd
	}

	switch {
	case keyMatches(i.keys.Pane, msg):
		i.pane = importPane((int(i.pane) + 1) % len(importPaneNames))
		return i.ensurePane()
	case keyMatches(i.keys.Parse, msg):
		if i.pane == paneStatement {
			i.openStatementForm()
		}
		return nil
	case keyMatches(i.keys.Open, msg):
		if i.pane == paneCSV {
			i.openCSVForm()
		}
		return nil
	case keyMatches(i.keys.Map, msg):
		if i.pane == paneCSV && len(i.csvHeader) > 0 {
			i.openMappingForm()
		}
		return nil
	case keyMatches(i.keys.Import, msg):
		if i.pane == panePaperless {
			return i.startPaperlessParse()
		}
		return nil
	case keyMatches(i.keys.Filters, msg):
		if i.pane == panePaperless {
			i.openFilterForm()
		}
		return nil
	case keyMatches(i.keys.Search, msg):
		if i.pane != panePaperless {
			return nil
		}
		i.searching = true
		i.search = textinput.New()
		i.search.Placeholder = "search document titles"
		i.search.Width = 44
		i.search.SetValue(i.query.Search)
		i.search.Focus()
		return textinput.Blink
	case keyMatches(i.keys.PrevPage, msg):
		if i.pane == panePaperless && i.query.Page > 1 {
			i.query.Page--
			return i.loadDocuments()
		}
		return nil
	case keyMatches(i.keys.NextPage, msg):
		if i.pane == panePaperless && i.query.Page < max(1, i.docs.TotalPages) {
			i.query.Page++
			return i.loadDocuments()
		}
		return nil
	}

	if i.pane != panePaperless {
		return nil
	}
	switch msg.String() {
	case "up", "k":
		i.docTable.Move(-1)
	case "down", "j":
		i.docTable.Move(1)
	case "pgup", "ctrl+b":
		i.docTable.Page(-1, 20)
	case "pgdown", "ctrl+f":
		i.docTable.Page(1, 20)
	case "home":
		i.docTable.Home()
	case "end":
		i.docTable.End()
	}
	return nil
}

// currentDoc returns the highlighted Paperless document.
func (i *Import) currentDoc() (api.PaperlessDocument, bool) {
	index := i.docTable.Cursor()
	if index < 0 || index >= len(i.docs.Documents) {
		return api.PaperlessDocument{}, false
	}
	return i.docs.Documents[index], true
}

// applyDocRows rebuilds the document table from the fetched page.
func (i *Import) applyDocRows() {
	rows := make([][]Cell, 0, len(i.docs.Documents))
	for _, doc := range i.docs.Documents {
		rows = append(rows, []Cell{
			Text(strconv.Itoa(doc.ID)),
			Text(doc.Title),
			Text(defaultTo(doc.Correspondent, "—")),
			Text(defaultTo(doc.DocumentType, "—")),
			Muted(documentDate(doc.Created)),
			Muted(strings.Join(doc.Tags, ", ")),
		})
	}
	i.docTable.SetRows(rows)
	i.docTable.SetCursor(0)
}

// applyPreviewRows rebuilds the preview table from the staged batch.
func (i *Import) applyPreviewRows() {
	if i.pending == nil {
		i.preview.SetRows(nil)
		return
	}
	rows := make([][]Cell, 0, len(i.pending.rows))
	for _, row := range i.pending.rows {
		rows = append(rows, []Cell{
			Text(row.Date),
			Text(row.Description),
			Cell{Text: signedAmount(row.Amount, row.Type), Role: amountRole(row.Type)},
			Muted(row.Type),
		})
	}
	i.preview.SetRows(rows)
	i.preview.SetCursor(0)
}

// documentDate shortens a Paperless timestamp to the date part, which is all the
// list column has room for.
func documentDate(created string) string {
	created = strings.TrimSpace(created)
	if len(created) < len(api.DateLayout) {
		return defaultTo(created, "—")
	}
	return created[:len(api.DateLayout)]
}

// documentNames renders a lookup table as a comma-separated list of the names a
// filter accepts.
func documentNames(names []string) string {
	if len(names) == 0 {
		return "none reported"
	}
	return strings.Join(names, ", ")
}

// importSummary renders the parser's reconciliation summary as one line. Every
// figure in it was computed by the parser service; the screen only orders the
// keys so the line reads the same way twice.
func importSummary(summary map[string]string) string {
	if len(summary) == 0 {
		return ""
	}
	keys := make([]string, 0, len(summary))
	for key := range summary {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, key+" "+summary[key])
	}
	return strings.Join(parts, " · ")
}

// acceptParse stages a finished parse. Neither parse endpoint writes, so an
// error costs the user nothing but a retry — which is why the form stays open
// with the message inside it, and the document it was opened for stays on the
// screen: a password-protected PDF answers 401 until the password is filled in.
func (i *Import) acceptParse(res api.StatementParseResult, err error, batch importBatch) tea.Cmd {
	if err != nil {
		if f := i.parseForm; f != nil && !f.Closed() {
			f.SetError(err)
			return nil
		}
		i.ctx.Notify(LevelError, "%s", err)
		return nil
	}
	if f := i.parseForm; f != nil && !f.Closed() {
		f.Close()
	}
	i.parseForm, i.docForm = nil, nil

	if len(res.Transactions) == 0 {
		message := "the parser returned no transactions"
		if len(res.ValidationErrors) > 0 {
			message += ": " + res.ValidationErrors[0]
		}
		i.ctx.Notify(LevelError, "%s", message)
		return nil
	}

	batch.rows = res.Transactions
	batch.summary = res.Summary
	batch.pageCount = res.PageCount
	batch.transactionCount = res.TransactionCount
	batch.errors = res.ValidationErrors
	i.stage(batch)
	return nil
}

// stage puts a batch in front of the user. Staging is the only way a batch
// appears, and it is deliberately the end of the parse: nothing has been written
// yet, so the preview is free to be discarded.
func (i *Import) stage(batch importBatch) {
	i.pending = &batch
	i.target = importTarget{action: "skip"}
	i.check = api.ValidateTransactionsResponse{}
	i.cycles = nil
	i.report = nil
	i.applyPreviewRows()
	source := batch.source
	rows := pluralise(len(batch.rows), "row", "rows")
	if len(batch.errors) > 0 {
		i.ctx.Notify(LevelError, "%s: %s parsed, %s — the parser could not reconcile its pages, so the rows are suspect", source, rows, pluralise(len(batch.errors), "error", "errors"))
		return
	}
	i.ctx.Notify(LevelInfo, "%s: %s parsed, nothing written yet", source, rows)
}

// discard drops the staged batch. It is always safe: the parse wrote nothing.
func (i *Import) discard() {
	i.pending, i.check, i.cycles, i.target = nil, api.ValidateTransactionsResponse{}, nil, importTarget{}
	i.applyPreviewRows()
	i.ctx.Notify(LevelInfo, "preview discarded, nothing was written")
}

// startCommit opens the first commit step: which account the rows land in and
// how duplicates are treated. The billing cycle cannot be offered here because
// cycles belong to one account and the account has not been chosen yet.
func (i *Import) startCommit() tea.Cmd {
	if i.pending == nil {
		return nil
	}
	if !i.ctx.Ref.Loaded() {
		i.ctx.Notify(LevelError, "accounts are still loading")
		return nil
	}
	accounts := i.ctx.Ref.AccountOptions()
	if len(accounts) == 0 {
		i.ctx.Notify(LevelError, "create an account before importing")
		return nil
	}
	current := i.target.accountID
	if current == "" {
		current = accounts[0].Value
	}
	fields := []Field{
		SelectField("Account", current, accounts, true),
		SelectField("Duplicates", defaultTo(i.target.action, "skip"), []Option{
			{Value: "skip", Label: "skip — drop rows that already exist"},
			{Value: "keep", Label: "keep — import every row"},
		}, true),
	}
	i.ctx.Open(NewForm("import.target", fmt.Sprintf("Import %s", pluralise(len(i.pending.rows), "transaction", "transactions")), fields, func(f *Form) tea.Cmd {
		i.target.accountID = f.Value("Account")
		i.target.action = f.Value("Duplicates")
		i.target.cycleID = ""
		f.Close()
		return i.loadCycles()
	}))
	return nil
}

// loadCycles fetches the chosen account's cycles, because a cycle is only valid
// for the account that generated it.
func (i *Import) loadCycles() tea.Cmd {
	accountID := i.target.accountID
	return load("import.cycles", func(ctx context.Context) ([]api.BillingCycle, error) {
		return i.ctx.Client.ListBillingCycles(ctx, accountID)
	})
}

// acceptCycles offers the account's billing cycles when it has any. An account
// without a billing day has no cycles and needs no step: the API attaches the
// rows by date on its own.
func (i *Import) acceptCycles(cycles []api.BillingCycle, err error) tea.Cmd {
	if err != nil {
		// A missing cycle list must not block the import: with no cycle chosen
		// the API falls back to the date-based default.
		i.ctx.Notify(LevelError, "billing cycles: %s — importing without one", err)
		return i.startValidate()
	}
	i.cycles = cycles
	account, ok := i.ctx.Ref.Account(i.target.accountID)
	if !ok || account.BillingDay == nil || len(cycles) == 0 {
		return i.startValidate()
	}
	options := make([]Option, 0, len(cycles))
	for _, cycle := range cycles {
		options = append(options, Option{
			Value: cycle.ID,
			Label: fmt.Sprintf("%s (%s … %s)", cycle.Label, cycle.StartDate.Format(api.DateLayout), cycle.EndDate.Format(api.DateLayout)),
		})
	}
	field := SelectField("Billing cycle", "", options, false)
	field.ClearLabel = "date-based default"
	field.Help = fmt.Sprintf("%s closes on day %d; every imported row is attached to the cycle its date falls in", account.Name, *account.BillingDay)
	i.ctx.Open(NewForm("import.cycle", "Billing cycle", []Field{field}, func(f *Form) tea.Cmd {
		i.target.cycleID = f.Value("Billing cycle")
		f.Close()
		return i.startValidate()
	}))
	return nil
}

// startValidate runs the read-only duplicate check. It mirrors the import's own
// fingerprint matching, so the count the user is shown is the count
// duplicateAction "skip" would drop.
func (i *Import) startValidate() tea.Cmd {
	if i.pending == nil {
		return nil
	}
	request := api.ValidateTransactionsRequest{
		AccountID:    i.target.accountID,
		Transactions: i.pending.rows,
	}
	return load("import.check", func(ctx context.Context) (api.ValidateTransactionsResponse, error) {
		return i.ctx.Client.ValidateTransactions(ctx, request)
	})
}

// openCommitConfirm shows what the import will do, with the duplicate count the
// validation just reported, and asks before writing anything.
func (i *Import) openCommitConfirm() {
	batch := i.pending
	if batch == nil {
		return
	}
	account := i.ctx.Ref.AccountName(i.target.accountID)
	body := fmt.Sprintf("%s into %s", batch.source, account)
	detail := []string{
		fmt.Sprintf("%d of %d row(s) are new to this account", i.check.MissingCount, i.check.Total),
		fmt.Sprintf("%d row(s) already exist and duplicate action \"%s\" applies", i.check.ExistingCount, i.target.action),
	}
	if i.target.cycleID != "" {
		detail = append(detail, "billing cycle: "+i.cycleLabel(i.target.cycleID))
	} else {
		detail = append(detail, "billing cycle: the date-based default")
	}
	if len(batch.documents) > 0 {
		detail = append(detail, fmt.Sprintf("paperless document %d is tagged with the configured tag once the import commits", batch.documents[0]))
	}
	if len(batch.errors) > 0 {
		detail = append(detail, fmt.Sprintf("warning: the parser reported %s, so these rows are suspect", pluralise(len(batch.errors), "reconciliation error", "reconciliation errors")))
	}
	if batch.skipped > 0 {
		detail = append(detail, fmt.Sprintf("%d row(s) the mapping could not read were dropped", batch.skipped))
	}
	i.ctx.Open(NewConfirm(fmt.Sprintf("Import %s", pluralise(len(batch.rows), "transaction", "transactions")), body, false, i.commit).WithDetail(detail...))
}

// cycleLabel resolves a cycle id for display.
func (i *Import) cycleLabel(id string) string {
	for _, cycle := range i.cycles {
		if cycle.ID == id {
			return cycle.Label
		}
	}
	return id
}

// commit writes the batch. This is the only step that changes anything: both
// parses and the validation are read-only, which is what makes discarding a
// preview free.
//
// It goes through load rather than act because the response is the report the
// screen has to show (imported / duplicates / total) and done carries only an
// error; a field written from an act goroutine would race the render loop, which
// reads this screen while it runs.
func (i *Import) commit() tea.Cmd {
	batch := i.pending
	if batch == nil {
		return nil
	}
	request := api.ImportRequest{
		AccountID:            i.target.accountID,
		Transactions:         batch.rows,
		DuplicateAction:      i.target.action,
		PaperlessDocumentIDs: batch.documents,
	}
	if i.target.cycleID != "" {
		cycleID := i.target.cycleID
		request.BillingCycleID = &cycleID
	}
	return load("import.commit", func(ctx context.Context) (api.ImportResult, error) {
		return i.ctx.Client.ImportTransactions(ctx, request)
	})
}

// acceptImport reports what the import wrote. A failure keeps the batch staged
// so the user can pick another account and try again; nothing was written.
func (i *Import) acceptImport(result api.ImportResult, err error) tea.Cmd {
	if err != nil {
		i.ctx.Notify(LevelError, "%s", err)
		return nil
	}
	batch := i.pending
	source, account, tagged := "", i.ctx.Ref.AccountName(i.target.accountID), false
	if batch != nil {
		source = batch.source
		tagged = len(batch.documents) > 0
	}
	i.report = &importReport{source: source, account: account, result: result}
	i.pending, i.check, i.cycles, i.target = nil, api.ValidateTransactionsResponse{}, nil, importTarget{}
	i.applyPreviewRows()
	i.ctx.Notify(LevelSuccess, "imported %d of %d transaction(s), %d duplicate(s) skipped", result.Imported, result.Total, result.Duplicates)
	i.ctx.Open(NewInfo("Import complete", importReportBody(*i.report)))
	// A Paperless import tags its source document, so the list on screen is now
	// out of date; every other source leaves it alone.
	if tagged && i.settings.HasToken {
		return i.loadDocuments()
	}
	return nil
}

// importReportBody renders the import result. The API computes every figure; the
// screen only lays them out.
func importReportBody(report importReport) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Imported        %d\n", report.result.Imported)
	fmt.Fprintf(&b, "Duplicates      %d\n", report.result.Duplicates)
	fmt.Fprintf(&b, "Total           %d\n", report.result.Total)
	if report.account != "" {
		fmt.Fprintf(&b, "Account         %s\n", report.account)
	}
	if report.source != "" {
		fmt.Fprintf(&b, "Source          %s\n", report.source)
	}
	b.WriteString("\nRows the duplicate action dropped already existed in the account; nothing else was skipped.")
	return b.String()
}

// openStatementForm collects the upload's options. The extractor list is the
// parser service's own registry, so whatever is sent back is a value it accepts,
// and blank means the server's default extractor.
func (i *Import) openStatementForm() {
	fields := append([]Field{
		{
			Label: "PDF file", Kind: FieldText, Width: 52,
			Validate: required("file path"),
			Help:     "path on the machine running this client",
		},
		{
			Label: "Password", Kind: FieldPassword, Width: 24,
			Help: "only for a password-protected PDF: without it the parse answers 401",
		},
	}, i.parseOptionFields()...)
	i.ctx.Open(NewForm("import.parse.form", "Parse a statement PDF", fields, func(f *Form) tea.Cmd {
		path := f.Value("PDF file")
		password := f.Value("Password")
		extractor := f.Value("Extractor")
		dateFormat := parseDateHint(f.Value("Date format"))
		i.parseForm = f
		return load("import.parse", func(ctx context.Context) (api.StatementParseResult, error) {
			info, err := os.Stat(path)
			if err != nil {
				return api.StatementParseResult{}, err
			}
			if info.Size() > maxImportUpload {
				return api.StatementParseResult{}, errText("the file is larger than 20 MB, which the parser refuses with 413")
			}
			pdf, err := os.ReadFile(path)
			if err != nil {
				return api.StatementParseResult{}, err
			}
			return i.ctx.Client.ParseStatement(ctx, filepath.Base(path), pdf, password, extractor, dateFormat)
		})
	}))
}

// parseOptionFields are the options both parse endpoints take: which extractor
// reads the document, and the date layout to assume when detection fails.
func (i *Import) parseOptionFields() []Field {
	options := make([]Option, 0, len(i.extractors))
	for _, extractor := range i.extractors {
		options = append(options, Option{
			Value: extractor.Name,
			Label: defaultTo(extractor.DisplayName, extractor.Name),
		})
	}
	extractorField := SelectField("Extractor", "", options, false)
	extractorField.ClearLabel = "server default"
	if len(options) == 0 {
		extractorField.Help = "the registry has not loaded; blank asks for the server's default extractor"
	} else {
		extractorField.Help = "which parser reads this statement"
	}
	return []Field{
		extractorField,
		SelectField("Date format", "auto", dateFormatOptions, true),
	}
}

// parseDateHint turns a date-format choice into the value the parse endpoints
// expect: an empty dateFormat leaves detection to the parser.
func parseDateHint(value string) string {
	if value == "" || value == "auto" {
		return ""
	}
	return value
}

// startPaperlessParse opens the parse options for the highlighted document.
func (i *Import) startPaperlessParse() tea.Cmd {
	doc, ok := i.currentDoc()
	if !ok {
		i.ctx.Notify(LevelError, "no document is highlighted")
		return nil
	}
	i.openPaperlessForm(doc)
	return nil
}

// openPaperlessForm parses one document. The document is remembered because the
// import hands its id back in PaperlessDocumentIDs, which is how the configured
// tag reaches the document once the rows have actually been written.
func (i *Import) openPaperlessForm(doc api.PaperlessDocument) {
	fields := append([]Field{{
		Label: "Password", Kind: FieldPassword, Width: 24,
		Help: "only for a password-protected document: without it the parse answers 401",
	}}, i.parseOptionFields()...)
	i.docForm = &doc
	i.ctx.Open(NewForm("import.paperless.form", fmt.Sprintf("Parse document %d", doc.ID), fields, func(f *Form) tea.Cmd {
		request := api.PaperlessImportRequest{
			DocumentID: doc.ID,
			Extractor:  f.Value("Extractor"),
			Password:   f.Value("Password"),
			DateFormat: parseDateHint(f.Value("Date format")),
		}
		i.parseForm = f
		return load("import.paperless", func(ctx context.Context) (api.StatementParseResult, error) {
			return i.ctx.Client.ImportPaperlessDocument(ctx, request)
		})
	}))
}

// openFilterForm edits the whole PaperlessQuery. Each include and exclude list
// carries names — not ids — from the lookup tables the document list returned,
// so the field's help names what is on offer instead of leaving the user to
// guess what Paperless calls a correspondent.
func (i *Import) openFilterForm() {
	query := i.query
	help := func(names []string) string {
		return "comma separated: " + truncate(documentNames(names), 80)
	}
	fields := []Field{
		{Label: "Correspondents in", Kind: FieldText, Value: strings.Join(query.CorrespondentInc, ","), Width: 40, Help: help(i.docs.Correspondents)},
		{Label: "Correspondents out", Kind: FieldText, Value: strings.Join(query.CorrespondentExc, ","), Width: 40, Help: help(i.docs.Correspondents)},
		{Label: "Document types in", Kind: FieldText, Value: strings.Join(query.DocumentTypeInc, ","), Width: 40, Help: help(i.docs.DocumentTypes)},
		{Label: "Document types out", Kind: FieldText, Value: strings.Join(query.DocumentTypeExc, ","), Width: 40, Help: help(i.docs.DocumentTypes)},
		{Label: "Tags in", Kind: FieldText, Value: strings.Join(query.TagInc, ","), Width: 40, Help: help(i.docs.Tags)},
		{Label: "Tags out", Kind: FieldText, Value: strings.Join(query.TagExc, ","), Width: 40, Help: help(i.docs.Tags)},
		{Label: "Page size", Kind: FieldText, Value: strconv.Itoa(query.PageSize), Width: 6, Validate: positiveInt, Help: "server default 25, capped at 100"},
	}
	i.ctx.Open(NewForm("import.filters", "Paperless filters", fields, func(f *Form) tea.Cmd {
		i.query = api.PaperlessQuery{
			// A new filter starts at the first page: page 4 of the old result
			// set usually does not exist for the new one.
			Page:             1,
			PageSize:         f.IntValue("Page size"),
			Search:           query.Search,
			CorrespondentInc: splitList(f.Value("Correspondents in")),
			CorrespondentExc: splitList(f.Value("Correspondents out")),
			DocumentTypeInc:  splitList(f.Value("Document types in")),
			DocumentTypeExc:  splitList(f.Value("Document types out")),
			TagInc:           splitList(f.Value("Tags in")),
			TagExc:           splitList(f.Value("Tags out")),
		}
		f.Close()
		return i.loadDocuments()
	}))
}

// csvFile is a CSV file the screen has read: its own path, its header row, and
// the records under it.
type csvFile struct {
	path   string
	header []string
	rows   [][]string
}

// readCSVFile parses a local CSV file. A ragged file is accepted, because a
// trailing separator or a short last row is common in bank exports and must not
// fail the whole file.
func readCSVFile(path string) (csvFile, error) {
	info, err := os.Stat(path)
	if err != nil {
		return csvFile{}, err
	}
	if info.Size() > maxImportUpload {
		return csvFile{}, errText("the file is larger than 20 MB")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return csvFile{}, err
	}
	reader := csv.NewReader(bytes.NewReader(data))
	reader.FieldsPerRecord = -1
	reader.TrimLeadingSpace = true
	records, err := reader.ReadAll()
	if err != nil {
		return csvFile{}, err
	}
	if len(records) == 0 {
		return csvFile{}, errText("the file is empty")
	}
	// A byte-order mark would otherwise become part of the first column's name
	// and keep that column from ever matching.
	if len(records[0]) > 0 {
		records[0][0] = strings.TrimPrefix(records[0][0], "\ufeff")
	}
	if len(records) < 2 {
		return csvFile{path: path, header: records[0]}, nil
	}
	return csvFile{path: path, header: records[0], rows: records[1:]}, nil
}

// openCSVForm asks for the file to read. The mapping is a second step because
// the columns cannot be offered before the header has been read.
func (i *Import) openCSVForm() {
	fields := []Field{{
		Label: "CSV file", Kind: FieldText, Value: i.csvPath, Width: 52,
		Validate: required("file path"),
		Help:     "read on this machine: the API has no CSV endpoint",
	}}
	i.ctx.Open(NewForm("import.csv.form", "Open a CSV file", fields, func(f *Form) tea.Cmd {
		path := f.Value("CSV file")
		i.csvForm = f
		return load("import.csv", func(ctx context.Context) (csvFile, error) {
			return readCSVFile(path)
		})
	}))
}

// acceptCSV opens the mapping step for a file that was read.
func (i *Import) acceptCSV(file csvFile, err error) tea.Cmd {
	if err != nil {
		if f := i.csvForm; f != nil && !f.Closed() {
			f.SetError(err)
			return nil
		}
		i.ctx.Notify(LevelError, "%s", err)
		return nil
	}
	if f := i.csvForm; f != nil && !f.Closed() {
		f.Close()
	}
	i.csvForm = nil
	i.csvPath, i.csvHeader, i.csvRows = file.path, file.header, file.rows
	if len(i.csvRows) == 0 {
		i.ctx.Notify(LevelError, "%s carries no data rows", file.path)
		return nil
	}
	i.openMappingForm()
	return nil
}

// openMappingForm maps the file's columns onto the API's import fields. The TUI
// parses the CSV itself, exactly as the web client does in the browser: amounts
// go through api.ParseAmount (never a float64, which would round what the API
// ends up storing) and the external sign or debit/credit convention is
// translated into the API's debit/credit type.
func (i *Import) openMappingForm() {
	columns := make([]Option, 0, len(i.csvHeader))
	for _, name := range i.csvHeader {
		columns = append(columns, Option{Value: name, Label: name})
	}
	m := i.csvMapping
	fields := []Field{
		SelectField("Date column", m.date, columns, true),
		SelectField("Description column", m.description, columns, true),
		SelectField("Amount layout", defaultTo(m.amountMode, "signed"), csvAmountModes, true),
		SelectField("Amount column", m.amount, columns, false),
		SelectField("Type column", m.typeColumn, columns, false),
		SelectField("Debit column", m.debit, columns, false),
		SelectField("Credit column", m.credit, columns, false),
		SelectField("Positive amounts", defaultTo(m.positive, "credit"), csvPositiveOptions, true),
		SelectField("Payee column", m.payee, columns, false),
		SelectField("Date format", defaultTo(m.dateFormat, "auto"), dateFormatOptions, true),
	}
	fields[0].Help = "the file's columns: " + truncate(strings.Join(i.csvHeader, " · "), 80)
	fields[2].Help = "how this export states the direction of a row"
	fields[4].Help = "only for the \"amount + type column\" layout"
	fields[5].Help = "only for the \"debit and credit columns\" layout"
	fields[6].Help = "only for the \"debit and credit columns\" layout"
	fields[7].Help = "what a positive amount means here; a credit-card statement often prints charges as positive"
	fields[8].Help = "optional: a name is matched against your payees, and an unmatched one stays empty"
	i.ctx.Open(NewForm("import.mapping", "Map CSV columns", fields, func(f *Form) tea.Cmd {
		mapping := csvMapping{
			date:        f.Value("Date column"),
			description: f.Value("Description column"),
			amountMode:  f.Value("Amount layout"),
			amount:      f.Value("Amount column"),
			typeColumn:  f.Value("Type column"),
			debit:       f.Value("Debit column"),
			credit:      f.Value("Credit column"),
			positive:    f.Value("Positive amounts"),
			payee:       f.Value("Payee column"),
			dateFormat:  f.Value("Date format"),
		}
		if err := mapping.validate(); err != nil {
			f.SetError(err)
			return nil
		}
		rows, skipped := csvRowsToTransactions(i.csvHeader, i.csvRows, mapping, i.ctx.Ref)
		if len(rows) == 0 {
			f.SetError(errText("no row could be read with this mapping: check the date format and the amount layout"))
			return nil
		}
		i.csvMapping = mapping
		f.Close()
		i.stage(importBatch{source: "csv file " + i.csvPath, rows: rows, skipped: skipped})
		return nil
	}))
}

// csvMapping is the column mapping chosen for one CSV layout, kept so the pane
// can offer the same choices again for another file.
type csvMapping struct {
	date        string
	description string
	// amountMode is "signed", "type" or "separate".
	amountMode string
	amount     string
	typeColumn string
	debit      string
	credit     string
	// positive is what a positive amount means: "credit" or "debit".
	positive   string
	payee      string
	dateFormat string
}

// validate rejects a mapping that cannot produce a row.
func (m csvMapping) validate() error {
	if m.date == "" {
		return errText("map the date column")
	}
	if m.description == "" {
		return errText("map the description column")
	}
	switch m.amountMode {
	case "type":
		if m.amount == "" {
			return errText("map the amount column")
		}
		if m.typeColumn == "" {
			return errText("map the type column")
		}
	case "separate":
		if m.debit == "" && m.credit == "" {
			return errText("map the debit or the credit column")
		}
	default:
		if m.amount == "" {
			return errText("map the amount column")
		}
	}
	return nil
}

// csvCell reads one mapped column out of a record.
func csvCell(record []string, columns map[string]int, column string) string {
	index, ok := columns[column]
	if !ok || index < 0 || index >= len(record) {
		return ""
	}
	return strings.TrimSpace(record[index])
}

// csvRowsToTransactions maps the chosen columns onto import rows. A record the
// mapping cannot resolve is dropped and counted rather than guessed at — a wrong
// direction or a wrong date in a ledger is worse than a missing row. Blank
// records are ignored outright, since they carry nothing to resolve.
func csvRowsToTransactions(header []string, records [][]string, m csvMapping, ref *RefData) ([]api.ImportTransaction, int) {
	columns := make(map[string]int, len(header))
	for index, name := range header {
		if _, exists := columns[name]; !exists {
			columns[name] = index
		}
	}
	rows := make([]api.ImportTransaction, 0, len(records))
	skipped := 0
	for _, record := range records {
		if strings.TrimSpace(strings.Join(record, "")) == "" {
			continue
		}
		row, ok := csvRowToTransaction(record, columns, m, ref)
		if !ok {
			skipped++
			continue
		}
		rows = append(rows, row)
	}
	return rows, skipped
}

// csvRowToTransaction converts one record.
func csvRowToTransaction(record []string, columns map[string]int, m csvMapping, ref *RefData) (api.ImportTransaction, bool) {
	date, ok := parseCSVDate(csvCell(record, columns, m.date), m.dateFormat)
	if !ok {
		return api.ImportTransaction{}, false
	}
	description := csvCell(record, columns, m.description)
	if description == "" {
		return api.ImportTransaction{}, false
	}
	amount, txnType, ok := csvDirection(record, columns, m)
	if !ok {
		return api.ImportTransaction{}, false
	}
	row := api.ImportTransaction{Date: date, Description: description, Amount: amount, Type: txnType}
	if m.payee != "" {
		if payee := matchPayee(csvCell(record, columns, m.payee), ref); payee != "" {
			row.PayeeID = &payee
		}
	}
	return row, true
}

// csvDirection derives the amount and the API's debit/credit type from one
// record, following the layout the user mapped. The API stores a positive
// amount plus a direction, so the magnitude is what is sent whichever way the
// export signed it.
func csvDirection(record []string, columns map[string]int, m csvMapping) (api.Amount, string, bool) {
	switch m.amountMode {
	case "type":
		amount, err := csvAmount(csvCell(record, columns, m.amount))
		if err != nil || amount == "" || amount.IsZero() {
			return "", "", false
		}
		direction, ok := csvType(csvCell(record, columns, m.typeColumn))
		if !ok {
			return "", "", false
		}
		return amount.Abs(), direction, true

	case "separate":
		debit, err := csvAmount(csvCell(record, columns, m.debit))
		if err != nil {
			return "", "", false
		}
		credit, err := csvAmount(csvCell(record, columns, m.credit))
		if err != nil {
			return "", "", false
		}
		if debit != "" && !debit.IsZero() {
			return debit.Abs(), "debit", true
		}
		if credit != "" && !credit.IsZero() {
			return credit.Abs(), "credit", true
		}
		return "", "", false
	}

	// One amount column: the sign carries the direction, and the mapping says
	// what a positive amount means for this export.
	amount, err := csvAmount(csvCell(record, columns, m.amount))
	if err != nil || amount == "" || amount.IsZero() {
		return "", "", false
	}
	if amount.IsNegative() {
		return amount.Abs(), csvOpposite(m.positive), true
	}
	return amount, defaultTo(m.positive, "credit"), true
}

// csvOpposite flips a direction, so the sign convention has one meaning.
func csvOpposite(direction string) string {
	if direction == "debit" {
		return "credit"
	}
	return "debit"
}

// csvType maps a direction column onto the API's two directions. Bank exports
// label it dr/cr, withdrawal/deposit or expense/income; anything else drops the
// row rather than guessing, because guessing the direction of a payment is how a
// ledger ends up wrong.
func csvType(raw string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "debit", "dr", "d", "db", "w", "withdrawal", "withdrawl", "expense", "payment", "out", "outflow":
		return "debit", true
	case "credit", "cr", "c", "deposit", "income", "refund", "in", "inflow":
		return "credit", true
	}
	return "", false
}

// matchPayee resolves a name from the file against the user's payees. The API
// takes an id, so an unknown name stays empty instead of being invented.
func matchPayee(name string, ref *RefData) string {
	name = strings.TrimSpace(name)
	if name == "" || ref == nil {
		return ""
	}
	for _, payee := range ref.Payees {
		if strings.EqualFold(payee.Name, name) {
			return payee.ID
		}
	}
	return ""
}

// csvAmountModes are the shapes an export can use to state a row's direction.
var csvAmountModes = []Option{
	{Value: "signed", Label: "one amount column, sign decides"},
	{Value: "type", Label: "one amount column + a type column"},
	{Value: "separate", Label: "separate debit and credit columns"},
}

// csvPositiveOptions is the sign convention. A statement exported from a credit
// card usually prints a charge as a positive number, the opposite of a savings
// account's export, so the mapping asks instead of assuming.
var csvPositiveOptions = []Option{
	{Value: "credit", Label: "credit (money in)"},
	{Value: "debit", Label: "debit (money out)"},
}

// dateFormatOptions are the layouts both the statement parser's dateFormat hint
// and the CSV reader offer. "auto" leaves the reading to the parser service for
// a PDF, and walks the layouts in turn for a CSV.
var dateFormatOptions = []Option{
	{Value: "auto", Label: "auto-detect"},
	{Value: "DD/MM/YYYY", Label: "DD/MM/YYYY"},
	{Value: "MM/DD/YYYY", Label: "MM/DD/YYYY"},
	{Value: "DD/MM/YY", Label: "DD/MM/YY"},
	{Value: "YYYY-MM-DD", Label: "YYYY-MM-DD"},
	{Value: "DD Mon YYYY", Label: "DD Mon YYYY"},
}

// csvLayouts maps a chosen date format onto the Go layouts that read it. The
// padded and unpadded forms are both listed because Go's Parse insists on the
// digit count a layout names, while exports happily write "3/4/2024".
var csvLayouts = map[string][]string{
	"DD/MM/YYYY":  {"02/01/2006", "2/1/2006"},
	"MM/DD/YYYY":  {"01/02/2006", "1/2/2006"},
	"DD/MM/YY":    {"02/01/06", "2/1/06"},
	"YYYY-MM-DD":  {"2006/01/02", "2006-01-02"},
	"DD Mon YYYY": {"02 Jan 2006", "2 Jan 2006", "02-Jan-2006", "02 January 2006"},
}

// csvAutoLayouts is the order "auto" walks: a full timestamp, then ISO, then the
// day-first reading (what most bank exports print), then month-first, then a
// month name.
var csvAutoLayouts = []string{
	"2006-01-02T15:04:05Z07:00",
	"2006-01-02 15:04:05",
	"2006/01/02",
	"02/01/2006",
	"2/1/2006",
	"01/02/2006",
	"1/2/2006",
	"02/01/06",
	"2/1/06",
	"02 Jan 2006",
	"2 Jan 2006",
	"02-Jan-2006",
	"02 January 2006",
}

// parseCSVDate normalizes an exported date onto the API's date-only layout. An
// explicit choice is trusted and a failure stays a failure: silently trying the
// other reading is how 03/04/2024 becomes the wrong month. "auto" walks the
// layouts instead, and Go's Parse rejects impossible calendar dates, so no row
// can arrive as 2024-15-03.
func parseCSVDate(raw, format string) (string, bool) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", false
	}
	if format != "" && format != "auto" {
		layouts, ok := csvLayouts[format]
		if !ok {
			layouts = csvAutoLayouts
		}
		return parseWithLayouts(value, layouts)
	}
	return parseWithLayouts(value, csvAutoLayouts)
}

// parseWithLayouts tries the layouts against the value and its
// separator-normalized form, because bank exports mix in dots and dashes
// ("15.01.2024", "15-01-2024") that the layouts spell with slashes.
func parseWithLayouts(value string, layouts []string) (string, bool) {
	for _, candidate := range []string{value, normalizeDateSeparators(value)} {
		for _, layout := range layouts {
			if parsed, err := time.Parse(layout, candidate); err == nil {
				return parsed.Format(api.DateLayout), true
			}
		}
	}
	return "", false
}

// normalizeDateSeparators rewrites the dot and dash separators onto the slashes
// the layouts use, leaving a value that carries a month name alone: its spaces
// separate the parts too.
func normalizeDateSeparators(value string) string {
	if strings.ContainsAny(value, " ") {
		return value
	}
	return strings.NewReplacer(".", "/", "-", "/").Replace(value)
}

// csvAmount normalizes one exported amount cell into the API's grammar. An empty
// cell is not an error — the debit/credit layout leaves one side blank on every
// row. The punctuation an export puts around a number (currency symbols,
// thousands separators, parentheses or a trailing minus for a negative) is
// stripped first, and api.ParseAmount is the only thing that decides what a
// valid amount is: no float64 ever touches the value, because a float would
// round what the API stores.
func csvAmount(raw string) (api.Amount, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", nil
	}
	negative := false
	if strings.HasPrefix(value, "(") && strings.HasSuffix(value, ")") {
		negative = true
		value = strings.TrimSpace(value[1 : len(value)-1])
	}
	if strings.HasSuffix(value, "-") {
		negative = true
		value = strings.TrimSpace(strings.TrimSuffix(value, "-"))
	}

	var digits strings.Builder
	for _, r := range value {
		switch {
		case r >= '0' && r <= '9', r == '.', r == ',', r == '-', r == '+':
			digits.WriteRune(r)
		}
	}
	number := digits.String()
	if strings.Trim(number, "+-,.") == "" {
		// Nothing numeric in the cell: absent, not invalid, so a debit/credit
		// row with an empty side is simply skipped.
		return "", nil
	}

	switch {
	case strings.Contains(number, ",") && strings.Contains(number, "."):
		// Both separators present: the rightmost is the decimal one and the
		// other groups thousands ("1.234,56" is a European export).
		if strings.LastIndex(number, ",") > strings.LastIndex(number, ".") {
			number = strings.ReplaceAll(number, ".", "")
			number = strings.Replace(number, ",", ".", 1)
		} else {
			number = strings.ReplaceAll(number, ",", "")
		}
	case strings.Contains(number, ","):
		// Money has at most two decimals, so a lone comma with exactly two
		// digits behind it is a decimal comma ("12,50"); anything else can only
		// be a thousands separator ("1,234").
		if index := strings.LastIndex(number, ","); strings.Count(number, ",") == 1 && len(number)-index-1 == 2 {
			number = strings.Replace(number, ",", ".", 1)
		} else {
			number = strings.ReplaceAll(number, ",", "")
		}
	}
	if negative && !strings.HasPrefix(number, "-") {
		number = "-" + number
	}
	return api.ParseAmount(number)
}

// payeeRows counts the parsed rows that carry a payee, which is the one thing
// the preview cannot show in a date/description/amount/type table.
func payeeRows(rows []api.ImportTransaction) int {
	count := 0
	for _, row := range rows {
		if row.PayeeID != nil {
			count++
		}
	}
	return count
}

// paperlessFilterSummary describes the active document filters.
func paperlessFilterSummary(query api.PaperlessQuery) string {
	var bits []string
	add := func(label string, include, exclude []string) {
		if len(include) > 0 {
			bits = append(bits, label+"="+strings.Join(include, ","))
		}
		if len(exclude) > 0 {
			bits = append(bits, label+" not "+strings.Join(exclude, ","))
		}
	}
	add("correspondent", query.CorrespondentInc, query.CorrespondentExc)
	add("type", query.DocumentTypeInc, query.DocumentTypeExc)
	add("tag", query.TagInc, query.TagExc)
	return strings.Join(bits, " · ")
}

// View implements Screen.
func (i *Import) View(width, height int) string {
	th := i.ctx.Theme
	lines := []string{i.paneBar()}
	if context := i.contextLine(width); context != "" {
		lines = append(lines, context)
	}
	lines = append(lines, "")

	used := 1 // the footer line
	for _, line := range lines {
		used += strings.Count(line, "\n") + 1
	}

	var body string
	if i.pending != nil {
		notes := i.validationLines(width)
		used += len(notes)
		body = strings.Join(append(notes, i.preview.View(th, width, max(3, height-used), "no rows were parsed")), "\n")
	} else {
		bodyHeight := max(3, height-used)
		switch i.pane {
		case panePaperless:
			body = i.paperlessBody(width, bodyHeight)
		case paneCSV:
			body = i.csvBody(width)
		default:
			body = i.statementBody(width)
		}
	}

	content := strings.Join(lines, "\n") + "\n" + body + "\n" + i.footerLine()
	return trimToBox(content, width, height)
}

// paneBar renders the source tabs with the active one highlighted.
func (i *Import) paneBar() string {
	th := i.ctx.Theme
	tabs := make([]string, 0, len(importPaneNames))
	for index, name := range importPaneNames {
		if importPane(index) == i.pane {
			tabs = append(tabs, th.Title.Render("["+name+"]"))
			continue
		}
		tabs = append(tabs, th.Subtle.Render(name))
	}
	return strings.Join(tabs, th.Subtle.Render(" · "))
}

// contextLine is the line above the body: what the visible pane is showing, or
// what the last import wrote. Paging, filters and the parsed batch's own report
// stay visible there while the list scrolls.
func (i *Import) contextLine(width int) string {
	th := i.ctx.Theme
	if i.pending != nil {
		batch := i.pending
		bits := []string{
			batch.source,
			pluralise(batch.pageCount, "page", "pages"),
			pluralise(batch.transactionCount, "transaction", "transactions"),
		}
		if batch.skipped > 0 {
			bits = append(bits, pluralise(batch.skipped, "row", "rows")+" skipped")
		}
		if matched := payeeRows(batch.rows); matched > 0 {
			bits = append(bits, pluralise(matched, "row", "rows")+" matched a payee")
		}
		head := th.Title.Render("preview: nothing written yet")
		line := head + "  " + th.Subtle.Render(truncate(strings.Join(bits, " · "), max(10, width-len("preview: nothing written yet")-2)))
		if summary := importSummary(batch.summary); summary != "" {
			line += "\n" + th.Subtle.Render(truncate(summary, width))
		}
		return line
	}

	var headline string
	if i.report != nil {
		headline = th.SuccessText.Render(fmt.Sprintf("imported %d of %d into %s, %d duplicate(s) skipped",
			i.report.result.Imported, i.report.result.Total, i.report.account, i.report.result.Duplicates))
	}

	var info string
	switch i.pane {
	case panePaperless:
		if i.searching {
			return th.Title.Render("/") + i.search.View()
		}
		bits := []string{
			pluralise(i.docs.TotalCount, "document", "documents"),
			fmt.Sprintf("page %d/%d", max(1, i.query.Page), max(1, i.docs.TotalPages)),
		}
		if i.query.Search != "" {
			bits = append(bits, "search="+i.query.Search)
		}
		if filters := paperlessFilterSummary(i.query); filters != "" {
			bits = append(bits, filters)
		}
		info = th.Subtle.Render(truncate(strings.Join(bits, " · "), width))
	case paneCSV:
		info = th.Subtle.Render(truncate(defaultTo(i.csvPath, "no file open"), width))
	default:
		info = th.Subtle.Render("statement pdf · the parse writes nothing")
	}

	if headline == "" {
		return info
	}
	if info == "" {
		return headline
	}
	return headline + "  " + info
}

// validationLines renders the parser's per-page reconciliation errors. A
// non-empty ValidationErrors means the parser could not make its own pages add
// up, so the rows are suspect and the preview says so above the table rather
// than after it.
func (i *Import) validationLines(width int) []string {
	if i.pending == nil || len(i.pending.errors) == 0 {
		return nil
	}
	th := i.ctx.Theme
	lines := []string{th.Error.Render(fmt.Sprintf("%s: the parser could not reconcile its pages, so these rows are suspect",
		pluralise(len(i.pending.errors), "validation error", "validation errors")))}
	for _, problem := range i.pending.errors {
		lines = append(lines, th.Error.Render(wrapText("  · "+problem, max(20, width-2))))
	}
	return lines
}

// footerLine is the pane's own hint, below the body; the status bar already
// lists the same keys.
func (i *Import) footerLine() string {
	th := i.ctx.Theme
	switch {
	case i.pending != nil:
		return th.Subtle.Render("enter review + import · esc discard (nothing has been written)")
	case i.pane == panePaperless:
		return th.Subtle.Render("enter parse the highlighted document · / search · f filters · ,/. page · p source pane")
	case i.pane == paneCSV:
		return th.Subtle.Render("o open a csv file · m map columns · p source pane")
	default:
		return th.Subtle.Render("n parse a statement pdf · p source pane")
	}
}

// statementBody documents the statement pane: what the extractor registry holds
// and the three refusals the user can do something about. A 401 means the PDF is
// password-protected, a 413 that the upload is too large, and a 429 that the
// parser is busy and the same file should be sent again shortly.
func (i *Import) statementBody(width int) string {
	th := i.ctx.Theme
	wrap := max(20, width)
	lines := []string{
		th.Header.Render("Bank statement PDF"),
		"",
		wrapText("Parse a statement into candidate rows, look them over, then pick the account to import into. The parse itself writes nothing: the rows only reach the ledger when the import step commits them.", wrap),
		"",
	}
	switch {
	case i.extractorsErr != nil:
		lines = append(lines, th.Error.Render("the extractor registry is unavailable: "+i.extractorsErr.Error()))
	case !i.extractorsReady:
		lines = append(lines, th.Subtle.Render("loading extractors…"))
	case len(i.extractors) == 0:
		lines = append(lines, th.Subtle.Render("the parser offers no named extractors; leaving the extractor blank in the form uses the server's default"))
	default:
		labels := make([]string, 0, len(i.extractors))
		for _, extractor := range i.extractors {
			labels = append(labels, defaultTo(extractor.DisplayName, extractor.Name))
		}
		lines = append(lines, th.Subtle.Render(wrapText("extractors: "+strings.Join(labels, " · "), wrap)))
	}
	lines = append(lines, "",
		th.Subtle.Render(wrapText("A password-protected PDF answers 401, so retry with the password filled in. An upload over 20 MB is refused with 413. A 429 means the parser is busy, so send the file again in a moment.", wrap)))
	return strings.Join(lines, "\n")
}

// paperlessBody renders the Paperless pane. The document list is only called
// once a token is stored: without one the endpoint answers 400, and a screen
// that showed that error would be reporting a request it should not have made.
func (i *Import) paperlessBody(width, height int) string {
	th := i.ctx.Theme
	wrap := max(20, width)
	switch {
	case i.settingsErr != nil:
		return strings.Join([]string{
			th.Error.Render("Paperless-ngx settings could not be read: " + i.settingsErr.Error()),
			"",
			th.Subtle.Render(wrapText("Check the URL and API token in the Settings screen.", wrap)),
		}, "\n")
	case !i.settingsReady:
		return th.Subtle.Render("loading Paperless settings…")
	case !i.settings.HasToken:
		return strings.Join([]string{
			th.WarnText.Render("Paperless-ngx is not configured"),
			"",
			wrapText("Add the Paperless URL and API token in the Settings screen. The document list is not fetched until a token is stored, because it would only answer 400.", wrap),
			"",
			th.Subtle.Render(truncate("url: "+defaultTo(i.settings.PaperlessURL, "unset")+" · tag: "+defaultTo(i.settings.PaperlessTag, "unset"), wrap)),
		}, "\n")
	}

	switch {
	case i.docsErr != nil:
		return th.Error.Render("the document list could not be read: " + i.docsErr.Error())
	case !i.docsReady:
		return th.Subtle.Render("loading documents…")
	case len(i.docs.Documents) == 0:
		reference := "correspondents: " + documentNames(i.docs.Correspondents) +
			"\ndocument types: " + documentNames(i.docs.DocumentTypes) +
			"\ntags: " + documentNames(i.docs.Tags)
		return strings.Join([]string{
			th.Subtle.Render("no document matches the current search and filters"),
			"",
			th.Subtle.Render(wrapText(reference, wrap)),
		}, "\n")
	}

	// The lookup tables are what the filter form's names have to match, so they
	// stay under the table — trimmed to three lines rather than crowding it out.
	reference := wrapText(fmt.Sprintf("correspondents: %s · types: %s · tags: %s",
		documentNames(i.docs.Correspondents), documentNames(i.docs.DocumentTypes), documentNames(i.docs.Tags)), wrap)
	if rows := strings.Split(reference, "\n"); len(rows) > 3 {
		reference = strings.Join(rows[:3], "\n") + " …"
	}
	referenceHeight := strings.Count(reference, "\n") + 1
	table := i.docTable.View(th, width, max(3, height-referenceHeight-1), "no documents")
	return table + "\n" + th.Subtle.Render(reference)
}

// csvBody documents the CSV pane: the columns the file carries, a sample of the
// rows, and the reminder that the parsing happens here rather than on the
// server, since the API has no CSV endpoint and the web client parses in the
// browser for the same reason.
func (i *Import) csvBody(width int) string {
	th := i.ctx.Theme
	wrap := max(20, width)
	lines := []string{th.Header.Render("CSV export")}
	if len(i.csvHeader) == 0 {
		return strings.Join(append(lines,
			"",
			wrapText("Open a CSV file exported from your bank. It is read on this machine, the columns are mapped onto the API's import fields, and the rows are posted like any other import.", wrap),
			"",
			th.Subtle.Render("o  choose a file · p  switch source pane"),
		), "\n")
	}
	lines = append(lines,
		"",
		fmt.Sprintf("%s · %s · %s", i.csvPath, pluralise(len(i.csvRows), "row", "rows"), pluralise(len(i.csvHeader), "column", "columns")),
		"",
		th.Subtle.Render(wrapText("columns: "+strings.Join(i.csvHeader, " · "), wrap)),
		"",
	)
	for index, record := range i.csvRows {
		if index >= 5 {
			break
		}
		lines = append(lines, th.Subtle.Render(truncate(strings.Join(record, " | "), width)))
	}
	if len(i.csvRows) > 5 {
		lines = append(lines, th.Subtle.Render(fmt.Sprintf("… %s", pluralise(len(i.csvRows)-5, "more row", "more rows"))))
	}
	lines = append(lines, "", th.Subtle.Render("m  map columns · o  open another file"))
	return strings.Join(lines, "\n")
}
