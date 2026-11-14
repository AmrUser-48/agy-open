package tui

import "testing"

func TestPromptHistoryNavigation(t *testing.T) {
	h := newPromptHistory([]string{"one", "two", "three"})

	got, ok := h.up("draft")
	if !ok || got != "three" {
		t.Fatalf("first up = %q, %v", got, ok)
	}
	got, ok = h.up(got)
	if !ok || got != "two" {
		t.Fatalf("second up = %q, %v", got, ok)
	}
	got, ok = h.up(got)
	if !ok || got != "one" {
		t.Fatalf("third up = %q, %v", got, ok)
	}

	got, ok = h.down(got)
	if !ok || got != "two" {
		t.Fatalf("first down = %q, %v", got, ok)
	}
	got, ok = h.down(got)
	if !ok || got != "three" {
		t.Fatalf("second down = %q, %v", got, ok)
	}
	got, ok = h.down(got)
	if !ok || got != "draft" {
		t.Fatalf("draft restore = %q, %v", got, ok)
	}
}

func TestPromptHistoryEditingDetachesCursor(t *testing.T) {
	h := newPromptHistory([]string{"one", "two"})
	got, _ := h.up("draft")
	if got != "two" {
		t.Fatal("expected two")
	}
	h.edit()
	if h.index != -1 {
		t.Fatalf("index = %d, want -1", h.index)
	}
}


func TestPromptNavigationSeparatesMultilineCursorFromHistory(t *testing.T) {
	draft := "first line\nsecond line"
	history := newPromptHistory([]string{"older command", "newer command"})
	u := &UI{
		input:    []rune(draft),
		cursor:   len([]rune("first line\nsec")),
		termCols: 80,
		termRows: 24,
		history:  history,
	}

	u.handlePromptNavigation("UP")
	if got, want := u.cursor, len([]rune("fir")); got != want {
		t.Fatalf("Up cursor = %d, want %d (same column on previous line)", got, want)
	}
	if got := string(u.input); got != draft {
		t.Fatalf("Up changed the input: %q, want %q", got, draft)
	}
	if history.index != -1 {
		t.Fatalf("Up changed history index to %d; multiline cursor movement must not navigate history", history.index)
	}

	u.handlePromptNavigation("DOWN")
	if got, want := u.cursor, len([]rune("first line\nsec")); got != want {
		t.Fatalf("Down cursor = %d, want %d (same column on next line)", got, want)
	}

	// Ctrl-P is a readline history shortcut even when a vertical cursor move
	// would be possible and a completion menu is active.
	u.completionActive = true
	u.handlePromptNavigation("CTRL-P")
	if got, want := string(u.input), "newer command"; got != want {
		t.Fatalf("Ctrl-P input = %q, want previous history entry %q", got, want)
	}
	if history.index != 1 {
		t.Fatalf("Ctrl-P history index = %d, want 1", history.index)
	}
	if u.completionActive {
		t.Fatal("Ctrl-P history navigation must close completion")
	}

	u.handlePromptNavigation("CTRL-N")
	if got := string(u.input); got != draft {
		t.Fatalf("Ctrl-N input = %q, want restored multiline draft %q", got, draft)
	}
	if history.index != -1 {
		t.Fatalf("Ctrl-N history index = %d, want -1 after restoring draft", history.index)
	}
	if u.cursor != len([]rune(draft)) {
		t.Fatalf("restored draft cursor = %d, want %d", u.cursor, len([]rune(draft)))
	}
}
