package ui

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/fintrak/client/api"
)

// Presentation helpers shared by the screens. Nothing here computes money: an
// amount is only ever formatted for display, and the display-only float
// conversion in api.Amount is used for bar widths alone.

// categoryOr renders a joined category name, falling back to the id and then to
// the uncategorized label.
func categoryOr(name string, id *string) string {
	if name != "" {
		return name
	}
	if id == nil || *id == "" {
		return "Uncategorized"
	}
	return *id
}

// payeeOr renders a joined payee name.
func payeeOr(name string, id *string) string {
	if name != "" {
		return name
	}
	if id == nil || *id == "" {
		return "—"
	}
	return *id
}

// derefID renders an optional identifier as a form value ("" when unset).
func derefID(id *string) string {
	if id == nil {
		return ""
	}
	return *id
}

// linkedState renders the tri-state linked filter as a picker value.
func linkedState(linked *bool) string {
	switch {
	case linked == nil:
		return ""
	case *linked:
		return "yes"
	default:
		return "no"
	}
}

// parseLinked turns a picker value back into the tri-state filter.
func parseLinked(value string) *bool {
	switch value {
	case "yes":
		return new(true)
	case "no":
		return new(false)
	default:
		return nil
	}
}

// splitList splits a comma-separated form value, dropping blanks and duplicates.
func splitList(raw string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" || seen[part] {
			continue
		}
		seen[part] = true
		out = append(out, part)
	}
	return out
}

// nowDate is today in the API's date-only layout.
func nowDate() string { return time.Now().Format(api.DateLayout) }

// defaultTo returns value when set, otherwise fallback.
func defaultTo(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

// requiredDate validates a "YYYY-MM-DD" value.
func requiredDate(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return errText("required (YYYY-MM-DD)")
	}
	if _, err := time.Parse(api.DateLayout, value); err != nil {
		return errText("expected YYYY-MM-DD")
	}
	return nil
}

// optionalDate validates a date only when one was entered.
func optionalDate(value string) error {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return requiredDate(value)
}

// positiveInt validates an optional non-negative integer.
func positiveInt(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	n, err := strconv.Atoi(value)
	if err != nil || n < 0 {
		return errText("expected a whole number")
	}
	return nil
}

// requiredInt validates a mandatory non-negative integer.
func requiredInt(value string) error {
	if strings.TrimSpace(value) == "" {
		return errText("required")
	}
	return positiveInt(value)
}

// signedAmount renders an amount with the direction implied by the transaction
// type. The digits come straight from the API; only the sign glyph is added.
func signedAmount(amount api.Amount, txnType string) string {
	text := amount.Display()
	if amount.IsZero() {
		return text
	}
	if txnType == "credit" {
		return "+" + strings.TrimPrefix(text, "-")
	}
	return text
}

// currencyLine renders one amount for a screen that is showing a single
// currency. When the response holds the selected currency and nothing else, it
// is the formatted value. When it holds more, the line names the others and
// what they hold rather than choosing one: a terminal has no room for two, and
// picking silently is the bug the per-currency map was introduced to stop.
//
// Empty is "nothing to show", not a zero of some currency — a missing key means
// no transaction in the window touched that currency, which is a different fact
// from "it was zero". An empty selection is not a currency that is missing: with
// nothing chosen there is no currency to have been left out, so the single
// currency the response holds is simply shown.
//
// The code is part of the line even when the selection already names it, because
// a figure read in the wrong currency is the whole failure this guards against
// and a bare number beside a header does not carry it.
func currencyLine(selected string, amounts api.CurrencyAmounts) string {
	if len(amounts) == 0 {
		return "no transactions"
	}
	code, value, ok := amounts.Single()
	if !ok {
		// More than one: say so and name what is being left out. Display already
		// renders the explicit multi-currency form ("2 currencies: EUR 1.00, INR
		// 5,000.00"), so this does not re-derive it.
		return amounts.Display() + " — not combined"
	}
	if selected == "" || code == selected {
		return code + " " + value.Display()
	}
	// The response holds exactly one currency and it is not the one on screen,
	// which is what a currency selection over a window that has nothing in that
	// currency looks like. Saying so is better than rendering a foreign number
	// in the wrong column.
	return "no transactions in " + selected
}

// currencyValue returns the figure a screen reads a sign or a width from: the
// selected currency's own entry, or — with nothing selected — the only entry
// there is. ok=false means there is no such figure: a window in two currencies
// with no selection has no single one, and a day carrying no key for the
// currency on screen is not a zero.
//
// This and currencyScale are the only routes from a CurrencyAmounts to a number,
// and they are for the paths that cannot be rendered at all. Text always goes
// through currencyLine, which refuses rather than reaching here to pick one.
func currencyValue(selected string, amounts api.CurrencyAmounts) (api.Amount, bool) {
	if selected != "" {
		// An absent key reads as zero, which is what the amount rules say a
		// missing key means: a currency with no money in it, not a figure.
		value, ok := amounts[selected]
		return value, ok
	}
	_, value, ok := amounts.Single()
	return value, ok
}

