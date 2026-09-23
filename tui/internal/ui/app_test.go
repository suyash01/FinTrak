package ui

import (
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"

	"github.com/fintrak/client/api"
)

// stubScreen records what it receives, so routing can be asserted directly.
type stubScreen struct {
	title        string
	keys         int
	dataMsg      int
	refreshes    int
	bindings     []key.Binding
	capturesText bool
}

func (s *stubScreen) Title() string        { return s.title }
func (s *stubScreen) Refresh() tea.Cmd     { s.refreshes++; return nil }
func (s *stubScreen) View(int, int) string { return "" }
func (s *stubScreen) Keys() []key.Binding  { return s.bindings }

// CapturesText stands in for an open inline search box.
func (s *stubScreen) CapturesText() bool { return s.capturesText }

func (s *stubScreen) Update(msg tea.Msg) tea.Cmd {
	if _, ok := msg.(tea.KeyMsg); ok {
		s.keys++
		return nil
	}
	s.dataMsg++
	return nil
}

// newAppForTest builds an App with two stub screens and a working context.
func newAppForTest(t *testing.T) (*App, *stubScreen, *stubScreen) {
	t.Helper()
	client, err := api.New("http://127.0.0.1:1/api/v1")
	if err != nil {
		t.Fatalf("api.New: %v", err)
	}
	a, ok := New(client).(*App)
	if !ok {
		t.Fatal("New did not return *App")
	}
	active := &stubScreen{title: "Active"}
	other := &stubScreen{title: "Other"}
	a.signedIn = true
	a.screens = []Screen{active, other}
	a.nav = 0
	a.focus = FocusContent
	return a, active, other
}

// TestCtrlCQuitsWhileSignedOut is a regression test for a defect the user hit:
// while signed out the login form owns every key, and the form's own ctrl+c only
// closes it — so the process could not be quit from the sign-in screen.
func TestCtrlCQuitsWhileSignedOut(t *testing.T) {
	a, _, _ := newAppForTest(t)
	a.signedIn = false
	a.screens = nil
	a.login = NewLoginModel(a.client)

	_, cmd := a.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmd == nil {
		t.Fatal("ctrl+c must quit from the sign-in screen")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Errorf("ctrl+c produced %T, want tea.QuitMsg", cmd())
	}
}

// TestCtrlCQuitsWithModalOpen is a regression test: an open overlay used to
// swallow ctrl+c as "close", so the documented "ctrl+c quits from anywhere"
// needed a second press. The quit check must run before the modal branch.
func TestCtrlCQuitsWithModalOpen(t *testing.T) {
	a, _, _ := newAppForTest(t)
	a.modal = NewHelp(nil, nil)

	_, cmd := a.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmd == nil {
		t.Fatal("ctrl+c must quit even while an overlay is open")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Errorf("ctrl+c produced %T, want tea.QuitMsg", cmd())
	}
}

// TestGlobalKeysYieldToAScreenReadingText covers the search-box defect: the App
// consumed `r`, `g`, the digits and `[`/`]` before the active screen, so those
// characters never reached an inline search box and the query silently lost them.
func TestGlobalKeysYieldToAScreenReadingText(t *testing.T) {
	a, active, _ := newAppForTest(t)

	// With no search box open, `r` is the refresh binding.
	a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	if active.refreshes != 1 {
		t.Fatalf("refreshes = %d, want 1", active.refreshes)
	}
	if active.keys != 0 {
		t.Fatalf("the global binding must not reach the screen: keys = %d", active.keys)
	}

	// With one open, the same key is a character in the query.
	active.capturesText = true
	a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	if active.refreshes != 1 {
		t.Errorf("a typed character was consumed as a refresh: refreshes = %d", active.refreshes)
	}
	if active.keys != 1 {
		t.Errorf("the key never reached the search box: keys = %d", active.keys)
	}

	// A digit jumps screens globally; inside a search box it is text.
	a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
	if a.nav != 0 {
		t.Errorf("a digit jumped screens while a search box was open: nav = %d", a.nav)
	}
	if active.keys != 2 {
		t.Errorf("keys = %d, want 2", active.keys)
	}
}

