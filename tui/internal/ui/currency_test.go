package ui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/fintrak/client/api"
)

// testCtx is the screen context these tests need: a theme to render with, a
// reference cache with two currencies' worth of accounts, and the two callbacks
// a screen calls when it has no App behind it. None of these tests make a
// request — they are unit tests on the text-building and measuring paths.
func testCtx() *Ctx {
	return &Ctx{
		Ref: &RefData{Accounts: []api.Account{
			{ID: "a1", Name: "Everyday", Currency: "USD"},
			{ID: "a2", Name: "Salary", Currency: "INR"},
		}},
		Theme:  DefaultTheme(),
		Notify: func(Level, string, ...any) {},
		Open:   func(Modal) {},
	}
}

// TestCurrencyLineRefusesToChooseACurrency is the TUI's half of the change: a
// two-currency map must never render as a single number, because a terminal has
// no room for two and picking one silently is the bug.
func TestCurrencyLineRefusesToChooseACurrency(t *testing.T) {
	single := currencyLine("INR", api.CurrencyAmounts{"INR": "5000.00"})
	if !strings.Contains(single, "INR") || !strings.Contains(single, "5,000.00") {
		t.Errorf("single-currency line = %q, want the code and the formatted amount", single)
	}

	// A currency that is not the selected one is named, not summed in.
	other := currencyLine("INR", api.CurrencyAmounts{"INR": "5000.00", "USD": "120.00"})
	if !strings.Contains(other, "USD") || !strings.Contains(other, "120.00") {
		t.Errorf("line = %q, want the unselected currency named", other)
	}
	if strings.Contains(other, "5,120") {
		t.Errorf("line = %q added two currencies together", other)
	}

	// Nothing to show is said, not rendered as a zero of some currency.
	empty := currencyLine("INR", api.CurrencyAmounts{})
	if empty == "" {
		t.Error("an empty map rendered as nothing at all")
	}
}

// TestCurrencyLineNamesEveryUnselectedCurrency keeps the notice complete: a
// window with three currencies must mention the two the user is not looking at.
func TestCurrencyLineNamesEveryUnselectedCurrency(t *testing.T) {
	got := currencyLine("INR", api.CurrencyAmounts{"INR": "1.00", "USD": "2.00", "EUR": "3.00"})

	for _, want := range []string{"EUR", "3.00", "USD", "2.00"} {
		if !strings.Contains(got, want) {
			t.Errorf("line = %q, missing %q", got, want)
		}
	}
}

// TestCurrencyLineWithNoSelectionShowsASingleCurrency covers the state every
// screen starts in. Nothing is selected, so no currency has been left out, and
// the single currency the response holds must be shown — the alternative the
// rule above rejects for a selected currency would claim "no transactions in "
// with nothing after it, which is a statement about a currency that does not
// exist.
func TestCurrencyLineWithNoSelectionShowsASingleCurrency(t *testing.T) {
	got := currencyLine("", api.CurrencyAmounts{"INR": "5000.00"})
	if strings.Contains(got, "no transactions") {
		t.Errorf("line = %q, want the single currency's figure: nothing was selected, so none was left out", got)
	}
	if !strings.Contains(got, "INR") || !strings.Contains(got, "5,000.00") {
		t.Errorf("line = %q, want the code and the formatted amount", got)
	}

	// Two currencies and no selection is still a refusal, not a pick.
	two := currencyLine("", api.CurrencyAmounts{"INR": "5000.00", "USD": "120.00"})
	if !strings.Contains(two, "not combined") {
		t.Errorf("line = %q, want the multi-currency refusal", two)
	}
}

// TestCurrencyLineWithASelectionAndAForeignOnlyResponse covers a currency picked
// over a window that has nothing in it: the response holds exactly one currency
// and it is not the one on screen.
func TestCurrencyLineWithASelectionAndAForeignOnlyResponse(t *testing.T) {
	got := currencyLine("INR", api.CurrencyAmounts{"USD": "120.00"})
	if !strings.Contains(got, "no transactions in INR") {
		t.Errorf("line = %q, want the missing currency named rather than a USD figure in the INR column", got)
	}
	if strings.Contains(got, "120") {
		t.Errorf("line = %q rendered the foreign amount", got)
	}
}

// TestCurrencyScaleReadsTheSelectedCurrencyOnly is the denominator half of the
// change, and the guard the bar and heatmap widths depend on: a bar is never
// sized by a number from a currency the user is not looking at.
//
// The foreign figure here is a million times the domestic one, so any bar
// denominator that reached across the currencies would collapse the INR bar to
// nothing at all.
func TestCurrencyScaleReadsTheSelectedCurrencyOnly(t *testing.T) {
	amounts := api.CurrencyAmounts{"INR": "500.00", "USD": "500000.00"}

	if got := currencyScale("INR", amounts); got != 500 {
		t.Errorf("currencyScale(INR) = %v, want 500: the USD entry is a different currency", got)
	}
	if got := currencyScale("EUR", amounts); got != 0 {
		t.Errorf("currencyScale(EUR) = %v, want 0: a currency the response never touched has no magnitude", got)
	}
	// Nothing selected: a scale is still needed and the largest single currency
	// supplies it, which is a maximum over magnitudes and not a sum.
	if got := currencyScale("", amounts); got != 500000 {
		t.Errorf("currencyScale(unselected) = %v, want the largest single magnitude 500000", got)
	}
	// A negative figure sizes by its magnitude, as the bar helpers have always
	// read it.
	if got := currencyScale("INR", api.CurrencyAmounts{"INR": "-500.00"}); got != 500 {
		t.Errorf("currencyScale(INR, negative) = %v, want 500", got)
	}
}

