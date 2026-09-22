package ui

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/fintrak/client/api"
)

func init() {
	registerScreen(50, func(ctx *Ctx) Screen { return NewPayees(ctx) })
}

// Payees is the payee directory: every merchant and counterparty the user has
// recorded, plus the account each one is linked to. The link matters because the
// API resolves an internal transfer through the payee's account — a linked
// payee names the other side of a transfer rather than a merchant — so the
// linked-account column is the difference between the two kinds of entry.
type Payees struct {
	ctx   *Ctx
	table Table

	payees []api.Payee

	// filtering is true while the inline name filter has the keyboard. The
	// filter is client-side only: the full list is already in memory, so typing
	// never costs a round trip and never touches shared reference data.
	filtering bool
	filter    textinput.Model

	keys payeeKeys
}

// payeeKeys are the screen's bindings.
type payeeKeys struct {
	Filter key.Binding
	New    key.Binding
	Edit   key.Binding
	Delete key.Binding
	Detail key.Binding
}

func newPayeeKeys() payeeKeys {
	return payeeKeys{
		Filter: key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "filter")),
		New:    key.NewBinding(key.WithKeys("n"), key.WithHelp("n", "new")),
		Edit:   key.NewBinding(key.WithKeys("e"), key.WithHelp("e", "edit")),
		Delete: key.NewBinding(key.WithKeys("d"), key.WithHelp("d", "delete")),
		Detail: key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "detail")),
	}
}

// NewPayees builds the payee directory screen.
func NewPayees(ctx *Ctx) *Payees {
	p := &Payees{ctx: ctx, keys: newPayeeKeys()}
	p.table.SetColumns(
		Column{Title: "Name"},
		Column{Title: "Linked account", Width: 22},
		Column{Title: "Created", Width: 12},
	)
	return p
}

// Title implements Screen.
func (p *Payees) Title() string { return "Payees" }

// Keys implements Screen.
func (p *Payees) Keys() []key.Binding {
	return []key.Binding{p.keys.Filter, p.keys.New, p.keys.Edit, p.keys.Detail, p.keys.Delete}
}

// Refresh implements Screen.
func (p *Payees) Refresh() tea.Cmd { return p.reload() }

// reload fetches every payee, alphabetically by name.
func (p *Payees) reload() tea.Cmd {
	return load("payees.list", func(ctx context.Context) ([]api.Payee, error) {
		return p.ctx.Client.ListPayees(ctx)
	})
}

// Update implements Screen.
func (p *Payees) Update(msg tea.Msg) tea.Cmd {
	switch m := msg.(type) {
	case loaded[[]api.Payee]:
		if m.tag != "payees.list" {
			break
		}
		if m.err != nil {
			p.ctx.Notify(LevelError, "%s", m.err)
			return nil
		}
		p.payees = m.data
		p.applyRows()
		return nil

	case done:
		if !strings.HasPrefix(m.tag, "payees.") {
			break
		}
		// A rejected save or delete is reported by the App (inside the form, or
		// on the status line), so only a success needs a refetch. Mutations run
		// with invalidate=true, which already refreshes the shared payee
		// options other screens hold.
		if m.err == nil {
			return p.reload()
		}

	case tea.KeyMsg:
		return p.handleKey(m)
	}
	return nil
}

// handleKey routes a key press.
func (p *Payees) handleKey(msg tea.KeyMsg) tea.Cmd {
	if p.filtering {
		switch msg.String() {
		case "enter":
			// Keep the query so the header goes on reporting the filter.
			p.filtering = false
			p.filter.Blur()
			return nil
		case "esc":
			// Esc abandons the filter rather than leaving a half-typed name
			// hiding rows the user cannot see or act on.
			p.filtering = false
			p.filter.SetValue("")
			p.filter.Blur()
			p.applyRows()
			return nil
		}
		var cmd tea.Cmd
		p.filter, cmd = p.filter.Update(msg)
		p.applyRows()
		return cmd
	}

	switch {
	case keyMatches(p.keys.Filter, msg):
		return p.startFilter()
	case keyMatches(p.keys.New, msg):
		p.openForm(nil)
		return nil
	case keyMatches(p.keys.Edit, msg):
		if row, ok := p.current(); ok {
			p.openForm(&row)
		}
		return nil
	case keyMatches(p.keys.Detail, msg):
		if row, ok := p.current(); ok {
			p.ctx.Open(NewInfo("Payee", p.detail(row)))
		}
		return nil
	case keyMatches(p.keys.Delete, msg):
		p.confirmDelete()
		return nil
	}

	switch msg.String() {
	case "up", "k":
		p.table.Move(-1)
	case "down", "j":
		p.table.Move(1)
	case "pgup", "ctrl+b":
		p.table.Page(-1, 20)
	case "pgdown", "ctrl+f":
		p.table.Page(1, 20)
	case "home":
		p.table.Home()
	case "end", "G":
		p.table.End()
	}
	return nil
}

