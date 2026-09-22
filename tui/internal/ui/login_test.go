package ui

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

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

	if cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlS}); cmd == nil {
		t.Fatal("the sign-in form did not submit")
	}

	// The user toggles to the register view while the request is in flight.
	m.Update(tea.KeyMsg{Type: tea.KeyCtrlR})
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
	m.Update(tea.KeyMsg{Type: tea.KeyCtrlR})
	if m.mode != "login" {
		t.Fatalf("mode = %q, want login", m.mode)
	}
	if got := m.login.View(DefaultTheme(), 40, 12); !strings.Contains(got, "invalid email or password") {
		t.Errorf("the form does not show why the sign-in failed: %q", got)
	}

	// And submitting works again: the guard was released on the form that had it.
	if cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlS}); cmd == nil {
		t.Error("the sign-in form swallowed the submit: its in-flight guard was never released")
	}
}
