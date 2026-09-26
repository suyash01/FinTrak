package ui

import (
	"image/color"
	"testing"
)

func TestThemeForResolvesThePaletteForLightAndDark(t *testing.T) {
	// Every colour ThemeFor names, both ways round. The row-selected background is
	// a style rather than a field, and is left to the rendered-style assertions
	// in app_test.go.
	tests := []struct {
		name                                          string
		isDark                                        bool
		primary, muted, danger, success, warn, border string
	}{
		{
			name: "dark", isDark: true,
			primary: "#22d3ee", muted: "#94a3b8", danger: "#f87171",
			success: "#4ade80", warn: "#fbbf24", border: "#334155",
		},
		{
			name: "light", isDark: false,
			primary: "#0e7490", muted: "#64748b", danger: "#b91c1c",
			success: "#15803d", warn: "#b45309", border: "#cbd5e1",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			th := ThemeFor(tc.isDark)
			assertHex(t, "Primary", th.Primary, tc.primary)
			assertHex(t, "Muted", th.Muted, tc.muted)
			assertHex(t, "Danger", th.Danger, tc.danger)
			assertHex(t, "Success", th.Success, tc.success)
			assertHex(t, "Warn", th.Warn, tc.warn)
			assertHex(t, "Border", th.Border, tc.border)
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
