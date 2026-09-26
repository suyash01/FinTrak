# TUI Migration to Bubble Tea v2 — Design

**Date:** 2026-09-27
**Status:** Design approved in conversation; awaiting written review
**Surface:** `tui/` only (`github.com/fintrak/tui`). No backend route, model, migration, or `client/` change is involved.

## Purpose

Move the terminal client onto Charm's v2 stack. The move is forced, not
cosmetic: v2 lives at new vanity import paths (`charm.land/*/v2`), and Go
cannot resolve `github.com/charmbracelet/bubbletea` v1 and `charm.land/bubbletea/v2`
in one module. The TUI compiles against one or the other.

The port is **faithful**: no user-visible behaviour changes. Every existing
`Screen`, `Modal`, layout, keymap and widget stays as it is, including the
hand-rolled table, picker, form, confirm, help and layout code.

One genuine design change falls out of the migration, and it is a simplification
rather than a feature. v2 deletes `lipgloss.Renderer`, which this codebase
threads per SSH session for one stated reason (Section 5.1). In v2 each
`tea.Program` downsamples at its own output layer, so the guarantee survives
without the plumbing.

## 1. Scope

**In:**

- `tui/go.mod`: the four module swaps, plus dropping `muesli/termenv`.
- Import rewrite across 43 files.
- The `tea.Model` interface changes (Section 4).
- The key-message rename and the routing rule it forces (Section 4.2).
- The theme rework: `AdaptiveColor` resolution and background-colour query
  (Section 5).
- `bubbles/textinput` v2 field changes, plus per-session input styles (Section 6).
- The SSH door onto `charm.land/wish/v2` (Section 7).
- The test suite, including five new tests for behaviour whose mechanics v2
  changes (Section 8).
- `README.md` and `AGENTS.md` references to the framework.

**Out:**

- `client/`, `mcp/`, `backend/`, `frontend/` — verified to import no Charm
  packages.
- Replacing the hand-rolled widgets with `bubbles` equivalents, or the
  hand-rolled overlay compositing (`replaceCenter`, `centerBlock`,
  `joinHorizontal` in `layout.go`) with `lipgloss/v2/layer`.
- Driving a real terminal cursor via `View.Cursor` instead of bubbles' virtual
  cursor.
- Re-basing the TUI's 18% coverage floor.

## 2. Dependencies

| From | To |
|---|---|
| `github.com/charmbracelet/bubbletea v1.3.10` | `charm.land/bubbletea/v2` |
| `github.com/charmbracelet/bubbles v1.0.0` | `charm.land/bubbles/v2` |
| `github.com/charmbracelet/lipgloss v1.1.0` | `charm.land/lipgloss/v2` |
| `github.com/charmbracelet/wish v1.4.7` | `charm.land/wish/v2` |
| `github.com/charmbracelet/ssh` | `charm.land/ssh` |
| `muesli/termenv v0.16.0` | dropped |
| `github.com/charmbracelet/colorprofile v0.4.1` | kept, bumped |

`github.com/charmbracelet/x/ansi` and `colorprofile` keep their paths.
`muesli/termenv` existed only to name colour profiles, which `colorprofile` and
v2's `View` now own; its four remaining uses (`app_test.go`, `theme_test.go`,
`table_test.go`, `sshd/server.go`) all disappear with Section 5 and Section 8.

**No toolchain bump.** The repo is on Go 1.27.1 (`tui/go.mod`,
`golang:1.27.1-alpine3.24`); the v2 modules require 1.26.x. `tui/Dockerfile`'s
root build context and the CI `build-images` matrix are unaffected.

The authoritative references are the `UPGRADE_GUIDE_V2.md` in both
`charmbracelet/bubbletea` and `charmbracelet/lipgloss`.

## 3. Why the port cannot be split

Two facts fix the order, and both were found by reading the v2 sources rather
than the guides.

**`bubbles` and `bubbletea` must move together.** `bubbles v1`'s
`key.Matches` accepts the v1 `tea.KeyMsg` *struct*; it cannot accept v2's
`KeyPressMsg`. All 129 `keyMatches` call sites therefore break the moment
bubbletea moves, before bubbles has changed. v2's `Matches` is generic over
`fmt.Stringer` precisely to remove this coupling.

