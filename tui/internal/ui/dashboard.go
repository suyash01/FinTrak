package ui

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/fintrak/client/api"
)

func init() {
	registerScreen(10, func(ctx *Ctx) Screen { return NewDashboard(ctx) })
}

// Message tags. The App forwards every message to every screen, so the tags are
// prefixed with this screen's name; a summary fetched for another screen can
// never be mistaken for this one's.
const (
	dashTagSummary = "dashboard.summary"
	dashTagWindow  = "dashboard.window"
)

// Statement-period framing. The server owns these bounds: it defaults to twelve
// cycles, refuses more than sixty, and answers 400 for GroupBy "billing_cycle"
// without an account that has a billing day.
const (
	dashGroupByCycle  = "billing_cycle"
	dashDefaultCycles = 12
	dashMaxCycles     = 60
)

// dashReportWidth is the width the full-breakdown overlay renders its sections
// at. The overlay scrolls, so unlike the main view nothing has to be dropped.
const dashReportWidth = 100

// Dashboard is the read-only overview: account and transaction counts, the
// window's income and expense, ranked category breakdowns with bars, the
// month-or-cycle trend, and the most recent transactions. Nothing here mutates
// anything, so it is the one screen that can be left open while the rest of the
// client is used to tighten the window it reports on.
//
// The headline stays pinned and the sections below it scroll, because the
// figures the screen exists for must survive a short terminal.
//
// currency is the screen's own choice of which currency to render in, taken
// from the window form. It is deliberately NOT part of the request: the server's
// ?currency= filter drops the other currencies from the payload, and a screen
// that had narrowed its own request could no longer name what it left out — the
// notice this screen's currencyLine exists to print would have nothing to print.
// So the request stays whole and the selection only chooses a key to read.
type Dashboard struct {
	ctx *Ctx

	filter   api.DashboardFilter
	currency string
	summary  api.DashboardSummary
	ready    bool

	// bodyOffset is the first section line the body pane shows, and paneHeight
	// is how many fit. View records the height on each render so the paging keys
	// move by exactly one screenful.
	bodyOffset int
	paneHeight int

	keys dashKeys
}

// dashKeys are the screen's bindings.
type dashKeys struct {
	Window    key.Binding
	Cycle     key.Binding
	More      key.Binding
	Fewer     key.Binding
	Breakdown key.Binding
	Scroll    key.Binding
	Top       key.Binding
	Bottom    key.Binding
}

func newDashKeys() dashKeys {
	return dashKeys{
		Window:    key.NewBinding(key.WithKeys("f"), key.WithHelp("f", "window")),
		Cycle:     key.NewBinding(key.WithKeys("c"), key.WithHelp("c", "billing-cycle view")),
		More:      key.NewBinding(key.WithKeys("+"), key.WithHelp("+", "more cycles")),
		Fewer:     key.NewBinding(key.WithKeys("-"), key.WithHelp("-", "fewer cycles")),
		Breakdown: key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "full breakdown")),
		Scroll: key.NewBinding(
			key.WithKeys("up", "down", "k", "j", "pgup", "pgdown"),
			key.WithHelp("↑/↓", "scroll sections"),
		),
		Top:    key.NewBinding(key.WithKeys("home"), key.WithHelp("home", "top")),
		Bottom: key.NewBinding(key.WithKeys("end"), key.WithHelp("end", "bottom")),
	}
}

// NewDashboard builds the overview screen. The filter starts empty, which is the
// server's own default window: everything, by calendar month.
func NewDashboard(ctx *Ctx) *Dashboard {
	return &Dashboard{ctx: ctx, keys: newDashKeys()}
}

// Title implements Screen.
func (d *Dashboard) Title() string { return "Dashboard" }

// Keys implements Screen.
func (d *Dashboard) Keys() []key.Binding {
	return []key.Binding{
		d.keys.Window, d.keys.Cycle, d.keys.More, d.keys.Fewer,
		d.keys.Breakdown, d.keys.Scroll,
	}
}

// CapturesText implements Screen: this screen has no inline text input.
func (d *Dashboard) CapturesText() bool { return false }

// Refresh implements Screen.
func (d *Dashboard) Refresh() tea.Cmd { return d.reload() }

// reload fetches the summary for the current filter and rewinds the body pane,
// whose length belongs to the data being replaced.
func (d *Dashboard) reload() tea.Cmd {
	d.bodyOffset = 0
	filter := d.request()
	return load(dashTagSummary, func(ctx context.Context) (api.DashboardSummary, error) {
		return d.ctx.Client.Summary(ctx, filter)
	})
}

