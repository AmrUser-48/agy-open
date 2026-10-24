package config

import (
	"os"
	"path/filepath"
	"testing"
)

func isolateSettingsHome(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg"))
}

func TestDefaultSafetySettingsUseBlockNone(t *testing.T) {
	cfg := defaults()
	want := []SafetySetting{
		{Category: "HARM_CATEGORY_HARASSMENT", Threshold: "BLOCK_NONE"},
		{Category: "HARM_CATEGORY_HATE_SPEECH", Threshold: "BLOCK_NONE"},
		{Category: "HARM_CATEGORY_SEXUALLY_EXPLICIT", Threshold: "BLOCK_NONE"},
		{Category: "HARM_CATEGORY_DANGEROUS_CONTENT", Threshold: "BLOCK_NONE"},
	}
	if len(cfg.SafetySettings) != len(want) {
		t.Fatalf("got %d default safety settings, want %d", len(cfg.SafetySettings), len(want))
	}
	for i, setting := range want {
		if cfg.SafetySettings[i] != setting {
			t.Errorf("safetySettings[%d] = %#v, want %#v", i, cfg.SafetySettings[i], setting)
		}
	}
}

func TestSystemPromptSettingRoundTrip(t *testing.T) {
	isolateSettingsHome(t)

	want := "Follow my instructions.\nKeep answers concise."
	cfg := defaults()
	cfg.SystemPrompt = want

	if err := Save(cfg); err != nil {
		t.Fatalf("save settings: %v", err)
	}

	got, err := Load()
	if err != nil {
		t.Fatalf("load settings: %v", err)
	}
	if got.SystemPrompt != want {
		t.Fatalf("systemPrompt = %q, want %q", got.SystemPrompt, want)
	}
}

func TestEmptySystemPromptOverridesDefault(t *testing.T) {
	isolateSettingsHome(t)

	cfg := defaults()
	cfg.SystemPrompt = ""
	if err := Save(cfg); err != nil {
		t.Fatalf("save settings: %v", err)
	}

	got, err := Load()
	if err != nil {
		t.Fatalf("load settings: %v", err)
	}
	if got.SystemPrompt != "" {
		t.Fatalf("systemPrompt = %q, want empty string", got.SystemPrompt)
	}
}

func TestPreviousSettingsPathIsReadAsFallback(t *testing.T) {
	isolateSettingsHome(t)

	if err := os.MkdirAll(filepath.Dir(previousSettingsPath()), 0700); err != nil {
		t.Fatalf("create previous settings directory: %v", err)
	}
	const customPrompt = "Migrated user instructions"
	if err := os.WriteFile(previousSettingsPath(), []byte(`{"systemPrompt":"Migrated user instructions"}`), 0600); err != nil {
		t.Fatalf("write previous settings: %v", err)
	}

	got, err := Load()
	if err != nil {
		t.Fatalf("load settings: %v", err)
	}
	if got.SystemPrompt != customPrompt {
		t.Fatalf("systemPrompt = %q, want %q", got.SystemPrompt, customPrompt)
	}
}

func TestExistingSettingsKeepDefaultSystemPrompt(t *testing.T) {
	isolateSettingsHome(t)

	if err := os.MkdirAll(filepath.Dir(path()), 0700); err != nil {
		t.Fatalf("create settings directory: %v", err)
	}
	if err := os.WriteFile(path(), []byte(`{"model":"gemini-3.8-flash-medium"}`), 0600); err != nil {
		t.Fatalf("write legacy-format settings: %v", err)
	}

	got, err := Load()
	if err != nil {
		t.Fatalf("load settings: %v", err)
	}
	if got.SystemPrompt != defaults().SystemPrompt {
		t.Fatalf("systemPrompt = %q, want default %q", got.SystemPrompt, defaults().SystemPrompt)
	}
}
