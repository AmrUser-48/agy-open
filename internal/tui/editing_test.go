package tui

import "testing"

func TestCurrentLineBounds(t *testing.T) {
	input := []rune("first line\nsecond")
	tests := []struct {
		cursor, wantStart, wantEnd int
	}{
		{cursor: 3, wantStart: 0, wantEnd: 10},
		{cursor: 11, wantStart: 11, wantEnd: 17},
		{cursor: len(input), wantStart: 11, wantEnd: 17},
	}
	for _, tt := range tests {
		start, end := currentLineBounds(input, tt.cursor)
		if start != tt.wantStart || end != tt.wantEnd {
			t.Fatalf("currentLineBounds(%d) = (%d,%d), want (%d,%d)",
				tt.cursor, start, end, tt.wantStart, tt.wantEnd)
		}
	}
}

func TestReadlineKillAndYankStayOnCurrentLine(t *testing.T) {
	t.Run("Ctrl-U and yank", func(t *testing.T) {
		u := &UI{input: []rune("first second\nthird"), cursor: 6}
		u.killToLineStart()
		if got := string(u.input); got != "second\nthird" {
			t.Fatalf("after Ctrl-U = %q", got)
		}
		if u.killBuffer != "first " {
			t.Fatalf("kill buffer = %q, want %q", u.killBuffer, "first ")
		}
		u.yank()
		if got := string(u.input); got != "first second\nthird" {
			t.Fatalf("after yank = %q", got)
		}
	})

	t.Run("Ctrl-K preserves newline", func(t *testing.T) {
		u := &UI{input: []rune("first second\nthird"), cursor: 6}
		u.killToLineEnd()
		if got := string(u.input); got != "first \nthird" {
			t.Fatalf("after Ctrl-K = %q", got)
		}
		if u.killBuffer != "second" {
			t.Fatalf("kill buffer = %q, want second", u.killBuffer)
		}
	})

	t.Run("Ctrl-W deletes previous word", func(t *testing.T) {
		u := &UI{input: []rune("first second"), cursor: 12}
		u.killWordBackward()
		if got := string(u.input); got != "first " {
			t.Fatalf("after Ctrl-W = %q", got)
		}
		if u.cursor != 6 || u.killBuffer != "second" {
			t.Fatalf("cursor=%d killBuffer=%q", u.cursor, u.killBuffer)
		}
	})

	t.Run("Alt-D deletes next word", func(t *testing.T) {
		u := &UI{input: []rune("first second"), cursor: 0}
		u.killWordForward()
		if got := string(u.input); got != " second" {
			t.Fatalf("after Alt-D = %q", got)
		}
		if u.killBuffer != "first" {
			t.Fatalf("kill buffer = %q, want first", u.killBuffer)
		}
	})
}

func TestWordMovementIsLineLocal(t *testing.T) {
	u := &UI{input: []rune("one two\nthree four"), cursor: 8}
	u.moveWordBackward()
	if u.cursor != 4 {
		t.Fatalf("Alt-B cursor = %d, want 4", u.cursor)
	}
	u.moveWordForward()
	if u.cursor != 7 {
		t.Fatalf("Alt-F cursor = %d, want 7", u.cursor)
	}
}

func TestVerticalCursorMovementKeepsColumnWhenPossible(t *testing.T) {
	u := &UI{input: []rune("one\ntwenty"), cursor: 9}
	if !u.moveCursorVertical(-1) {
		t.Fatal("expected move to previous line")
	}
	if u.cursor != 3 {
		t.Fatalf("up cursor = %d, want 3 (clamped to short line)", u.cursor)
	}
	if !u.moveCursorVertical(1) {
		t.Fatal("expected move to next line")
	}
	if u.cursor != 9 {
		t.Fatalf("down cursor = %d, want 9 (restored preferred column)", u.cursor)
	}
	if u.moveCursorVertical(1) {
		t.Fatal("unexpected movement below final line")
	}
}

func TestInsertNewlineAtCursorIsUndoable(t *testing.T) {
	u := &UI{input: []rune("leftRIGHT"), cursor: 4}
	u.insertNewline()
	if got := string(u.input); got != "left\nRIGHT" {
		t.Fatalf("input = %q, want %q", got, "left\nRIGHT")
	}
	if u.cursor != 5 {
		t.Fatalf("cursor = %d, want 5", u.cursor)
	}
	if len(u.undoStack) != 1 || u.undoStack[0] != "leftRIGHT" {
		t.Fatalf("undo stack = %#v", u.undoStack)
	}
	u.undo()
	if got := string(u.input); got != "leftRIGHT" {
		t.Fatalf("after undo = %q", got)
	}
}


func TestVerticalCursorMovementAcrossSoftWrappedRows(t *testing.T) {
	u := &UI{input: []rune("abcdefghij"), cursor: 8, termCols: 10, termRows: 24}
	if !u.moveCursorVertical(-1) {
		t.Fatal("expected move to previous visual row")
	}
	if u.cursor != 1 {
		t.Fatalf("up cursor = %d, want 1", u.cursor)
	}
	if !u.moveCursorVertical(1) {
		t.Fatal("expected move to next visual row")
	}
	if u.cursor != 8 {
		t.Fatalf("down cursor = %d, want 8", u.cursor)
	}
}
