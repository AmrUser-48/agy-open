package agent

import (
	"testing"

	"github.com/AmrUser-48/agy-open/internal/gemini"
	"github.com/AmrUser-48/agy-open/internal/tools"
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

func TestDisabledModeDoesNotAdvertiseTools(t *testing.T) {
	a := &Agent{
		tools:    tools.New(t.TempDir(), "disable"),
		toolDefs: declarations(),
	}
	if got := a.declarationsAsTools(); len(got) != 0 {
		t.Fatalf("disabled mode advertised %d tool groups", len(got))
	}
}

func TestWorkspaceAndAskModesAdvertiseTools(t *testing.T) {
	for _, mode := range []string{"enable", "ask"} {
		a := &Agent{
			tools:    tools.New(t.TempDir(), mode),
			toolDefs: declarations(),
		}
		if got := a.declarationsAsTools(); len(got) != 6 {
			t.Fatalf("%s mode advertised %d tool groups, want 6", mode, len(got))
		}
	}
}
