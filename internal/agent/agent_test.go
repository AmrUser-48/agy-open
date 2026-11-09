package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AmrUser-48/agy-open/internal/config"
	"github.com/AmrUser-48/agy-open/internal/gemini"
	"github.com/AmrUser-48/agy-open/internal/roles"
	"github.com/AmrUser-48/agy-open/internal/session"
	"github.com/AmrUser-48/agy-open/internal/tools"
	"unicode/utf8"
)

func TestNormalizeApprovalMode(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{input: "read-only", want: tools.ModeDisabled},
		{input: "strict", want: tools.ModeDisabled},
		{input: "workspace-write", want: tools.ModeWorkspace},
		{input: "ask-for-approve", want: tools.ModeAsk},
		{input: "ask", want: tools.ModeAsk},
		{input: "unrestricted", want: tools.ModeAuto},
		{input: "always-proceed", want: tools.ModeAuto},
		{input: "disable", want: tools.ModeDisabled},
		{input: "enable", want: tools.ModeWorkspace},
		{input: "autopilot", want: tools.ModeAuto},
		{input: "unknown", want: tools.ModeAsk},
	}
	for _, tt := range tests {
		if got := normalizeApprovalMode(tt.input); got != tt.want {
			t.Errorf("normalizeApprovalMode(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}


func TestDeclarations(t *testing.T) {
	groups := declarations()
	if len(groups) != 8 {
		t.Fatalf("expected 6 tool groups, got %d", len(groups))
	}

	want := map[string]bool{
		"list_files": true,
		"read_file":  true,
		"search":     true,
		"write_file": true,
		"edit_file":  true,
		"shell":      true,
		"web_search": true,
		"web_fetch":  true,
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


func TestUnrestrictedBypassesRoleToolAllowlistAndReadOnlyMode(t *testing.T) {
	workspace := t.TempDir()
	outside := t.TempDir()
	outsideFile := filepath.Join(outside, "outside.txt")
	a := &Agent{
		tools: tools.New(workspace, "auto"),
		activeRole: roles.Definition{
			Name: "reviewer",
			AllowedTools: []string{"read_file"},
			PermissionMode: roles.PermissionReadOnly,
		},
		toolDefs: declarations(),
	}
	if !a.toolAllowed("write_file") || !a.toolAllowed("shell") || !a.toolAllowed("future_tool") {
		t.Fatal("unrestricted mode did not bypass the role tool allowlist")
	}
	filtered := a.declarationsAsTools()
	if len(filtered) != len(declarations()) {
		t.Fatalf("unrestricted declarations count = %d, want %d", len(filtered), len(declarations()))
	}
	result := a.callTool(context.Background(), "write_file", map[string]any{
		"path": outsideFile, "content": "written outside workspace",
	})
	if !result.OK {
		t.Fatalf("unrestricted read-only role could not write outside workspace: %s", result.Output)
	}
	content, err := os.ReadFile(outsideFile)
	if err != nil || string(content) != "written outside workspace" {
		t.Fatalf("outside-workspace write missing: content=%q err=%v", content, err)
	}
}


func TestDisabledModeDoesNotAdvertiseRoleTools(t *testing.T) {
	a := &Agent{
		tools: tools.New(t.TempDir(), "disable"),
		toolDefs: declarations(),
	}
	if got := a.declarationsAsTools(); len(got) != 0 {
		t.Fatalf("disabled mode advertised %d tool groups", len(got))
	}
}


func TestRunLimitErrorReportsAgentRuntimeBudget(t *testing.T) {
	runCtx, cancel := context.WithTimeout(context.Background(), 0)
	defer cancel()
	err := runLimitError(context.DeadlineExceeded, runCtx, context.Background(), 1800)
	want := "agent stopped after maxRunSeconds=1800; workspace changes already made were retained"
	if err == nil || err.Error() != want {
		t.Fatalf("runLimitError = %v, want %q", err, want)
	}
}

func TestRunLimitErrorPreservesCallerDeadline(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	cancel()
	runCtx, cancelRun := context.WithTimeout(context.Background(), 0)
	defer cancelRun()
	err := runLimitError(context.DeadlineExceeded, runCtx, parent, 1800)
	if err != context.DeadlineExceeded {
		t.Fatalf("runLimitError = %v, want original caller deadline", err)
	}
}

func TestRunLimitErrorDoesNotRelabelOtherErrors(t *testing.T) {
	err := runLimitError(context.DeadlineExceeded, context.Background(), context.Background(), 1800)
	if err != context.DeadlineExceeded {
		t.Fatalf("runLimitError = %v, want original deadline error", err)
	}
}

func TestSetMaxTurnsAllowsZero(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg"))
	a := &Agent{cfg: config.Config{MaxTurns: 100, ApprovalMode: "ask"}}
	if err := a.SetMaxTurns(0); err != nil {
		t.Fatalf("SetMaxTurns(0): %v", err)
	}
	if a.Config().MaxTurns != 0 {
		t.Fatalf("MaxTurns = %d, want 0", a.Config().MaxTurns)
	}
	if err := a.SetMaxTurns(-1); err == nil {
		t.Fatal("SetMaxTurns(-1) unexpectedly succeeded")
	}
}


func TestRedactHistoryArgsPreservesValidJSONAndHidesSecrets(t *testing.T) {
	args := map[string]any{
		"command": "curl -H 'Authorization: Bearer bearer-secret-value'",
		"api_key": "api-secret-value",
		"refresh_token": "refresh-secret-value",
		"note": "ghp_abcdefghijklmnopqrstuvwxyz123456",
	}
	got := redactHistoryArgs(args)
	for _, secret := range []string{"bearer-secret-value", "api-secret-value", "refresh-secret-value", "ghp_abcdefghijklmnopqrstuvwxyz123456"} {
		if strings.Contains(got, secret) {
			t.Errorf("redacted arguments retained %q: %s", secret, got)
		}
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(got), &decoded); err != nil {
		t.Fatalf("redacted arguments are not valid JSON: %v (%s)", err, got)
	}
	for _, key := range []string{"api_key", "refresh_token"} {
		if decoded[key] != "[REDACTED]" {
			t.Errorf("%s = %#v, want [REDACTED]", key, decoded[key])
		}
	}
}

func TestRedactHistoryTextHandlesAssignmentsAndProviderTokens(t *testing.T) {
	input := "API_KEY=plain-secret Authorization: Bearer token-secret ghp_abcdefghijklmnopqrstuvwxyz123456"
	got := redactHistoryText(input)
	for _, secret := range []string{"plain-secret", "token-secret", "ghp_abcdefghijklmnopqrstuvwxyz123456"} {
		if strings.Contains(got, secret) {
			t.Errorf("history retained %q: %s", secret, got)
		}
	}
	if !strings.Contains(got, "[REDACTED]") {
		t.Fatalf("redaction marker missing: %q", got)
	}
}

func TestLimitHistoryTextKeepsUTF8Valid(t *testing.T) {
	got := limitHistoryText("abéçd", 4)
	if !utf8.ValidString(got) {
		t.Fatalf("truncated history is invalid UTF-8: %q", got)
	}
	if !strings.HasSuffix(got, "[output truncated]") {
		t.Fatalf("truncation marker missing: %q", got)
	}
}

func TestApprovalDecisionsArePersistedWithRedactedTargets(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg"))
	history, err := session.New("none")
	if err != nil {
		t.Fatal(err)
	}
	defer history.Close()
	a := &Agent{
		tools: tools.New(t.TempDir(), "ask"),
		history: history,
	}
	a.SetConfirm(func(action, target string) tools.ApprovalDecision {
		return tools.ApprovalOnce
	})
	got := a.tools.Confirm("shell", "curl -H 'Authorization: Bearer private-token'")
	if got != tools.ApprovalOnce {
		t.Fatalf("approval decision = %v, want ApprovalOnce", got)
	}
	entries, err := session.LoadRecent(history.Path(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Kind != "tool_approval" || entries[0].Decision != "allowed_once" {
		t.Fatalf("approval history entry missing: %#v", entries)
	}
	if strings.Contains(entries[0].Target, "private-token") {
		t.Fatalf("approval target retained secret: %q", entries[0].Target)
	}
}

func TestResumeEntryKeepsToolHistoryButExcludesItFromModelContext(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg"))
	history, err := session.New("none")
	if err != nil {
		t.Fatal(err)
	}
	defer history.Close()
	entries := []session.Message{
		{Role: "user", Content: "inspect"},
		{Role: "tool", Kind: "tool_call", Tool: "shell", Target: "git status", Output: "clean", OK: true},
		{Role: "model", Content: "Working tree is clean."},
	}
	for _, entry := range entries {
		if err := history.Add(entry); err != nil {
			t.Fatal(err)
		}
	}
	active, err := session.New("none")
	if err != nil {
		t.Fatal(err)
	}
	defer active.Close()
	a := &Agent{
		cfg: config.Config{MaxTurns: 100, SessionCompression: "none"},
		tools: tools.New(t.TempDir(), "ask"),
		history: active,
	}
	if err := a.resumeEntry(session.Entry{ID: "history", Path: history.Path()}); err != nil {
		t.Fatalf("resumeEntry: %v", err)
	}
	if len(a.chatMessages) != 3 || a.chatMessages[1].Kind != "tool_call" {
		t.Fatalf("visible history did not retain tool activity: %#v", a.chatMessages)
	}
	if len(a.messages) != 2 || a.messages[0].Parts[0].Text != "inspect" ||
		a.messages[1].Parts[0].Text != "Working tree is clean." {
		t.Fatalf("tool activity leaked into model context: %#v", a.messages)
	}
}
