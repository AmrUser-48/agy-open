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

type ApprovalRule struct {
	Tool         string `json:"tool"`
	CommandPrefix string `json:"commandPrefix"`
}

const (
	currentConfigVersion = 3
	defaultMaxTurns       = 100
	defaultMaxWorkers     = 3
	maxTeamWorkers        = 4
	defaultMaxToolCalls   = 200
	defaultMaxRunSeconds  = 1800
	legacyDefaultMaxTurns = 12
	DefaultLocalModelBaseURL = "http://127.0.0.1:11434/v1"
)

type Config struct {
	ConfigVersion          int    `json:"configVersion,omitempty"`
	SessionCompression      string `json:"sessionCompression,omitempty"`
	AgentRole              string `json:"agentRole,omitempty"`
	ModelProvider          string `json:"modelProvider"`
	ModelBaseURL           string `json:"modelBaseURL,omitempty"`
	Model                  string `json:"model"`
	MaxTurns               int    `json:"maxTurns"`
	MaxToolCalls           int    `json:"maxToolCalls"`
	MaxRunSeconds          int    `json:"maxRunSeconds"`
	MaxWorkers              int    `json:"maxWorkers"`
	ApprovalMode           string `json:"approvalMode"`
	ApprovalRules          []ApprovalRule `json:"approvalRules"`
	Theme                  string `json:"theme"`
	WebBackground          string `json:"webBackground,omitempty"`
	WebForeground          string `json:"webForeground,omitempty"`
	WebAccent              string `json:"webAccent,omitempty"`
	WebFont                string `json:"webFont,omitempty"`
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
		ConfigVersion:     currentConfigVersion,
	SessionCompression: "gzip",
		AgentRole:         "default",
		ModelProvider:     "gemini",
		ModelBaseURL:      DefaultLocalModelBaseURL,
		Model:             "gemini-3.8-flash-medium",
		MaxTurns:          defaultMaxTurns,
		MaxToolCalls:      defaultMaxToolCalls,
		MaxRunSeconds:     defaultMaxRunSeconds,
		MaxWorkers:         defaultMaxWorkers,
		ApprovalMode:      "ask",
		ApprovalRules:     []ApprovalRule{},
		Theme:             "dark",
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
		SystemPrompt:      "",
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
		var stored struct {
			ConfigVersion int  `json:"configVersion"`
			MaxTurns      *int `json:"maxTurns"`
			MaxToolCalls  *int `json:"maxToolCalls"`
			MaxRunSeconds *int `json:"maxRunSeconds"`
		}
		if err := json.Unmarshal(b, &stored); err != nil {
			return defaults(), nil
		}
		if err := json.Unmarshal(b, &cfg); err != nil {
			return defaults(), nil
		}

		// Upgrade shipped defaults while preserving explicit custom limits.
		needsSave := false
		if stored.ConfigVersion < currentConfigVersion {
			if stored.MaxTurns != nil {
				switch {
				case stored.ConfigVersion == 0 && *stored.MaxTurns == legacyDefaultMaxTurns:
					cfg.MaxTurns = defaultMaxTurns
				case stored.ConfigVersion == 1 && *stored.MaxTurns == 50 &&
					stored.MaxToolCalls == nil && stored.MaxRunSeconds == nil:
					cfg.MaxTurns = defaultMaxTurns
				}
			}
			cfg.ConfigVersion = currentConfigVersion
			needsSave = true
		}
		if cfg.MaxTurns < 0 {
			cfg.MaxTurns = defaultMaxTurns
			needsSave = true
		}
		if cfg.MaxToolCalls < 0 {
			cfg.MaxToolCalls = defaultMaxToolCalls
			needsSave = true
		}
		if cfg.MaxRunSeconds < 0 {
			cfg.MaxRunSeconds = defaultMaxRunSeconds
			needsSave = true
		}
		if cfg.MaxWorkers < 2 || cfg.MaxWorkers > maxTeamWorkers {
			cfg.MaxWorkers = defaultMaxWorkers
			needsSave = true
		}
		if needsSave {
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
