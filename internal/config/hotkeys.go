package config

import (
	"encoding/json"
	"fmt"
	"strings"
)

var defaultHotkeys = map[string]string{
	"submit": "ENTER",
	"insertNewline": "CTRL-J",
	"interrupt": "CTRL-C",
	"forwardDelete": "CTRL-D",
	"clearScreen": "CTRL-L",
	"lineStart": "CTRL-A",
	"lineEnd": "CTRL-E",
	"cursorLeft": "CTRL-B",
	"cursorRight": "CTRL-F",
	"deletePreviousWord": "CTRL-W",
	"deleteNextWord": "ALT-D",
	"killLineStart": "CTRL-U",
	"killLineEnd": "CTRL-K",
	"yank": "CTRL-Y",
	"transpose": "CTRL-T",
	"externalEditor": "CTRL-G",
	"paste": "CTRL-V",
	"openShell": "CTRL-O",
	"toggleTrajectory": "ALT-O",
	"showDiff": "CTRL-R",
	"toggleMouse": "CTRL-S",
	"undo": "ALT-Z",
	"redo": "ALT-Y",
	"complete": "TAB",
	"cursorUp": "UP",
	"cursorDown": "DOWN",
	"historyPrevious": "CTRL-P",
	"historyNext": "CTRL-N",
	"pageUp": "PUP",
	"pageDown": "PDOWN",
	"home": "HOME",
	"end": "END",
}

var hotkeyEvents = map[string]string{
	"submit": "ENTER",
	"insertNewline": "CTRL-J",
	"interrupt": "CTRL-C",
	"forwardDelete": "CTRL-D",
	"clearScreen": "CTRL-L",
	"lineStart": "CTRL-A",
	"lineEnd": "CTRL-E",
	"cursorLeft": "CTRL-B",
	"cursorRight": "CTRL-F",
	"deletePreviousWord": "CTRL-W",
	"deleteNextWord": "ALT-D",
	"killLineStart": "CTRL-U",
	"killLineEnd": "CTRL-K",
	"yank": "CTRL-Y",
	"transpose": "CTRL-T",
	"externalEditor": "CTRL-G",
	"paste": "CTRL-V",
	"openShell": "CTRL-O",
	"toggleTrajectory": "ALT-O",
	"showDiff": "CTRL-R",
	"toggleMouse": "CTRL-S",
	"undo": "ALT-Z",
	"redo": "ALT-Y",
	"complete": "TAB",
	"cursorUp": "UP",
	"cursorDown": "DOWN",
	"historyPrevious": "CTRL-P",
	"historyNext": "CTRL-N",
	"pageUp": "PUP",
	"pageDown": "PDOWN",
	"home": "HOME",
	"end": "END",
}

func DefaultHotkeys() map[string]string {
	out := make(map[string]string, len(defaultHotkeys))
	for action, key := range defaultHotkeys {
		out[action] = key
	}
	return out
}

func EffectiveHotkeys(cfg Config) map[string]string {
	out := DefaultHotkeys()
	for action, key := range cfg.Hotkeys {
		out[action] = key
	}
	return out
}

// NormalizeHotkeys validates a complete or partial action-to-key map.
// Ctrl+M is canonicalized to ENTER because the terminal sends the same CR
// byte for Ctrl+M and Enter.
func NormalizeHotkeys(input map[string]string) (map[string]string, error) {
	normalized := make(map[string]string, len(input))
	for action, key := range input {
		if _, ok := defaultHotkeys[action]; !ok {
			return nil, fmt.Errorf("unknown hotkey action %q", action)
		}
		key = strings.ToUpper(strings.TrimSpace(strings.ReplaceAll(key, "+", "-")))
		if key == "CTRL-M" {
			key = "ENTER"
		}
		if key != "" && !validHotkey(key) {
			return nil, fmt.Errorf("unsupported key %q for hotkey action %q", key, action)
		}
		normalized[action] = key
	}
	effective := DefaultHotkeys()
	for action, key := range normalized {
		effective[action] = key
	}
	owners := make(map[string]string, len(effective))
	for action, key := range effective {
		if key == "" {
			continue
		}
		if previous, exists := owners[key]; exists {
			return nil, fmt.Errorf("hotkey %q is assigned to both %s and %s", key, previous, action)
		}
		owners[key] = action
	}
	return normalized, nil
}

func validHotkey(key string) bool {
	if key == "ENTER" || key == "SHIFT-ENTER" || key == "TAB" || key == "BACKSPACE" ||
		key == "UP" || key == "DOWN" || key == "LEFT" || key == "RIGHT" ||
		key == "HOME" || key == "END" || key == "PUP" || key == "PDOWN" {
		return true
	}
	if strings.HasPrefix(key, "CTRL-") || strings.HasPrefix(key, "ALT-") {
		name := strings.TrimPrefix(strings.TrimPrefix(key, "CTRL-"), "ALT-")
		if name == "BACKSPACE" {
			return true
		}
		return len(name) == 1 && name[0] >= 'A' && name[0] <= 'Z'
	}
	return false
}

// ResolveHotkey translates a configured physical key to the legacy event name
// used by the TUI dispatchers. It also suppresses a built-in binding after the
// user has moved or disabled that action.
func ResolveHotkey(cfg Config, key string) string {
	effective := EffectiveHotkeys(cfg)
	for action, binding := range effective {
		if binding != "" && binding == key {
			return hotkeyEvents[action]
		}
	}
	for _, binding := range defaultHotkeys {
		if binding == key {
			return "HOTKEY-UNBOUND"
		}
	}
	return key
}

// MarshalHotkeys formats the complete active key map for settings editors.
func MarshalHotkeys(cfg Config) (string, error) {
	data, err := json.MarshalIndent(EffectiveHotkeys(cfg), "", "  ")
	return string(data), err
}
