package config

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

type Setting struct {
	Key         string
	Label       string
	Description string
	Kind        string
	Options     []string
}

func Settings() []Setting {
	settings := []Setting{
		{Key: "sessionCompression", Label: "Session compression", Description: "Compression for newly created conversation history files.", Kind: "enum", Options: []string{"zstd", "gzip", "lzo", "none"}},
		{Key: "modelProvider", Label: "Model provider", Description: "Provider backend. This build supports Gemini.", Kind: "enum", Options: []string{"gemini"}},
		{Key: "model", Label: "Model", Description: "Model used for requests. Opens the live model catalog.", Kind: "model"},
		{Key: "maxTurns", Label: "Maximum turns", Description: "Maximum model/tool rounds per request. Range: 1–500.", Kind: "number"},
		{Key: "approvalMode", Label: "Tool permissions", Description: "Controls file and shell access.", Kind: "enum", Options: []string{"read-only", "workspace-write", "unrestricted"}},
		{Key: "approvalRules", Label: "Command allow rules", Description: "Persistent shell-command prefixes. Option 3 in the approval prompt adds a rule; edit the JSON list here to review or remove rules.", Kind: "approval-rules"},
		{Key: "theme", Label: "Web UI theme", Description: "Color theme used by the web interface.", Kind: "enum", Options: []string{"dark", "catppuccin", "monokai", "gruvbox", "solarized", "tokyo-night", "dracula", "lcd7s"}},
		{Key: "colorScheme", Label: "TUI color scheme", Description: "ANSI palette used by the terminal interface.", Kind: "enum", Options: []string{"terminal", "catppuccin", "monokai", "gruvbox", "solarized", "tokyo-night", "dracula", "dark", "lcd7s"}},
		{Key: "altScreenMode", Label: "Alternate screen", Description: "Terminal alternate-screen behavior: never, always, or auto.", Kind: "enum", Options: []string{"never", "always", "auto"}},
		{Key: "notifications", Label: "Notifications", Description: "Play a terminal bell when a request completes.", Kind: "bool"},
		{Key: "showTips", Label: "Startup tips", Description: "Show the startup usage hint.", Kind: "bool"},
		{Key: "verbosity", Label: "Tool output verbosity", Description: "Amount of tool output shown when trajectory is enabled.", Kind: "enum", Options: []string{"low", "medium", "high"}},
		{Key: "runningLightSpeed", Label: "Spinner speed", Description: "Animation speed while the agent is working.", Kind: "enum", Options: []string{"fast", "medium", "slow", "off"}},
		{Key: "showStatus", Label: "Status line", Description: "Show request status in the full-screen TUI.", Kind: "bool"},
		{Key: "trajectory", Label: "Tool trajectory", Description: "Include tool execution output in the transcript.", Kind: "bool"},
		{Key: "mouseMode", Label: "Mouse support", Description: "Enable mouse reporting for the full-screen TUI.", Kind: "bool"},
		{Key: "editor", Label: "External editor", Description: "Editor executable used for prompt and file editing. Use auto to honor $EDITOR.", Kind: "text"},
		{Key: "editorMode", Label: "Editor mode", Description: "Prompt editor behavior: default or advanced.", Kind: "enum", Options: []string{"default", "advanced"}},
		{Key: "enableTerminalSandbox", Label: "Terminal sandbox", Description: "Run shell commands inside bubblewrap when available. Enabling it fails closed if bubblewrap is unavailable.", Kind: "bool"},
		{Key: "allowNonWorkspaceAccess", Label: "Allow outside-workspace paths", Description: "Permit file tools to access paths outside the selected workspace. Shell commands still run in the workspace.", Kind: "bool"},
		{Key: "effort", Label: "Reasoning effort", Description: "Model reasoning effort.", Kind: "enum", Options: []string{"low", "medium", "high"}},
		{Key: "systemPrompt", Label: "System prompt", Description: "Complete system instruction sent to the model. Enter saves; Shift+Enter inserts a newline.", Kind: "text-multiline"},
	}
	for _, category := range []string{
		"HARM_CATEGORY_HARASSMENT",
		"HARM_CATEGORY_HATE_SPEECH",
		"HARM_CATEGORY_SEXUALLY_EXPLICIT",
		"HARM_CATEGORY_DANGEROUS_CONTENT",
	} {
		settings = append(settings, Setting{
			Key:         "safetySettings:" + category,
			Label:       "Safety · " + strings.TrimPrefix(category, "HARM_CATEGORY_"),
			Description: "Gemini content filter threshold for " + strings.ToLower(strings.ReplaceAll(category, "HARM_CATEGORY_", "")) + ".",
			Kind:        "enum",
			Options:     []string{"BLOCK_NONE", "BLOCK_LOW_AND_ABOVE", "BLOCK_MEDIUM_AND_ABOVE", "BLOCK_HIGH_AND_ABOVE"},
		})
	}
	return settings
}

