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
	wantStart := "⏺ Run shell\n  $ echo unsafe\n␛[31m"
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


func TestShellToolActivityShowsExactCommandAtStartAndCompletion(t *testing.T) {
	u := &UI{termCols: 80, termRows: 24}
	command := "make -j1 oldconfig"
	start := u.toolStartLine(uiEvent{tool: "shell", target: command, command: command})
	if !strings.Contains(start, "$ "+command) {
		t.Fatalf("tool start line omitted command: %q", start)
	}
	kind, done := u.toolEndLine(uiEvent{
		tool: "shell",
		target: command,
		command: command,
		output: "HOSTCC scripts/kconfig/conf.o\nHOSTCC scripts/kconfig/confdata.o",
		ok: true,
		duration: 655700 * time.Millisecond,
	})
	if kind != "success" {
		t.Fatalf("completion kind = %q, want success", kind)
	}
	if !strings.Contains(done, "$ "+command) || !strings.Contains(done, "655.7 s") || !strings.Contains(done, "HOSTCC") {
		t.Fatalf("tool completion omitted command/result: %q", done)
	}
}

func TestShellToolCommandIsSanitizedWithoutTruncation(t *testing.T) {
	u := &UI{termCols: 40, termRows: 24}
	command := "make oldconfig && printf '\\x1b[31munsafe'"
	start := u.toolStartLine(uiEvent{tool: "shell", command: command})
	if strings.ContainsRune(start, '\x1b') {
		t.Fatalf("tool start includes terminal escape: %q", start)
	}
	if !strings.Contains(start, "make oldconfig") || !strings.Contains(start, "printf") {
		t.Fatalf("tool start truncated the exact command: %q", start)
	}
	_, done := u.toolEndLine(uiEvent{tool: "shell", command: command, ok: true})
	if strings.ContainsRune(done, '\x1b') || !strings.Contains(done, "make oldconfig") {
		t.Fatalf("tool completion lost/sent unsafe command: %q", done)
	}
}


func TestShellCommandNewlinesArePreservedInTUI(t *testing.T) {
	u := &UI{termCols: 80, termRows: 24}
	command := "make oldconfig\nprintf done\n\x1b[31munsafe"
	safeCommand := "make oldconfig\nprintf done\n␛[31munsafe"

	start := u.toolStartLine(uiEvent{tool: "shell", command: command})
	wantStart := "⏺ Run shell\n  $ " + safeCommand
	if start != wantStart {
		t.Fatalf("multiline command start = %q, want %q", start, wantStart)
	}
	if strings.ContainsAny(start, "\x00\x1b") {
		t.Fatalf("multiline command start contains unsafe terminal controls: %q", start)
	}

	kind, done := u.toolEndLine(uiEvent{
		tool: "shell", command: command, output: "done\nsecond", ok: true,
	})
	if kind != "success" {
		t.Fatalf("completion kind = %q, want success", kind)
	}
	wantEndPrefix := "$ " + safeCommand + "\n  ✓ Run shell"
	if !strings.HasPrefix(done, wantEndPrefix) {
		t.Fatalf("multiline command completion = %q, want prefix %q", done, wantEndPrefix)
	}
	if strings.ContainsAny(done, "\x00\x1b") {
		t.Fatalf("multiline command completion contains unsafe terminal controls: %q", done)
	}

	wantOutputLabel := "  ↳ output · $ " + safeCommand
	if got := toolOutputLabel(uiEvent{tool: "shell", command: command}); got != wantOutputLabel {
		t.Fatalf("multiline output label = %q, want %q", got, wantOutputLabel)
	}
}


func TestAgentShellEventsAlwaysPreserveTheExactCommandInTUI(t *testing.T) {
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
		if rendered := u.toolStartLine(got); !strings.Contains(rendered, "$ "+command) {
			t.Errorf("%s start rendering omitted command: %q", event.Kind, rendered)
		}
		if _, rendered := u.toolEndLine(got); !strings.Contains(rendered, "$ "+command) {
			t.Errorf("%s completion rendering omitted command: %q", event.Kind, rendered)
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
	if !strings.Contains(rendered, "$ "+command) {
		t.Fatalf("tool completion did not recover the exact command: %q", rendered)
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
	for workerID, command := range commands {
		end := agentEventToUIEvent(agent.Event{
			Kind: "tool_end", Tool: "shell", WorkerID: workerID,
			CallID: sharedCallID, Output: "finished", OK: true,
		})
		end = u.reconcileToolCommand(end)
		if end.command != command {
			t.Errorf("%s command = %q, want %q", workerID, end.command, command)
		}
		_, rendered := u.toolEndLine(end)
		if !strings.Contains(rendered, "$ "+command) {
			t.Errorf("%s completion omitted its command: %q", workerID, rendered)
		}
	}
	if len(u.toolCommands) != 0 {
		t.Fatalf("worker command cache retained entries: %#v", u.toolCommands)
	}
}


func TestShellOutputLabelKeepsExactCommandAttachedToOutput(t *testing.T) {
	command := "make -C /work/linux oldconfig"
	got := toolOutputLabel(uiEvent{tool: "shell", command: command})
	if got != "  ↳ output · $ "+command {
		t.Fatalf("shell output label = %q, want exact command attached", got)
	}
	if generic := toolOutputLabel(uiEvent{tool: "read_file", target: "kernel/.config"}); generic != "  ↳ output" {
		t.Fatalf("non-shell output label = %q, want generic label", generic)
	}
}


func TestPermissionCompletionContainsAllSupportedModes(t *testing.T) {
	u := &UI{input: []rune("/permissions ")}
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


