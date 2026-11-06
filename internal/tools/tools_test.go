package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWorkspacePathEscapeBlocked(t *testing.T) {
	root := t.TempDir()
	w := New(root, ModeWorkspace)
	if _, err := w.safe("../outside"); err == nil {
		t.Fatal("expected path escape to fail")
	}
}

func TestWorkspaceSymlinkEscapeBlocked(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	secret := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(secret, []byte("outside"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(root, "secret-link")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	w := New(root, ModeWorkspace)
	if r := w.ReadFile("secret-link"); r.OK {
		t.Fatal("workspace mode followed a symlink outside the workspace")
	}
}

func TestDisabledModeBlocksEveryTool(t *testing.T) {
	root := t.TempDir()
	w := New(root, ModeDisabled)
	if r := w.ListFiles("."); r.OK {
		t.Fatal("disabled mode allowed list_files")
	}
	if r := w.ReadFile("missing"); r.OK {
		t.Fatal("disabled mode allowed read_file")
	}
	if r := w.Search("x", "."); r.OK {
		t.Fatal("disabled mode allowed search")
	}
	if r := w.WriteFile("new.txt", "x"); r.OK {
		t.Fatal("disabled mode allowed write_file")
	}
	if r := w.Shell("date"); r.OK {
		t.Fatal("disabled mode allowed shell")
	}
}

func TestWorkspaceWriteRead(t *testing.T) {
	root := t.TempDir()
	w := New(root, ModeWorkspace)
	if r := w.WriteFile("hello.txt", "world"); !r.OK {
		t.Fatal(r.Output)
	}
	r := w.ReadFile("hello.txt")
	if !r.OK || r.Output != "world" {
		t.Fatalf("unexpected read result: %#v", r)
	}
}

func TestListFilesIncludesModificationDate(t *testing.T) {
	root := t.TempDir()
	w := New(root, ModeWorkspace)
	path := filepath.Join(root, "dated.txt")
	if err := os.WriteFile(path, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	stamp := time.Date(2020, time.January, 2, 3, 4, 0, 0, time.Local)
	if err := os.Chtimes(path, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	r := w.ListFiles(".")
	if !r.OK {
		t.Fatal(r.Output)
	}
	if !strings.Contains(r.Output, "2020-01-02 03:04 f dated.txt") {
		t.Fatalf("listing does not include modification date: %q", r.Output)
	}
}

func TestAskModeFailsClosedWithoutApprovalCallback(t *testing.T) {
	w := New(t.TempDir(), ModeAsk)
	if r := w.ListFiles("."); r.OK {
		t.Fatal("ask mode allowed a tool operation without an approval callback")
	}
	if r := w.Shell("date"); r.OK {
		t.Fatal("ask mode allowed shell without an approval callback")
	}
}

func TestAskModeRequiresApprovalForEachOperation(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "input.txt"), []byte("input"), 0600); err != nil {
		t.Fatal(err)
	}
	w := New(root, ModeAsk)
	approvals := 0
	w.Confirm = func(action, target string) bool {
		approvals++
		return true
	}
	if r := w.ReadFile("input.txt"); !r.OK {
		t.Fatal(r.Output)
	}
	if r := w.WriteFile("output.txt", "output"); !r.OK {
		t.Fatal(r.Output)
	}
	if approvals != 2 {
		t.Fatalf("got %d approvals, want one for each operation", approvals)
	}
}

func TestAskModeResponseApprovalIsTemporary(t *testing.T) {
	root := t.TempDir()
	w := New(root, ModeAsk)
	approvals := 0
	w.Confirm = func(action, target string) bool {
		approvals++
		return false
	}
	w.BeginResponse()
	w.AllowResponse()
	if r := w.WriteFile("one.txt", "1"); !r.OK {
		t.Fatal(r.Output)
	}
	if r := w.ReadFile("one.txt"); !r.OK {
		t.Fatal(r.Output)
	}
	if approvals != 0 {
		t.Fatalf("response approval unexpectedly prompted %d times", approvals)
	}
	w.EndResponse()
	if r := w.WriteFile("two.txt", "2"); r.OK {
		t.Fatal("response approval leaked into the next prompt")
	}
	if approvals != 1 {
		t.Fatalf("next prompt should request approval once, got %d", approvals)
	}
}

func TestAskModeResponseDenialBlocksRemainingOperations(t *testing.T) {
	w := New(t.TempDir(), ModeAsk)
	approvals := 0
	w.Confirm = func(action, target string) bool {
		approvals++
		return true
	}
	w.BeginResponse()
	w.DenyResponse()
	if r := w.ReadFile("one.txt"); r.OK {
		t.Fatal("response denial allowed a tool operation")
	}
	if r := w.WriteFile("two.txt", "2"); r.OK {
		t.Fatal("response denial allowed a later tool operation")
	}
	if approvals != 0 {
		t.Fatalf("response denial still prompted %d times", approvals)
	}
	w.EndResponse()
}

func TestWorkspaceShellAllowsDateAndGitGH(t *testing.T) {
	w := New(t.TempDir(), ModeWorkspace)
	if r := w.Shell("date +%Y"); !r.OK {
		t.Fatalf("date should be allowed: %s", r.Output)
	}
}

func TestWorkspaceShellBlocksSudoAndArbitraryCommands(t *testing.T) {
	w := New(t.TempDir(), ModeWorkspace)
	for _, command := range []string{"sudo id", "sh -c 'id'", "python3 -c 'print(1)'"} {
		if r := w.Shell(command); r.OK || !strings.Contains(r.Output, "PERMISSION_DENIED") {
			t.Fatalf("command %q was not blocked: %#v", command, r)
		}
	}
}

func TestWorkspaceShellDoesNotInterpretShellOperators(t *testing.T) {
	root := t.TempDir()
	w := New(root, ModeWorkspace)
	marker := filepath.Join(root, "should-not-exist")
	r := w.Shell("date +%Y; touch " + marker)
	if r.OK {
		t.Fatal("compound shell command unexpectedly succeeded")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("workspace shell interpreted a command separator")
	}
}

func TestWorkspaceShellBlocksAlternateGitDirectories(t *testing.T) {
	w := New(t.TempDir(), ModeWorkspace)
	r := w.Shell("git -C /tmp status")
	if r.OK || !strings.Contains(r.Output, "PERMISSION_DENIED") {
		t.Fatalf("alternate git directory was not blocked: %#v", r)
	}
}

func TestShellFailureIncludesExitStatus(t *testing.T) {
	w := New(t.TempDir(), ModeAuto)
	r := w.Shell("exit 7")
	if r.OK {
		t.Fatal("expected non-zero shell exit to fail")
	}
	if !strings.Contains(r.Output, "exit status 7") {
		t.Fatalf("failure output should include exit status: %q", r.Output)
	}
}

func TestLegacyModesNormalize(t *testing.T) {
	for legacy, want := range map[string]string{
		"read-only":      ModeDisabled,
		"deny":           ModeDisabled,
		"workspace-write": ModeAsk,
		"request-review": ModeAsk,
		"unrestricted":    ModeAuto,
	} {
		if got := NormalizeApprovalMode(legacy); got != want {
			t.Errorf("NormalizeApprovalMode(%q) = %q, want %q", legacy, got, want)
		}
	}
}
