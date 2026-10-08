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