// currencyScale is currencyValue read as a display-only magnitude, for a bar
// width and a heatmap level. With a currency on screen it is that
// currency's own magnitude and nothing else, so a bar is never sized by a
// number from a currency the user is not looking at — and a node the currency
// on screen never touched reads as zero, which leaves it flat.
//
// With nothing selected there is no currency to prefer, and a scale is still
// needed, so it is the largest magnitude any single currency holds. A maximum
// over magnitudes is not a sum: it reports no figure that never existed, and
// every figure printed beside such a bar goes through currencyLine, which names
// the currencies rather than adding them.
func currencyScale(selected string, amounts api.CurrencyAmounts) float64 {
	if selected != "" {
		value, _ := amounts[selected]
		return value.Abs().Float64()
	}
	largest := 0.0
	for _, value := range amounts {
		if magnitude := value.Abs().Float64(); magnitude > largest {
			largest = magnitude
		}
	}
	return largest
}

// currencySign is which way a per-currency figure points, as the three answers
// the response can actually support. signNone is the whole reason it exists: a
// figure negative in one currency and positive in another has no single sign, and
// a two-valued sign cannot express that — the caller has to read its false as
// either "positive" or "unknown", and it picked "positive", so a day that spent
// in dollars and earned in rupees drew as a green surplus.
type currencySign int

const (
	// signNone is the zero value on purpose. A currencySign that is read before
	// it is set must land on the answer that claims nothing, and the value a
	// fresh variable holds is the one a missing case would take.
	signNone currencySign = iota
	signNegative
	signPositive
)

// currencySignOf reports which way a per-currency figure points: the selected
// currency's own sign, or — with nothing selected — a sign only when the map
// agrees with itself. The map is negative when every currency it covers is, and
// positive when every one is; mixed is signNone.
//
// A currency on screen that the response never held is signNone whatever the
// other entries say. currencyValue reports that case as "no figure", but a
// figure of the wrong currency is not a figure of none, and reading the map's
// agreement in its place would tell the user their INR day went up because a
// USD day did.
//
// The three-valued answer is the point. A bool here has no way to say "this
// figure has no sign", and every caller reading false as a surplus is how a
// mixed-sign day became a green block.
func currencySignOf(selected string, amounts api.CurrencyAmounts) currencySign {
	if selected != "" {
		value, ok := amounts[selected]
		if !ok {
			return signNone
		}
		switch {
		case value.IsNegative():
			return signNegative
		case value.IsZero():
			// A present zero is a figure, and zero is not a surplus. It falls
			// through to the flat rendering every other screen gives it.
			return signNone
		default:
			return signPositive
		}
	}
	// No selected figure, so the map has to speak for itself, and it speaks by
	// the rule CurrencyAmounts.IsNegative already states: negative only when
	// every currency it covers is. A present zero is not negative, so it blocks a
	// deficit the same way the type's own method blocks one — a day of INR 0.00
	// against a USD 5.00 spend has not gone down, it has one currency that did
	// nothing and another that spent. Positive is the mirror: every entry
	// nonzero and not negative.
	switch {
	case len(amounts) == 0:
		return signNone
	case amounts.IsNegative():
		return signNegative
	}
	for _, value := range amounts {
		if value.IsNegative() || value.IsZero() {
			return signNone
		}
	}
	return signPositive
}

// style is the theme style a signed figure is drawn in, and what a figure with
// no sign falls back to. It exists so the four callers cannot each pick their
// own treatment of signNone, which is the mistake this type was made to stop.
func (s currencySign) style(th Theme) lipgloss.Style {
	switch s {
	case signNegative:
		return th.Negative
	case signPositive:
		return th.Positive
	default:
		// Deliberately muted rather than Positive: the screen does not know which
		// way this figure points, and colouring it as though it did is the claim
		// the payload refused to make.
		return th.Subtle
	}
}

// currencyAmount formats a display-only scale as money, for the one place that
// has to print a magnitude rather than an amount: the calendar legend's "the
// largest of N currencies" note. It is given the float the bar helpers already
// computed, so nothing here is derived from a map and no scale is ever a sum.
func currencyAmount(magnitude float64) string {
	return api.Amount(fmt.Sprintf("%.2f", magnitude)).Display()
}

