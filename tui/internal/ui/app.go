package ui

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/fintrak/client/api"
)

// Focus is which pane owns the arrow keys.
type Focus int

// Panes.
const (
	FocusNav Focus = iota
	FocusContent
)

// Modal is an overlay that takes every key while open.
type Modal interface {
	Update(tea.Msg) tea.Cmd
	View(th Theme, width, height int) string
	Closed() bool
	Canceled() bool
}

// App is the root model: it owns the session, the shared reference-data cache,
// the sidebar and any open modal, and forwards messages to the screens.
type App struct {
	client *api.Client
	keys   KeyMap
	theme  Theme

	width  int
	height int

	// session
	login    *LoginModel
	signedIn bool
	ref      *RefData
	refErr   error
	ctx      *Ctx

	screens []Screen
	nav     int
	focus   Focus

	// freshAt records the data generation each screen last loaded at, and gen
	// advances on every successful write. A screen shown at an older generation
	// reloads, so a change made anywhere cannot leave a previously visited screen
	// displaying stale rows — a first-visit-only load was exactly that bug.
	freshAt map[int]uint64
	gen     uint64

	modal  Modal
	status string
	level  Level

	quitting bool
}

// New builds the root model for a locally run terminal, where lipgloss's
// package-level renderer already describes the terminal the process writes to.
func New(client *api.Client) tea.Model { return NewWithRenderer(client, nil) }

// NewWithRenderer builds the root model with a renderer for the terminal it will
// draw on. The SSH door serves many terminals from one process, so each session
// passes the renderer wishtea made for its own pty: the door's own environment is
// not the client's, and a shared package-level renderer would let one session's
// colour profile leak into another's. A nil renderer means the local terminal.
func NewWithRenderer(client *api.Client, r *lipgloss.Renderer) tea.Model {
	ref := &RefData{}
	ctx := &Ctx{Client: client, Ref: ref}
	ctx.Notify = func(level Level, format string, args ...any) {
		// The App installs the real sink in Init; screens built before that
		// still have a working (dropping) target.
		_ = level
		_ = fmt.Sprintf(format, args...)
	}
	a := &App{
		client:  client,
		keys:    DefaultKeyMap(),
		theme:   ThemeFor(r),
		login:   NewLoginModel(client),
		ref:     ref,
		ctx:     ctx,
		freshAt: map[int]uint64{},
		gen:     1,
		// A client that already holds tokens (the SSH door exchanges the
		// credentials for a session before the program starts) goes straight to
		// the workspace; without this the sign-in screen would be shown over a
		// perfectly valid session and never leave it.
		signedIn: client.HasSession(),
	}
	ctx.Theme = a.theme
	ctx.Notify = a.notify
	ctx.Open = func(m Modal) { a.modal = m }
	return a
}

// notify writes a transient message to the status line.
func (a *App) notify(level Level, format string, args ...any) {
	a.status = fmt.Sprintf(format, args...)
	a.level = level
}

// Init starts the first fetch: the reference data if a session already exists
// (an embedded token, say), otherwise the login screen.
func (a *App) Init() tea.Cmd {
	if a.client.HasSession() {
		return a.startSession()
	}
	return a.login.Init()
}

// refDataMsg carries a refreshed reference-data snapshot.
type refDataMsg struct {
	ref *RefData
	err error
}

// startSession loads the shared lookups and then activates the first screen.
func (a *App) startSession() tea.Cmd {
	return load("app.refdata", func(ctx context.Context) (*RefData, error) {
		return fetchRefData(ctx, a.client)
	})
}

// buildScreens creates one instance of every registered screen. They are created
// once and kept, so each screen's cursor and filters survive navigation.
func (a *App) buildScreens() {
	a.screens = buildRegisteredScreens(a.ctx)
}

// Update implements tea.Model.
func (a *App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	model, cmd := a.update(msg)
	if a.modal != nil && a.modal.Closed() {
		a.modal = nil
	}
	return model, cmd
}

