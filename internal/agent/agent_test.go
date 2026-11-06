func TestNormalizeApprovalMode(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{input: "read-only", want: "deny"},
		{input: "strict", want: "deny"},
		{input: "workspace-write", want: "ask"},
		{input: "ask-for-approve", want: "ask"},
		{input: "ask", want: "ask"},
		{input: "unrestricted", want: "auto"},
		{input: "always-proceed", want: "auto"},
		{input: "unknown", want: "ask"},
	}
	for _, tt := range tests {
		if got := normalizeApprovalMode(tt.input); got != tt.want {
			t.Errorf("normalizeApprovalMode(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

package agent

import (
	"testing"

	"github.com/AmrUser-48/agy-open/internal/gemini"
)

func TestDeclarations(t *testing.T) {
	groups := declarations()
	if len(groups) != 6 {
		t.Fatalf("expected 6 tool groups, got %d", len(groups))
	}

	want := map[string]bool{
		"list_files": true,
		"read_file":  true,
		"search":     true,
		"write_file": true,
		"edit_file":  true,
		"shell":      true,
	}
	got := map[string]bool{}
	for _, group := range groups {
		decls, ok := group["functionDeclarations"].([]gemini.FunctionDeclaration)
		if !ok || len(decls) != 1 {
			t.Fatalf("invalid function declaration group: %#v", group)
		}
		got[decls[0].Name] = true
	}
	for name := range want {
		if !got[name] {
			t.Fatalf("missing tool %q", name)
		}
	}
}
