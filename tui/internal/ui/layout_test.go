package ui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/fintrak/tui/internal/api"
)

// These tests cover the fitting rule the widgets share: a scrolling area is sized
// from the box it is given, and the fixed rows around it are dropped before the
// area is allowed to shrink below a few rows. A fixed chrome is what left a
// two-field sign-in form showing one field and a fifty-entry category chooser
// showing one entry.

// TestLoginFormShowsAllFields is the reported case: the login card used to hand
// the form a height of zero, so even two fields did not fit.
func TestLoginFormShowsAllFields(t *testing.T) {
	client, err := api.New("http://127.0.0.1:1/api/v1")
	if err != nil {
		t.Fatalf("api.New: %v", err)
	}
	login := NewLoginModel(client)

	for _, size := range [][2]int{{80, 24}, {80, 14}, {80, 10}, {80, 8}} {
		body := ansi.Strip(login.View(DefaultTheme(), size[0], size[1]))
		for _, want := range []string{"Email", "Password"} {
			if !strings.Contains(body, want) {
				t.Errorf("%dx%d: %q missing:\n%s", size[0], size[1], want, body)
			}
		}
		if lines := strings.Count(body, "\n") + 1; lines > size[1] {
			t.Errorf("%dx%d: rendered %d lines, want at most %d:\n%s", size[0], size[1], lines, size[1], body)
		}
	}
}

// TestPickerShowsManyItemsAtUsualSizes checks the chooser no longer collapses:
// inside a modal it is given the box, and its own rows adapt.
func TestPickerShowsManyItemsAtUsualSizes(t *testing.T) {
	// Ungrouped, so the row count and the option count are the same thing here;
	// grouping (which consumes rows with headings) is covered separately.
	options := make([]Option, 40)
	for i := range options {
		options[i] = Option{Value: fmt.Sprint(i), Label: fmt.Sprintf("Option %02d", i)}
	}

	for _, tc := range []struct {
		height int
		want   int
	}{
		{height: 24, want: 8},
		{height: 14, want: 4},
		{height: 8, want: 3},
	} {
		p := NewPicker("Category", options, "", false, "")
		body := ansi.Strip(p.View(DefaultTheme(), 60, tc.height))
		shown := 0
		for _, o := range options {
			if strings.Contains(body, o.Label) {
				shown++
			}
		}
		if shown < tc.want {
			t.Errorf("height %d: %d options visible, want at least %d:\n%s", tc.height, shown, tc.want, body)
		}
		if lines := strings.Count(body, "\n") + 1; lines > tc.height {
			t.Errorf("height %d: rendered %d lines:\n%s", tc.height, lines, body)
		}
	}
}

// TestCategoryChooserInAFormShowsManyCategories drives the reported path: editing
// a transaction, opening the category field's chooser at a normal terminal size.
func TestCategoryChooserInAFormShowsManyCategories(t *testing.T) {
	ctx := &Ctx{Ref: &RefData{}, Theme: DefaultTheme(), Notify: func(Level, string, ...any) {}}
	categories := make([]api.Category, 30)
	for i := range categories {
		categories[i] = api.Category{ID: fmt.Sprintf("c%d", i), Name: fmt.Sprintf("Category %02d", i), GroupID: "expense", GroupName: "Expense"}
	}
	ctx.Ref.Categories = categories

	form := NewForm("t", "Edit transaction", []Field{
		SelectField("Category", "", ctx.Ref.CategoryOptions(), false),
	}, nil)
	form.Update(tea.KeyMsg{Type: tea.KeyEnter}) // opens the chooser
	if form.picker == nil {
		t.Fatal("the select field did not open a chooser")
	}

	body := ansi.Strip(form.View(DefaultTheme(), 60, 22))
	shown := 0
	for _, c := range categories {
		if strings.Contains(body, c.Name) {
			shown++
		}
	}
	if shown < 8 {
		t.Errorf("only %d categories visible in a 22-row modal, want at least 8:\n%s", shown, body)
	}
	if !strings.Contains(body, "Expense") {
		t.Errorf("the group heading is missing:\n%s", body)
	}
	if lines := strings.Count(body, "\n") + 1; lines > 22 {
		t.Errorf("rendered %d lines for a 22-row modal:\n%s", lines, body)
	}
}

// TestInfoOverlayShowsSeveralRows covers the same rule for read-only overlays
// (a loan schedule, a cycle list), which used to subtract a fixed six rows.
func TestInfoOverlayShowsSeveralRows(t *testing.T) {
	var body strings.Builder
	for i := range 40 {
		fmt.Fprintf(&body, "row %02d\n", i)
	}

	for _, height := range []int{20, 12, 8, 6} {
		modal := NewInfo("Report", strings.TrimRight(body.String(), "\n"))
		shown := 0
		for i := range 40 {
			if strings.Contains(ansi.Strip(modal.View(DefaultTheme(), 60, height)), fmt.Sprintf("row %02d", i)) {
				shown++
			}
		}
		if shown < minListRows {
			t.Errorf("height %d: only %d rows visible, want at least %d", height, shown, minListRows)
		}
	}
}

// TestListRowsNeverVanishes keeps a screen's table from being handed a height
// that renders nothing at all.
func TestListRowsNeverVanishes(t *testing.T) {
	for height := 2; height <= 12; height++ {
		if got := listRows(height); got < 2 {
			t.Errorf("listRows(%d) = %d, want at least a header row plus one item", height, got)
		}
	}
	if got := listRows(1); got < 1 {
		t.Errorf("listRows(1) = %d, want at least one row", got)
	}
	if got := listRows(30); got > 30 {
		t.Errorf("listRows(30) = %d, more than the box", got)
	}
}
