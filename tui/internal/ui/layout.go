package ui

import (
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// minListRows is how many rows of a scrolling list stay visible before a widget
// gives up its fixed rows. Below this a list is unusable, which is what a fixed
// chrome produced: a 50-entry category chooser showing a single row.
const minListRows = 3

// chromeBudget splits the height of a box between the fixed rows a widget draws
// (title, filter line, hint, separators) and the rows left for scrolling content.
// Widgets ask for their fixed rows in order of value; a request is granted only
// while at least minContent rows remain for the content, so a short terminal
// shows fewer fixed rows instead of collapsing the list.
//
// It exists so the arithmetic lives in one place: a widget that counted rows by
// hand (and forgot one) rendered taller than its box.
type chromeBudget struct {
	height     int
	minContent int
	used       int
}

// newChromeBudget starts a budget for a box of the given height.
func newChromeBudget(height, minContent int) chromeBudget {
	return chromeBudget{height: max(1, height), minContent: max(1, minContent)}
}

// want reserves one row, reporting whether it fit.
func (b *chromeBudget) want() bool {
	if b.height-b.used-1 < b.minContent {
		return false
	}
	b.used++
	return true
}

// wantRows reserves n rows at once, all or nothing, for a block of chrome whose
// rows only make sense together (a footer's text and its separator).
func (b *chromeBudget) wantRows(n int) bool {
	if n <= 0 {
		return true
	}
	if b.height-b.used-n < b.minContent {
		return false
	}
	b.used += n
	return true
}

// content reports the rows left for the scrolling area.
func (b *chromeBudget) content() int { return max(1, b.height-b.used) }

// listRows is the height a list screen's table may use inside a screen box: the
// box minus the screen's own fixed rows (header, separator, hint), but never so
// little that the list disappears.
func listRows(height int) int {
	b := newChromeBudget(height, minListRows)
	b.want()
	b.want()
	b.want()
	return b.content()
}

// keyMatches reports whether a key press satisfies a binding.
func keyMatches(binding key.Binding, msg tea.KeyMsg) bool {
	return binding.Enabled() && key.Matches(msg, binding)
}

// digitIndex maps "1".."9" to 0..8 and "0" to 9, returning -1 otherwise.
func digitIndex(s string) int {
	if len(s) != 1 || s[0] < '0' || s[0] > '9' {
		return -1
	}
	if s == "0" {
		return 9
	}
	return int(s[0] - '1')
}

// truncate clips a string to a display width, counting wide runes correctly.
func truncate(s string, width int) string {
	if width <= 0 {
		return ""
	}
	return ansi.Truncate(s, width, "…")
}

// trimToBox clips and pads content into an exact width x height rectangle, so
// the surrounding layout cannot drift as content changes.
func trimToBox(content string, width, height int) string {
	if width <= 0 || height <= 0 {
		return ""
	}
	lines := strings.Split(content, "\n")
	out := make([]string, 0, height)
	for i := range height {
		line := ""
		if i < len(lines) {
			line = ansi.Truncate(lines[i], width, "")
		}
		if pad := width - ansi.StringWidth(line); pad > 0 {
			line += strings.Repeat(" ", pad)
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

// joinHorizontal puts a column of a fixed width next to another block.
func joinHorizontal(left string, leftWidth int, right string) string {
	leftLines := strings.Split(left, "\n")
	rightLines := strings.Split(right, "\n")
	n := max(len(leftLines), len(rightLines))

	out := make([]string, 0, n)
	for i := range n {
		var l, r string
		if i < len(leftLines) {
			l = leftLines[i]
		}
		if i < len(rightLines) {
			r = rightLines[i]
		}
		if pad := leftWidth - ansi.StringWidth(l); pad > 0 {
			l += strings.Repeat(" ", pad)
		}
		out = append(out, l+r)
	}
	return strings.Join(out, "\n")
}

// centerBlock places a block in the middle of a width x height area.
func centerBlock(content string, width, height int) string {
	return replaceCenter(strings.Repeat("\n", max(0, height-1)), content, width, height)
}

// replaceCenter splices a foreground block over a background, centred, leaving
// the background visible around it. Slicing is done by display width so styled
// (escape-laden) lines are not corrupted.
func replaceCenter(background, foreground string, width, height int) string {
	bg := strings.Split(background, "\n")
	fg := strings.Split(foreground, "\n")

	fgWidth := 0
	for _, line := range fg {
		fgWidth = max(fgWidth, ansi.StringWidth(line))
	}
	top := max(0, (height-len(fg))/2)
	left := max(0, (width-fgWidth)/2)

	out := make([]string, len(bg))
	copy(out, bg)
	for i, line := range fg {
		row := top + i
		if row >= len(out) {
			break
		}
		base := out[row]
		if pad := width - ansi.StringWidth(base); pad > 0 {
			base += strings.Repeat(" ", pad)
		}
		before := ansi.Cut(base, 0, left)
		after := ansi.Cut(base, left+ansi.StringWidth(line), width)
		out[row] = before + line + after
	}
	return strings.Join(out, "\n")
}

// hstack renders labelled value pairs on one line, used by the detail panes.
func hstack(pairs ...string) string {
	return strings.Join(pairs, "  ")
}

// bar renders a proportional bar of a given width, used for category breakdowns
// and the calendar's intensity scale. ratio is clamped to [0,1] and comes from
// display-only float conversions of amounts.
func bar(th Theme, ratio float64, width int) string {
	if width <= 0 {
		return ""
	}
	ratio = min(max(ratio, 0), 1)
	filled := int(ratio*float64(width) + 0.5)
	if filled == 0 && ratio > 0 {
		filled = 1
	}
	if filled > width {
		filled = width
	}
	return th.Bar.Render(strings.Repeat("█", filled)) + th.BarEmpty.Render(strings.Repeat("░", width-filled))
}
