package config

import (
	"encoding/json"
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


func TestDefaultExecutionBudgets(t *testing.T) {
	cfg := defaults()
	if cfg.MaxTurns != 100 {
		t.Errorf("MaxTurns = %d, want 100", cfg.MaxTurns)
	}
	if cfg.MaxToolCalls != 200 {
		t.Errorf("MaxToolCalls = %d, want 200", cfg.MaxToolCalls)
	}
	if cfg.MaxRunSeconds != 1800 {
		t.Errorf("MaxRunSeconds = %d, want 1800", cfg.MaxRunSeconds)
	}
}

func TestLegacyTurnLimitMigratesWithoutLosingSettings(t *testing.T) {
	isolateSettingsHome(t)
	if err := os.MkdirAll(filepath.Dir(path()), 0700); err != nil {
		t.Fatalf("create settings directory: %v", err)
	}
	if err := os.WriteFile(path(), []byte(`{"maxTurns":12,"systemPrompt":"keep this"}`), 0600); err != nil {
		t.Fatalf("write legacy settings: %v", err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("load settings: %v", err)
	}
	if cfg.MaxTurns != 100 || cfg.MaxToolCalls != 200 || cfg.MaxRunSeconds != 1800 {
		t.Fatalf("runtime budgets = turns:%d tools:%d seconds:%d; want 100/200/1800",
			cfg.MaxTurns, cfg.MaxToolCalls, cfg.MaxRunSeconds)
	}
	if cfg.SystemPrompt != "keep this" {
		t.Fatalf("systemPrompt = %q, want existing value to be preserved", cfg.SystemPrompt)
	}

	persisted, err := os.ReadFile(path())
	if err != nil {
		t.Fatalf("read migrated settings: %v", err)
	}
	var saved Config
	if err := json.Unmarshal(persisted, &saved); err != nil {
		t.Fatalf("parse migrated settings: %v", err)
	}
	if saved.MaxTurns != 100 || saved.MaxToolCalls != 200 || saved.MaxRunSeconds != 1800 {
		t.Fatalf("persisted budgets = turns:%d tools:%d seconds:%d",
			saved.MaxTurns, saved.MaxToolCalls, saved.MaxRunSeconds)
	}
}

func TestZeroTurnLimitDisablesOnlyTurnCap(t *testing.T) {
	isolateSettingsHome(t)
	if err := os.MkdirAll(filepath.Dir(path()), 0700); err != nil {
		t.Fatalf("create settings directory: %v", err)
	}
	if err := os.WriteFile(path(), []byte(`{"maxTurns":0,"maxToolCalls":200,"maxRunSeconds":1800}`), 0600); err != nil {
		t.Fatalf("write settings: %v", err)
	}
	cfg, err := Load()
	if err != nil {
		t.Fatalf("load settings: %v", err)
	}
	if cfg.MaxTurns != 0 {
		t.Fatalf("MaxTurns = %d, want 0", cfg.MaxTurns)
	}
	if cfg.MaxToolCalls != 200 || cfg.MaxRunSeconds != 1800 {
		t.Fatalf("other safety budgets changed: tools=%d seconds=%d", cfg.MaxToolCalls, cfg.MaxRunSeconds)
	}
}
