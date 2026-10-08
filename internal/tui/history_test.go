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
