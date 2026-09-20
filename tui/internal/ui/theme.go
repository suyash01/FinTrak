package ui

import (
	"github.com/charmbracelet/lipgloss"
)

// Theme is the terminal client's palette and the styles built from it. The
// colors are adaptive, so the same binary reads correctly on a light or dark
// terminal without configuration.
type Theme struct {
	Primary lipgloss.AdaptiveColor
	Muted   lipgloss.AdaptiveColor
	Danger  lipgloss.AdaptiveColor
	Success lipgloss.AdaptiveColor
	Warn    lipgloss.AdaptiveColor
	Border  lipgloss.AdaptiveColor

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

// DefaultTheme returns the standard palette.
func DefaultTheme() Theme {
	primary := lipgloss.AdaptiveColor{Light: "#0e7490", Dark: "#22d3ee"}
	muted := lipgloss.AdaptiveColor{Light: "#64748b", Dark: "#94a3b8"}
	danger := lipgloss.AdaptiveColor{Light: "#b91c1c", Dark: "#f87171"}
	success := lipgloss.AdaptiveColor{Light: "#15803d", Dark: "#4ade80"}
	warn := lipgloss.AdaptiveColor{Light: "#b45309", Dark: "#fbbf24"}
	border := lipgloss.AdaptiveColor{Light: "#cbd5e1", Dark: "#334155"}

	return Theme{
		Primary: primary,
		Muted:   muted,
		Danger:  danger,
		Success: success,
		Warn:    warn,
		Border:  border,

		Sidebar:        lipgloss.NewStyle().Foreground(muted).Padding(0, 1),
		SidebarItem:    lipgloss.NewStyle().Foreground(muted).Padding(0, 1),
		SidebarCurrent: lipgloss.NewStyle().Foreground(primary).Bold(true).Padding(0, 1),
		Title:          lipgloss.NewStyle().Foreground(primary).Bold(true),
		Subtle:         lipgloss.NewStyle().Foreground(muted),
		Header:         lipgloss.NewStyle().Foreground(muted).Bold(true),
		Row:            lipgloss.NewStyle(),
		RowSelected:    lipgloss.NewStyle().Background(lipgloss.AdaptiveColor{Light: "#e2e8f0", Dark: "#1e293b"}),
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
