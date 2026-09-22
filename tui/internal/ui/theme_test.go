package ui

import (
	"io"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// TestThemeForRendersWithTheSessionRenderer is the unit half of the SSH door's
// colour fix. One door process serves many terminals, so the palette has to take
// its colour profile from the renderer built for the session's pty: the styles
// used to come from lipgloss's package-level renderer, which describes the door's
// own stdout — in a container, not a terminal, so every session rendered in
// monochrome no matter what its client supported.
func TestThemeForRendersWithTheSessionRenderer(t *testing.T) {
	mono := lipgloss.NewRenderer(io.Discard)
	mono.SetColorProfile(termenv.Ascii)
	colour := lipgloss.NewRenderer(io.Discard)
	colour.SetColorProfile(termenv.ANSI256)

	if got := ThemeFor(mono).Negative.Render("x"); got != "x" {
		t.Errorf("a session with no colour support was sent escape codes: %q", got)
	}
	styled := ThemeFor(colour).Negative.Render("x")
	if styled == "x" || !strings.Contains(styled, "\x1b[") {
		t.Errorf("a 256-colour session got no colour: %q", styled)
	}

	// Every style is built on that renderer, not just the money colours.
	for name, got := range map[string]string{
		"title":    ThemeFor(colour).Title.Render("x"),
		"sidebar":  ThemeFor(colour).Sidebar.Render("x"),
		"modal":    ThemeFor(colour).Modal.Render("x"),
		"key hint": ThemeFor(colour).Key.Render("x"),
	} {
		if !strings.Contains(got, "\x1b[") {
			t.Errorf("the %s style ignored the session renderer: %q", name, got)
		}
	}
}
