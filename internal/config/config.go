package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

type SafetySetting struct {
	Category  string `json:"category"`
	Threshold string `json:"threshold"`
}

type Config struct {
	ModelProvider          string `json:"modelProvider"`
	Model                  string `json:"model"`
	MaxTurns               int    `json:"maxTurns"`
	MaxToolCalls            int    `json:"maxToolCalls"`
	MaxRunSeconds           int    `json:"maxRunSeconds"`
	ApprovalMode           string `json:"approvalMode"`
	Theme                  string `json:"theme"`
	ColorScheme            string `json:"colorScheme,omitempty"`
	AltScreenMode          string `json:"altScreenMode,omitempty"`
	Notifications          bool   `json:"notifications,omitempty"`
	ShowTips               bool   `json:"showTips,omitempty"`
	Verbosity              string `json:"verbosity,omitempty"`
	RunningLightSpeed      string `json:"runningLightSpeed,omitempty"`
	ShowStatus             bool   `json:"showStatus,omitempty"`
	Trajectory             bool   `json:"trajectory,omitempty"`
	MouseMode              bool   `json:"mouseMode,omitempty"`
	Editor                 string `json:"editor,omitempty"`
	EditorMode             string `json:"editorMode,omitempty"`
	EnableTerminalSandbox  bool   `json:"enableTerminalSandbox,omitempty"`
	AllowNonWorkspaceAccess bool  `json:"allowNonWorkspaceAccess,omitempty"`
	Effort                 string `json:"effort"`
	SystemPrompt           string `json:"systemPrompt"`
	SafetySettings         []SafetySetting `json:"safetySettings"`
}

func defaults() Config {
	return Config{
		ModelProvider:     "gemini",
		Model:             "gemini-3.8-flash-medium",
		MaxTurns:          100,
		MaxToolCalls:      200,
		MaxRunSeconds:     1800,
		ApprovalMode:      "ask",
		Theme:             "default",
		ColorScheme:       "terminal",
		AltScreenMode:     "never",
		Notifications:     false,
		ShowTips:          true,
		Verbosity:         "high",
		RunningLightSpeed: "medium",
		ShowStatus:        true,
		Trajectory:        false,
		MouseMode:         false,
		Editor:            "auto",
		EditorMode:        "default",
		Effort:            "medium",
		SystemPrompt:      "You are agy-open, a terminal-first software engineering agent. Work inside the supplied workspace. Inspect before editing. Use tools whenever repository information is needed. Never invent tool output. Explain risky actions before executing them. Prefer the smallest correct change. Return clear, direct answers.",
		SafetySettings: []SafetySetting{
			{Category: "HARM_CATEGORY_HARASSMENT", Threshold: "BLOCK_NONE"},
			{Category: "HARM_CATEGORY_HATE_SPEECH", Threshold: "BLOCK_NONE"},
			{Category: "HARM_CATEGORY_SEXUALLY_EXPLICIT", Threshold: "BLOCK_NONE"},
			{Category: "HARM_CATEGORY_DANGEROUS_CONTENT", Threshold: "BLOCK_NONE"},
		},
	}
}

// path returns agy-open's canonical per-user settings location.
func path() string {
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return filepath.Join(dir, "agy-open", "settings.json")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "agy-open", "settings.json")
}

// legacyPath is the pre-0.6.4 agy-open settings location.
func legacyPath() string {
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return filepath.Join(dir, "agy", "settings.json")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "agy", "settings.json")
}

// previousSettingsPath preserves settings created by versions that used
// Antigravity's settings directory. agy-open no longer writes there.
func previousSettingsPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".gemini", "antigravity-cli", "settings.json")
}

func Load() (Config, error) {
	cfg := defaults()

	// Prefer agy-open's own config directory, then migrate older locations.
	for _, p := range []string{path(), legacyPath(), previousSettingsPath()} {
		b, err := os.ReadFile(p)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return cfg, err
		}
		if err := json.Unmarshal(b, &cfg); err != nil {
			return defaults(), nil
		}

		// Version 0.6.5 shipped maxTurns=12 as its default. Upgrade that
		// legacy default when the newer independent safety budgets are absent.
		var stored struct {
			MaxTurns      *int `json:"maxTurns"`
			MaxToolCalls  *int `json:"maxToolCalls"`
			MaxRunSeconds *int `json:"maxRunSeconds"`
		}
		if err := json.Unmarshal(b, &stored); err == nil &&
			stored.MaxTurns != nil && *stored.MaxTurns == 12 &&
			stored.MaxToolCalls == nil && stored.MaxRunSeconds == nil {
			cfg.MaxTurns = defaults().MaxTurns
			if err := Save(cfg); err != nil {
				return cfg, err
			}
		}
		return cfg, nil
	}
	return cfg, nil
}

func Save(cfg Config) error {
	p := path()
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, append(b, '\n'), 0600)
}
