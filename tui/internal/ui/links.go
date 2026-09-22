package ui

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbletea"

	"github.com/fintrak/client/api"
)

func init() {
	registerScreen(80, func(ctx *Ctx) Screen { return NewLinks(ctx) })
}

// linkSuggestionLimit is how many suggestions one page holds. The server caps
// the parameter at 100 and defaults to 50; asking for 50 keeps a page close to a
// terminal's height.
const linkSuggestionLimit = 50

// Links is the transaction-link screen — the relationships that make this app
// more than a ledger. A link pairs two of the user's own transactions: a
// transfer between two accounts, a refund or cashback against an earlier
// purchase, or a bill payment. It has three panes:
//
//   - Links: what is recorded, with create/bulk-create/delete.
//   - Suggestions: the server's guesses. Nothing is ever linked from here
//     without the user confirming a row, because a wrong transfer quietly
//     rewrites two transactions' categories and payees.
//   - Circular money: the account-to-account flows the money-flow graph cannot
//     draw (it must stay acyclic), plus one-sided flows that may be
//     half-entered transfers.
//
// The panes share one table and one cursor; the screen's methods dispatch on the
// active pane instead of duplicating state per pane.
type Links struct {
	ctx   *Ctx
	table Table
	keys  linkKeys

	pane linkPane

	// Links pane. The type filter is sent to the API rather than applied here,
	// so the server's own paging and ordering semantics are preserved.
	links       []api.Link
	linksLoaded bool
	typeFilter  string
	selected    map[string]bool

	// Suggestions pane. The suggestion endpoints carry no total, only HasMore,
	// so the page cursor is the only way to know where the user is.
	suggKind   string
	sugg       api.SuggestionPage
	suggLoaded bool
	suggPage   int
	suggSel    map[int]bool

	// Circular money pane.
	cycles       api.LinkCycleReport
	cyclesLoaded bool
	cycleOffset  int
	window       linkWindow

	// lastLinkType remembers the type the user last created, so a second link
	// of the same kind does not need the field chosen again.
	lastLinkType string
}

// linkPane names one of the screen's three panes.
type linkPane int

// Panes, in cycling order.
const (
	linkPaneLinks linkPane = iota
	linkPaneSuggestions
	linkPaneCycles
)

// label names the pane in the header.
func (p linkPane) label() string {
	switch p {
	case linkPaneSuggestions:
		return "Suggestions"
	case linkPaneCycles:
		return "Circular money"
	default:
		return "Links"
	}
}

// linkWindow is the circular-money filter. The API reads an empty bound as
// unbounded and an empty account as unfiltered, so the zero value is a valid
// "everything" window.
type linkWindow struct {
	dateFrom  string
	dateTo    string
	accountID string
}

// describe renders the window for the header; an open side reads "…".
func (w linkWindow) describe(ref *RefData) string {
	span := defaultTo(w.dateFrom, "…") + " … " + defaultTo(w.dateTo, "…")
	account := "all accounts"
	if w.accountID != "" {
		account = ref.AccountName(w.accountID)
	}
	return span + " · " + account
}

// linkKeys are the screen's bindings. `tab` would be the natural pane key, but
// the App owns it (it moves focus between the sidebar and the content area), so
// panes cycle on `p`.
type linkKeys struct {
	Pane      key.Binding
	Select    key.Binding
	SelectAll key.Binding
	New       key.Binding
	BulkNew   key.Binding
	Delete    key.Binding
	Detail    key.Binding
	Type      key.Binding
	Transfer  key.Binding
	Cashback  key.Binding
	Confirm   key.Binding
	PrevPage  key.Binding
	NextPage  key.Binding
	Window    key.Binding
}

func newLinkKeys() linkKeys {
	return linkKeys{
		Pane:      key.NewBinding(key.WithKeys("p"), key.WithHelp("p", "pane")),
		Select:    key.NewBinding(key.WithKeys(" "), key.WithHelp("space", "select")),
		SelectAll: key.NewBinding(key.WithKeys("a"), key.WithHelp("a", "all/none")),
		New:       key.NewBinding(key.WithKeys("n"), key.WithHelp("n", "new")),
		BulkNew:   key.NewBinding(key.WithKeys("b"), key.WithHelp("b", "bulk from ids")),
		Delete:    key.NewBinding(key.WithKeys("d"), key.WithHelp("d", "delete")),
		Detail:    key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "detail")),
		Type:      key.NewBinding(key.WithKeys("t"), key.WithHelp("t", "type filter")),
		Transfer:  key.NewBinding(key.WithKeys("T"), key.WithHelp("T", "transfer suggestions")),
		Cashback:  key.NewBinding(key.WithKeys("C"), key.WithHelp("C", "cashback suggestions")),
		Confirm:   key.NewBinding(key.WithKeys("y"), key.WithHelp("y", "confirm -> link")),
		PrevPage:  key.NewBinding(key.WithKeys(","), key.WithHelp(",", "prev page")),
		NextPage:  key.NewBinding(key.WithKeys("."), key.WithHelp(".", "next page")),
		Window:    key.NewBinding(key.WithKeys("f"), key.WithHelp("f", "window")),
	}
}