**`lipgloss` must land in the same step as `bubbletea`.** In v1 the session
renderer downsampled at `Style.Render()` time, using the profile of the writer
it was built for. v2's `Style.Render` always emits full-fidelity ANSI and the
`tea.Program` downsamples at the output layer. So a tree with bubbletea v2 and
lipgloss v1 has lipgloss v1 downsampling with the **door process's** detected
profile rather than the **session's** — which is precisely the cross-session
leak the comment at `app.go:69-78` documents the current design as preventing.

Splitting the theme rework into its own later step would therefore land a known
colour regression in the SSH door for the duration of that step. The port lands
bubbletea, bubbles and lipgloss as one step instead.

## 4. The App's model

### 4.1 `tea.Model` — three sites, not thirty

`tea.Model` in v2 is `Init() Cmd`, `Update(Msg) (Model, Cmd)`, `View() View`.
Only the `View` return type actually differs here, and only three sites are
affected:

- `app.go:439` — `App.View() string` becomes `View() tea.View`, wrapping the
  existing string in `tea.NewView(...)` and setting `v.AltScreen = true`.
- `main.go:91` — drop `tea.WithAltScreen()`; the option no longer exists
  because the flag moved to the `View`.
- `sshd/server.go:355` — `errorModel.View()` likewise.

`Screen` and `Modal` are **not** affected. Their
`Update(tea.Msg) tea.Cmd` and `View(w, h int) string` are this codebase's own
contract, not bubbletea's: `tea.Msg` is still an alias for an empty interface,
`tea.Cmd` is still `func() Msg`, and the `(w, h)` shape stays because the App
still owns the layout. This is why the migration is tractable — 13 screens and
4 overlays keep their rendering code untouched.

`tea.Cmd` keeps its type, so `load[T]`/`act` (`screen.go:100-114`) and the four
hand-rolled command literals (`categories.go:507,636`, `accounts.go:1044`,
`links.go:946`) need no change. `tea.Quit` is now a `func() Msg` value, still
assignable wherever `tea.Quit` was used as a command. `tea.QuitMsg` survives as
a type, so the four `cmd().(tea.QuitMsg)` assertions in `app_test.go` still
compile.

### 4.2 Key messages, and the one rule that must not drift

In v1 `tea.KeyMsg` was a struct. In v2 it is an **interface** covering both
presses and releases; `tea.KeyPressMsg` is the concrete press type.

Mechanical part: `keyMatches` (`layout.go:71-74`) and the 21
`handleKey(msg tea.KeyMsg)` signatures take `tea.KeyPressMsg`, as do
`handleGlobalKey` (`app.go:279`) and the ctrl+c / modal / global-key branch at
`app.go:158`. Because production code dispatches on `msg.String()` and never on
`tea.KeyType` constants, the body of those 129 `keyMatches` call sites is
untouched.

The part that is not mechanical is `app.go:251`:

```go
if _, isKey := msg.(tea.KeyMsg); isKey {
    return a, a.updateActive(msg)
}
return a, tea.Batch(a.forward(msg)...)
```

This is the app's core routing invariant, stated at `app.go:247-253`: a key
press carries no tag, so broadcasting it would move every screen's cursor and
let two screens answer one press. Under v1 the assertion caught every key
message. Under v2 a `KeyReleaseMsg` satisfies `tea.KeyMsg` but not
`tea.KeyPressMsg`, so a naive rename of the *first* switch alone would drop
releases into the broadcast branch and break the invariant.

The rule becomes explicit about both halves:

```go
switch msg.(type) {
case tea.KeyPressMsg, tea.KeyReleaseMsg:
    return a, a.updateActive(msg)   // the keyboard is not broadcast
}
return a, tea.Batch(a.forward(msg)...)
```

A release reaching `updateActive` is harmless: every screen's `handleKey`
matches on `KeyPressMsg` and ignores it, and `textinput.Model.Update` uses it
to settle its virtual cursor. What matters is that it is not broadcast.

