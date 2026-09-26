# Bubble Tea v2 Migration Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Move the `tui/` module from Charm's v1 stack to `charm.land/*/v2`, with no user-visible behaviour change, keeping every hand-rolled widget and the existing layout.

**Architecture:** One atomic lift of the whole module — `go.mod`, every import, the `tea.Model` interface, the key-message types, the theme, `textinput`, and the SSH door — followed by the tests that pin the behaviours v2 changes and the docs. The theme is the only design change: v2 deletes `lipgloss.Renderer`, so the app asks the terminal for its background (`tea.RequestBackgroundColor` → `tea.BackgroundColorMsg`) and resolves the palette with `lipgloss.LightDark`, while colour-profile downsampling moves to the `tea.Program` and the per-session renderer plumbing in the door is deleted.

**Tech Stack:** Go 1.27.1, `charm.land/bubbletea/v2` v2.0.10, `charm.land/bubbles/v2` v2.2.1, `charm.land/lipgloss/v2` v2.0.6, `charm.land/wish/v2` v2.0.4, `charm.land/ssh`, `charm.land/log/v2`, `github.com/charmbracelet/x/ansi`.

**Spec:** `docs/superpowers/specs/2026-09-27-bubbletea-v2-migration-design.md` — read it before starting; this plan argues from it.

## Global Constraints

- The port is **faithful**: no user-visible behaviour change. The hand-rolled `table.go`, `picker.go`, `form.go`, `confirm.go`, `keys.go` and `layout.go` keep their design and rendering.
- `Screen` and `Modal` keep their signatures: `Update(tea.Msg) tea.Cmd`, `View(w, h int) string`, `Keys() []key.Binding`. Only `App.View()` and `errorModel.View()` return `tea.View`.
- `tea.Msg` stays an alias for an empty interface, so `loaded[T]` and `done` are unchanged.
- **A key message is never broadcast.** Keys reach the active screen only; tagged data messages are broadcast to all screens. In v2 `tea.KeyMsg` is an interface covering presses *and* releases, so both must be matched.
- **v2's `Key.String()` returns `"space"`, not `" "`.**
- The theme resolves for a light or dark background from `tea.BackgroundColorMsg`. `compat.AdaptiveColor` is **not** used — it reads a process global, which over SSH answers for the door container rather than the client.
- The workspace **never blocks** on the background-colour answer. The dark theme is in force until the reply lands.
- `DefaultTheme()` keeps its no-argument signature and returns the dark theme.
- Logging uses `log/slog` with typed attrs; never the bare key/value form.
- TUI coverage floor is 18% (`make test-tui-cover-check`); hold it, and raise it only deliberately.
- `go mod tidy` must leave `tui/go.mod` and `tui/go.sum` unchanged — CI enforces this.

## Review Focus

The spec is a vision document; its silence on an input is not permission to break it. These are the five failure modes most likely to bite a real user, each pinned by a test in the task that owns the code:

1. **Space stops toggling a boolean form field.** `form.go` matches `case " ":` today; v2 stringifies space as `"space"`. A user pressing space on a "closed" or "active" toggle gets nothing — no error, no visual change, and the form still submits the old value. The whole suite stays green, because nothing asserts the toggle.
2. **A form's text inputs collapse to one character wide.** `textinput.Width` became private, so a dropped `SetWidth` leaves the field rendering at its default. The user cannot read what they typed and cannot tell which field is focused. A write-only accessor fails silently in exactly the way a removed exported field cannot.
3. **A light-background terminal renders with dark colours, or one SSH client's colours leak to another.** This is the theme seam. The user sees an unreadable or wrong-looking TUI. The leak direction is the dangerous one: it is the exact failure `app.go:69-78` documents the current design as preventing, so a regression here is invisible locally and only shows with two clients on one door.
4. **ctrl+c stops quitting.** The App intercepts it before any modal or the sign-in screen can swallow it, and three existing regression tests exist because it broke before. v2 moves the interception site (`tea.KeyMsg` struct → `tea.KeyPressMsg`); if the interception is left after the modal branch, the process becomes unquittable from the sign-in screen and from any open overlay.
5. **Global navigation dies silently — `q`, `r`, the digits, `[`/`]`, `g`.** All of these match on `msg.String()`. If v2 changed any of those strings, the TUI stops responding to its own chrome with no error and no panic; it just looks frozen. `digitIndex` is the most exposed, because it depends on `String()` returning a bare `"1"`..`"9"`.

---

## File Structure

Nothing is created or deleted. Responsibility is unchanged except in `theme.go`, which loses the renderer and gains the background-colour flag plus the text-input styles.

| File | Change |
|---|---|
| `tui/go.mod` | The four module swaps; `muesli/termenv` dropped by `tidy` |
| `tui/main.go` | Import path; drop `tea.WithAltScreen()` |
| `tui/internal/ui/theme.go` | **The design change.** `ThemeFor(isDark bool)`, `color.Color` palette, `InputStyles`, no renderer |
| `tui/internal/ui/app.go` | `View() tea.View`; key routing; `Init` requests the background; `setTheme`; `ctx.Open` styles forms; `NewWithRenderer` deleted |
| `tui/internal/ui/screen.go` | Import paths only |
| `tui/internal/ui/layout.go` | `keyMatches` takes `tea.KeyPressMsg` |
| `tui/internal/ui/form.go` | `KeyPressMsg`; `case "space"`; `SetWidth`; `SetStyles` |
| `tui/internal/ui/{accounts,calendar,categories,dashboard,import,links,moneyflow,payees,recurring,rules,settings,tags,transactions}.go` | Import paths; `handleKey(tea.KeyPressMsg)`; the four `SetWidth` calls |
| `tui/internal/ui/{confirm,picker,login}.go` | Import paths; `KeyPressMsg` type assertions |
| `tui/internal/sshd/server.go` | `wish/v2` + `charm.land/ssh`; `Middleware`; `sessionRenderer` deleted; `errorModel.View() tea.View` |
| `tui/internal/ui/{app,form,layout,picker,table,theme,login,accounts,links,transactions,screens}_test.go` | Key constructors; the three renderer tests redesigned |