// update resolves one message.
func (a *App) update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		a.width, a.height = m.Width, m.Height
		return a, nil

	case tea.KeyMsg:
		// ctrl+c quits from anywhere, before any model or modal can swallow it:
		// while signed out the login form owns every key, and its own ctrl+c
		// merely closes the form — which left the process unquittable from the
		// sign-in screen; an open overlay also handles ctrl+c as "close", which
		// would otherwise demand a second ctrl+c to quit.
		if m.String() == "ctrl+c" {
			return a, tea.Quit
		}
		if a.modal != nil {
			return a, a.modal.Update(m)
		}
		// A screen that is reading text owns the keyboard: `r`, `g`, `[`/`]` and
		// the digits are characters in a search box there, not navigation.
		if a.signedIn && !capturesText(a.screens, a.nav) {
			if cmd, handled := a.handleGlobalKey(m); handled {
				return a, cmd
			}
		}
	}

	// While signed out the login screen owns every message, including the
	// response to the sign-in call it started: no screens exist yet, so a data
	// message would otherwise be broadcast to an empty list and the form would
	// sit there forever.
	if !a.signedIn {
		cmd := a.login.Update(msg)
		if a.login.Done() {
			return a, a.completeLogin()
		}
		return a, cmd
	}

	switch m := msg.(type) {
	case loaded[*RefData]:
		if m.tag != "app.refdata" {
			break
		}
		if m.err != nil {
			if api.Unauthorized(m.err) {
				// The session died (30-day deadline, or the server restarted
				// with a new secret): fall back to the login screen.
				return a, a.signOut("session expired — sign in again", LevelError)
			}
			a.refErr = m.err
			a.status, a.level = m.err.Error(), LevelError
			return a, nil
		}
		// Mutate the existing cache in place: the screens hold this pointer.
		*a.ref = *m.data
		a.refErr = nil
		a.ctx.User = m.data.Me
		if len(a.screens) == 0 {
			a.buildScreens()
		}
		return a, a.refreshActive()

	case done:
		if a.modal != nil {
			if form, ok := a.modal.(*Form); ok && form.Tag == m.tag {
				if m.err != nil {
					form.SetError(m.err)
					a.status, a.level = m.err.Error(), LevelError
					return a, nil
				}
				a.modal = nil
			}
		}
		if m.err != nil {
			a.status, a.level = m.err.Error(), LevelError
		} else {
			if m.note != "" {
				a.status, a.level = m.note, LevelSuccess
			}
			// A write can be visible beyond the screen that made it — a new
			// transaction moves the dashboard, the calendar and the flow graph —
			// so every screen counts as stale until it is shown again. The screen
			// that performed the write reloads itself from this same message.
			a.gen++
			a.freshAt = map[int]uint64{}
		}
		var cmds []tea.Cmd
		if m.invalidate {
			cmds = append(cmds, a.startSession())
		}
		cmds = append(cmds, a.forward(msg)...)
		return a, tea.Batch(cmds...)
	}

	// Keys go to the active screen only. A key press carries no tag, so
	// broadcasting it would move every screen's cursor and let two screens
	// answer the same press; data messages, which are tagged, are still
	// broadcast so a load that finishes while the user is elsewhere lands.
	if _, isKey := msg.(tea.KeyMsg); isKey {
		return a, a.updateActive(msg)
	}
	return a, tea.Batch(a.forward(msg)...)
}