// NewLinks builds the links screen.
func NewLinks(ctx *Ctx) *Links {
	l := &Links{
		ctx:          ctx,
		keys:         newLinkKeys(),
		selected:     map[string]bool{},
		suggSel:      map[int]bool{},
		suggKind:     "transfer",
		suggPage:     1,
		typeFilter:   "",
		lastLinkType: "transfer",
	}
	l.applyRows()
	return l
}

// Title implements Screen.
func (l *Links) Title() string { return "Links" }

// Keys implements Screen, advertising only what the active pane does.
func (l *Links) Keys() []key.Binding {
	switch l.pane {
	case linkPaneSuggestions:
		return []key.Binding{
			l.keys.Transfer, l.keys.Cashback, l.keys.Select, l.keys.Confirm,
			l.keys.PrevPage, l.keys.NextPage, l.keys.Pane,
		}
	case linkPaneCycles:
		return []key.Binding{l.keys.Window, l.keys.Pane}
	default:
		return []key.Binding{
			l.keys.New, l.keys.BulkNew, l.keys.Type, l.keys.Select, l.keys.SelectAll,
			l.keys.Delete, l.keys.Detail, l.keys.Pane,
		}
	}
}

// Refresh implements Screen: whichever pane is showing is the one reloaded.
func (l *Links) Refresh() tea.Cmd { return l.refreshPane() }

// refreshPane fetches the active pane. Each pane keeps its own rows, filter and
// page, so cycling through them does not lose anything.
func (l *Links) refreshPane() tea.Cmd {
	switch l.pane {
	case linkPaneSuggestions:
		return l.reloadSuggestions()
	case linkPaneCycles:
		return l.reloadCycles()
	default:
		return l.reloadLinks()
	}
}

// reloadLinks fetches the link list. The type filter goes to the API: the
// endpoint accepts "transfer", "refund", "cashback" or "bill_payment", and an
// empty value is its unfiltered default.
func (l *Links) reloadLinks() tea.Cmd {
	filter := l.typeFilter
	return load("links.list", func(ctx context.Context) ([]api.Link, error) {
		return l.ctx.Client.ListLinks(ctx, filter, "")
	})
}

// reloadSuggestions fetches a page of the active suggestion kind.
func (l *Links) reloadSuggestions() tea.Cmd {
	kind, page, limit := l.suggKind, l.suggPage, linkSuggestionLimit
	return load("links.suggest", func(ctx context.Context) (api.SuggestionPage, error) {
		if kind == "cashback" {
			return l.ctx.Client.CashbackSuggestions(ctx, page, limit)
		}
		return l.ctx.Client.TransferSuggestions(ctx, page, limit)
	})
}

// reloadCycles fetches the circular-money report for the current window.
func (l *Links) reloadCycles() tea.Cmd {
	window := l.window
	return load("links.cycles", func(ctx context.Context) (api.LinkCycleReport, error) {
		return l.ctx.Client.LinkCycles(ctx, window.dateFrom, window.dateTo, window.accountID)
	})
}

// Update implements Screen.
func (l *Links) Update(msg tea.Msg) tea.Cmd {
	switch m := msg.(type) {
	case loaded[[]api.Link]:
		if m.tag != "links.list" {
			break
		}
		l.linksLoaded = true
		if m.err != nil {
			// A failed refresh keeps the rows the user is looking at; the App
			// shows the reason on the status line.
			l.ctx.Notify(LevelError, "%s", m.err)
			return nil
		}
		l.links = m.data
		l.pruneSelection()
		l.applyRows()
		return nil

	case loaded[api.SuggestionPage]:
		if m.tag != "links.suggest" {
			break
		}
		l.suggLoaded = true
		if m.err != nil {
			l.ctx.Notify(LevelError, "%s", m.err)
			return nil
		}
		l.sugg = m.data
		// Selections are row indexes, so a new page invalidates them.
		l.suggSel = map[int]bool{}
		l.applyRows()
		return nil

	case loaded[api.LinkCycleReport]:
		if m.tag != "links.cycles" {
			break
		}
		l.cyclesLoaded = true
		if m.err != nil {
			l.ctx.Notify(LevelError, "%s", m.err)
			return nil
		}
		l.cycles = m.data
		l.cycleOffset = 0
		return nil

	case done:
		if !strings.HasPrefix(m.tag, "links.") {
			break
		}
		if m.err != nil {
			// The App has already shown it, inside the form that caused it when
			// one is open, so the user's input is still there to correct.
			return nil
		}
		l.clearSelection()
		return l.refreshPane()

	case tea.KeyMsg:
		return l.handleKey(m)
	}
	return nil
}

// handleKey routes a key press: first the pane-independent navigation and pane
// cycle, then whatever the active pane binds.
func (l *Links) handleKey(msg tea.KeyMsg) tea.Cmd {
	if cmd, handled := l.navKey(msg); handled {
		return cmd
	}
	switch l.pane {
	case linkPaneSuggestions:
		return l.suggestionKey(msg)
	case linkPaneCycles:
		return l.cycleKey(msg)
	default:
		return l.linkKey(msg)
	}
}

// navKey handles movement and the pane cycle.
func (l *Links) navKey(msg tea.KeyMsg) (tea.Cmd, bool) {
	if keyMatches(l.keys.Pane, msg) {
		return l.nextPane(), true
	}
	switch msg.String() {
	case "up", "k":
		l.move(-1)
	case "down", "j":
		l.move(1)
	case "pgup", "ctrl+b":
		l.movePage(-1)
	case "pgdown", "ctrl+f":
		l.movePage(1)
	case "home":
		l.moveEdge(false)
	case "end", "G":
		l.moveEdge(true)
	default:
		return nil, false
	}
	return nil, true
}

