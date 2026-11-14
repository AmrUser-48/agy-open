package agent

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/AmrUser-48/agy-open/internal/config"
)

func systemPromptTestAgent(t *testing.T) *Agent {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	a, err := New(t.TempDir(), config.Config{
		AgentRole:          "default",
		ModelProvider:      "openai-compatible",
		ModelBaseURL:       config.DefaultLocalModelBaseURL,
		Model:              "test-model",
		SessionCompression: "gzip",
		ApprovalMode:       "ask",
		MaxWorkers:         2,
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })
	return a
}

func TestSystemPromptProfilesSeedDefaultsAndPreserveUserFiles(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	profiles, err := ListSystemPromptProfiles()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(profiles, []string{"default.md", "t800.md"}) {
		t.Fatalf("initial profiles = %#v", profiles)
	}

	path := filepath.Join(home, ".config", "agy-open", "sys-prompt", "default.md")
	const customized = "# My default prompt\nKeep this customized file.\n"
	if err := os.WriteFile(path, []byte(customized), 0600); err != nil {
		t.Fatal(err)
	}
	profiles, err = ListSystemPromptProfiles()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != customized {
		t.Fatalf("existing default profile was overwritten: %q", data)
	}
	if !reflect.DeepEqual(profiles, []string{"default.md", "t800.md"}) {
		t.Fatalf("profiles after customization = %#v", profiles)
	}
}

func TestSetSystemPromptProfilePersistsContentAndFilename(t *testing.T) {
	a := systemPromptTestAgent(t)
	if err := a.SetSystemPromptProfile("t800"); err != nil {
		t.Fatal(err)
	}
	cfg := a.Config()
	if cfg.SystemPromptProfile != "t800.md" {
		t.Fatalf("active profile = %q, want t800.md", cfg.SystemPromptProfile)
	}
	if !strings.Contains(cfg.SystemPrompt, "Cyberdyne Systems Series 800") {
		t.Fatalf("T-800 prompt was not loaded: %q", cfg.SystemPrompt)
	}

	saved, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if saved.SystemPromptProfile != "t800.md" || saved.SystemPrompt != cfg.SystemPrompt {
		t.Fatalf("saved system prompt profile did not round-trip: profile=%q", saved.SystemPromptProfile)
	}
}

func TestSystemPromptProfileRejectsTraversalAndSymlinks(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	if err := ensureSystemPromptDefaults(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := loadSystemPromptProfile("../../outside"); err == nil {
		t.Fatal("path traversal profile name was accepted")
	}

	dir := filepath.Join(home, ".config", "agy-open", "sys-prompt")
	target := filepath.Join(home, "outside.md")
	if err := os.WriteFile(target, []byte("outside"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "external.md")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	profiles, err := ListSystemPromptProfiles()
	if err != nil {
		t.Fatal(err)
	}
	for _, profile := range profiles {
		if profile == "external.md" {
			t.Fatal("symlinked profile appeared in catalog")
		}
	}
	if _, _, err := loadSystemPromptProfile("external.md"); err == nil {
		t.Fatal("symlinked profile was accepted")
	}
}
