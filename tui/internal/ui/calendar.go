package ui

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/fintrak/client/api"
)

func init() {
	registerScreen(110, func(ctx *Ctx) Screen { return NewCalendar(ctx) })
}

// calendarCellWidth is one day cell: "NN BB F" — the day number, a two-glyph
// intensity block and a marker flag. The grid always renders at this width, and
// trimToBox clips it on a terminal too narrow to hold seven columns.
const calendarCellWidth = 6

// calendarMaxMonths caps how many month grids a window may span, so a mistyped
// year in the window form cannot make the screen walk thousands of months.
const calendarMaxMonths = 60

// Intensity glyph families. A terminal has no colour ramp, so the glyph shape
// carries the magnitude and the family carries the sign: a surplus is drawn with
// solid blocks that grow with |net|, a deficit with hatched blocks. The two
// families stay tellable apart on a monochrome terminal, where the
// theme's Positive/Negative colours are the only other difference.
var (
	calendarSurplus = [3]string{"▁", "▄", "█"}
	calendarDeficit = [3]string{"░", "▒", "▓"}
)

// Calendar is the cash-flow calendar: a GitHub-style heatmap of daily net flow
// over the server's window.
//
// GET /dashboard/cash-flow-calendar answers with only the days that HAVE
// transactions, so the screen owns the gap filling: it draws a continuous
// Sunday-first month grid, blanks the days the API omitted, and shades each day
// against MaxAbsNet — the scale reference the API computes, which the TUI only
// ever divides by. When the window names one account the payload also carries
// billing-cycle boundaries and synthetic markers (month-end running balance,
// per-cycle outstanding), which the grid separates and the overlay list prints;
// those figures are the server's, never recomputed here. The screen is read-only
// and calls no mutation, so it owns no done message.
type Calendar struct {
	ctx    *Ctx
	filter api.WindowFilter

	data    api.CashFlowCalendar
	fetched bool

	// byDate holds the sparse day list, markers the summary points per day and
	// cycleStarts the days a billing cycle opens, so drawing a cell costs a map
	// lookup rather than a scan.
	byDate      map[string]api.CashFlowCalendarDay
	markers     map[string][]api.CashFlowCalendarMarker
	cycleStarts map[string]bool

	// months are the first days of every month the window touches, ascending.
	// The cursor is the selected day and therefore also picks the drawn month,
	// which keeps one source of truth for "where am I".
	months []time.Time
	cursor time.Time

	keys calendarKeys
}

// calendarKeys are the screen's bindings.
type calendarKeys struct {
	Window key.Binding
	Day    key.Binding
	Next   key.Binding
	Prev   key.Binding
}

func newCalendarKeys() calendarKeys {
	return calendarKeys{
		Window: key.NewBinding(key.WithKeys("f"), key.WithHelp("f", "window")),
		Day:    key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "day detail")),
		Next:   key.NewBinding(key.WithKeys("m"), key.WithHelp("m", "next month")),
		Prev:   key.NewBinding(key.WithKeys("M"), key.WithHelp("M", "prev month")),
	}
}

// NewCalendar builds the calendar screen. The window starts empty, which asks
// the server for its default range.
func NewCalendar(ctx *Ctx) *Calendar {
	return &Calendar{
		ctx:         ctx,
		byDate:      map[string]api.CashFlowCalendarDay{},
		markers:     map[string][]api.CashFlowCalendarMarker{},
		cycleStarts: map[string]bool{},
		cursor:      calendarToday(),
		keys:        newCalendarKeys(),
	}
}

// Title implements Screen.
func (c *Calendar) Title() string { return "Calendar" }

// Keys implements Screen.
func (c *Calendar) Keys() []key.Binding {
	return []key.Binding{c.keys.Window, c.keys.Day, c.keys.Next, c.keys.Prev}
}

// CapturesText implements Screen: this screen has no inline text input.
func (c *Calendar) CapturesText() bool { return false }

// Refresh implements Screen.
func (c *Calendar) Refresh() tea.Cmd { return c.reload() }

