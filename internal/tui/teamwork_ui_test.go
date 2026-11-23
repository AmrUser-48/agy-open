package tui

import (
	"context"
	"testing"
)

func TestTeamworkCommandOpensInteractiveWorkspace(t *testing.T) {
	u := &UI{lineMode:true, termCols:80, termRows:24}
	u.command("/teamwork", context.Background())
	if u.overlay == nil || u.overlay.kind != "teamwork-home" {
		t.Fatalf("teamwork command opened %#v, want teamwork workspace", u.overlay)
	}
	if len(u.overlay.values) < 3 || u.overlay.values[0] != "run" || u.overlay.values[1] != "preview" {
		t.Fatalf("unexpected teamwork actions: %#v", u.overlay.values)
	}
}