// TestCurrencyValueRefusesWithoutASelection pins that a figure is only reached
// through one sanctioned route, and that the route declines when the map holds
// no single answer: a day in two currencies with nothing selected has no one
// sign and no one value, and inventing either is the bug.
func TestCurrencyValueRefusesWithoutASelection(t *testing.T) {
	two := api.CurrencyAmounts{"INR": "500.00", "USD": "5.00"}
	if value, ok := currencyValue("", two); ok {
		t.Errorf("currencyValue(unselected, two currencies) = %q, ok — want a refusal", value)
	}

	if value, ok := currencyValue("USD", two); !ok || value != "5.00" {
		t.Errorf("currencyValue(USD) = %q, %v, want 5.00, true", value, ok)
	}
	if _, ok := currencyValue("EUR", two); ok {
		t.Error("currencyValue(EUR) reported a value for a currency the map does not hold")
	}
	// A present zero and an absent key are the same value to a bar and a glyph,
	// but only the second is absent, and the caller says which it got.
	zero := api.CurrencyAmounts{"INR": "0.00", "USD": "5.00"}
	if _, ok := currencyValue("INR", zero); !ok {
		t.Error("a present zero read as an absent key")
	}
}

// TestCurrencySignOfIsTriState pins the sign rule the heatmap and the timeline
// strip colour by. The three-valued answer is the point: a bool could say "not
// negative" for both "positive" and "no sign", and every caller read the second
// as the first, which is how a day that spent in dollars and earned in rupees
// drew as a green surplus.
func TestCurrencySignOfIsTriState(t *testing.T) {
	mixed := api.CurrencyAmounts{"INR": "-500.00", "USD": "5.00"}

	// The defect: one way down and one way up is not "positive".
	if got := currencySignOf("", mixed); got != signNone {
		t.Errorf("currencySignOf(unselected, mixed) = %v, want signNone", got)
	}
	// A selection resolves it, and only for the currency the user chose.
	if got := currencySignOf("INR", mixed); got != signNegative {
		t.Errorf("currencySignOf(INR, mixed) = %v, want signNegative", got)
	}
	if got := currencySignOf("USD", mixed); got != signPositive {
		t.Errorf("currencySignOf(USD, mixed) = %v, want signPositive", got)
	}

	// Agreement across the currencies is what earns a sign with nothing selected.
	both := api.CurrencyAmounts{"INR": "-500.00", "USD": "-5.00"}
	if got := currencySignOf("", both); got != signNegative {
		t.Errorf("currencySignOf(unselected, all negative) = %v, want signNegative", got)
	}
	bothUp := api.CurrencyAmounts{"INR": "500.00", "USD": "5.00"}
	if got := currencySignOf("", bothUp); got != signPositive {
		t.Errorf("currencySignOf(unselected, all positive) = %v, want signPositive", got)
	}

	// Neither an empty map, a present zero, nor an absent key is a surplus.
	for name, amounts := range map[string]api.CurrencyAmounts{
		"empty":  {},
		"nil":    nil,
		"zero":   {"INR": "0.00"},
		"absent": {"INR": "0.00", "USD": "-5.00"},
	} {
		if got := currencySignOf("", amounts); got != signNone {
			t.Errorf("currencySignOf(unselected, %s) = %v, want signNone", name, got)
		}
	}
	// A currency on screen the response never held has no sign to read.
	if got := currencySignOf("EUR", bothUp); got != signNone {
		t.Errorf("currencySignOf(EUR, never held) = %v, want signNone", got)
	}
}

