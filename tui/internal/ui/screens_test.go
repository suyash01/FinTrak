package ui

import (
	"testing"

	"github.com/fintrak/tui/internal/api"
)

// TestScreenRegistryMatchesTheSidebar pins the screen set and its order. The
// order is part of the contract between the App and every screen file, and the
// registry is populated from twelve separate init() functions, so a missing or
// duplicated registration is exactly the kind of failure that would otherwise
// only show up as a silently absent tab.
func TestScreenRegistryMatchesTheSidebar(t *testing.T) {
	want := []int{10, 20, 30, 40, 50, 60, 70, 80, 90, 100, 110, 120, 130}

	if len(screenRegistry) != len(want) {
		t.Fatalf("registry holds %d screens, want %d", len(screenRegistry), len(want))
	}
	for _, e := range screenRegistry {
		if e.build == nil {
			t.Fatalf("screen registered at order %d has no constructor", e.order)
		}
	}

	client, err := api.New("http://127.0.0.1:1/api/v1")
	if err != nil {
		t.Fatalf("api.New: %v", err)
	}
	ctx := &Ctx{Client: client, Ref: &RefData{}, Theme: DefaultTheme(), Notify: func(Level, string, ...any) {}}
	screens := buildRegisteredScreens(ctx)

	if len(screens) != len(want) {
		t.Fatalf("built %d screens, want %d", len(screens), len(want))
	}
	seen := map[string]int{}
	for i, s := range screens {
		if s == nil {
			t.Fatalf("screen %d built to nil", i)
		}
		title := s.Title()
		if title == "" {
			t.Fatalf("screen %d has no title", i)
		}
		if prev, dup := seen[title]; dup {
			t.Errorf("screens %d and %d share the title %q", prev, i, title)
		}
		seen[title] = i
	}

	// The registration order is the file/init order, which is arbitrary; what
	// matters is that the side orders are exactly the expected set, each used
	// once, and that construction sorts by them.
	orders := make([]int, 0, len(screenRegistry))
	for _, e := range screenRegistry {
		orders = append(orders, e.order)
	}
	sorted := sortedCopy(orders)
	for i, want := range want {
		if sorted[i] != want {
			t.Fatalf("sidebar order %v, want %v", sorted, want)
		}
	}

	// The sidebar and the digit shortcuts rely on construction returning the
	// screens in `order`, not in registration order.
	saved := screenRegistry
	screenRegistry = []screenEntry{
		{order: 30, build: func(*Ctx) Screen { return &stubScreen{title: "third"} }},
		{order: 10, build: func(*Ctx) Screen { return &stubScreen{title: "first"} }},
		{order: 20, build: func(*Ctx) Screen { return &stubScreen{title: "second"} }},
	}
	defer func() { screenRegistry = saved }()

	built := buildRegisteredScreens(ctx)
	wantTitles := []string{"first", "second", "third"}
	for i, s := range built {
		if got := s.Title(); got != wantTitles[i] {
			t.Errorf("screen %d is %q, want %q", i, got, wantTitles[i])
		}
	}
}

func sortedCopy(in []int) []int {
	out := make([]int, len(in))
	copy(out, in)
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}
