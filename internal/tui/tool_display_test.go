package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/AmrUser-48/agy-open/internal/agent"
	"github.com/AmrUser-48/agy-open/internal/config"
)

func TestToolActivityUsesReadableNamesAndDuration(t *testing.T) {
	u := &UI{termCols: 80, termRows: 24}
	start := u.toolStartLine(uiEvent{
		tool: "read_file",
		target: "src/main.go",
	})
	if !strings.Contains(start, "Read file") || !strings.Contains(start, "src/main.go") {
		t.Fatalf("tool start line = %q", start)
	}

	kind, done := u.toolEndLine(uiEvent{
		tool: "read_file",
		output: "package main\nfunc main() {}\n",
		ok: true,
		duration: 1250 * time.Millisecond,
	})
	if kind != "success" {
		t.Fatalf("completion kind = %q, want success", kind)
	}
	for _, want := range []string{"✓ Read file", "1.2 s", "2 lines", "B"} {
		if !strings.Contains(done, want) {
			t.Fatalf("completion line %q does not contain %q", done, want)
		}
	}
	if visualLen(done) > 78 {
		t.Fatalf("completion line exceeds terminal width: %d cells", visualLen(done))
	}
}

func TestToolActivitySanitizesUntrustedTargetAndErrors(t *testing.T) {
	u := &UI{termCols: 40, termRows: 24}
	start := u.toolStartLine(uiEvent{
		tool: "shell",
		target: "echo unsafe\n\x1b[31m",
	})
	wantStart := "$ echo unsafe\n␛[31m"
	if start != wantStart {
		t.Fatalf("tool start = %q, want real newlines and escaped terminal bytes: %q", start, wantStart)
	}
	if strings.ContainsAny(start, "\x00\x1b") {
		t.Fatalf("tool start contains unsafe terminal controls: %q", start)
	}

	kind, done := u.toolEndLine(uiEvent{
		tool: "shell",
		output: "failure\x1b[2J\nmore",
		ok: false,
	})
	if kind != "error" || !strings.Contains(done, "failure") {
		t.Fatalf("failure line = (%q, %q)", kind, done)
	}
	if strings.ContainsAny(done, "\x00\x1b") {
		t.Fatalf("tool completion contains unsafe terminal controls: %q", done)
	}
}

func TestExpandedToolOutputCannotInjectTerminalEscapes(t *testing.T) {
	u := &UI{cfg: config.Config{Verbosity: "medium"}}
	got := u.formatToolOutput("first\x1b[2J\nsecond")
	if strings.ContainsRune(got, '\x1b') {
		t.Fatalf("expanded output contains ESC: %q", got)
	}
	if !strings.Contains(got, "␛[2J") {
		t.Fatalf("ESC should be rendered visibly: %q", got)
	}
}


func TestShellToolOutputShowsOneCommandAndRawOutput(t *testing.T) {
	u := &UI{termCols: 80, termRows: 24}
	command := "ls /tmp"
	start := u.toolStartLine(uiEvent{tool: "shell", target: command, command: command})
	if start != "$ "+command {
		t.Fatalf("shell start line = %q, want one command only", start)
	}
	kind, output := u.toolEndLine(uiEvent{
		tool: "shell", command: command,
		output: "first\nsecond\n", ok: true,
		duration: 655700 * time.Millisecond,
	})
	if kind != "tool-output" || output != "first\nsecond" {
		t.Fatalf("shell output = (%q, %q), want dim raw output without labels or summary", kind, output)
	}
	if strings.Contains(output, "output") || strings.Contains(output, command) {
		t.Fatalf("shell output contains duplicate UI text: %q", output)
	}
}

func TestShellToolCommandAndOutputAreSanitized(t *testing.T) {
	command := "make oldconfig && printf '\\x1b[31munsafe'"
	start := (&UI{termCols: 40, termRows: 24}).toolStartLine(uiEvent{tool: "shell", command: command})
	if strings.ContainsRune(start, '\x1b') || !strings.Contains(start, "make oldconfig") || !strings.Contains(start, "printf") {
		t.Fatalf("shell command was truncated or contains terminal escape: %q", start)
	}
	kind, output := (&UI{}).toolEndLine(uiEvent{tool: "shell", command: command, output: "done\x1b[2J\nsecond", ok: true})
	if kind != "tool-output" || strings.ContainsRune(output, '\x1b') || !strings.Contains(output, "␛[2J") {
		t.Fatalf("shell output was not safely sanitized: (%q, %q)", kind, output)
	}
}

func TestShellCommandNewlinesArePreservedWithoutDuplicateLabels(t *testing.T) {
	u := &UI{termCols: 80, termRows: 24}
	command := "make oldconfig\nprintf done\n\x1b[31munsafe"
	safeCommand := "make oldconfig\nprintf done\n␛[31munsafe"
	start := u.toolStartLine(uiEvent{tool: "shell", command: command})
	if start != "$ "+safeCommand {
		t.Fatalf("multiline shell command = %q, want %q", start, "$ "+safeCommand)
	}
	kind, output := u.toolEndLine(uiEvent{tool: "shell", command: command, output: "done\nsecond", ok: true})
	if kind != "tool-output" || output != "done\nsecond" {
		t.Fatalf("multiline shell output = (%q, %q)", kind, output)
	}
}