// TestASignWithNoSignIsNeverDrawnAsASurplus is the render-level half of the
// tri-state fix, and it exists because the helper test structurally cannot catch
// this. Asserting that IsNegative returns false for a mixed map was already true
// before the fix and was never the bug: the bug was in the four callers that read
// that false as "positive". So this drives the actual rendering and looks for the
// thing a reader would see.
//
// The day's net here is income in rupees against a card spend in dollars — the
// exact shape that used to draw a full-intensity green surplus block.
func TestASignWithNoSignIsNeverDrawnAsASurplus(t *testing.T) {
	day := time.Date(2026, 3, 14, 0, 0, 0, 0, time.UTC)
	mixed := api.CurrencyAmounts{"INR": "500.00", "USD": "-500.00"}

	t.Run("calendar cell", func(t *testing.T) {
		c := NewCalendar(testCtx())
		c.fetched = true
		c.cursor = day
		c.data = api.CashFlowCalendar{
			Days:     []api.CashFlowCalendarDay{{Date: "2026-03-14", Net: mixed, Count: 2}},
			MaxAbsNet: api.CurrencyAmounts{"INR": "1000.00", "USD": "1000.00"},
		}
		c.index()
		c.buildMonths()

		cell := c.dayCell(day)
		// Checked for surplus glyphs rather than for a colour: the cell is styled
		// as a whole, so the escape codes wrap the number and the block together
		// and there is no per-block colour to match on.
		for _, glyph := range calendarSurplus {
			if strings.Contains(cell, glyph) {
				t.Errorf("cell = %q, want no surplus block: the day's net has no single sign", cell)
			}
		}
		// The flat marker, not a blank and not a surplus: the day happened, and
		// the screen knows it, it just cannot say which way it went.
		if !strings.Contains(cell, "··") {
			t.Errorf("cell = %q, want the flat marker for a day with no sign", cell)
		}
		// A real deficit still draws its hatched block, and a real surplus a solid
		// one, so the assertions above are not just a cell that stopped drawing
		// anything. The screen indexes the days into its own map, so each case
		// replaces the payload and re-indexes rather than editing the slice the
		// lookup already copied out of.
		c.data.Days = []api.CashFlowCalendarDay{{Date: "2026-03-14", Net: api.CurrencyAmounts{"INR": "-500.00"}, Count: 1}}
		c.index()
		if deficit := c.dayCell(day); !strings.Contains(deficit, calendarDeficit[1]) {
			t.Errorf("cell = %q, want a deficit block for a real INR deficit", deficit)
		}
		c.data.Days = []api.CashFlowCalendarDay{{Date: "2026-03-14", Net: api.CurrencyAmounts{"INR": "500.00"}, Count: 1}}
		c.index()
		if up := c.dayCell(day); !strings.Contains(up, calendarSurplus[1]) {
			t.Errorf("cell = %q, want a surplus block for a real INR surplus", up)
		}
	})

	t.Run("calendar header", func(t *testing.T) {
		c := NewCalendar(testCtx())
		c.fetched = true
		c.data = api.CashFlowCalendar{Net: mixed}
		c.buildMonths()

		header := strings.Join(c.header(200), "\n")
		// The exact string the header renders, so the match below is on the
		// style the net was actually drawn in rather than on a substring the
		// renderer never wraps whole. The label is styled separately from the
		// amount, so it is the amount alone that carries the sign's colour.
		net := currencyNetText("", mixed)
		if !strings.Contains(header, DefaultTheme().Subtle.Render(net)) {
			t.Errorf("header = %q, want the net muted: it points both ways", header)
		}
		if strings.Contains(header, DefaultTheme().Positive.Render(net)) {
			t.Errorf("header = %q, want no positive styling on a figure with no sign", header)
		}
		// The surplus colour on a real figure still shows, so the above is not
		// just a header that stopped styling anything.
		c.data.Net = api.CurrencyAmounts{"INR": "500.00"}
		up := strings.Join(c.header(200), "\n")
		if !strings.Contains(up, DefaultTheme().Positive.Render(currencyNetText("", c.data.Net))) {
			t.Errorf("header = %q, want a single-currency surplus styled positive", up)
		}
	})

	t.Run("money-flow period", func(t *testing.T) {
		m := NewMoneyFlow(testCtx())
		period := api.MoneyFlowTimelinePeriod{Key: "2026-03", Label: "March", Net: mixed}
		line := m.periodLine(period, 0, 8, 0, false)

		net := currencyNetText("", mixed)
		if !strings.Contains(line, net) {
			t.Fatalf("period line = %q, want the net rendered as %q", line, net)
		}
		if !strings.Contains(line, DefaultTheme().Subtle.Render(net)) {
			t.Errorf("period line = %q, want the net muted: it points both ways", line)
		}
	})
}

// TestMixedSignStillRendersWhenACurrencyIsSelected is the other half: a selection
// resolves a mixed figure for the currency chosen, and it must keep doing so, or
// the tri-state fix would have cost every single-currency user their sign.
func TestMixedSignStillRendersWhenACurrencyIsSelected(t *testing.T) {
	mixed := api.CurrencyAmounts{"INR": "500.00", "USD": "-500.00"}

	day := time.Date(2026, 3, 14, 0, 0, 0, 0, time.UTC)
	c := NewCalendar(testCtx())
	c.fetched = true
	c.cursor = day
	c.currency = "INR"
	c.data = api.CashFlowCalendar{
		Days:     []api.CashFlowCalendarDay{{Date: "2026-03-14", Net: mixed, Count: 2}},
		MaxAbsNet: api.CurrencyAmounts{"INR": "1000.00"},
	}
	c.index()
	c.buildMonths()

	if cell := c.dayCell(day); !strings.Contains(cell, calendarSurplus[1]) {
		t.Errorf("cell = %q, want the INR surplus block: INR selected resolves the sign", cell)
	}
}

// TestCurrencyScopeLabelSaysWhatIsNotShown is the frame-line notice: the screen
// must be able to say which currency it is reading and which the response holds
// besides, because the figures beside it can only show one.
func TestCurrencyScopeLabelSaysWhatIsNotShown(t *testing.T) {
	scope := api.CurrencyScope{Currencies: []string{"EUR", "INR", "USD"}}

	one := currencyScopeLabel("INR", scope)
	for _, want := range []string{"INR", "EUR", "USD", "not showing"} {
		if !strings.Contains(one, want) {
			t.Errorf("label = %q, missing %q", one, want)
		}
	}
	// Nothing is chosen, so the label has to say the figures were never combined
	// and that the bars are scaled by the fallback — the clause that discloses
	// dashLargest, stageMax and the strip's busiest all using the largest single
	// magnitude when nothing is selected.
	none := currencyScopeLabel("", scope)
	if !strings.Contains(none, "no currency selected") || !strings.Contains(none, "EUR") {
		t.Errorf("label = %q, want the unchosen state named with the currencies", none)
	}
	if !strings.Contains(none, "largest") {
		t.Errorf("label = %q, want the bar-scale fallback disclosed", none)
	}
	// One currency and no choice: there is nothing to report, so the frame line
	// is not filled with a notice about a decision nobody made.
	if got := currencyScopeLabel("", api.CurrencyScope{Currencies: []string{"INR"}}); got != "" {
		t.Errorf("label = %q, want nothing said about a single-currency window", got)
	}
	if got := currencyScopeLabel("INR", api.CurrencyScope{Currencies: []string{"INR"}}); got != "INR only" {
		t.Errorf("label = %q, want %q", got, "INR only")
	}
}

