package ui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/fintrak/client/api"
)

// apiParseAmount is the amount validator the amount fields use, named so the UI
// layer never re-implements the grammar.
func apiParseAmount(s string) (api.Amount, error) { return api.ParseAmount(s) }

// Confirm is a yes/no modal for destructive actions. Confirmation is required
// for anything that deletes or rewrites history, mirroring the web UI's
// AlertDialog convention.
type Confirm struct {
	Title  string
	Body   string
	Detail []string
	// Danger marks the action as destructive, which colours the prompt.
	Danger bool

	closed    bool
	confirmed bool
	onYes     func() tea.Cmd
}

// NewConfirm builds a confirmation modal. onYes returns the command that
// performs the action, built with act so the result flows back as a done
// message.
func NewConfirm(title, body string, danger bool, onYes func() tea.Cmd) *Confirm {
	return &Confirm{Title: title, Body: body, Danger: danger, onYes: onYes}
}

// WithDetail adds context lines (what exactly will be affected).
func (c *Confirm) WithDetail(lines ...string) *Confirm {
	c.Detail = append(c.Detail, lines...)
	return c
}

// Closed reports whether the modal has been dismissed.
func (c *Confirm) Closed() bool { return c.closed }

// Canceled reports whether the dismissal declined the action.
func (c *Confirm) Canceled() bool { return c.closed && !c.confirmed }

// Update handles one key press.
func (c *Confirm) Update(msg tea.Msg) tea.Cmd {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil
	}
	switch key.String() {
	case "y", "enter":
		c.closed, c.confirmed = true, true
		if c.onYes != nil {
			return c.onYes()
		}
	case "n", "esc", "q", "ctrl+c":
		c.closed = true
	}
	return nil
}

// View renders the confirm body.
func (c *Confirm) View(th Theme, width, _ int) string {
	var b strings.Builder
	title := c.Title
	if c.Danger {
		title = th.Error.Render(title)
	}
	b.WriteString(th.ModalTitle.Render(title))
	b.WriteString("\n\n")
	b.WriteString(wrapText(c.Body, max(20, width-6)))
	for _, line := range c.Detail {
		b.WriteString("\n")
		b.WriteString(th.Subtle.Render(wrapText(line, max(20, width-6))))
	}
	b.WriteString("\n\n")
	yes := th.Key.Render("y") + th.KeyDesc.Render(" confirm")
	no := th.Key.Render("n/esc") + th.KeyDesc.Render(" cancel")
	b.WriteString(yes + th.Subtle.Render("  ·  ") + no)
	return b.String()
}

// InfoModal is a read-only overlay for details, reports and previews. It scrolls
// so a long report (a loan schedule, a cycle list, parsed statement rows) does
// not need its own screen.
type InfoModal struct {
	Title  string
	Body   string
	Footer string

	closed bool
	offset int
}

// NewInfo builds a read-only modal.
func NewInfo(title, body string) *InfoModal {
	return &InfoModal{Title: title, Body: body}
}

// WithFooter sets the hint line shown above the key hints.
func (i *InfoModal) WithFooter(footer string) *InfoModal {
	i.Footer = footer
	return i
}

// Closed reports whether the overlay has been dismissed.
func (i *InfoModal) Closed() bool { return i.closed }

// Canceled reports whether the dismissal discarded anything (it never does).
func (i *InfoModal) Canceled() bool { return true }

// Update scrolls or dismisses.
func (i *InfoModal) Update(msg tea.Msg) tea.Cmd {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil
	}
	switch key.String() {
	case "esc", "enter", "q", "ctrl+c":
		i.closed = true
	case "down", "j":
		i.offset++
	case "up", "k":
		if i.offset > 0 {
			i.offset--
		}
	case "pgdown", "ctrl+f":
		i.offset += 10
	case "pgup", "ctrl+b":
		i.offset = max(0, i.offset-10)
	case "home", "g":
		i.offset = 0
	case "end", "G":
		i.offset = 1 << 20
	}
	return nil
}

