package ui

import "sort"

// The screen set is a registry rather than a list literal in the App, so each
// screen file owns its own registration and the framework does not depend on
// every screen existing to compile. Order is explicit; a test asserts the exact
// expected set so a missing or duplicated registration cannot pass unnoticed.

type screenEntry struct {
	order int
	build func(*Ctx) Screen
}

var screenRegistry []screenEntry

// registerScreen adds a screen to the sidebar. It is called from each screen
// file's init.
func registerScreen(order int, build func(*Ctx) Screen) {
	screenRegistry = append(screenRegistry, screenEntry{order: order, build: build})
}

// buildRegisteredScreens constructs every registered screen in order.
func buildRegisteredScreens(ctx *Ctx) []Screen {
	entries := make([]screenEntry, len(screenRegistry))
	copy(entries, screenRegistry)
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].order < entries[j].order })

	screens := make([]Screen, 0, len(entries))
	for _, e := range entries {
		screens = append(screens, e.build(ctx))
	}
	return screens
}
