package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func IsSupportedModelProvider(provider string) bool {
	switch provider {
	case "gemini", "gemini-api", "ai-studio", "gemini-live", "openai", "openai-api", "openai-compatible", "openrouter", "groq", "nvidia":
		return true
	default:
		return false
	}
}

// DefaultModelForProvider returns the provider's initial model selection.
// An empty value is intentional for local servers that require model discovery.
// ModelFilter returns the saved catalogue search query for one provider.
func (cfg Config) ModelFilter(provider string) string {
	return strings.TrimSpace(cfg.ModelFilters[provider])
}

// SetModelFilter persists one provider's catalogue search query. Empty queries
// remove only that provider's entry; filters for all other providers remain.
func (cfg *Config) SetModelFilter(provider, filter string) error {
	if cfg == nil {
		return errors.New("nil config")
	}
	if !IsSupportedModelProvider(provider) {
		return fmt.Errorf("unsupported model provider %q", provider)
	}
	filter = strings.TrimSpace(filter)
	if len(filter) > 256 {
		return errors.New("model filter must be 256 bytes or fewer")
	}
	next := make(map[string]string, len(cfg.ModelFilters)+1)
	for key, value := range cfg.ModelFilters {
		next[key] = value
	}
	if filter == "" {
		delete(next, provider)
	} else {
		next[provider] = filter
	}
	if len(next) == 0 {
		cfg.ModelFilters = nil
	} else {
		cfg.ModelFilters = next
	}
	return nil
}

func DefaultModelForProvider(provider string) string {
	switch provider {
	case "gemini":
		return "gemini-3.8-flash-medium"
	case "gemini-api":
		return "gemini-3.8-flash"
	case "ai-studio":
		return "gemini-3.8-flash"
	case "gemini-live":
		return "gemini-3.8-live"
	case "openai":
		return "gpt-6.1-sol"
	case "openai-api":
		return "gpt-4.1"
	case "openrouter":
		return "openrouter/auto"
	case "groq":
		return "groq:qwen/qwen3.8-27b"
	case "nvidia":
		return "nvidia:nvidia/nemotron-3-super-120b-a12b"
	default:
		return ""
	}
}

// ModelMatchesFilter applies a case-insensitive AND filter to a model ID and
// display name. Each whitespace-separated term must occur in either field.
func ModelMatchesFilter(modelID, displayName, filter string) bool {
	filter = strings.ToLower(strings.TrimSpace(filter))
	if filter == "" {
		return true
	}
	haystack := strings.ToLower(modelID + " " + displayName)
	for _, term := range strings.Fields(filter) {
		if !strings.Contains(haystack, term) {
			return false
		}
	}
	return true
}

type SafetySetting struct {
	Category  string `json:"category"`
	Threshold string `json:"threshold"`
}

type ApprovalRule struct {
	Tool         string `json:"tool"`
	CommandPrefix string `json:"commandPrefix"`
}

const (
	currentConfigVersion = 4
	defaultMaxTurns       = 0
	DefaultMaxWorkers     = 3
	MaxTeamWorkers        = 4
	defaultMaxToolCalls   = 0
	defaultMaxRunSeconds  = 0
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
	ModelFilters           map[string]string `json:"modelFilters,omitempty"`
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
	WebCodeFont            string `json:"webCodeFont,omitempty"`
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
	SystemPromptProfile    string `json:"systemPromptProfile,omitempty"`
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
		MaxWorkers:         DefaultMaxWorkers,
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

		// Remove shipped request caps by default. Preserve limits that users
		// deliberately changed from the historical shipped values.
		needsSave := false
		// Replace the old Gemini API defaults that no longer work for new API
		// projects. Both text API providers now use the Interactions endpoint.
		if cfg.ModelProvider == "gemini-api" && cfg.Model == "gemini-2.5-flash" {
			cfg.Model = DefaultModelForProvider("gemini-api")
			needsSave = true
		}
		if cfg.ModelProvider == "ai-studio" && cfg.Model == "gemini-4-hydrogen" {
			cfg.Model = DefaultModelForProvider("ai-studio")
			needsSave = true
		}
		if stored.ConfigVersion < currentConfigVersion {
			if stored.MaxTurns != nil {
				switch {
				case stored.ConfigVersion == 0 && *stored.MaxTurns == legacyDefaultMaxTurns,
					stored.ConfigVersion == 1 && *stored.MaxTurns == 50,
					*stored.MaxTurns == 100:
					cfg.MaxTurns = defaultMaxTurns
				}
			}
			if stored.MaxToolCalls != nil && *stored.MaxToolCalls == 200 {
				cfg.MaxToolCalls = defaultMaxToolCalls
			}
			if stored.MaxRunSeconds != nil && *stored.MaxRunSeconds == 1800 {
				cfg.MaxRunSeconds = defaultMaxRunSeconds
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
		if cfg.MaxWorkers < 2 || cfg.MaxWorkers > MaxTeamWorkers {
			cfg.MaxWorkers = DefaultMaxWorkers
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