// TestCurrencyScopeLabelDoesNotClaimACurrencyTheResponseLacks keeps the label and
// the figures from disagreeing about one state. With INR selected over a window
// that only holds USD, currencyLine says "no transactions in INR" — so a label
// reading "INR only — not showing USD" would claim the report *is* in INR while
// the numbers beside it say it holds nothing at all. The label has to be the one
// that gives way.
func TestCurrencyScopeLabelDoesNotClaimACurrencyTheResponseLacks(t *testing.T) {
	scope := api.CurrencyScope{Currencies: []string{"USD"}}

	only := currencyScopeLabel("INR", scope)
	if strings.Contains(only, "INR only") {
		t.Errorf("label = %q, want it not to claim the report is in INR", only)
	}
	if !strings.Contains(only, "no INR") {
		t.Errorf("label = %q, want the absent currency named", only)
	}
	// What the report does hold is the whole of the answer.
	if !strings.Contains(only, "USD") {
		t.Errorf("label = %q, want the currency the response holds named", only)
	}
	// Nothing held at all is the degenerate case of the same thing.
	if got := currencyScopeLabel("INR", api.CurrencyScope{}); !strings.Contains(got, "no INR") {
		t.Errorf("label = %q, want the absent currency named for an empty response", got)
	}
	// And the figure and the label now agree, which is the point.
	figures := currencyLine("INR", api.CurrencyAmounts{"USD": "120.00"})
	if !strings.Contains(figures, "no transactions in INR") {
		t.Errorf("figures = %q, want the same absence currencyLine has always reported", figures)
	}

	// An empty code in the scope is not a currency, and with nothing selected it
	// must not be mistaken for the selection being satisfied.
	if got := currencyScopeLabel("", api.CurrencyScope{Currencies: []string{""}}); got != "" {
		t.Errorf("label = %q, want nothing said for a scope holding no currency", got)
	}
	if got := currencyScopeLabel("INR", api.CurrencyScope{Currencies: []string{"", "INR"}}); got != "INR only" {
		t.Errorf("label = %q, want %q: the empty code is not a currency in scope", got, "INR only")
	}
}

// TestLinkNotesQualifyTheCycleByItsKeyCount covers the two note helpers on the
// fourth screen, which is outside the brief's file list and therefore uncovered
// until now. The single-currency reading of a cycle's net is that it circulates
// the loop; the multi-currency reading is that nothing circulates a loop whose
// legs differ. Printing the first for a cycle that is the second is the same
// false statement as every other finding in this pass.
func TestLinkNotesQualifyTheCycleByItsKeyCount(t *testing.T) {
	single := api.CurrencyAmounts{"INR": "5000.00"}
	multi := api.CurrencyAmounts{"INR": "5000.00", "USD": "40.00"}

	if got := linkNetNote(single); !strings.Contains(got, "what actually circulates") {
		t.Errorf("net note = %q, want the single-currency reading", got)
	}
	if got := linkNetNote(multi); !strings.Contains(got, "per currency") {
		t.Errorf("net note = %q, want the multi-currency reading", got)
	}
	// A multi-currency note must not also claim a circulation figure.
	if strings.Contains(linkNetNote(multi), "what actually circulates") {
		t.Errorf("net note = %q, want it not to claim anything circulates", linkNetNote(multi))
	}

	if got := linkCircularNote(single); !strings.Contains(got, "flows back") {
		t.Errorf("circular note = %q, want the single-currency reading", got)
	}
	if got := linkCircularNote(multi); !strings.Contains(got, "never one combined figure") {
		t.Errorf("circular note = %q, want the multi-currency reading", got)
	}
	// An empty report is not a multi-currency one, so it must not be told it is
	// a set of per-currency figures.
	if got := linkNetNote(nil); !strings.Contains(got, "what actually circulates") {
		t.Errorf("net note for an empty report = %q, want the neutral reading", got)
	}
}

// TestTheGraphHeaderShowsTheServersNet covers the money-flow net decision: the
// response carries one (MoneyFlowGraph.TotalNet, the server's per-currency
// difference), so the header states it rather than describing the graph as
// having no net — the dashboard already shows the same figure, and two screens
// disagreeing about whether the payload carries a net is the defect.
func TestTheGraphHeaderShowsTheServersNet(t *testing.T) {
	m := NewMoneyFlow(testCtx())
	m.loaded = true
	m.graph = api.MoneyFlowGraph{
		TotalIncome:  api.CurrencyAmounts{"INR": "80000.00"},
		TotalExpense: api.CurrencyAmounts{"INR": "20000.00"},
		TotalNet:     api.CurrencyAmounts{"INR": "60000.00"},
	}

	header := strings.Join(m.headerLines(200), "\n")
	if !strings.Contains(header, "net +60,000.00") {
		t.Errorf("header = %q, want the server's net shown", header)
	}
	// A mixed-sign net is muted, and its text names the currencies.
	m.graph.TotalNet = api.CurrencyAmounts{"INR": "60000.00", "USD": "-100.00"}
	mixed := strings.Join(m.headerLines(200), "\n")
	if !strings.Contains(mixed, "2 currencies") {
		t.Errorf("header = %q, want the multi-currency net named", mixed)
	}
	if strings.Contains(mixed, DefaultTheme().Positive.Render(currencyNetText("", m.graph.TotalNet))) {
		t.Errorf("header = %q, want the mixed-sign net muted", mixed)
	}
}

