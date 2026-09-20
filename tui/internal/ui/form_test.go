package ui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// TestFormScrollsToKeepTheFocusedFieldVisible is a regression test for the
// reported overflow: the form rendered every field regardless of the height it
// was given, so on a short terminal the App clipped the box and the upper fields
// could neither be seen nor reached.
func TestFormScrollsToKeepTheFocusedFieldVisible(t *testing.T) {
	fields := make([]Field, 12)
	for i := range fields {
		fields[i] = Field{Label: fmt.Sprintf("Field %02d", i), Kind: FieldText}
	}
	form := NewForm("tall", "Tall form", fields, nil)

	const (
		width  = 60
		height = 12
	)
	assertFits := func(t *testing.T, body string) {
		t.Helper()
		if lines := strings.Count(body, "\n") + 1; lines > height {
			t.Errorf("form rendered %d lines for a %d-row modal:\n%s", lines, height, body)
		}
	}

	body := ansi.Strip(form.View(DefaultTheme(), width, height))
	assertFits(t, body)
	if !strings.Contains(body, "Field 00") {
		t.Errorf("the first field should be visible before any movement:\n%s", body)
	}
	if !strings.Contains(body, "more") {
		t.Errorf("hidden fields should be announced:\n%s", body)
	}
	if strings.Contains(body, "Field 11") {
		t.Errorf("the last field should not be rendered yet:\n%s", body)
	}

	// Walking to the end must scroll the focused field into view.
	for range len(fields) - 1 {
		form.Update(tea.KeyMsg{Type: tea.KeyTab})
	}
	body = ansi.Strip(form.View(DefaultTheme(), width, height))
	assertFits(t, body)
	if !strings.Contains(body, "▸ Field 11") {
		t.Errorf("the focused field must be scrolled into view:\n%s", body)
	}
	if strings.Contains(body, "Field 00") {
		t.Errorf("the top of the list should have scrolled away:\n%s", body)
	}

	// And walking back brings the top back.
	for range len(fields) - 1 {
		form.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	}
	body = ansi.Strip(form.View(DefaultTheme(), width, height))
	if !strings.Contains(body, "▸ Field 00") {
		t.Errorf("scrolling up must return to the first field:\n%s", body)
	}
}

// TestFormFitsAShortModalWithHelpAndError keeps the focused field visible even
// when its help line and an error line are also on screen.
func TestFormFitsAShortModalWithHelpAndError(t *testing.T) {
	fields := make([]Field, 8)
	for i := range fields {
		fields[i] = Field{Label: fmt.Sprintf("Field %02d", i), Kind: FieldText, Help: "hint for this field"}
	}
	form := NewForm("tall", "Tall form", fields, nil)
	form.SetError(fmt.Errorf("amount: must be positive"))

	body := ansi.Strip(form.View(DefaultTheme(), 60, 10))
	if lines := strings.Count(body, "\n") + 1; lines > 10 {
		t.Errorf("form rendered %d lines for a 10-row modal:\n%s", lines, body)
	}
	if !strings.Contains(body, "amount: must be positive") {
		t.Errorf("the error must stay visible:\n%s", body)
	}
	if !strings.Contains(body, "▸ Field 00") {
		t.Errorf("the focused field must stay visible:\n%s", body)
	}
}

// TestFormAlignsLabelsAndValues covers the other half of the report: the label
// and value ran together ("▸ AccountEveryday") because the label was padded with
// %-28s, which counts the escape bytes of the styled text.
func TestFormAlignsLabelsAndValues(t *testing.T) {
	form := NewForm("one", "One field", []Field{
		{Label: "Account", Kind: FieldText, Value: "Everyday"},
	}, nil)

	for _, line := range strings.Split(ansi.Strip(form.View(DefaultTheme(), 60, 10)), "\n") {
		if !strings.Contains(line, "Account") {
			continue
		}
		at := strings.Index(line, "Account") + len("Account")
		if !strings.HasPrefix(line[at:], "   ") {
			t.Errorf("label and value are not separated: %q", line)
		}
		return
	}
	t.Fatal("the field line is missing")
}

// oversizedModal returns more content than any terminal could hold.
type oversizedModal struct{}

func (oversizedModal) Update(tea.Msg) tea.Cmd { return nil }
func (oversizedModal) Closed() bool           { return false }
func (oversizedModal) Canceled() bool         { return true }
func (oversizedModal) View(Theme, int, int) string {
	return strings.Repeat("a very long modal line that keeps going and going\n", 200)
}

