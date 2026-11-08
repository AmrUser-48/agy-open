package agent

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/AmrUser-48/agy-open/internal/config"
	"github.com/AmrUser-48/agy-open/internal/gemini"
	"github.com/AmrUser-48/agy-open/internal/session"
)

func TestDeclarations(t *testing.T) {
	groups := declarations()
	if len(groups) != 6 {
		t.Fatalf("expected 6 tool groups, got %d", len(groups))
	}

	want := map[string]bool{
		"list_files": true,
		"read_file":  true,
		"search":     true,
		"write_file": true,
		"edit_file":  true,
		"shell":      true,
	}
	got := map[string]bool{}
	for _, group := range groups {
		decls, ok := group["functionDeclarations"].([]gemini.FunctionDeclaration)
		if !ok || len(decls) != 1 {
			t.Fatalf("invalid function declaration group: %#v", group)
		}
		got[decls[0].Name] = true
	}
	for name := range want {
		if !got[name] {
			t.Fatalf("missing tool %q", name)
		}
	}
}


func TestToolHistoryRedactsCommonSecrets(t *testing.T) {
	input := "Authorization: Bearer bearer-secret api_key=api-secret ghp_abcdefghijklmnopqrstuvwxyz123456 sk-abcdefghijklmnopqrstuvwxyz123456"
	got := redactHistoryText(input)
	for _, secret := range []string{"bearer-secret", "api-secret", "ghp_abcdefghijklmnopqrstuvwxyz123456", "sk-abcdefghijklmnopqrstuvwxyz123456"} {
		if strings.Contains(got, secret) {
			t.Errorf("history retained secret %q in %q", secret, got)
		}
	}
	if !strings.Contains(got, "[REDACTED]") {
		t.Fatalf("redaction marker missing: %q", got)
	}
}

func TestResumeEntryKeepsToolHistoryButExcludesItFromModelContext(t *testing.T) {
	dir := t.TempDir()
	oldPath := filepath.Join(dir, "saved.jsonl")
	old := session.Open(oldPath, "none")
	entries := []session.Message{
		{Role: "user", Content: "inspect the project"},
		{Role: "tool", Kind: "tool_call", Tool: "list_files", Target: ".", Output: "README.md", OK: true},
		{Role: "tool", Kind: "tool_approval", Tool: "shell", Target: "git status", Decision: "allowed"},
		{Role: "model", Content: "The project contains a README."},
	}
	for _, entry := range entries {
		if err := old.Add(entry); err != nil {
			t.Fatal(err)
		}
	}
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}

	a := &Agent{
		cfg:     config.Config{MaxTurns: 50, SessionCompression: "none"},
		history: session.Open(filepath.Join(dir, "active.jsonl"), "none"),
	}
	defer a.history.Close()
	if err := a.resumeEntry(session.Entry{ID: "saved", Path: oldPath}); err != nil {
		t.Fatal(err)
	}
	if len(a.chatMessages) != len(entries) {
		t.Fatalf("visible history has %d entries, want %d", len(a.chatMessages), len(entries))
	}
	if len(a.messages) != 2 {
		t.Fatalf("model context has %d messages, want only user and model entries", len(a.messages))
	}
	if a.messages[0].Role != "user" || a.messages[1].Role != "model" {
		t.Fatalf("unexpected model context roles: %#v", a.messages)
	}
	if a.chatMessages[1].Kind != "tool_call" || a.chatMessages[2].Kind != "tool_approval" {
		t.Fatalf("tool activity missing from visible history: %#v", a.chatMessages)
	}
}
