package ui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// Option is one choice in a picker or a select field. Group, when set, becomes a
// non-selectable heading above the option, so a long list (a category chooser,
// say) reads as grouped sections instead of one flat run of names.
type Option struct {
	Value string
	Label string
	Group string
}

// Picker is a filterable single-select overlay: type to filter, arrows to move,
// enter to choose, esc to cancel. It mutates in place and reports closure so the
// owner can read the result or discard it. With OnSelect set it satisfies the
// modal interface directly.
type Picker struct {
	Title      string
	options    []Option
	matched    []int
	query      string
	cursor     int
	offset     int
	allowClear bool
	clearLabel string
	value      string
	closed     bool
	canceled   bool

	// OnSelect runs when an entry is chosen, so a picker used as a modal can act
	// on its own result.
	OnSelect func(value string) tea.Cmd
}

// NewPicker builds a picker for options, pre-selecting current (matched against
// option values, not labels). allowClear adds a leading entry that selects the
// empty value, which is how a nullable field is cleared.
func NewPicker(title string, options []Option, current string, allowClear bool, clearLabel string) *Picker {
	if clearLabel == "" {
		clearLabel = "(none)"
	}
	p := &Picker{Title: title, options: options, allowClear: allowClear, clearLabel: clearLabel, value: current}
	p.filter()
	for i, row := range p.rows() {
		if row.option >= 0 && p.options[row.option].Value == current {
			p.cursor = i
			break
		}
	}
	return p
}

// Closed reports whether the picker has been dismissed.
func (p *Picker) Closed() bool { return p.closed }

// Canceled reports whether the dismissal discarded the choice.
func (p *Picker) Canceled() bool { return p.canceled }

// Value returns the chosen value.
func (p *Picker) Value() string { return p.value }

// Label returns the chosen option's label, for display inside a form field.
func (p *Picker) Label() string {
	if p.value == "" {
		return ""
	}
	for _, o := range p.options {
		if o.Value == p.value {
			return o.Label
		}
	}
	return p.value
}

// Update implements the modal interface.
func (p *Picker) Update(msg tea.Msg) tea.Cmd {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil
	}
	if !p.handle(key) {
		return nil
	}
	if p.closed && !p.canceled && p.OnSelect != nil {
		return p.OnSelect(p.value)
	}
	return nil
}

// handle processes one key press, reporting whether it changed state.
func (p *Picker) handle(msg tea.KeyMsg) bool {
	if p.closed {
		return false
	}
	switch msg.String() {
	case "up", "ctrl+p":
		p.move(-1)
	case "down", "ctrl+n":
		p.move(1)
	case "pgup":
		for range 10 {
			p.move(-1)
		}
	case "pgdown":
		for range 10 {
			p.move(1)
		}
	case "enter":
		p.commit()
	case "esc", "ctrl+c":
		p.closed, p.canceled = true, true
	case "backspace":
		if p.query != "" {
			p.query = strings.TrimSuffix(p.query, string([]rune(p.query)[len([]rune(p.query))-1]))
			p.filter()
		}
	default:
		if len(msg.Runes) == 0 {
			return false
		}
		p.query += string(msg.Runes)
		p.filter()
	}
	return true
}

// commit selects the highlighted entry. The leading clear row (present when the
// field is nullable) selects the empty value: without that, pressing enter on it
// would do nothing and the picker would swallow the key, which reads as a hang.
func (p *Picker) commit() {
	rows := p.rows()
	if len(rows) == 0 {
		if p.allowClear {
			p.value, p.closed = "", true
		}
		return
	}
	switch row := rows[p.cursor]; {
	case row.option == -1:
		p.value, p.closed = "", true
	case row.option >= 0:
		p.value = p.options[row.option].Value
		p.closed = true
	}
}

// pickerRow is one rendered line: a selectable option, the clear entry, or a
// non-selectable group heading.
type pickerRow struct {
	label  string
	option int // index into options; -1 is the clear entry, -2 a heading
}

func (r pickerRow) selectable() bool { return r.option != -2 }

// header reports whether the row is a non-selectable group heading.
func (r pickerRow) header() bool { return r.option == -2 }

