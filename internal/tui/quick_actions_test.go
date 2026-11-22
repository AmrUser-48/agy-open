package tui

import (
	"context"
	"testing"
)

func TestAltPOpensQuickActionsPalette(t *testing.T) {
	u := &UI{lineMode:true, termCols:80, termRows:24}
	u.handleKey(context.Background(), "ALT-P")
	if u.overlay == nil || u.overlay.kind != "quick-actions" {
		t.Fatalf("Alt+P opened %#v, want quick actions", u.overlay)
	}
	if len(u.overlay.values) < 3 || u.overlay.values[0] != "skills" || u.overlay.values[1] != "teamwork" {
		t.Fatalf("unexpected quick actions: %#v", u.overlay.values)
	}
}