func TestAgentShellEventsPreserveCommandAndRawOutput(t *testing.T) {
	u := &UI{termCols: 100, termRows: 24}
	command := "make -C /work/linux oldconfig"
	for _, event := range []agent.Event{
		{Kind: "tool_start", Tool: "shell", Target: command},
		{Kind: "tool_end", Tool: "shell", Target: command, Output: "HOSTCC scripts/kconfig/conf.o", OK: true},
		{Kind: "tool_end", Tool: "shell", Command: command, Output: "HOSTCC scripts/kconfig/conf.o", OK: true},
	} {
		got := agentEventToUIEvent(event)
		if got.command != command {
			t.Fatalf("%s command = %q, want exact target %q", event.Kind, got.command, command)
		}
		if event.Kind == "tool_start" && u.toolStartLine(got) != "$ "+command {
			t.Errorf("%s start rendering = %q", event.Kind, u.toolStartLine(got))
		}
		if event.Kind == "tool_end" {
			kind, rendered := u.toolEndLine(got)
			if kind != "tool-output" || rendered != "HOSTCC scripts/kconfig/conf.o" {
				t.Errorf("%s output = (%q, %q)", event.Kind, kind, rendered)
			}
		}
	}
}

func TestTUIRecoversShellCommandFromCorrelatedStartEvent(t *testing.T) {
	u := &UI{termCols: 100, termRows: 24, toolCommands: make(map[string]string)}
	command := "make -C /work/linux oldconfig"
	start := agentEventToUIEvent(agent.Event{
		Kind: "tool_start", Tool: "shell", Target: command, CallID: "shell-42",
	})
	start = u.reconcileToolCommand(start)
	if got := u.toolStartLine(start); !strings.Contains(got, "$ "+command) {
		t.Fatalf("tool start omitted exact command: %q", got)
	}

	end := agentEventToUIEvent(agent.Event{
		Kind: "tool_end", Tool: "shell", Output: "HOSTCC scripts/kconfig/conf.o",
		CallID: "shell-42", OK: true, Duration: 655700 * time.Millisecond,
	})
	end = u.reconcileToolCommand(end)
	_, rendered := u.toolEndLine(end)
	if rendered != "HOSTCC scripts/kconfig/conf.o" {
		t.Fatalf("tool completion output = %q, want raw output only", rendered)
	}
	if _, exists := u.toolCommands["shell-42"]; exists {
		t.Fatal("completed command was not removed from the correlation cache")
	}
}


func TestRunCompletionNoticeShowsFailureAfterPartialStream(t *testing.T) {
	kind, text, show := runCompletionNotice(false, "model quota exhausted\nModel: gemini-3.1-flash-image")
	if !show || kind != "error" {
		t.Fatalf("completion notice = (%q, %q, %v), want visible error", kind, text, show)
	}
	if !strings.Contains(text, "gemini-3.1-flash-image") {
		t.Fatalf("completion notice lost provider detail: %q", text)
	}
}

func TestRunCompletionNoticeDoesNotDuplicateSuccessfulResponse(t *testing.T) {
	if kind, text, show := runCompletionNotice(true, ""); show || kind != "" || text != "" {
		t.Fatalf("successful completion generated a notice: (%q, %q, %v)", kind, text, show)
	}
}

func TestRunCompletionNoticeNormalizesCancellation(t *testing.T) {
	kind, text, show := runCompletionNotice(false, "context canceled")
	if !show || kind != "warning" || text != "Interrupted" {
		t.Fatalf("cancellation notice = (%q, %q, %v)", kind, text, show)
	}
}


func TestTUICommandCorrelationIsScopedToWorker(t *testing.T) {
	u := &UI{termCols: 100, termRows: 24, toolCommands: make(map[string]string)}
	const sharedCallID = "call_1"
	commands := map[string]string{
		"worker-a": "make -C kernel oldconfig",
		"worker-b": "make -C userspace test",
	}
	for workerID, command := range commands {
		start := agentEventToUIEvent(agent.Event{
			Kind: "tool_start", Tool: "shell", WorkerID: workerID,
			CallID: sharedCallID, Target: command,
		})
		u.reconcileToolCommand(start)
	}
	for workerID := range commands {
		end := agentEventToUIEvent(agent.Event{
			Kind: "tool_end", Tool: "shell", WorkerID: workerID,
			CallID: sharedCallID, Output: "finished", OK: true,
		})
		end = u.reconcileToolCommand(end)
		if end.command != commands[workerID] {
			t.Errorf("%s command = %q, want %q", workerID, end.command, commands[workerID])
		}
		kind, rendered := u.toolEndLine(end)
		if kind != "tool-output" || rendered != "finished" {
			t.Errorf("%s rendered output = (%q, %q)", workerID, kind, rendered)
		}
	}
	if len(u.toolCommands) != 0 {
		t.Fatalf("worker command cache retained entries: %#v", u.toolCommands)
	}
}

func TestPermissionCompletionContainsAllSupportedModes(t *testing.T) {
	u := &UI{input: []rune("/permissions ")}
	u.cursor = len(u.input)
	got := u.completionMatches()
	want := []string{
		"/permissions disable",
		"/permissions read-only",
		"/permissions workspace-write",
		"/permissions ask",
		"/permissions autopilot",
	}
	if len(got) != len(want) {
		t.Fatalf("permission completions = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("permission completions = %#v, want %#v", got, want)
		}
	}
}



func TestToolOutputUsesDedicatedDarkGreyStyle(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	t.Setenv("FORCE_COLOR", "1")
	t.Setenv("TERM", "xterm-256color")
	u := &UI{col: newColors(config.Config{})}
	if u.col.toolOutput != "\x1b[38;5;240m" {
		t.Fatalf("tool output foreground = %q, want ANSI 240 dark grey", u.col.toolOutput)
	}
	got := u.paint("tool-output", "raw output")
	if got != "\x1b[38;5;240mraw output\x1b[0m" {
		t.Fatalf("tool output rendering = %q", got)
	}
}