// TestSessionExpiryLetsTheUserSignInAgain is a regression test for a defect the
// user hit: the login model still reported the previous successful sign-in as
// done, so the next key "completed" that stale sign-in and the sign-in screen
// could never sign in for real.
func TestSessionExpiryLetsTheUserSignInAgain(t *testing.T) {
	a, _, _ := newAppForTest(t)
	a.login = NewLoginModel(a.client)
	a.login.done = true // what a successful sign-in leaves behind

	a.Update(loaded[*RefData]{tag: "app.refdata", err: &api.APIError{Status: 401}})

	if a.signedIn {
		t.Fatal("a 401 must return to the sign-in screen")
	}
	if a.login.Done() {
		t.Fatal("the sign-in screen still reports the expired session as signed in")
	}
	// Any key afterwards must not re-complete the stale sign-in.
	a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	if a.signedIn {
		t.Error("the expired session was completed again instead of signing in")
	}
}

// TestLoginFormSurvivesEsc is a regression test for a defect the user hit:
// pressing esc at the sign-in screen closed the form, and a closed form ignores
// every later key, so the screen could neither sign in nor be quit.
func TestLoginFormSurvivesEsc(t *testing.T) {
	a, _, _ := newAppForTest(t)
	a.signedIn = false
	a.screens = nil
	a.login = NewLoginModel(a.client)

	a.Update(tea.KeyMsg{Type: tea.KeyEsc})
	a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})

	if got := a.login.form().Value("Email"); got != "a" {
		t.Errorf("the sign-in form ignored input after esc: email = %q", got)
	}
}

// TestKeysReachOnlyTheActiveScreen is a regression test for a real defect: the
// App used to broadcast every message, and because a key press carries no tag,
// one press moved every screen's cursor and let two screens answer it (whichever
// opened a modal last won).
func TestKeysReachOnlyTheActiveScreen(t *testing.T) {
	a, active, other := newAppForTest(t)

	if _, cmd := a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}}); cmd != nil {
		t.Fatalf("unexpected command: %v", cmd)
	}
	if active.keys != 1 {
		t.Errorf("active screen saw %d keys, want 1", active.keys)
	}
	if other.keys != 0 {
		t.Errorf("inactive screen saw %d keys, want 0", other.keys)
	}
}

// TestKeysAreIgnoredWhileTheSidebarHasFocus keeps arrow keys from driving a
// screen while the user is navigating the sidebar.
func TestKeysAreIgnoredWhileTheSidebarHasFocus(t *testing.T) {
	a, active, other := newAppForTest(t)
	a.focus = FocusNav

	a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})

	if active.keys != 0 || other.keys != 0 {
		t.Errorf("screens saw keys while the sidebar had focus: active=%d other=%d", active.keys, other.keys)
	}
}

// TestDataMessagesStillReachEveryScreen keeps the other half of the contract: a
// load that finishes while the user is on another screen must still land, which
// is why tagged messages are broadcast and each screen filters by tag.
func TestDataMessagesStillReachEveryScreen(t *testing.T) {
	a, active, other := newAppForTest(t)

	a.Update(loaded[[]api.TagCount]{tag: "other.list"})

	if active.dataMsg != 1 || other.dataMsg != 1 {
		t.Errorf("data message reached active=%d other=%d, want 1 and 1", active.dataMsg, other.dataMsg)
	}
}

// TestSignInResponseReachesTheLoginScreen is a regression test for a defect the
// end-to-end run caught: the sign-in response was broadcast to the screen list,
// which is empty until the reference data loads, so the login form never left
// its "submitting" state and the session could never start.
func TestSignInResponseReachesTheLoginScreen(t *testing.T) {
	a, _, _ := newAppForTest(t)
	a.signedIn = false
	a.screens = nil
	a.freshAt = map[int]uint64{}
	a.login = NewLoginModel(a.client)

	_, cmd := a.Update(loaded[api.User]{tag: "login", data: api.User{ID: "u1", Email: "a@b.c", Role: "admin"}})

	if !a.signedIn {
		t.Fatal("the sign-in response never reached the login screen")
	}
	if cmd == nil {
		t.Error("signing in must start the reference-data load")
	}
	if got := a.login.User().Email; got != "a@b.c" {
		t.Errorf("session user = %q", got)
	}
}