## Task 1: Lift `tui/` onto the v2 stack

This task is atomic, and that is a property of the migration rather than a choice. Three separate things force it:

- `bubbles v1`'s `key.Matches` takes the v1 `tea.KeyMsg` struct, so it cannot accept a v2 `KeyPressMsg`. Bubbles and bubbletea move together or not at all.
- v1 downsampled colour inside `Style.Render` via the per-session renderer; v2 downsamples at the program's output layer. A bubbletea-v2/lipgloss-v1 tree would downsample with the **door process's** profile instead of the session's, which is the leak `app.go:69-78` exists to prevent.
- The door is in the same module, and `wish/bubbletea` v1 is pinned to bubbletea v1, so the door cannot compile against a v2 model. It moves in the same unit.

The steps below are small and ordered so the compiler drives the work; the unit is one review.

**Files:**
- Modify: `tui/go.mod`, `tui/go.sum`
- Modify: `tui/main.go`
- Modify: `tui/internal/ui/theme.go`, `app.go`, `screen.go`, `layout.go`, `form.go`, `confirm.go`, `picker.go`, `login.go`
- Modify: `tui/internal/ui/accounts.go`, `calendar.go`, `categories.go`, `dashboard.go`, `import.go`, `links.go`, `moneyflow.go`, `payees.go`, `recurring.go`, `rules.go`, `settings.go`, `tags.go`, `transactions.go`
- Modify: `tui/internal/sshd/server.go`
- Modify: all 13 `tui/internal/ui/*_test.go`, `tui/internal/sshd/server_test.go`, `tui/internal/sshd/zz_real_test.go`

**Interfaces:**
- Consumes: nothing (first task).
- Produces, for Task 2:
  - `func ThemeFor(isDark bool) Theme`
  - `func DefaultTheme() Theme` — unchanged signature
  - `func (f *Form) SetStyles(s textinput.Styles)`
  - `func (a *App) setTheme(isDark bool)`
  - `func (a *App) view() string` — the old `View` body, unexported
  - `type Theme struct { …; InputStyles textinput.Styles; … }` with `Primary/Muted/Danger/Success/Warn/Border` as `color.Color`
  - test helpers `press(code rune) tea.KeyPressMsg`, `ctrlPress(code rune) tea.KeyPressMsg` (Task 2 adds the release helper it needs)

- [ ] **Step 1: Swap the modules in `go.mod`**

Run from the repo root:

```powershell
cd tui
go get charm.land/bubbletea/v2@v2.0.10 charm.land/bubbles/v2@v2.2.1 charm.land/lipgloss/v2@v2.0.6
go get charm.land/wish/v2@v2.0.4 charm.land/ssh@v0.4.3 charm.land/log/v2@v2.0.1
```

`charm.land/log/v2` is required because `wish/v2` uses it, not because this module imports it. Do not add it to the `require` block by hand; `go mod tidy` decides.

- [ ] **Step 2: Rewrite every import path**

Order matters: `bubbles` and `bubbletea` share a prefix, and `wish/bubbletea` must become `wish/v2/bubbletea`, so the longer paths go first.

```powershell
cd tui
$map = [ordered]@{
  'github.com/charmbracelet/wish/bubbletea'          = 'charm.land/wish/v2/bubbletea'
  'github.com/charmbracelet/wish'                    = 'charm.land/wish/v2'
  'github.com/charmbracelet/bubbles/'                = 'charm.land/bubbles/v2/'
  'github.com/charmbracelet/bubbletea'               = 'charm.land/bubbletea/v2'
  'github.com/charmbracelet/lipgloss'                = 'charm.land/lipgloss/v2'
  'github.com/charmbracelet/ssh'                     = 'charm.land/ssh'
}
$files = git grep -l 'github.com/charmbracelet' -- .
foreach ($f in $files) {
  $text = Get-Content -Raw $f
  foreach ($k in $map.Keys) { $text = $text.Replace($k, $map[$k]) }
  Set-Content -NoNewline -Path $f -Value $text
}
```

`github.com/charmbracelet/x/ansi` and `github.com/charmbracelet/colorprofile` are **not** in the map and must be left alone. `links.go` imports bubbletea without an alias; a path-based rewrite is safe because the v2 package is still named `tea`.

- [ ] **Step 3: Remove `muesli/termenv` and re-tidy**

```powershell
cd tui
go mod tidy
```

Expected: `muesli/termenv` disappears from `go.mod`. If it does not, a `termenv` import survives somewhere — find it with `git grep -n 'muesli/termenv'`.

- [ ] **Step 4: Survey the damage the compiler reports**

```powershell
cd tui
go build ./... 2>&1 | Select-Object -First 60
```

Expect errors in five clusters: `tea.KeyMsg` uses, `View` signatures, `tea.WithAltScreen`, `lipgloss.AdaptiveColor`/`Renderer`, and `textinput` `Width`. Work through them in the order the remaining steps give, re-running this after each cluster.

- [ ] **Step 5: `keyMatches` and the 21 `handleKey` signatures**

`tui/internal/ui/layout.go:71-74`:

```go
// keyMatches reports whether a key press satisfies a binding.
func keyMatches(binding key.Binding, msg tea.KeyPressMsg) bool {
	return binding.Enabled() && key.Matches(msg, binding)
}
```

`bubbles v2`'s `Matches` is generic over `fmt.Stringer`, and `tea.KeyPressMsg` satisfies it, so the `Enabled()` guard stays.

