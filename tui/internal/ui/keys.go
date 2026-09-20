package ui

import (
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/x/ansi"
)

// KeyMap holds the bindings the App itself handles, before a screen sees the
// key. Screens declare their own bindings for the actions they own.
type KeyMap struct {
	Quit       key.Binding
	Help       key.Binding
	Refresh    key.Binding
	FocusNext  key.Binding
	ScreenNext key.Binding
	ScreenPrev key.Binding
	Up         key.Binding
	Down       key.Binding
	Filter     key.Binding
	New        key.Binding
	Edit       key.Binding
	Delete     key.Binding
	Detail     key.Binding
	Back       key.Binding
	PageUp     key.Binding
	PageDown   key.Binding
	Confirm    key.Binding
	GotoScreen key.Binding
	SignOut    key.Binding
}

// DefaultKeyMap is the binding set the App installs.
func DefaultKeyMap() KeyMap {
	return KeyMap{
		Quit:       key.NewBinding(key.WithKeys("q", "ctrl+c"), key.WithHelp("q", "quit")),
		Help:       key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "help")),
		Refresh:    key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "refresh")),
		FocusNext:  key.NewBinding(key.WithKeys("tab"), key.WithHelp("tab", "switch pane")),
		ScreenNext: key.NewBinding(key.WithKeys("]"), key.WithHelp("]", "next screen")),
		ScreenPrev: key.NewBinding(key.WithKeys("["), key.WithHelp("[", "prev screen")),
		Up:         key.NewBinding(key.WithKeys("up", "k"), key.WithHelp("↑/k", "up")),
		Down:       key.NewBinding(key.WithKeys("down", "j"), key.WithHelp("↓/j", "down")),
		Filter:     key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "filter")),
		New:        key.NewBinding(key.WithKeys("n"), key.WithHelp("n", "new")),
		Edit:       key.NewBinding(key.WithKeys("e"), key.WithHelp("e", "edit")),
		Delete:     key.NewBinding(key.WithKeys("d"), key.WithHelp("d", "delete")),
		Detail:     key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "details")),
		Back:       key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "back")),
		PageUp:     key.NewBinding(key.WithKeys("pgup", "ctrl+b"), key.WithHelp("pgup", "page up")),
		PageDown:   key.NewBinding(key.WithKeys("pgdown", "ctrl+f"), key.WithHelp("pgdn", "page down")),
		Confirm:    key.NewBinding(key.WithKeys("y"), key.WithHelp("y", "confirm")),
		GotoScreen: key.NewBinding(key.WithKeys("g"), key.WithHelp("g", "jump to screen")),
		SignOut:    key.NewBinding(key.WithKeys("ctrl+o"), key.WithHelp("ctrl+o", "sign out")),
	}
}

// HelpRow is one key/description pair for the status bar and help overlay.
type HelpRow struct {
	Keys string
	Desc string
}

// helpRows flattens bindings into display rows, skipping disabled ones.
func helpRows(bindings []key.Binding) []HelpRow {
	rows := make([]HelpRow, 0, len(bindings))
	for _, b := range bindings {
		if !b.Enabled() {
			continue
		}
		h := b.Help()
		if h.Key == "" {
			continue
		}
		rows = append(rows, HelpRow{Keys: h.Key, Desc: h.Desc})
	}
	return rows
}

// renderBindings draws the status-bar hint line, dropping hints that do not fit
// rather than wrapping.
func renderBindings(th Theme, width int, bindings []key.Binding) string {
	rows := helpRows(bindings)
	var parts []string
	used := 0
	for i, row := range rows {
		part := th.Key.Render(row.Keys) + " " + th.KeyDesc.Render(row.Desc)
		length := ansi.StringWidth(part) + 2
		if used+length > width && len(parts) > 0 {
			if i < len(rows)-1 {
				parts = append(parts, th.Subtle.Render("…"))
			}
			break
		}
		parts = append(parts, part)
		used += length
	}
	return strings.Join(parts, th.Subtle.Render("  "))
}