// startFilter puts the cursor in the inline name filter, keeping whatever was
// typed before. The list is filtered as the user types.
func (p *Payees) startFilter() tea.Cmd {
	p.filtering = true
	previous := p.query()
	p.filter = textinput.New()
	p.filter.Placeholder = "filter by name"
	p.filter.Width = 32
	p.filter.SetValue(previous)
	p.filter.Focus()
	return textinput.Blink
}

// query returns the current name filter, normalised.
func (p *Payees) query() string {
	return strings.TrimSpace(p.filter.Value())
}

// visible returns the payees matching the inline filter, preserving the API's
// alphabetical order. With no query it returns the fetched slice unchanged.
func (p *Payees) visible() []api.Payee {
	query := strings.ToLower(p.query())
	if query == "" {
		return p.payees
	}
	matches := make([]api.Payee, 0, len(p.payees))
	for _, payee := range p.payees {
		if strings.Contains(strings.ToLower(payee.Name), query) {
			matches = append(matches, payee)
		}
	}
	return matches
}

// current returns the payee under the cursor. The cursor indexes the filtered
// list, so every action below operates on a row the user can actually see.
func (p *Payees) current() (api.Payee, bool) {
	rows := p.visible()
	index := p.table.Cursor()
	if index < 0 || index >= len(rows) {
		return api.Payee{}, false
	}
	return rows[index], true
}

// applyRows rebuilds the table from the visible payees.
func (p *Payees) applyRows() {
	rows := p.visible()
	cells := make([][]Cell, 0, len(rows))
	for _, payee := range rows {
		link := Muted("—")
		if payee.AccountID != nil {
			link = Text(p.ctx.Ref.AccountName(*payee.AccountID))
		}
		cells = append(cells, []Cell{
			Text(payee.Name),
			link,
			Muted(formatDate(payee.CreatedAt)),
		})
	}
	p.table.SetRows(cells)
}

// openForm opens the create or edit form. A nil row creates.
//
// The API binds the same request body to both calls, so both paths build one
// api.CreatePayeeRequest: on the update a nil AccountID is an explicit unlink
// rather than "leave the link alone", which is why the account select must map
// an empty choice to nil.
func (p *Payees) openForm(row *api.Payee) {
	creating := row == nil
	title := "New payee"
	var current api.Payee
	if row != nil {
		title = "Edit payee"
		current = *row
	}

	fields := []Field{
		{Label: "Name", Kind: FieldText, Value: current.Name, Width: 32, Validate: required("name"), Help: "must be unique — a duplicate name is refused by the API"},
		{Label: "Linked account", Kind: FieldSelect, Value: derefID(current.AccountID), Options: p.ctx.Ref.AccountOptions(), AllowClear: true, ClearLabel: "none — standalone", Help: "an account-linked payee is what a transfer resolves to"},
	}

	p.ctx.Open(NewForm("payees.save", title, fields, func(f *Form) tea.Cmd {
		request := api.CreatePayeeRequest{Name: strings.TrimSpace(f.Value("Name"))}
		if accountID := f.Value("Linked account"); accountID != "" {
			request.AccountID = &accountID
		}
		if creating {
			return act("payees.save", "payee created", true, func(ctx context.Context) error {
				_, err := p.ctx.Client.CreatePayee(ctx, request)
				return err
			})
		}
		id := current.ID
		return act("payees.save", "payee updated", true, func(ctx context.Context) error {
			_, err := p.ctx.Client.UpdatePayee(ctx, id, request)
			return err
		})
	}))
}