// View renders the overlay body. The fixed rows are fitted around the content
// rather than subtracted from it: a fixed chrome left a long body — a loan
// schedule, a cycle list — showing one row on a short terminal.
func (i *InfoModal) View(th Theme, width, height int) string {
	lines := strings.Split(i.Body, "\n")
	footer := ""
	if i.Footer != "" {
		footer = wrapText(i.Footer, max(20, width-2))
	}

	// Reserve in order of what a reader needs: the way out, the footer that
	// explains the content, then the title.
	budget := newChromeBudget(height, minListRows)
	showHint := budget.wantRows(2) // blank + hint
	showFooter := footer != "" && budget.wantRows(1+strings.Count(footer, "\n")+1)
	showTitle := budget.wantRows(2) // title + blank
	visible := budget.content()

	i.offset = min(i.offset, max(0, len(lines)-visible))

	var b strings.Builder
	if showTitle {
		b.WriteString(th.ModalTitle.Render(i.Title))
		b.WriteString("\n\n")
	}
	for n := i.offset; n < len(lines) && n-i.offset < visible; n++ {
		b.WriteString(ansi.Truncate(lines[n], max(10, width-2), "…"))
		b.WriteString("\n")
	}
	if showFooter {
		b.WriteString("\n")
		b.WriteString(th.Subtle.Render(footer))
		b.WriteString("\n")
	}
	if showHint {
		b.WriteString("\n")
		if len(lines) > visible {
			b.WriteString(th.Subtle.Render(fmt.Sprintf("↑/↓ scroll (%d/%d) · esc close", i.offset+1, len(lines))))
		} else {
			b.WriteString(th.Subtle.Render("esc close"))
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// wrapText hard-wraps a paragraph to width, preserving explicit newlines.
func wrapText(s string, width int) string {
	if width < 8 {
		width = 8
	}
	var out []string
	for _, paragraph := range strings.Split(s, "\n") {
		words := strings.Fields(paragraph)
		if len(words) == 0 {
			out = append(out, "")
			continue
		}
		line := words[0]
		for _, word := range words[1:] {
			if len(line)+1+len(word) > width {
				out = append(out, line)
				line = word
				continue
			}
			line += " " + word
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

// HelpModal is the full key reference, opened with `?`.
type HelpModal struct {
	offset int
	closed bool
	rows   []HelpRow
	global []HelpRow
	screen []HelpRow
}

// NewHelp builds the help overlay from the global and screen bindings.
func NewHelp(global, screen []HelpRow) *HelpModal {
	return &HelpModal{global: global, screen: screen}
}

// Closed reports whether the overlay has been dismissed.
func (h *HelpModal) Closed() bool { return h.closed }

// Canceled reports whether the dismissal discarded anything (it never does).
func (h *HelpModal) Canceled() bool { return true }

// Update dismisses the overlay on any of the usual close keys.
func (h *HelpModal) Update(msg tea.Msg) tea.Cmd {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil
	}
	switch key.String() {
	case "?", "esc", "q", "enter", "ctrl+c":
		h.closed = true
	case "down", "j":
		h.offset++
	case "up", "k":
		h.offset = max(0, h.offset-1)
	}
	return nil
}

// View renders the overlay, fitted to the height it is given.
func (h *HelpModal) View(th Theme, width, height int) string {
	lines := make([]string, 0, len(h.global)+len(h.screen)+4)
	section := func(title string, rows []HelpRow) {
		if len(rows) == 0 {
			return
		}
		lines = append(lines, th.Header.Render(title))
		for _, row := range rows {
			lines = append(lines, "  "+th.Key.Render(pad(row.Keys, 12))+" "+th.KeyDesc.Render(row.Desc))
		}
		lines = append(lines, "")
	}
	section("global", h.global)
	section("this screen", h.screen)

	budget := newChromeBudget(height, minListRows)
	showFooter := budget.wantRows(2) // blank + footer
	showTitle := budget.wantRows(2)  // title + blank
	visible := budget.content()

	h.offset = clampOffset(h.offset, h.offset, visible, len(lines))

	var b strings.Builder
	if showTitle {
		b.WriteString(th.ModalTitle.Render("FinTrak keys"))
		b.WriteString("\n\n")
	}
	for i := h.offset; i < len(lines) && i < h.offset+visible; i++ {
		b.WriteString(lines[i])
		b.WriteString("\n")
	}
	if showFooter {
		b.WriteString("\n")
		if hidden := len(lines) - (h.offset + visible); hidden > 0 {
			b.WriteString(th.Subtle.Render(fmt.Sprintf("↓ %d more · ↑/↓ scroll · ?/esc close", hidden)))
		} else {
			b.WriteString(th.Subtle.Render("?/esc close"))
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// pad right-pads a key hint so descriptions line up.
func pad(s string, width int) string {
	if len(s) >= width {
		return s
	}
	return s + strings.Repeat(" ", width-len(s))
}