// currencyNetText renders a net with the explicit "+" a surplus has earned. The
// server's amounts already carry their sign — this is the only glyph added — so
// signedAmount is the wrong helper here: that one is built for credit
// transactions, whose stored amount carries no sign of its own.
//
// A figure in more than one currency gets no "+": it has no single sign to earn
// one, and Display names the currencies rather than resolving them. currencyLine
// is used for one case only — an empty map, where the two have no figure to
// disagree about and must not answer it in two different sentences. For every
// other case this is the wrong helper: it is the answer for a total, and a net
// line reads differently, because the sign is the payload.
func currencyNetText(selected string, amounts api.CurrencyAmounts) string {
	if len(amounts) == 0 {
		// Said once, by currencyLine. An empty map is one fact — nothing in this
		// currency, and nothing at all when none is selected — and the two
		// helpers answering it with two different sentences ("no currency" here,
		// "no transactions" there) reads on one screen as two different answers
		// to the same day. Delegating rather than repeating the string is what
		// keeps the two in step.
		return currencyLine(selected, amounts)
	}
	value, ok := currencyValue(selected, amounts)
	if !ok {
		return amounts.Display()
	}
	if value.IsZero() || value.IsNegative() {
		return value.Display()
	}
	return "+" + value.Display()
}

// currencyScopeLabel says which currency a screen is showing and what its
// response holds besides, for the frame line. It is built from the response's own
// scope rather than from the screen's filter, so a window that turned out to span
// more currencies than the accounts suggested is reported rather than hidden.
//
// A selection the response does not hold is not a report in that currency, so it
// is not described as one: currencyLine says "no transactions in INR" for the
// same state, and a label claiming "INR only" beside a figure saying there is
// nothing would put the two halves of one screen in contradiction. An empty label
// means there is nothing to say — one currency and no selection.
func currencyScopeLabel(selected string, scope api.CurrencyScope) string {
	others := make([]string, 0, len(scope.Currencies))
	held := false
	for _, code := range scope.Currencies {
		// An empty code is not a currency and must not be counted as the
		// selection: with nothing selected, "" == "" would otherwise mark the
		// report as holding the empty currency.
		if code == "" {
			continue
		}
		if code == selected {
			held = true
			continue
		}
		others = append(others, code)
	}

	switch {
	case selected != "" && !held:
		// The window has nothing in the currency on screen. Naming what it does
		// hold is the whole of the answer, and it is the same answer currencyLine
		// gives for the figures themselves.
		if len(others) == 0 {
			return "no " + selected + " in this window"
		}
		return "no " + selected + " in this window — showing " + strings.Join(others, ", ")
	case selected != "" && len(others) == 0:
		return selected + " only"
	case selected != "":
		return selected + " only — not showing " + strings.Join(others, ", ")
	case len(others) < 2:
		// Nothing is selected and the window is a single currency, so there is
		// no choice being made and nothing to report.
		return ""
	default:
		// The clause about the bars is here rather than repeated by each screen's
		// own legend: with nothing chosen, dashLargest, stageMax and the strip's
		// busiest all fall back to the largest single magnitude, and a reader of
		// this line is a reader of a bar scaled by it.
		return "no currency selected — " + strings.Join(others, ", ") +
			", never combined; bars scale to the largest of them"
	}
}

// padLeft right-aligns a value in a fixed number of cells, so money columns line
// up. It measures the rendered width rather than the byte length — a per-currency
// line ends in an em dash, and counting its bytes would skew every column it sits
// in. A value too wide for the column is left alone and clipped by the caller's
// box: silently shortening a money figure would report a different number.
func padLeft(value string, width int) string {
	if current := ansi.StringWidth(value); current < width {
		return strings.Repeat(" ", width-current) + value
	}
	return value
}

// amountRole colours an amount by transaction type.
func amountRole(txnType string) CellRole {
	if txnType == "credit" {
		return RolePositive
	}
	return RoleNegative
}

// formatDate renders a timestamp as a date-only string.
func formatDate(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	return t.Format(api.DateLayout)
}

// formatDatePtr renders an optional timestamp.
func formatDatePtr(t *time.Time) string {
	if t == nil {
		return "—"
	}
	return formatDate(*t)
}

// formatRange renders an inclusive date range.
func formatRange(from, to time.Time) string {
	return formatDate(from) + " … " + formatDate(to)
}

// pluralise renders "n thing(s)".
func pluralise(n int, singular, plural string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", singular)
	}
	if plural == "" {
		plural = singular + "s"
	}
	return fmt.Sprintf("%d %s", n, plural)
}

// ratioOf reports amount/total for a bar width, guarding a zero denominator.
func ratioOf(amount api.Amount, total float64) float64 {
	if total == 0 {
		return 0
	}
	return amount.Float64() / total
}

// ratioOfScale is ratioOf for the figures that are already display-only scales
// rather than amounts — currencyScale and the denominators built from it. A
// per-currency total has no single amount behind it while the screen may not
// have chosen one, so the bar width cannot go through ratioOf.
func ratioOfScale(value, total float64) float64 {
	if total == 0 {
		return 0
	}
	return value / total
}

// optionalID bridges a form value to the API's three-state nullable identifier:
// an empty value becomes an explicit null, which is what clears the column on a
// PATCH.
func optionalID(value string) api.OptionalUUID {
	if strings.TrimSpace(value) == "" {
		return api.UUIDNull()
	}
	return api.UUID(value)
}

// truncateID shortens an identifier for display.
func truncateID(id string) string {
	if len(id) <= 8 {
		return id
	}
	return id[:8]
}
