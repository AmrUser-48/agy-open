package tui

import (
	"fmt"
	"strings"
)

var tuiColorSchemes = []string{
	"terminal",
	"catppuccin",
	"monokai",
	"gruvbox",
	"solarized",
	"tokyo-night",
	"dracula",
	"dark",
	"lcd7s",
}

func normalizeColorScheme(value string) (string, bool) {
	value = strings.ToLower(strings.TrimSpace(value))
	switch value {
	case "tokyo night":
		value = "tokyo-night"
	case "solarized dark":
		value = "solarized"
	}
	for _, scheme := range tuiColorSchemes {
		if value == scheme {
			return scheme, true
		}
	}
	return "", false
}

func (u *UI) applyColorScheme(value string) error {
	scheme, ok := normalizeColorScheme(value)
	if !ok {
		return fmt.Errorf("unknown TUI color scheme %q; choose one of: %s",
			strings.TrimSpace(value), strings.Join(tuiColorSchemes, ", "))
	}
	return u.applyConfigSetting("colorScheme", scheme)
}