// TestSidebarArrowsKeepFocusInTheSidebar is a regression test for a defect the
// user hit: every sidebar arrow went through a helper that also moved the
// keyboard into the content pane, so tab reached the sidebar and the first arrow
// press left it again — the sidebar could never actually be browsed.
func TestSidebarArrowsKeepFocusInTheSidebar(t *testing.T) {
	a, _, _ := newAppForTest(t)
	a.focus = FocusNav
	a.nav = 0

	a.Update(tea.KeyMsg{Type: tea.KeyDown})

	if a.nav != 1 {
		t.Errorf("sidebar selection = %d, want 1", a.nav)
	}
	if a.focus != FocusNav {
		t.Fatal("browsing the sidebar must not move the keyboard into the content pane")
	}

	// Walking further down and back up stays in the sidebar.
	a.Update(tea.KeyMsg{Type: tea.KeyDown})
	a.Update(tea.KeyMsg{Type: tea.KeyUp})
	if a.focus != FocusNav || a.nav != 1 {
		t.Errorf("focus=%v nav=%d, want the sidebar still focused on 1", a.focus, a.nav)
	}
}

// TestTabTogglesBetweenSidebarAndContent covers the way out of a screen.
func TestTabTogglesBetweenSidebarAndContent(t *testing.T) {
	a, _, _ := newAppForTest(t)
	if a.focus != FocusContent {
		t.Fatalf("a fresh app should focus the content pane, got %v", a.focus)
	}
	tab := tea.KeyMsg{Type: tea.KeyTab}

	a.Update(tab)
	if a.focus != FocusNav {
		t.Fatal("tab must reach the sidebar")
	}
	a.Update(tab)
	if a.focus != FocusContent {
		t.Fatal("tab must return to the content pane")
	}
}

// TestDeliberateJumpEntersTheScreen distinguishes a jump (digit, ] or [, the
// go-to list) from browsing: the user asked for that screen, so the keyboard
// follows.
func TestDeliberateJumpEntersTheScreen(t *testing.T) {
	a, _, _ := newAppForTest(t)
	a.focus = FocusNav
	a.nav = 0

	a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("2")})
	if a.nav != 1 || a.focus != FocusContent {
		t.Errorf("digit jump left nav=%d focus=%v, want 1 and content", a.nav, a.focus)
	}
}

// TestStatusLineGivesTheHintsRoomAndASeparator checks the reported defect: the
// separator was only added for informational messages, so "signed in" run into
// "f filter" and read as "signed inf filter".
func TestStatusLineGivesTheHintsRoomAndASeparator(t *testing.T) {
	a, active, _ := newAppForTest(t)
	active.bindings = []key.Binding{key.NewBinding(key.WithKeys("n"), key.WithHelp("n", "new"))}
	a.width, a.height = 120, 24
	a.status, a.level = "signed in", LevelSuccess

	line := ansi.Strip(a.renderStatus(120))
	if !strings.Contains(line, "signed in") {
		t.Fatalf("status message missing from %q", line)
	}
	if !strings.Contains(line, "n new") {
		t.Errorf("key hints missing from %q", line)
	}
	if !strings.Contains(line, "tab sidebar") {
		t.Errorf("the status bar must advertise the way back to the sidebar: %q", line)
	}

	rest := line[strings.Index(line, "signed in")+len("signed in"):]
	gap := len(rest) - len(strings.TrimLeft(rest, " |"))
	if gap < 3 {
		t.Errorf("status and hints are %d columns apart in %q, want a visible gap", gap, line)
	}
	if strings.Contains(line, "signed inf") {
		t.Errorf("the message still runs into the hints: %q", line)
	}
}

// TestStatusHintNamesTheDestination keeps the focus hint honest about where tab
// goes, so a screen never looks like a trap.
func TestStatusHintNamesTheDestination(t *testing.T) {
	a, _, _ := newAppForTest(t)

	a.focus = FocusContent
	if bindings := a.statusBindings(); !strings.Contains(bindings[0].Help().Desc, "sidebar") {
		t.Errorf("in the content pane the hint should point at the sidebar, got %q", bindings[0].Help().Desc)
	}
	a.focus = FocusNav
	if bindings := a.statusBindings(); !strings.Contains(bindings[0].Help().Desc, "content") {
		t.Errorf("in the sidebar the hint should point at the content, got %q", bindings[0].Help().Desc)
	}
}

