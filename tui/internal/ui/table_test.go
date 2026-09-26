package ui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// stylePrefix is the escape sequence a style emits before its text. Comparing
// against it lets a test assert that a row carries a role's style without
// restating the column padding the table applied first.
//
// lipgloss v2 downsamples at each tea.Program's output layer rather than here,
// so a style renders its full-colour escape whether or not a terminal asked for
// it. That makes the prefix a statement about the colour the table chose, which
// is what these tests are about; whether the session's terminal can display it
// is the program's decision and the door's end-to-end test's to check.
func stylePrefix(style lipgloss.Style) string {
	const marker = "\x00"
	rendered := style.Render(marker)
	if i := strings.Index(rendered, marker); i >= 0 {
		return rendered[:i]
	}
	return rendered
}

// TestTableAppliesCellRoles is a regression test for a defect: styleRow computed
// the row's roles and then returned the row unchanged in every branch, so no
// table ever coloured a cell — the amounts, negative balances and muted rows that
// six screens carefully mark rendered as plain text.
func TestTableAppliesCellRoles(t *testing.T) {
	th := DefaultTheme()
	table := &Table{}
	table.SetColumns(
		Column{Title: "Payee", Width: 12},
		Column{Title: "Amount", Width: 12, Align: AlignRight},
	)
	table.SetRows([][]Cell{
		{Text("Groceries"), {Text: "-42.00", Role: RoleNegative}},
		{Text("Salary"), {Text: "900.00", Role: RolePositive}},
		{Text("Closed card"), Muted("0.00")},
		{Text("Plain"), Text("1.00")},
	})
	table.SetCursor(0)

	lines := strings.Split(table.View(th, 26, 10, "none"), "\n")
	if len(lines) < 5 {
		t.Fatalf("rendered %d lines, want a header and four rows", len(lines))
	}
	negative, positive, muted, plain := lines[1], lines[2], lines[3], lines[4]

	for _, tc := range []struct {
		name  string
		line  string
		style lipgloss.Style
	}{
		{"negative", negative, th.Negative},
		{"positive", positive, th.Positive},
		{"muted", muted, th.Subtle},
	} {
		if !strings.Contains(tc.line, stylePrefix(tc.style)) {
			t.Errorf("the %s row was not rendered in its role's colour: %q", tc.name, tc.line)
		}
	}
	if plain != ansi.Strip(plain) {
		t.Errorf("a row of plain cells was styled anyway: %q", plain)
	}

	// The role colour wraps the padded text, so a styled row keeps the column
	// layout of an unstyled one.
	if got, want := ansi.StringWidth(negative), ansi.StringWidth(plain); got != want {
		t.Errorf("a styled row is %d columns wide, want %d: the colour shifted the columns", got, want)
	}
	if got, want := ansi.Strip(negative), ansi.Strip(plain); !strings.HasSuffix(got, "-42.00") || !strings.HasSuffix(want, "1.00") {
		t.Errorf("the amount was not padded before it was styled: %q / %q", got, want)
	}
}

// TestTableKeepsTheRoleColourOnTheSelectedRow keeps the two styles from cancelling
// each other: the cursor row carries the selection background as well as its
// cell's role colour, and a role's reset must not clear the highlight.
func TestTableKeepsTheRoleColourOnTheSelectedRow(t *testing.T) {
	th := DefaultTheme()
	table := &Table{}
	table.SetColumns(Column{Title: "Payee", Width: 12}, Column{Title: "Amount", Width: 12, Align: AlignRight})
	table.SetRows([][]Cell{
		{Text("Groceries"), {Text: "-42.00", Role: RoleNegative}},
		{Text("Salary"), {Text: "900.00", Role: RolePositive}},
	})

	selected := strings.Split(table.View(th, 26, 10, "none"), "\n")[1]
	for name, style := range map[string]lipgloss.Style{"role colour": th.Negative, "selection background": th.RowSelected} {
		if !strings.Contains(selected, stylePrefix(style)) {
			t.Errorf("the cursor row lost its %s: %q", name, selected)
		}
	}

	// Moving the cursor moves the background with it.
	table.SetCursor(1)
	lines := strings.Split(table.View(th, 26, 10, "none"), "\n")
	if strings.Contains(lines[1], stylePrefix(th.RowSelected)) {
		t.Errorf("the previous row kept the selection background: %q", lines[1])
	}
	if !strings.Contains(lines[2], stylePrefix(th.RowSelected)) {
		t.Errorf("the new cursor row has no selection background: %q", lines[2])
	}
}
