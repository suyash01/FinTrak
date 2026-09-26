package ui

import (
	"errors"
	"strings"
	"testing"

	"charm.land/bubbles/v2/textinput"

	"github.com/fintrak/client/api"
)

// TestFailedSignInReleasesTheGuardOnTheSubmittingForm is a regression test for a
// defect in the in-flight submit guard: the failure was handed to whichever form
// happened to be on screen, so switching mode with ctrl+r while the request was
// in flight left the form that actually submitted still holding its guard — and a
// guarded form swallows every later submit until it is closed, which throws away
// the typed credentials.
func TestFailedSignInReleasesTheGuardOnTheSubmittingForm(t *testing.T) {
	client, err := api.New("http://127.0.0.1:1/api/v1")
	if err != nil {
		t.Fatalf("api.New: %v", err)
	}
	m := NewLoginModel(client)
	m.login.inputs[0].SetValue("user@example.com")
	m.login.inputs[1].SetValue("correct horse battery")

	if cmd := m.Update(ctrlPress('s')); cmd == nil {
		t.Fatal("the sign-in form did not submit")
	}

	// The user toggles to the register view while the request is in flight.
	m.Update(ctrlPress('r'))
	if m.mode != "register" {
		t.Fatalf("mode = %q, want register", m.mode)
	}

	// The rejection lands while the register form is the one displayed.
	m.Update(loaded[api.User]{tag: loginTag, err: errors.New("invalid email or password")})
	if m.Err() == nil {
		t.Fatal("the failure was not recorded")
	}

	// Back on the sign-in form, the failure is shown where the user can see it —
	// the form that submitted is the one that reports it.
	m.Update(ctrlPress('r'))
	if m.mode != "login" {
		t.Fatalf("mode = %q, want login", m.mode)
	}
	if got := m.login.View(DefaultTheme(), 40, 12); !strings.Contains(got, "invalid email or password") {
		t.Errorf("the form does not show why the sign-in failed: %q", got)
	}

	// And submitting works again: the guard was released on the form that had it.
	if cmd := m.Update(ctrlPress('s')); cmd == nil {
		t.Error("the sign-in form swallowed the submit: its in-flight guard was never released")
	}
}

// TestSignInCardAdoptsTheThemesInputStyles is the other half of the ctx.Open pin
// in app_test.go. The sign-in form is the one form that never passes through that
// hook — it is built by NewLoginModel, before any App exists — so LoginModel.View
// pushes the theme's input styles itself.
//
// inputs[1] is the Password field, which is the blurred one (NewForm focuses field
// 0). Blurred.Text is the only entry in textinput.Styles that paints a
// foreground, and bubbles' default is the dark one, so before this was fixed a
// password typed on a light-background terminal was painted Color("7") — invisible
// until the user tabbed to the field.
func TestSignInCardAdoptsTheThemesInputStyles(t *testing.T) {
	client, err := api.New("http://127.0.0.1:1/api/v1")
	if err != nil {
		t.Fatalf("api.New: %v", err)
	}
	m := NewLoginModel(client)

	// A light terminal, and the theme is only passed to View — the model holds no
	// palette of its own, which is what makes the render-time push necessary.
	th := ThemeFor(false)
	m.View(th, 80, 24)

	password := m.login.inputs[1]
	if got, want := password.Styles().Blurred.Text.Render("x"),
		th.InputStyles.Blurred.Text.Render("x"); got != want {
		t.Errorf("the password field kept bubbles' default input styles: got %q, want %q", got, want)
	}
	if got, dark := password.Styles().Blurred.Text.Render("x"),
		textinput.DefaultDarkStyles().Blurred.Text.Render("x"); got == dark {
		t.Errorf("the password field is still on bubbles' dark default (%q), which is unreadable on a light terminal", got)
	}
}
