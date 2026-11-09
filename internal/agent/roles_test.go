package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/AmrUser-48/agy-open/internal/gemini"
	rolecfg "github.com/AmrUser-48/agy-open/internal/roles"
)

func TestDeclarationsRespectActiveRole(t *testing.T) {
	a := &Agent{
		toolDefs: declarations(),
		activeRole: rolecfg.Definition{
			Name:         "reviewer",
			AllowedTools: []string{"list_files", "read_file", "search"},
		},
	}

	var names []string
	for _, group := range a.declarationsAsTools() {
		declarations, ok := group["functionDeclarations"].([]gemini.FunctionDeclaration)
		if !ok {
			t.Fatalf("unexpected declaration type %T", group["functionDeclarations"])
		}
		for _, declaration := range declarations {
			names = append(names, declaration.Name)
		}
	}
	if len(names) != 3 {
		t.Fatalf("exposed %d tools, want 3: %v", len(names), names)
	}
	for _, name := range names {
		if name == "shell" || name == "write_file" || name == "edit_file" {
			t.Fatalf("reviewer unexpectedly received %q", name)
		}
	}
}

func TestCallToolRejectsDisallowedTool(t *testing.T) {
	a := &Agent{
		activeRole: rolecfg.Definition{
			Name:           "reviewer",
			AllowedTools:   []string{"read_file"},
			PermissionMode: rolecfg.PermissionReadOnly,
		},
	}

	result := a.callTool(context.Background(), "write_file", map[string]any{
		"path":    "should-not-exist.txt",
		"content": "blocked",
	})
	if result.OK {
		t.Fatal("disallowed write_file call succeeded")
	}
	if !strings.Contains(result.Output, "PERMISSION_DENIED") {
		t.Fatalf("unexpected rejection: %s", result.Output)
	}
}

func TestReadOnlyRoleRejectsShellEvenWhenListed(t *testing.T) {
	a := &Agent{
		activeRole: rolecfg.Definition{
			Name:           "reviewer",
			AllowedTools:   []string{"shell"},
			PermissionMode: rolecfg.PermissionReadOnly,
		},
	}

	result := a.callTool(context.Background(), "shell", map[string]any{"command": "true"})
	if result.OK {
		t.Fatal("read-only role executed shell")
	}
	if !strings.Contains(result.Output, "read-only") {
		t.Fatalf("unexpected rejection: %s", result.Output)
	}
}
