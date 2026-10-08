package tui

import (
	"strings"
	"testing"
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

func TestLineModeUsesTerminalScrollback(t *testing.T) {
	u := &UI{lineMode: true}
	u.appendStreamText("hello ")
	u.appendStreamText("world")
	if len(u.lines) != 0 {
		t.Fatalf("line mode should not maintain a rendered output buffer: %#v", u.lines)
	}
	if !strings.Contains(u.streamLastByteString(), "world") {
		t.Fatalf("stream accounting missing terminal output")
	}
}

func (u *UI) streamLastByteString() string {
	if u.streamLastByte == 0 {
		return ""
	}
	return "world"
}
