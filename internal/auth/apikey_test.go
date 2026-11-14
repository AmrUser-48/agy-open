package auth

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAPIKeyStoreSeparatesProvidersAndProtectsFiles(t *testing.T) {
	root := t.TempDir()
	store := &APIKeyStore{Path: filepath.Join(root, "config", "agy-open", "api-keys.json")}
	if err := store.Set("gemini-api", "gemini-secret-value"); err != nil {
		t.Fatal(err)
	}
	if err := store.Set("openai-api", "openai-secret-value"); err != nil {
		t.Fatal(err)
	}
	for provider, want := range map[string]string{
		"gemini-api": "gemini-secret-value",
		"openai-api": "openai-secret-value",
	} {
		got, err := store.Get(provider)
		if err != nil || got != want {
			t.Fatalf("%s key = %q, err=%v", provider, got, err)
		}
		status, err := store.Status(provider)
		if err != nil {
			t.Fatal(err)
		}
		if status.Source != "saved" || strings.Contains(status.Source, want) || strings.Contains(status.Environment, want) {
			t.Fatalf("credential status leaked or misreported key: %#v", status)
		}
	}
	info, err := os.Stat(store.Path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0600 {
		t.Fatalf("API-key file mode = %04o, want 0600", got)
	}
	dirInfo, err := os.Stat(filepath.Dir(store.Path))
	if err != nil {
		t.Fatal(err)
	}
	if got := dirInfo.Mode().Perm(); got != 0700 {
		t.Fatalf("API-key directory mode = %04o, want 0700", got)
	}
	if err := store.Remove("gemini-api"); err != nil {
		t.Fatal(err)
	}
	if got, err := store.Get("openai-api"); err != nil || got != "openai-secret-value" {
		t.Fatalf("removing Gemini key affected OpenAI key: %q, %v", got, err)
	}
	if got, err := store.Get("gemini-api"); err != nil || got != "" {
		t.Fatalf("removed Gemini key = %q, %v", got, err)
	}
}

func TestResolveAPIKeyUsesOnlySelectedProviderEnvironment(t *testing.T) {
	t.Setenv("AGY_GEMINI_API_KEY", "gemini-env-key")
	t.Setenv("AGY_AI_STUDIO_API_KEY", "ai-studio-env-key")
	t.Setenv("AGY_GEMINI_LIVE_API_KEY", "gemini-live-env-key")
	t.Setenv("GEMINI_LIVE_API_KEY", "shared-live-key")
	t.Setenv("GEMINI_API_KEY", "shared-google-key")
	t.Setenv("GOOGLE_API_KEY", "shared-fallback-key")
	t.Setenv("AGY_OPENAI_API_KEY", "openai-env-key")
	t.Setenv("OPENAI_API_KEY", "shared-openai-key")
	t.Setenv("AGY_LOCAL_API_KEY", "local-env-key")
	t.Setenv("OPENROUTER_API_KEY", "openrouter-env-key")
	 t.Setenv("AGY_OPENROUTER_API_KEY", "openrouter-prefixed-key")
	t.Setenv("AGY_GROQ_API_KEY", "groq-prefixed-key")
	t.Setenv("GROQ_API_KEY", "groq-shared-key")
	t.Setenv("AGY_GOOGLE_API_KEY", "google-shared-env-key")
	store := &APIKeyStore{Path: filepath.Join(t.TempDir(), "missing.json")}

	cases := map[string]string{
		"gemini-api": "gemini-env-key",
		"ai-studio": "ai-studio-env-key",
		"gemini-live": "gemini-live-env-key",
		"openai-api": "openai-env-key",
		"openai-compatible": "local-env-key",
		"openrouter": "openrouter-prefixed-key",
		"groq": "groq-prefixed-key",
		"gemini": "",
		"openai": "",
	}
	for provider, want := range cases {
		got, err := ResolveAPIKey(provider, store)
		if err != nil {
			t.Fatalf("ResolveAPIKey(%s): %v", provider, err)
		}
		if got != want {
			t.Errorf("ResolveAPIKey(%s) = %q, want %q", provider, got, want)
		}
	}
}

func TestGoogleAPIProvidersShareOneSavedKey(t *testing.T) {
	for _, name := range []string{"AGY_GEMINI_API_KEY", "AGY_AI_STUDIO_API_KEY", "AGY_GEMINI_LIVE_API_KEY", "GEMINI_LIVE_API_KEY", "GEMINI_API_KEY", "GOOGLE_API_KEY", "AGY_GOOGLE_API_KEY"} {
		t.Setenv(name, "")
	}
	store := &APIKeyStore{Path: filepath.Join(t.TempDir(), "keys.json")}
	if err := store.Set("gemini-api", "one-google-key"); err != nil {
		t.Fatal(err)
	}
	for _, provider := range []string{"gemini-api", "ai-studio", "gemini-live"} {
		got, err := store.Get(provider)
		if err != nil || got != "one-google-key" {
			t.Errorf("%s shared key = %q, %v", provider, got, err)
		}
	}
	if err := store.Set("google-api", "updated-google-key"); err != nil {
		t.Fatal(err)
	}
	for _, provider := range []string{"gemini-api", "ai-studio", "gemini-live", "google-api"} {
		got, err := store.Get(provider)
		if err != nil || got != "updated-google-key" {
			t.Errorf("%s updated shared key = %q, %v", provider, got, err)
		}
	}
	raw, err := os.ReadFile(store.Path)
	if err != nil {
		t.Fatal(err)
	}
	for _, legacy := range []string{"gemini-api", "ai-studio", "gemini-live"} {
		if strings.Contains(string(raw), "\""+legacy+"\"") {
			t.Errorf("shared store retained duplicate per-provider entry %q: %s", legacy, raw)
		}
	}
	if err := store.Remove("gemini-live"); err != nil {
		t.Fatal(err)
	}
	for _, provider := range []string{"gemini-api", "ai-studio", "gemini-live"} {
		got, err := store.Get(provider)
		if err != nil || got != "" {
			t.Errorf("%s key after shared removal = %q, %v", provider, got, err)
		}
	}
}

	t.Setenv("AGY_GOOGLE_API_KEY", "google-environment-key")
	for _, provider := range []string{"gemini-api", "ai-studio", "gemini-live"} {
		got, err := ResolveAPIKey(provider, store)
		if err != nil || got != "google-environment-key" {
			t.Errorf("%s did not resolve the shared Google environment key: %q, %v", provider, got, err)
		}
	}
}

func TestAPIKeyStoreStatusIdentifiesEnvironmentWithoutReturningValue(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "router-secret-value")
	store := &APIKeyStore{Path: filepath.Join(t.TempDir(), "missing.json")}
	status, err := store.Status("openrouter")
	if err != nil {
		t.Fatal(err)
	}
	if status.Source != "environment" || status.Environment != "OPENROUTER_API_KEY" {
		t.Fatalf("OpenRouter status = %#v", status)
	}
	if strings.Contains(status.Environment, "secret-value") || strings.Contains(status.Source, "router-secret-value") {
		t.Fatalf("credential value leaked in status: %#v", status)
	}
}