// updateActive delivers a message to the visible screen, if it owns the
// keyboard. Keys are ignored while the sidebar has focus.
func (a *App) updateActive(msg tea.Msg) tea.Cmd {
	if len(a.screens) == 0 || a.focus != FocusContent {
		return nil
	}
	return a.screens[a.nav].Update(msg)
}
func (a *App) forward(msg tea.Msg) []tea.Cmd {
	var cmds []tea.Cmd
	for _, s := range a.screens {
		if cmd := s.Update(msg); cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	return cmds
}

// handleGlobalKey processes the App's own bindings, reporting whether the key was
// consumed. Unconsumed keys move the sidebar cursor or fall through to the active
// screen. ctrl+c is handled by update before this runs, so it reaches the program
// even while a modal or the sign-in screen owns the keyboard.
func (a *App) handleGlobalKey(key tea.KeyMsg) (tea.Cmd, bool) {
	// Quit, retry and sign-out are live before the screens exist, because that is
	// the state a failed reference-data load leaves behind: the session is signed
	// in but has no screens, so every binding used to be swallowed by the guard
	// below and the error card was a dead end. The bindings after it all address
	// a screen, so they still need one.
	switch {
	case keyMatches(a.keys.Quit, key):
		// `q` quits from either pane: it is advertised as a global key and no
		// screen binds it, so the content pane — the pane that has the keyboard
		// by default — must honour it as well.
		return tea.Quit, true
	case keyMatches(a.keys.Refresh, key):
		if len(a.screens) == 0 {
			return a.startSession(), true
		}
		return a.refreshActive(), true
	case keyMatches(a.keys.SignOut, key):
		return a.signOut("signed out", LevelInfo), true
	}

	if len(a.screens) == 0 {
		return nil, false
	}
	switch {
	case keyMatches(a.keys.Help, key):
		a.modal = NewHelp(helpRows(a.globalBindings()), helpRows(a.screens[a.nav].Keys()))
		return nil, true
	case keyMatches(a.keys.FocusNext, key):
		if a.focus == FocusNav {
			a.focus = FocusContent
		} else {
			a.focus = FocusNav
		}
		return nil, true
	case keyMatches(a.keys.ScreenNext, key):
		return a.goToScreen(a.nav + 1), true
	case keyMatches(a.keys.ScreenPrev, key):
		return a.goToScreen(a.nav - 1), true
	case keyMatches(a.keys.GotoScreen, key):
		options := make([]Option, 0, len(a.screens))
		for i, s := range a.screens {
			options = append(options, Option{Value: fmt.Sprint(i), Label: s.Title()})
		}
		picker := NewPicker("Go to screen", options, fmt.Sprint(a.nav), false, "")
		picker.OnSelect = func(value string) tea.Cmd {
			index, err := strconv.Atoi(value)
			if err != nil {
				return nil
			}
			return a.goToScreen(index)
		}
		a.modal = picker
		return nil, true
	}

	// Number keys jump straight to a screen and into it.
	if n := digitIndex(key.String()); n >= 0 && n < len(a.screens) {
		return a.goToScreen(n), true
	}

	if a.focus == FocusNav {
		// The sidebar's keys come from the same bindings the status bar
		// advertises, so the hint cannot drift from what the pane actually does.
		switch {
		case keyMatches(a.keys.Up, key):
			return a.selectScreen(a.nav - 1), true
		case keyMatches(a.keys.Down, key):
			return a.selectScreen(a.nav + 1), true
		case keyMatches(a.keys.SidebarEnter, key):
			a.focus = FocusContent
			return nil, true
		}
	}
	return nil, false
}

// selectScreen moves the sidebar selection, wrapping at the ends, and reloads
// the newly shown screen when its data is stale. It deliberately leaves the
// keyboard focus alone: browsing the sidebar with the arrow keys used to hand
// focus to the content pane on the first press, which made the sidebar
// impossible to use — tab reached it, and the next arrow left it.
func (a *App) selectScreen(index int) tea.Cmd {
	if len(a.screens) == 0 {
		return nil
	}
	n := len(a.screens)
	a.nav = ((index % n) + n) % n
	if a.freshAt[a.nav] == a.gen {
		return nil
	}
	a.freshAt[a.nav] = a.gen
	return a.screens[a.nav].Refresh()
}

// goToScreen selects a screen and moves the keyboard into it, which is what a
// deliberate jump means (a digit, [/], or picking from the go-to list).
func (a *App) goToScreen(index int) tea.Cmd {
	cmd := a.selectScreen(index)
	a.focus = FocusContent
	return cmd
}

// refreshActive reloads the visible screen and marks it current.
func (a *App) refreshActive() tea.Cmd {
	if len(a.screens) == 0 {
		return nil
	}
	a.freshAt[a.nav] = a.gen
	return a.screens[a.nav].Refresh()
}

// signOut tears the signed-in workspace down and returns the sign-in screen,
// showing reason on the status line. Everything the session owned goes with it:
// the screens, the cache's load markers, and any open overlay — an overlay left
// behind keeps taking every key for a screen that no longer exists, so the
// sign-in card is displayed but the typing goes nowhere (esc happens to close
// every modal, which is the only reason that state looked like a hang rather
// than being one).
func (a *App) signOut(reason string, level Level) tea.Cmd {
	a.signedIn = false
	a.client.ClearSession()
	a.modal = nil
	a.screens = nil
	a.freshAt = map[int]uint64{}
	// The old load error belongs to the session that is ending; keeping it would
	// put the error card back on screen during the next sign-in's load.
	a.refErr = nil
	a.status, a.level = reason, level
	// Reset, not just Init: the model still reports the previous successful
	// sign-in as done, so without this the next key "completes" that stale
	// sign-in again and the screen can never sign in for real.
	a.login.Reset()
	return a.login.Init()
}

// completeLogin finishes the sign-in handshake: the login model has already
// captured the session cookies, so the next step is the shared lookups.
func (a *App) completeLogin() tea.Cmd {
	if err := a.login.Err(); err != nil {
		a.status, a.level = err.Error(), LevelError
		return nil
	}
	a.signedIn = true
	if a.login.Registered() {
		a.status, a.level = "account created", LevelSuccess
	} else {
		a.status, a.level = "signed in", LevelSuccess
	}
	return a.startSession()
}

// capturesText reports whether the screen at index i is reading raw text from
// the keyboard, in which case the App's global bindings must not consume the key
// first.
func capturesText(screens []Screen, i int) bool {
	return i >= 0 && i < len(screens) && screens[i].CapturesText()
}

// View renders the whole program: either the login screen or the workspace.
func (a *App) View() string {
	if a.width == 0 || a.height == 0 {
		return "loading…"
	}
	if !a.signedIn {
		width, height := a.modalBox()
		return a.centerModal(a.login.View(a.theme, width, height))
	}
	if len(a.screens) == 0 {
		if a.refErr != nil {
			// The load failed, so there is no screen to address and no status
			// bar to hint from: the card carries its own way out, because the
			// session cannot be used before the lookups load.
			return a.centerModal(a.theme.Error.Render("cannot load reference data: "+a.refErr.Error()) +
				"\n\n" + renderBindings(a.theme, a.width, []key.Binding{a.keys.Refresh, a.keys.SignOut}))
		}
		return a.centerModal(a.theme.Subtle.Render("loading…"))
	}

	navWidth := 22
	bodyWidth := a.width - navWidth
	bodyHeight := a.height - 2 // status bar + hint line

	body := a.screens[a.nav].View(bodyWidth-2, bodyHeight-1)
	if a.refErr != nil {
		body = a.theme.Error.Render("cannot load reference data: "+a.refErr.Error()) + "\n\n" + body
	}

	layout := joinHorizontal(
		a.renderSidebar(navWidth, bodyHeight),
		navWidth,
		a.box(body, bodyWidth, bodyHeight),
	)

	footer := a.renderStatus(a.width)

	out := layout + "\n" + footer
	if a.modal != nil {
		width, height := a.modalBox()
		return a.overlay(out, a.modal.View(a.theme, width, height))
	}
	return out
}

// renderSidebar draws the screen list and the session footer.
func (a *App) renderSidebar(width, height int) string {
	var b strings.Builder
	b.WriteString(a.theme.Title.Render("FinTrak"))
	b.WriteString("\n")
	b.WriteString(a.theme.Subtle.Render(strings.Repeat("─", max(4, width-2))))
	b.WriteString("\n")

	for i, s := range a.screens {
		label := " " + s.Title()
		switch {
		case i == a.nav && a.focus == FocusNav:
			b.WriteString(a.theme.RowSelected.Render(a.theme.Title.Render(label)))
		case i == a.nav:
			b.WriteString(a.theme.SidebarCurrent.Render(label))
		default:
			b.WriteString(a.theme.SidebarItem.Render(label))
		}
		b.WriteString("\n")
	}

	info := []string{
		"",
		a.theme.Subtle.Render(strings.Repeat("─", max(4, width-2))),
		a.theme.Subtle.Render(truncate(a.ctx.User.Email, width-2)),
		a.theme.Subtle.Render(truncate(a.client.BaseURL(), width-2)),
	}
	if a.ctx.User.Role == "admin" {
		info = append(info, a.theme.WarnText.Render("admin"))
	}
	info = append(info, "", a.theme.Subtle.Render("? help · g jump"))

	lines := strings.Split(b.String(), "\n")
	for _, line := range info {
		if len(lines) >= height {
			break
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

// box pads content into an exact rectangle so the layout does not drift.
func (a *App) box(content string, width, height int) string {
	return trimToBox(content, width, height)
}

// statusSeparator sits between a status message and the key hints. Without it a
// success message runs into the first hint — "signed in" followed by "f filter"
// reads as "signed inf filter".
const statusSeparator = "   │   "

// renderStatus draws the status message and the key hints, giving the hints
// whatever width the message leaves so nothing is silently clipped.
func (a *App) renderStatus(width int) string {
	status, sep, hints := "", "", []key.Binding{}
	hints = append(hints, a.statusBindings()...)

	// Advertise the keys that actually work right now: while the sidebar has the
	// keyboard its own navigation is live and the screen's keys are not, so
	// showing the screen's hints there would describe dead keys.
	if a.focus == FocusNav {
		hints = append(hints, a.keys.Up, a.keys.Down, a.keys.SidebarEnter)
	} else {
		hints = append(hints, a.screens[a.nav].Keys()...)
	}

	used := 0
	if a.status != "" {
		status = a.statusText()
		used += ansi.StringWidth(status)
		sep = a.theme.Subtle.Render(statusSeparator)
		used += ansi.StringWidth(sep)
	}
	return trimToBox(status+sep+renderBindings(a.theme, max(0, width-used), hints), width, 1)
}

// statusBindings are the hints that are always available, ahead of the active
// screen's own keys: how to move between the sidebar and the content, and how to
// open the key reference. The focus hint names its destination rather than the
// current pane, so the way out of a screen is visible on screen.
func (a *App) statusBindings() []key.Binding {
	destination := "sidebar"
	if a.focus == FocusNav {
		destination = "content"
	}
	return []key.Binding{
		key.NewBinding(key.WithKeys("tab"), key.WithHelp("tab", destination)),
		a.keys.Help,
	}
}

// statusText renders the status message in its level's colour.
func (a *App) statusText() string {
	switch a.level {
	case LevelError:
		return a.theme.Error.Render(a.status)
	case LevelSuccess:
		return a.theme.SuccessText.Render(a.status)
	default:
		return a.theme.Subtle.Render(a.status)
	}
}

// globalBindings are the App's own keys, for the help overlay. The focus hint
// comes from statusBindings so the overlay names the same destination the status
// bar does.
func (a *App) globalBindings() []key.Binding {
	out := a.statusBindings()
	out = append(out,
		a.keys.Refresh, a.keys.ScreenNext, a.keys.ScreenPrev,
		a.keys.GotoScreen, a.keys.SignOut, a.keys.Quit,
	)
	if a.focus == FocusNav {
		out = append(out, a.keys.Up, a.keys.Down, a.keys.SidebarEnter)
	}
	return out
}

// modalFrame is the frame a modal is drawn in for this terminal, and modalBox is
// the content area inside it. Both come from the same style, measured rather than
// assumed, so the framed box always fits the screen.
func (a *App) modalFrame() lipgloss.Style { return a.theme.ModalFor(a.height) }

func (a *App) modalBox() (int, int) {
	style := a.modalFrame()
	width := max(20, a.width-style.GetHorizontalFrameSize()-2)
	height := max(3, a.height-style.GetVerticalFrameSize()-1)
	return min(width, 96), height
}

// centerModal renders a modal without an underlying workspace (the login screen).
func (a *App) centerModal(content string) string {
	return centerBlock(content, a.width, a.height)
}

// overlay draws a modal on top of the workspace, keeping the layout stable. The
// body is clipped to the box that exists before it is framed, so no modal can be
// spliced off-screen: an oversized body used to lose its top rows silently, and
// the modal is expected to scroll its own content within this box.
func (a *App) overlay(background, modalBody string) string {
	width, height := a.modalBox()
	// Trailing blank rows are dropped so the frame hugs the content: a widget that
	// needs fewer rows than it was offered should not draw an empty box.
	body := strings.TrimRight(trimToBox(modalBody, width, height), "\n")
	boxed := a.modalFrame().Render(body)
	return replaceCenter(background, boxed, a.width, a.height)
}
