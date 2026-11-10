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

func TestDefaultExecutionBudgets(t *testing.T) {
	cfg := defaults()
	if cfg.MaxTurns != 100 || cfg.MaxToolCalls != 200 || cfg.MaxRunSeconds != 1800 || cfg.MaxWorkers != DefaultMaxWorkers {
		t.Fatalf("default execution budgets = turns:%d tools:%d seconds:%d; want 100/200/1800", cfg.MaxTurns, cfg.MaxToolCalls, cfg.MaxRunSeconds)
	}
}

func TestLegacyDefaultMaxTurnsIsMigrated(t *testing.T) {
	isolateSettingsHome(t)
	if err := os.MkdirAll(filepath.Dir(path()), 0700); err != nil {
		t.Fatalf("create settings directory: %v", err)
	}
	if err := os.WriteFile(path(), []byte(`{"maxTurns":12,"model":"gemini-3.8-flash-medium"}`), 0600); err != nil {
		t.Fatalf("write old settings: %v", err)
	}

	got, err := Load()
	if err != nil {
		t.Fatalf("load settings: %v", err)
	}
	if got.MaxTurns != 100 {
		t.Fatalf("migrated MaxTurns = %d, want 100", got.MaxTurns)
	}
	if got.ConfigVersion != currentConfigVersion {
		t.Fatalf("configVersion = %d, want %d", got.ConfigVersion, currentConfigVersion)
	}
}

func TestLegacyCustomMaxTurnsIsPreserved(t *testing.T) {
	isolateSettingsHome(t)
	if err := os.MkdirAll(filepath.Dir(path()), 0700); err != nil {
		t.Fatalf("create settings directory: %v", err)
	}
	if err := os.WriteFile(path(), []byte(`{"maxTurns":24}`), 0600); err != nil {
		t.Fatalf("write old settings: %v", err)
	}

	got, err := Load()
	if err != nil {
		t.Fatalf("load settings: %v", err)
	}
	if got.MaxTurns != 24 {
		t.Fatalf("MaxTurns = %d, want preserved value 24", got.MaxTurns)
	}
}

func TestInvalidStoredMaxTurnsUsesDefault(t *testing.T) {
	isolateSettingsHome(t)
	if err := os.MkdirAll(filepath.Dir(path()), 0700); err != nil {
		t.Fatalf("create settings directory: %v", err)
	}
	if err := os.WriteFile(path(), []byte(`{"configVersion":2,"maxTurns":-1}`), 0600); err != nil {
		t.Fatalf("write invalid settings: %v", err)
	}

	got, err := Load()
	if err != nil {
		t.Fatalf("load settings: %v", err)
	}
	if got.MaxTurns != 100 {
		t.Fatalf("MaxTurns = %d, want default 100", got.MaxTurns)
	}
}

func TestVersionedMaxTurns12IsPreserved(t *testing.T) {
	isolateSettingsHome(t)
	if err := os.MkdirAll(filepath.Dir(path()), 0700); err != nil {
		t.Fatalf("create settings directory: %v", err)
	}
	if err := os.WriteFile(path(), []byte(`{"configVersion":1,"maxTurns":12}`), 0600); err != nil {
		t.Fatalf("write versioned settings: %v", err)
	}

	got, err := Load()
	if err != nil {
		t.Fatalf("load settings: %v", err)
	}
	if got.MaxTurns != 12 {
		t.Fatalf("MaxTurns = %d, want 12", got.MaxTurns)
	}
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


func TestVersionOneShippedDefaultMigratesTo100Turns(t *testing.T) {
	isolateSettingsHome(t)
	if err := os.MkdirAll(filepath.Dir(path()), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path(), []byte(`{"configVersion":1,"maxTurns":50}`), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if got.MaxTurns != 100 || got.MaxToolCalls != 200 || got.MaxRunSeconds != 1800 {
		t.Fatalf("migrated execution budgets = turns:%d tools:%d seconds:%d", got.MaxTurns, got.MaxToolCalls, got.MaxRunSeconds)
	}
	if got.ConfigVersion != currentConfigVersion {
		t.Fatalf("configVersion = %d, want %d", got.ConfigVersion, currentConfigVersion)
	}
}

func TestZeroTurnLimitIsPreserved(t *testing.T) {
	isolateSettingsHome(t)
	if err := os.MkdirAll(filepath.Dir(path()), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path(), []byte(`{"configVersion":2,"maxTurns":0,"maxToolCalls":0,"maxRunSeconds":0}`), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if got.MaxTurns != 0 || got.MaxToolCalls != 0 || got.MaxRunSeconds != 0 {
		t.Fatalf("zero budgets were not preserved: turns:%d tools:%d seconds:%d", got.MaxTurns, got.MaxToolCalls, got.MaxRunSeconds)
	}
}

func TestLegacyShippedDefaultMigratesAndPreservesCustomTurns(t *testing.T) {
	isolateSettingsHome(t)
	if err := os.MkdirAll(filepath.Dir(path()), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path(), []byte(`{"maxTurns":12,"theme":"keep"}`), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if got.MaxTurns != 100 || got.Theme != "keep" {
		t.Fatalf("legacy migration lost settings: turns=%d theme=%q", got.MaxTurns, got.Theme)
	}
}


func TestSetSettingAllowsZeroTurnLimit(t *testing.T) {
	cfg := defaults()
	if err := SetSetting(&cfg, "maxTurns", "0"); err != nil {
		t.Fatalf("SetSetting(maxTurns=0): %v", err)
	}
	if cfg.MaxTurns != 0 {
		t.Fatalf("MaxTurns = %d, want 0", cfg.MaxTurns)
	}
	if err := SetSetting(&cfg, "maxTurns", "-1"); err == nil {
		t.Fatal("negative maxTurns unexpectedly accepted")
	}
}


func TestDefaultTeamworkWorkerLimit(t *testing.T) {
	if got := defaults().MaxWorkers; got != 3 {
		t.Fatalf("default MaxWorkers = %d, want 3", got)
	}
	cfg := defaults()
	if err := SetSetting(&cfg, "maxWorkers", "4"); err != nil {
		t.Fatalf("SetSetting(maxWorkers=4): %v", err)
	}
	if cfg.MaxWorkers != 4 {
		t.Fatalf("MaxWorkers = %d, want 4", cfg.MaxWorkers)
	}
	for _, invalid := range []string{"0", "1", "5", "invalid"} {
		if err := SetSetting(&cfg, "maxWorkers", invalid); err == nil {
			t.Errorf("SetSetting(maxWorkers=%q) unexpectedly succeeded", invalid)
		}
	}
}

func TestStoredInvalidTeamworkWorkerLimitUsesDefault(t *testing.T) {
	isolateSettingsHome(t)
	if err := os.MkdirAll(filepath.Dir(path()), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path(), []byte(`{"configVersion":3,"maxWorkers":99}`), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if got.MaxWorkers != DefaultMaxWorkers {
		t.Fatalf("MaxWorkers = %d, want %d", got.MaxWorkers, DefaultMaxWorkers)
	}
}
