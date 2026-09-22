package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// FieldKind is how a form field is edited.
type FieldKind int

// Field kinds.
const (
	FieldText FieldKind = iota
	FieldPassword
	FieldSelect
	FieldBool
)

// Field is one row of a form.
type Field struct {
	Label string
	Kind  FieldKind
	Value string
	// Placeholder is shown while a text field is empty.
	Placeholder string
	// Options are the choices for a select field.
	Options []Option
	// AllowClear lets a select field be set to the empty value, which is how a
	// nullable column is cleared on the API's PATCH bodies.
	AllowClear bool
	ClearLabel string
	// Width is the text-input width in cells (0 = 32).
	Width int
	// Validate rejects a value before the form is submitted. The API validates
	// again; this exists so obvious mistakes surface without a round trip.
	Validate func(value string) error
	// Help is shown under the focused field.
	Help string
}

// Form is a modal editor for a list of fields. It owns its validation and stays
// open until the mutation it triggers reports success, so a rejected save does
// not lose what the user typed.
type Form struct {
	// Tag identifies the mutation this form triggers, so the App can match the
	// resulting done message back to it.
	Tag    string
	Title  string
	Submit string

	fields []Field
	inputs []textinput.Model
	index  int
	err    string
	picker *Picker
	// offset is the first field line shown, so scrolling sticks between redraws.
	offset   int
	done     bool
	canceled bool
	submit   func(*Form) tea.Cmd
	// submitting is set while the mutation the submit closure started is in
	// flight, and cleared once it reports back. The form stays open and
	// editable until then, so without it a second ctrl+s/enter runs the same
	// write twice (two identical transactions, two CSV writers on one path).
	submitting bool
}

// NewForm builds a form. submit is called once validation passes and must return
// a command built with act using this form's Tag.
func NewForm(tag, title string, fields []Field, submit func(*Form) tea.Cmd) *Form {
	f := &Form{Tag: tag, Title: title, Submit: "save", fields: fields, submit: submit}
	f.inputs = make([]textinput.Model, len(fields))
	for i, field := range fields {
		width := field.Width
		if width <= 0 {
			width = 32
		}
		ti := textinput.New()
		ti.Placeholder = field.Placeholder
		ti.Width = width
		ti.CharLimit = 256
		if field.Kind == FieldPassword {
			ti.EchoMode = textinput.EchoPassword
		}
		ti.SetValue(field.Value)
		if i == 0 && field.Kind != FieldSelect && field.Kind != FieldBool {
			ti.Focus()
		}
		f.inputs[i] = ti
	}
	return f
}

// Closed reports whether the form has been dismissed.
func (f *Form) Closed() bool { return f.done }

// Close dismisses the form without a server round trip. A submit closure that
// performs a local action (applying filters, say) must call it, since the App
// otherwise closes the form only when a mutation reports back.
func (f *Form) Close() { f.done = true }

// Canceled reports whether the dismissal discarded the edit.
func (f *Form) Canceled() bool { return f.canceled }

// Value returns a field's current value by label.
func (f *Form) Value(label string) string {
	for i, field := range f.fields {
		if field.Label != label {
			continue
		}
		switch field.Kind {
		case FieldText, FieldPassword:
			return f.inputs[i].Value()
		default:
			return f.fields[i].Value
		}
	}
	return ""
}

// IntValue parses a field as an integer, returning zero when it is blank or not
// a number (validation is what reports that to the user).
func (f *Form) IntValue(label string) int {
	var n int
	_, _ = fmt.Sscanf(strings.TrimSpace(f.Value(label)), "%d", &n)
	return n
}

// BoolValue reports a toggle field's state.
func (f *Form) BoolValue(label string) bool {
	return strings.EqualFold(f.Value(label), "yes")
}

// SetError shows a server-side failure inside the form, and releases the
// in-flight guard: the mutation has reported back rejected, so the form is the
// user's to fix and submit again.
func (f *Form) SetError(err error) {
	if err == nil {
		return
	}
	f.err = err.Error()
	f.submitting = false
}

// Update handles one message.
func (f *Form) Update(msg tea.Msg) tea.Cmd {
	if f.done {
		return nil
	}
	if key, ok := msg.(tea.KeyMsg); ok && f.picker != nil {
		if f.picker.handle(key) && f.picker.Closed() {
			if !f.picker.Canceled() {
				f.fields[f.index].Value = f.picker.Value()
			}
			f.picker = nil
		}
		return nil
	}

	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return f.updateInputs(msg)
	}

	switch key.String() {
	case "esc":
		f.done, f.canceled = true, true
		return nil
	case "ctrl+c":
		f.done, f.canceled = true, true
		return nil
	case "tab", "down", "ctrl+n":
		f.move(1)
		return nil
	case "shift+tab", "up", "ctrl+p":
		f.move(-1)
		return nil
	case "ctrl+s":
		return f.commit()
	case "enter":
		field := f.fields[f.index]
		switch field.Kind {
		case FieldSelect:
			f.openPicker()
			return nil
		case FieldBool:
			f.toggle()
			return nil
		}
		if f.index == len(f.fields)-1 {
			return f.commit()
		}
		f.move(1)
		return nil
	case " ":
		if f.fields[f.index].Kind == FieldBool {
			f.toggle()
			return nil
		}
	}

	// Left/right switch a select's value in place, which is faster than opening
	// the picker for a two-option field.
	if f.fields[f.index].Kind == FieldSelect {
		switch key.String() {
		case "left", "right":
			f.cycle(key.String() == "right")
			return nil
		}
	}
	return f.updateInputs(msg)
}