// nextPane cycles the panes and loads the one it lands on.
func (l *Links) nextPane() tea.Cmd {
	switch l.pane {
	case linkPaneLinks:
		l.pane = linkPaneSuggestions
	case linkPaneSuggestions:
		l.pane = linkPaneCycles
	default:
		l.pane = linkPaneLinks
	}
	l.applyRows()
	return l.refreshPane()
}

// move shifts the cursor, or the report's scroll offset in the pane that has no
// table.
func (l *Links) move(delta int) {
	if l.pane == linkPaneCycles {
		l.cycleOffset = max(0, l.cycleOffset+delta)
		return
	}
	l.table.Move(delta)
}

// movePage moves by a screenful.
func (l *Links) movePage(delta int) {
	if l.pane == linkPaneCycles {
		l.cycleOffset = max(0, l.cycleOffset+delta*10)
		return
	}
	l.table.Page(delta, 20)
}

// moveEdge jumps to the first or last row (or the top/bottom of the report).
func (l *Links) moveEdge(last bool) {
	if l.pane == linkPaneCycles {
		if last {
			l.cycleOffset = 1 << 20
		} else {
			l.cycleOffset = 0
		}
		return
	}
	if last {
		l.table.End()
	} else {
		l.table.Home()
	}
}

// linkKey handles the Links pane's actions.
func (l *Links) linkKey(msg tea.KeyMsg) tea.Cmd {
	switch {
	case keyMatches(l.keys.Type, msg):
		l.openTypePicker()
		return nil
	case keyMatches(l.keys.New, msg):
		l.openCreateForm()
		return nil
	case keyMatches(l.keys.BulkNew, msg):
		l.openBulkForm()
		return nil
	case keyMatches(l.keys.Delete, msg):
		l.confirmDelete()
		return nil
	case keyMatches(l.keys.Select, msg):
		l.toggleSelection()
		return nil
	case keyMatches(l.keys.SelectAll, msg):
		l.toggleAll()
		return nil
	case keyMatches(l.keys.Detail, msg):
		l.showDetail()
		return nil
	}
	return nil
}

// suggestionKey handles the Suggestions pane's actions.
func (l *Links) suggestionKey(msg tea.KeyMsg) tea.Cmd {
	switch {
	case keyMatches(l.keys.Transfer, msg):
		return l.setSuggestionKind("transfer")
	case keyMatches(l.keys.Cashback, msg):
		return l.setSuggestionKind("cashback")
	case keyMatches(l.keys.Select, msg):
		l.toggleSuggestion()
		return nil
	case keyMatches(l.keys.Confirm, msg):
		return l.confirmSuggestions()
	case keyMatches(l.keys.PrevPage, msg):
		if l.suggPage <= 1 {
			return nil
		}
		l.suggPage--
		l.suggSel = map[int]bool{}
		return l.reloadSuggestions()
	case keyMatches(l.keys.NextPage, msg):
		// The response has no total, so only the server's HasMore can say
		// whether a further page exists.
		if !l.sugg.HasMore {
			return nil
		}
		l.suggPage++
		l.suggSel = map[int]bool{}
		return l.reloadSuggestions()
	}
	return nil
}

// setSuggestionKind switches between the transfer and cashback suggesters.
func (l *Links) setSuggestionKind(kind string) tea.Cmd {
	if l.suggKind == kind {
		return nil
	}
	l.suggKind = kind
	l.suggPage = 1
	l.suggSel = map[int]bool{}
	return l.reloadSuggestions()
}

// cycleKey handles the Circular money pane's actions.
func (l *Links) cycleKey(msg tea.KeyMsg) tea.Cmd {
	if keyMatches(l.keys.Window, msg) {
		l.openWindowForm()
	}
	return nil
}

// applyRows rebuilds the table for the active pane. The list panes share one
// table, so the columns are replaced with the pane's own and the cursor is left
// where SetRows can keep it.
func (l *Links) applyRows() {
	switch l.pane {
	case linkPaneSuggestions:
		l.table.SetColumns(linkSuggestionColumns()...)
		l.table.SetRows(l.suggestionRows())
	default:
		l.table.SetColumns(linkRowColumns()...)
		l.table.SetRows(l.linkRows())
	}
}

// linkRowColumns describe a link: both of its transactions, side by side.
func linkRowColumns() []Column {
	return []Column{
		{Title: "Type", Width: 14},
		{Title: "Date", Width: 11},
		{Title: "From (account · description)"},
		{Title: "To (account · description)"},
		{Title: "Amount", Width: 14, Align: AlignRight},
	}
}

// linkSuggestionColumns describe a proposed pair plus the server's confidence.
func linkSuggestionColumns() []Column {
	return []Column{
		{Title: "Date", Width: 11},
		{Title: "Debit (account · description)"},
		{Title: "Credit (account · description)"},
		{Title: "Amount", Width: 14, Align: AlignRight},
		{Title: "Score", Width: 6, Align: AlignRight},
	}
}