// TestOversizedModalCannotEscapeTheScreen is the belt-and-braces guarantee: a
// modal that ignores the size it is handed must still be framed inside the
// terminal, rather than having rows silently spliced off-screen.
func TestOversizedModalCannotEscapeTheScreen(t *testing.T) {
	a, _, _ := newAppForTest(t)
	a.width, a.height = 80, 24
	a.focus = FocusContent
	a.modal = oversizedModal{}

	out := a.View()
	lines := strings.Split(out, "\n")
	if len(lines) > 24 {
		t.Errorf("the frame is %d lines tall for a 24-row terminal", len(lines))
	}
	for i, line := range lines {
		if width := ansi.StringWidth(line); width > 80 {
			t.Errorf("line %d is %d cells wide, want at most 80: %q", i, width, ansi.Strip(line))
		}
	}
}

// TestHelpOverlayFitsAShortTerminal covers the same problem for the key
// reference, whose two sections do not fit a 24-row terminal.
func TestHelpOverlayFitsAShortTerminal(t *testing.T) {
	rows := make([]HelpRow, 20)
	for i := range rows {
		rows[i] = HelpRow{Keys: fmt.Sprintf("k%d", i), Desc: fmt.Sprintf("action %d", i)}
	}
	help := NewHelp(rows, rows)

	body := ansi.Strip(help.View(DefaultTheme(), 60, 12))
	if lines := strings.Count(body, "\n") + 1; lines > 12 {
		t.Errorf("help rendered %d lines for a 12-row modal:\n%s", lines, body)
	}
	if !strings.Contains(body, "more") {
		t.Errorf("hidden rows should be announced:\n%s", body)
	}

	// Scrolling reveals the rest: reaching the end means the last row of the second
	// section is on screen (its heading has scrolled past by then, which is fine).
	for range 40 {
		help.Update(tea.KeyMsg{Type: tea.KeyDown})
	}
	body = ansi.Strip(help.View(DefaultTheme(), 60, 12))
	if lines := strings.Count(body, "\n") + 1; lines > 12 {
		t.Errorf("help rendered %d lines after scrolling, want at most 12", lines)
	}
	if !strings.Contains(body, "action 19") {
		t.Errorf("scrolling to the end should reach the last row:\n%s", body)
	}
	if strings.Contains(body, "action 0 ") {
		t.Errorf("the top of the reference should have scrolled away:\n%s", body)
	}
}

// TestShortModalStillShowsSeveralFields is the regression test for a real report:
// windowing was introduced to stop clipping, but the chrome around the window was
// fixed, so on a short terminal the window collapsed to a single field. A usable
// modal shows several, and scrolls for the rest.
func TestShortModalStillShowsSeveralFields(t *testing.T) {
	fields := make([]Field, 9)
	for i := range fields {
		fields[i] = Field{Label: fmt.Sprintf("Field %02d", i), Kind: FieldText}
	}

	for height := 5; height <= 14; height++ {
		form := NewForm("tall", "Tall form", fields, nil)
		body := ansi.Strip(form.View(DefaultTheme(), 60, height))

		if lines := strings.Count(body, "\n") + 1; lines > height {
			t.Errorf("height %d: rendered %d lines", height, lines)
		}
		shown := 0
		for _, field := range fields {
			if strings.Contains(body, field.Label) {
				shown++
			}
		}
		if shown < 3 {
			t.Errorf("height %d: only %d field(s) visible, want at least 3:\n%s", height, shown, body)
		}
	}
}

// TestSmallTerminalModalShowsSeveralFormFields drives the whole path: the App
// sizes the frame and the box, the form fills it. This is the reported case — a
// short terminal where only one line of the edit form was reachable.
func TestSmallTerminalModalShowsSeveralFormFields(t *testing.T) {
	fields := make([]Field, 9)
	labels := make([]string, 0, len(fields))
	for i := range fields {
		fields[i] = Field{Label: fmt.Sprintf("Field %02d", i), Kind: FieldText}
		labels = append(labels, fields[i].Label)
	}

	for _, size := range [][2]int{{80, 10}, {80, 11}, {80, 14}, {80, 24}, {120, 40}} {
		a, _, _ := newAppForTest(t)
		a.width, a.height = size[0], size[1]
		a.focus = FocusContent
		a.modal = NewForm("tall", "Tall form", fields, nil)

		frame := ansi.Strip(a.View())
		shown := 0
		for _, label := range labels {
			if strings.Contains(frame, label) {
				shown++
			}
		}
		if shown < 3 {
			t.Errorf("%dx%d: only %d field(s) visible, want at least 3:\n%s", size[0], size[1], shown, frame)
		}
		if lines := strings.Count(frame, "\n") + 1; lines > size[1] {
			t.Errorf("%dx%d: frame is %d lines", size[0], size[1], lines)
		}
	}
}
