package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPathEscapeBlocked(t *testing.T) {
	root := t.TempDir()
	w := New(root, "workspace-write")
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

func TestUnrestrictedAllowsPathsOutsideWorkspace(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "workspace")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	w := New(root, "unrestricted")
	got, err := w.safe("../outside")
	if err != nil {
		t.Fatalf("unrestricted path was rejected: %v", err)
	}
	want := filepath.Join(parent, "outside")
	if got != want {
		t.Fatalf("safe path = %q, want %q", got, want)
	}
}

func TestReadOnlyCannotWrite(t *testing.T) {
	w := New(t.TempDir(), "read-only")
	if result := w.WriteFile("blocked.txt", "no"); result.OK {
		t.Fatal("read-only mode allowed a write")
	}
}

func TestAskForApproveRequiresConfirmation(t *testing.T) {
	w := New(t.TempDir(), "ask-for-approve")
	w.Confirm = func(string, string) bool { return false }
	if result := w.WriteFile("blocked.txt", "no"); result.OK {
		t.Fatal("ask-for-approve allowed a write without confirmation")
	}
	w.Confirm = func(string, string) bool { return true }
	if result := w.WriteFile("allowed.txt", "yes"); !result.OK {
		t.Fatalf("confirmed write failed: %s", result.Output)
	}
}

