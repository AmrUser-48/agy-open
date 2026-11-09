package config

import (
	"encoding/json"
	"testing"
)

func TestSettingsExposeEveryUserConfigField(t *testing.T) {
	cfg := defaults()
	fields := Settings()
	seen := make(map[string]bool, len(fields))
	for _, field := range fields {
		if seen[field.Key] {
			t.Fatalf("duplicate setting key %q", field.Key)
		}
		seen[field.Key] = true
		if _, err := SettingValue(cfg, field.Key); err != nil {
			t.Errorf("SettingValue(%q): %v", field.Key, err)
		}
	}

	required := []string{
		"sessionCompression", "modelProvider", "modelBaseURL", "model", "maxTurns", "approvalMode", "approvalRules",
		"theme", "colorScheme", "altScreenMode", "notifications", "showTips",
		"verbosity", "runningLightSpeed", "showStatus", "trajectory", "mouseMode",
		"editor", "editorMode", "enableTerminalSandbox", "allowNonWorkspaceAccess",
		"effort", "systemPrompt",
		"safetySettings:HARM_CATEGORY_HARASSMENT",
		"safetySettings:HARM_CATEGORY_HATE_SPEECH",
		"safetySettings:HARM_CATEGORY_SEXUALLY_EXPLICIT",
		"safetySettings:HARM_CATEGORY_DANGEROUS_CONTENT",
	}
	for _, key := range required {
		if !seen[key] {
			t.Errorf("config field %q is missing from the TUI settings list", key)
		}
	}
}

func TestSetSettingRoundTripsAllConfigValues(t *testing.T) {
	cfg := defaults()
	for _, field := range Settings() {
		value, err := SettingValue(cfg, field.Key)
		if err != nil {
			t.Fatalf("read %s: %v", field.Key, err)
		}
		if err := SetSetting(&cfg, field.Key, value); err != nil {
			t.Errorf("write %s=%q: %v", field.Key, value, err)
		}
	}
}

func TestSystemPromptPreservesWhitespace(t *testing.T) {
	cfg := defaults()
	want := "  first line\n  second line\n"
	if err := SetSetting(&cfg, "systemPrompt", want); err != nil {
		t.Fatal(err)
	}
	if cfg.SystemPrompt != want {
		t.Fatalf("SystemPrompt = %q, want %q", cfg.SystemPrompt, want)
	}
}

func TestSafetyThresholdCanBeEdited(t *testing.T) {
	cfg := defaults()
	key := "safetySettings:HARM_CATEGORY_HATE_SPEECH"
	if err := SetSetting(&cfg, key, "BLOCK_MEDIUM_AND_ABOVE"); err != nil {
		t.Fatal(err)
	}
	got, err := SettingValue(cfg, key)
	if err != nil {
		t.Fatal(err)
	}
	if got != "BLOCK_MEDIUM_AND_ABOVE" {
		t.Fatalf("threshold = %q", got)
	}
}

func TestApprovalRulesSettingRoundTrip(t *testing.T) {
	cfg := defaults()
	want := []ApprovalRule{{Tool: "shell", CommandPrefix: "grep"}, {Tool: "shell", CommandPrefix: "git status"}}
	data := "[\n  {\"tool\":\"shell\",\"commandPrefix\":\"grep\"},\n  {\"tool\":\"shell\",\"commandPrefix\":\"git status\"}\n]"
	if err := SetSetting(&cfg, "approvalRules", data); err != nil { t.Fatal(err) }
	if len(cfg.ApprovalRules) != len(want) { t.Fatalf("got %d rules, want %d", len(cfg.ApprovalRules), len(want)) }
	for i := range want { if cfg.ApprovalRules[i] != want[i] { t.Errorf("rule %d = %#v, want %#v", i, cfg.ApprovalRules[i], want[i]) } }
	value, err := SettingValue(cfg, "approvalRules"); if err != nil { t.Fatal(err) }
	var got []ApprovalRule; if err := json.Unmarshal([]byte(value), &got); err != nil { t.Fatal(err) }
	if len(got) != len(want) { t.Fatalf("round-trip produced %d rules, want %d", len(got), len(want)) }
}

func TestApprovalRulesRejectUnsupportedToolsAndEmptyPrefixes(t *testing.T) {
	for _, input := range []string{"[{\"tool\":\"write_file\",\"commandPrefix\":\"grep\"}]", "[{\"tool\":\"shell\",\"commandPrefix\":\"  \"}]"} {
		if err := SetSetting(&Config{}, "approvalRules", input); err == nil { t.Errorf("expected invalid approval rules to fail: %s", input) }
	}
}

func TestLocalModelProviderSettingUsesEndpointAndClearsCloudModel(t *testing.T) {
	cfg := defaults()
	if err := SetSetting(&cfg, "modelProvider", "openai-compatible"); err != nil {
		t.Fatal(err)
	}
	if cfg.ModelProvider != "openai-compatible" {
		t.Fatalf("provider = %q", cfg.ModelProvider)
	}
	if cfg.Model != "" {
		t.Fatalf("local provider inherited cloud model %q", cfg.Model)
	}
	if cfg.ModelBaseURL != DefaultLocalModelBaseURL {
		t.Fatalf("local base URL = %q", cfg.ModelBaseURL)
	}
	customURL := "http://127.0.0.1:8000/v1"
	if err := SetSetting(&cfg, "modelBaseURL", customURL); err != nil {
		t.Fatal(err)
	}
	if cfg.ModelBaseURL != customURL {
		t.Fatalf("custom local base URL = %q", cfg.ModelBaseURL)
	}
	if err := SetSetting(&cfg, "model", "qwen2.5-coder:7b"); err != nil {
		t.Fatal(err)
	}
	if cfg.Model != "qwen2.5-coder:7b" {
		t.Fatalf("local model ID = %q", cfg.Model)
	}
}

func TestModelProviderSettingSwitchesModelDefaults(t *testing.T) {
	cfg := defaults()
	if err := SetSetting(&cfg, "modelProvider", "openai"); err != nil {
		t.Fatal(err)
	}
	if cfg.ModelProvider != "openai" || cfg.Model != "gpt-6.1-sol" {
		t.Fatalf("OpenAI selection = provider %q model %q", cfg.ModelProvider, cfg.Model)
	}
	if err := SetSetting(&cfg, "modelProvider", "gemini"); err != nil {
		t.Fatal(err)
	}
	if cfg.ModelProvider != "gemini" || cfg.Model != "gemini-3.8-flash-medium" {
		t.Fatalf("Gemini selection = provider %q model %q", cfg.ModelProvider, cfg.Model)
	}
	if err := SetSetting(&cfg, "modelProvider", "unsupported"); err == nil {
		t.Fatal("expected unsupported provider to be rejected")
	}
}
