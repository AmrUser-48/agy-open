package tui

import "testing"

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
