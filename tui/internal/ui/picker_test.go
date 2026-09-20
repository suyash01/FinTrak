package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// press delivers one key to a picker as the App would.
func press(p *Picker, key string) {
	var msg tea.KeyMsg
	switch key {
	case "enter":
		msg = tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		msg = tea.KeyMsg{Type: tea.KeyEsc}
	case "up":
		msg = tea.KeyMsg{Type: tea.KeyUp}
	default:
		msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)}
	}
	p.Update(msg)
}

func options() []Option {
	return []Option{{Value: "a", Label: "Alpha"}, {Value: "b", Label: "Beta"}, {Value: "c", Label: "Gamma"}}
}

// TestPickerClearRowSelectsEmptyValue covers a real defect: enter on the leading
// "(none)" row used to be a no-op that left the picker open, so a nullable form
// field could not be cleared and the next key press was swallowed.
func TestPickerClearRowSelectsEmptyValue(t *testing.T) {
	p := NewPicker("Category", options(), "b", true, "none")

	// The cursor starts on the current value (Beta); Alpha sits between it and the
	// clear row, so walk up twice.
	press(p, "up")
	if p.Closed() {
		t.Fatal("moving the cursor must not close the picker")
	}
	press(p, "up")
	press(p, "enter")

	if !p.Closed() {
		t.Fatal("enter on the clear row must close the picker")
	}
	if p.Canceled() {
		t.Error("choosing the clear row is a selection, not a cancellation")
	}
	if got := p.Value(); got != "" {
		t.Errorf("value = %q, want the empty value", got)
	}
}

func TestPickerSelectsAnOption(t *testing.T) {
	p := NewPicker("Category", options(), "a", false, "")
	press(p, "down")
	press(p, "enter")

	if !p.Closed() || p.Value() != "b" {
		t.Fatalf("closed=%v value=%q, want b", p.Closed(), p.Value())
	}
	if got := p.Label(); got != "Beta" {
		t.Errorf("label = %q, want Beta", got)
	}
}

// TestPickerFilterThenSelect checks that filtering narrows the list and enter
// chooses the highlighted match rather than the first entry overall.
func TestPickerFilterThenSelect(t *testing.T) {
	p := NewPicker("Payee", options(), "", false, "")
	for _, r := range "gam" {
		press(p, string(r))
	}
	press(p, "enter")

	if !p.Closed() || p.Value() != "c" {
		t.Fatalf("closed=%v value=%q, want c", p.Closed(), p.Value())
	}
}

func TestPickerEscapeCancels(t *testing.T) {
	p := NewPicker("Payee", options(), "a", false, "")
	press(p, "esc")

	if !p.Closed() {
		t.Fatal("esc must close the picker")
	}
	if !p.Canceled() || p.Value() != "a" {
		t.Errorf("canceled=%v value=%q, want the original value kept", p.Canceled(), p.Value())
	}
}

// TestPickerEnterOnEmptyListClosesForNullableFields keeps a filtered-to-nothing
// list from trapping the user in an open overlay.
func TestPickerEnterOnEmptyListClosesForNullableFields(t *testing.T) {
	p := NewPicker("Payee", options(), "", true, "none")
	for _, r := range "zzz" {
		press(p, string(r))
	}
	press(p, "enter")

	if !p.Closed() {
		t.Fatal("enter with no matches must not leave the picker open")
	}
	if p.Value() != "" {
		t.Errorf("value = %q, want empty", p.Value())
	}
}

// TestPickerOnSelectFiresOnceWithTheChosenValue covers the modal path: a picker
// used directly as an overlay must act on its own result.
func TestPickerOnSelectFiresOnceWithTheChosenValue(t *testing.T) {
	p := NewPicker("Screen", options(), "a", false, "")
	var got []string
	p.OnSelect = func(value string) tea.Cmd {
		got = append(got, value)
		return nil
	}

	press(p, "enter")
	press(p, "enter") // a closed picker must not act again

	if len(got) != 1 || got[0] != "a" {
		t.Fatalf("OnSelect calls = %v, want exactly [a]", got)
	}
}

// TestPickerReachesTheFirstOptionWhenClearable covers a latent bug the grouped
// rework removed: the clear entry used to occupy cursor position zero, so once a
// field was nullable the first real option could never be highlighted or chosen.
func TestPickerReachesTheFirstOptionWhenClearable(t *testing.T) {
	p := NewPicker("Category", options(), "b", true, "none")

	press(p, "up")
	press(p, "enter")

	if !p.Closed() || p.Value() != "a" {
		t.Fatalf("closed=%v value=%q, want the first option a", p.Closed(), p.Value())
	}
}

// TestPickerGroupsAndSkipsHeadings checks that group headings are rendered, that
// the cursor steps over them, and that choosing a row still picks the option
// under it.
func TestPickerGroupsAndSkipsHeadings(t *testing.T) {
	grouped := []Option{
		{Value: "1", Label: "Groceries", Group: "Expense"},
		{Value: "2", Label: "Rent", Group: "Expense"},
		{Value: "3", Label: "Salary", Group: "Income"},
	}
	p := NewPicker("Category", grouped, "", false, "")

	// Two headings plus three options.
	if got := len(p.rows()); got != 5 {
		t.Fatalf("rows = %d, want 5 (2 headings + 3 options)", got)
	}
	if !p.rows()[0].header() {
		t.Error("the first row of a grouped list should be a heading")
	}
	if p.rows()[p.cursor].header() {
		t.Fatal("the cursor must not rest on a heading")
	}

	body := ansi.Strip(p.View(DefaultTheme(), 60, 12))
	for _, want := range []string{"Expense", "Income", "Groceries", "Salary"} {
		if !strings.Contains(body, want) {
			t.Errorf("%q missing from the rendered picker:\n%s", want, body)
		}
	}

	// Walking down must skip the "Income" heading and land on Salary.
	press(p, "down")
	press(p, "down")
	if p.rows()[p.cursor].header() {
		t.Fatal("down landed on a heading")
	}
	press(p, "enter")
	if p.Value() != "3" {
		t.Errorf("selected %q, want the option after the heading (3)", p.Value())
	}
}