// linkRows renders one row per link. Both ends are shown because a link is only
// meaningful as a pair.
func (l *Links) linkRows() [][]Cell {
	rows := make([][]Cell, 0, len(l.links))
	for _, link := range l.links {
		marker := "  "
		if l.selected[link.ID] {
			marker = "▌ "
		}
		date, amount := formatDate(link.CreatedAt), ""
		from, to := "—", "—"
		if link.FromTxn != nil {
			// The money leaves on the FROM side, so its date and signed amount
			// are the pair's anchor; the detail overlay shows both dates.
			date = formatDate(link.FromTxn.Date)
			amount = signedAmount(link.FromTxn.Amount, link.FromTxn.Type)
			from = linkTxnSide(link.FromTxn, l.ctx.Ref)
		}
		if link.ToTxn != nil {
			to = linkTxnSide(link.ToTxn, l.ctx.Ref)
		}
		rows = append(rows, []Cell{
			Text(marker + link.Type),
			Text(date),
			Muted(from),
			Muted(to),
			Cell{Text: amount, Role: RoleMoney},
		})
	}
	return rows
}

// linkTxnSide renders one end of a link as "account · description". A link
// returned by CreateLink carries no joined transaction (the API joins them on
// reads only), so the row still renders from the link's own ids.
func linkTxnSide(t *api.Transaction, ref *RefData) string {
	account := defaultTo(t.AccountName, ref.AccountName(t.AccountID))
	if t.Description == "" {
		return account
	}
	return account + " · " + t.Description
}

// suggestionRows renders one row per proposed pair, marking the ones the user
// has selected for confirmation.
func (l *Links) suggestionRows() [][]Cell {
	rows := make([][]Cell, 0, len(l.sugg.Data))
	for i, s := range l.sugg.Data {
		marker := "  "
		if l.suggSel[i] {
			marker = "▌ "
		}
		rows = append(rows, []Cell{
			Text(marker + formatDate(s.DebitTxn.Date)),
			Muted(linkTxnSide(&s.DebitTxn, l.ctx.Ref)),
			Muted(linkTxnSide(&s.CreditTxn, l.ctx.Ref)),
			Cell{Text: s.DebitTxn.Amount.Display(), Role: RoleMoney},
			Cell{Text: fmt.Sprintf("%.2f", s.Score), Role: RoleMuted},
		})
	}
	return rows
}

// currentLink returns the cursor link.
func (l *Links) currentLink() (api.Link, bool) {
	index := l.table.Cursor()
	if index < 0 || index >= len(l.links) {
		return api.Link{}, false
	}
	return l.links[index], true
}

// currentSuggestion returns the cursor suggestion.
func (l *Links) currentSuggestion() (api.TransferSuggestion, bool) {
	index := l.table.Cursor()
	if index < 0 || index >= len(l.sugg.Data) {
		return api.TransferSuggestion{}, false
	}
	return l.sugg.Data[index], true
}

// pruneSelection drops links that are no longer on screen, so a bulk delete can
// never touch a link the user cannot see.
func (l *Links) pruneSelection() {
	visible := make(map[string]bool, len(l.links))
	for _, link := range l.links {
		visible[link.ID] = true
	}
	for id := range l.selected {
		if !visible[id] {
			delete(l.selected, id)
		}
	}
}

// toggleSelection flips the cursor link's selection.
func (l *Links) toggleSelection() {
	link, ok := l.currentLink()
	if !ok {
		return
	}
	if l.selected[link.ID] {
		delete(l.selected, link.ID)
	} else {
		l.selected[link.ID] = true
	}
	l.applyRows()
}

// toggleAll selects every listed link, or clears when all are selected.
func (l *Links) toggleAll() {
	all := len(l.links) > 0
	for _, link := range l.links {
		if !l.selected[link.ID] {
			all = false
			break
		}
	}
	l.selected = map[string]bool{}
	if !all {
		for _, link := range l.links {
			l.selected[link.ID] = true
		}
	}
	l.applyRows()
}

// toggleSuggestion flips the cursor suggestion's selection.
func (l *Links) toggleSuggestion() {
	index := l.table.Cursor()
	if index < 0 || index >= len(l.sugg.Data) {
		return
	}
	if l.suggSel[index] {
		delete(l.suggSel, index)
	} else {
		l.suggSel[index] = true
	}
	l.applyRows()
}

// clearSelection empties both selections.
func (l *Links) clearSelection() {
	l.selected = map[string]bool{}
	l.suggSel = map[int]bool{}
	l.applyRows()
}

// selectedIDs returns the selected link ids, in list order.
func (l *Links) selectedIDs() []string {
	ids := make([]string, 0, len(l.selected))
	for _, link := range l.links {
		if l.selected[link.ID] {
			ids = append(ids, link.ID)
		}
	}
	return ids
}

// selectedSuggestions returns the selected suggestions as create bodies, in the
// order they appear on screen.
func (l *Links) selectedSuggestions() []api.CreateLinkRequest {
	requests := make([]api.CreateLinkRequest, 0, len(l.suggSel))
	for i, s := range l.sugg.Data {
		if l.suggSel[i] {
			requests = append(requests, linkSuggestionRequest(s, l.suggKind))
		}
	}
	return requests
}

// linkSuggestionRequest turns a suggestion into a create body. Both endpoints
// propose a debit and a credit, so the direction is debit -> credit and only the
// link type differs between a transfer and a cashback.
func linkSuggestionRequest(s api.TransferSuggestion, kind string) api.CreateLinkRequest {
	return api.CreateLinkRequest{
		Type:      kind,
		FromTxnID: s.DebitTxn.ID,
		ToTxnID:   s.CreditTxn.ID,
	}
}

