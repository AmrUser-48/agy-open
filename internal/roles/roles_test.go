package roles

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadSeedsDefaultRoles(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", root)

	got, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"default", "coder", "reviewer", "researcher", "planner", "implementer", "tester", "documentation", "no-tools"} {
		if _, ok := Find(got, name); !ok {
			t.Fatalf("missing seeded role %q", name)
		}
	}
	info, err := os.Stat(filepath.Join(root, "agy-open", "roles"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0077 != 0 {
		t.Fatalf("role directory permissions = %o, want private permissions", info.Mode().Perm())
	}
}

func TestLoadPreservesCustomizedRole(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", root)
	if _, err := Load(); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(root, "agy-open", "roles", "coder.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	custom := string(data)
	custom = replaceOnce(custom, "Software development, repository changes, and targeted tests.", "My customized coder role.")
	if err := os.WriteFile(path, []byte(custom), 0600); err != nil {
		t.Fatal(err)
	}

	got, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	role, ok := Find(got, "coder")
	if !ok || role.Description != "My customized coder role." {
		t.Fatalf("custom role was not preserved: %#v", role)
	}
}

func replaceOnce(value, old, next string) string {
	for i := 0; i+len(old) <= len(value); i++ {
		if value[i:i+len(old)] == old {
			return value[:i] + next + value[i+len(old):]
		}
	}
	return value
}

func TestReadOnlyRoleCannotAllowWriteTools(t *testing.T) {
	role := Definition{
		Name:           "unsafe-reviewer",
		AllowedTools:   []string{"read_file", "write_file"},
		PermissionMode: PermissionReadOnly,
	}
	if err := validate(role); err == nil {
		t.Fatal("expected read-only role with write tool to be rejected")
	}
}

func TestRoleExecutionOverridesAndWorkspaceScopeValidate(t *testing.T) {
	role := Definition{
		Name: "scoped", Description: "Scoped worker", Instructions: "Inspect only assigned files.",
		AllowedTools: []string{"list_files", "read_file", "search", "write_file", "edit_file"},
		PermissionMode: PermissionInherit, Model: "local-coder", Effort: "high",
		WorkspaceSubdir: "internal/agent", MaxConcurrent: 1, MaxTurns: 40,
		MaxToolCalls: 80, MaxRunSeconds: 600, MaxOutputBytes: 8192,
	}
	if err := validate(role); err != nil {
		t.Fatalf("valid role overrides rejected: %v", err)
	}

	tests := []struct {
		name string
		edit func(*Definition)
	}{
		{"absolute workspace", func(r *Definition) { r.WorkspaceSubdir = filepath.Join(string(filepath.Separator), "tmp") }},
		{"parent traversal", func(r *Definition) { r.WorkspaceSubdir = "../outside" }},
		{"invalid effort", func(r *Definition) { r.Effort = "turbo" }},
		{"too many concurrent workers", func(r *Definition) { r.MaxConcurrent = 5 }},
		{"negative turns", func(r *Definition) { r.MaxTurns = -1 }},
		{"excessive tool budget", func(r *Definition) { r.MaxToolCalls = 2001 }},
		{"excessive runtime", func(r *Definition) { r.MaxRunSeconds = 86401 }},
		{"excessive output", func(r *Definition) { r.MaxOutputBytes = 65537 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := role
			test.edit(&candidate)
			if err := validate(candidate); err == nil {
				t.Fatal("invalid role override was accepted")
			}
		})
	}
}

func TestLoadPreservesRoleExecutionOverrides(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", root)
	dir := filepath.Join(root, "agy-open", "roles")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	want := Definition{
		Name: "scoped", Description: "Scoped worker", Instructions: "Stay in the assigned directory.",
		AllowedTools: []string{"list_files", "read_file"}, PermissionMode: PermissionReadOnly,
		Model: "local-coder", Effort: "high", WorkspaceSubdir: "internal/agent",
		MaxConcurrent: 1, MaxTurns: 25, MaxToolCalls: 40, MaxRunSeconds: 300, MaxOutputBytes: 4096,
	}
	data, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "scoped.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	got, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	role, ok := Find(got, "scoped")
	if !ok {
		t.Fatal("custom scoped role was not loaded")
	}
	if role.Model != want.Model || role.Effort != want.Effort || role.WorkspaceSubdir != want.WorkspaceSubdir ||
		role.MaxConcurrent != want.MaxConcurrent || role.MaxTurns != want.MaxTurns ||
		role.MaxToolCalls != want.MaxToolCalls || role.MaxRunSeconds != want.MaxRunSeconds ||
		role.MaxOutputBytes != want.MaxOutputBytes {
		t.Fatalf("role execution overrides changed during load: %#v", role)
	}
}

func TestAllowsTool(t *testing.T) {
	role := Definition{AllowedTools: []string{"read_file"}}
	if !role.AllowsTool("read_file") {
		t.Fatal("expected read_file to be allowed")
	}
	if role.AllowsTool("shell") {
		t.Fatal("unexpected shell permission")
	}
}

func TestNoToolsRoleHasEmptyAllowlist(t *testing.T) {
	got := Defaults()
	role, ok := Find(got, "no-tools")
	if !ok {
		t.Fatal("no-tools role is missing")
	}
	if len(role.AllowedTools) != 0 || role.AllowsTool("read_file") {
		t.Fatalf("no-tools role unexpectedly allows tools: %#v", role.AllowedTools)
	}
	if err := validate(role); err != nil {
		t.Fatalf("empty allowlist should be valid: %v", err)
	}
}


func TestTeamworkRolesHaveBoundedCapabilities(t *testing.T) {
	got := Defaults()
	for _, name := range []string{"planner", "reviewer", "researcher"} {
		role, ok := Find(got, name)
		if !ok {
			t.Fatalf("missing read-only teamwork role %q", name)
		}
		if !role.IsReadOnly() || role.AllowsTool("write_file") || role.AllowsTool("edit_file") || role.AllowsTool("shell") {
			t.Errorf("role %q is not strictly read-only: %#v", name, role)
		}
		if err := validate(role); err != nil {
			t.Errorf("role %q is invalid: %v", name, err)
		}
	}
	for _, name := range []string{"implementer", "tester", "documentation"} {
		role, ok := Find(got, name)
		if !ok {
			t.Fatalf("missing writer teamwork role %q", name)
		}
		if role.IsReadOnly() {
			t.Errorf("writer role %q unexpectedly read-only", name)
		}
		if err := validate(role); err != nil {
			t.Errorf("role %q is invalid: %v", name, err)
		}
	}
}