Then change every `handleKey(msg tea.KeyMsg)` to `handleKey(msg tea.KeyPressMsg)`:

```powershell
cd tui
$files = git grep -l 'handleKey(msg tea.KeyMsg)' -- .
foreach ($f in $files) {
  (Get-Content -Raw $f).Replace('handleKey(msg tea.KeyMsg)', 'handleKey(msg tea.KeyPressMsg)') |
    Set-Content -NoNewline -Path $f -Value $_
}
```

The bodies need no change: production code dispatches on `msg.String()` and never on a `tea.KeyType` constant.

- [ ] **Step 6: `App` — the interception, the routing rule, and `View`**

`tui/internal/ui/app.go`. Three edits.

The key branch in `update` becomes `tea.KeyPressMsg`:

```go
	case tea.KeyPressMsg:
		// ctrl+c quits from anywhere, before any model or modal can swallow it:
		// while signed out the login form owns every key, and its own ctrl+c
		// merely closes the form — which left the process unquittable from the
		// sign-in screen; an open overlay also handles ctrl+c as "close", which
		// would otherwise demand a second ctrl+c to quit.
		if m.String() == "ctrl+c" {
			return a, tea.Quit
		}
```

The routing rule near the end of `update` — this is Review Focus #1's neighbour and the highest-severity edit in the migration:

```go
	// Keys go to the active screen only. A key press carries no tag, so
	// broadcasting it would move every screen's cursor and let two screens
	// answer the same press; data messages, which are tagged, are still
	// broadcast so a load that finishes while the user is elsewhere lands.
	//
	// tea.KeyMsg is an interface in v2 and covers releases as well as presses,
	// so both are matched. A release is not one this app asks for — it never
	// requests the ReportEventTypes keyboard enhancement — but matching only
	// KeyPressMsg here would let a release fall through to the broadcast below
	// and break the rule for every screen at once.
	switch msg.(type) {
	case tea.KeyPressMsg, tea.KeyReleaseMsg:
		return a, a.updateActive(msg)
	}
	return a, tea.Batch(a.forward(msg)...)
```

`handleGlobalKey` takes `tea.KeyPressMsg` too:

```go
func (a *App) handleGlobalKey(key tea.KeyPressMsg) (tea.Cmd, bool) {
```

Rename the existing `View` to `view` and add the `tea.Model` method above it:

```go
// View renders the program. v2's Model returns a tea.View rather than a string,
// which is also where the alternate screen is declared: tea.WithAltScreen is
// gone, so the flag that used to be a program option is set here instead.
func (a *App) View() tea.View {
	v := tea.NewView(a.view())
	v.AltScreen = true
	return v
}

// view renders either the login screen or the workspace into a string. The
// layout, the sidebar, the status bar and the overlay compositing are unchanged.
func (a *App) view() string {
```

