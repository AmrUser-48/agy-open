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


func TestReadEscapeDecodesModifiedCursorKeys(t *testing.T) {
	tests := []struct {
		sequence string
		want     string
	}{
		{"[1;2A", "UP"},
		{"[1;2B", "DOWN"},
		{"[1;2C", "RIGHT"},
		{"[1;2D", "LEFT"},
		{"[1;3A", "UP"},
		{"[1;4D", "LEFT"},
		{"[1;5A", "UP"},
		{"[1;5B", "DOWN"},
		{"[1;6A", "UP"},
		{"[5~", "PUP"},
		{"[6~", "PDOWN"},
	}
	for _, tt := range tests {
		t.Run(tt.sequence, func(t *testing.T) {
			got, err := readEscape(bufio.NewReader(strings.NewReader(tt.sequence)))
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Fatalf("readEscape(%q) = %q, want %q", tt.sequence, got, tt.want)
			}
		})
	}
}

func TestReadEscapeDecodesShiftedPrintableKeys(t *testing.T) {
	tests := []struct {
		sequence string
		want     string
	}{
		{"[97;2u", "RUNE:A"},
		{"[49;2u", "RUNE:!"},
		{"[27;2;97~", "RUNE:A"},
		{"[13;2u", "SHIFT-ENTER"},
		{"[9;2u", "SHIFT-TAB"},
		{"[57352;2u", "UP"},
		{"[99;6u", "CTRL-C"},
	}
	for _, tt := range tests {
		t.Run(tt.sequence, func(t *testing.T) {
			got, err := readEscape(bufio.NewReader(strings.NewReader(tt.sequence)))
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Fatalf("readEscape(%q) = %q, want %q", tt.sequence, got, tt.want)
			}
		})
	}
}

func TestReadEscapeShiftEnterFallbackSequences(t *testing.T) {
	for _, sequence := range []string{"\r", "\n"} {
		t.Run(sequence, func(t *testing.T) {
			got, err := readEscape(bufio.NewReader(strings.NewReader(sequence)))
			if err != nil {
				t.Fatal(err)
			}
			if got != "SHIFT-ENTER" {
				t.Fatalf("readEscape(%q) = %q, want SHIFT-ENTER", sequence, got)
			}
		})
	}
}
