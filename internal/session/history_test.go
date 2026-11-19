package session

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRecentPrompts(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".agy", "history")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	files := []struct {
		name string
		body string
	}{
		{"20260101T000000.000Z.jsonl", `{"role":"user","content":"first"}
{"role":"model","content":"ok"}
`},
		{"20260102T000000.000Z.jsonl", `{"role":"user","content":"second"}
{"role":"user","content":"third"}
`},
	}
	for _, f := range files {
		p := filepath.Join(dir, f.name)
		if err := os.WriteFile(p, []byte(f.body), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, time.Now(), time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	got, err := RecentPrompts(10)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"first", "second", "third"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}
