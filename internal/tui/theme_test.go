package tui

import (
	"testing"

	"github.com/AmrUser-48/agy-open/internal/config"
)

func TestNormalizeColorScheme(t *testing.T) {
	tests := []struct {
		input string
		want string
	}{
		{input: " terminal ", want: "terminal"},
		{input: "CATPPUCCIN", want: "catppuccin"},
		{input: "tokyo night", want: "tokyo-night"},
		{input: "tokyo-night", want: "tokyo-night"},
		{input: "solarized dark", want: "solarized"},
		{input: "lcd7s", want: "lcd7s"},
	}
	for _, tt := range tests {
		got, ok := normalizeColorScheme(tt.input)
		if !ok || got != tt.want {
			t.Errorf("normalizeColorScheme(%q) = (%q,%t), want (%q,true)",
				tt.input, got, ok, tt.want)
		}
	}

	for _, input := range []string{"", "light", "dracula pro", "web"} {
		if got, ok := normalizeColorScheme(input); ok {
			t.Errorf("normalizeColorScheme(%q) unexpectedly accepted %q", input, got)
		}
	}
}

func TestEveryTUIColorSchemeHasAnActivePalette(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	t.Setenv("FORCE_COLOR", "")
	t.Setenv("TERM", "xterm-256color")

	for _, scheme := range tuiColorSchemes {
		t.Run(scheme, func(t *testing.T) {
			palette := newColors(config.Config{ColorScheme: scheme})
			if !palette.on || palette.cyan == "" || palette.green == "" ||
				palette.yellow == "" || palette.red == "" || palette.blue == "" {
				t.Fatalf("scheme %q produced incomplete palette: %#v", scheme, palette)
			}
		})
	}
}