// reload fetches the window the user last applied.
func (c *Calendar) reload() tea.Cmd {
	filter := c.filter
	return load("calendar.list", func(ctx context.Context) (api.CashFlowCalendar, error) {
		return c.ctx.Client.CashFlowCalendar(ctx, filter)
	})
}

// Update implements Screen.
func (c *Calendar) Update(msg tea.Msg) tea.Cmd {
	switch m := msg.(type) {
	case loaded[api.CashFlowCalendar]:
		if m.tag != "calendar.list" {
			break
		}
		if m.err != nil {
			c.ctx.Notify(LevelError, "%s", m.err)
			return nil
		}
		c.data = m.data
		c.fetched = true
		c.index()
		c.buildMonths()
		c.selectCursor()
		return nil

	case tea.KeyMsg:
		return c.handleKey(m)
	}
	return nil
}

// handleKey routes a key press. Arrows move the cursor day by day and the drawn
// month follows the cursor; m/M are the only way to leave the current grid.
func (c *Calendar) handleKey(msg tea.KeyMsg) tea.Cmd {
	switch {
	case keyMatches(c.keys.Window, msg):
		c.openWindowForm()
		return nil
	case keyMatches(c.keys.Day, msg):
		c.openDayInfo()
		return nil
	case keyMatches(c.keys.Next, msg):
		c.shiftMonth(1)
		return nil
	case keyMatches(c.keys.Prev, msg):
		c.shiftMonth(-1)
		return nil
	}

	switch msg.String() {
	case "left":
		c.moveCursor(-1)
	case "right":
		c.moveCursor(1)
	case "up", "k":
		c.moveCursor(-7)
	case "down", "j":
		c.moveCursor(7)
	}
	return nil
}

// index rebuilds the lookups the grid reads; drawing is a per-cell map lookup.
func (c *Calendar) index() {
	c.byDate = make(map[string]api.CashFlowCalendarDay, len(c.data.Days))
	for _, day := range c.data.Days {
		if day.Date == "" {
			continue
		}
		c.byDate[day.Date] = day
	}

	c.markers = make(map[string][]api.CashFlowCalendarMarker, len(c.data.Markers))
	for _, marker := range c.data.Markers {
		c.markers[marker.Date] = append(c.markers[marker.Date], marker)
	}

	c.cycleStarts = make(map[string]bool, len(c.data.Cycles))
	for _, cycle := range c.data.Cycles {
		if cycle.StartDate.IsZero() {
			continue
		}
		c.cycleStarts[cycle.StartDate.Format(api.DateLayout)] = true
	}
}

// buildMonths collects every month the grid may show: the months the filter
// window spans, plus any month a returned day or cycle falls in. The second
// source matters because an empty window asks for the server's own range, and
// the screen only learns its extent from the payload.
func (c *Calendar) buildMonths() {
	seen := map[string]time.Time{}
	add := func(t time.Time) {
		if len(seen) >= calendarMaxMonths {
			return
		}
		first := calendarMonthFirst(t)
		seen[first.Format(api.DateLayout)] = first
	}

	from, fromOK := calendarParseDate(c.filter.DateFrom)
	to, toOK := calendarParseDate(c.filter.DateTo)
	switch {
	case fromOK && toOK && !to.Before(from):
		for m := calendarMonthFirst(from); !m.After(calendarMonthFirst(to)) && len(seen) < calendarMaxMonths; m = m.AddDate(0, 1, 0) {
			add(m)
		}
	case fromOK:
		add(from)
	case toOK:
		add(to)
	}

	for _, day := range c.data.Days {
		if t, ok := calendarParseDate(day.Date); ok {
			add(t)
		}
	}
	for _, cycle := range c.data.Cycles {
		if !cycle.StartDate.IsZero() {
			add(cycle.StartDate)
		}
		if !cycle.EndDate.IsZero() {
			add(cycle.EndDate)
		}
	}
	if len(seen) == 0 {
		add(calendarToday())
	}

	c.months = c.months[:0]
	for _, m := range seen {
		c.months = append(c.months, m)
	}
	sort.Slice(c.months, func(i, j int) bool { return c.months[i].Before(c.months[j]) })
}

