package ui

import (
	"image/color"

	"charm.land/bubbles/v2/textinput"
	"charm.land/lipgloss/v2"
)

// Theme is the terminal client's palette and the styles built from it. The
// colours resolve for a light or a dark terminal background, chosen per session
// from the terminal's own answer to a background-colour query. lipgloss v2 has
// no renderer to carry that choice — v1 did, and this app built one per SSH
// session so a client's colours could not leak into another's — so the theme is
// a plain value built from an explicit flag and rebuilt when the answer arrives.
type Theme struct {
	Primary color.Color
	Muted   color.Color
	Danger  color.Color
	Success color.Color
	Warn    color.Color
	Border  color.Color

	// InputStyles are the text field's styles. bubbles keeps these per input
	// model rather than inheriting them from the app's palette, so they are
	// carried here and pushed into a form's inputs by Form.SetStyles.
	InputStyles textinput.Styles

	Sidebar        lipgloss.Style
	SidebarItem    lipgloss.Style
	SidebarCurrent lipgloss.Style
	Title          lipgloss.Style
	Subtle         lipgloss.Style
	Header         lipgloss.Style
	Row            lipgloss.Style
	RowSelected    lipgloss.Style
	Money          lipgloss.Style
	Negative       lipgloss.Style
	Positive       lipgloss.Style
	Error          lipgloss.Style
	SuccessText    lipgloss.Style
	WarnText       lipgloss.Style
	Panel          lipgloss.Style
	PanelTitle     lipgloss.Style
	Status         lipgloss.Style
	Key            lipgloss.Style
	KeyDesc        lipgloss.Style
	Modal          lipgloss.Style
	ModalTitle     lipgloss.Style
	FieldLabel     lipgloss.Style
	FieldFocused   lipgloss.Style
	Bar            lipgloss.Style
	BarEmpty       lipgloss.Style
}

// Modal frame sizes, in terminal rows. Below compactModalHeight the frame drops
// its vertical padding, and below minimalModalHeight it drops the border too: on
// a short terminal those rows are worth more to the content than to the chrome.
const (
	compactModalHeight = 20
	minimalModalHeight = 12
)

// ModalFor returns the modal frame to draw for a terminal of the given height.
// The frame is chrome: when rows are scarce the content matters more, and a
// fixed frame is what left a nine-field form showing a single field.
func (t Theme) ModalFor(height int) lipgloss.Style {
	switch {
	case height >= compactModalHeight:
		return t.Modal
	case height >= minimalModalHeight:
		return t.Modal.Padding(0, 2)
	default:
		return lipgloss.NewStyle().Padding(0, 1)
	}
}

// DefaultTheme returns the dark palette. It is the theme in force before the
// terminal answers the background-colour query — most terminals are dark, and a
// dark guess leaves a light terminal merely wrong for one frame rather than
// making every later frame wrong — and it is what the tests render with.
func DefaultTheme() Theme { return ThemeFor(true) }

// ThemeFor builds the palette for a terminal whose background is dark (isDark)
// or light. v1 took a *lipgloss.Renderer because the renderer was what knew the
// terminal's colours; v2 has no renderer, so the caller states the answer and
// every style is a plain value.
func ThemeFor(isDark bool) Theme {
	lightDark := lipgloss.LightDark(isDark)

	primary := lightDark(lipgloss.Color("#0e7490"), lipgloss.Color("#22d3ee"))
	muted := lightDark(lipgloss.Color("#64748b"), lipgloss.Color("#94a3b8"))
	danger := lightDark(lipgloss.Color("#b91c1c"), lipgloss.Color("#f87171"))
	success := lightDark(lipgloss.Color("#15803d"), lipgloss.Color("#4ade80"))
	warn := lightDark(lipgloss.Color("#b45309"), lipgloss.Color("#fbbf24"))
	border := lightDark(lipgloss.Color("#cbd5e1"), lipgloss.Color("#334155"))
	rowSelected := lightDark(lipgloss.Color("#e2e8f0"), lipgloss.Color("#1e293b"))

	return Theme{
		Primary: primary,
		Muted:   muted,
		Danger:  danger,
		Success: success,
		Warn:    warn,
		Border:  border,

		InputStyles: textinput.DefaultStyles(isDark),

		Sidebar:        lipgloss.NewStyle().Foreground(muted).Padding(0, 1),
		SidebarItem:    lipgloss.NewStyle().Foreground(muted).Padding(0, 1),
		SidebarCurrent: lipgloss.NewStyle().Foreground(primary).Bold(true).Padding(0, 1),
		Title:          lipgloss.NewStyle().Foreground(primary).Bold(true),
		Subtle:         lipgloss.NewStyle().Foreground(muted),
		Header:         lipgloss.NewStyle().Foreground(muted).Bold(true),
		Row:            lipgloss.NewStyle(),
		RowSelected:    lipgloss.NewStyle().Background(rowSelected),
		Money:          lipgloss.NewStyle(),
		Negative:       lipgloss.NewStyle().Foreground(danger),
		Positive:       lipgloss.NewStyle().Foreground(success),
		Error:          lipgloss.NewStyle().Foreground(danger),
		SuccessText:    lipgloss.NewStyle().Foreground(success),
		WarnText:       lipgloss.NewStyle().Foreground(warn),
		Panel:          lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(border).Padding(0, 1),
		PanelTitle:     lipgloss.NewStyle().Foreground(primary).Bold(true),
		Status:         lipgloss.NewStyle().Foreground(muted),
		Key:            lipgloss.NewStyle().Foreground(primary),
		KeyDesc:        lipgloss.NewStyle().Foreground(muted),
		Modal:          lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(primary).Padding(1, 2),
		ModalTitle:     lipgloss.NewStyle().Foreground(primary).Bold(true),
		FieldLabel:     lipgloss.NewStyle().Foreground(muted),
		FieldFocused:   lipgloss.NewStyle().Foreground(primary).Bold(true),
		Bar:            lipgloss.NewStyle().Foreground(primary),
		BarEmpty:       lipgloss.NewStyle().Foreground(border),
	}
}
