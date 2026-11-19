package tui

import (
	"testing"

	"github.com/AmrUser-48/agy-open/internal/tools"
)

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


func TestApprovalShortcutAllowsRemainingResponse(t *testing.T) {
	decision, response, selected := approvalDecisionForKey("RUNE:a", false, false)
	if !selected || !response || decision != tools.ApprovalOnce {
		t.Fatalf("a shortcut = (%v, %v, %v), want (once, response-wide, selected)", decision, response, selected)
	}
	decision, response, selected = approvalDecisionForKey("RUNE:A", false, false)
	if !selected || !response || decision != tools.ApprovalOnce {
		t.Fatalf("uppercase A shortcut = (%v, %v, %v), want (once, response-wide, selected)", decision, response, selected)
	}
}

func TestLifecycleApprovalDoesNotAcceptResponseWideShortcut(t *testing.T) {
	if decision, response, selected := approvalDecisionForKey("RUNE:a", true, false); selected || response || decision != tools.ApprovalDeny {
		t.Fatalf("lifecycle a shortcut = (%v, %v, %v), want (deny, not response-wide, not selected)", decision, response, selected)
	}
	if decision, response, selected := approvalDecisionForKey("RUNE:1", true, false); !selected || response || decision != tools.ApprovalOnce {
		t.Fatalf("lifecycle once shortcut = (%v, %v, %v), want (once, not response-wide, selected)", decision, response, selected)
	}
}