// request returns the filter as it goes on the wire. The server ignores the date
// window while grouping by billing cycle, so those bounds are dropped in that
// mode rather than sent and silently discarded; they stay on the struct so
// switching back to the calendar view restores the user's window.
//
// WindowFilter carries a Currency of its own and it is left empty on purpose:
// see the Dashboard type's comment. d.currency is a rendering choice, and a
// request that narrowed to it would cost the screen its notice.
func (d *Dashboard) request() api.DashboardFilter {
	filter := d.filter
	if filter.GroupBy == dashGroupByCycle {
		filter.DateFrom, filter.DateTo = "", ""
	} else {
		filter.Cycles = 0
	}
	return filter
}

// Update implements Screen.
func (d *Dashboard) Update(msg tea.Msg) tea.Cmd {
	switch m := msg.(type) {
	case loaded[api.DashboardSummary]:
		if m.tag != dashTagSummary {
			break
		}
		if m.err != nil {
			d.ctx.Notify(LevelError, "%s", m.err)
			return nil
		}
		d.summary = m.data
		d.ready = true
		return nil

	case done:
		// This screen mutates nothing today, so nothing here produces a done
		// message; reloading anyway keeps a future action (saving a window, say)
		// from having to remember to refresh.
		if !strings.HasPrefix(m.tag, "dashboard.") {
			break
		}
		if m.err == nil {
			return d.reload()
		}

	case tea.KeyPressMsg:
		return d.handleKey(m)
	}
	return nil
}

// handleKey routes a key press.
func (d *Dashboard) handleKey(msg tea.KeyPressMsg) tea.Cmd {
	switch {
	case keyMatches(d.keys.Window, msg):
		d.openWindowForm()
		return nil
	case keyMatches(d.keys.Cycle, msg):
		return d.toggleCycle()
	case keyMatches(d.keys.More, msg):
		return d.stepCycles(1)
	case keyMatches(d.keys.Fewer, msg):
		return d.stepCycles(-1)
	case keyMatches(d.keys.Breakdown, msg):
		d.ctx.Open(NewInfo("Dashboard breakdown", d.breakdown()))
		return nil
	case keyMatches(d.keys.Scroll, msg):
		d.scrollBy(msg.String())
	case keyMatches(d.keys.Top, msg):
		d.bodyOffset = 0
	case keyMatches(d.keys.Bottom, msg):
		// A huge offset is clamped back to the last screenful at render time.
		d.bodyOffset = 1 << 20
	}
	return nil
}

// scrollBy moves the body pane for one navigation key. A page moves by the
// visible height, which View records on every render.
func (d *Dashboard) scrollBy(name string) {
	step := max(1, d.paneHeight)
	switch name {
	case "up", "k":
		d.bodyOffset--
	case "down", "j":
		d.bodyOffset++
	case "pgup":
		d.bodyOffset -= step
	case "pgdown":
		d.bodyOffset += step
	}
	d.bodyOffset = max(0, d.bodyOffset)
}

// toggleCycle switches between the calendar-month view and the statement-period
// view. A billing-cycle summary without an accountId, or for an account with no
// billing day, is a 400 the server refuses to guess at, so the switch is
// declined with an explanation instead of being sent and failed.
func (d *Dashboard) toggleCycle() tea.Cmd {
	if d.filter.GroupBy == dashGroupByCycle {
		d.filter.GroupBy = ""
		d.ctx.Notify(LevelInfo, "calendar-month view")
		return d.reload()
	}
	if d.filter.AccountID == "" {
		d.ctx.Notify(LevelError, "billing-cycle view needs an account — pick one with f")
		return nil
	}
	account, ok := d.ctx.Ref.Account(d.filter.AccountID)
	if !ok {
		d.ctx.Notify(LevelError, "billing-cycle view needs an account — pick one with f")
		return nil
	}
	if account.BillingDay == nil {
		d.ctx.Notify(LevelError, "%s has no billing day, so it has no statement periods", account.Name)
		return nil
	}
	d.filter.GroupBy = dashGroupByCycle
	d.ctx.Notify(LevelInfo, "billing-cycle view of %s", account.Name)
	return d.reload()
}

