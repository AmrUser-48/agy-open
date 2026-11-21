package tui

import (
	"strings"
	"testing"

	"github.com/AmrUser-48/agy-open/internal/config"
)

func TestDisplayPromptKeepsTerminalOutputSafe(t *testing.T) {
	got := displayPrompt("hello\r\nworld")
	if got != "hello↵world" {
		t.Fatalf("displayPrompt = %q", got)
	}
}

func TestSpinnerFramesRotate(t *testing.T) {
	a := spinnerFrame(0)
	b := spinnerFrame(1)
	if a == b || a == "" || b == "" {
		t.Fatalf("spinner frames did not rotate: %q %q", a, b)
	}
}

func TestLineOverlayDiffTouchesOnlyChangedMenuRows(t *testing.T) {
	previous := []string{
		"Model picker",
		"› Gemini Flash",
		"  Gemini Pro",
		"  Claude Sonnet",
		"↑/↓ move · Enter select",
	}
	next := []string{
		"Model picker",
		"  Gemini Flash",
		"› Gemini Pro",
		"  Claude Sonnet",
		"↑/↓ move · Enter select",
	}

	got := lineOverlayDiff(previous, next, len(previous))
	if count := strings.Count(got, "\x1b[s"); count != 2 {
		t.Fatalf("selection move patched %d rows, want only old/new selection rows: %q", count, got)
	}
	for _, untouched := range []string{"Model picker", "Claude Sonnet", "Enter select"} {
		if strings.Contains(got, untouched) {
			t.Errorf("unchanged row %q was redrawn: %q", untouched, got)
		}
	}
	for _, changed := range []string{"Gemini Flash", "Gemini Pro"} {
		if !strings.Contains(got, changed) {
			t.Errorf("changed selection row %q missing from patch: %q", changed, got)
		}
	}
	if strings.Contains(got, "\n") || strings.Contains(got, "\x1b[2J") {
		t.Fatalf("incremental update emitted line breaks or full-screen clear: %q", got)
	}
}

func TestLineOverlayDiffHandlesVisibleWindowShiftAndResize(t *testing.T) {
	previous := []string{"Title", "› row 1", "  row 2", "  row 3", "Footer"}
	next := []string{"Title", "  row 2", "  row 3", "› row 4", "  row 5", "Footer"}

	got := lineOverlayDiff(previous, next, len(previous))
	if !strings.HasPrefix(got, "\x1b[1B") {
		t.Fatalf("growing menu did not preserve cursor anchor: %q", got)
	}
	if !strings.Contains(got, "row 4") || !strings.Contains(got, "row 5") {
		t.Fatalf("shifted rows missing from incremental update: %q", got)
	}
	if strings.Contains(got, "\x1b[2J") {
		t.Fatalf("menu resize cleared the whole screen: %q", got)
	}
}

func TestLineModeDoesNotPopulateRenderBuffer(t *testing.T) {
	u := &UI{lineMode: true}
	u.appendStreamText("hello ")
	u.appendStreamText("world")
	if len(u.lines) != 0 {
		t.Fatalf("line mode should not maintain a rendered output buffer: %#v", u.lines)
	}
	if u.streamLastByte != 'd' {
		t.Fatalf("stream last byte = %q", u.streamLastByte)
	}
}

func TestFullscreenOverlayRendersMenuFooterOnlyOnce(t *testing.T) {
	footer := "j/k move · Enter select · Esc back"
	u := &UI{
		overlay: &overlay{
			title: "Roles",
			items: []string{"coder", "researcher"},
			values: []string{"coder", "researcher"},
			kind: "agents",
			footer: footer,
		},
		col: colors{dim: "[dim]", bold: "[bold]", invert: "[invert]", reset: "[reset]"},
		termRows: 24,
	}
	frame := u.overlayFrame(80)
	if got := strings.Count(frame, footer); got != 1 {
		t.Fatalf("overlay footer appears %d times, want once: %q", got, frame)
	}
}

func TestLineModeAlwaysKeepsPrimaryTerminalScreen(t *testing.T) {
	tests := []struct {
		name string
		lineMode bool
		mode string
		term string
		want bool
	}{
		{name: "line mode ignores always", lineMode: true, mode: "always", term: "xterm-256color", want: false},
		{name: "line mode ignores auto", lineMode: true, mode: "auto", term: "xterm-256color", want: false},
		{name: "fullscreen honors always", lineMode: false, mode: "always", term: "xterm-256color", want: true},
		{name: "fullscreen honors auto", lineMode: false, mode: "auto", term: "xterm-256color", want: true},
		{name: "dumb terminal stays primary", lineMode: false, mode: "auto", term: "dumb", want: false},
		{name: "never stays primary", lineMode: false, mode: "never", term: "xterm-256color", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := wantsAltScreen(tt.lineMode, tt.mode, tt.term); got != tt.want {
				t.Fatalf("wantsAltScreen(%t, %q, %q) = %t, want %t", tt.lineMode, tt.mode, tt.term, got, tt.want)
			}
		})
	}
}

func TestTUIColorsApplyToPromptCommandsAndLiveResponses(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	t.Setenv("FORCE_COLOR", "")
	t.Setenv("TERM", "xterm-256color")

	palette := newColors(config.Config{ColorScheme: "terminal"})
	if palette.green == "" || palette.cyan == "" || palette.blue == "" {
		t.Fatalf("terminal palette is missing ANSI colors: %#v", palette)
	}
	ui := &UI{col: palette}
	if got := ui.paint("agent-stream", "response"); !strings.Contains(got, palette.green) {
		t.Fatalf("live response is not colored: %q", got)
	}
	if got := ui.paint("info", "agy-open"); !strings.Contains(got, palette.blue) {
		t.Fatalf("info line is not colored: %q", got)
	}
	if got := ui.paintPromptLine("/help"); !strings.Contains(got, palette.green) {
		t.Fatalf("slash command is not highlighted: %q", got)
	}
}

func TestForceColorOverridesNoColorAndDumbTerm(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	t.Setenv("TERM", "dumb")
	t.Setenv("FORCE_COLOR", "1")
	if !colorOutputEnabled() {
		t.Fatal("FORCE_COLOR should override NO_COLOR and TERM=dumb")
	}
	if palette := newColors(config.Config{}); palette.green == "" {
		t.Fatal("forced color palette is empty")
	}
}

func TestNoColorStillDisablesColorsWithoutForce(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	t.Setenv("FORCE_COLOR", "")
	t.Setenv("TERM", "xterm-256color")
	if colorOutputEnabled() {
		t.Fatal("NO_COLOR should disable ANSI output unless FORCE_COLOR is set")
	}
	if palette := newColors(config.Config{}); palette.green != "" {
		t.Fatalf("palette should be empty when color is disabled: %#v", palette)
	}
}

func TestTokyoNightUsesConfiguredPalette(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	t.Setenv("FORCE_COLOR", "")
	t.Setenv("TERM", "xterm-256color")
	palette := newColors(config.Config{ColorScheme: "tokyo-night"})
	if palette.cyan != "\x1b[38;5;117m" {
		t.Fatalf("tokyo-night cyan = %q", palette.cyan)
	}
}


func TestAuthLabelShowsEnvironmentRefreshToken(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("AGY_OAUTH_REFRESH_TOKEN", "runner-token")
	t.Setenv("GEMINI_API_KEY", "")
	t.Setenv("GOOGLE_API_KEY", "")
	if got := authLabel("gemini"); got != "Google account (refresh token)" {
		t.Fatalf("authLabel = %q, want Google account (refresh token)", got)
	}
}