// confirmDelete asks before removing the cursor payee. Deletion clears the link
// from every transaction that used the payee — the payee_id foreign key is
// ON DELETE SET NULL — so the history itself survives; the wording says exactly
// that rather than implying the transactions go too.
func (p *Payees) confirmDelete() {
	row, ok := p.current()
	if !ok {
		return
	}
	id := row.ID
	confirm := NewConfirm("Delete payee", fmt.Sprintf("Delete \"%s\"?", row.Name), true, func() tea.Cmd {
		return act("payees.delete", "payee deleted", true, func(ctx context.Context) error {
			return p.ctx.Client.DeletePayee(ctx, id)
		})
	}).WithDetail("Transactions that used this payee keep their history; only the payee link is cleared.")
	if row.AccountID != nil {
		confirm.WithDetail(fmt.Sprintf("It is linked to %s, so transfers that resolved to that account lose their payee.", p.ctx.Ref.AccountName(*row.AccountID)))
	}
	p.ctx.Open(confirm)
}

// detail renders the read-only overlay for one payee.
func (p *Payees) detail(payee api.Payee) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Name         %s\n", payee.Name)
	if payee.AccountID != nil {
		fmt.Fprintf(&b, "Account      %s\n", p.ctx.Ref.AccountName(*payee.AccountID))
	} else {
		b.WriteString("Account      not linked\n")
	}
	fmt.Fprintf(&b, "Created      %s\n", formatDate(payee.CreatedAt))
	fmt.Fprintf(&b, "Updated      %s\n", formatDate(payee.UpdatedAt))
	fmt.Fprintf(&b, "Id           %s\n", payee.ID)
	if payee.AccountID != nil {
		b.WriteString("\nLinked to an account, so a transfer recorded against this payee resolves to that account's counterpart entry.\n")
	} else {
		b.WriteString("\nNot linked: this payee names a merchant or counterparty but no account of yours.\n")
	}
	return b.String()
}

// View implements Screen.
func (p *Payees) View(width, height int) string {
	header := p.headerLine(width)
	if p.filtering {
		header = p.ctx.Theme.Title.Render("/") + p.filter.View()
	}
	body := p.table.View(p.ctx.Theme, width, listRows(height), p.emptyMessage())
	parts := []string{header, body, ""}
	if detail := p.detailLine(width); detail != "" {
		parts = append(parts, detail)
	}
	parts = append(parts, "enter detail · / filter · n new · e edit · d delete")
	return trimToBox(strings.Join(parts, "\n"), width, height)
}

// headerLine reports how many payees are listed, how many are linked to an
// account, and any active filter.
func (p *Payees) headerLine(width int) string {
	th := p.ctx.Theme
	summary := pluralise(len(p.payees), "payee", "payees")
	if query := p.query(); query != "" {
		summary = fmt.Sprintf("%s · %d matching %q", summary, len(p.visible()), query)
	}
	line := th.Title.Render(summary)

	linked := 0
	for _, payee := range p.payees {
		if payee.AccountID != nil {
			linked++
		}
	}
	if linked > 0 {
		note := fmt.Sprintf("%d linked to an account", linked)
		line += "  " + th.Subtle.Render(truncate(note, max(10, width-len(summary)-4)))
	}
	return line
}

// detailLine describes the cursor row below the table.
func (p *Payees) detailLine(width int) string {
	row, ok := p.current()
	if !ok {
		return ""
	}
	parts := []string{"id " + truncate(row.ID, 8)}
	if row.AccountID != nil {
		parts = append(parts, "linked to "+p.ctx.Ref.AccountName(*row.AccountID))
	} else {
		parts = append(parts, "not linked")
	}
	parts = append(parts, "created "+formatDate(row.CreatedAt))
	return p.ctx.Theme.Subtle.Render(truncate(strings.Join(parts, " · "), width))
}

// emptyMessage distinguishes an empty directory from a filter that matches
// nothing, because the two need different next steps.
func (p *Payees) emptyMessage() string {
	if len(p.payees) == 0 {
		return "no payees yet — press n to add the first one"
	}
	return "no payee matches this filter"
}