Releases are not currently *requested* — the app does not set
`View.KeyboardEnhancements.ReportEventTypes`, so a conforming terminal sends
none. The guard is written for correctness by construction rather than relying
on that, and Section 8 adds a test that sends one anyway.

One silent behaviour change to make explicitly: **v2's `Key.String()` returns
`"space"`, not `" "`.** The rename has **two halves**, and both matter because
`key.Matches` compares `msg.String()` against the strings a binding declares:

- The **call site**: `form.go:199` matches `case " ":` to toggle a `BoolField`,
  and becomes `case "space":`.
- The **declaration**: `transactions.go:73` and `links.go:141` declare
  `key.WithKeys(" ")`, and become `key.WithKeys("space")`. A binding left on the
  bare character can never match, because `uv.Key.String()` returns `Keystroke()`
  — the literal `"space"` — whenever `Text` is a single space. That silently kills
  multi-row selection for bulk actions and Links row selection while the status
  bar still advertises "space".

The ctrl+c interception at `app.go:165` uses `m.String() == "ctrl+c"` and is
unaffected.

`digitIndex(key.String())` (`app.go:336`) is assumed unchanged for `"1"`..`"9"`
and `"0"`; Section 8 pins it with a test rather than trusting the assumption.

## 5. Theme

### 5.1 What the renderer was doing, and what replaces it

The v1 `lipgloss.Renderer` served two unrelated jobs, and conflating them is
what made the plumbing feel necessary:

1. **Colour profile** — how many colours the terminal can show. v2 moves the
   *downsampling* to the `tea.Program`, per program, at its own output layer, so
   the renderer object itself is unnecessary and `sessionRenderer`
   (`server.go:319-328`) is deleted. But the *detection* is not free over SSH:
   `colorprofile.Detect` gates its whole answer on `term.IsTerminal(out.Fd())`,
   and wish's `MakeOptions` sets `WithOutput(sess)`, where an `ssh.Session` is an
   `io.Writer` and not a `term.File` — so `Detect` returns `NoTTY` and the door
   goes monochrome. The door therefore still names the profile explicitly, with
   `tea.WithColorProfile(colorprofile.Env(env))` in the options its handler
   returns, which is the same call v1's `sessionRenderer` made. `colorprofile`
   stays a direct dependency.
2. **Background lightness** — light or dark terminal, which is what
   `AdaptiveColor` needs. v2 does *not* decide this for us; the app asks.

Only the second survives as app responsibility, and it is asked for explicitly:
`Init` returns `tea.RequestBackgroundColor`, `Update` handles
`tea.BackgroundColorMsg`, and the theme is rebuilt from `msg.IsDark()` via
`lipgloss.LightDark`. This is a faithful port — v1's renderer performed the same
query against the same pty — differing only in that the query is now stated
rather than implied.

One trap in the request itself. `RequestBackgroundColor` is declared
`func RequestBackgroundColor() Msg`, so the **paren-less function value** is what
satisfies `Cmd` (`Msg` is an alias, making the signature identical to `Cmd`):

```go
return tea.RequestBackgroundColor                        // correct
return tea.RequestBackgroundColor()                      // does not compile
```

Upstream's own doc comment in `color.go` shows the second form, so this is worth
reading rather than copying. The same value batches correctly, so `Init`
returns `tea.Batch(tea.RequestBackgroundColor, <existing command>)`.

The request is skipped entirely when input is disabled, since the reply could
not be read; the door sets `WithInput(sess)` via `MakeOptions`, so the query
does go out over SSH. A program built with input disabled simply keeps the
Section 5.3 default.

`lipgloss.AdaptiveColor` is gone from the root package. The `compat` package
offers a drop-in, and is **rejected**: `compat.AdaptiveColor` reads a
process-global, which over SSH is the door container's background, not the
client's. `LightDark` with the queried answer is the only per-session-correct
option, and the one the door's existing comment is already arguing for.

### 5.2 Shape

`Theme`'s six colour fields become `color.Color`, resolved once at construction
from an `isDark` flag; `ThemeFor(r *lipgloss.Renderer)` becomes
`ThemeFor(isDark bool)`, and the `newStyle := r.NewStyle` indirection at
`theme.go:77-80` is deleted because `lipgloss.NewStyle` is package-level in v2.
`lipgloss.Color("#hex")` is now a function returning `color.Color`.