// openTypePicker picks the link type that is sent to the API as the list filter;
// the empty value is the server's "everything" and is offered as "all types".
func (l *Links) openTypePicker() {
	picker := NewPicker("Link type", linkTypeOptions(), l.typeFilter, false, "")
	picker.OnSelect = func(value string) tea.Cmd {
		l.typeFilter = value
		l.clearSelection()
		return l.reloadLinks()
	}
	l.ctx.Open(picker)
}

// linkTypeChoices are the types a link may be created with. The API rejects any
// other value, so the forms only offer these four.
func linkTypeChoices() []Option {
	return []Option{
		{Value: "transfer", Label: "transfer"},
		{Value: "refund", Label: "refund"},
		{Value: "cashback", Label: "cashback"},
		{Value: "bill_payment", Label: "bill payment"},
	}
}

// linkTypeOptions adds the unfiltered entry the list filter needs.
func linkTypeOptions() []Option {
	return append([]Option{{Value: "", Label: "all types"}}, linkTypeChoices()...)
}

// openCreateForm links two transactions by id. The ids are the API's own
// identifiers, which is why the Links detail overlay shows them.
func (l *Links) openCreateForm() {
	fields := []Field{
		SelectField("Type", defaultTo(l.lastLinkType, "transfer"), linkTypeChoices(), true),
		{
			Label: "From transaction", Kind: FieldText, Width: 40,
			Validate: required("from transaction id"),
			Help:     "id of the transaction the money leaves",
		},
		{
			Label: "To transaction", Kind: FieldText, Width: 40,
			Validate: required("to transaction id"),
			Help:     "id of the transaction the money arrives in",
		},
		{Label: "Notes", Kind: FieldText, Width: 44, Help: "why these two belong together"},
	}
	l.ctx.Open(NewForm("links.create", "New link", fields, func(f *Form) tea.Cmd {
		req := api.CreateLinkRequest{
			Type:      f.Value("Type"),
			FromTxnID: strings.TrimSpace(f.Value("From transaction")),
			ToTxnID:   strings.TrimSpace(f.Value("To transaction")),
			Notes:     f.Value("Notes"),
		}
		l.lastLinkType = req.Type
		// A self-link (400) and an exact duplicate (409) are rejected by the
		// API; the error lands back in this form, so the ids stay editable.
		return act("links.create", "link created", false, func(ctx context.Context) error {
			_, err := l.ctx.Client.CreateLink(ctx, req)
			return err
		})
	}))
}

// openBulkForm links many pairs in one call. The single-create form is the only
// other path, so this form takes raw ids: "fromId,toId" pairs separated by ";",
// because the shared form has no multi-line field.
func (l *Links) openBulkForm() {
	fields := []Field{
		SelectField("Type", defaultTo(l.lastLinkType, "transfer"), linkTypeChoices(), true),
		{
			Label: "Pairs", Kind: FieldText, Width: 44,
			Placeholder: "fromId,toId; fromId,toId",
			Validate:    linkValidatePairs,
			Help:        "fromId,toId pairs separated by ';' — the API skips duplicates",
		},
	}
	l.ctx.Open(NewForm("links.bulk", "Link many transactions", fields, func(f *Form) tea.Cmd {
		linkType := f.Value("Type")
		requests, err := linkParsePairs(f.Value("Pairs"))
		if err != nil {
			f.SetError(err)
			return nil
		}
		for i := range requests {
			requests[i].Type = linkType
			requests[i].Notes = "created from the bulk link form"
		}
		l.lastLinkType = linkType
		return linkActCount("links.bulk", func(ctx context.Context) (int, error) {
			return l.ctx.Client.BulkCreateLinks(ctx, requests)
		}, func(n int) string {
			return fmt.Sprintf("created %s (duplicates skipped)", pluralise(n, "link", "links"))
		})
	}))
}

// linkValidatePairs is the bulk form's validator; an empty list is the mistake
// this form exists to catch.
func linkValidatePairs(value string) error {
	_, err := linkParsePairs(value)
	return err
}

// linkParsePairs turns "fromId,toId" pairs into create bodies without a type,
// which the caller fills in. Pairs are separated by ";" or a newline: the shared
// form's text field is a single-line input whose sanitizer strips control
// characters, so a literal newline can never reach this parser — the ";" is what
// makes several pairs enterable at once.
func linkParsePairs(raw string) ([]api.CreateLinkRequest, error) {
	fields := strings.FieldsFunc(raw, func(r rune) bool { return r == '\n' || r == ';' })
	requests := make([]api.CreateLinkRequest, 0, len(fields))
	for i, field := range fields {
		field = strings.TrimSpace(field)
		if field == "" {
			continue
		}
		parts := strings.Split(field, ",")
		if len(parts) != 2 {
			return nil, errText(fmt.Sprintf("pair %d: expected fromId,toId", i+1))
		}
		from, to := strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
		if from == "" || to == "" {
			return nil, errText(fmt.Sprintf("pair %d: both ids are required", i+1))
		}
		requests = append(requests, api.CreateLinkRequest{FromTxnID: from, ToTxnID: to})
	}
	if len(requests) == 0 {
		return nil, errText("add at least one fromId,toId pair")
	}
	return requests, nil
}