// stepCycles widens or narrows the statement-period window. Zero is how the
// request says "use your default", so a value that lands back on the default is
// stored as zero rather than pinning the number the server happens to use.
func (d *Dashboard) stepCycles(delta int) tea.Cmd {
	if d.filter.GroupBy != dashGroupByCycle {
		d.ctx.Notify(LevelInfo, "cycles apply to the billing-cycle view — press c")
		return nil
	}
	current := d.filter.Cycles
	if current == 0 {
		current = dashDefaultCycles
	}
	next := min(max(current+delta, 1), dashMaxCycles)
	if next == dashDefaultCycles {
		d.filter.Cycles = 0
	} else {
		d.filter.Cycles = next
	}
	d.ctx.Notify(LevelInfo, "showing %s", d.cycleWindow())
	return d.reload()
}

// openWindowForm edits the window the whole screen reports on: the inclusive
// date range, the account, the currency to read the figures in, and how many
// statement periods a billing-cycle view spans. Applying it is purely local, so
// the form closes itself and the reload is issued from here.
func (d *Dashboard) openWindowForm() {
	filter := d.filter
	cycles := ""
	if filter.Cycles > 0 {
		cycles = strconv.Itoa(filter.Cycles)
	}
	currency := SelectField("Currency", d.currency, d.ctx.Ref.CurrencyOptions(), false)
	currency.ClearLabel = "every currency"
	currency.Help = "which currency to read the figures in; the others are named, never added"
	fields := []Field{
		{Label: "Date from", Kind: FieldText, Value: filter.DateFrom, Width: 14, Validate: optionalDate},
		{Label: "Date to", Kind: FieldText, Value: filter.DateTo, Width: 14, Validate: optionalDate},
		SelectField("Account", filter.AccountID, d.ctx.Ref.AccountOptions(), false),
		currency,
		{Label: "Cycles", Kind: FieldText, Value: cycles, Width: 6, Validate: positiveInt,
			Help: "billing-cycle view only: 1-60, blank for the default of 12"},
	}
	grouped := filter.GroupBy == dashGroupByCycle
	d.ctx.Open(NewForm(dashTagWindow, "Dashboard window", fields, func(f *Form) tea.Cmd {
		d.filter.DateFrom = strings.TrimSpace(f.Value("Date from"))
		d.filter.DateTo = strings.TrimSpace(f.Value("Date to"))
		d.filter.AccountID = f.Value("Account")
		d.currency = f.Value("Currency")

		entered := f.IntValue("Cycles")
		switch {
		case entered > dashMaxCycles:
			entered = dashMaxCycles
			d.ctx.Notify(LevelInfo, "cycles capped at %d", dashMaxCycles)
		case entered < 0:
			entered = 0
		case entered == dashDefaultCycles:
			entered = 0
		}
		d.filter.Cycles = entered

		// A cycle view is defined by its account, so clearing the account drops
		// the screen back to the calendar view rather than leaving it in a mode
		// whose every request would be rejected.
		if grouped && d.filter.AccountID == "" {
			d.filter.GroupBy = ""
			d.ctx.Notify(LevelInfo, "no account selected — back to the calendar-month view")
		}
		f.Close()
		return d.reload()
	}))
}

// cycleWindow describes the statement-period span, naming the server default
// when the request leaves it unset.
func (d *Dashboard) cycleWindow() string {
	if d.filter.Cycles == 0 {
		return fmt.Sprintf("%d cycles (server default)", dashDefaultCycles)
	}
	return pluralise(d.filter.Cycles, "cycle", "cycles")
}

// View implements Screen.
func (d *Dashboard) View(width, height int) string {
	head := d.headLines(width)
	body := d.bodyLines(width)

	// One line is held back for the scroll indicator; the rest of the box is the
	// body pane, which is why the pane height is captured here for scrollBy.
	d.paneHeight = max(1, height-len(head)-1)
	d.bodyOffset = clampOffset(d.bodyOffset, d.bodyOffset, d.paneHeight, len(body))

	lines := make([]string, 0, height)
	lines = append(lines, head...)
	for i := d.bodyOffset; i < len(body) && len(lines) < height-1; i++ {
		lines = append(lines, body[i])
	}
	lines = append(lines, d.scrollLine(width, len(body)))
	return trimToBox(strings.Join(lines, "\n"), width, height)
}

// headLines renders the pinned headline: the framing line, the stat cards, and
// the note that explains where the net figure came from.
func (d *Dashboard) headLines(width int) []string {
	th := d.ctx.Theme
	return []string{
		d.frameLine(width),
		d.cardLine(width),
		th.Subtle.Render(truncate("net is the server's per-currency difference — this client never subtracts", width)),
		"",
	}
}