`DefaultTheme()` keeps its no-argument signature and returns the dark theme, so
its 12 call sites across 7 test files and the `Ctx{Theme: DefaultTheme()}`
composite literals do not churn. This also makes it agree with the Section 5.3
pre-answer default rather than inventing a second notion of "the default".

`Theme` also gains one field, `InputStyles textinput.Styles` (Section 6).

`GetHorizontalFrameSize` / `GetVerticalFrameSize` survive, so `modalBox`
(`app.go:607-612`) is unchanged.

`NewWithRenderer` and the `*lipgloss.Renderer` parameter are deleted; the door
and `main.go` both call `ui.New(client)` again.

**Rebuild propagation.** `App` holds `theme Theme`; on `tea.BackgroundColorMsg`
it does `a.theme = ThemeFor(msg.IsDark())` and `a.ctx.Theme = a.theme`. This
is sufficient and requires no screen changes, because `Ctx` is a pointer and
every screen reads `ctx.Theme` at *render* time (`th := d.ctx.Theme` inside each
`View`) rather than caching a copy in a struct field. Verified across all 13
screens. The App's own `a.theme` reads are likewise live.

### 5.3 The window before the answer

There is a period between the first frame and the terminal's reply in which the
lightness is unknown. The design:

- **Default to dark.** Most terminals are dark, and the alternative — guessing
  light — makes a light-terminal user's first impression wrong instead.
- **Never block on the answer.** The workspace renders immediately and restyles
  when the reply lands. A client whose terminal does not answer the OSC query
  would otherwise sit on a blank screen forever, which is the failure mode this
  repository is consistently hostile to elsewhere (`signOut` clearing a stale
  `refErr`, `app_test.go` pinning the error card as escapable, the dashboard's
  "loading…" card rather than a blank pane). A brief restyle is consistent with
  `App.View` already returning `"loading…"` before the first `WindowSizeMsg`.
- **Pin it with a test**: an `App` that never receives `BackgroundColorMsg` still
  reaches the signed-in workspace and still quits on ctrl+c.

## 6. `bubbles/textinput`

Field-level changes:

- `Width` became private. The five composite-literal writes become `SetWidth`:
  `form.go:84`, `transactions.go:338`, `payees.go:193`, `tags.go:211`,
  `import.go:456`.
