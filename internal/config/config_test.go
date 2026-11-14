package config

import (
	"os"
	"strings"
	"path/filepath"
	"testing"
)

func isolateSettingsHome(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg"))
}

func TestWebCodeFontPersists(t *testing.T) {
	isolateSettingsHome(t)
	cfg := defaults()
	cfg.WebFont = "system"
	cfg.WebCodeFont = "terminus"
	if err := Save(cfg); err != nil {
		t.Fatalf("save appearance settings: %v", err)
	}
	got, err := Load()
	if err != nil {
		t.Fatalf("load appearance settings: %v", err)
	}
	if got.WebFont != "system" || got.WebCodeFont != "terminus" {
		t.Fatalf("loaded font choices = UI:%q code:%q, want system/terminus", got.WebFont, got.WebCodeFont)
	}
}

func TestDefaultSessionCompressionIsGzip(t *testing.T) {
	if got := defaults().SessionCompression; got != "gzip" {
		t.Fatalf("default session compression = %q, want gzip", got)
	}
}

func TestDefaultExecutionBudgets(t *testing.T) {
	cfg := defaults()
	if cfg.MaxTurns != 0 || cfg.MaxToolCalls != 0 || cfg.MaxRunSeconds != 0 || cfg.MaxWorkers != DefaultMaxWorkers {
		t.Fatalf("default execution budgets = turns:%d tools:%d seconds:%d; want unlimited (0/0/0)", cfg.MaxTurns, cfg.MaxToolCalls, cfg.MaxRunSeconds)
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
	if got.MaxTurns != 0 {
		t.Fatalf("migrated MaxTurns = %d, want unlimited (0)", got.MaxTurns)
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
	if got.MaxTurns != 0 {
		t.Fatalf("MaxTurns = %d, want unlimited default 0", got.MaxTurns)
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

func TestStaleGeminiAPIDefaultMigratesToGemini38Flash(t *testing.T) {
	isolateSettingsHome(t)
	if err := os.MkdirAll(filepath.Dir(path()), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path(), []byte(`{"configVersion":4,"modelProvider":"gemini-api","model":"gemini-2.5-flash"}`), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if got.ModelProvider != "gemini-api" || got.Model != "gemini-3.8-flash" {
		t.Fatalf("migrated Gemini API settings = provider %q model %q", got.ModelProvider, got.Model)
	}
}

func TestStaleAIStudioDefaultMigratesToGemini38Flash(t *testing.T) {
	isolateSettingsHome(t)
	if err := os.MkdirAll(filepath.Dir(path()), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path(), []byte(`{"configVersion":4,"modelProvider":"ai-studio","model":"gemini-4-hydrogen"}`), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if got.ModelProvider != "ai-studio" || got.Model != "gemini-3.8-flash" {
		t.Fatalf("migrated AI Studio settings = provider %q model %q", got.ModelProvider, got.Model)
	}
}

func TestModelFiltersPersistInSettingsJSON(t *testing.T) {
	isolateSettingsHome(t)
	cfg := defaults()
	for provider, filter := range map[string]string{
		"openrouter": "qwen 27b",
		"groq": "qwen",
		"gemini-api": "gemini-3.8",
		"nvidia": "nemotron",
	} {
		if err := cfg.SetModelFilter(provider, filter); err != nil {
			t.Fatal(err)
		}
	}
	if err := Save(cfg); err != nil {
		t.Fatalf("save provider filters: %v", err)
	}
	raw, err := os.ReadFile(path())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"\"modelFilters\"", "\"openrouter\": \"qwen 27b\"", "\"groq\": \"qwen\"", "\"nvidia\": \"nemotron\""} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("settings.json is missing %q: %s", want, raw)
		}
	}
	loaded, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	for provider, want := range cfg.ModelFilters {
		if got := loaded.ModelFilter(provider); got != want {
			t.Errorf("loaded %s model filter = %q, want %q", provider, got, want)
		}
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


func TestVersionOneShippedDefaultsMigrateToUnlimited(t *testing.T) {
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
	if got.MaxTurns != 0 || got.MaxToolCalls != 0 || got.MaxRunSeconds != 0 {
		t.Fatalf("migrated execution budgets = turns:%d tools:%d seconds:%d", got.MaxTurns, got.MaxToolCalls, got.MaxRunSeconds)
	}
	if got.ConfigVersion != currentConfigVersion {
		t.Fatalf("configVersion = %d, want %d", got.ConfigVersion, currentConfigVersion)
	}
}

func TestVersionThreeShippedCapsMigrateToUnlimited(t *testing.T) {
	isolateSettingsHome(t)
	if err := os.MkdirAll(filepath.Dir(path()), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path(), []byte(`{"configVersion":3,"maxTurns":100,"maxToolCalls":200,"maxRunSeconds":1800,"theme":"keep"}`), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if got.MaxTurns != 0 || got.MaxToolCalls != 0 || got.MaxRunSeconds != 0 || got.Theme != "keep" {
		t.Fatalf("migrated limits/settings = turns:%d tools:%d seconds:%d theme:%q", got.MaxTurns, got.MaxToolCalls, got.MaxRunSeconds, got.Theme)
	}
	if got.ConfigVersion != currentConfigVersion {
		t.Fatalf("configVersion = %d, want %d", got.ConfigVersion, currentConfigVersion)
	}
}

func TestCustomExecutionCapsSurviveMigration(t *testing.T) {
	isolateSettingsHome(t)
	if err := os.MkdirAll(filepath.Dir(path()), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path(), []byte(`{"configVersion":3,"maxTurns":240,"maxToolCalls":500,"maxRunSeconds":7200}`), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if got.MaxTurns != 240 || got.MaxToolCalls != 500 || got.MaxRunSeconds != 7200 {
		t.Fatalf("custom limits were not preserved: turns:%d tools:%d seconds:%d", got.MaxTurns, got.MaxToolCalls, got.MaxRunSeconds)
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
	if got.MaxTurns != 0 || got.Theme != "keep" {
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

func TestNVIDIARateLimitSettingsPersistInSettingsJSON(t *testing.T) {
	isolateSettingsHome(t)
	cfg := defaults()
	cfg.NVIDIARPM = 34
	cfg.NVIDIATPM = 18000
	cfg.NVIDIAMaxOutputTokens = 4096
	cfg.NVIDIARetryOn429 = false
	cfg.NVIDIAMaxBackoff = 17
	if err := Save(cfg); err != nil {
		t.Fatalf("save NVIDIA limits: %v", err)
	}
	got, err := Load()
	if err != nil {
		t.Fatalf("load NVIDIA limits: %v", err)
	}
	if got.NVIDIARPM != 34 || got.NVIDIATPM != 18000 || got.NVIDIAMaxOutputTokens != 4096 || got.NVIDIARetryOn429 || got.NVIDIAMaxBackoff != 17 {
		t.Fatalf("loaded NVIDIA limits = RPM:%d TPM:%d output:%d", got.NVIDIARPM, got.NVIDIATPM, got.NVIDIAMaxOutputTokens)
	}

	got.NVIDIARPM = 0
	if err := Save(got); err != nil {
		t.Fatal(err)
	}
	disabled, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if disabled.NVIDIARPM != 0 {
		t.Fatalf("explicit RPM disable was not persisted: %d", disabled.NVIDIARPM)
	}
}

func TestVersionFourSettingsReceiveNVIDIARetryDefaults(t *testing.T) {
	isolateSettingsHome(t)
	if err := os.MkdirAll(filepath.Dir(path()), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path(), []byte(`{"configVersion":4,"modelProvider":"nvidia","nvidiaRPM":34,"nvidiaTPM":16000}`), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if got.ConfigVersion != currentConfigVersion {
		t.Fatalf("configVersion = %d, want %d", got.ConfigVersion, currentConfigVersion)
	}
	if !got.NVIDIARetryOn429 || got.NVIDIAMaxBackoff != DefaultNVIDIAMaxBackoffSeconds {
		t.Fatalf("migrated retry policy = enabled:%t backoff:%d, want true/%d", got.NVIDIARetryOn429, got.NVIDIAMaxBackoff, DefaultNVIDIAMaxBackoffSeconds)
	}
}
