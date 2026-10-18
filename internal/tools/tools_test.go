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