// frameLine names the view and the window it covers, so a filter left over from
// an earlier session is visible without opening the form, and the currency the
// figures are read in with what the response holds besides it.
func (d *Dashboard) frameLine(width int) string {
	th := d.ctx.Theme
	mode := "calendar-month view"
	if d.filter.GroupBy == dashGroupByCycle {
		mode = "billing-cycle view · " + d.cycleWindow()
	}
	parts := []string{mode}
	if d.filter.AccountID != "" {
		parts = append(parts, "account "+d.ctx.Ref.AccountName(d.filter.AccountID))
	}
	if d.filter.GroupBy != dashGroupByCycle && (d.filter.DateFrom != "" || d.filter.DateTo != "") {
		parts = append(parts, "dates "+defaultTo(d.filter.DateFrom, "…")+"…"+defaultTo(d.filter.DateTo, "…"))
	}
	if scope := d.currencyNotice(); scope != "" {
		parts = append(parts, scope)
	}
	if !d.ready {
		parts = append(parts, "loading…")
	}
	line := th.Title.Render("Dashboard")
	return line + "  " + th.Subtle.Render(truncate(strings.Join(parts, " · "), max(10, width-11)))
}

// currencyNotice names the currency on screen and the ones the response holds
// besides it. It comes from the response's own scope, so a window that spans a
// currency the account list never suggested is still reported.
func (d *Dashboard) currencyNotice() string {
	if !d.ready {
		return ""
	}
	return currencyScopeLabel(d.currency, d.summary.CurrencyScope)
}

// cardLine renders the headline counts and totals. Income, expense and net all
// go through currencyLine, so a window in two currencies is named rather than
// rendered as one number: the net is the server's own per-currency difference,
// which is the only subtraction anywhere near this figure.
func (d *Dashboard) cardLine(width int) string {
	th := d.ctx.Theme
	if !d.ready {
		return th.Subtle.Render("no summary loaded yet")
	}
	s := d.summary
	cards := []string{
		th.Subtle.Render("Accounts") + " " + strconv.Itoa(s.TotalAccounts),
		th.Subtle.Render("Transactions") + " " + strconv.Itoa(s.TotalTransactions),
		th.Subtle.Render("Income") + " " + th.Positive.Render(currencyLine(d.currency, s.TotalIncome)),
		th.Subtle.Render("Expense") + " " + th.Negative.Render(currencyLine(d.currency, s.TotalExpense)),
		th.Subtle.Render("Net") + " " + th.WarnText.Render(currencyLine(d.currency, s.TotalNet)),
	}
	return truncate(strings.Join(cards, "   "), width)
}

// bodyLines renders the scrolling sections: the two category breakdowns, the
// trend of the active framing, then the most recent transactions.
func (d *Dashboard) bodyLines(width int) []string {
	if !d.ready {
		return []string{d.ctx.Theme.Subtle.Render("loading the dashboard summary…")}
	}
	lines := d.categorySection("Spend by category", d.summary.ByCategory, width)
	lines = append(lines, "")
	lines = append(lines, d.categorySection("Income by category", d.summary.IncomeByCategory, width)...)
	lines = append(lines, "")
	lines = append(lines, d.trendSection(width)...)
	lines = append(lines, "")
	lines = append(lines, d.recentSection(width)...)
	return lines
}

// scrollLine reports the visible slice of the body when there is more of it than
// fits, which is the only cue that the sections continue below the fold.
func (d *Dashboard) scrollLine(width, total int) string {
	if total <= d.paneHeight {
		return ""
	}
	last := min(total, d.bodyOffset+d.paneHeight)
	return d.ctx.Theme.Subtle.Render(truncate(
		fmt.Sprintf("sections %d-%d of %d · ↑/↓ scroll", d.bodyOffset+1, last, total), width))
}

