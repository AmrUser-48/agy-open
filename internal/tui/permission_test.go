package tui

import "testing"

func TestPermissionModesShareOneCanonicalOrder(t *testing.T) {
	want := []string{"disable", "read-only", "workspace-write", "ask", "autopilot"}
	if len(permissionModeNames) != len(want) {
		t.Fatalf("permission mode count = %d, want %d", len(permissionModeNames), len(want))
	}
	for i, expected := range want {
		if permissionModeNames[i] != expected {
			t.Fatalf("permission mode %d = %q, want %q", i, permissionModeNames[i], expected)
		}
		got, ok := permissionModeAt(i)
		if !ok || got != expected {
			t.Fatalf("permissionModeAt(%d) = %q, %t; want %q, true", i, got, ok, expected)
		}
		if index := permissionModeIndex(expected); index != i {
			t.Fatalf("permissionModeIndex(%q) = %d, want %d", expected, index, i)
		}
	}
	for _, index := range []int{-1, len(want)} {
		if _, ok := permissionModeAt(index); ok {
			t.Fatalf("permissionModeAt(%d) unexpectedly succeeded", index)
		}
	}
	if permissionModeIndex("enable") != -1 {
		t.Fatal("legacy alias 'enable' must not replace the canonical workspace-write menu item")
	}
}