// confirmDelete deletes the selection, or the cursor link when nothing is
// selected.
func (l *Links) confirmDelete() {
	ids := l.selectedIDs()
	if len(ids) == 0 {
		link, ok := l.currentLink()
		if !ok {
			return
		}
		ids = []string{link.ID}
	}
	l.ctx.Open(NewConfirm(
		"Delete links",
		fmt.Sprintf("Delete %s?", pluralise(len(ids), "link", "links")),
		true,
		func() tea.Cmd {
			return linkActCount("links.delete", func(ctx context.Context) (int, error) {
				if len(ids) == 1 {
					if err := l.ctx.Client.DeleteLink(ctx, ids[0]); err != nil {
						return 0, err
					}
					return 1, nil
				}
				return l.ctx.Client.BulkDeleteLinks(ctx, ids)
			}, func(n int) string {
				return fmt.Sprintf("deleted %s", pluralise(n, "link", "links"))
			})
		},
	).WithDetail(
		"Transfer-derived categories and payees are cleared from the two transactions, but only while no other link still references them.",
		"Nothing is written on a bulk delete: an empty list is not an error, it just deletes nothing.",
	))
}

// showDetail opens the link overlay, which is also where the transaction ids
// needed by the create form are readable.
func (l *Links) showDetail() {
	link, ok := l.currentLink()
	if !ok {
		return
	}
	l.ctx.Open(NewInfo("Link", linkDetailBody(link, l.ctx.Ref)))
}

// confirmSuggestions creates real links from the selected suggestions, or from
// the cursor one when nothing is selected. This is the screen's one deliberate
// confirmation: nothing on the Suggestions pane is linked until the user asks.
func (l *Links) confirmSuggestions() tea.Cmd {
	pending := l.selectedSuggestions()
	if len(pending) == 0 {
		suggestion, ok := l.currentSuggestion()
		if !ok {
			return nil
		}
		pending = []api.CreateLinkRequest{linkSuggestionRequest(suggestion, l.suggKind)}
	}

	if len(pending) == 1 {
		req := pending[0]
		return linkActCount("links.confirm", func(ctx context.Context) (int, error) {
			if _, err := l.ctx.Client.CreateLink(ctx, req); err != nil {
				return 0, err
			}
			return 1, nil
		}, func(n int) string {
			return fmt.Sprintf("linked %s", pluralise(n, "suggestion", "suggestions"))
		})
	}

	requests := pending
	return linkActCount("links.confirm", func(ctx context.Context) (int, error) {
		return l.ctx.Client.BulkCreateLinks(ctx, requests)
	}, func(n int) string {
		return fmt.Sprintf("linked %s (already-linked pairs skipped)", pluralise(n, "suggestion", "suggestions"))
	})
}

// openWindowForm edits the circular-money window. An empty account is the API's
// unfiltered default and an empty date bound is unbounded.
func (l *Links) openWindowForm() {
	window := l.window
	accounts := append([]Option{{Value: "", Label: "all accounts"}}, l.ctx.Ref.AccountOptions()...)
	fields := []Field{
		{Label: "Date from", Kind: FieldText, Value: window.dateFrom, Width: 14, Validate: optionalDate},
		{Label: "Date to", Kind: FieldText, Value: window.dateTo, Width: 14, Validate: optionalDate},
		{Label: "Account", Kind: FieldSelect, Value: window.accountID, Options: accounts, ClearLabel: "all accounts"},
	}
	l.ctx.Open(NewForm("links.window", "Circular money window", fields, func(f *Form) tea.Cmd {
		l.window = linkWindow{
			dateFrom:  strings.TrimSpace(f.Value("Date from")),
			dateTo:    strings.TrimSpace(f.Value("Date to")),
			accountID: f.Value("Account"),
		}
		// The window is local state, so the form closes itself and the fetch
		// runs as an ordinary load.
		f.Close()
		l.cycleOffset = 0
		return l.reloadCycles()
	}))
}

// linkActCount runs a mutation whose result is a count. The shared act helper
// fixes its note before the call runs, and a bulk create or delete only knows
// how many rows it touched afterwards — the API skips duplicates and empty
// lists, so the count is the honest report.
func linkActCount(tag string, fn func(context.Context) (int, error), note func(int) string) tea.Cmd {
	return func() tea.Msg {
		n, err := fn(context.Background())
		if err != nil {
			return done{tag: tag, err: err}
		}
		return done{tag: tag, note: note(n)}
	}
}

// View implements Screen.
func (l *Links) View(width, height int) string {
	th := l.ctx.Theme
	bodyHeight := max(1, height-4)

	var body string
	switch l.pane {
	case linkPaneSuggestions:
		if l.suggLoaded {
			body = l.table.View(th, width, bodyHeight, "no suggestions — nothing is linked until you confirm one")
		} else {
			body = th.Subtle.Render("asking the server for suggestions…")
		}
	case linkPaneCycles:
		body = l.cyclesView(bodyHeight)
	default:
		if l.linksLoaded {
			body = l.table.View(th, width, bodyHeight, "no links match this filter")
		} else {
			body = th.Subtle.Render("loading links…")
		}
	}

	parts := []string{l.headerLine(width), body, ""}
	if detail := l.detailLine(width); detail != "" {
		parts = append(parts, detail)
	}
	parts = append(parts, l.hintLine())
	return trimToBox(strings.Join(parts, "\n"), width, height)
}

