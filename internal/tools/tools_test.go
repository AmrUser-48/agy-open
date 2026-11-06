package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPathEscapeBlocked(t *testing.T) {
	root := t.TempDir()
	w := New(root, "auto")
	if _, err := w.safe("../outside"); err == nil {
		t.Fatal("expected path escape to fail")
	}
}

func TestWriteRead(t *testing.T) {
	root := t.TempDir()
	w := New(root, "auto")
	if r := w.WriteFile("hello.txt", "world"); !r.OK {
		t.Fatal(r.Output)
	}
	b, err := os.ReadFile(filepath.Join(root, "hello.txt"))
	if err != nil || string(b) != "world" {
		t.Fatalf("unexpected file contents")
	}
}

func TestListFilesIncludesModificationDate(t *testing.T) {
	root := t.TempDir()
	w := New(root, "auto")
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

func TestShellFailureIncludesExitStatus(t *testing.T) {
	w := New(t.TempDir(), "auto")
	r := w.Shell("exit 7")
	if r.OK {
		t.Fatal("expected non-zero shell exit to fail")
	}
	if !strings.Contains(r.Output, "exit status 7") {
		t.Fatalf("failure output should include exit status: %q", r.Output)
	}
}

func TestCommandPrefixExtractsExecutable(t *testing.T) {
	cases := map[string]string{
		"grep -C 5 \"Gemini 3.8 Flash\" CHANGELOG.md": "grep",
		"/usr/bin/grep -n needle file.txt": "grep",
		"sudo grep -n needle file.txt": "grep",
	}
	for command, want := range cases {
		if got := CommandPrefix(command); got != want {
			t.Errorf("CommandPrefix(%q) = %q, want %q", command, got, want)
		}
	}
}

func TestSessionApprovalAllowsSameCommandPrefixOnlyForSession(t *testing.T) {
	w := New(t.TempDir(), "ask")
	calls := 0
	w.Confirm = func(action, target string) ApprovalDecision {
		calls++
		if action != "shell" {
			t.Fatalf("unexpected approval action: %s", action)
		}
		if CommandPrefix(target) != "printf" {
			return ApprovalDeny
		}
		return ApprovalForSession
	}
	for _, command := range []string{"printf first", "printf second"} {
		if result := w.ShellContext(context.Background(), command); !result.OK {
			t.Fatalf("%q failed: %s", command, result.Output)
		}
	}
	if calls != 1 {
		t.Fatalf("approval callback called %d times, want once", calls)
	}
	if result := w.ShellContext(context.Background(), "printfx third"); result.OK || calls != 2 {
		t.Fatalf("prefix rule incorrectly allowed another executable: result=%#v approvals=%d", result, calls)
	}
}

func TestPersistentApprovalAllowsCommandPrefix(t *testing.T) {
	w := New(t.TempDir(), "ask")
	if err := os.WriteFile(filepath.Join(w.Root, "file.txt"), []byte("first\nsecond\n"), 0644); err != nil {
		t.Fatal(err)
	}
	calls := 0
	w.Confirm = func(action, target string) ApprovalDecision {
		calls++
		return ApprovalPersistent
	}
	if result := w.ShellContext(context.Background(), "grep -n first file.txt"); !result.OK {
		t.Fatalf("first approved command failed: %s", result.Output)
	}
	if result := w.ShellContext(context.Background(), "grep -n second file.txt"); !result.OK {
		t.Fatalf("command matching persisted prefix failed: %s", result.Output)
	}
	if calls != 1 {
		t.Fatalf("approval callback called %d times, want once", calls)
	}
	if len(w.PersistentAllowRules) != 1 || w.PersistentAllowRules[0].Prefix != "grep" {
		t.Fatalf("persistent rules = %#v, want one grep prefix", w.PersistentAllowRules)
	}
}

func TestOneTimeApprovalDoesNotCreateRule(t *testing.T) {
	w := New(t.TempDir(), "ask")
	calls := 0
	w.Confirm = func(action, target string) ApprovalDecision {
		calls++
		return ApprovalOnce
	}
	for _, command := range []string{"printf first", "printf second"} {
		if result := w.ShellContext(context.Background(), command); !result.OK {
			t.Fatalf("%q failed: %s", command, result.Output)
		}
	}
	if calls != 2 || len(w.SessionAllowRules) != 0 || len(w.PersistentAllowRules) != 0 {
		t.Fatalf("one-time approval created a rule: calls=%d session=%#v persistent=%#v", calls, w.SessionAllowRules, w.PersistentAllowRules)
	}
}

func TestPersistentPrefixDoesNotApproveCompoundCommands(t *testing.T) {
	root := t.TempDir()
	w := New(root, "ask")
	w.PersistentAllowRules = []CommandApprovalRule{{Action: "shell", Prefix: "grep"}}
	if err := os.WriteFile(filepath.Join(root, "file.txt"), []byte("needle\\n"), 0644); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(root, "should-not-exist")
	calls := 0
	w.Confirm = func(action, target string) ApprovalDecision {
		calls++
		return ApprovalDeny
	}
	command := "grep needle file.txt; touch " + marker
	if result := w.ShellContext(context.Background(), command); result.OK {
		t.Fatal("compound command was unexpectedly allowed by grep prefix rule")
	}
	if calls != 1 {
		t.Fatalf("expected a fresh approval prompt for compound command, got %d", calls)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("compound command executed despite denial")
	}
}
