package ui

import (
	"strings"
	"testing"

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

// TestCurrencyIsNegativeNeedsEveryCurrency pins the sign rule the heatmap and
// the timeline strip colour by: a figure negative in one currency and positive
// in another has no single sign, so it is neither family rather than resolved by
// guessing which currency was meant.
func TestCurrencyIsNegativeNeedsEveryCurrency(t *testing.T) {
	mixed := api.CurrencyAmounts{"INR": "-500.00", "USD": "5.00"}

	if currencyIsNegative("", mixed) {
		t.Error("a figure with no single sign was drawn as a deficit")
	}
	// A selection resolves it, and only for the currency the user chose.
	if !currencyIsNegative("INR", mixed) {
		t.Error("the INR entry is negative and was not drawn as a deficit")
	}
	if currencyIsNegative("USD", mixed) {
		t.Error("the USD entry is positive and was drawn as a deficit")
	}

	both := api.CurrencyAmounts{"INR": "-500.00", "USD": "-5.00"}
	if !currencyIsNegative("", both) {
		t.Error("a figure negative in every currency was not drawn as a deficit")
	}
	if currencyIsNegative("", api.CurrencyAmounts{}) {
		t.Error("an empty map has a sign")
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
	// Nothing is chosen, so the label has to say the figures were never combined.
	none := currencyScopeLabel("", scope)
	if !strings.Contains(none, "no currency selected") || !strings.Contains(none, "EUR") {
		t.Errorf("label = %q, want the unchosen state named with the currencies", none)
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
