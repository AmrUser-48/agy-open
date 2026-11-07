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

func TestReadKeyDecodesBracketedMultilinePasteAsOneEvent(t *testing.T) {
	input := "\x1b[200~first line\r\nsecond line\rthird line\nλ\x1b[201~"
	key, err := readKey(bufio.NewReader(strings.NewReader(input)))
	if err != nil {
		t.Fatal(err)
	}
	want := "PASTE:first line\nsecond line\nthird line\nλ"
	if key != want {
		t.Fatalf("key = %q, want %q", key, want)
	}
}

func TestInsertPastedTextPreservesMultilineTextAndCursor(t *testing.T) {
	u := &UI{
		input: []rune("leftRIGHT"),
		cursor: 4,
	}
	u.insertPastedText("one\r\ntwo\rthree\n")

	want := "leftone\ntwo\nthree\nRIGHT"
	if got := string(u.input); got != want {
		t.Fatalf("input = %q, want %q", got, want)
	}
	if u.cursor != len([]rune("leftone\ntwo\nthree\n")) {
		t.Fatalf("cursor = %d, want end of inserted text", u.cursor)
	}
	if u.completionActive {
		t.Fatal("multiline paste must close slash-command completion")
	}
	if len(u.undoStack) != 1 || u.undoStack[0] != "leftRIGHT" {
		t.Fatalf("undo stack = %#v, want original input", u.undoStack)
	}
}

func TestNormalizePastedTextRemovesTerminalControlBytes(t *testing.T) {
	got := normalizePastedText("one\r\ntwo\x00\x1b[31m\x7f")
	want := "one\ntwo[31m"
	if got != want {
		t.Fatalf("normalized paste = %q, want %q", got, want)
	}
}


func TestReadKeyDecodesShiftEnterAcrossTerminalProtocols(t *testing.T) {
	tests := []struct {
		name string
		input string
	}{
		{name: "Kitty keyboard", input: "\x1b[13;2u"},
		{name: "xterm modifyOtherKeys", input: "\x1b[27;2;13~"},
		{name: "ESC carriage return", input: "\x1b\r"},
		{name: "ESC newline", input: "\x1b\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := readKey(bufio.NewReader(strings.NewReader(tt.input)))
			if err != nil {
				t.Fatal(err)
			}
			if got != "SHIFT-ENTER" {
				t.Fatalf("readKey() = %q, want SHIFT-ENTER", got)
			}
		})
	}
}

func TestReadKeyDecodesReadlineControlKeys(t *testing.T) {
	tests := []struct {
		name string
		input string
		want string
	}{
		{name: "Ctrl-N", input: "\x0e", want: "CTRL-N"},
		{name: "Ctrl-P", input: "\x10", want: "CTRL-P"},
		{name: "Ctrl-U", input: "\x15", want: "CTRL-U"},
		{name: "Ctrl-W", input: "\x17", want: "CTRL-W"},
		{name: "Alt-B", input: "\x1bb", want: "ALT-B"},
		{name: "Alt-F", input: "\x1bf", want: "ALT-F"},
		{name: "Alt-D", input: "\x1bd", want: "ALT-D"},
		{name: "Alt-Backspace", input: "\x1b\x7f", want: "ALT-BACKSPACE"},
		{name: "Ctrl-Left", input: "\x1b[1;5D", want: "ALT-B"},
		{name: "Ctrl-Right", input: "\x1b[1;5C", want: "ALT-F"},
		{name: "Home tilde", input: "\x1b[1~", want: "HOME"},
		{name: "End tilde", input: "\x1b[4~", want: "END"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := readKey(bufio.NewReader(strings.NewReader(tt.input)))
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Fatalf("readKey() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestLayoutPromptAccountsForWideAndCombiningRunes(t *testing.T) {
	wide := layoutPrompt([]rune("abcdef😀"), 6, 10)
	if len(wide.lines) != 2 || wide.lines[0] != "abcdef" || wide.lines[1] != "😀" {
		t.Fatalf("wide rune layout = %#v", wide.lines)
	}
	if wide.cursorRow != 1 || wide.cursorCol != 3 {
		t.Fatalf("wide rune cursor = (%d,%d), want (1,3)", wide.cursorRow, wide.cursorCol)
	}

	combining := layoutPrompt([]rune("e\u0301x"), 2, 10)
	if len(combining.lines) != 1 || combining.cursorCol != 4 {
		t.Fatalf("combining mark layout = %#v, cursor col %d", combining.lines, combining.cursorCol)
	}
}