// selectCursor keeps the selection inside the window after a reload, moving it
// to today when the window covers today and to the newest month otherwise — the
// days a user most likely wants to inspect first.
func (c *Calendar) selectCursor() {
	if len(c.months) == 0 {
		return
	}
	first := c.months[0]
	last := c.months[len(c.months)-1].AddDate(0, 1, -1)
	inWindow := func(t time.Time) bool { return !t.Before(first) && !t.After(last) }

	if inWindow(c.cursor) {
		return
	}
	c.cursor = c.months[len(c.months)-1]
	if today := calendarToday(); inWindow(today) {
		c.cursor = today
	}
}

// moveCursor shifts the selection by whole days, stopping at the window edges
// instead of wrapping, so an arrow key can never silently change the window.
func (c *Calendar) moveCursor(delta int) {
	if len(c.months) == 0 {
		return
	}
	next := c.cursor.AddDate(0, 0, delta)
	first := c.months[0]
	last := c.months[len(c.months)-1].AddDate(0, 1, -1)
	if next.Before(first) || next.After(last) {
		return
	}
	c.cursor = next
}

// shiftMonth jumps the cursor a calendar month at a time, keeping the day of
// month where the target month has it (the 31st lands on the 28th or 30th). A
// month the window does not cover is refused with a status note rather than
// drawn empty.
func (c *Calendar) shiftMonth(delta int) {
	if len(c.months) == 0 {
		return
	}
	target := c.monthIndex() + delta
	if target < 0 || target >= len(c.months) {
		c.ctx.Notify(LevelInfo, "the window covers %s", pluralise(len(c.months), "month", "months"))
		return
	}
	c.cursor = calendarSameDayInMonth(c.cursor, c.months[target])
}

// monthIndex is the position of the cursor's month in the window.
func (c *Calendar) monthIndex() int {
	key := calendarMonthFirst(c.cursor).Format(api.DateLayout)
	for i, m := range c.months {
		if m.Format(api.DateLayout) == key {
			return i
		}
	}
	return 0
}

// openWindowForm edits the request window. The submit is local — it rewrites the
// filter and asks for a fresh payload — so the form closes itself instead of
// waiting for a mutation to report back.
func (c *Calendar) openWindowForm() {
	fields := []Field{
		{
			Label: "From", Kind: FieldText, Value: c.filter.DateFrom, Placeholder: "YYYY-MM-DD", Width: 12,
			Validate: optionalDate, Help: "inclusive; blank means the server's default start",
		},
		{
			Label: "To", Kind: FieldText, Value: c.filter.DateTo, Placeholder: "YYYY-MM-DD", Width: 12,
			Validate: optionalDate, Help: "inclusive; blank means the server's default end",
		},
		SelectField("Account", c.filter.AccountID, c.ctx.Ref.AccountOptions(), false),
	}
	c.ctx.Open(NewForm("calendar.window", "Cash-flow window", fields, func(f *Form) tea.Cmd {
		from, to := strings.TrimSpace(f.Value("From")), strings.TrimSpace(f.Value("To"))
		// The layout sorts lexicographically, so the bounds compare as strings.
		if from != "" && to != "" && to < from {
			f.SetError(errText("the window ends before it starts"))
			return nil
		}
		c.filter = api.WindowFilter{DateFrom: from, DateTo: to, AccountID: f.Value("Account")}
		c.fetched = false
		f.Close()
		return c.reload()
	}))
}