// headerLine names the pane and summarises what it is showing.
func (l *Links) headerLine(width int) string {
	th := l.ctx.Theme
	title := th.Title.Render(l.pane.label())

	var bits []string
	switch l.pane {
	case linkPaneSuggestions:
		limit := l.sugg.Limit
		if limit <= 0 {
			limit = linkSuggestionLimit
		}
		bits = append(bits, l.suggKind+" suggestions",
			fmt.Sprintf("page %d", max(1, l.sugg.Page)),
			fmt.Sprintf("%d per page", limit))
		if l.sugg.HasMore {
			bits = append(bits, "more pages — the server reports no total")
		}
		if n := len(l.suggSel); n > 0 {
			bits = append(bits, fmt.Sprintf("%d selected", n))
		}
	case linkPaneCycles:
		bits = append(bits,
			"circular "+l.cycles.TotalCircular.Display(),
			pluralise(len(l.cycles.Cycles), "cycle", "cycles"),
			pluralise(len(l.cycles.OneSidedFlows), "one-sided flow", "one-sided flows"),
			l.window.describe(l.ctx.Ref))
	default:
		bits = append(bits, pluralise(len(l.links), "link", "links"), "type "+defaultTo(l.typeFilter, "all"))
		if n := len(l.selected); n > 0 {
			bits = append(bits, fmt.Sprintf("%d selected", n))
		}
	}

	line := title
	if len(bits) > 0 {
		line += "  " + th.Subtle.Render(truncate(strings.Join(bits, " · "), max(10, width-len(l.pane.label())-4)))
	}
	return line
}

// detailLine describes the cursor row.
func (l *Links) detailLine(width int) string {
	switch l.pane {
	case linkPaneLinks:
		link, ok := l.currentLink()
		if !ok {
			return ""
		}
		parts := []string{"id " + truncateID(link.ID)}
		parts = append(parts, "from "+linkSideRef(link.FromTxn, link.FromTxnID),
			"to "+linkSideRef(link.ToTxn, link.ToTxnID))
		if link.Notes != "" {
			parts = append(parts, "notes "+link.Notes)
		}
		return l.ctx.Theme.Subtle.Render(truncate(strings.Join(parts, " · "), width))

	case linkPaneSuggestions:
		suggestion, ok := l.currentSuggestion()
		if !ok {
			return ""
		}
		parts := []string{
			fmt.Sprintf("score %.2f", suggestion.Score),
			"debit " + linkSideRef(&suggestion.DebitTxn, suggestion.DebitTxn.ID),
			"credit " + linkSideRef(&suggestion.CreditTxn, suggestion.CreditTxn.ID),
		}
		return l.ctx.Theme.Subtle.Render(truncate(strings.Join(parts, " · "), width))
	}
	return ""
}

// linkSideRef names one end of a link or suggestion for a detail line. The id is
// always shown because it is what the create form takes.
func linkSideRef(t *api.Transaction, id string) string {
	if t == nil {
		return truncateID(id)
	}
	date := formatDate(t.Date)
	if t.Description == "" {
		return truncateID(t.ID) + " " + date
	}
	return truncateID(t.ID) + " " + date + " " + t.Description
}

// hintLine is the view's last line: the keys the pane does not advertise in the
// status bar, and on Suggestions the rule that matters — the server's guesses
// are not links until the user confirms one.
func (l *Links) hintLine() string {
	th := l.ctx.Theme
	switch l.pane {
	case linkPaneSuggestions:
		return th.Subtle.Render("space select · y confirm — nothing is linked until you confirm · T transfer · C cashback · p pane")
	case linkPaneCycles:
		return th.Subtle.Render("f window · ↑/↓ scroll · p pane")
	default:
		return th.Subtle.Render("space select · a all · n new · b bulk ids · d delete · t type · enter detail · p pane")
	}
}

// cyclesView renders the circular-money report as a scrolling text block: the
// report is deeply nested (cycles, their legs, per-type totals), which no column
// layout shows honestly.
func (l *Links) cyclesView(height int) string {
	th := l.ctx.Theme
	if height < 1 {
		return ""
	}
	if !l.cyclesLoaded {
		return th.Subtle.Render("loading circular money…")
	}

	lines := l.cycleLines()
	visible := max(1, height-1)
	l.cycleOffset = min(l.cycleOffset, max(0, len(lines)-visible))
	end := min(len(lines), l.cycleOffset+visible)
	out := append([]string{}, lines[l.cycleOffset:end]...)
	if len(lines) > visible {
		out = append(out, th.Subtle.Render(fmt.Sprintf("↑/↓ scroll (%d/%d)", l.cycleOffset+1, len(lines))))
	}
	return strings.Join(out, "\n")
}