// TestStatusBarHidesDeadKeys keeps the bar honest: while the sidebar has the
// keyboard, the screen's own keys do nothing, so they must not be advertised —
// and the sidebar's navigation must be.
func TestStatusBarHidesDeadKeys(t *testing.T) {
	a, active, _ := newAppForTest(t)
	active.bindings = []key.Binding{key.NewBinding(key.WithKeys("n"), key.WithHelp("n", "new"))}
	a.status, a.level = "", LevelInfo

	a.focus = FocusContent
	inContent := ansi.Strip(a.renderStatus(120))
	if !strings.Contains(inContent, "n new") {
		t.Errorf("the screen's keys should be advertised while it has the keyboard: %q", inContent)
	}

	a.focus = FocusNav
	inSidebar := ansi.Strip(a.renderStatus(120))
	if strings.Contains(inSidebar, "n new") {
		t.Errorf("the screen's keys are dead while the sidebar is focused: %q", inSidebar)
	}
	for _, want := range []string{"up", "down", "enter content"} {
		if !strings.Contains(inSidebar, want) {
			t.Errorf("the sidebar's own navigation should be advertised; %q missing from %q", want, inSidebar)
		}
	}
	// `enter` in the sidebar hands the keyboard to the content pane; it does not
	// open a detail overlay, so the bar must not label it "details" (the label
	// used to be reused from the screens' binding and described the wrong action).
	if strings.Contains(inSidebar, "enter details") {
		t.Errorf("the sidebar's enter key is mislabelled: %q", inSidebar)
	}
	if !strings.Contains(inSidebar, "tab content") {
		t.Errorf("the way into the content pane should be advertised: %q", inSidebar)
	}
}

// TestShowingAScreenReloadsWhenItsDataIsStale pins the fix for the other half of
// the stale-page report: a screen used to load only on its first visit, so a
// write made on one screen left every previously visited screen showing old
// data (edit a transaction, go back to the dashboard, see the old total).
func TestShowingAScreenReloadsWhenItsDataIsStale(t *testing.T) {
	a, active, other := newAppForTest(t)
	a.focus = FocusNav

	a.selectScreen(0)
	a.selectScreen(1)
	if active.refreshes != 1 || other.refreshes != 1 {
		t.Fatalf("each screen should load on its first visit, got %d and %d", active.refreshes, other.refreshes)
	}

	a.selectScreen(0)
	if active.refreshes != 1 {
		t.Errorf("re-showing a screen with current data refetched it (%d loads)", active.refreshes)
	}

	// A successful write anywhere makes every screen stale.
	a.nav = 0
	a.Update(done{tag: "txn.save", note: "transaction updated"})

	a.selectScreen(1)
	if other.refreshes != 2 {
		t.Errorf("a screen visited before the write showed stale data (%d loads, want 2)", other.refreshes)
	}
	a.selectScreen(0)
	if active.refreshes != 2 {
		t.Errorf("the screen the write happened on was not reloaded (%d loads, want 2)", active.refreshes)
	}
}

// TestFailedWriteLeavesEveryScreenFresh keeps a rejected save from triggering a
// pointless refetch storm across the sidebar.
func TestFailedWriteLeavesEveryScreenFresh(t *testing.T) {
	a, active, other := newAppForTest(t)
	a.focus = FocusNav
	a.selectScreen(0)
	a.selectScreen(1)

	a.nav = 0
	a.Update(done{tag: "txn.save", err: errors.New("amount: must be positive")})

	a.selectScreen(1)
	if other.refreshes != 1 {
		t.Errorf("a failed write invalidated another screen (%d loads, want 1)", other.refreshes)
	}
	a.selectScreen(0)
	if active.refreshes != 1 {
		t.Errorf("a failed write invalidated the current screen (%d loads, want 1)", active.refreshes)
	}
}

// TestAppStylesWithTheSessionRenderer pins the App half of the SSH colour fix: the
// model builds its palette (and the one its screens share through the context)
// from the renderer handed in for the session's terminal, rather than from the
// process-wide default.
func TestAppStylesWithTheSessionRenderer(t *testing.T) {
	client, err := api.New("http://127.0.0.1:1/api/v1")
	if err != nil {
		t.Fatalf("api.New: %v", err)
	}
	r := lipgloss.NewRenderer(io.Discard)
	r.SetColorProfile(termenv.ANSI256)
	r.SetHasDarkBackground(true)

	a, ok := NewWithRenderer(client, r).(*App)
	if !ok {
		t.Fatal("NewWithRenderer did not return *App")
	}
	if got := a.theme.Negative.Render("x"); !strings.Contains(got, "\x1b[") {
		t.Errorf("the App did not style with the session renderer: %q", got)
	}
	if a.ctx.Theme.Negative.Render("x") != a.theme.Negative.Render("x") {
		t.Error("the screens' context does not carry the session's theme")
	}
}

