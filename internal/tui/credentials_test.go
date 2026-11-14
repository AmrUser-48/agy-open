package tui

import (
	"strings"
	"testing"

	"github.com/AmrUser-48/agy-open/internal/config"
	"github.com/AmrUser-48/agy-open/internal/gemini"
)

func TestPromptDisplayInputMasksSecretWithoutMutatingValue(t *testing.T) {
	ui := &UI{
		configEdit: &settingEdit{secret: true},
		input:      []rune("sk-test-secret"),
	}
	if got, want := string(ui.promptDisplayInput()), "**************"; got != want {
		t.Fatalf("masked prompt = %q, want %q", got, want)
	}
	if got := string(ui.input); got != "sk-test-secret" {
		t.Fatalf("masking changed the stored input: %q", got)
	}
}

func TestProviderDisplayNamesCoverAllProviderIDs(t *testing.T) {
	want := map[string]string{
		"gemini": "Google Antigravity · OAuth",
		"gemini-api": "Google Gemini Developer API",
		"ai-studio": "Google AI Studio",
		"gemini-live": "Google Gemini Live",
		"openai": "ChatGPT · OAuth",
		"openai-api": "OpenAI API",
		"openai-compatible": "OpenAI-compatible · local",
		"openrouter": "OpenRouter API",
		"google-api": "Google APIs · shared key",
		"groq": "Groq API",
	}
	for provider, label := range want {
		if got := providerDisplayName(provider); got != label {
			t.Errorf("providerDisplayName(%q) = %q, want %q", provider, got, label)
		}
	}
}

func TestCredentialProvidersExposeOneSharedGoogleKey(t *testing.T) {
	var googleEntries int
	for _, option := range credentialProviders {
		switch option.id {
		case "google-api":
			googleEntries++
		case "gemini-api", "ai-studio", "gemini-live":
			t.Fatalf("Google provider %q should use the shared credential entry", option.id)
		}
	}
	if googleEntries != 1 {
		t.Fatalf("shared Google credential entries = %d, want 1", googleEntries)
	}
}

func TestModelPickerFiltersLargeCatalogueByIDAndLabel(t *testing.T) {
	models := []gemini.ModelOption{
		{ID: "anthropic/claude-sonnet-4", DisplayName: "Claude Sonnet 4"},
		{ID: "qwen/qwen3.8-27b", DisplayName: "Qwen 3.8 27B"},
		{ID: "openai/gpt-oss-120b", DisplayName: "GPT OSS 120B"},
	}
	picker := newModelPickerOverlay(models, "")
	if len(picker.values) != 3 {
		t.Fatalf("initial model count = %d, want 3", len(picker.values))
	}
	applyModelPickerFilter(picker, "qwen 27b", "")
	if len(picker.values) != 1 || picker.values[0] != "qwen/qwen3.8-27b" {
		t.Fatalf("filtered models = %#v, want only Qwen 3.8 27B", picker.values)
	}
	if !strings.Contains(picker.footer, "1/3 models") {
		t.Fatalf("filter count footer = %q", picker.footer)
	}
	applyModelPickerFilter(picker, "CLAUDE sonnet", "")
	if len(picker.values) != 1 || picker.values[0] != "anthropic/claude-sonnet-4" {
		t.Fatalf("case-insensitive label filter = %#v", picker.values)
	}
	applyModelPickerFilter(picker, "nothing-matches", "")
	if len(picker.values) != 1 || picker.values[0] != "" ||
		picker.items[0] != "(no models match this filter)" {
		t.Fatalf("empty filter result = %#v / %#v", picker.values, picker.items)
	}
}

func TestModelPickerFilterKeepsSelectionAndPersistsPerProvider(t *testing.T) {
	models := []gemini.ModelOption{
		{ID: "meta/llama-4-scout", DisplayName: "Llama 4 Scout"},
		{ID: "qwen/qwen3.8-27b", DisplayName: "Qwen 3.8 27B"},
		{ID: "openai/gpt-oss-120b", DisplayName: "GPT OSS 120B"},
	}
	picker := newModelPickerOverlay(models, "qwen")
	if picker.filter != "qwen" || len(picker.values) != 1 ||
		picker.values[0] != "qwen/qwen3.8-27b" {
		t.Fatalf("saved OpenRouter-style filter = %#v", picker)
	}
	applyModelPickerFilter(picker, "", "qwen/qwen3.8-27b")
	if len(picker.values) != 3 || picker.values[picker.index] != "qwen/qwen3.8-27b" {
		t.Fatalf("clearing filter lost the selected model: %#v", picker)
	}

	cfg := config.Config{ModelProvider: "openrouter"}
	if err := cfg.SetModelFilter("openrouter", "qwen 27b"); err != nil {
		t.Fatal(err)
	}
	if err := cfg.SetModelFilter("groq", "qwen"); err != nil {
		t.Fatal(err)
	}
	if got := cfg.ModelFilter("openrouter"); got != "qwen 27b" {
		t.Fatalf("OpenRouter model filter = %q", got)
	}
	if got := cfg.ModelFilter("groq"); got != "qwen" {
		t.Fatalf("Groq model filter = %q; another provider filter was overwritten", got)
	}
}

func TestPromptDisplayInputLeavesOrdinaryInputVisible(t *testing.T) {
	ui := &UI{input: []rune("ordinary prompt")}
	if got := string(ui.promptDisplayInput()); got != "ordinary prompt" {
		t.Fatalf("ordinary prompt display = %q", got)
	}
}