// cycleLines builds the report. Net is the cycle's smallest leg — the amount
// that actually circulates the whole loop — so it is shown next to gross, which
// is what simply moves.
func (l *Links) cycleLines() []string {
	th := l.ctx.Theme
	report := l.cycles

	lines := []string{
		th.Header.Render("total circular  "+report.TotalCircular.Display()) +
			"  " + th.Subtle.Render("the amount that flows back to where it started"),
	}
	if len(report.Cycles) == 0 {
		lines = append(lines, th.Subtle.Render("  no circular flows in this window"))
	}
	for i, cycle := range report.Cycles {
		lines = append(lines,
			th.PanelTitle.Render(fmt.Sprintf("%d. %s", i+1, defaultTo(cycle.Kind, "cycle")))+"  "+
				th.Subtle.Render(fmt.Sprintf("%s · %s",
					pluralise(len(cycle.Accounts), "account", "accounts"),
					pluralise(cycle.Transactions, "transaction", "transactions"))),
			"   "+th.Subtle.Render("route  ")+linkCycleRoute(cycle),
		)
		for _, leg := range cycle.Legs {
			lines = append(lines, "   "+th.Subtle.Render("leg    ")+linkLegLine(leg))
		}
		lines = append(lines, "   "+th.Subtle.Render("net    ")+th.Money.Render(cycle.Net.Display())+
			th.Subtle.Render("   gross "+cycle.Gross.Display()+" · net is the smallest leg, i.e. what actually circulates"))
	}

	lines = append(lines, "")
	lines = append(lines, th.WarnText.Render("one-sided flows")+"  "+
		th.Subtle.Render(pluralise(len(report.OneSidedFlows), "flow", "flows")))
	lines = append(lines, "   "+th.Subtle.Render("no flow comes back — a one-way bill payment or refund looks exactly like a half-entered transfer"))
	if len(report.OneSidedFlows) == 0 {
		lines = append(lines, th.Subtle.Render("  none in this window"))
	}
	for _, flow := range report.OneSidedFlows {
		route := defaultTo(flow.FromAccountName, flow.FromAccountID) + " → " + defaultTo(flow.ToAccountName, flow.ToAccountID)
		text := fmt.Sprintf("%s  %s ×%d", route, flow.Total.Display(), flow.Count)
		if types := linkFlowTypeTotals(flow.Types); types != "" {
			text += "  (" + types + ")"
		}
		lines = append(lines, "   "+text)
	}
	return lines
}

// linkCycleRoute draws a cycle's participants in flow order; a longer loop is
// closed back on itself, a reciprocal pair flows both ways.
func linkCycleRoute(cycle api.LinkCycle) string {
	names := make([]string, 0, len(cycle.Accounts))
	for _, account := range cycle.Accounts {
		names = append(names, defaultTo(account.Name, account.ID))
	}
	if len(names) == 0 {
		return "—"
	}
	if cycle.Kind == "reciprocal" {
		return strings.Join(names, " ⇄ ")
	}
	route := strings.Join(names, " → ")
	if len(names) > 1 {
		route += " → " + names[0]
	}
	return route
}

// linkLegLine renders one leg of a cycle: who paid whom, how much moved, and the
// link types behind it.
func linkLegLine(leg api.LinkCycleLeg) string {
	route := defaultTo(leg.FromAccountName, leg.FromAccountID) + " → " + defaultTo(leg.ToAccountName, leg.ToAccountID)
	text := fmt.Sprintf("%s  %s ×%d", route, leg.Amount.Display(), leg.Count)
	if types := linkFlowTypeTotals(leg.Types); types != "" {
		text += "  (" + types + ")"
	}
	return text
}

// linkFlowTypeTotals renders a per-link-type rollup, e.g. "transfer 500.00 ×2".
func linkFlowTypeTotals(totals []api.LinkFlowTypeTotal) string {
	parts := make([]string, 0, len(totals))
	for _, total := range totals {
		parts = append(parts, fmt.Sprintf("%s %s ×%d", defaultTo(total.Type, "link"), total.Total.Display(), total.Count))
	}
	return strings.Join(parts, ", ")
}

// linkDetailBody renders the link overlay. Both transactions are spelled out
// with their ids, because a link is diagnosed by its two ids.
func linkDetailBody(link api.Link, ref *RefData) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Type         %s\n", link.Type)
	fmt.Fprintf(&b, "Id           %s\n", link.ID)
	fmt.Fprintf(&b, "Created      %s\n", formatDate(link.CreatedAt))
	if link.Notes != "" {
		fmt.Fprintf(&b, "Notes        %s\n", link.Notes)
	}

	b.WriteString("\nFrom\n")
	b.WriteString(linkEndDetail(link.FromTxn, link.FromTxnID, ref))
	b.WriteString("\nTo\n")
	b.WriteString(linkEndDetail(link.ToTxn, link.ToTxnID, ref))

	b.WriteString("\nDeleting a transfer also clears the category and payee it derived, while no other link references those transactions.\n")
	return b.String()
}

// linkEndDetail describes one end of a link. The API only joins the two
// transactions on reads, so a link created in this session — or one whose
// transaction was deleted — renders from its id alone.
func linkEndDetail(t *api.Transaction, id string, ref *RefData) string {
	if t == nil {
		return fmt.Sprintf("  id           %s\n  (no transaction joined — it may have been deleted)\n", defaultTo(id, "—"))
	}
	var b strings.Builder
	fmt.Fprintf(&b, "  id           %s\n", t.ID)
	fmt.Fprintf(&b, "  date         %s\n", formatDate(t.Date))
	fmt.Fprintf(&b, "  description  %s\n", defaultTo(t.Description, "—"))
	fmt.Fprintf(&b, "  account      %s\n", defaultTo(t.AccountName, ref.AccountName(t.AccountID)))
	fmt.Fprintf(&b, "  amount       %s (%s)\n", t.Amount.Display(), t.Type)
	return b.String()
}
