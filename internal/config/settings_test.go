package config

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
)

func TestSettingsExposeEveryUserConfigField(t *testing.T) {
	cfg := defaults()
	fields := Settings()
	if len(fields) < 5 || fields[0].Key != "nvidiaRPM" || fields[1].Key != "nvidiaTPM" || fields[2].Key != "nvidiaMaxOutputTokens" || fields[3].Key != "nvidiaRetryOn429" || fields[4].Key != "nvidiaMaxBackoff" {
		t.Fatalf("NVIDIA controls must be the first configurable entries, got %#v", fields[:min(5, len(fields))])
	}
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
		"nvidiaRPM", "nvidiaTPM", "nvidiaMaxOutputTokens", "nvidiaRetryOn429", "nvidiaMaxBackoff",
		"groqRPM", "groqRPD", "groqITPM", "groqOTPM", "groqTPM", "groqTPD", "groqMaxOutputTokens", "groqRetryOn429", "groqMaxBackoff", "hotkeys",
		"sessionCompression", "modelProvider", "credentials", "modelBaseURL", "model", "maxTurns", "maxToolCalls", "maxRunSeconds", "approvalMode", "approvalRules",
		"theme", "colorScheme", "altScreenMode", "notifications", "showTips",
		"verbosity", "runningLightSpeed", "showStatus", "trajectory", "mouseMode",
		"editor", "editorMode", "enableTerminalSandbox", "allowNonWorkspaceAccess",
		"effort", "temperature", "showThoughts", "googleSearchGrounding", "systemPrompt", "modelFilters",
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
	for _, provider := range []string{"gemini", "gemini-api", "ai-studio", "gemini-live", "openai", "openai-api", "openai-compatible", "openrouter", "groq", "nvidia"} {
		key := "modelFilter:" + provider
		if !seen[key] {
			t.Errorf("provider-specific model filter %q is missing from the TUI settings list", key)
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

func TestProviderModelFiltersPersistIndependently(t *testing.T) {
	cfg := defaults()
	for provider, filter := range map[string]string{
		"gemini": "gemini-3.8",
		"gemini-api": "gemini-3.8",
		"ai-studio": "gemini-3.8",
		"gemini-live": "gemini-3.8-live",
		"openai": "gpt-6",
		"openai-api": "gpt-4.1",
		"openai-compatible": "qwen",
		"openrouter": "qwen 27b",
		"groq": "qwen",
		"nvidia": "nemotron",
	} {
		key := "modelFilter:" + provider
		if err := SetSetting(&cfg, key, filter); err != nil {
			t.Fatalf("SetSetting(%s): %v", key, err)
		}
		got, err := SettingValue(cfg, key)
		if err != nil || got != filter {
			t.Errorf("SettingValue(%s) = %q, %v; want %q", key, got, err, filter)
		}
	}
	if cfg.ModelFilters["openrouter"] != "qwen 27b" ||
		cfg.ModelFilters["groq"] != "qwen" ||
		cfg.ModelFilters["gemini-api"] != "gemini-3.8" || cfg.ModelFilters["nvidia"] != "nemotron" {
		t.Fatalf("provider filters were not kept separately: %#v", cfg.ModelFilters)
	}

	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var restored Config
	if err := json.Unmarshal(raw, &restored); err != nil {
		t.Fatal(err)
	}
	for provider, want := range cfg.ModelFilters {
		if restored.ModelFilters[provider] != want {
			t.Errorf("JSON model filter %s = %q, want %q", provider, restored.ModelFilters[provider], want)
		}
	}
	if err := SetSetting(&restored, "modelFilter:openrouter", ""); err != nil {
		t.Fatal(err)
	}
	if _, ok := restored.ModelFilters["openrouter"]; ok {
		t.Fatal("empty model filter should remove only that provider's setting")
	}
	if restored.ModelFilters["groq"] != "qwen" || restored.ModelFilters["nvidia"] != "nemotron" {
		t.Fatalf("clearing OpenRouter filter changed another provider filter: %#v", restored.ModelFilters)
	}
}

func TestProviderModelFilterValidation(t *testing.T) {
	cfg := defaults()
	for _, test := range []struct{ key, value string }{
		{"modelFilter:not-a-provider", "qwen"},
		{"modelFilter:groq", strings.Repeat("x", 257)},
		{"modelFilter:openrouter", "qwen\n27b"},
	} {
		if err := SetSetting(&cfg, test.key, test.value); err == nil {
			t.Errorf("SetSetting(%q, %q) should fail", test.key, test.value)
		}
	}
}

func TestModelMatchesFilterUsesAllCaseInsensitiveTerms(t *testing.T) {
	tests := []struct {
		id, label, filter string
		want bool
	}{
		{"qwen/qwen3.8-27b", "Qwen 3.8 27B", "qwen 27b", true},
		{"anthropic/claude-sonnet", "Claude Sonnet", "CLAUDE sonnet", true},
		{"qwen/qwen3.8-27b", "Qwen 3.8 27B", "qwen 70b", false},
		{"meta/llama-4-maverick", "Llama 4 Maverick", "llama qwen", false},
		{"model-x", "Example model", "", true},
	}
	for _, test := range tests {
		if got := ModelMatchesFilter(test.id, test.label, test.filter); got != test.want {
			t.Errorf("ModelMatchesFilter(%q, %q, %q) = %v, want %v", test.id, test.label, test.filter, got, test.want)
		}
	}
}

func TestTerminalColorSchemeSettingDocumentsTerminalPalette(t *testing.T) {
	cfg := defaults()
	if err := SetSetting(&cfg, "colorScheme", "terminal"); err != nil {
		t.Fatal(err)
	}
	if cfg.ColorScheme != "terminal" {
		t.Fatalf("ColorScheme = %q, want terminal", cfg.ColorScheme)
	}
	for _, field := range Settings() {
		if field.Key != "colorScheme" {
			continue
		}
		if !strings.Contains(strings.ToLower(field.Description), "terminal profile") ||
			!strings.Contains(strings.ToLower(field.Description), "ansi 16-color") {
			t.Fatalf("terminal color scheme description does not explain native palette: %q", field.Description)
		}
		for _, option := range field.Options {
			if option == "terminal" {
				return
			}
		}
		t.Fatal("terminal scheme is missing from options")
	}
	t.Fatal("colorScheme setting is missing")
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
		{"nvidia", "nvidia:nvidia/nemotron-3-super-120b-a12b"},
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

func TestNVIDIARateLimitSettingsAreEditableAndValidated(t *testing.T) {
	cfg := defaults()
	for key, want := range map[string]string{
		"nvidiaRPM": "38",
		"nvidiaTPM": "0",
		"nvidiaMaxOutputTokens": "32768",
		"nvidiaRetryOn429": "true",
		"nvidiaMaxBackoff": "30",
	} {
		got, err := SettingValue(cfg, key)
		if err != nil || got != want {
			t.Errorf("SettingValue(%s) = %q, %v; want %q", key, got, err, want)
		}
	}
	for key, value := range map[string]string{
		"nvidiaRPM": "32",
		"nvidiaTPM": "24000",
		"nvidiaMaxOutputTokens": "4096",
		"nvidiaRetryOn429": "false",
		"nvidiaMaxBackoff": "12",
	} {
		if err := SetSetting(&cfg, key, value); err != nil {
			t.Fatalf("SetSetting(%s=%s): %v", key, value, err)
		}
		got, err := SettingValue(cfg, key)
		if err != nil || got != value {
			t.Errorf("round trip %s = %q, %v; want %q", key, got, err, value)
		}
	}
	for _, invalid := range []struct{ key, value string }{
		{"nvidiaRPM", "-1"},
		{"nvidiaRPM", "10001"},
		{"nvidiaTPM", "-1"},
		{"nvidiaTPM", "10000001"},
		{"nvidiaMaxOutputTokens", "0"},
		{"nvidiaMaxOutputTokens", "131073"},
		{"nvidiaRetryOn429", "yes"},
		{"nvidiaMaxBackoff", "0"},
		{"nvidiaMaxBackoff", "301"},
	} {
		if err := SetSetting(&cfg, invalid.key, invalid.value); err == nil {
			t.Errorf("SetSetting(%s=%s) should fail", invalid.key, invalid.value)
		}
	}
	if err := SetSetting(&cfg, "nvidiaRPM", "0"); err != nil || cfg.NVIDIARPM != 0 {
		t.Errorf("disabling NVIDIA RPM = %d, err=%v", cfg.NVIDIARPM, err)
	}
}


func TestGroqQuotaSettingsAreEditableValidatedAndPersisted(t *testing.T) {
	cfg := defaults()
	wantDefaults := map[string]string{
		"groqRPM": "0", "groqRPD": "0", "groqITPM": "0", "groqOTPM": "0",
		"groqTPM": "0", "groqTPD": "0", "groqMaxOutputTokens": "2048",
		"groqRetryOn429": "true", "groqMaxBackoff": "30",
	}
	for key, want := range wantDefaults {
		got, err := SettingValue(cfg, key)
		if err != nil || got != want {
			t.Errorf("SettingValue(%s) = %q, %v; want %q", key, got, err, want)
		}
	}
	values := map[string]string{
		"groqRPM": "30", "groqRPD": "1000", "groqITPM": "50000",
		"groqOTPM": "12000", "groqTPM": "60000", "groqTPD": "500000",
	}
	for key, value := range values {
		if err := SetSetting(&cfg, key, value); err != nil {
			t.Fatalf("SetSetting(%s=%s): %v", key, value, err)
		}
		got, err := SettingValue(cfg, key)
		if err != nil || got != value {
			t.Errorf("round trip %s = %q, %v; want %q", key, got, err, value)
		}
	}
	encoded, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var restored Config
	if err := json.Unmarshal(encoded, &restored); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]int{
		"groqRPM": 30, "groqRPD": 1000, "groqITPM": 50000,
		"groqOTPM": 12000, "groqTPM": 60000, "groqTPD": 500000,
	} {
		got, err := SettingValue(restored, key)
		if err != nil || got != strconv.Itoa(want) {
			t.Errorf("persisted %s = %q, %v; want %d", key, got, err, want)
		}
	}
	for _, invalid := range []struct{ key, value string }{
		{"groqRPM", "-1"}, {"groqRPM", "10001"},
		{"groqRPD", "-1"}, {"groqRPD", "10000001"},
		{"groqITPM", "-1"}, {"groqITPM", "1000000001"},
		{"groqOTPM", "-1"}, {"groqOTPM", "1000000001"},
		{"groqTPM", "-1"}, {"groqTPM", "1000000001"},
		{"groqTPD", "-1"}, {"groqTPD", "1000000001"},
	} {
		if err := SetSetting(&cfg, invalid.key, invalid.value); err == nil {
			t.Errorf("SetSetting(%s=%s) should fail", invalid.key, invalid.value)
		}
	}
	if err := SetSetting(&cfg, "groqITPM", "0"); err != nil || cfg.GroqITPM != 0 {
		t.Errorf("disabling Groq ITPM = %d, err=%v", cfg.GroqITPM, err)
	}
}

func TestReasoningEffortSettingAcceptsEveryAdvertisedOption(t *testing.T) {
	var options []string
	for _, field := range Settings() {
		if field.Key == "effort" {
			options = field.Options
			break
		}
	}
	if len(options) == 0 {
		t.Fatal("reasoning effort setting is missing")
	}
	cfg := defaults()
	for _, option := range options {
		if err := SetSetting(&cfg, "effort", option); err != nil {
			t.Errorf("SetSetting(effort=%q): %v", option, err)
			continue
		}
		if cfg.Effort != option {
			t.Errorf("effort = %q, want %q", cfg.Effort, option)
		}
	}
	if err := SetSetting(&cfg, "effort", "turbo"); err == nil {
		t.Fatal("unsupported reasoning effort was accepted")
	}
}


func TestContextWindowSettingsAreExposedAndEditable(t *testing.T) {
	cfg := defaults()
	for _, key := range []string{"contextWindowTokens", "contextOutputReserveTokens"} {
		if _, err := SettingValue(cfg, key); err != nil {
			t.Fatalf("SettingValue(%s): %v", key, err)
		}
		found := false
		for _, setting := range Settings() {
			if setting.Key == key {
				found = true
				if setting.Kind != "number" {
					t.Errorf("%s setting kind = %q, want number", key, setting.Kind)
				}
				break
			}
		}
		if !found {
			t.Errorf("settings omitted %s", key)
		}
	}
	if err := SetSetting(&cfg, "contextWindowTokens", "2000001"); err == nil {
		t.Fatal("accepted an excessive context window")
	}
	if err := SetSetting(&cfg, "contextOutputReserveTokens", "-1"); err == nil {
		t.Fatal("accepted a negative output reserve")
	}
}


func TestRemoteMCPGlobalSwitchIsVisibleAndEditable(t *testing.T) {
	cfg := defaults()
	value, err := SettingValue(cfg, "mcpEnabled")
	if err != nil { t.Fatal(err) }
	if value != "false" { t.Fatalf("MCP default = %q, want false", value) }
	found := false
	for _, setting := range Settings() {
		if setting.Key == "mcpEnabled" {
			found = true
			if setting.Kind != "bool" { t.Fatalf("MCP setting kind = %q, want bool", setting.Kind) }
			break
		}
	}
	if !found { t.Fatal("mcpEnabled setting is missing") }
	if err := SetSetting(&cfg, "mcpEnabled", "true"); err != nil || !cfg.MCPEnabled {
		t.Fatalf("enable MCP=%t err=%v", cfg.MCPEnabled, err)
	}
	if value, err := SettingValue(cfg, "mcpEnabled"); err != nil || value != "true" {
		t.Fatalf("enabled setting value=%q err=%v", value, err)
	}
	if err := SetSetting(&cfg, "mcpEnabled", "false"); err != nil || cfg.MCPEnabled {
		t.Fatalf("disable MCP=%t err=%v", cfg.MCPEnabled, err)
	}
	if err := SetSetting(&cfg, "mcpEnabled", "yes"); err == nil { t.Fatal("accepted invalid MCP boolean") }
}


func TestStreamingModeSettingDefaultsAndPersists(t *testing.T) {
	cfg := defaults()
	if cfg.StreamingMode != "line" {
		t.Fatalf("streaming mode default = %q, want line", cfg.StreamingMode)
	}
	fieldFound := false
	for _, field := range Settings() {
		if field.Key != "streamingMode" {
			continue
		}
		fieldFound = true
		if field.Kind != "enum" {
			t.Fatalf("streaming mode setting kind = %q, want enum", field.Kind)
		}
		if strings.Join(field.Options, ",") != "off,line,delta" {
			t.Fatalf("streaming mode options = %v, want [off line delta]", field.Options)
		}
	}
	if !fieldFound {
		t.Fatal("streaming mode setting is missing")
	}
	for _, mode := range []string{"off", "line", "delta"} {
		if err := SetSetting(&cfg, "streamingMode", mode); err != nil {
			t.Fatalf("SetSetting(streamingMode=%q): %v", mode, err)
		}
		got, err := SettingValue(cfg, "streamingMode")
		if err != nil || got != mode {
			t.Fatalf("streaming mode round trip = %q, %v; want %q", got, err, mode)
		}
		encoded, err := json.Marshal(cfg)
		if err != nil {
			t.Fatal(err)
		}
		var restored Config
		if err := json.Unmarshal(encoded, &restored); err != nil {
			t.Fatal(err)
		}
		if restored.StreamingMode != mode {
			t.Fatalf("persisted streaming mode = %q, want %q", restored.StreamingMode, mode)
		}
	}
	if err := SetSetting(&cfg, "streamingMode", "tokens"); err == nil {
		t.Fatal("unsupported streaming mode was accepted")
	}
}

func TestShowThoughtsSettingIsIndependentFromReasoningEffort(t *testing.T) {
	cfg := defaults()
	if cfg.ShowThoughts {
		t.Fatal("thought output must default to off")
	}
	if got, err := SettingValue(cfg, "showThoughts"); err != nil || got != "false" {
		t.Fatalf("showThoughts default = %q, err=%v; want false", got, err)
	}
	cfg.Effort = "high"
	if err := SetSetting(&cfg, "showThoughts", "true"); err != nil {
		t.Fatal(err)
	}
	if !cfg.ShowThoughts || cfg.Effort != "high" {
		t.Fatalf("showThoughts toggle changed reasoning effort: %#v", cfg)
	}
	if err := SetSetting(&cfg, "showThoughts", "false"); err != nil {
		t.Fatal(err)
	}
	if cfg.ShowThoughts || cfg.Effort != "high" {
		t.Fatalf("showThoughts off changed reasoning effort: %#v", cfg)
	}
	if err := SetSetting(&cfg, "showThoughts", "yes"); err == nil {
		t.Fatal("invalid showThoughts value was accepted")
	}
}


func TestTemperatureSettingIsOptionalValidatedAndPersisted(t *testing.T) {
	cfg := defaults()
	if cfg.Temperature != nil { t.Fatalf("temperature default = %v, want unset", *cfg.Temperature) }
	if got, err := SettingValue(cfg, "temperature"); err != nil || got != "default" { t.Fatalf("default temperature = %q, err=%v", got, err) }
	for _, value := range []string{"0", "0.25", "1", "2"} {
		if err := SetSetting(&cfg, "temperature", value); err != nil { t.Fatalf("SetSetting(%s): %v", value, err) }
		got, err := SettingValue(cfg, "temperature")
		if err != nil || got != value { t.Errorf("round trip = %q, %v; want %q", got, err, value) }
		encoded, err := json.Marshal(cfg); if err != nil { t.Fatal(err) }
		var restored Config; if err := json.Unmarshal(encoded, &restored); err != nil { t.Fatal(err) }
		if restored.Temperature == nil || *restored.Temperature != *cfg.Temperature { t.Errorf("JSON temperature = %#v, want %#v", restored.Temperature, cfg.Temperature) }
	}
	for _, value := range []string{"-0.01", "2.01", "NaN", "+Inf", "abc"} {
		if err := SetSetting(&cfg, "temperature", value); err == nil { t.Errorf("accepted invalid temperature %q", value) }
	}
	if err := SetSetting(&cfg, "temperature", "default"); err != nil { t.Fatal(err) }
	if cfg.Temperature != nil { t.Fatalf("default reset left temperature set: %v", *cfg.Temperature) }
	if err := ValidateTemperature(nil); err != nil { t.Fatalf("unset temperature rejected: %v", err) }
	invalid := 2.1; if err := ValidateTemperature(&invalid); err == nil { t.Fatal("invalid persisted temperature passed validation") }
}


func TestGoogleSearchGroundingSettingDefaultsOffAndPersists(t *testing.T) {
	cfg := defaults()
	if cfg.GoogleSearchGrounding { t.Fatal("Google Search grounding must default to off") }
	if got, err := SettingValue(cfg, "googleSearchGrounding"); err != nil || got != "false" {
		t.Fatalf("grounding default = %q, err=%v", got, err)
	}
	if err := SetSetting(&cfg, "googleSearchGrounding", "true"); err != nil { t.Fatal(err) }
	if !cfg.GoogleSearchGrounding { t.Fatal("grounding was not enabled") }
	encoded, err := json.Marshal(cfg); if err != nil { t.Fatal(err) }
	var restored Config
	if err := json.Unmarshal(encoded, &restored); err != nil { t.Fatal(err) }
	if !restored.GoogleSearchGrounding { t.Fatal("grounding setting did not persist") }
	if err := SetSetting(&cfg, "googleSearchGrounding", "false"); err != nil { t.Fatal(err) }
	if cfg.GoogleSearchGrounding { t.Fatal("grounding was not disabled") }
	if err := SetSetting(&cfg, "googleSearchGrounding", "yes"); err == nil { t.Fatal("invalid grounding toggle was accepted") }
}
