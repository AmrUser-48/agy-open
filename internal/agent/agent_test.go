package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/AmrUser-48/agy-open/internal/config"
	"github.com/AmrUser-48/agy-open/internal/gemini"
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
	want := map[string]bool{"list_files": true, "read_file": true, "search": true, "web_search": true, "web_fetch": true}
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
	aiClient, err := newModelClient("ai-studio", "", "", nil, nil)
	if err != nil { t.Fatal(err) }
	ai, ok := aiClient.(*gemini.AIStudioClient)
	if !ok { t.Fatalf("AI Studio provider client = %T", aiClient) }
	if ai.ModelName() != "gemini-4-hydrogen" { t.Fatalf("AI Studio default model = %q", ai.ModelName()) }

	liveClient, err := newModelClient("gemini-live", "", "", nil, nil)
	if err != nil { t.Fatal(err) }
	live, ok := liveClient.(*gemini.LiveClient)
	if !ok { t.Fatalf("Gemini Live provider client = %T", liveClient) }
	if live.ModelName() != "gemini-3.8-live" { t.Fatalf("Gemini Live default model = %q", live.ModelName()) }
}