// updateInputs forwards a message to the focused text field.
func (f *Form) updateInputs(msg tea.Msg) tea.Cmd {
	if f.index >= len(f.inputs) {
		return nil
	}
	field := f.fields[f.index]
	if field.Kind != FieldText && field.Kind != FieldPassword {
		return nil
	}
	var cmd tea.Cmd
	f.inputs[f.index], cmd = f.inputs[f.index].Update(msg)
	return cmd
}

// move shifts focus, skipping nothing: every field is editable.
func (f *Form) move(delta int) {
	if len(f.fields) == 0 {
		return
	}
	if f.index < len(f.inputs) {
		f.inputs[f.index].Blur()
	}
	f.index = (f.index + delta + len(f.fields)) % len(f.fields)
	if f.index < len(f.inputs) {
		kind := f.fields[f.index].Kind
		if kind == FieldText || kind == FieldPassword {
			f.inputs[f.index].Focus()
		}
	}
	f.err = ""
}

// toggle flips a boolean field.
func (f *Form) toggle() {
	field := &f.fields[f.index]
	if strings.EqualFold(field.Value, "yes") {
		field.Value = "no"
	} else {
		field.Value = "yes"
	}
}

// cycle steps a select field through its options.
func (f *Form) cycle(forward bool) {
	field := &f.fields[f.index]
	values := make([]string, 0, len(field.Options)+1)
	if field.AllowClear {
		values = append(values, "")
	}
	for _, o := range field.Options {
		values = append(values, o.Value)
	}
	if len(values) == 0 {
		return
	}
	current := 0
	for i, v := range values {
		if v == field.Value {
			current = i
			break
		}
	}
	if forward {
		current = (current + 1) % len(values)
	} else {
		current = (current - 1 + len(values)) % len(values)
	}
	field.Value = values[current]
}

// openPicker shows the chooser for a select field.
func (f *Form) openPicker() {
	field := f.fields[f.index]
	f.picker = NewPicker(field.Label, field.Options, field.Value, field.AllowClear, field.ClearLabel)
}

// commit validates and submits. A submit already in flight ignores the call: the
// form stays live until its mutation reports back, so a repeat of ctrl+s/enter
// would otherwise run the same write again.
func (f *Form) commit() tea.Cmd {
	if f.submitting {
		return nil
	}
	for i, field := range f.fields {
		if field.Validate == nil {
			continue
		}
		if err := field.Validate(f.Value(field.Label)); err != nil {
			f.err = field.Label + ": " + err.Error()
			f.moveTo(i)
			return nil
		}
	}
	f.err = ""
	if f.submit == nil {
		f.done = true
		return nil
	}
	f.submitting = true
	return f.submit(f)
}

// moveTo focuses a field by index.
func (f *Form) moveTo(i int) {
	if i == f.index || i < 0 || i >= len(f.fields) {
		return
	}
	if f.index < len(f.inputs) {
		f.inputs[f.index].Blur()
	}
	f.index = i
	if kind := f.fields[i].Kind; kind == FieldText || kind == FieldPassword {
		f.inputs[i].Focus()
	}
}

// formLabelWidth is the label column, padded by display width so styled labels
// line up (padding a styled string with %-28s counts escape bytes, which glued
// the values to their labels).
const formLabelWidth = 24

// formKeyHint is the form's key reminder.
const formKeyHint = "tab/↓ next · ctrl+s submit · esc cancel"

// formMinFields is how many field rows a form keeps in view before it will give
// up any chrome. Below this a form stops being usable, which is exactly what a
// fixed chrome produced: a nine-field form on a ten-row terminal showed one.
const formMinFields = 3

// formIndicatorRows is the room reserved for the window's "↑/↓ N more" rows,
// which are drawn inside the content area. Reserving only the fields left those
// rows eating into them.
const formIndicatorRows = 2