func SettingValue(cfg Config, key string) (string, error) {
	switch key {
	case "sessionCompression":
		return cfg.SessionCompression, nil
	case "modelProvider":
		return cfg.ModelProvider, nil
	case "model":
		return cfg.Model, nil
	case "maxTurns":
		return strconv.Itoa(cfg.MaxTurns), nil
	case "approvalMode":
		switch normalizeApprovalMode(cfg.ApprovalMode) {
		case "deny":
			return "read-only", nil
		case "auto":
			return "unrestricted", nil
		default:
			return "workspace-write", nil
		}
	case "approvalRules":
		rules := cfg.ApprovalRules
		if rules == nil {
			rules = []ApprovalRule{}
		}
		data, err := json.MarshalIndent(rules, "", "  ")
		return string(data), err
	case "theme":
		return cfg.Theme, nil
	case "colorScheme":
		return cfg.ColorScheme, nil
	case "altScreenMode":
		return cfg.AltScreenMode, nil
	case "notifications":
		return strconv.FormatBool(cfg.Notifications), nil
	case "showTips":
		return strconv.FormatBool(cfg.ShowTips), nil
	case "verbosity":
		return cfg.Verbosity, nil
	case "runningLightSpeed":
		return cfg.RunningLightSpeed, nil
	case "showStatus":
		return strconv.FormatBool(cfg.ShowStatus), nil
	case "trajectory":
		return strconv.FormatBool(cfg.Trajectory), nil
	case "mouseMode":
		return strconv.FormatBool(cfg.MouseMode), nil
	case "editor":
		return cfg.Editor, nil
	case "editorMode":
		return cfg.EditorMode, nil
	case "enableTerminalSandbox":
		return strconv.FormatBool(cfg.EnableTerminalSandbox), nil
	case "allowNonWorkspaceAccess":
		return strconv.FormatBool(cfg.AllowNonWorkspaceAccess), nil
	case "effort":
		return cfg.Effort, nil
	case "systemPrompt":
		return cfg.SystemPrompt, nil
	}
	if strings.HasPrefix(key, "safetySettings:") {
		category := strings.TrimPrefix(key, "safetySettings:")
		for _, setting := range cfg.SafetySettings {
			if setting.Category == category {
				return setting.Threshold, nil
			}
		}
		return "", fmt.Errorf("unknown safety category %q", category)
	}
	return "", fmt.Errorf("unknown setting %q", key)
}

