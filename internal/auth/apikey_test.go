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
	t.Setenv("GEMINI_API_KEY", "shared-google-key")
	t.Setenv("GOOGLE_API_KEY", "shared-fallback-key")
	t.Setenv("AGY_OPENAI_API_KEY", "openai-env-key")
	t.Setenv("OPENAI_API_KEY", "shared-openai-key")
	t.Setenv("AGY_LOCAL_API_KEY", "local-env-key")
	store := &APIKeyStore{Path: filepath.Join(t.TempDir(), "missing.json")}

	cases := map[string]string{
		"gemini-api": "gemini-env-key",
		"ai-studio": "shared-google-key",
		"gemini-live": "shared-google-key",
		"openai-api": "openai-env-key",
		"openai-compatible": "local-env-key",
		"openrouter": "",
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
