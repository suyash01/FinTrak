package ui

import (
	"fmt"
	"strconv"
	"strings"
	"time"

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

// sumAmountsForDisplay adds amounts for a display-only total such as a bar
// denominator. It parses through the display-only float conversion, so the
// result must never be shown as currency or sent to the API.
func sumAmountsForDisplay(amounts []api.Amount) float64 {
	total := 0.0
	for _, a := range amounts {
		total += a.Float64()
	}
	return total
}

// ratioOf reports amount/total for a bar width, guarding a zero denominator.
func ratioOf(amount api.Amount, total float64) float64 {
	if total == 0 {
		return 0
	}
	return amount.Float64() / total
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
