package roles

import (
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
	for _, name := range []string{"default", "coder", "reviewer", "researcher", "no-tools"} {
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
	got, err := Defaults(), error(nil)
	if err != nil {
		t.Fatal(err)
	}
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