// openDayInfo reports the cursor day. Income, expense, net and count are the
// server's figures; the grid only shades them.
func (c *Calendar) openDayInfo() {
	key := c.cursor.Format(api.DateLayout)
	entry, has := c.byDate[key]

	var b strings.Builder
	if !has || entry.Count == 0 {
		fmt.Fprintf(&b, "No transactions on %s.\n", key)
	} else {
		fmt.Fprintf(&b, "Date     %s\n", key)
		fmt.Fprintf(&b, "Income   %s\n", entry.Income.Display())
		fmt.Fprintf(&b, "Expense  %s\n", entry.Expense.Display())
		fmt.Fprintf(&b, "Net      %s\n", calendarNetText(entry.Net))
		fmt.Fprintf(&b, "Count    %s\n", pluralise(entry.Count, "transaction", "transactions"))
	}

	if markers := c.markers[key]; len(markers) > 0 {
		b.WriteString("\nServer markers\n")
		for _, marker := range markers {
			fmt.Fprintf(&b, "  %-18s %10s  %s\n", calendarMarkerKind(marker.Kind), marker.Amount.Display(), marker.Label)
		}
	}
	if cycle, ok := c.cycleFor(c.cursor); ok {
		fmt.Fprintf(&b, "\nBilling cycle\n  %s\n  %s\n  outstanding %s\n",
			cycle.Label, formatRange(cycle.StartDate, cycle.EndDate), cycle.Outstanding.Display())
	}

	c.ctx.Open(NewInfo("Day "+key, b.String()).WithFooter(
		"Figures are the server's cash-flow calendar for this day; the grid only scales them."))
}

// cycleFor reports the billing cycle containing t. Cycles arrive only when the
// window names one account, because a statement period belongs to an account.
func (c *Calendar) cycleFor(t time.Time) (api.CashFlowCalendarCycle, bool) {
	for _, cycle := range c.data.Cycles {
		if !t.Before(cycle.StartDate) && !t.After(cycle.EndDate) {
			return cycle, true
		}
	}
	return api.CashFlowCalendarCycle{}, false
}

// View implements Screen.
func (c *Calendar) View(width, height int) string {
	th := c.ctx.Theme
	parts := c.header(width)
	if !c.fetched {
		parts = append(parts, th.Subtle.Render("loading…"))
		return trimToBox(strings.Join(parts, "\n"), width, height)
	}

	parts = append(parts, strings.Split(c.monthGrid(), "\n")...)
	if len(c.data.Days) == 0 {
		parts = append(parts, th.Subtle.Render("no transactions in this window"))
	}
	parts = append(parts, c.legendLine(width))

	// The grid, the cursor's day and the key hints are the screen's reason to
	// exist, so the overlay list is what shrinks when the box is short.
	parts = append(parts, c.overlayLines(width, height-len(parts)-3)...)
	parts = append(parts, c.detailLine(width))
	parts = append(parts, th.Subtle.Render(truncate("←/→ day · ↑/↓ week · m/M month · enter day · f window", width)))
	return trimToBox(strings.Join(parts, "\n"), width, height)
}

// header summarises the window, the account it is scoped to and the totals over
// the whole window — not the drawn month.
func (c *Calendar) header(width int) []string {
	th := c.ctx.Theme

	window := "server default window"
	switch {
	case c.filter.DateFrom != "" && c.filter.DateTo != "":
		window = c.filter.DateFrom + " … " + c.filter.DateTo
	case c.filter.DateFrom != "":
		window = "from " + c.filter.DateFrom
	case c.filter.DateTo != "":
		window = "until " + c.filter.DateTo
	}
	account := "all accounts"
	if c.filter.AccountID != "" {
		account = c.ctx.Ref.AccountName(c.filter.AccountID)
	}

	// The month counter leads the subtitle: it is what m/M act on, so it must
	// survive the truncation a narrow box forces.
	title := th.Title.Render("Cash flow calendar")
	title += "  " + th.Subtle.Render(truncate(fmt.Sprintf("month %d/%d · %s · %s",
		c.monthIndex()+1, max(1, len(c.months)), window, account), max(10, width-len("Cash flow calendar")-4)))
	if !c.fetched {
		return []string{title}
	}

	netStyle := th.Positive
	if c.data.Net.IsNegative() {
		netStyle = th.Negative
	}
	totals := hstack(
		th.Subtle.Render("income ")+c.data.TotalIncome.Display(),
		th.Subtle.Render("expense ")+c.data.TotalExpense.Display(),
		th.Subtle.Render("net ")+netStyle.Render(calendarNetText(c.data.Net)),
		th.Subtle.Render(pluralise(len(c.data.Days), "active day", "active days")),
	)
	return []string{title, truncate(totals, width)}
}

