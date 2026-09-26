package ui

import (
	"image/color"
	"testing"
)

func TestThemeForResolvesThePaletteForLightAndDark(t *testing.T) {
	tests := []struct {
		name           string
		isDark         bool
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
