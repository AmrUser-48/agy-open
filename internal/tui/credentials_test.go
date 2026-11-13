package tui

import "testing"

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
	}
	for provider, label := range want {
		if got := providerDisplayName(provider); got != label {
			t.Errorf("providerDisplayName(%q) = %q, want %q", provider, got, label)
		}
	}
}

func TestPromptDisplayInputLeavesOrdinaryInputVisible(t *testing.T) {
	ui := &UI{input: []rune("ordinary prompt")}
	if got := string(ui.promptDisplayInput()); got != "ordinary prompt" {
		t.Fatalf("ordinary prompt display = %q", got)
	}
}