// rows renders the display list: the clear entry when the field is nullable,
// then the matches with a heading wherever the group changes.
func (p *Picker) rows() []pickerRow {
	rows := make([]pickerRow, 0, len(p.matched)+4)
	if p.allowClear {
		rows = append(rows, pickerRow{label: p.clearLabel, option: -1})
	}
	lastGroup := ""
	for _, idx := range p.matched {
		opt := p.options[idx]
		if opt.Group != "" && opt.Group != lastGroup {
			rows = append(rows, pickerRow{label: opt.Group, option: -2})
			lastGroup = opt.Group
		}
		rows = append(rows, pickerRow{label: opt.Label, option: idx})
	}
	return rows
}

// move shifts the highlight by delta selectable rows, stepping over headings and
// stopping at the ends rather than wrapping.
func (p *Picker) move(delta int) {
	rows := p.rows()
	if len(rows) == 0 {
		p.cursor = 0
		return
	}
	for i := p.cursor + sign(delta); i >= 0 && i < len(rows); i += sign(delta) {
		if rows[i].selectable() {
			p.cursor = i
			return
		}
	}
}

// sign is -1, 0 or 1 for the direction of a movement.
func sign(n int) int {
	switch {
	case n > 0:
		return 1
	case n < 0:
		return -1
	default:
		return 0
	}
}

// firstSelectable is the row the highlight starts on when the list changes.
func (p *Picker) firstSelectable() int {
	for i, row := range p.rows() {
		if row.selectable() {
			return i
		}
	}
	return 0
}

// filter recomputes the matches for the current query.
func (p *Picker) filter() {
	query := strings.ToLower(strings.TrimSpace(p.query))
	p.matched = p.matched[:0]
	for i, o := range p.options {
		if query == "" || strings.Contains(strings.ToLower(o.Label), query) {
			p.matched = append(p.matched, i)
		}
	}
	rows := p.rows()
	if p.cursor > len(rows)-1 || !rows[p.cursor].selectable() {
		p.cursor = p.firstSelectable()
	}
}

// View renders the picker's modal body.
func (p *Picker) View(th Theme, width, height int) string {
	// The list is the point of a picker, so the fixed rows are fitted around it
	// rather than subtracted from it: a fixed chrome left a long list showing one
	// row on a short terminal.
	budget := newChromeBudget(height, minListRows)
	showFilter := budget.want()
	showTitle := budget.want()
	showHint := budget.want()
	blankAboveList := budget.want()
	blankBelowList := budget.want()
	visible := budget.content()

	rows := p.rows()
	p.offset = clampOffset(p.offset, p.cursor, visible, len(rows))

	var b strings.Builder
	if showTitle {
		b.WriteString(th.ModalTitle.Render(p.Title))
		b.WriteString("\n")
	}
	if showFilter {
		if p.query == "" {
			b.WriteString(th.Subtle.Render("filter: (type to filter)"))
		} else {
			b.WriteString(th.Subtle.Render("filter: /") + p.query)
		}
		b.WriteString("\n")
	}
	if blankAboveList {
		b.WriteString("\n")
	}

	if len(rows) == 0 {
		b.WriteString(th.Subtle.Render("  no matches"))
		b.WriteString("\n")
	}
	for i := p.offset; i < len(rows) && i-p.offset < visible; i++ {
		switch row := rows[i]; {
		case !row.selectable():
			// A group heading: indented, dimmed, and never highlighted.
			b.WriteString(th.Header.Render(ansi.Truncate(row.label, max(4, width-4), "…")))
		case i == p.cursor:
			b.WriteString(th.RowSelected.Render("  " + ansi.Truncate(row.label, max(4, width-6), "…")))
		default:
			b.WriteString("  " + ansi.Truncate(row.label, max(4, width-6), "…"))
		}
		b.WriteString("\n")
	}

	if blankBelowList {
		b.WriteString("\n")
	}
	if showHint {
		b.WriteString(th.Subtle.Render("enter select · esc cancel · ↑/↓ move"))
	}
	return strings.TrimRight(b.String(), "\n")
}