// TestCalendarLevelIsFlatWithoutADenominatorInTheSelectedCurrency is the
// zero-denominator case the per-currency MaxAbsNet made possible. The server's
// Add skips a zero contribution, so a currency whose every day nets exactly zero
// has no key in MaxAbsNet at all — the denominator is absent, not zero — and a
// day that does have one must still come out flat instead of dividing by
// nothing.
//
// The second case is the one that would be missed: a large USD day beside a
// calendar whose INR scale is missing must not borrow the USD magnitude.
func TestCalendarLevelIsFlatWithoutADenominatorInTheSelectedCurrency(t *testing.T) {
	c := &Calendar{ctx: testCtx(), currency: "INR"}

	// No MaxAbsNet at all: every currency netted exactly zero.
	c.data = api.CashFlowCalendar{MaxAbsNet: api.CurrencyAmounts{}}
	if got := c.level(api.CurrencyAmounts{"INR": "-900.00"}); got != 0 {
		t.Errorf("level with no denominator = %d, want 0 (a flat cell)", got)
	}

	// MaxAbsNet holds only the currency nobody is looking at.
	c.data = api.CashFlowCalendar{MaxAbsNet: api.CurrencyAmounts{"USD": "100.00"}}
	if got := c.level(api.CurrencyAmounts{"INR": "-900.00"}); got != 0 {
		t.Errorf("level = %d, want 0: the denominator was a USD figure beside an INR day", got)
	}
	// With the currency on screen present, the same day does get a level — so the
	// guard above is the denominator and not a screen that is always flat.
	c.data = api.CashFlowCalendar{MaxAbsNet: api.CurrencyAmounts{"INR": "1000.00", "USD": "100.00"}}
	if got := c.level(api.CurrencyAmounts{"INR": "-900.00"}); got != 2 {
		t.Errorf("level = %d, want 2: 0.9 of the INR maximum is the top block", got)
	}
}

// TestMoneyFlowStageMaxIgnoresTheCurrencyOnScreenDoesNotHave pins the graph's
// denominator. A stage whose biggest node is a foreign account would otherwise
// shrink every domestic bar to a hairline, and one that has no domestic node at
// all would size them against a currency the user never asked for.
func TestMoneyFlowStageMaxIgnoresTheCurrencyOnScreenDoesNotHave(t *testing.T) {
	m := NewMoneyFlow(testCtx())
	m.currency = "INR"
	m.nodes = []api.MoneyFlowNode{
		{ID: "income:1", Kind: "income", Name: "Salary", Total: api.CurrencyAmounts{"INR": "80000.00"}},
		{ID: "income:2", Kind: "income", Name: "Consulting", Total: api.CurrencyAmounts{"USD": "9000.00"}},
		{ID: "account:1", Kind: "account", Name: "Card", Total: api.CurrencyAmounts{"USD": "900000.00"}},
	}

	if got := m.stageMax("income"); got != 80000 {
		t.Errorf("stageMax(income) = %v, want 80000: the USD node is not a bar the INR view draws", got)
	}
	// A stage with nothing in the currency on screen has no maximum, and every
	// bar in it is flat rather than sized against a currency nobody selected.
	if got := m.stageMax("account"); got != 0 {
		t.Errorf("stageMax(account) = %v, want 0", got)
	}
}

// TestMoneyFlowSplitBarHasNoSegmentForAnotherCurrency pins the same rule on the
// timeline strip: a period with a large USD figure and no INR figure gets no
// income segment, because a bar denominated in a currency the user is not
// looking at is the mistake this whole change exists to stop.
func TestMoneyFlowSplitBarHasNoSegmentForAnotherCurrency(t *testing.T) {
	th := DefaultTheme()
	inr := mfSplitBar(th, "INR", api.CurrencyAmounts{"INR": "500.00"}, nil, 1000, 20)
	if !strings.Contains(inr, "█") {
		t.Errorf("split bar = %q, want an income segment for the INR half", inr)
	}

	foreign := mfSplitBar(th, "INR", api.CurrencyAmounts{"USD": "500.00"}, nil, 1000, 20)
	if strings.Contains(foreign, "█") {
		t.Errorf("split bar = %q, want no income segment: the period holds no INR", foreign)
	}
}

// TestDashboardCardLineRefusesToCombineCurrencies drives the screen rather than
// the helper, so the substitution is pinned where the user reads it. Two
// accounts in two currencies must leave the cards naming both, and the net card
// must carry the server's own per-currency difference rather than a claim that
// the client never received one.
func TestDashboardCardLineRefusesToCombineCurrencies(t *testing.T) {
	d := NewDashboard(testCtx())
	d.ready = true
	d.summary = api.DashboardSummary{
		TotalAccounts: 2, TotalTransactions: 9,
		TotalIncome:  api.CurrencyAmounts{"INR": "80000.00", "USD": "900.00"},
		TotalExpense: api.CurrencyAmounts{"INR": "20000.00", "USD": "300.00"},
		TotalNet:     api.CurrencyAmounts{"INR": "60000.00", "USD": "600.00"},
	}

	cards := d.cardLine(200)
	for _, want := range []string{"INR 80,000.00", "USD 900.00", "not combined"} {
		if !strings.Contains(cards, want) {
			t.Errorf("cards = %q, missing %q", cards, want)
		}
	}
	if strings.Contains(cards, "80900") || strings.Contains(cards, "20,300") {
		t.Errorf("cards = %q added two currencies together", cards)
	}
	if !strings.Contains(cards, "Net") {
		t.Errorf("cards = %q, want the server's net shown", cards)
	}
}

