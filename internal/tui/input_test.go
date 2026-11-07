package tui

import (
	"bufio"
	"strings"
	"testing"
)

func TestReadKeyDecodesCtrlS(t *testing.T) {
	key, err := readKey(bufio.NewReader(strings.NewReader("\x13")))
	if err != nil {
		t.Fatal(err)
	}
	if key != "CTRL-S" {
		t.Fatalf("key = %q, want CTRL-S", key)
	}
}

func TestReadKeyDecodesCtrlJAsNewline(t *testing.T) {
	key, err := readKey(bufio.NewReader(strings.NewReader("\n")))
	if err != nil {
		t.Fatal(err)
	}
	if key != "CTRL-J" {
		t.Fatalf("key = %q, want CTRL-J", key)
	}
}

func TestLayoutPromptWrapsAndTracksCursor(t *testing.T) {
	got := layoutPrompt([]rune("abcdefgh"), 8, 10)
	if len(got.lines) != 2 || got.lines[0] != "abcdefg" || got.lines[1] != "h" {
		t.Fatalf("lines = %#v", got.lines)
	}
	if got.cursorRow != 1 || got.cursorCol != 4 {
		t.Fatalf("cursor = (%d,%d), want (1,4)", got.cursorRow, got.cursorCol)
	}
}

func TestLayoutPromptTracksMultilineCursor(t *testing.T) {
	got := layoutPrompt([]rune("one\ntwo"), 6, 80)
	if len(got.lines) != 2 || got.lines[0] != "one" || got.lines[1] != "two" {
		t.Fatalf("lines = %#v", got.lines)
	}
	if got.cursorRow != 1 || got.cursorCol != 5 {
		t.Fatalf("cursor = (%d,%d), want (1,5)", got.cursorRow, got.cursorCol)
	}
}

func TestReadKeyDecodesEnterCarriageReturn(t *testing.T) {
	key, err := readKey(bufio.NewReader(strings.NewReader("\r")))
	if err != nil {
		t.Fatal(err)
	}
	if key != "ENTER" {
		t.Fatalf("key = %q, want ENTER", key)
	}
}

func TestLayoutPromptCursorAtWrapBoundary(t *testing.T) {
	got := layoutPrompt([]rune("abcdefgh"), 7, 10)
	if got.cursorRow != 1 || got.cursorCol != 3 {
		t.Fatalf("cursor = (%d,%d), want (1,3)", got.cursorRow, got.cursorCol)
	}
}

func TestReadEscapeShiftModifiedCursorKeys(t *testing.T) {
	tests := []struct {
		sequence string
		want     string
	}{
		{"[1;2A", "UP"},
		{"[1;2B", "DOWN"},
		{"[1;2C", "RIGHT"},
		{"[1;2D", "LEFT"},
		{"[1;2H", "HOME"},
		{"[1;2F", "END"},
		{"[1;3A", "UP"},
		{"[1;4D", "LEFT"},
		{"[1;5A", "PUP"},
		{"[1;5B", "PDOWN"},
	}

	for _, test := range tests {
		t.Run(test.sequence, func(t *testing.T) {
			got, err := readEscape(bufio.NewReader(strings.NewReader(test.sequence)))
			if err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Fatalf("readEscape(%q) = %q, want %q", test.sequence, got, test.want)
			}
		})
	}
}
