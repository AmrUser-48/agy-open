package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/AmrUser-48/agy-open/internal/auth"
	"github.com/AmrUser-48/agy-open/internal/config"
	"github.com/AmrUser-48/agy-open/internal/gemini"
	"github.com/AmrUser-48/agy-open/internal/openaicompat"
	"github.com/AmrUser-48/agy-open/internal/roles"
	"github.com/AmrUser-48/agy-open/internal/session"
	"github.com/AmrUser-48/agy-open/internal/tools"
)

func TestNormalizeApprovalMode(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{input: "read-only", want: tools.ModeReadOnly},
		{input: "strict", want: tools.ModeReadOnly},
		{input: "workspace-write", want: tools.ModeWorkspace},
		{input: "ask-for-approve", want: tools.ModeAsk},
		{input: "ask", want: tools.ModeAsk},
		{input: "unrestricted", want: tools.ModeAuto},
		{input: "always-proceed", want: tools.ModeAuto},
		{input: "disable", want: tools.ModeDisabled},
		{input: "enable", want: tools.ModeWorkspace},
		{input: "autopilot", want: tools.ModeAuto},
		{input: "unknown", want: ""},
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
		t.Fatalf("expected 8 default tool groups, got %d", len(groups))
	}

	want := map[string]bool{
		"list_files":        true,
		"read_file":         true,
		"search":            true,
		"write_file":        true,
		"edit_file":         true,
		"shell":             true,
		"web_search":        true,
		"web_fetch":         true,
	}
	got := map[string]bool{}
	for _, group := range groups {
		decls, ok := group["functionDeclarations"].([]gemini.FunctionDeclaration)
		if !ok || len(decls) != 1 {
			t.Fatalf("invalid function declaration group: %#v", group)
		}
		got[decls[0].Name] = true
		if decls[0].Name == "shell" {
			for _, expected := range []string{"non-login shell command", "Batch related checks", "bound output", "separate dependent or mutating commands"} {
				if !strings.Contains(decls[0].Description, expected) {
					t.Errorf("shell description missing batching guidance %q", expected)
				}
			}
		}
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


func TestWorkspaceWriteAdvertisesToolsForLegacyDefaultRole(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", home)
	rolesDir := filepath.Join(home, "agy-open", "roles")
	if err := os.MkdirAll(rolesDir, 0700); err != nil {
		t.Fatal(err)
	}
	legacyRole := []byte(`{"name":"default","description":"legacy default","instructions":"legacy role file"}`)
	if err := os.WriteFile(filepath.Join(rolesDir, "default.json"), legacyRole, 0600); err != nil {
		t.Fatal(err)
	}

	definitions, err := roles.Load()
	if err != nil {
		t.Fatal(err)
	}
	defaultRole, ok := roles.Find(definitions, "default")
	if !ok {
		t.Fatal("default role missing")
	}
	root := t.TempDir()
	a := &Agent{
		tools:      tools.New(root, "workspace-write"),
		activeRole: defaultRole,
		toolDefs:   declarations(),
	}
	if got := a.declarationsAsTools(); len(got) != len(declarations()) {
		t.Fatalf("workspace-write advertised %d tool groups, want %d", len(got), len(declarations()))
	}
	result := a.callTool(context.Background(), "write_file", map[string]any{
		"path": "created.txt", "content": "workspace-write works",
	})
	if !result.OK {
		t.Fatalf("workspace-write file tool failed: %s", result.Output)
	}
	content, err := os.ReadFile(filepath.Join(root, "created.txt"))
	if err != nil || string(content) != "workspace-write works" {
		t.Fatalf("workspace-write did not create the file: content=%q err=%v", content, err)
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
	got := limitHistoryText("abéçd", 3)
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


func TestResumeLastLoadsMostRecentPreviousSession(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	historyDir := filepath.Join(home, ".agy", "history")
	if err := os.MkdirAll(historyDir, 0700); err != nil {
		t.Fatal(err)
	}
	previousPath := filepath.Join(historyDir, "20261110T100000.000Z.jsonl")
	activePath := filepath.Join(historyDir, "20261111T100000.000Z.jsonl")
	previous := "{\"role\":\"user\",\"content\":\"previous conversation\"}\n{\"role\":\"model\",\"content\":\"restored answer\"}\n"
	active := "{\"role\":\"user\",\"content\":\"current conversation\"}\n"
	if err := os.WriteFile(previousPath, []byte(previous), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(activePath, []byte(active), 0600); err != nil {
		t.Fatal(err)
	}

	a := &Agent{
		cfg: config.Config{MaxTurns: 100, SessionCompression: "none"},
		tools: tools.New(t.TempDir(), tools.ModeAsk),
		history: session.Open(activePath, "none"),
	}
	if err := a.ResumeLast(); err != nil {
		t.Fatalf("ResumeLast: %v", err)
	}
	if got := a.history.Path(); got != previousPath {
		t.Fatalf("resumed path = %q, want most recent previous session %q", got, previousPath)
	}
	if len(a.chatMessages) != 2 || a.chatMessages[0].Content != "previous conversation" ||
		a.chatMessages[1].Content != "restored answer" {
		t.Fatalf("restored chat history = %#v", a.chatMessages)
	}
}

func TestReadOnlyModeOnlyAdvertisesReadTools(t *testing.T) {
	a := &Agent{
		tools:      tools.New(t.TempDir(), "read-only"),
		activeRole: roles.Definition{Name: "default", AllowedTools: []string{"*"}},
		toolDefs:   declarations(),
	}
	var got []string
	for _, group := range a.declarationsAsTools() {
		decls, ok := group["functionDeclarations"].([]gemini.FunctionDeclaration)
		if !ok {
			t.Fatalf("invalid declaration group: %#v", group)
		}
		for _, decl := range decls {
			got = append(got, decl.Name)
			if !readOnlyTool(decl.Name) {
				t.Errorf("read-only mode advertised mutating tool %q", decl.Name)
			}
		}
	}
	want := map[string]bool{
		"list_files": true, "read_file": true, "search": true,
		"web_search": true, "web_fetch": true,
	}
	if len(got) != len(want) {
		t.Fatalf("read-only exposed %v, want exactly %d read tools", got, len(want))
	}
	for _, name := range got {
		if !want[name] {
			t.Errorf("unexpected read-only tool %q", name)
		}
	}
}

func TestInvalidPermissionModeIsRejectedByAgent(t *testing.T) {
	_, err := New(t.TempDir(), config.Config{
		ModelProvider: "openai-compatible",
		Model:         "test-model",
		ApprovalMode:  "not-a-mode",
		SessionCompression: "none",
	})
	if err == nil || !strings.Contains(err.Error(), "invalid permission mode") {
		t.Fatalf("invalid permission mode error = %v, want validation error", err)
	}
}


func TestDirectShellActivityPersistsCommandAndCorrelatesEvents(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg"))
	history, err := session.New("none")
	if err != nil {
		t.Fatal(err)
	}
	defer history.Close()

	a := &Agent{
		tools: tools.New(t.TempDir(), "autopilot"),
		history: history,
	}
	var events []Event
	a.SetEventSink(func(event Event) { events = append(events, event) })
	command := "printf 'command-activity-test'"
	result := a.RunShell(context.Background(), command)
	if !result.OK || !strings.Contains(result.Output, "command-activity-test") {
		t.Fatalf("RunShell result = %#v", result)
	}
	if len(events) != 2 || events[0].Kind != "tool_start" || events[1].Kind != "tool_end" {
		t.Fatalf("shell events = %#v; want correlated start/end", events)
	}
	for _, event := range events {
		if event.Tool != "shell" || event.Command != command || event.CallID == "" {
			t.Errorf("shell event omitted command/call identity: %#v", event)
		}
		if !strings.Contains(event.Arguments, command) {
			t.Errorf("shell event omitted exact arguments: %#v", event)
		}
	}
	if events[0].CallID != events[1].CallID {
		t.Fatalf("shell call IDs differ: %q != %q", events[0].CallID, events[1].CallID)
	}
	entries, err := session.LoadRecent(history.Path(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Kind != "tool_call" ||
		entries[0].Command != command || entries[0].Arguments != events[0].Arguments ||
		entries[0].CallID != events[0].CallID {
		t.Fatalf("stored shell activity lost exact command: %#v", entries)
	}
}


func TestTeamworkTaskFromPrompt(t *testing.T) {
	tests := []struct {
		prompt string
		task   string
		ok     bool
	}{
		{prompt: "/teamwork inspect permissions", task: "inspect permissions", ok: true},
		{prompt: "/teamwork", task: "", ok: true},
		{prompt: "  /teamwork-preview\timplement choreography ", ok: false},
		{prompt: "/teamworkish inspect", ok: false},
		{prompt: "please use /teamwork for this", ok: false},
		{prompt: "", ok: false},
	}
	for _, tt := range tests {
		task, ok := teamworkTaskFromPrompt(tt.prompt)
		if task != tt.task || ok != tt.ok {
			t.Errorf("teamworkTaskFromPrompt(%q) = (%q, %v), want (%q, %v)", tt.prompt, task, ok, tt.task, tt.ok)
		}
	}
}

func TestTeamworkRecoveryFromPrompt(t *testing.T) {
	tests := []struct {
		prompt string
		id     string
		ok     bool
	}{
		{prompt: "/teamwork-recover", id: "", ok: true},
		{prompt: "/teamwork-recover 20261111T100000.000Z/worker-1", id: "20261111T100000.000Z/worker-1", ok: true},
		{prompt: " /teamwork-recover\t20261111T100000.000Z/worker-2 ", id: "20261111T100000.000Z/worker-2", ok: true},
		{prompt: "/teamwork-recovery worker-1", ok: false},
		{prompt: "please /teamwork-recover worker-1", ok: false},
	}
	for _, tt := range tests {
		id, ok := teamworkRecoveryFromPrompt(tt.prompt)
		if id != tt.id || ok != tt.ok {
			t.Errorf("teamworkRecoveryFromPrompt(%q) = (%q, %v), want (%q, %v)", tt.prompt, id, ok, tt.id, tt.ok)
		}
	}
}

func TestTeamworkPreviewTaskFromPrompt(t *testing.T) {
	tests := []struct {
		prompt string
		task   string
		ok     bool
	}{
		{prompt: "/teamwork-preview inspect permissions", task: "inspect permissions", ok: true},
		{prompt: "  /teamwork-preview\timplement choreography ", task: "implement choreography", ok: true},
		{prompt: "/teamwork inspect permissions", ok: false},
		{prompt: "/teamwork-previewish inspect", ok: false},
		{prompt: "please use /teamwork-preview for this", ok: false},
		{prompt: "", ok: false},
	}
	for _, tt := range tests {
		task, ok := teamworkPreviewTaskFromPrompt(tt.prompt)
		if task != tt.task || ok != tt.ok {
			t.Errorf("teamworkPreviewTaskFromPrompt(%q) = (%q, %v), want (%q, %v)", tt.prompt, task, ok, tt.task, tt.ok)
		}
	}
}


func TestSetRuntimeInstructionIsPerRunAndDoesNotChangeRoleOrSavedPrompt(t *testing.T) {
	a := &Agent{
		cfg: config.Config{SystemPrompt: "saved prompt", AgentRole: "default"},
		activeRole: roles.Definition{
			Name: "default",
			Instructions: "default role instructions",
			AllowedTools: []string{"read_file"},
		},
	}
	instruction := "You are a consulting agent. Give concise, evidence-based recommendations."
	if err := a.SetRuntimeInstruction(instruction); err != nil {
		t.Fatal(err)
	}
	if a.runtimeInstruction != instruction {
		t.Fatalf("runtime instruction = %q, want %q", a.runtimeInstruction, instruction)
	}
	if a.cfg.SystemPrompt != "saved prompt" || a.cfg.AgentRole != "default" {
		t.Fatalf("runtime instruction changed saved configuration: %#v", a.cfg)
	}
	if a.activeRole.Name != "default" || !a.activeRole.AllowsTool("read_file") || a.activeRole.AllowsTool("shell") {
		t.Fatalf("runtime instruction changed active role permissions: %#v", a.activeRole)
	}
	if err := a.SetRuntimeInstruction(strings.Repeat("x", maxCustomSystemPromptBytes+1)); err == nil {
		t.Fatal("oversized runtime instruction unexpectedly accepted")
	}
}


func TestNormalizePermissionModeRejectsUnknownValues(t *testing.T) {
	for _, mode := range []string{"disable", "read-only", "workspace-write", "ask", "autopilot", "unrestricted"} {
		if got, err := normalizePermissionMode(mode); err != nil || got == "" {
			t.Errorf("normalizePermissionMode(%q) = %q, %v; expected a valid mode", mode, got, err)
		}
	}
	if got, err := normalizePermissionMode("not-a-mode"); err == nil || got != "" {
		t.Fatalf("normalizePermissionMode(unknown) = %q, %v; expected validation error", got, err)
	}
}

func TestUpdateConfigRejectsInvalidPermissionModeWithoutChangingRuntime(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg"))
	root := t.TempDir()
	cfg := config.Config{
		ModelProvider: "openai-compatible",
		ModelBaseURL: config.DefaultLocalModelBaseURL,
		Model: "test-model",
		MaxTurns: 100,
		MaxToolCalls: 200,
		MaxRunSeconds: 1800,
		MaxWorkers: config.DefaultMaxWorkers,
		ApprovalMode: "ask",
		SessionCompression: "none",
		Effort: "medium",
		AgentRole: "default",
	}
	defaultRole := roles.Definition{
		Name: "default",
		AllowedTools: []string{"list_files", "read_file", "search", "write_file", "edit_file", "shell", "web_search", "web_fetch"},
	}
	a := &Agent{
		cfg: cfg,
		tools: tools.New(root, "ask"),
		activeRole: defaultRole,
		roleDefinitions: []roles.Definition{defaultRole},
	}
	invalid := cfg
	invalid.ApprovalMode = "not-a-mode"
	if err := a.UpdateConfig(invalid); err == nil || !strings.Contains(err.Error(), "permission mode") {
		t.Fatalf("UpdateConfig(invalid permission) = %v; expected validation error", err)
	}
	if a.ApprovalMode() != "ask" || a.Config().ApprovalMode != "ask" {
		t.Fatalf("invalid settings changed active permission mode: runtime=%q config=%q", a.ApprovalMode(), a.Config().ApprovalMode)
	}
}


func TestUpdateConfigCanSwitchProviderWithDefaultEffort(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("load default config: %v", err)
	}
	cfg.ModelProvider = "nvidia"
	cfg.Model = "nvidia:nvidia/nemotron-3-super-120b-a12b"
	cfg.Effort = "default"

	a, err := New(t.TempDir(), cfg)
	if err != nil {
		t.Fatalf("create NVIDIA agent: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	next := a.Config()
	next.ModelProvider = "openai-compatible"
	next.Model = ""
	next.ModelBaseURL = "http://127.0.0.1:8000/v1"
	if err := a.UpdateConfig(next); err != nil {
		t.Fatalf("switch provider while preserving native default effort: %v", err)
	}
	if got := a.Config().ModelProvider; got != "openai-compatible" {
		t.Fatalf("provider after switch = %q, want openai-compatible", got)
	}
	if got := a.Effort(); got != "default" {
		t.Fatalf("effort after provider switch = %q, want default", got)
	}
}

func TestTeamWorkerAutopilotStillHonoursRoleToolAllowlist(t *testing.T) {
	a := &Agent{
		tools: tools.New(t.TempDir(), tools.ModeAuto),
		activeRole: roles.Definition{
			Name: "coder",
			AllowedTools: []string{"read_file", "write_file", "edit_file", "shell"},
		},
	}
	a.tools.TeamWorkerMode = true
	for _, name := range []string{"read_file", "write_file", "edit_file", "shell"} {
		if !a.toolAllowed(name) {
			t.Errorf("worker role could not use declared tool %q", name)
		}
	}
	for _, name := range []string{"list_files", "search", "web_search", "web_fetch", "future_tool"} {
		if a.toolAllowed(name) {
			t.Errorf("worker autopilot bypassed role allowlist for %q", name)
		}
	}
}


func TestChatMessagesRecoversLegacyShellCommand(t *testing.T) {
	a := &Agent{chatMessages: []session.Message{{
		Role: "tool", Kind: "tool_call", Tool: "shell",
		Arguments: `{"command":"make -C kernel oldconfig"}`,
		Output: "HOSTCC scripts/kconfig/conf.o", OK: true,
	}}}
	messages := a.ChatMessages()
	if len(messages) != 1 {
		t.Fatalf("ChatMessages returned %d entries, want 1", len(messages))
	}
	if got := messages[0].Command; got != "make -C kernel oldconfig" {
		t.Fatalf("restored command = %q, want exact command", got)
	}
	if a.chatMessages[0].Command != "" {
		t.Fatal("ChatMessages mutated the agent's stored in-memory history")
	}
}

func TestNotifyNormalizesShellCommandForEveryEventConsumer(t *testing.T) {
	var got []Event
	a := &Agent{eventSink: func(event Event) { got = append(got, event) }}
	a.notify(Event{Kind: "tool_start", Tool: "shell", Target: "make -C kernel oldconfig"})
	a.notify(Event{Kind: "tool_end", Tool: "shell", Command: "make -C kernel oldconfig"})
	if len(got) != 2 {
		t.Fatalf("received %d events, want 2", len(got))
	}
	for i, event := range got {
		if event.Command != "make -C kernel oldconfig" || event.Target != "make -C kernel oldconfig" {
			t.Errorf("event %d lost command/target: %#v", i, event)
		}
	}
}


func TestRunPromptRoutesExplicitTeamworkCommandToCoordinator(t *testing.T) {
	a := &Agent{}
	err := a.RunPrompt(context.Background(), "/teamwork", &strings.Builder{})
	if err == nil || err.Error() != "teamwork task is empty" {
		t.Fatalf("RunPrompt(/teamwork) = %v, want coordinator validation error", err)
	}
}

func TestNativeGoogleProvidersUseSeparateClientsAndDefaultModels(t *testing.T) {
	t.Setenv("GEMINI_LIVE_WS_URL", "")
	aiClient, err := newModelClient("ai-studio", "", "", nil, nil, nil)
	if err != nil { t.Fatal(err) }
	ai, ok := aiClient.(*gemini.AIStudioClient)
	if !ok { t.Fatalf("AI Studio provider client = %T", aiClient) }
	if ai.ModelName() != "gemini-3.8-flash" { t.Fatalf("AI Studio default model = %q", ai.ModelName()) }

	liveClient, err := newModelClient("gemini-live", "", "", nil, nil, nil)
	if err != nil { t.Fatal(err) }
	live, ok := liveClient.(*gemini.LiveClient)
	if !ok { t.Fatalf("Gemini Live provider client = %T", liveClient) }
	if live.ModelName() != "gemini-3.8-live" { t.Fatalf("Gemini Live default model = %q", live.ModelName()) }
}

func TestSavingOpenRouterKeyRefreshesActiveModelClient(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg"))
	t.Setenv("AGY_OPENROUTER_API_KEY", "")

	store := auth.NewAPIKeyStore()
	client, err := newModelClient("openrouter", "openrouter/auto", "", nil, nil, store)
	if err != nil {
		t.Fatal(err)
	}
	agent := &Agent{
		cfg: config.Config{ModelProvider: "openrouter", Model: "openrouter/auto"},
		apiKeys: store,
		model: client,
	}

	before, ok := agent.model.(*openaicompat.Client)
	if !ok || before.APIKey != "" {
		t.Fatalf("initial OpenRouter client = %#v", agent.model)
	}
	if err := agent.SetProviderAPIKey("openrouter", "tui-saved-openrouter-key"); err != nil {
		t.Fatal(err)
	}
	after, ok := agent.model.(*openaicompat.Client)
	if !ok {
		t.Fatalf("refreshed OpenRouter client = %T", agent.model)
	}
	if after.APIKey != "tui-saved-openrouter-key" {
		t.Fatalf("refreshed OpenRouter API key = %q; TUI-saved key was not applied", after.APIKey)
	}
	if after.Model != "openrouter/auto" {
		t.Fatalf("refresh changed selected model to %q", after.Model)
	}
}

func TestLogoutRefreshesActiveOpenRouterClient(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg"))
	t.Setenv("AGY_OPENROUTER_API_KEY", "")

	store := auth.NewAPIKeyStore()
	if err := store.Set("openrouter", "saved-key"); err != nil {
		t.Fatal(err)
	}
	client, err := newModelClient("openrouter", "openrouter/auto", "", nil, nil, store)
	if err != nil {
		t.Fatal(err)
	}
	agent := &Agent{
		cfg: config.Config{ModelProvider: "openrouter", Model: "openrouter/auto"},
		apiKeys: store,
		model: client,
	}
	if err := agent.Logout(); err != nil {
		t.Fatal(err)
	}
	refreshed, ok := agent.model.(*openaicompat.Client)
	if !ok || refreshed.APIKey != "" {
		t.Fatalf("logout left the removed API key active: %#v", agent.model)
	}
}

func TestSavingOpenRouterKeyAllowsImmediateModelDiscovery(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg"))
	t.Setenv("AGY_OPENROUTER_API_KEY", "")

	var authorization string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" {
			t.Errorf("OpenRouter models path = %q", r.URL.Path)
		}
		authorization = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"openai/gpt-4.1"},{"id":"anthropic/claude-sonnet"}]}`))
	}))
	defer server.Close()

	store := auth.NewAPIKeyStore()
	client, err := newModelClient("openrouter", "openrouter/auto", "", nil, nil, store)
	if err != nil {
		t.Fatal(err)
	}
	agent := &Agent{
		cfg: config.Config{ModelProvider: "openrouter", Model: "openrouter/auto"},
		apiKeys: store,
		model: client,
	}
	if err := agent.SetProviderAPIKey("openrouter", "saved-in-tui-key"); err != nil {
		t.Fatal(err)
	}
	refreshed, ok := agent.model.(*openaicompat.Client)
	if !ok {
		t.Fatalf("refreshed client = %T", agent.model)
	}
	refreshed.BaseURL = server.URL
	refreshed.HTTP = server.Client()

	options, err := agent.ListModelOptions(context.Background())
	if err != nil {
		t.Fatalf("TUI model discovery after saving credential: %v", err)
	}
	if authorization != "Bearer saved-in-tui-key" {
		t.Fatalf("model discovery Authorization = %q; TUI-saved key was not used", authorization)
	}
	if len(options) != 2 || options[0].ID != "anthropic/claude-sonnet" || options[1].ID != "openai/gpt-4.1" {
		t.Fatalf("OpenRouter catalogue = %#v", options)
	}
}

func TestRemovingOpenRouterKeyRefreshesActiveModelClient(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg"))
	t.Setenv("AGY_OPENROUTER_API_KEY", "")

	store := auth.NewAPIKeyStore()
	if err := store.Set("openrouter", "saved-key"); err != nil {
		t.Fatal(err)
	}
	client, err := newModelClient("openrouter", "openrouter/auto", "", nil, nil, store)
	if err != nil {
		t.Fatal(err)
	}
	agent := &Agent{
		cfg: config.Config{ModelProvider: "openrouter", Model: "openrouter/auto"},
		apiKeys: store,
		model: client,
	}
	if err := agent.RemoveProviderAPIKey("openrouter"); err != nil {
		t.Fatal(err)
	}
	refreshed, ok := agent.model.(*openaicompat.Client)
	if !ok || refreshed.APIKey != "" {
		t.Fatalf("removed key left a stale API key in model client: %#v", agent.model)
	}
}

func TestSavingSharedGoogleKeyRefreshesActiveGoogleClient(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg"))
	for _, name := range []string{
		"AGY_GEMINI_API_KEY", "AGY_AI_STUDIO_API_KEY", "AGY_GEMINI_LIVE_API_KEY",
		"GEMINI_LIVE_API_KEY", "AGY_GOOGLE_API_KEY", "GEMINI_API_KEY", "GOOGLE_API_KEY",
	} {
		t.Setenv(name, "")
	}

	store := auth.NewAPIKeyStore()
	client, err := newModelClient("gemini-api", "gemini-3.8-flash", "", nil, nil, store)
	if err != nil {
		t.Fatal(err)
	}
	agent := &Agent{
		cfg: config.Config{ModelProvider: "gemini-api", Model: "gemini-3.8-flash"},
		apiKeys: store,
		model: client,
	}
	if err := agent.SetProviderAPIKey("google-api", "one-google-key"); err != nil {
		t.Fatal(err)
	}
	refreshed, ok := agent.model.(*gemini.Client)
	if !ok || refreshed.Key != "one-google-key" || refreshed.ModelName() != "gemini-3.8-flash" {
		t.Fatalf("Gemini Developer API client did not receive shared key: %#v", agent.model)
	}
}

func TestGeminiDeveloperAPIUsesGenerateContentClient(t *testing.T) {
	client, err := newModelClient("gemini-api", "gemini-3.8-flash", "", nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	developerAPI, ok := client.(*gemini.Client)
	if !ok {
		t.Fatalf("Gemini Developer API client = %T, want GenerateContent client", client)
	}
	if developerAPI.ModelName() != "gemini-3.8-flash" {
		t.Fatalf("Gemini Developer API model = %q", developerAPI.ModelName())
	}
}

func TestGroqProviderUsesDedicatedNamespacedClient(t *testing.T) {
	client, err := newModelClient("groq", "groq:qwen/qwen3.8-27b", "", nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	compatible, ok := client.(*openaicompat.Client)
	if !ok {
		t.Fatalf("Groq provider client = %T, want *openaicompat.Client", client)
	}
	if compatible.Model != "groq:qwen/qwen3.8-27b" || compatible.BaseURL != openaicompat.DefaultGroqURL {
		t.Fatalf("Groq provider configuration = model %q base %q", compatible.Model, compatible.BaseURL)
	}
}

func TestNVIDIAProviderUsesDedicatedNamespacedClient(t *testing.T) {
	client, err := newModelClient("nvidia", "nvidia:nvidia/nemotron-3-super-120b-a12b", "", nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	compatible, ok := client.(*openaicompat.Client)
	if !ok {
		t.Fatalf("NVIDIA provider client = %T, want *openaicompat.Client", client)
	}
	if compatible.Model != "nvidia:nvidia/nemotron-3-super-120b-a12b" || compatible.BaseURL != openaicompat.DefaultNVIDIAURL || !compatible.RequireAPIKey {
		t.Fatalf("NVIDIA provider configuration = model %q base %q required_key=%v", compatible.Model, compatible.BaseURL, compatible.RequireAPIKey)
	}
}

func TestAIStudioModelOutputStepsRestoreOnResume(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg"))

	stored, err := session.New("none")
	if err != nil {
		t.Fatal(err)
	}
	rawOutput := json.RawMessage(`{"type":"model_output","content":[{"type":"text","text":"AI Studio interaction restored."}]}`)
	for _, message := range []session.Message{
		{Role: "user", Content: "Continue this interaction."},
		{Role: "model", Kind: "provider_steps", ProviderSteps: []json.RawMessage{rawOutput}},
		{Role: "model", Content: "Gemini API interaction restored."},
	} {
		if err := stored.Add(message); err != nil {
			t.Fatal(err)
		}
	}
	path := stored.Path()
	if err := stored.Close(); err != nil {
		t.Fatal(err)
	}

	activeHistory, err := session.New("none")
	if err != nil {
		t.Fatal(err)
	}
	a := &Agent{
		cfg: config.Config{ModelProvider: "ai-studio", SessionCompression: "none"},
		history: activeHistory,
		tools: tools.New(t.TempDir(), "ask"),
	}
	if err := a.resumeEntry(session.Entry{ID: "saved", Path: path}); err != nil {
		t.Fatal(err)
	}
	defer a.Close()

	if len(a.messages) != 2 || len(a.messages[1].NativeSteps) != 1 ||
		string(a.messages[1].NativeSteps[0]) != string(rawOutput) {
		t.Fatalf("restored AI Studio Interactions history = %#v", a.messages)
	}
	if len(a.chatMessages) != 2 || a.chatMessages[0].Content != "Continue this interaction." {
		t.Fatalf("visible Gemini API session history = %#v", a.chatMessages)
	}
}

func TestAIStudioNativeStepsAndFunctionResultsRestoreOnResume(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg"))

	stored, err := session.New("none")
	if err != nil { t.Fatal(err) }
	rawCall := json.RawMessage(`{"type":"function_call","id":"call-1","name":"list_files","arguments":{"path":"."}}`)
	rawOutput := json.RawMessage(`{"type":"model_output","content":[{"type":"text","text":"The files are listed."}]}`)
	for _, message := range []session.Message{
		{Role:"user",Content:"List the workspace."},
		{Role:"model",Kind:"provider_steps",ProviderSteps:[]json.RawMessage{rawCall}},
		{Role:"tool",Kind:"tool_call",Tool:"list_files",CallID:"call-1",Output:"file-a.txt",OK:true},
		{Role:"model",Kind:"provider_steps",ProviderSteps:[]json.RawMessage{rawOutput}},
		{Role:"model",Content:"The files are listed."},
	} {
		if err:=stored.Add(message); err!=nil { t.Fatal(err) }
	}
	path:=stored.Path()
	if err:=stored.Close(); err!=nil { t.Fatal(err) }

	activeHistory,err:=session.New("none")
	if err!=nil { t.Fatal(err) }
	a:=&Agent{
		cfg:config.Config{ModelProvider:"ai-studio",SessionCompression:"none"},
		history:activeHistory,
		tools:tools.New(t.TempDir(),"ask"),
	}
	if err:=a.resumeEntry(session.Entry{ID:"saved",Path:path}); err!=nil { t.Fatal(err) }
	defer a.Close()

	if len(a.messages)!=4 { t.Fatalf("restored provider messages = %d, want 4: %#v",len(a.messages),a.messages) }
	if a.messages[0].Role!="user" || a.messages[0].Parts[0].Text!="List the workspace." {
		t.Fatalf("first restored message = %#v",a.messages[0])
	}
	if len(a.messages[1].NativeSteps)!=1 || !strings.Contains(string(a.messages[1].NativeSteps[0]),"call-1") {
		t.Fatalf("restored AI Studio function-call steps = %#v",a.messages[1].NativeSteps)
	}
	response:=a.messages[2].Parts[0].FunctionResponse
	if response==nil || response.ID!="call-1" || response.Name!="list_files" || response.Response["output"]!="file-a.txt" {
		t.Fatalf("restored AI Studio function result = %#v",a.messages[2])
	}
	if len(a.messages[3].NativeSteps)!=1 || string(a.messages[3].NativeSteps[0])!=string(rawOutput) {
		t.Fatalf("restored final AI Studio step = %#v",a.messages[3].NativeSteps)
	}
	for _,message:=range a.chatMessages {
		if message.Kind=="provider_steps" { t.Fatal("provider_steps should not appear in visible transcript") }
	}
}
type scriptedAgentModel struct {
	responses []gemini.Content
	requests  []gemini.Request
	calls     int
}

func (m *scriptedAgentModel) ModelName() string { return "scripted-test-model" }
func (m *scriptedAgentModel) Generate(_ context.Context, request gemini.Request) (gemini.Content, error) {
	if m.calls >= len(m.responses) {
		return gemini.Content{}, fmt.Errorf("unexpected model request %d", m.calls+1)
	}
	m.requests = append(m.requests, request)
	response := m.responses[m.calls]
	m.calls++
	return response, nil
}
func (m *scriptedAgentModel) GenerateStream(ctx context.Context, request gemini.Request, onText func(string)) (gemini.Content, error) {
	response, err := m.Generate(ctx, request)
	if err == nil && onText != nil {
		for _, part := range response.Parts {
			if part.Text != "" {
				onText(part.Text)
			}
		}
	}
	return response, err
}
func (m *scriptedAgentModel) ListModelOptions(context.Context) ([]gemini.ModelOption, error) {
	return nil, nil
}
func (m *scriptedAgentModel) ListModels(context.Context) ([]string, error) { return nil, nil }

func requestHasTool(request gemini.Request, name string) bool {
	for _, group := range request.Tools {
		declarations, ok := group["functionDeclarations"].([]gemini.FunctionDeclaration)
		if !ok {
			continue
		}
		for _, declaration := range declarations {
			if declaration.Name == name {
				return true
			}
		}
	}
	return false
}

func scriptedWriteCall(id, path string) gemini.Content {
	return gemini.Content{Role: "model", Parts: []gemini.Part{{
		FunctionCall: &gemini.FunctionCall{
			ID: id, Name: "write_file",
			Args: map[string]any{"path": path, "content": "allowed"},
		},
	}}}
}

func newScriptedAgent(t *testing.T, approvalMode string, model *scriptedAgentModel) *Agent {
	t.Helper()
	history, err := session.New("none")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = history.Close() })
	return &Agent{
		cfg: config.Config{ModelProvider: "openai-compatible", Model: "scripted-test-model", MaxTurns: 8, MaxToolCalls: 20, SessionCompression: "none"},
		model: model,
		tools: tools.New(t.TempDir(), approvalMode),
		history: history,
		effort: "low",
		activeRole: roles.Definition{Name: "default", AllowedTools: []string{"*"}},
		toolDefs: declarations(),
	}
}

func TestArtifactToolsRejectInvalidOffsetsAndBounds(t *testing.T) {
	model := &scriptedAgentModel{}
	a := newScriptedAgent(t, tools.ModeReadOnly, model)
	artifact, err := a.history.SaveArtifact("test", strings.Repeat("artifact ", 2000))
	if err != nil || artifact == nil {
		t.Fatalf("SaveArtifact = %#v, err=%v", artifact, err)
	}
	tests := []struct {
		name string
		tool string
		args map[string]any
	}{
		{"fractional read offset", "read_tool_output", map[string]any{"artifact_id": artifact.ID, "offset": 1.5}},
		{"negative read offset", "read_tool_output", map[string]any{"artifact_id": artifact.ID, "offset": -1}},
		{"oversized read", "read_tool_output", map[string]any{"artifact_id": artifact.ID, "max_bytes": 8193}},
		{"zero search page", "search_tool_output", map[string]any{"artifact_id": artifact.ID, "query": "artifact", "max_matches": 0}},
		{"oversized search page", "search_tool_output", map[string]any{"artifact_id": artifact.ID, "query": "artifact", "max_matches": 21}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := a.callTool(context.Background(), tt.tool, tt.args)
			if result.OK {
				t.Fatalf("invalid arguments were accepted: %#v", tt.args)
			}
		})
	}
}

func TestOffloadToolOutputArchivesLargeSearchWebAndMCPResults(t *testing.T) {
	model := &scriptedAgentModel{}
	a := newScriptedAgent(t, tools.ModeAuto, model)
	source := strings.Repeat("complete provider output ", 900)
	for _, name := range []string{"search", "web_search", "web_fetch", "mcp__server__lookup"} {
		result := a.offloadToolOutput(name, tools.Result{Output: source, OK: true})
		if !result.OK || result.OutputRef == "" || result.OutputBytes != int64(len(source)) {
			t.Fatalf("%s result metadata = %#v", name, result)
		}
		chunk, err := a.history.ReadArtifact(result.OutputRef, 0, 8192)
		if err != nil {
			t.Fatalf("%s artifact read: %v", name, err)
		}
		if chunk.Content != source[:8192] || chunk.NextOffset != 8192 || chunk.Complete {
			t.Fatalf("%s artifact did not preserve its full source output", name)
		}
		if !strings.Contains(result.Output, result.OutputRef) || !strings.Contains(result.Output, "Output preview") {
			t.Fatalf("%s summary omitted artifact ID or preview: %q", name, result.Output)
		}
	}
}

func TestRunReadFileStoresCompleteLargeSelectedOutputAsArtifact(t *testing.T) {
	model := &scriptedAgentModel{}
	a := newScriptedAgent(t, tools.ModeReadOnly, model)
	lines := make([]string, 1000)
	for i := range lines {
		lines[i] = fmt.Sprintf("%04d:%s", i+1, strings.Repeat("file-content-", 8))
	}
	expected := strings.Join(lines, "\n")
	if err := os.WriteFile(filepath.Join(a.tools.Root, "large-read.txt"), []byte(expected), 0600); err != nil {
		t.Fatal(err)
	}
	result := a.callTool(context.Background(), "read_file", map[string]any{"path": "large-read.txt"})
	if !result.OK || result.OutputRef == "" || result.OutputBytes != int64(len(expected)) {
		t.Fatalf("large read result = %#v", result)
	}
	chunk, err := a.history.ReadArtifact(result.OutputRef, int64(len(expected)-128), 128)
	if err != nil {
		t.Fatal(err)
	}
	if chunk.Content != expected[len(expected)-128:] || !chunk.Complete {
		t.Fatalf("artifact tail = %q; expected %q", chunk.Content, expected[len(expected)-128:])
	}
}

func TestArtifactDeclarationsStillRespectRoleAllowlist(t *testing.T) {
	model := &scriptedAgentModel{}
	a := newScriptedAgent(t, tools.ModeReadOnly, model)
	a.activeRole = roles.Definition{Name: "no-tools"}
	artifact, err := a.history.SaveArtifact("test", strings.Repeat("x", session.OutputArtifactInlineLimit+1))
	if err != nil || artifact == nil {
		t.Fatalf("SaveArtifact = %#v, err=%v", artifact, err)
	}
	for _, group := range a.declarationsAsTools() {
		declarations, _ := group["functionDeclarations"].([]gemini.FunctionDeclaration)
		for _, declaration := range declarations {
			if declaration.Name == "read_tool_output" || declaration.Name == "search_tool_output" {
				t.Fatalf("no-tools role was offered %q", declaration.Name)
			}
		}
	}
}

func TestArtifactToolsAreAdvertisedOnlyWhenSessionHasArtifacts(t *testing.T) {
	model := &scriptedAgentModel{}
	a := newScriptedAgent(t, tools.ModeReadOnly, model)
	hasArtifactTools := func() bool {
		for _, group := range a.declarationsAsTools() {
			declarations, _ := group["functionDeclarations"].([]gemini.FunctionDeclaration)
			for _, declaration := range declarations {
				if declaration.Name == "read_tool_output" || declaration.Name == "search_tool_output" {
					return true
				}
			}
		}
		return false
	}
	if hasArtifactTools() {
		t.Fatal("empty session advertised artifact retrieval tools")
	}
	artifact, err := a.history.SaveArtifact("test", strings.Repeat("output ", 3000))
	if err != nil || artifact == nil {
		t.Fatalf("SaveArtifact = %#v, err=%v", artifact, err)
	}
	if !hasArtifactTools() {
		t.Fatal("session with an output artifact did not advertise retrieval tools")
	}
}

func TestRunShellStoresCompleteOutputAsReadableSessionArtifact(t *testing.T) {
	model := &scriptedAgentModel{}
	a := newScriptedAgent(t, tools.ModeAuto, model)
	const size = 100000
	command := fmt.Sprintf("head -c %d /dev/zero | tr '\\000' x", size)
	result := a.RunShell(context.Background(), command)
	if !result.OK {
		t.Fatalf("RunShell failed: %s", result.Output)
	}
	if result.OutputRef == "" || result.OutputBytes != size || result.OutputSHA256 == "" {
		t.Fatalf("large shell output metadata = %#v", result)
	}
	if !strings.Contains(result.Output, result.OutputRef) || !strings.Contains(result.Output, "Output preview") {
		t.Fatalf("shell result omitted artifact reference or preview: %q", result.Output)
	}

	read := a.callTool(context.Background(), "read_tool_output", map[string]any{
		"artifact_id": result.OutputRef, "offset": float64(95000), "max_bytes": float64(5000),
	})
	if !read.OK || !strings.Contains(read.Output, strings.Repeat("x", 128)) {
		t.Fatalf("read_tool_output failed to recover full tail: %q", read.Output)
	}
	search := a.callTool(context.Background(), "search_tool_output", map[string]any{
		"artifact_id": result.OutputRef, "query": strings.Repeat("x", 32),
	})
	if !search.OK || !strings.Contains(search.Output, "match at byte") {
		t.Fatalf("search_tool_output did not find output: %q", search.Output)
	}

	records, err := session.LoadRecent(a.history.Path(), 0)
	if err != nil {
		t.Fatal(err)
	}
	var stored *session.Message
	for i := range records {
		if records[i].Kind == "tool_call" && records[i].Tool == "shell" {
			stored = &records[i]
		}
	}
	if stored == nil || stored.OutputRef != result.OutputRef ||
		stored.OutputBytes != size || stored.OutputSHA256 != result.OutputSHA256 {
		t.Fatalf("session history did not preserve artifact metadata: %#v", stored)
	}
}

func TestTeamWorkerForcedReadOnlyHidesFileMutationTools(t *testing.T) {
	a := &Agent{
		tools: tools.New(t.TempDir(), tools.ModeAuto),
		activeRole: roles.Definition{Name: "coder", AllowedTools: []string{"*"}},
		toolDefs: declarations(),
		forcedReadOnly: true,
		blockFileMutations: true,
	}
	a.tools.TeamWorkerMode = true

	var names []string
	for _, group := range a.declarationsAsTools() {
		for _, declaration := range group["functionDeclarations"].([]gemini.FunctionDeclaration) {
			names = append(names, declaration.Name)
		}
	}
	if containsAgentTool(names, "write_file") || containsAgentTool(names, "edit_file") {
		t.Fatalf("read-only worker advertised file mutation tools: %v", names)
	}
	if !containsAgentTool(names, "shell") {
		t.Fatalf("read-only tester worker should retain its explicitly role-allowed sandboxed shell: %v", names)
	}
	for _, tool := range a.MCPToolDefinitions() {
		if tool.Name == "write_file" || tool.Name == "edit_file" {
			t.Fatalf("read-only worker exported mutation tool %q over MCP", tool.Name)
		}
	}
}

func containsAgentTool(names []string, target string) bool {
	for _, name := range names {
		if name == target {
			return true
		}
	}
	return false
}

func TestRunContextPreservesOneTimeDenyReplanningAndDistinctArguments(t *testing.T) {
	model := &scriptedAgentModel{responses: []gemini.Content{
		scriptedWriteCall("call-1", "denied-once.txt"),
		scriptedWriteCall("call-2", "allowed-once.txt"),
		{Role: "model", Parts: []gemini.Part{{Text: "Completed the second operation."}}},
	}}
	a := newScriptedAgent(t, tools.ModeAsk, model)
	confirmCalls := 0
	a.tools.Confirm = func(action, target string) tools.ApprovalDecision {
		confirmCalls++
		if confirmCalls == 1 {
			return tools.ApprovalDeny
		}
		return tools.ApprovalOnce
	}

	if err := a.RunContext(context.Background(), "write the requested files", io.Discard); err != nil {
		t.Fatalf("RunContext: %v", err)
	}
	if confirmCalls != 2 {
		t.Fatalf("approval prompts = %d, want 2 (one-time denial must allow replanning)", confirmCalls)
	}
	if !requestHasTool(model.requests[0], "write_file") || !requestHasTool(model.requests[1], "write_file") {
		t.Fatal("one-time denial incorrectly removed write_file from the replanning request")
	}
	if _, err := os.Stat(filepath.Join(a.tools.Root, "denied-once.txt")); !os.IsNotExist(err) {
		t.Fatalf("denied file exists or unexpected stat error: %v", err)
	}
	if data, err := os.ReadFile(filepath.Join(a.tools.Root, "allowed-once.txt")); err != nil || string(data) != "allowed" {
		t.Fatalf("second distinct write was not allowed: content=%q err=%v", data, err)
	}
}

type scriptedLiveAgentModel struct {
	*scriptedAgentModel
	liveCalls    []gemini.FunctionCall
	toolResponses []gemini.FunctionResponse
}

func (m *scriptedLiveAgentModel) GenerateWithTools(
	ctx context.Context,
	request gemini.Request,
	execute gemini.LiveToolExecutor,
	_ func(string),
) (gemini.Content, error) {
	if err := ctx.Err(); err != nil {
		return gemini.Content{}, err
	}
	m.requests = append(m.requests, request)
	m.calls++
	for _, call := range m.liveCalls {
		response, err := execute(ctx, call)
		if err != nil {
			return gemini.Content{}, err
		}
		m.toolResponses = append(m.toolResponses, response)
	}
	return gemini.Content{
		Role:  "model",
		Parts: []gemini.Part{{Text: "I will continue without further tool access."}},
	}, nil
}

func TestRunContextLiveResponseWideDenialReturnsResultsForRemainingCalls(t *testing.T) {
	base := &scriptedAgentModel{}
	model := &scriptedLiveAgentModel{
		scriptedAgentModel: base,
		liveCalls: []gemini.FunctionCall{
			{ID: "live-call-1", Name: "write_file", Args: map[string]any{"path": "live-denied-first.txt", "content": "first"}},
			{ID: "live-call-2", Name: "write_file", Args: map[string]any{"path": "live-denied-second.txt", "content": "second"}},
		},
	}
	a := newScriptedAgent(t, tools.ModeAsk, base)
	a.model = model
	confirmCalls := 0
	a.tools.Confirm = func(action, target string) tools.ApprovalDecision {
		confirmCalls++
		a.DenyCurrentResponse()
		return tools.ApprovalDeny
	}

	if err := a.RunContext(context.Background(), "write two files", io.Discard); err != nil {
		t.Fatalf("Live generation should finish with denial results for the entire response: %v", err)
	}
	if confirmCalls != 1 || model.calls != 1 {
		t.Fatalf("approval calls=%d Live generations=%d, want 1 and 1", confirmCalls, model.calls)
	}
	if len(model.toolResponses) != 2 {
		t.Fatalf("Live tool responses=%d, want 2", len(model.toolResponses))
	}
	for i, response := range model.toolResponses {
		if ok, _ := response.Response["ok"].(bool); ok {
			t.Errorf("Live tool response %d unexpectedly allowed the denied operation: %#v", i+1, response.Response)
		}
		if output, _ := response.Response["output"].(string); !strings.Contains(output, "PERMISSION_DENIED") {
			t.Errorf("Live tool response %d lacks an explicit denial result: %q", i+1, output)
		}
	}
	if !requestHasTool(model.requests[0], "write_file") {
		t.Fatal("initial Live request did not expose the requested write tool")
	}
	for _, path := range []string{"live-denied-first.txt", "live-denied-second.txt"} {
		if _, err := os.Stat(filepath.Join(a.tools.Root, path)); !os.IsNotExist(err) {
			t.Errorf("denied Live operation created %q: %v", path, err)
		}
	}
}

func TestRunContextRemovesToolsAfterResponseWideDenial(t *testing.T) {
	model := &scriptedAgentModel{responses: []gemini.Content{
		scriptedWriteCall("call-1", "denied-response.txt"),
		{Role: "model", Parts: []gemini.Part{{Text: "I will continue without further tool access."}}},
	}}
	a := newScriptedAgent(t, tools.ModeAsk, model)
	confirmCalls := 0
	a.tools.Confirm = func(action, target string) tools.ApprovalDecision {
		confirmCalls++
		a.DenyCurrentResponse()
		return tools.ApprovalDeny
	}

	if err := a.RunContext(context.Background(), "write a file", io.Discard); err != nil {
		t.Fatalf("RunContext should allow a final response after tool denial: %v", err)
	}
	if confirmCalls != 1 || model.calls != 2 {
		t.Fatalf("approval calls=%d model calls=%d, want 1 and 2", confirmCalls, model.calls)
	}
	if !requestHasTool(model.requests[0], "write_file") {
		t.Fatal("initial request did not expose the requested write tool")
	}
	if len(model.requests[1].Tools) != 0 {
		t.Fatalf("response-wide denial left tools available in the next request: %#v", model.requests[1].Tools)
	}
	if _, err := os.Stat(filepath.Join(a.tools.Root, "denied-response.txt")); !os.IsNotExist(err) {
		t.Fatalf("denied file exists or unexpected stat error: %v", err)
	}
}

func TestRunContextWarnsAndContinuesAfterResponseWideDenial(t *testing.T) {
	model := &scriptedAgentModel{responses: []gemini.Content{
		scriptedWriteCall("call-1", "denied-first.txt"),
		scriptedWriteCall("call-2", "denied-second.txt"),
		{Role: "model", Parts: []gemini.Part{{Text: "I will continue without further tool access."}}},
	}}
	a := newScriptedAgent(t, tools.ModeAsk, model)
	confirmCalls := 0
	a.tools.Confirm = func(action, target string) tools.ApprovalDecision {
		confirmCalls++
		a.DenyCurrentResponse()
		return tools.ApprovalDeny
	}

	if err := a.RunContext(context.Background(), "write two files", io.Discard); err != nil {
		t.Fatalf("RunContext stopped instead of returning a denial result: %v", err)
	}
	if confirmCalls != 1 || model.calls != 3 {
		t.Fatalf("approval calls=%d model calls=%d, want 1 and 3 including final answer", confirmCalls, model.calls)
	}
	if len(model.requests[1].Tools) != 0 {
		t.Fatalf("tools remained advertised after response-wide denial: %#v", model.requests[1].Tools)
	}
	var denialWarning string
	for _, content := range model.requests[2].Contents {
		for _, part := range content.Parts {
			if part.FunctionResponse != nil {
				if output, ok := part.FunctionResponse.Response["output"].(string); ok && strings.Contains(output, "PERMISSION_DENIED") {
					denialWarning = output
				}
			}
		}
	}
	if denialWarning == "" {
		t.Fatal("model did not receive a denial result for the later tool call")
	}
	if _, err := os.Stat(filepath.Join(a.tools.Root, "denied-first.txt")); !os.IsNotExist(err) {
		t.Fatalf("first denied file exists or unexpected stat error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(a.tools.Root, "denied-second.txt")); !os.IsNotExist(err) {
		t.Fatalf("second denied file exists or unexpected stat error: %v", err)
	}
}

func TestRunContextWarnsAndContinuesAfterMaxToolCalls(t *testing.T) {
	model := &scriptedAgentModel{}
	a := newScriptedAgent(t, tools.ModeAuto, model)
	a.cfg.MaxToolCalls = 1
	first := filepath.Join(a.tools.Root, "first.txt")
	second := filepath.Join(a.tools.Root, "second.txt")
	makeWrite := func(id, path string) gemini.Content {
		return gemini.Content{Role: "model", Parts: []gemini.Part{{
			FunctionCall: &gemini.FunctionCall{
				ID: id, Name: "write_file", Args: map[string]any{"path": filepath.Base(path), "content": "written"},
			},
		}}}
	}
	model.responses = []gemini.Content{
		makeWrite("call-1", first),
		makeWrite("call-2", second),
		{Role: "model", Parts: []gemini.Part{{Text: "I can finish with the completed work."}}},
	}

	if err := a.RunContext(context.Background(), "write two files", io.Discard); err != nil {
		t.Fatalf("RunContext stopped instead of warning after maxToolCalls: %v", err)
	}
	if model.calls != 3 {
		t.Fatalf("model requests=%d, want 3 including final answer", model.calls)
	}
	if data, err := os.ReadFile(first); err != nil || string(data) != "written" {
		t.Fatalf("first permitted write = %q, err=%v", data, err)
	}
	if _, err := os.Stat(second); !os.IsNotExist(err) {
		t.Fatalf("write beyond maxToolCalls unexpectedly ran: %v", err)
	}
	if len(model.requests[2].Tools) != 0 {
		t.Fatalf("tools remained advertised after maxToolCalls: %#v", model.requests[2].Tools)
	}
	var warning string
	for _, content := range model.requests[2].Contents {
		for _, part := range content.Parts {
			if part.FunctionResponse != nil {
				if output, ok := part.FunctionResponse.Response["output"].(string); ok && strings.Contains(output, "maxToolCalls=1") {
					warning = output
				}
			}
		}
	}
	if warning == "" {
		t.Fatal("model did not receive the maxToolCalls warning")
	}
}

func TestRunContextExecutesDistinctRepeatedToolCalls(t *testing.T) {
	model := &scriptedAgentModel{}
	a := newScriptedAgent(t, tools.ModeAuto, model)
	marker := filepath.Join(a.tools.Root, "cycle-executions.txt")
	makeShellCall := func(id, value string) gemini.Content {
		command := fmt.Sprintf("printf %s >> %q", value, marker)
		return gemini.Content{Role: "model", Parts: []gemini.Part{{
			FunctionCall: &gemini.FunctionCall{
				ID: id, Name: "shell", Args: map[string]any{"command": command},
			},
		}}}
	}
	model.responses = []gemini.Content{
		makeShellCall("call-1", "A"),
		makeShellCall("call-2", "B"),
		makeShellCall("call-3", "A"),
		{Role: "model", Parts: []gemini.Part{{Text: "I have the results and can continue."}}},
	}
	var events []Event
	a.SetEventSink(func(event Event) { events = append(events, event) })

	if err := a.RunContext(context.Background(), "check alternating calls", io.Discard); err != nil {
		t.Fatalf("RunContext failed: %v", err)
	}
	if model.calls != 4 {
		t.Fatalf("model requests = %d, want 4 including the final answer", model.calls)
	}
	if got, err := os.ReadFile(marker); err != nil || string(got) != "ABA" {
		t.Fatalf("shell side effects = %q, err=%v; want all three calls to execute", got, err)
	}
	executions := 0
	for _, event := range events {
		if event.Kind == "tool_end" && event.Tool == "shell" {
			executions++
			if strings.Contains(event.Output, "WARNING: duplicate") {
				t.Fatal("agent suppressed a distinct tool call based only on its arguments")
			}
		}
	}
	if executions != 3 {
		t.Fatalf("shell executions = %d, want 3", executions)
	}
}

func TestRunContextExecutesIdenticalCallsWithDistinctIDs(t *testing.T) {
	model := &scriptedAgentModel{}
	a := newScriptedAgent(t, tools.ModeAuto, model)
	marker := filepath.Join(a.tools.Root, "shell-executions.txt")
	command := fmt.Sprintf("printf x >> %q", marker)
	makeCall := func(id string) gemini.Content {
		return gemini.Content{Role: "model", Parts: []gemini.Part{{
			FunctionCall: &gemini.FunctionCall{
				ID: id, Name: "shell", Args: map[string]any{"command": command},
			},
		}}}
	}
	model.responses = []gemini.Content{
		makeCall("call-1"), makeCall("call-2"),
		{Role: "model", Parts: []gemini.Part{{Text: "Completed both calls."}}},
	}
	var events []Event
	a.SetEventSink(func(event Event) { events = append(events, event) })

	if err := a.RunContext(context.Background(), "execute both requested calls", io.Discard); err != nil {
		t.Fatalf("RunContext failed: %v", err)
	}
	if model.calls != 3 {
		t.Fatalf("model requests = %d, want 3 including the final answer", model.calls)
	}
	if got, err := os.ReadFile(marker); err != nil || string(got) != "xx" {
		t.Fatalf("shell side effects = %q, err=%v; both distinct calls must execute", got, err)
	}
	executions := 0
	for _, event := range events {
		if event.Kind == "tool_end" && event.Tool == "shell" {
			executions++
		}
	}
	if executions != 2 {
		t.Fatalf("shell executions = %d, want 2", executions)
	}
}

func TestRunContextExecutesRepeatedReadFileCallsAndContinues(t *testing.T) {
	model := &scriptedAgentModel{}
	a := newScriptedAgent(t, tools.ModeAuto, model)
	if err := os.WriteFile(filepath.Join(a.tools.Root, "tools.txt"), []byte("the current tool list"), 0600); err != nil {
		t.Fatal(err)
	}
	makeRead := func(id string) gemini.Content {
		return gemini.Content{Role: "model", Parts: []gemini.Part{{
			FunctionCall: &gemini.FunctionCall{
				ID: id, Name: "read_file", Args: map[string]any{"path": "tools.txt"},
			},
		}}}
	}
	model.responses = []gemini.Content{
		makeRead("read-1"), makeRead("read-2"),
		{Role: "model", Parts: []gemini.Part{{Text: "I can continue with the file contents."}}},
	}
	var events []Event
	a.SetEventSink(func(event Event) { events = append(events, event) })

	if err := a.RunContext(context.Background(), "inspect tools.txt", io.Discard); err != nil {
		t.Fatalf("RunContext failed: %v", err)
	}
	if model.calls != 3 {
		t.Fatalf("model requests=%d, want 3 including the final answer", model.calls)
	}
	responses := 0
	for _, content := range model.requests[2].Contents {
		for _, part := range content.Parts {
			if part.FunctionResponse == nil || part.FunctionResponse.Name != "read_file" {
				continue
			}
			responses++
			output, _ := part.FunctionResponse.Response["output"].(string)
			if output != "the current tool list" {
				t.Errorf("read_file call %d output = %q", responses, output)
			}
		}
	}
	if responses != 2 {
		t.Fatalf("read_file function responses=%d, want 2", responses)
	}
	if a.LastResponse() != "I can continue with the file contents." {
		t.Fatalf("last response = %q; model did not finish after repeated reads", a.LastResponse())
	}
	executions := 0
	for _, event := range events {
		if event.Kind == "tool_end" && event.Tool == "read_file" {
			executions++
		}
	}
	if executions != 2 {
		t.Fatalf("read_file executions=%d, want 2", executions)
	}
}

func TestCompatibleModelEffortHandlesFixedAndUnknownCapabilities(t *testing.T) {
	fixed := gemini.ModelOption{ReasoningKnown:true}
	if got := compatibleModelEffort("medium", fixed); got != "default" {
		t.Fatalf("fixed reasoning model effort = %q, want default", got)
	}
	if got := compatibleModelEffort("default", fixed); got != "default" {
		t.Fatalf("explicit default effort = %q, want default", got)
	}
	unknown := gemini.ModelOption{}
	if got := compatibleModelEffort("medium", unknown); got != "medium" {
		t.Fatalf("unknown model effort = %q, want unchanged medium", got)
	}
	restricted := gemini.ModelOption{
		ReasoningKnown:true,
		SupportedEfforts:[]string{"high"},
		DefaultEffort:"high",
	}
	if got := compatibleModelEffort("medium", restricted); got != "high" {
		t.Fatalf("unsupported effort fallback = %q, want high", got)
	}
}


func TestContextCompactionCutNeverSplitsFunctionCallFromResult(t *testing.T) {
	messages := []gemini.Content{
		{Role: "user", Parts: []gemini.Part{{Text: strings.Repeat("g", 500)}}},
		{Role: "model", Parts: []gemini.Part{{Text: strings.Repeat("d", 500)}}},
		{Role: "model", Parts: []gemini.Part{{FunctionCall: &gemini.FunctionCall{
			ID: "call-1", Name: "read_file", Args: map[string]any{"path": "README.md"},
		}}}},
		{Role: "user", Parts: []gemini.Part{{FunctionResponse: &gemini.FunctionResponse{
			ID: "call-1", Name: "read_file", Response: map[string]any{"output": strings.Repeat("r", 500), "ok": true},
		}}}},
	}
	for i := 0; i < 8; i++ {
		messages = append(messages, gemini.Content{Role: "user", Parts: []gemini.Part{{Text: "tail"}}})
	}
	cut := contextCompactionCut(messages)
	if cut == 0 || cut == 3 {
		t.Fatalf("safe boundary = %d, must preserve the matched call/result on the same side", cut)
	}
	if (2 < cut) != (3 < cut) {
		t.Fatalf("boundary %d splits the function call from its result", cut)
	}

	// The result appears before its matching call. No boundary is safe.
	split := append([]gemini.Content(nil), messages...)
	call := split[2]
	split = append(split[:2], split[3:]...)
	split = append(split, call)
	if got := contextCompactionCut(split); got != 0 {
		t.Fatalf("boundary = %d, want none when response has no preceding call", got)
	}
}

func TestContextSnapshotPreservesNativeStepsAndFunctionLinks(t *testing.T) {
	rawStep := json.RawMessage(`{"type":"function_call","call_id":"native-1","name":"read_file"}`)
	call := gemini.FunctionCall{ID: "call-1", Name: "shell", Args: map[string]any{"command": "pwd"}}
	response := gemini.FunctionResponse{ID: "call-1", Name: "shell", Response: map[string]any{"output": "/workspace", "ok": true}}
	original := []gemini.Content{
		{Role: "model", NativeSteps: []json.RawMessage{rawStep}},
		{Role: "model", Parts: []gemini.Part{{FunctionCall: &call}}},
		{Role: "user", Parts: []gemini.Part{{FunctionResponse: &response}}},
	}
	snapshot, err := encodeContextSnapshot(original)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := decodeContextSnapshot(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if len(restored) != 3 || len(restored[0].NativeSteps) != 1 || string(restored[0].NativeSteps[0]) != string(rawStep) {
		t.Fatalf("native steps were not preserved: %#v", restored)
	}
	if restored[1].Parts[0].FunctionCall == nil || restored[1].Parts[0].FunctionCall.ID != "call-1" ||
		restored[2].Parts[0].FunctionResponse == nil || restored[2].Parts[0].FunctionResponse.ID != "call-1" {
		t.Fatalf("function call/result relationship changed: %#v", restored)
	}
}

func TestBuildContextSummaryInputIsBoundedAndKeepsInitialGoal(t *testing.T) {
	messages := []gemini.Content{
		{Role: "user", Parts: []gemini.Part{{Text: "PRIMARY GOAL: preserve the whole project history"}}},
	}
	for i := 0; i < 30; i++ {
		messages = append(messages, gemini.Content{Role: "model", Parts: []gemini.Part{{Text: strings.Repeat(fmt.Sprintf("record-%d ", i), 1200)}}})
	}
	got := buildContextSummaryInput(messages)
	if !strings.Contains(got, "PRIMARY GOAL: preserve the whole project history") {
		t.Fatal("summary source omitted the initial user goal")
	}
	if len(got) > maxCompactionSourceBytes+512 {
		t.Fatalf("summary input grew to %d bytes, want bounded near %d", len(got), maxCompactionSourceBytes)
	}
}
