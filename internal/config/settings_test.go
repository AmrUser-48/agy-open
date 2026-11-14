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
		"sessionCompression", "modelProvider", "credentials", "modelBaseURL", "model", "maxTurns", "maxToolCalls", "maxRunSeconds", "approvalMode", "approvalRules",
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

func TestRequestBudgetsAreUnlimitedAndEditable(t *testing.T) {
	cfg := defaults()
	for _, key := range []string{"maxTurns", "maxToolCalls", "maxRunSeconds"} {
		value, err := SettingValue(cfg, key)
		if err != nil {
			t.Fatal(err)
		}
		if value != "0" {
			t.Errorf("%s default = %q, want unlimited (0)", key, value)
		}
		if err := SetSetting(&cfg, key, "0"); err != nil {
			t.Errorf("SetSetting(%s=0): %v", key, err)
		}
	}
	if err := SetSetting(&cfg, "maxToolCalls", "2500"); err != nil || cfg.MaxToolCalls != 2500 {
		t.Errorf("maxToolCalls override = %d, err=%v", cfg.MaxToolCalls, err)
	}
	if err := SetSetting(&cfg, "maxRunSeconds", "7200"); err != nil || cfg.MaxRunSeconds != 7200 {
		t.Errorf("maxRunSeconds override = %d, err=%v", cfg.MaxRunSeconds, err)
	}
}

func TestSessionCompressionOptionsNormalizeLegacyLZO(t *testing.T) {
	cfg := defaults()
	if err := SetSetting(&cfg, "sessionCompression", "lzo"); err != nil {
		t.Fatal(err)
	}
	if cfg.SessionCompression != "zstd" {
		t.Fatalf("legacy lzo setting normalized to %q, want zstd", cfg.SessionCompression)
	}
	value, err := SettingValue(Config{SessionCompression: "lzo"}, "sessionCompression")
	if err != nil {
		t.Fatal(err)
	}
	if value != "zstd" {
		t.Fatalf("legacy config displays compression %q, want zstd", value)
	}
	for _, field := range Settings() {
		if field.Key != "sessionCompression" {
			continue
		}
		for _, option := range field.Options {
			if option == "lzo" {
				t.Fatal("misleading lzo option is still exposed")
			}
		}
		return
	}
	t.Fatal("sessionCompression setting is missing")
}

func TestWebThemeSettingIncludesSystemAndLightModes(t *testing.T) {
	for _, name := range []string{"system", "light"} {
		cfg := defaults()
		if err := SetSetting(&cfg, "theme", name); err != nil {
			t.Errorf("SetSetting(theme=%q): %v", name, err)
			continue
		}
		if cfg.Theme != name {
			t.Errorf("Theme = %q, want %q", cfg.Theme, name)
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
	for _, test := range []struct{ provider, model string }{
		{"gemini-api", "gemini-3.8-flash"},
		{"ai-studio", "gemini-3.8-flash"},
		{"gemini-live", "gemini-3.8-live"},
		{"openai-api", "gpt-4.1"},
		{"openrouter", "openrouter/auto"},
		{"groq", "groq:qwen/qwen3.8-27b"},
		{"openai-compatible", ""},
	} {
		if err := SetSetting(&cfg, "modelProvider", test.provider); err != nil {
			t.Fatalf("SetSetting(modelProvider=%s): %v", test.provider, err)
		}
		if cfg.ModelProvider != test.provider || cfg.Model != test.model {
			t.Errorf("%s selection = provider %q model %q, want %q", test.provider, cfg.ModelProvider, cfg.Model, test.model)
		}
	}
	if err := SetSetting(&cfg, "modelProvider", "unsupported"); err == nil {
		t.Fatal("expected unsupported provider to be rejected")
	}
}