func SetSetting(cfg *Config, key, value string) error {
	if cfg == nil {
		return fmt.Errorf("nil config")
	}
	if key != "systemPrompt" {
		value = strings.TrimSpace(value)
	}
	switch key {
	case "sessionCompression":
		if !oneOf(value, "zstd", "gzip", "lzo", "none") { return fmt.Errorf("session compression must be zstd, gzip, lzo, or none") }
		cfg.SessionCompression = value
	case "modelProvider":
		if value != "gemini" { return fmt.Errorf("this build supports only the gemini provider") }
		cfg.ModelProvider = value
	case "model":
		if value == "" { return fmt.Errorf("model is required") }
		cfg.Model = value
	case "maxTurns":
		n, err := strconv.Atoi(value)
		if err != nil || n < 1 || n > 500 { return fmt.Errorf("max turns must be between 1 and 500") }
		cfg.MaxTurns = n
	case "approvalMode":
		switch value {
		case "read-only": cfg.ApprovalMode = "deny"
		case "workspace-write": cfg.ApprovalMode = "ask"
		case "unrestricted": cfg.ApprovalMode = "auto"
		default: return fmt.Errorf("invalid tool permission mode %q", value)
		}
	case "approvalRules":
		var rules []ApprovalRule
		if strings.TrimSpace(value) != "" {
			if err := json.Unmarshal([]byte(value), &rules); err != nil {
				return fmt.Errorf("command allow rules must be a JSON array: %w", err)
			}
		}
		for i := range rules {
			rules[i].Tool = strings.ToLower(strings.TrimSpace(rules[i].Tool))
			rules[i].CommandPrefix = strings.TrimSpace(rules[i].CommandPrefix)
			if rules[i].Tool == "" {
				rules[i].Tool = "shell"
			}
			if rules[i].Tool != "shell" || rules[i].CommandPrefix == "" {
				return fmt.Errorf("approval rule %d must specify tool=\"shell\" and a non-empty commandPrefix", i+1)
			}
		}
		cfg.ApprovalRules = rules
	case "theme":
		if !oneOf(value, "dark", "catppuccin", "monokai", "gruvbox", "solarized", "tokyo-night", "dracula", "lcd7s") { return fmt.Errorf("unsupported web theme %q", value) }
		cfg.Theme = value
	case "colorScheme":
		if !oneOf(value, "terminal", "catppuccin", "monokai", "gruvbox", "solarized", "tokyo-night", "dracula", "dark", "lcd7s") { return fmt.Errorf("unsupported TUI color scheme %q", value) }
		cfg.ColorScheme = value
	case "altScreenMode":
		if !oneOf(value, "never", "always", "auto") { return fmt.Errorf("alternate screen mode must be never, always, or auto") }
		cfg.AltScreenMode = value
	case "notifications":
		v, err := strconv.ParseBool(value); if err != nil { return fmt.Errorf("notifications must be true or false") }; cfg.Notifications = v
	case "showTips":
		v, err := strconv.ParseBool(value); if err != nil { return fmt.Errorf("showTips must be true or false") }; cfg.ShowTips = v
	case "verbosity":
		if !oneOf(value, "low", "medium", "high") { return fmt.Errorf("verbosity must be low, medium, or high") }; cfg.Verbosity = value
	case "runningLightSpeed":
		if !oneOf(value, "fast", "medium", "slow", "off") { return fmt.Errorf("spinner speed must be fast, medium, slow, or off") }; cfg.RunningLightSpeed = value
	case "showStatus":
		v, err := strconv.ParseBool(value); if err != nil { return fmt.Errorf("showStatus must be true or false") }; cfg.ShowStatus = v
	case "trajectory":
		v, err := strconv.ParseBool(value); if err != nil { return fmt.Errorf("trajectory must be true or false") }; cfg.Trajectory = v
	case "mouseMode":
		v, err := strconv.ParseBool(value); if err != nil { return fmt.Errorf("mouseMode must be true or false") }; cfg.MouseMode = v
	case "editor":
		if value == "" { value = "auto" }; cfg.Editor = value
	case "editorMode":
		if !oneOf(value, "default", "advanced") { return fmt.Errorf("editorMode must be default or advanced") }; cfg.EditorMode = value
	case "enableTerminalSandbox":
		v, err := strconv.ParseBool(value); if err != nil { return fmt.Errorf("enableTerminalSandbox must be true or false") }; cfg.EnableTerminalSandbox = v
	case "allowNonWorkspaceAccess":
		v, err := strconv.ParseBool(value); if err != nil { return fmt.Errorf("allowNonWorkspaceAccess must be true or false") }; cfg.AllowNonWorkspaceAccess = v
	case "effort":
		if !oneOf(value, "low", "medium", "high") { return fmt.Errorf("effort must be low, medium, or high") }; cfg.Effort = value
	case "systemPrompt":
		cfg.SystemPrompt = value
	default:
		if strings.HasPrefix(key, "safetySettings:") {
			category := strings.TrimPrefix(key, "safetySettings:")
			if !oneOf(category, "HARM_CATEGORY_HARASSMENT", "HARM_CATEGORY_HATE_SPEECH", "HARM_CATEGORY_SEXUALLY_EXPLICIT", "HARM_CATEGORY_DANGEROUS_CONTENT") {
				return fmt.Errorf("unknown safety category %q", category)
			}
			if !oneOf(value, "BLOCK_NONE", "BLOCK_LOW_AND_ABOVE", "BLOCK_MEDIUM_AND_ABOVE", "BLOCK_HIGH_AND_ABOVE") {
				return fmt.Errorf("invalid safety threshold %q", value)
			}
			for i := range cfg.SafetySettings {
				if cfg.SafetySettings[i].Category == category { cfg.SafetySettings[i].Threshold = value; return nil }
			}
			cfg.SafetySettings = append(cfg.SafetySettings, SafetySetting{Category: category, Threshold: value})
			return nil
		}
		return fmt.Errorf("unknown setting %q", key)
	}
	return nil
}

func oneOf(value string, options ...string) bool {
	for _, option := range options {
		if value == option { return true }
	}
	return false
}

func normalizeApprovalMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "read-only", "strict", "deny": return "deny"
	case "unrestricted", "always-proceed", "auto", "yolo": return "auto"
	default: return "ask"
	}
}