// View renders the form body (the App wraps it in a modal frame).
//
// The field rows are windowed to the height the modal actually has, so a long
// form on a short terminal scrolls rather than being clipped off-screen. The
// rows around the window are included only while they fit — most valuable first
// (title, key hint, blank separators) — because every chrome row is a field row.
func (f *Form) View(th Theme, width, height int) string {
	lines := make([]string, 0, len(f.fields)+2)
	focusLine := 0
	for i, field := range f.fields {
		if i == f.index {
			focusLine = len(lines)
		}
		lines = append(lines, f.fieldLine(th, i, width, field))
		if i == f.index && field.Help != "" {
			lines = append(lines, th.Subtle.Render("    "+field.Help))
		}
	}

	// Reserve the fixed rows in order of what a reader needs, around a window of
	// at least formMinFields fields.
	chrome := newChromeBudget(height, formMinFields+formIndicatorRows)
	showError := f.err != "" && chrome.want()
	showTitle := chrome.want()
	showHint := chrome.want()
	spacerBelowTitle := chrome.want()
	spacerAboveHint := chrome.want()

	avail := chrome.content()
	offset := clampOffset(f.offset, focusLine, avail, len(lines))
	above := offset > 0
	below := offset+avail < len(lines)
	budget := max(1, avail-boolInt(above)-boolInt(below))
	offset = clampOffset(f.offset, focusLine, budget, len(lines))
	f.offset = offset

	var b strings.Builder
	writeRow := func(row string) {
		if row != "" {
			b.WriteString(row)
			b.WriteString("\n")
		}
	}
	if showTitle {
		writeRow(th.ModalTitle.Render(f.Title))
	}
	if spacerBelowTitle {
		writeRow(" ")
	}
	if above {
		writeRow(th.Subtle.Render(fmt.Sprintf("  ↑ %d more", offset)))
	}
	for i := offset; i < len(lines) && i < offset+budget; i++ {
		writeRow(lines[i])
	}
	if remaining := len(lines) - (offset + budget); remaining > 0 {
		writeRow(th.Subtle.Render(fmt.Sprintf("  ↓ %d more", remaining)))
	}
	if showError {
		writeRow(th.Error.Render(f.err))
	}
	if spacerAboveHint {
		writeRow(" ")
	}
	if showHint {
		writeRow(th.Subtle.Render(formKeyHint))
	}
	body := strings.TrimRight(b.String(), "\n")

	if f.picker != nil {
		// The chooser takes the box: giving it the leftovers of the field list
		// (minus its own chrome) left a 50-entry category list showing one row.
		if showTitle {
			return th.ModalTitle.Render(f.Title) + "\n\n" + f.picker.View(th, width, max(1, height-2))
		}
		return f.picker.View(th, width, height)
	}
	return body
}

// fieldLine renders one field row: a padded label and the field's current value.
func (f *Form) fieldLine(th Theme, i, width int, field Field) string {
	plain := "  " + field.Label
	if i == f.index {
		plain = "▸ " + field.Label
	}
	label := plain + strings.Repeat(" ", max(0, formLabelWidth-ansi.StringWidth(plain)))
	if i == f.index {
		label = th.FieldFocused.Render(label)
	} else {
		label = th.FieldLabel.Render(label)
	}

	var value string
	switch field.Kind {
	case FieldText, FieldPassword:
		value = f.inputs[i].View()
	case FieldSelect:
		value = selectLabel(field)
		if i == f.index {
			value = th.RowSelected.Render(value)
		}
	case FieldBool:
		if strings.EqualFold(field.Value, "yes") {
			value = th.Positive.Render("yes")
		} else {
			value = th.Subtle.Render("no")
		}
	}
	return label + ansi.Truncate(value, max(8, width-formLabelWidth-2), "…")
}

// boolInt renders a bool as 0 or 1 for row budgeting.
func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// selectLabel renders a select field's current choice.
func selectLabel(field Field) string {
	if field.Value == "" {
		if field.ClearLabel != "" {
			return "(" + field.ClearLabel + ")"
		}
		return "(none)"
	}
	for _, o := range field.Options {
		if o.Value == field.Value {
			return o.Label
		}
	}
	return field.Value
}

// TextField builds a required-by-default text field.
func TextField(label, value string, validate func(string) error) Field {
	return Field{Label: label, Kind: FieldText, Value: value, Validate: validate}
}

// SelectField builds a select field. When required is false the field accepts
// the empty value and offers a clear entry.
func SelectField(label, value string, options []Option, required bool) Field {
	return Field{
		Label:      label,
		Kind:       FieldSelect,
		Value:      value,
		Options:    options,
		AllowClear: !required,
		ClearLabel: "none",
		Validate: func(v string) error {
			if required && strings.TrimSpace(v) == "" {
				return fmt.Errorf("required")
			}
			return nil
		},
	}
}

// BoolField builds a yes/no field.
func BoolField(label string, value bool) Field {
	state := "no"
	if value {
		state = "yes"
	}
	return Field{Label: label, Kind: FieldBool, Value: state}
}

// AmountField builds a field that accepts the same amount grammar as the API.
func AmountField(label, value string) Field {
	return Field{
		Label:       label,
		Kind:        FieldText,
		Value:       value,
		Placeholder: "0.00",
		Width:       16,
		Validate: func(v string) error {
			_, err := apiParseAmount(v)
			return err
		},
		Help: "decimal, at most two places",
	}
}
