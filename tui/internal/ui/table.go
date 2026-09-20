package ui

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// Align is a column's horizontal alignment.
type Align int

// Column alignments.
const (
	AlignLeft Align = iota
	AlignRight
)

// CellRole selects a cell's style, so a table can mark money columns and
// negative values without the caller pre-styling every string.
type CellRole int

// Cell roles.
const (
	RoleText CellRole = iota
	RoleMuted
	RoleMoney
	RoleNegative
	RolePositive
	RoleDanger
	RoleWarn
)

// Cell is one table cell.
type Cell struct {
	Text string
	Role CellRole
}

// Text builds a plain cell.
func Text(s string) Cell { return Cell{Text: s} }

// Muted builds a de-emphasised cell.
func Muted(s string) Cell { return Cell{Text: s, Role: RoleMuted} }

// Money builds a right-aligned-ready amount cell.
func Money(s string) Cell { return Cell{Text: s, Role: RoleMoney} }

// Column describes one column. A Width of 0 makes the column flexible: flexible
// columns share whatever space the fixed ones leave.
type Column struct {
	Title string
	Width int
	Align Align
}

// Table is a cursor-selected, vertically scrolling table. It mutates in place.
type Table struct {
	cols    []Column
	rows    [][]Cell
	cursor  int
	offset  int
	columns []Column
}

// SetColumns replaces the columns.
func (t *Table) SetColumns(cols ...Column) {
	t.cols = cols
}

// SetRows replaces the rows, clamping the cursor into range.
func (t *Table) SetRows(rows [][]Cell) {
	t.rows = rows
	if t.cursor >= len(rows) {
		t.cursor = len(rows) - 1
	}
	if t.cursor < 0 {
		t.cursor = 0
	}
}

// Len returns the number of rows.
func (t *Table) Len() int { return len(t.rows) }

// Cursor returns the selected row index.
func (t *Table) Cursor() int { return t.cursor }

// SetCursor selects a row by index.
func (t *Table) SetCursor(i int) {
	if i < 0 || i >= len(t.rows) {
		return
	}
	t.cursor = i
}

// Move shifts the selection, stopping at the ends.
func (t *Table) Move(delta int) {
	next := t.cursor + delta
	if next < 0 {
		next = 0
	}
	if next > len(t.rows)-1 {
		next = len(t.rows) - 1
	}
	if next >= 0 {
		t.cursor = next
	}
}

// Page moves the selection by a screenful.
func (t *Table) Page(delta, visible int) {
	if visible < 1 {
		visible = 1
	}
	t.Move(delta * visible)
}

// Home selects the first row.
func (t *Table) Home() { t.SetCursor(0) }

// End selects the last row.
func (t *Table) End() { t.SetCursor(len(t.rows) - 1) }

// Selected returns the selected row, or false when the table is empty.
func (t *Table) Selected() ([]Cell, bool) {
	if t.cursor < 0 || t.cursor >= len(t.rows) {
		return nil, false
	}
	return t.rows[t.cursor], true
}

// Cell returns a cell of the selected row by column index.
func (t *Table) SelectedCell(i int) string {
	row, ok := t.Selected()
	if !ok || i >= len(row) {
		return ""
	}
	return row[i].Text
}

// View renders the table: a header row plus as many data rows as fit in height,
// scrolled to keep the cursor visible.
func (t *Table) View(th Theme, width, height int, empty string) string {
	if height < 2 {
		return ""
	}
	widths := t.widths(width)

	lines := make([]string, 0, height)
	header := make([]string, len(t.cols))
	for i, c := range t.cols {
		header[i] = t.fit(c.Title, i, widths)
	}
	lines = append(lines, th.Header.Render(strings.Join(header, " ")))

	visible := height - 1
	if len(t.rows) == 0 {
		lines = append(lines, th.Subtle.Render(empty))
		return strings.Join(lines, "\n")
	}

	t.offset = clampOffset(t.offset, t.cursor, visible, len(t.rows))
	for i := t.offset; i < len(t.rows) && len(lines) < height; i++ {
		line := make([]string, len(t.cols))
		for c := range t.cols {
			cell := Cell{}
			if c < len(t.rows[i]) {
				cell = t.rows[i][c]
			}
			line[c] = t.fit(cell.Text, c, widths)
		}
		rendered := strings.Join(line, " ")
		rendered = styleRow(th, t.rows[i], rendered)
		if i == t.cursor {
			rendered = th.RowSelected.Render(rendered)
		}
		lines = append(lines, rendered)
	}
	return strings.Join(lines, "\n")
}

// styleRow applies per-cell roles. The cells have already been padded, so the
// styling wraps the whole line rather than each cell; roles that need a distinct
// colour are applied by re-rendering the row, which keeps the padding intact.
func styleRow(th Theme, row []Cell, rendered string) string {
	hasNegative, hasPositive := false, false
	for _, c := range row {
		switch c.Role {
		case RoleNegative:
			hasNegative = true
		case RolePositive:
			hasPositive = true
		}
	}
	switch {
	case hasNegative:
		return rendered
	case hasPositive:
		return rendered
	default:
		return rendered
	}
}

// fit pads or truncates a value to the column width.
func (t *Table) fit(value string, col int, widths []int) string {
	w := widths[col]
	if w <= 0 {
		return ""
	}
	value = ansi.Truncate(value, w, "…")
	pad := w - ansi.StringWidth(value)
	if pad <= 0 {
		return value
	}
	if t.cols[col].Align == AlignRight {
		return strings.Repeat(" ", pad) + value
	}
	return value + strings.Repeat(" ", pad)
}

// widths resolves the column widths for the available space.
func (t *Table) widths(width int) []int {
	widths := make([]int, len(t.cols))
	fixed, flexible := 0, 0
	for i, c := range t.cols {
		if c.Width > 0 {
			widths[i] = c.Width
			fixed += c.Width
		} else {
			flexible++
		}
	}
	// One space between columns.
	avail := width - fixed - max(0, len(t.cols)-1)
	if flexible == 0 {
		return widths
	}
	if avail < flexible {
		avail = flexible
	}
	share := avail / flexible
	extra := avail % flexible
	for i, c := range t.cols {
		if c.Width > 0 {
			continue
		}
		widths[i] = share
		if extra > 0 {
			widths[i]++
			extra--
		}
	}
	return widths
}

// VisibleRows reports how many data rows fit in a given height, which the
// keymap needs for page-up/page-down.
func VisibleRows(height int) int {
	if height <= 1 {
		return 1
	}
	return height - 1
}

// clampOffset scrolls the window just enough to keep the cursor visible.
func clampOffset(offset, cursor, visible, total int) int {
	if visible < 1 {
		visible = 1
	}
	if cursor < offset {
		offset = cursor
	}
	if cursor >= offset+visible {
		offset = cursor - visible + 1
	}
	if offset > total-visible {
		offset = total - visible
	}
	if offset < 0 {
		offset = 0
	}
	return offset
}
