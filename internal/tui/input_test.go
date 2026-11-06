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