// monthGrid draws the cursor's month as a Sunday-first grid of seven day cells
// per week, blank cells for the days outside the month, and a heavier separator
// where a billing cycle opens. It is clipped by the caller's box, not here.
func (c *Calendar) monthGrid() string {
	month := calendarMonthFirst(c.cursor)
	total := calendarDaysInMonth(month)
	leading := int(month.Weekday())

	lines := []string{c.weekdayHeader()}
	for week := range (leading + total + 6) / 7 {
		cells := make([]string, 0, 7)
		for weekday := range 7 {
			index := week*7 + weekday - leading
			if index < 0 || index >= total {
				cells = append(cells, " "+strings.Repeat(" ", calendarCellWidth))
				continue
			}
			day := time.Date(month.Year(), month.Month(), index+1, 0, 0, 0, 0, time.UTC)
			cells = append(cells, c.cellSeparator(day)+c.dayCell(day))
		}
		lines = append(lines, strings.Join(cells, ""))
	}
	return strings.Join(lines, "\n")
}

// weekdayHeader labels the grid's seven columns, one cell per column so the day
// cells line up under their weekday.
func (c *Calendar) weekdayHeader() string {
	th := c.ctx.Theme
	var b strings.Builder
	for _, label := range []string{"Su", "Mo", "Tu", "We", "Th", "Fr", "Sa"} {
		b.WriteString(th.Header.Render(pad(" "+label, calendarCellWidth+1)))
	}
	return b.String()
}

// cellSeparator is the one column between two day cells: a cycle boundary glyph
// where a statement period opens, a plain space everywhere else.
func (c *Calendar) cellSeparator(day time.Time) string {
	if c.cycleStarts[day.Format(api.DateLayout)] {
		return c.ctx.Theme.WarnText.Render("│")
	}
	return " "
}

// dayCell renders one day: its number, an intensity block scaled by the server's
// MaxAbsNet, and a diamond when the server attached a summary marker to the day.
// A day the API omitted stays muted and blank, which is how a gap reads.
func (c *Calendar) dayCell(day time.Time) string {
	th := c.ctx.Theme
	key := day.Format(api.DateLayout)
	entry, has := c.byDate[key]

	flag := " "
	if len(c.markers[key]) > 0 {
		flag = "◆"
	}

	block := "  "
	style := th.Subtle
	switch {
	case !has || entry.Count == 0:
		// Left blank: the API sends no day for a date without transactions.
	case entry.Net.IsNegative():
		block = strings.Repeat(calendarDeficit[calendarLevel(entry.Net, c.data.MaxAbsNet)], 2)
		style = th.Negative
	case entry.Net.IsZero():
		block = "··"
	default:
		block = strings.Repeat(calendarSurplus[calendarLevel(entry.Net, c.data.MaxAbsNet)], 2)
		style = th.Positive
	}

	cell := style.Render(fmt.Sprintf("%2d %s%s", day.Day(), block, flag))
	if day.Equal(c.cursor) {
		cell = th.RowSelected.Render(cell)
	}
	return cell
}

// legendLine states the scale and the glyph vocabulary, since the shading is
// the only cue a colour-blind or monochrome terminal has.
func (c *Calendar) legendLine(width int) string {
	th := c.ctx.Theme
	parts := []string{
		th.Subtle.Render("server max |net| " + c.data.MaxAbsNet.Display()),
		th.Positive.Render("▁▄█ surplus"),
		th.Negative.Render("░▒▓ deficit"),
		th.WarnText.Render("│ cycle start"),
		th.WarnText.Render("◆ marker"),
	}
	return truncate(strings.Join(parts, th.Subtle.Render(" · ")), width)
}