- `Focus()` now returns `tea.Cmd` (it starts the virtual cursor's blink). All
  eight call sites are statements — `form.go:91,244,330`, `import.go:458`,
  `payees.go:195`, `tags.go:213`, `transactions.go:340` — so they compile
  unchanged. A value-returning call used as a statement is legal Go, and
  discarding the command is harmless because the four sites that already return
  `textinput.Blink` explicitly (`transactions.go:341`, `payees.go:196`,
  `tags.go:214`, `import.go:459`) keep doing so; those may now be redundant
  since `Focus()` covers them, but keeping them is the faithful choice and
  costs nothing.
- `textinput.Blink` is unchanged (`func() Msg`), so those four sites need no
  edit.
- `Model.Update` type-asserts `tea.KeyPressMsg`, so the existing habit of
  forwarding raw messages — `Form.updateInputs` (`form.go:219`, reached from two
  call sites) and the four inline search boxes (`transactions.go:240`,
  `payees.go:143`, `tags.go:165`, `import.go:416`) — keeps working; a forwarded
  `WindowSizeMsg` is simply ignored.

**Input styles.** `New()` hardcodes `DefaultDarkStyles()`, so the zero value no
longer adapts. v1's `DefaultStyles()` chose the blurred-text colour from
`lipgloss.HasDarkBackground()`, a process global — meaning that over SSH it was
already answering for the door container rather than the client. The faithful
*and* per-session-correct move is to call
`textinput.DefaultStyles(isDark)` with the same flag the rest of the theme uses.

Threading `isDark` into `NewForm` would touch its ~60 call sites for a
one-colour difference, so the styles are applied at the one place every form
already passes through: `ctx.Open`, which the App installs at `app.go:104` and
which already type-asserts `*Form` at `app.go:217`.

```go
ctx.Open = func(m Modal) {
    if f, ok := m.(*Form); ok {
        f.SetStyles(a.theme.InputStyles)
    }
    a.modal = m
}
```

`Form` gains a `SetStyles(textinput.Styles)` method that pushes the styles into
every `f.inputs[i]`. A stored field would not do: bubbles renders each input
from *its own* `styles`, read at `View()` time, so a `Form.Styles` field that
nothing copies into the inputs would be inert and the change would silently do
nothing. `SetStyles` is the one place that copy happens, and it runs after
`NewForm` has built the inputs, which is why the hook is `Open` and not the
constructor.

`ThemeFor` populates `InputStyles` as `textinput.DefaultStyles(isDark)`, so the
inputs follow the same flag as the rest of the palette. Until the background
answer arrives they carry the dark default, consistent with Section 5.3.

Note this is the one place the app's theme begins styling a bubbles widget:
until now the text inputs were styled by bubbles alone, from a process global.
That inconsistency is pre-existing and this change narrows it rather than
resolving it — the inputs still use bubbles' palette, not the app's.

`CapturesText()` (`screen.go:41-47`) is unaffected: its four callers
(`import.go:217`, `payees.go:79`, `tags.go:99`, `transactions.go:129`) report
their own boolean, not bubbles' focus state.

## 7. The SSH door

- `wish/bubbletea` (midjourney) is pinned to bubbletea v1 and cannot carry a v2
  program. It becomes `charm.land/wish/v2/bubbletea`, and
  `github.com/charmbracelet/ssh` becomes `charm.land/ssh`.
- `wishtea.MiddlewareWithColorProfile(s.model, termenv.TrueColor)`
  (`server.go:174`) becomes `bubbletea.Middleware(s.model)`, and the profile moves
  into the options `s.model` returns (`server.go:299`) as
  `tea.WithColorProfile(colorprofile.Env(env))` — per Section 5.1 this is
  required, not an override, because detection cannot see through an
  `ssh.Session`. The "colour floor is TrueColor, not wish's default Ascii"
  comment is deleted rather than rewritten: a fixed floor was the v1 workaround
  for a renderer that had to be told, and the per-client profile replaces it.
- `sessionRenderer` (`server.go:319-328`) is deleted in full. The `lipgloss` and
  `termenv` imports go with it; `colorprofile` does not, because the
  `colorprofile.Env` call it made moves into `model`'s options rather than
  disappearing (Section 5.1).
- `tea.WithEnvironment(sessionEnv(sess))` (`server.go:299`) still exists and is
  kept — it is what lets bubbletea read the *client's* `TERM` rather than the
  door container's.
- `server.go:348` `case tea.KeyMsg:` becomes `case tea.KeyPressMsg:`.
- The middleware ordering invariant is re-verified, not assumed: wish composes
  so the **last** entry is outermost, and the session cap depends on it
  (`AGENTS.md`). `server_test.go:372` is the test that catches a mistake.

`wish/v2` transitively adds `go-git` and `charmbracelet/x/xpty`. This is a real
increase in the door image's dependency weight; it does not affect the
local-only build path.

## 8. Testing

No `teatest` is used today, and none is introduced: the suite drives `Update`
directly and asserts on state and on `ansi.Strip`-ed substrings, which is a
better fit for this app's render logic than golden frames. The migration is
mostly a rename of message constructors. Three behaviours need new coverage
because v2 changes their mechanics, and one regression guard is needed for a
field that became write-only:

1. **A `tea.KeyReleaseMsg` is not broadcast** to the screens. Directly pins the
   Section 4.2 rule; the existing `TestKeysReachOnlyTheActiveScreen` and
   `TestDataMessagesStillReachEveryScreen` pin the halves.
2. **Digit jump still resolves** through `digitIndex` (`app.go:336`), pinning
   v2's `Key.String()` for `"1"`..`"9"`/`"0"`. `TestDeliberateJumpEntersTheScreen`
   covers the behaviour but not the string.
3. **Space toggles a `BoolField`** (`form.go:199`), pinning the `"space"` rename.
4. **A text field renders at its configured width** after `SetWidth`, since the
   field is now write-only through an accessor and a silently dropped width
   would only show up as a truncated form.

Plus the Section 5.3 default-theme test: an `App` that never receives a
`BackgroundColorMsg` still reaches the workspace and still quits.

**Key construction gets a helper.** ~63 sites build `tea.KeyMsg` literals by
hand across 5 test files. A `press(code, mod)` / `runes(s)` helper collapses
them and gives the four new tests something to call. `picker_test.go:11-25`
already has a local equivalent to repoint.

**The renderer tests lose their *subject*, but not all three.** `theme_test.go`
and `TestAppStylesWithTheSessionRenderer` (`app_test.go:439`) asserted a
`termenv.Ascii` or `termenv.ANSI256` *renderer* choosing to emit escapes or not,
and the renderer is gone, so both become assertions about the theme itself: that
`ThemeFor(true)` and `ThemeFor(false)` resolve each palette entry to the intended
hex, and that a background reply reaches `Ctx`.

`table_test.go` is different and keeps its assertions. lipgloss v2's package-level
`Render` does **not** downsample — `Foreground(Color("#f87171")).Render("x")`
emits `\x1b[38;2;248;113;113mx\x1b[m` — because downsampling moved to the
program. So an escape prefix from a style is still a statement about which style
the table chose, which is the app's behaviour and the subject of that test. Only
its `colourTheme` helper is deleted, since it existed to build a renderer;
its call sites become `DefaultTheme()`. Escape bytes at the session level stay
covered end to end by `sshd/server_test.go`, which asserts `\x1b[38;5;` /
`\x1b[38;2;` over a real SSH connection.

The TUI's 18% coverage floor is held, not re-based. If the mechanical test
rewrite allows coverage to rise, the floor is raised deliberately; the number is
reported rather than adjusted silently, because `AGENTS.md` ties it to
`make release`.

## 9. Landing

Three steps, each building and testing green. Step 1 is landed as a sequence of
commits — imports, then `Model`/`View`, then keys, then theme — so it stays
reviewable in `git log` even though the tree never sits in a regressed state.

**Step 1 — app core** (`bubbletea` + `bubbles` + `lipgloss` + theme). Sections
4, 5, 6. Large by necessity (Section 3).

**Step 2 — the door** (`wish`/`ssh`). Section 7. `server_test.go` and
`zz_real_test.go` move to `charm.land/ssh`.

**Step 3 — tests and docs.** Section 8, plus `README.md:86`, `README.md:231`
and the `AGENTS.md` middleware note.

## 10. Acceptance

- `make test-tui-cover-check` and `make test-tui-cover` pass, at or above 18%.
- `make vet-tui` and `make build-tui` pass.
- `go mod tidy` leaves `tui/go.mod` and `tui/go.sum` unchanged, as CI enforces.
- Every pre-existing `Test*` in `tui/internal/ui` and `tui/internal/sshd` still
  passes. Renaming a framework type in an assertion is expected; weakening or
  deleting an assertion is not, and is a review flag.
- Manual, over SSH with one door process: two clients advertising different
  `TERM`/`COLORTERM` each get their own colour depth; a light-background client
  gets light-adapted colours and text inputs; alt screen, resize, and one form
  containing a text field and a bool field all behave.

## 11. Risks

| Risk | Severity | Handling |
|---|---|---|
| `app.go:251` routing — releases broadcast | High, silent | Section 4.2 makes the rule explicit; new test (8.1) |
| `form.go:199` space rename | High, silent — suite stays green | New test (8.3) |
| Step 1 mixes a mechanical rename with a design change | Medium | Intra-step commit sequence; the tree is never regressed |
| Colour profile regressing mid-migration | Medium | Section 3 forbids splitting lipgloss out of step 1 |
| A non-responding client never restyles | Low | Section 5.3: never block, default dark, pinned by test |
| Text inputs visibly restyle | Low | Section 6 supplies `isDark`; manual check in step 1 |
| `wish/v2` pulls `go-git` into the door image | Low | Accepted; noted in Section 7 |
