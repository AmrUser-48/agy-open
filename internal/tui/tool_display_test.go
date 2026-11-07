package tui

import (
	"strings"
	"testing"
	"time"

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
	if strings.ContainsAny(start, "\x00\x1b\r\n") {
		t.Fatalf("tool start contains terminal controls: %q", start)
	}

	kind, done := u.toolEndLine(uiEvent{
		tool: "shell",
		output: "failure\x1b[2J\nmore",
		ok: false,
	})
	if kind != "error" || !strings.Contains(done, "failure") {
		t.Fatalf("failure line = (%q, %q)", kind, done)
	}
	if strings.ContainsAny(done, "\x00\x1b\r\n") {
		t.Fatalf("tool completion contains terminal controls: %q", done)
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
