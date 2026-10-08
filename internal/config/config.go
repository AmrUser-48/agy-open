package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

type Config struct {
	ModelProvider          string `json:"modelProvider"`
	Model                  string `json:"model"`
	MaxTurns               int    `json:"maxTurns"`
	ApprovalMode           string `json:"approvalMode"`
	Theme                  string `json:"theme"`
	ColorScheme            string `json:"colorScheme,omitempty"`
	AltScreenMode          string `json:"altScreenMode,omitempty"`
	Notifications          bool   `json:"notifications,omitempty"`
	ShowTips               bool   `json:"showTips,omitempty"`
	Verbosity              string `json:"verbosity,omitempty"`
	RunningLightSpeed      string `json:"runningLightSpeed,omitempty"`
	Editor                 string `json:"editor,omitempty"`
	EditorMode             string `json:"editorMode,omitempty"`
	EnableTerminalSandbox  bool   `json:"enableTerminalSandbox,omitempty"`
	AllowNonWorkspaceAccess bool  `json:"allowNonWorkspaceAccess,omitempty"`
	Effort                 string `json:"effort"`
}

func defaults() Config {
	return Config{
		ModelProvider:     "gemini",
		Model:             "gemini-3.8-flash-medium",
		MaxTurns:          12,
		ApprovalMode:      "ask",
		Theme:             "default",
		ColorScheme:       "terminal",
		AltScreenMode:     "always",
		Notifications:     false,
		ShowTips:          true,
		Verbosity:         "high",
		RunningLightSpeed: "medium",
		Editor:            "auto",
		EditorMode:        "default",
		Effort:            "medium",
	}
}

func officialPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".gemini", "antigravity-cli", "settings.json")
}

func legacyPath() string {
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return filepath.Join(dir, "agy", "settings.json")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "agy", "settings.json")
}

func path() string {
	return officialPath()
}

func Load() (Config, error) {
	cfg := defaults()

	// Read the current Antigravity-compatible location first. Fall back to
	// the old agy-open path so existing installations keep their settings.
	for _, p := range []string{officialPath(), legacyPath()} {
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
