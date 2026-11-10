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
	startLines := strings.SplitN(start, "\n  $ ", 2)
	if len(startLines) != 2 || strings.ContainsAny(startLines[1], "\x00\x1b\r\n") {
		t.Fatalf("tool start has an invalid command line: %q", start)
	}
	if !strings.Contains(startLines[1], "echo unsafe↵␛[31m") {
		t.Fatalf("tool start did not render sanitized command: %q", start)
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


func TestShellCommandNewlinesAreVisibleWithoutBreakingToolLayout(t *testing.T) {
	u := &UI{termCols: 80, termRows: 24}
	command := "make oldconfig\nprintf done\n\x1b[31munsafe"
	start := u.toolStartLine(uiEvent{tool: "shell", command: command})
	startLines := strings.SplitN(start, "\n  $ ", 2)
	if len(startLines) != 2 || strings.ContainsAny(startLines[1], "\x00\x1b\r\n") {
		t.Fatalf("multiline shell command injected controls into its command line: %q", start)
	}
	if !strings.Contains(startLines[1], "make oldconfig↵printf done↵␛[31munsafe") {
		t.Fatalf("multiline shell command was not preserved visibly: %q", start)
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