// categorySection renders one ranked breakdown with a bar per row. The API
// already returns these ordered by total descending and capped at fifteen, so
// the list is shown as served: the client never sorts by amount, because
// ordering two amounts would need the display-only float conversion that is
// reserved for bar widths. The bar is scaled by the currency on screen, so a
// quiet foreign category cannot flatten the one beside it.
func (d *Dashboard) categorySection(title string, spends []api.CategorySpend, width int) []string {
	th := d.ctx.Theme
	lines := []string{th.PanelTitle.Render(truncate(title, width))}
	if len(spends) == 0 {
		return append(lines, th.Subtle.Render("  none in this window"))
	}
	nameWidth, barWidth := dashCategoryWidths(width)
	largest := dashLargest(d.currency, spends)
	for _, spend := range spends {
		// An id with no joined name still has to render, so the shared helper
		// supplies the id and then the uncategorized label.
		name := categoryOr(spend.CategoryName, &spend.CategoryID)
		// The amount column is padded to the width the budget reserved for it, and
		// a per-currency total that outgrows it is allowed to: the bar is pushed
		// right and the view's box clips the rest, which is a cosmetic cost against
		// the alternative of hiding a currency the response reported.
		lead := fmt.Sprintf("%-*s %6d %s  ",
			nameWidth, truncate(name, nameWidth), spend.Count, padLeft(currencyLine(d.currency, spend.Total), 14))
		lines = append(lines, lead+bar(th, ratioOfScale(currencyScale(d.currency, spend.Total), largest), barWidth))
	}
	return lines
}

// trendSection renders the income/expense series of the active framing: one row
// per calendar month, or per statement period, where the API replaces
// MonthlyTrend with BillingCycleTrend and describes the period in progress.
func (d *Dashboard) trendSection(width int) []string {
	th := d.ctx.Theme
	if d.filter.GroupBy == dashGroupByCycle {
		lines := []string{th.PanelTitle.Render("Billing cycles")}
		if cycle := d.summary.CurrentCycle; cycle != nil {
			lines = append(lines, th.Subtle.Render(truncate(
				"  current "+cycle.Label+" · "+formatRange(cycle.StartDate, cycle.EndDate), width)))
		}
		trend := d.summary.BillingCycleTrend
		if len(trend) == 0 {
			return append(lines, th.Subtle.Render("  no statement periods in this window"))
		}
		lines = append(lines, trendHeader(th, width))
		for _, item := range trend {
			label := defaultTo(item.Label, formatRange(item.StartDate, item.EndDate))
			lines = append(lines, d.trendRow(th, label, item.Income, item.Expense, width))
		}
		return lines
	}
	lines := []string{th.PanelTitle.Render("Monthly trend")}
	trend := d.summary.MonthlyTrend
	if len(trend) == 0 {
		return append(lines, th.Subtle.Render("  no months in this window"))
	}
	lines = append(lines, trendHeader(th, width))
	for _, item := range trend {
		lines = append(lines, d.trendRow(th, defaultTo(item.Month, "?"), item.Income, item.Expense, width))
	}
	return lines
}

// recentSection lists the most recent transactions of the window — what the
// summary carries, not the whole ledger, which is what the Transactions screen
// is for.
func (d *Dashboard) recentSection(width int) []string {
	th := d.ctx.Theme
	lines := []string{th.PanelTitle.Render("Recent transactions")}
	recent := d.summary.RecentTransactions
	if len(recent) == 0 {
		return append(lines, th.Subtle.Render("  none in this window"))
	}
	descWidth, amountWidth := dashRecentWidths(width)
	lines = append(lines, th.Header.Render(fmt.Sprintf("%-10s %-*s %-14s %*s",
		"Date", descWidth, "Description", "Account", amountWidth, "Amount")))
	for _, row := range recent {
		account := defaultTo(row.AccountName, d.ctx.Ref.AccountName(row.AccountID))
		head := fmt.Sprintf("%-10s %-*s %-14s ",
			row.Date.Format(api.DateLayout),
			descWidth, truncate(row.Description, descWidth),
			truncate(account, 14))
		lines = append(lines, head+dashAmount(th, row, amountWidth))
	}
	return lines
}

