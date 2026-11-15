package config

import (
	"encoding/json"
	"testing"
)

func TestHotkeyDefaultsAndCtrlMAlias(t *testing.T) {
	cfg := defaults()
	if got := DefaultHotkeys()["openShell"]; got != "CTRL-O" {
		t.Fatalf("openShell default = %q", got)
	}
	if got := DefaultHotkeys()["toggleTrajectory"]; got != "ALT-O" {
		t.Fatalf("trajectory default = %q", got)
	}
	normalized, err := NormalizeHotkeys(map[string]string{"submit": "CTRL-M"})
	if err != nil {
		t.Fatal(err)
	}
	if normalized["submit"] != "ENTER" {
		t.Fatalf("Ctrl+M normalized to %q, want ENTER", normalized["submit"])
	}
	cfg.Hotkeys = normalized
	if got := ResolveHotkey(cfg, "ENTER"); got != "ENTER" {
		t.Fatalf("submit binding resolves to %q, want ENTER", got)
	}
}

func TestNormalizeHotkeysRejectsUnknownActionsAndCollisions(t *testing.T) {
	tests := []map[string]string{
		{"mystery": "CTRL-Q"},
		{"openShell": "CTRL-G"},
		{"openShell": "ALT-O"},
		{"openShell": "not-a-key"},
	}
	for _, bindings := range tests {
		if _, err := NormalizeHotkeys(bindings); err == nil {
			t.Errorf("NormalizeHotkeys(%v) should reject the mapping", bindings)
		}
	}
}

func TestHotkeysSettingRoundTripsRemapsAndUnbinds(t *testing.T) {
	cfg := defaults()
	value, err := SettingValue(cfg, "hotkeys")
	if err != nil {
		t.Fatal(err)
	}
	var bindings map[string]string
	if err := json.Unmarshal([]byte(value), &bindings); err != nil {
		t.Fatal(err)
	}
	bindings["openShell"] = "ALT-S"
	bindings["toggleTrajectory"] = ""
	data, err := json.Marshal(bindings)
	if err != nil {
		t.Fatal(err)
	}
	if err := SetSetting(&cfg, "hotkeys", string(data)); err != nil {
		t.Fatal(err)
	}
	if got := ResolveHotkey(cfg, "ALT-S"); got != "CTRL-O" {
		t.Fatalf("remapped shell binding resolves to %q, want CTRL-O action", got)
	}
	if got := ResolveHotkey(cfg, "CTRL-O"); got != "HOTKEY-UNBOUND" {
		t.Fatalf("old shell shortcut resolves to %q, want unbound", got)
	}
	if got := ResolveHotkey(cfg, "ALT-O"); got != "HOTKEY-UNBOUND" {
		t.Fatalf("cleared trajectory shortcut resolves to %q, want unbound", got)
	}
}
