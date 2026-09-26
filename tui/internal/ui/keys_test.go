package ui

import (
	"testing"

	"charm.land/bubbles/v2/key"
)

// TestSpaceSelectBindingsMatchTheSpaceKey is a regression test for a silent v2
// migration failure. In v1 a space key press matched the binding " ";
// in v2 tea reports the space bar's name rather than the character it produces,
// so a binding still declared as " " is inert: keyMatches compares the press's
// String() ("space") against the binding's keys (" "), never matches, and
// nothing fails. Space-to-select simply stops selecting while the status bar
// keeps advertising it, so the only way to notice is by using the screen.
//
// Both screens that bind a bare space are covered, because the two declarations
// are independent and a fix to one would not catch the other. The assertion goes
// through keyMatches, the same helper production uses, rather than comparing
// strings here — a test that re-implements the comparison cannot catch a change
// to the comparison.
func TestSpaceSelectBindingsMatchTheSpaceKey(t *testing.T) {
	space := press(' ')

	for _, tc := range []struct {
		name    string
		binding key.Binding
	}{
		{"transactions", newTxKeys().Select},
		{"links", newLinkKeys().Select},
	} {
		if !keyMatches(tc.binding, space) {
			t.Errorf("the %s Select binding %q does not match a space press, so space-to-select is dead",
				tc.name, tc.binding.Keys())
		}
	}
}