// TestDashboardCurrencySelectionSizesTheBarsNotTheFigures states the design the
// screens settled on, because the two are easy to confuse: the *text* of a
// per-currency figure never narrows — it always names every currency the
// response holds — while the selection chooses which currency the bars and the
// heatmap are scaled in, and the frame line says so. A selection that narrowed
// the text would be a currency picked on the user's behalf, and the one figure
// it dropped would be the one they could not see.
func TestDashboardCurrencySelectionSizesTheBarsNotTheFigures(t *testing.T) {
	spends := []api.CategorySpend{
		{CategoryName: "Rent", Total: api.CurrencyAmounts{"INR": "20000.00"}},
		{CategoryName: "Travel", Total: api.CurrencyAmounts{"USD": "900.00"}},
		// A far larger foreign figure, which is the case that would swallow the
		// INR bar whole if the denominator could reach across currencies.
		{CategoryName: "Hardware", Total: api.CurrencyAmounts{"USD": "500000.00"}},
	}

	// No selection: the scale is the largest single currency, which is the
	// foreign one — there is nothing to prefer and nothing to add.
	if got := dashLargest("", spends); got != 500000 {
		t.Errorf("dashLargest(unselected) = %v, want 500000", got)
	}
	// INR on screen: the USD rows are flat rather than the INR row shrinking
	// against a currency nobody selected.
	d := NewDashboard(testCtx())
	d.currency = "INR"
	if got := dashLargest(d.currency, spends); got != 20000 {
		t.Errorf("dashLargest(INR) = %v, want 20000", got)
	}
	if got := ratioOfScale(currencyScale("INR", spends[2].Total), dashLargest("INR", spends)); got != 0 {
		t.Errorf("the USD row's bar ratio = %v, want 0: a flat bar, not one sized by 500000", got)
	}
	if got := ratioOfScale(currencyScale("INR", spends[0].Total), dashLargest("INR", spends)); got != 1 {
		t.Errorf("the INR row's bar ratio = %v, want 1: it is the largest in its own currency", got)
	}

	// The frame line names the currency the bars are in and the ones left out.
	d.ready = true
	d.summary.CurrencyScope = api.CurrencyScope{Currencies: []string{"INR", "USD"}}
	notice := d.frameLine(200)
	if !strings.Contains(notice, "INR only") || !strings.Contains(notice, "USD") {
		t.Errorf("frame = %q, want the notice naming the currency left out", notice)
	}
	// With nothing chosen the notice says so rather than quietly picking.
	d.currency = ""
	if frame := d.frameLine(200); !strings.Contains(frame, "no currency selected") {
		t.Errorf("frame = %q, want the unchosen state named", frame)
	}
}

// TestCurrencyOptionsComeFromTheAccounts pins where the picker's list comes
// from: the accounts the screen already holds, in code order and deduplicated,
// because a screen cannot know which currencies a window spans before it asks.
func TestCurrencyOptionsComeFromTheAccounts(t *testing.T) {
	ref := &RefData{Accounts: []api.Account{
		{ID: "a1", Name: "Everyday", Currency: "usd"},
		{ID: "a2", Name: "Salary", Currency: "INR"},
		{ID: "a3", Name: "Card", Currency: "USD"},
		{ID: "a4", Name: "Old", Currency: ""},
	}}

	options := ref.CurrencyOptions()
	if len(options) != 2 || options[0].Value != "INR" || options[1].Value != "USD" {
		t.Fatalf("options = %+v, want INR then USD", options)
	}
	if got := (&RefData{}).CurrencyOptions(); len(got) != 0 {
		t.Errorf("options with no accounts = %+v, want none", got)
	}
}

// TestCurrencyNetTextOnlySignsWhatHasOneSign pins the one glyph a net line
// adds, and pins that it is not added to a figure with no single sign: the two
// screens that show a net share this helper precisely so they cannot disagree
// about when a "+" is earned.
func TestCurrencyNetTextOnlySignsWhatHasOneSign(t *testing.T) {
	if got := currencyNetText("INR", api.CurrencyAmounts{"INR": "500.00"}); got != "+500.00" {
		t.Errorf("net text = %q, want %q", got, "+500.00")
	}
	if got := currencyNetText("INR", api.CurrencyAmounts{"INR": "-500.00"}); got != "-500.00" {
		t.Errorf("net text = %q, want the server's own sign left alone", got)
	}
	two := api.CurrencyAmounts{"INR": "500.00", "USD": "-5.00"}

	// A selection resolves which figure is being signed, and only that one: the
	// INR net earns its "+" because the user asked for INR, and the USD entry is
	// not what the sign is about.
	if got := currencyNetText("INR", two); got != "+500.00" {
		t.Errorf("net text = %q with INR selected, want the INR net signed", got)
	}
	if got := currencyNetText("USD", two); got != "-5.00" {
		t.Errorf("net text = %q with USD selected, want the USD net signed", got)
	}

	// With nothing selected there is no figure to sign, so Display names both
	// rather than the helper picking the positive one and adding a "+" to it.
	mixed := currencyNetText("", two)
	if strings.HasPrefix(mixed, "+") {
		t.Errorf("net text = %q, want no sign on a figure that has none", mixed)
	}
	for _, want := range []string{"INR", "USD", "500.00", "5.00"} {
		if !strings.Contains(mixed, want) {
			t.Errorf("net text = %q, missing %q", mixed, want)
		}
	}
}

// TestTheEmptyMapIsOneSentence covers the state the two helpers used to answer
// differently: "no currency" from currencyNetText, "no transactions" from
// currencyLine, in the same response, on the same screen. A net row saying one
// thing and the row above it saying another is a reader being told two facts
// about one day, and only one of them is a sentence the rest of the client uses.
//
// It is compared as an equality against currencyLine rather than against a
// literal, because the point is that there is one answer, not which words it is:
// a hardcoded string here would let the two drift apart again the moment either
// side was reworded.
func TestTheEmptyMapIsOneSentence(t *testing.T) {
	for _, selected := range []string{"", "INR"} {
		for _, amounts := range []api.CurrencyAmounts{nil, {}} {
			net, line := currencyNetText(selected, amounts), currencyLine(selected, amounts)
			if net != line {
				t.Errorf("with %q selected and %v: the net says %q and the figure says %q, about one response",
					selected, amounts, net, line)
			}
		}
	}
	if got := currencyNetText("", nil); got != "no transactions" {
		t.Errorf("net text for an empty map = %q, want %q", got, "no transactions")
	}
}