// overlayLines lists the server-computed overlays — billing cycles and summary
// markers — under the grid, bounded by the lines the box has left. These are not
// summed from the drawn days: the API computes them.
func (c *Calendar) overlayLines(width, budget int) []string {
	if budget <= 0 {
		return nil
	}
	th := c.ctx.Theme

	rows := make([]string, 0, len(c.data.Cycles)+len(c.data.Markers)+1)
	if len(c.data.Cycles) > 0 || len(c.data.Markers) > 0 {
		rows = append(rows, th.Header.Render("server-computed overlays"))
	}
	for _, cycle := range c.data.Cycles {
		rows = append(rows, th.Subtle.Render(fmt.Sprintf("  cycle  %-20s %-25s outstanding %s",
			truncate(cycle.Label, 20), formatRange(cycle.StartDate, cycle.EndDate), cycle.Outstanding.Display())))
	}
	for _, marker := range c.data.Markers {
		rows = append(rows, th.Subtle.Render(fmt.Sprintf("  marker %-10s %-18s %12s  %s",
			marker.Date, calendarMarkerKind(marker.Kind), marker.Amount.Display(), marker.Label)))
	}
	if len(rows) > budget {
		hidden := len(rows) - budget + 1
		rows = rows[:budget-1]
		rows = append(rows, th.Subtle.Render(fmt.Sprintf("  … %s", pluralise(hidden, "more overlay row", "more overlay rows"))))
	}
	return rows
}

// detailLine describes the cursor day, or says plainly that the server sent no
// figures for it.
func (c *Calendar) detailLine(width int) string {
	th := c.ctx.Theme
	key := c.cursor.Format(api.DateLayout)
	entry, has := c.byDate[key]
	if !has || entry.Count == 0 {
		return th.Subtle.Render(truncate(key+" · no transactions", width))
	}

	netStyle := th.Positive
	if entry.Net.IsNegative() {
		netStyle = th.Negative
	}
	line := hstack(
		key,
		"income "+entry.Income.Display(),
		"expense "+entry.Expense.Display(),
		"net "+netStyle.Render(calendarNetText(entry.Net)),
		pluralise(entry.Count, "transaction", "transactions"),
	)
	return truncate(line, width)
}

// calendarToday is the local calendar date carried as UTC midnight, so the
// cursor, the window bounds and the API's date strings all compare on the same
// basis.
func calendarToday() time.Time {
	now := time.Now()
	return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
}

// calendarMonthFirst is the first day of t's month, in the UTC basis every date
// in this screen uses.
func calendarMonthFirst(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
}

// calendarDaysInMonth reports how many days the month of t holds.
func calendarDaysInMonth(t time.Time) int {
	return calendarMonthFirst(t).AddDate(0, 1, -1).Day()
}

// calendarSameDayInMonth moves t into another month, clamping the day of month
// to the target's length.
func calendarSameDayInMonth(t, month time.Time) time.Time {
	day := min(t.Day(), calendarDaysInMonth(month))
	return time.Date(month.Year(), month.Month(), day, 0, 0, 0, 0, time.UTC)
}

// calendarParseDate reads the API's date-only layout, reporting whether the
// value was a date at all: an empty window bound is simply "not set".
func calendarParseDate(value string) (time.Time, bool) {
	t, err := time.Parse(api.DateLayout, strings.TrimSpace(value))
	return t, err == nil
}

// calendarLevel buckets a day's magnitude into the three-glyph scale, using the
// server's MaxAbsNet as the denominator — the API owns that reference and the
// TUI must not rescale it. ratioOf is the display-only conversion shared with
// the bar helpers.
func calendarLevel(net, maxAbs api.Amount) int {
	ratio := ratioOf(net, maxAbs.Float64())
	if ratio < 0 {
		ratio = -ratio
	}
	switch {
	case ratio > 0.67:
		return 2
	case ratio > 0.34:
		return 1
	default:
		return 0
	}
}

// calendarNetText renders a signed net figure. The API's Amount already carries
// its sign, so this only adds the "+" a surplus has earned.
func calendarNetText(a api.Amount) string {
	if a.IsZero() || a.IsNegative() {
		return a.Display()
	}
	return "+" + a.Display()
}

// calendarMarkerKind spells a marker kind the way the overlay list labels it.
func calendarMarkerKind(kind string) string {
	switch kind {
	case "balance":
		return "month-end balance"
	case "outstanding":
		return "cycle outstanding"
	default:
		return defaultTo(kind, "marker")
	}
}