// TestFailedReferenceDataLoadIsRetryable is a regression test for a defect: the
// global-key handler returned before its switch whenever no screens existed, and
// the screens are only ever built by a *successful* reference-data load. One
// failed load therefore left the session on its error card with no working key at
// all — not a retry, not a sign-out, not even the help overlay — so the only way
// out was to quit and reconnect.
func TestFailedReferenceDataLoadIsRetryable(t *testing.T) {
	a, _, _ := newAppForTest(t)
	a.screens = nil

	a.Update(loaded[*RefData]{tag: "app.refdata", err: errors.New("backend restarting")})
	if a.refErr == nil {
		t.Fatal("the failed load was not recorded, so there is nothing to retry")
	}

	_, cmd := a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	if cmd == nil {
		t.Fatal("`r` did nothing after a failed reference-data load: the session is a dead end")
	}

	// The retry is the load that builds the screens: delivering its success
	// leaves a usable workspace.
	a.Update(loaded[*RefData]{tag: "app.refdata", data: &RefData{AccountsByID: map[string]api.Account{}}})
	if len(a.screens) == 0 {
		t.Error("a successful retry did not build the screens")
	}
	if a.refErr != nil {
		t.Errorf("the retry left the load error in place: %v", a.refErr)
	}
}

// TestSignOutWorksWithoutScreens keeps the way out of the failed-load state
// open: with no screens there is no sidebar to reach the sign-out binding from,
// so the key has to work on its own.
func TestSignOutWorksWithoutScreens(t *testing.T) {
	a, _, _ := newAppForTest(t)
	a.screens = nil
	a.refErr = errors.New("backend restarting")

	a.Update(tea.KeyMsg{Type: tea.KeyCtrlO})

	if a.signedIn {
		t.Fatal("ctrl+o did not sign out of a session with no screens")
	}
	if a.login == nil || a.login.form() == nil {
		t.Fatal("signing out left no sign-in form to type into")
	}
}

// TestSessionExpiryDropsTheOpenOverlay is a regression test for a defect: the
// expiry path tore down the workspace but not the modal, and the App hands every
// key to an open modal before anything else — so the sign-in card was displayed
// while the keyboard belonged to an invisible overlay (ctrl+c could not even quit
// until esc happened to close it).
func TestSessionExpiryDropsTheOpenOverlay(t *testing.T) {
	a, _, _ := newAppForTest(t)
	a.modal = NewHelp(nil, nil)

	a.Update(loaded[*RefData]{tag: "app.refdata", err: &api.APIError{Status: 401}})

	if a.modal != nil {
		t.Fatal("the overlay survived the session expiry: it keeps consuming keys")
	}
	// The keys reach the sign-in form again.
	a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	if got := a.login.form().Value("Email"); got != "a" {
		t.Errorf("the sign-in form did not receive the key: email = %q", got)
	}
}

// TestQuitWorksFromEitherPane is a regression test for a defect: `q` was bound as
// a global quit and advertised as one in the key reference, but the handler only
// honoured it while the sidebar held the keyboard — so in the content pane, the
// pane that has focus by default, the advertised key did nothing.
func TestQuitWorksFromEitherPane(t *testing.T) {
	for _, focus := range []Focus{FocusContent, FocusNav} {
		a, _, _ := newAppForTest(t)
		a.focus = focus

		_, cmd := a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
		if cmd == nil {
			t.Fatalf("`q` did not quit with focus=%v", focus)
		}
		if _, ok := cmd().(tea.QuitMsg); !ok {
			t.Errorf("`q` with focus=%v produced %T, want tea.QuitMsg", focus, cmd())
		}
	}

	// It works from the failed-load card too, which has no screens to address.
	a, _, _ := newAppForTest(t)
	a.screens = nil
	a.refErr = errors.New("backend restarting")

	_, cmd := a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	if cmd == nil {
		t.Fatal("`q` did not quit from the reference-data error card")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Errorf("`q` on the error card produced %T, want tea.QuitMsg", cmd())
	}
}