// TestTheTimelineStripIsScaledByAMagnitudeNotASum is the one bar left in the TUI
// whose denominator was an unconditional sum — a period's income plus its expense
// — which with nothing selected was two magnitudes that could be two different
// currencies added together. The frame line discloses "bars scale to the largest
// of them", and currencyScale's own comment is careful to promise a maximum, so
// the code and the disclosure were saying two different things about the same
// bar.
func TestTheTimelineStripIsScaledByAMagnitudeNotASum(t *testing.T) {
	periods := []api.MoneyFlowTimelinePeriod{{
		Key: "2026-01", Label: "Jan 2026", StartDate: "2026-01-01", EndDate: "2026-01-31",
		Income:  api.CurrencyAmounts{"INR": "1000.00"},
		Expense: api.CurrencyAmounts{"INR": "1000.00"},
		Net:     api.CurrencyAmounts{"INR": "0.00"},
	}}

	// With a currency on screen the two magnitudes are that currency's own, so the
	// sum is a total inside one currency — and a stacked bar is only legible if
	// both segments fit inside it, which is what the sum buys.
	if got := mfBusiest(periods, "INR"); got != 2000 {
		t.Errorf("busiest with INR selected = %v, want 2000 so both segments fit", got)
	}

	// With nothing selected the same two figures are 1,000 rupees in and 1,000
	// dollars out, and a denominator of 2,000 would be a bar scaled by a figure
	// that never existed.
	mixed := []api.MoneyFlowTimelinePeriod{{
		Key: "2026-01", Label: "Jan 2026", StartDate: "2026-01-01", EndDate: "2026-01-31",
		Income:  api.CurrencyAmounts{"INR": "1000.00"},
		Expense: api.CurrencyAmounts{"USD": "80.00"},
		Net:     api.CurrencyAmounts{},
	}}
	if got := mfBusiest(mixed, ""); got != 1000 {
		t.Errorf("busiest with no currency selected = %v, want 1000, never 1,080 rupees and dollars", got)
	}
}

// TestCalendarLegendNamesTheScaleItActuallyUses covers the legend's one branch
// on the selection. With a currency chosen the legend prints that currency's
// MaxAbsNet; with none, the cells are scaled by the largest single currency and
// the legend has to say so, because a per-currency list there would read like a
// promise of a scale per currency that no cell is using.
func TestCalendarLegendNamesTheScaleItActuallyUses(t *testing.T) {
	c := NewCalendar(testCtx())
	c.fetched = true
	c.data = api.CashFlowCalendar{
		MaxAbsNet:     api.CurrencyAmounts{"INR": "1000.00", "USD": "80.00"},
		CurrencyScope: api.CurrencyScope{Currencies: []string{"INR", "USD"}},
	}

	if got := c.legendLine(200); !strings.Contains(got, "1,000.00") || !strings.Contains(got, "none selected") {
		t.Errorf("legend = %q, want the largest single magnitude and the reason", got)
	}

	c.currency = "INR"
	chosen := c.legendLine(200)
	if !strings.Contains(chosen, "INR 1,000.00") {
		t.Errorf("legend = %q, want the INR entry of MaxAbsNet", chosen)
	}
	if strings.Contains(chosen, "none selected") {
		t.Errorf("legend = %q, want no fallback note when a currency is chosen", chosen)
	}
	if !strings.Contains(chosen, "not showing USD") {
		t.Errorf("legend = %q, want the entry left out named", chosen)
	}
}

// TestCalendarLegendNamesTheFlatCell is the legend's other half. A monochrome
// terminal has nothing but the shading to go on, and the flat cell is the one
// glyph that means the payload gave no sign at all — a day that netted to zero, a
// day the currency on screen never touched, or a day that points different ways
// in different currencies. The last is the case this whole change exists to be
// able to show, and an unlabelled cell is a state the user cannot read, so the
// legend has to carry it alongside the surplus, deficit and overlay glyphs.
func TestCalendarLegendNamesTheFlatCell(t *testing.T) {
	c := NewCalendar(testCtx())
	c.fetched = true
	c.data = api.CashFlowCalendar{
		MaxAbsNet:     api.CurrencyAmounts{"INR": "1000.00"},
		CurrencyScope: api.CurrencyScope{Currencies: []string{"INR"}},
	}
	c.currency = "INR"

	got := c.legendLine(200)
	// Every glyph dayCell can draw is named, so none of them is a mystery.
	for _, want := range []string{"surplus", "deficit", "··", "no sign", "cycle start", "marker"} {
		if !strings.Contains(got, want) {
			t.Errorf("legend = %q, does not name %q", got, want)
		}
	}
}

// TestCurrencyLineSurvivesANilMap is the decode path's case: a field the API
// omitted arrives as a nil map, and a nil map rendered through the len() branch
// must still say so rather than panic on the Single call below it.
func TestCurrencyLineSurvivesANilMap(t *testing.T) {
	if got := currencyLine("INR", nil); got != "no transactions" {
		t.Errorf("currencyLine(nil) = %q, want %q", got, "no transactions")
	}
	if got := currencyLine("", nil); got != "no transactions" {
		t.Errorf("currencyLine(unselected, nil) = %q, want %q", got, "no transactions")
	}
}