// breakdown renders the overlay behind `enter`: the figures the headline cannot
// spell out, then the same sections at a comfortable width. The overlay scrolls,
// so this is where the full lists live.
func (d *Dashboard) breakdown() string {
	if !d.ready {
		return "no summary loaded yet"
	}
	s := d.summary
	var b strings.Builder
	if d.filter.GroupBy == dashGroupByCycle {
		fmt.Fprintf(&b, "billing-cycle view · %s\n", d.cycleWindow())
	} else {
		b.WriteString("calendar-month view\n")
	}
	if d.filter.AccountID != "" {
		fmt.Fprintf(&b, "Account  %s\n", d.ctx.Ref.AccountName(d.filter.AccountID))
	}
	if d.filter.GroupBy != dashGroupByCycle && (d.filter.DateFrom != "" || d.filter.DateTo != "") {
		fmt.Fprintf(&b, "Dates    %s…%s\n", defaultTo(d.filter.DateFrom, "…"), defaultTo(d.filter.DateTo, "…"))
	}
	if scope := d.currencyNotice(); scope != "" {
		fmt.Fprintf(&b, "Currency %s\n", scope)
	}
	fmt.Fprintf(&b, "Accounts %d · Transactions %d\n", s.TotalAccounts, s.TotalTransactions)
	fmt.Fprintf(&b, "Income   %s · Expense %s\n", currencyLine(d.currency, s.TotalIncome), currencyLine(d.currency, s.TotalExpense))
	fmt.Fprintf(&b, "Net      %s — the server's per-currency difference, not a subtraction made here\n", currencyLine(d.currency, s.TotalNet))
	if cycle := s.CurrentCycle; cycle != nil {
		fmt.Fprintf(&b, "Current cycle %s · %s\n", cycle.Label, formatRange(cycle.StartDate, cycle.EndDate))
	}
	b.WriteString("\n")
	b.WriteString(strings.Join(d.bodyLines(dashReportWidth), "\n"))
	return b.String()
}

// dashCategoryWidths splits a category line between its label and its bar: the
// label takes what is left after the count, the amount and a usable bar, capped
// so a wide terminal does not strand the amount in the middle of the screen.
func dashCategoryWidths(width int) (name, barWidth int) {
	const count, amount, gaps, minBar = 6, 14, 3, 8
	name = min(max(width-count-amount-gaps-minBar, 10), 34)
	barWidth = max(minBar, width-name-count-amount-gaps)
	return name, barWidth
}

// dashLargest returns the largest total of a breakdown, which is the bar
// denominator. It is the currency on screen's own figure, read through the
// display-only float conversion and never shown: it only ever scales bars. A
// breakdown the currency on screen never touched has no largest, which leaves
// every bar in it flat rather than sized against a foreign account.
func dashLargest(currency string, spends []api.CategorySpend) float64 {
	largest := 0.0
	for _, spend := range spends {
		if value := currencyScale(currency, spend.Total); value > largest {
			largest = value
		}
	}
	return largest
}

// dashTrendWidths splits a trend line between its period label and the two money
// columns.
func dashTrendWidths(width int) (label, amount int) {
	const gaps, minAmount = 4, 12
	label = min(max(width-2*minAmount-gaps, 8), 24)
	amount = max(minAmount, (width-label-gaps)/2)
	return label, amount
}

// trendHeader labels the trend's two money columns.
func trendHeader(th Theme, width int) string {
	labelWidth, amountWidth := dashTrendWidths(width)
	return th.Header.Render(fmt.Sprintf("%-*s %*s   %*s",
		labelWidth, "Period", amountWidth, "Income", amountWidth, "Expense"))
}

// trendRow renders one period's income and expense. The amounts are padded
// before they are coloured, so the escape codes cannot skew the columns, and
// both go through currencyLine so a period the currency on screen never touched
// says so instead of borrowing a neighbour's number.
func (d *Dashboard) trendRow(th Theme, label string, income, expense api.CurrencyAmounts, width int) string {
	labelWidth, amountWidth := dashTrendWidths(width)
	return fmt.Sprintf("%-*s ", labelWidth, truncate(label, labelWidth)) +
		th.Positive.Render(padLeft(currencyLine(d.currency, income), amountWidth)) + "   " +
		th.Negative.Render(padLeft(currencyLine(d.currency, expense), amountWidth))
}

// dashRecentWidths splits a recent-transaction line: the date, account and
// amount are fixed, and the description takes the remainder.
func dashRecentWidths(width int) (desc, amount int) {
	const date, account, gaps = 10, 14, 3
	amount = 14
	desc = max(10, width-date-account-gaps-amount)
	return desc, amount
}

// dashAmount renders a ledger amount with the direction the type implies, in the
// colours amountRole assigns, padded to a fixed width first so the styling
// cannot shift the column.
func dashAmount(th Theme, row api.Transaction, width int) string {
	text := fmt.Sprintf("%*s", width, signedAmount(row.Amount, row.Type))
	switch amountRole(row.Type) {
	case RolePositive:
		return th.Positive.Render(text)
	case RoleNegative:
		return th.Negative.Render(text)
	}
	return text
}