No test calls `a.View()` directly (they call `renderStatus`, and widgets' own `View`), so nothing else moves.

- [ ] **Step 7: `main.go` — drop the alt-screen option**

`tui/main.go:91`:

```go
	program := tea.NewProgram(ui.New(client))
```

- [ ] **Step 8: `form.go` — the space rename, the key type, `SetWidth`, `SetStyles`**

Four edits in `tui/internal/ui/form.go`.

The key type assertion at `form.go:164`:

```go
	key, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return f.updateInputs(msg)
	}
```

A release now takes the `updateInputs` path rather than the switch below, which is correct: bubbles settles its virtual cursor on a release, and the form's own bindings should not see one.

The boolean toggle, which is Review Focus #1:

```go
	case "space":
		if f.fields[f.index].Kind == FieldBool {
			f.toggle()
			return nil
		}
	}

	// Left/right switch a select's value in place, which is faster than opening
	// the picker for a two-option field.
	if f.fields[f.index].Kind == FieldSelect {
		switch key.String() {
		case "left", "right":
			f.cycle(key.String() == "right")
			return nil
		}
	}
	return f.updateInputs(msg)
```

`textinput.Width` is write-only now, so `NewForm` sets it through the accessor — this is Review Focus #2:

```go
		ti := textinput.New()
		ti.Placeholder = field.Placeholder
		ti.SetWidth(width)
		ti.CharLimit = 256
```

The other four widths become accessor calls too:

```powershell
cd tui
(Get-Content -Raw internal\ui\transactions.go).Replace('t.search.Width = 48', 't.search.SetWidth(48)') | Set-Content -NoNewline internal\ui\transactions.go
(Get-Content -Raw internal\ui\payees.go).Replace('p.filter.Width = 32', 'p.filter.SetWidth(32)') | Set-Content -NoNewline internal\ui\payees.go
(Get-Content -Raw internal\ui\tags.go).Replace('t.search.Width = 32', 't.search.SetWidth(32)') | Set-Content -NoNewline internal\ui\tags.go
(Get-Content -Raw internal\ui\import.go).Replace('i.search.Width = 44', 'i.search.SetWidth(44)') | Set-Content -NoNewline internal\ui\import.go
```

Add the styles hook, next to `SetError` in `form.go`:

```go
// SetStyles pushes the theme's text-input styles into every field. bubbles
// renders each input from its own copy of the styles, so a form cannot adopt
// the palette by holding a field — the copy has to happen here, after NewForm
// has built the inputs. The App calls this from ctx.Open, which is the one
// place every form passes through; threading the background through NewForm
// instead would touch its ~60 call sites for one grey colour.
func (f *Form) SetStyles(s textinput.Styles) {
	for i := range f.inputs {
		f.inputs[i].SetStyles(s)
	}
}
```

- [ ] **Step 9: `theme.go` — the design change**

Replace the palette block of `tui/internal/ui/theme.go`. The header and the `Theme` struct:

```go
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
```

`DefaultTheme` and `ThemeFor` replace the old `ThemeFor(r *lipgloss.Renderer)` entirely:

```go
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
```

`ModalFor` needs no change: it already calls `lipgloss.NewStyle()`, which is package-level in v2. `GetHorizontalFrameSize`/`GetVerticalFrameSize` survive, so `app.go`'s `modalBox` is untouched.

- [ ] **Step 10: `app.go` — constructors, the background query, theme rebuild, form styles**

`tui/internal/ui/app.go`. `NewWithRenderer` and the renderer parameter are gone; `New` absorbs what is left:

```go
// New builds the root model.
//
// There is no renderer parameter. v1 needed one per SSH session so that one
// client's colour profile could not leak into another's, because lipgloss
// downsampled inside Style.Render using the renderer's profile. v2 downsamples
// at each tea.Program's own output layer, so the door gets per-session colour
// from the program itself and the door's MakeRenderer plumbing is deleted.
func New(client *api.Client) tea.Model {
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
		theme:   DefaultTheme(),
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
	ctx.Open = func(m Modal) {
		// A form is constructed by its screen, before this hook runs, so its text
		// inputs still carry bubbles' own styles. Pushing the theme's here is the
		// one place every form passes through.
		if f, ok := m.(*Form); ok {
			f.SetStyles(a.theme.InputStyles)
		}
		a.modal = m
	}
	return a
}
```

`Init` asks for the background:

```go
// Init starts the first fetch: the reference data if a session already exists
// (an embedded token, say), otherwise the login screen — alongside a request for
// the terminal's background colour, so the palette can resolve for a light or a
// dark terminal.
//
// req is the paren-less function value: RequestBackgroundColor is declared
// func() Msg, and because Msg is an alias that value already has type tea.Cmd.
// Calling it (RequestBackgroundColor()) yields a Msg, which is what upstream's
// own doc comment shows and which does not compile here.
//
// The workspace never waits for the answer. A client whose terminal ignores the
// query would otherwise sit on an unstyled — or, if this blocked, a blank —
// screen indefinitely, and the dark theme in force meanwhile is a far better
// failure than a hang.
func (a *App) Init() tea.Cmd {
	req := tea.RequestBackgroundColor
	if a.client.HasSession() {
		return tea.Batch(req, a.startSession())
	}
	return tea.Batch(req, a.login.Init())
}
```

`Update` handles the reply, in the **first** switch so it applies while signed out too — the sign-in screen is themed:

```go
	case tea.BackgroundColorMsg:
		a.setTheme(msg.IsDark())
		return a, nil
```

And the rebuild helper, beside `notify`:

```go
// setTheme rebuilds the palette for a light or dark terminal and republishes it
// on the shared context. Ctx is a pointer and every screen reads ctx.Theme at
// render time rather than caching a copy, so screens pick the new theme up on
// their next frame without being told — which is why nothing else has to change.
func (a *App) setTheme(isDark bool) {
	a.theme = ThemeFor(isDark)
	a.ctx.Theme = a.theme
}
```

- [ ] **Step 11: The SSH door**

`tui/internal/sshd/server.go`. Imports become:

```go
	tea "charm.land/bubbletea/v2"
	"charm.land/ssh"
	"charm.land/wish/v2"
	wishtea "charm.land/wish/v2/bubbletea"

	"github.com/fintrak/client/api"
	"github.com/fintrak/tui/internal/ui"
```

`colorprofile`, `lipgloss` and `termenv` are all dropped: they existed only to build the per-session renderer, which is deleted below.

The middleware block, with its colour-floor comment rewritten because the floor's meaning changed:

```go
		// wish composes these by wrapping each around the previous chain, so the
		// LAST entry is the outermost handler. The session cap must be outermost:
		// it has to hold its slot for the whole session, wrapping the bubbletea
		// middleware rather than sitting inside it.
		//
		// No colour floor is set here, and that is a change rather than an
		// omission. v1 passed MiddlewareWithColorProfile(..., termenv.TrueColor)
		// because wish's default was Ascii and a session renderer was forced down
		// to that floor, so without an explicit TrueColor floor every client got a
		// monochrome TUI. In v2 each tea.Program detects its own profile from the
		// session environment handed to it by WithEnvironment below, and
		// downsamples at its own output layer, so each client gets the depth it
		// actually advertises and no floor is wanted.
		wish.WithMiddleware(
			wishtea.Middleware(s.model),
			s.logSession,
			s.limitSessions,
		),
```

`model` drops the renderer, and keeps its signature — `wish/v2`'s `bubbletea.Handler` is still `func(ssh.Session) (tea.Model, []tea.ProgramOption)`:

```go
func (s *server) model(sess ssh.Session) (tea.Model, []tea.ProgramOption) {
	// The program's own environment is the client's too: bubbletea reads TERM
	// from it for terminal handling and for colour-profile detection, and the
	// door process's TERM (usually unset in a container) is not the one the
	// session is drawn on.
	opts := []tea.ProgramOption{tea.WithEnvironment(sessionEnv(sess))}

	client, _ := sess.Context().Value(clientContextKey{}).(*api.Client)
	if client != nil {
		// The credentials were exchanged for a session during authentication; the
		// client keeps those tokens for the life of the connection, exactly as a
		// locally-run TUI would, so the session starts signed in.
		return ui.New(client), opts
	}

	// Public-key authentication opened the door but cannot be traded for an API
	// session, so the TUI starts at its own sign-in screen.
	fresh, err := api.New(s.cfg.APIURL)
	if err != nil {
		s.logger.Error("ssh session: bad API URL", slog.String("error", err.Error()))
		return errorModel{text: "This TUI is misconfigured (bad API URL). Ask the operator to check the logs."}, opts
	}
	return ui.New(fresh), opts
}
```

`sessionRenderer` is deleted in full — it existed only to carry a per-session colour profile and background answer, and the program now does the first while `tea.BackgroundColorMsg` does the second. `sessionEnv` stays, because `WithEnvironment` still needs it.

`errorModel` becomes a v2 model:

```go
func (m errorModel) Init() tea.Cmd { return nil }

func (m errorModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if _, ok := msg.(tea.KeyPressMsg); ok {
		return m, tea.Quit
	}
	return m, nil
}

// View renders the message. AltScreen is set for the same reason the real App
// sets it: v2 declares terminal modes on the View, and a misconfigured door
// that skipped the alternate screen would leave the message on the scrollback
// instead of replacing it.
func (m errorModel) View() tea.View {
	v := tea.NewView(m.text)
	v.AltScreen = true
	return v
}
```

`charm.land/ssh` is the same `gliderlabs/ssh` fork under a new path, and `wish/v2`'s option helpers keep their names (`WithAddress`, `WithHostKeyPath`, `WithVersion`, `WithIdleTimeout`, `WithMaxTimeout`, `WithMiddleware`). If the compiler disagrees about any of them, that disagreement is a real v1→v2 API difference to resolve at that step, not a stale import.

- [ ] **Step 12: The test files — key constructors**

`tea.KeyMsg` is an interface in v2, so every `tea.KeyMsg{Type: …, Runes: …}` literal is gone. Add the helpers to `app_test.go`, above `newAppForTest`:

```go
// press builds a key press for a printable rune.
func press(r rune) tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: r, Text: string(r)}
}

// ctrlPress builds a ctrl-modified key press.
func ctrlPress(r rune) tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: r, Mod: tea.ModCtrl}
}

// runes builds a key press carrying several runes, which is how v1 spelled a
// typed string. Almost every call site is a single character and should use
// press; this exists for the few that are not, such as a picker test that feeds
// an arbitrary label.
func runes(s string) tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: []rune(s)[0], Text: s}
}
```

`stubScreen.Update` must count a release as a key, or Task 2's routing test cannot see one:

```go
func (s *stubScreen) Update(msg tea.Msg) tea.Cmd {
	switch msg.(type) {
	case tea.KeyPressMsg, tea.KeyReleaseMsg:
		s.keys++
		return nil
	}
	s.dataMsg++
	return nil
}
```

Then the mechanical rewrites across the five test files:

```powershell
cd tui
$files = git grep -l 'tea.KeyMsg{Type: tea.KeyRunes, Runes: \[\]rune' -- .
foreach ($f in $files) {
  $text = Get-Content -Raw $f
  $text = [regex]::Replace($text, 'tea\.KeyMsg\{Type: tea\.KeyRunes, Runes: \[\]rune\{(.+?)\}\}', 'press($1)')
  $text = $text.Replace('tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)}', 'runes(key)')
  $text = [regex]::Replace($text, 'tea\.KeyMsg\{Type: (tea\.Key\w+)\}', 'tea.KeyPressMsg{Code: $1}')
  $text = [regex]::Replace($text, 'tea\.KeyMsg\{Type: (tea\.Key\w+), (\w+): (.+?)\}', 'tea.KeyPressMsg{Code: $1, Text: $3}')
  Set-Content -NoNewline -Path $f -Value $text
}
```

Named keys become `tea.KeyPressMsg{Code: tea.KeyEnter}` and so on — the `Code` field is a `rune` and `tea.KeyEnter` is such a constant. The four `tea.KeyMsg{Type: tea.KeyCtrlC}`, `KeyCtrlS`, `KeyCtrlO` and `KeyCtrlR` sites have no `Code` equivalent, so replace them by hand with `ctrlPress('c')`, `ctrlPress('s')`, `ctrlPress('o')` and `ctrlPress('r')`.

Those three ctrl+c sites are the entire coverage for Review Focus #4 — ctrl+c must quit from the sign-in screen and from an open overlay, in one press, and it only does because `app.go` checks it *before* the modal branch. They are load-bearing: if one of them fails after this step, the fix is in `app.go`, never in the test. `ctrlPress('c').String()` is `"ctrl+c"`, which is what `app.go:165` compares against, so they pass unchanged once constructed correctly.

`picker_test.go:11-25` already has a local `press(p *Picker, key string)` helper whose name now collides. Rename it to `pressKey` and update its five call sites.

The `run` pump in `transactions_test.go:18-32` needs no change: `tea.Cmd` is still `func() Msg`.

- [ ] **Step 13: Redesign the three renderer tests**

Their subject — a chosen lipgloss colour profile applied at `Render` time — no longer exists; downsampling is the program's. They become assertions about the theme, which is what the app owns. This is Review Focus #3.

`theme_test.go` in full:

```go
package ui

import (
	"image/color"
	"testing"
)

func TestThemeForResolvesThePaletteForLightAndDark(t *testing.T) {
	tests := []struct {
		name          string
		isDark        bool
		primary, muted string
	}{
		{"dark", true, "#22d3ee", "#94a3b8"},
		{"light", false, "#0e7490", "#64748b"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			th := ThemeFor(tc.isDark)
			assertHex(t, "Primary", th.Primary, tc.primary)
			assertHex(t, "Muted", th.Muted, tc.muted)
		})
	}
}

// assertHex compares a palette entry against an expected hex. The colours are
// resolved once, at construction, from a light/dark flag — there is no renderer
// to downsample them any more — so the resolved value is the whole contract.
func assertHex(t *testing.T, name string, got color.Color, want string) {
	t.Helper()
	r, g, b, a := got.RGBA()
	const shift = 8
	if hex := colorHex(uint8(r>>shift), uint8(g>>shift), uint8(b>>shift), uint8(a>>shift)); hex != want {
		t.Errorf("%s = %s, want %s", name, hex, want)
	}
}
```

Add `colorHex` to the same file:

```go
// colorHex formats a colour as #rrggbb, the form the palette is written in.
func colorHex(r, g, b, a uint8) string {
	const hexdigits = "0123456789abcdef"
	buf := []byte{'#', 0, 0, 0, 0, 0, 0}
	for i, c := range []uint8{r, g, b} {
		buf[1+i*2] = hexdigits[c>>4]
		buf[2+i*2] = hexdigits[c&0x0f]
	}
	return string(buf)
}
```

Delete `TestAppStylesWithTheSessionRenderer` from `app_test.go:439` and replace it with a test that the theme reaches the shared context, which is what propagation now means:

```go
// TestSetThemeRepublishesOnTheContext covers the propagation a session's
// background answer depends on: screens read ctx.Theme at render time, so the
// App has to republish the rebuilt theme there or every screen keeps the old one.
func TestSetThemeRepublishesOnTheContext(t *testing.T) {
	a, _, _ := newAppForTest(t)

	a.Update(tea.BackgroundColorMsg{Color: color.White})

	if a.ctx.Theme != a.theme {
		t.Error("setTheme did not republish the theme on the shared context")
	}
	lightPrimary, _, _, _ := ThemeFor(false).Primary.RGBA()
	gotPrimary, _, _, _ := a.theme.Primary.RGBA()
	if lightPrimary != gotPrimary {
		t.Error("a light background did not produce the light palette")
	}
}
```

That test needs `"image/color"` in `app_test.go`'s import block. Deleting the old test also orphans two imports it was the only user of — `"io"` and `"github.com/muesli/termenv"`, both used for the `lipgloss.NewRenderer(io.Discard)` it built. Remove them or the package will not compile.

`table_test.go`'s `stylePrefix` helper asserted the escape prefix a `termenv.TrueColor` renderer produced. Colour downsampling is now the program's, so those assertions are the door's end-to-end test's job — it already checks for `\x1b[38;5;` and `\x1b[38;2;` over a real session. Replace `table_test.go`'s escape-sequence assertions with a check that the cell roles still select the right styles, and drop the `lipgloss`/`termenv` imports. Note `SetColumns` is variadic, not a slice:

```go
// TestTableAppliesCellRoles checks the part of the table the app owns: each
// cell's role picks its own style, and the selected row's background is applied
// over the gaps. Which escape codes that ultimately emits is the tea.Program's
// business, not the table's.
func TestTableAppliesCellRoles(t *testing.T) {
	th := DefaultTheme()
	tbl := &Table{}
	tbl.SetColumns(Column{Title: "Amount", Width: 10, Align: AlignRight})
	tbl.SetRows([][]Cell{{Money("12.00")}, {Money("12.00")}})
	tbl.SetCursor(0)

	lines := strings.Split(ansi.Strip(tbl.View(th, 12, 6, "none")), "\n")
	if len(lines) < 2 {
		t.Fatalf("expected a header and a row, got %d lines", len(lines))
	}
	if !strings.Contains(lines[1], "12.00") {
		t.Errorf("the amount is missing from the row: %q", lines[1])
	}
}
```

- [ ] **Step 14: Build**

```powershell
cd tui
go build ./... 2>&1 | Select-Object -First 40
```

Expected: no output. Anything still failing is a site this plan did not name; fix it the same way — a v2 type in a signature, a `tea.KeyMsg` literal, or a lipgloss v1 symbol.

- [ ] **Step 15: Vet and test**

```powershell
cd tui
go vet ./...
go test ./... 2>&1 | Select-Object -Last 40
```

Expected: `go vet` silent, and every package `ok`. **Read the failures rather than editing assertions.** A weakened assertion is a review flag: if a test only fails because it asserted a v1 framework detail, rewrite it to assert the app's behaviour, and say so in the commit message.

- [ ] **Step 16: Confirm the module is tidy**

```powershell
cd tui
go mod tidy
cd ..
git diff --exit-code tui/go.mod tui/go.sum
```

Expected: no diff. CI enforces this, and it is the check that catches a dependency left behind or a version resolved differently than pinned.

- [ ] **Step 17: Commit**

```powershell
git add tui
git commit -m "feat(tui): migrate to the Bubble Tea v2 stack

Moves tui/ onto charm.land/*/v2. Behaviour is unchanged: the hand-rolled
table, picker, form, confirm, help and layout code all stay as they were,
and so does the app's own Screen/Modal contract -- v2's Model returns a
tea.View, which only App.View and errorModel.View adopt.

The one design change is the theme. v2 deletes lipgloss.Renderer, which
this app built per SSH session so one client's colour profile could not
leak into another's. In v2 each tea.Program downsamples at its own output
layer, so the door's sessionRenderer is deleted and per-session colour is
the program's job. What stays the app's is background lightness, which is
now an explicit tea.RequestBackgroundColor query answered by
tea.BackgroundColorMsg and resolved with lipgloss.LightDark. compat is
deliberately not used: compat.AdaptiveColor reads a process global, which
over SSH answers for the door container rather than the client.

The workspace never waits for that answer -- a client whose terminal
ignores the query would otherwise sit on a blank screen -- so the dark
theme is in force until the reply lands.

Three silent v2 changes are handled explicitly: tea.KeyMsg is now an
interface covering key releases as well as presses, so the key-vs-data
routing in app.go matches both and a release is never broadcast to every
screen; Key.String() returns \"space\" rather than \" \", which is what
toggled a boolean form field; and textinput.Width became write-only, so
the five widths are set through SetWidth."
```

## Task 2: Pin the behaviours v2 changes

Task 1 makes the suite pass, but it cannot make it *assert* the things v2
changed: every one of these behaviours either worked before the migration or was
never asserted, and each fails silently if it regresses.

**Files:**
- Modify: `tui/internal/ui/app_test.go`, `tui/internal/ui/form_test.go`, `tui/internal/ui/layout_test.go`

**Interfaces:**
- Consumes: `press`, `ctrlPress`, `newAppForTest`, `stubScreen` (Task 1); `Digit` keys via `tea.KeyPressMsg{Code: '1', Text: "1"}`.
- Produces: nothing; this task only adds tests.

- [ ] **Step 1: A key release must not be broadcast**

`tea.KeyMsg` is an interface in v2, so a naive rename of `app.go`'s key branch
lets a `KeyReleaseMsg` fall through to `tea.Batch(a.forward(msg)...)` and reach
all 13 screens — which would move every screen's cursor from one keypress. The
app never requests releases, so nothing else exercises this.

Add to `app_test.go`:

```go
// TestKeyReleasesAreNotBroadcast covers the routing rule for the message v2
// added. tea.KeyMsg is an interface over presses and releases, so matching only
// KeyPressMsg would drop a release into the broadcast branch and move every
// screen's cursor. The app never requests releases -- it does not ask for the
// ReportEventTypes enhancement -- so nothing else would catch it.
func TestKeyReleasesAreNotBroadcast(t *testing.T) {
	a, active, other := newAppForTest(t)

	a.Update(tea.KeyReleaseMsg{Code: 'j', Text: "j"})

	if active.keys != 1 {
		t.Errorf("the release did not reach the active screen: keys = %d, want 1", active.keys)
	}
	if other.keys != 0 {
		t.Errorf("the release was broadcast to another screen: keys = %d, want 0", other.keys)
	}
	if active.dataMsg != 0 || other.dataMsg != 0 {
		t.Errorf("a release was counted as a data message: active %d, other %d",
			active.dataMsg, other.dataMsg)
	}
}
```

- [ ] **Step 2: Digit shortcuts still resolve**

`app.go`'s `digitIndex` depends on `Key.String()` returning a bare `"1"`..`"9"`
and `"0"`. If v2 changed that, every digit shortcut dies with no error and the
TUI looks frozen. `TestDeliberateJumpEntersTheScreen` covers the navigation but
not the string the navigation is built on.

Add to `layout_test.go`:

```go
// TestDigitIndexUnderV2KeyStrings pins the assumption the digit shortcuts rest
// on: v2 stringifies a digit keypress as that digit alone. If this stops
// holding, `digitIndex` returns -1 for every digit and the 1..9 and 0 jumps
// silently do nothing.
func TestDigitIndexUnderV2KeyStrings(t *testing.T) {
	for r, want := range map[rune]int{'1': 0, '5': 4, '9': 8, '0': 9} {
		if got := digitIndex(press(r).String()); got != want {
			t.Errorf("digitIndex(%q) = %d, want %d", press(r).String(), got, want)
		}
	}
	if got := digitIndex(press('a').String()); got != -1 {
		t.Errorf("digitIndex(%q) = %d, want -1", press('a').String(), got)
	}
}
```

- [ ] **Step 3: Space toggles a boolean form field**

`form.go` matched `case " ":` before the migration and `case "space":` after.
Nothing asserted the toggle, so the rename would have shipped silently: a user
pressing space on a "closed" or "active" field sees nothing happen and the form
still submits the old value.

Add to `form_test.go`:

```go
// TestSpaceTogglesABoolField pins the v2 stringification of the space bar.
// form.go matched " " in v1 and must match "space" in v2; a mismatch is silent,
// because the toggle simply stops happening and the form still submits.
// BoolField stores "yes"/"no" rather than a bool, which is the stored value the
// assertion reads.
func TestSpaceTogglesABoolField(t *testing.T) {
	form := NewForm("t", "Edit", []Field{
		BoolField("Closed", false),
	}, nil)

	form.Update(press(' '))
	if form.fields[0].Value != "yes" {
		t.Errorf("space did not toggle the field on: Value = %q, want %q",
			form.fields[0].Value, "yes")
	}

	form.Update(press(' '))
	if form.fields[0].Value != "no" {
		t.Errorf("space did not toggle the field back off: Value = %q, want %q",
			form.fields[0].Value, "no")
	}
}
```

- [ ] **Step 4: A text field keeps its width**

`textinput.Width` became write-only. A dropped `SetWidth` leaves the field at
bubbles' default, and the user cannot read what they typed or see which field is
focused — a failure mode a removed exported field cannot produce.

Add to `form_test.go`:

```go
// TestTextFieldsKeepTheirWidth pins SetWidth. textinput.Width became
// write-only in v2, so a form that forgets to set it renders a field too narrow
// to read, with no error anywhere. TextField's third argument is a validator,
// not a width, so the width is set on the Field; NewForm falls back to 32 when
// it is zero.
func TestTextFieldsKeepTheirWidth(t *testing.T) {
	field := TextField("Name", "hello", nil)
	field.Width = 24
	form := NewForm("t", "Edit", []Field{field}, nil)

	body := ansi.Strip(form.View(DefaultTheme(), 60, 12))
	if !strings.Contains(body, "hello") {
		t.Fatalf("the field value is not visible:\n%s", body)
	}
	for _, line := range strings.Split(body, "\n") {
		if !strings.Contains(line, "hello") {
			continue
		}
		if w := ansi.StringWidth(line); w < 24 {
			t.Errorf("field line is %d wide, want at least the configured 24: %q", w, line)
		}
		return
	}
	t.Fatalf("no line carried the field value:\n%s", body)
}
```

- [ ] **Step 5: A terminal that never answers still gets a working TUI**

The background-colour query is fire-and-forget. If a client ignores it, the app
must not hang, and the user must still be able to quit. This is the test that
keeps Section 5.3 of the spec honest.

Add to `app_test.go`:

```go
// TestTheWorkspaceDoesNotWaitForTheBackgroundAnswer covers a terminal that
// never replies to the background-colour query. The workspace renders in the
// dark theme meanwhile and stays fully usable, because blocking on the answer
// would leave such a client on a blank screen forever.
func TestTheWorkspaceDoesNotWaitForTheBackgroundAnswer(t *testing.T) {
	a, _, _ := newAppForTest(t)
	a.width, a.height = 120, 40

	view := a.view()
	if view == "" {
		t.Fatal("the workspace rendered nothing before the background answer arrived")
	}
	if !strings.Contains(ansi.Strip(view), "Active") {
		t.Errorf("the active screen is missing from the first frame:\n%s", ansi.Strip(view))
	}

	_, cmd := a.Update(ctrlPress('c'))
	if cmd == nil {
		t.Fatal("ctrl+c must still quit before the background answer arrives")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Errorf("ctrl+c produced %T, want tea.QuitMsg", cmd())
	}
}
```

- [ ] **Step 6: Run the suite**

```powershell
cd tui
go test ./internal/ui/ -run 'TestKeyReleases|TestDigitIndex|TestSpaceToggles|TestTextFieldsKeep|TestTheWorkspaceDoesNotWait' -v 2>&1 | Select-Object -Last 30
```

Expected: all five pass. If either form test fails, the cause is a wrong assumption about a constructor in this plan, not about the behaviour — correct the call to the real signature rather than loosening the assertion.

- [ ] **Step 7: Full suite and coverage**

```powershell
cd ..
make test-tui-cover
```

Expected: pass, at or above 18%. Note the number.

- [ ] **Step 8: Commit**

```powershell
git add tui
git commit -m "test(tui): pin the behaviours the v2 migration changed

Four of these assert something that worked before the migration and
nothing checked: that a key release is routed to the active screen rather
than broadcast to all thirteen (tea.KeyMsg became an interface over
presses and releases, and the app never requests releases, so nothing else
would catch it), that a digit keypress still stringifies as a bare digit
(digitIndex, and therefore every number shortcut, depends on it), that
space still toggles a boolean form field, and that a text field still
renders at its configured width (textinput.Width became write-only).

The fifth asserts that the workspace does not wait for the terminal's
answer to the background-colour query: a client that ignores it must still
get a usable, quittable TUI in the dark theme rather than a blank screen."
```

## Task 3: Docs and the coverage gate

**Files:**
- Modify: `README.md:86`, `README.md:231`, `AGENTS.md:76`

**Interfaces:**
- Consumes: nothing.
- Produces: nothing.

- [ ] **Step 1: `README.md:86`**

```markdown
- **Framework**: Bubble Tea v2 + Bubbles/Lip Gloss v2, with [wish](https://github.com/charmbracelet/wish) for the optional SSH door
```

- [ ] **Step 2: `README.md:231`**

```markdown
A keyboard-driven terminal client lives in `tui/` (Go + [Bubble Tea](https://github.com/charmbracelet/bubbletea), v2).
```

- [ ] **Step 3: `AGENTS.md` — the SSH door bullet**

The existing bullet's last sentence is still true and should stay:

> wish composes middleware so the **last entry is outermost**: the session cap must be listed last to hold its slot for a whole session.

Replace the sentence about middleware with one that records what the migration changed, appended to the same bullet:

> Each session gets its own `tea.Program`, which detects that client's colour profile from the environment `WithEnvironment` hands it and downsamples at its own output layer — so there is no per-session lipgloss renderer to build, and no colour floor to set. Background lightness is the app's: `Init` asks with `tea.RequestBackgroundColor`, `tea.BackgroundColorMsg` rebuilds the `Theme` through `lipgloss.LightDark`, and `App.setTheme` republishes it on `Ctx` because every screen reads `ctx.Theme` at render time. The workspace never waits for that answer.

- [ ] **Step 4: Check nothing else in the docs names the v1 stack**

```powershell
git grep -n 'charmbracelet/bubbletea\|charmbracelet/bubbles\|charmbracelet/lipgloss\|charmbracelet/wish\|charmbracelet/ssh\|muesli/termenv' -- '*.md'
```

Expected: no hits outside `docs/superpowers/specs/` and `docs/superpowers/plans/`, which describe the migration itself and are meant to name the old paths.

- [ ] **Step 5: The full gate**

```powershell
make vet-tui build-tui test-tui-cover test-tui-cover-check
cd tui; go mod tidy; cd ..
git diff --exit-code tui/go.mod tui/go.sum
```

Expected: all pass, no module diff. Report the coverage number against the 18% floor; if it rose, say so rather than raising the floor in `Makefile` unannounced.

- [ ] **Step 6: Commit**

```powershell
git add README.md AGENTS.md
git commit -m "docs: point the framework references at the v2 stack

README names Bubble Tea v2 and the v2 import path. The AGENTS.md SSH-door
bullet gains what the migration changed: each session's colour profile is
now its tea.Program's, so the per-session lipgloss renderer and the colour
floor are both gone, and background lightness became an explicit query the
App resolves into the theme without waiting for."
```

## Manual verification

Not automatable, and the one place Review Focus #3 can still fail. With the
stack up, over SSH against one door process:

1. Connect a client advertising `TERM=xterm-256color` and one advertising
   `TERM=xterm-direct`. Each must get its own colour depth — not the door's.
2. Connect from a light-background terminal. The palette and the form text
   inputs must be the light variants.
3. Confirm the alternate screen is entered and left cleanly (the program
   restores the terminal on exit; a scrolled-past message after quitting means
   AltScreen is not being set).
4. Resize mid-session.
5. Open a form with a text field and a boolean field: type into the text field,
   press space on the boolean, and confirm both behave and that the text field
   is wide enough to read.
6. Press ctrl+c from the sign-in screen and from an open overlay — both must
   quit in one press.