// TestTheCurrencySelectionStaysOffTheWire pins the decision the three screens
// share, and it pins it at the place where it would be broken: the window form's
// Currency field must set the screen's own rendering choice and must not reach
// the filter the request is built from.
//
// A ?currency= on the request would make the server drop the other currencies
// from the payload, and every notice the screens print — "not combined", "INR
// only — not showing USD" — would then have nothing to report, which is the
// silence this change exists to end.
func TestTheCurrencySelectionStaysOffTheWire(t *testing.T) {
	// The dashboard: the form offers the currency, the screen keeps it, and the
	// filter the request is built from carries none.
	d := NewDashboard(testCtx())
	var opened Modal
	d.ctx.Open = func(m Modal) { opened = m }
	d.openWindowForm()

	form, ok := opened.(*Form)
	if !ok {
		t.Fatalf("the window key opened a %T, want a *Form", opened)
	}
	if got := form.Value("Currency"); got != "" {
		t.Errorf("the form's currency = %q, want it to start unselected", got)
	}
	// Focus the currency field by label rather than by position, so a field
	// added to the form above it does not silently turn this into a test of
	// nothing.
	focusSelect(t, form, "Currency")
	form.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	// Enter on a select opens its picker, so the form is submitted the way the
	// hint line says it can be.
	form.Update(ctrlPress('s'))

	if d.currency != "INR" {
		t.Errorf("the screen's currency = %q after applying the form, want INR", d.currency)
	}
	if got := d.request().Currency; got != "" {
		t.Errorf("the request carries currency=%q: the server would drop every other currency from the payload", got)
	}

	// The money-flow screen: the same field, and neither of its two requests
	// carries it.
	m := NewMoneyFlow(testCtx())
	m.ctx.Open = func(modal Modal) { opened = modal }
	m.openWindowForm()
	flowForm, ok := opened.(*Form)
	if !ok {
		t.Fatalf("the window key opened a %T, want a *Form", opened)
	}
	focusSelect(t, flowForm, "Currency")
	flowForm.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	flowForm.Update(ctrlPress('s'))
	if m.currency != "INR" {
		t.Errorf("the money-flow screen's currency = %q, want INR", m.currency)
	}
	if got := m.graphFilter().Currency; got != "" {
		t.Errorf("the graph request carries currency=%q", got)
	}
	if got := m.timelineFilter().Currency; got != "" {
		t.Errorf("the timeline request carries currency=%q", got)
	}

	// The calendar, which shades with it and so must not send it either.
	c := NewCalendar(testCtx())
	c.ctx.Open = func(modal Modal) { opened = modal }
	c.openWindowForm()
	calForm, ok := opened.(*Form)
	if !ok {
		t.Fatalf("the window key opened a %T, want a *Form", opened)
	}
	focusSelect(t, calForm, "Currency")
	calForm.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	calForm.Update(ctrlPress('s'))
	if c.currency != "INR" {
		t.Errorf("the calendar's currency = %q, want INR", c.currency)
	}
	if got := c.filter.Currency; got != "" {
		t.Errorf("the calendar request carries currency=%q", got)
	}
}

// TestTheLinkPanelNeverCutsACurrencyCode pins the overflow trade where it was
// being applied inconsistently. The panel budgets 12 cells for a total and is 32
// wide, so a multi-currency figure cannot fit beside the label — and truncating
// it there would cut a currency code out of the middle of a money line, which is
// the one outcome padLeft is written to prevent. The amount takes its own rows
// and wraps instead.
func TestTheLinkPanelNeverCutsACurrencyCode(t *testing.T) {
	m := NewMoneyFlow(testCtx())
	m.loaded = true
	m.graph = api.MoneyFlowGraph{
		LinkSummary: []api.MoneyFlowLinkSummary{{
			Type:  "transfer",
			Count: 3,
			Total:  api.CurrencyAmounts{"INR": "50000.00", "USD": "120.00"},
		}},
	}

	panel := m.linkPanel(mfPanelWidth, 20)
	// Both currency codes survive, and the total is not cut short of them.
	for _, want := range []string{"INR", "USD", "50,000.00", "120.00"} {
		if !strings.Contains(panel, want) {
			t.Errorf("panel = %q, missing %q: a currency code was cut from a money line", panel, want)
		}
	}
	// A single-currency total still reads as one line, so the mixed-currency case
	// is not simply a panel that always wraps now.
	m.graph.LinkSummary[0].Total = api.CurrencyAmounts{"INR": "50000.00"}
	single := m.linkPanel(mfPanelWidth, 20)
	if !strings.Contains(single, "50,000.00") {
		t.Errorf("panel = %q, want the single-currency total", single)
	}
}

// TestTheLinksHeaderDoesNotTruncateTheCircularTotal is the same overflow issue in
// the second place it appeared: the cycles header is truncated to the pane's
// label width, so a per-currency total there would lose a code. The header
// carries counts instead, and the report body opens with the total in full.
func TestTheLinksHeaderDoesNotTruncateTheCircularTotal(t *testing.T) {
	l := NewLinks(testCtx())
	l.pane = linkPaneCycles
	l.cycles = api.LinkCycleReport{
		TotalCircular: api.CurrencyAmounts{"INR": "50000.00", "USD": "120.00"},
		Cycles:        []api.LinkCycle{{Kind: "reciprocal"}},
	}

	header := l.headerLine(60)
	if strings.Contains(header, "circular") {
		t.Errorf("header = %q, want no total in a line that truncates mid-figure", header)
	}
	if !strings.Contains(header, "1 cycle") {
		t.Errorf("header = %q, want the counts it does carry", header)
	}
	// And the report itself still states the total in full.
	if body := strings.Join(l.cycleLines(), "\n"); !strings.Contains(body, "USD 120.00") {
		t.Errorf("report = %q, want the total stated in full in the body", body)
	}
}

// focusSelect moves a form's focus onto the named select field, by label rather
// than by position, so a field inserted above it does not silently turn the test
// into a test of some other field.
func focusSelect(t *testing.T, f *Form, label string) {
	t.Helper()
	for range len(f.fields) + 1 {
		if f.fields[f.index].Kind == FieldSelect && f.fields[f.index].Label == label {
			return
		}
		f.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	}
	t.Fatalf("the form has no select field labelled %q", label)
}
