package agent

import (
	"testing"

	"github.com/AmrUser-48/agy-open/internal/config"
	"github.com/AmrUser-48/agy-open/internal/gemini"
	"github.com/AmrUser-48/agy-open/internal/tools"
)

func TestNormalizeApprovalMode(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{input: "read-only", want: "deny"},
		{input: "strict", want: "deny"},
		{input: "workspace-write", want: "ask"},
		{input: "ask-for-approve", want: "ask"},
		{input: "ask", want: "ask"},
		{input: "unrestricted", want: "auto"},
		{input: "always-proceed", want: "auto"},
		{input: "unknown", want: "ask"},
	}
	for _, tt := range tests {
		if got := normalizeApprovalMode(tt.input); got != tt.want {
			t.Errorf("normalizeApprovalMode(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}


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

func TestNormalizeConfigApprovalRules(t *testing.T) {
	rules, err := normalizeConfigApprovalRules([]config.ApprovalRule{
		{CommandPrefix: " grep "},
		{Tool: "SHELL", CommandPrefix: "grep"},
		{Tool: "shell", CommandPrefix: "git status"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(rules) != 2 || rules[0] != (config.ApprovalRule{Tool: "shell", CommandPrefix: "grep"}) {
		t.Fatalf("normalized rules = %#v", rules)
	}
	if _, err := normalizeConfigApprovalRules([]config.ApprovalRule{{Tool: "write_file", CommandPrefix: "grep"}}); err == nil {
		t.Fatal("expected unsupported tool rule to be rejected")
	}
}

func TestAddApprovalRulePersistsAndActivatesRule(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", home+"/xdg")
	a := &Agent{
		cfg: config.Config{ApprovalRules: []config.ApprovalRule{}},
		tools: tools.New(t.TempDir(), "ask"),
	}
	if err := a.AddApprovalRule("shell", "grep"); err != nil {
		t.Fatal(err)
	}
	if len(a.tools.PersistentAllowRules) != 1 || a.tools.PersistentAllowRules[0].Prefix != "grep" {
		t.Fatalf("active rules = %#v", a.tools.PersistentAllowRules)
	}
	stored, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(stored.ApprovalRules) != 1 || stored.ApprovalRules[0].CommandPrefix != "grep" {
		t.Fatalf("stored rules = %#v", stored.ApprovalRules)
	}
}

func TestClearDropsConversationApprovalRules(t *testing.T) {
	a := &Agent{tools: tools.New(t.TempDir(), "ask")}
	a.tools.SessionAllowRules = []tools.CommandApprovalRule{{Action: "shell", Prefix: "grep"}}
	a.Clear()
	if len(a.tools.SessionAllowRules) != 0 {
		t.Fatalf("session approval rules remain after Clear: %#v", a.tools.SessionAllowRules)
	}
}
