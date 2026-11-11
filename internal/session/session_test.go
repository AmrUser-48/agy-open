package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStructuredToolActivityRoundTripsInJSONLHistory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	store, err := New("none")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer store.Close()

	started := time.Date(2026, time.November, 9, 10, 0, 0, 0, time.UTC)
	entries := []Message{
		{Role: "user", Content: "inspect this project"},
		{
			Role: "tool", Kind: "tool_call", Tool: "shell",
			Target: "git status", Command: "git status", CallID: "shell-1", Arguments: "{\"command\":\"git status\"}",
			Output: "clean", OK: true, StartedAt: started,
			FinishedAt: started.Add(12 * time.Millisecond), DurationMS: 12,
		},
		{
			Role: "tool", Kind: "tool_approval", Tool: "shell",
			Target: "reboot", Decision: "denied",
			Content: "denied shell", StartedAt: started.Add(time.Second),
		},
		{Role: "model", Content: "The working tree is clean."},
	}
	for _, entry := range entries {
		if err := store.Add(entry); err != nil {
			t.Fatalf("Add(%s): %v", entry.Kind, err)
		}
	}

	got, err := LoadRecent(store.Path(), 0)
	if err != nil {
		t.Fatalf("LoadRecent: %v", err)
	}
	if len(got) != len(entries) {
		t.Fatalf("loaded %d entries, want %d", len(got), len(entries))
	}
	if got[1].Kind != "tool_call" || got[1].Tool != "shell" || got[1].Target != "git status" ||
		got[1].Command != "git status" || got[1].CallID != "shell-1" ||
		got[1].Arguments != entries[1].Arguments || got[1].Output != "clean" ||
		!got[1].OK || got[1].DurationMS != 12 || !got[1].StartedAt.Equal(started) {
		t.Fatalf("tool activity did not round-trip: %#v", got[1])
	}
	if got[2].Kind != "tool_approval" || got[2].Decision != "denied" {
		t.Fatalf("approval activity did not round-trip: %#v", got[2])
	}
	if got[0].Role != "user" || got[0].Content != entries[0].Content ||
		got[3].Role != "model" || got[3].Content != entries[3].Content {
		t.Fatalf("legacy transcript fields changed: %#v", got)
	}
}

func TestLegacyLZOSettingCreatesCorrectlyLabeledZstdHistory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	store, err := New("lzo")
	if err != nil {
		t.Fatalf("New(lzo): %v", err)
	}
	if !strings.HasSuffix(store.Path(), ".jsonl.zst") {
		t.Fatalf("legacy lzo setting created misleading path %q", store.Path())
	}
	if err := store.Add(Message{Role: "user", Content: "legacy alias round-trip"}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	path := store.Path()
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	got, err := LoadRecent(path, 0)
	if err != nil {
		t.Fatalf("LoadRecent: %v", err)
	}
	if len(got) != 1 || got[0].Content != "legacy alias round-trip" {
		t.Fatalf("Zstandard history round-trip = %#v", got)
	}
}

func TestLoadRecentReadsLegacyLZOExtensionAsZstd(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.jsonl.lzo")
	store := Open(path, "lzo")
	if err := store.Add(Message{Role: "user", Content: "legacy extension"}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	got, err := LoadRecent(path, 0)
	if err != nil {
		t.Fatalf("LoadRecent legacy .lzo: %v", err)
	}
	if len(got) != 1 || got[0].Content != "legacy extension" {
		t.Fatalf("legacy .lzo history = %#v", got)
	}
}

func TestLoadRecentReadsLegacyMessagesWithoutActivityFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.jsonl")
	if err := os.WriteFile(path, []byte("{\"role\":\"user\",\"content\":\"hello\"}\n{\"role\":\"model\",\"content\":\"world\"}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := LoadRecent(path, 0)
	if err != nil {
		t.Fatalf("LoadRecent: %v", err)
	}
	if len(got) != 2 || got[0].Role != "user" || got[0].Content != "hello" ||
		got[1].Role != "model" || got[1].Content != "world" {
		t.Fatalf("legacy messages did not load: %#v", got)
	}
}


func TestLoadRecentRecoversShellCommandFromLegacyArguments(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy-tools.jsonl")
	records := []string{
		`{"role":"tool","kind":"tool_call","tool":"shell","arguments":"{\"command\":\"make -C /work/linux oldconfig\"}","output":"HOSTCC scripts/kconfig/conf.o","ok":true}`,
		`{"role":"tool","kind":"tool_call","tool":"shell","target":"pwd","output":"/work","ok":true}`,
		`{"role":"tool","kind":"tool_call","tool":"read_file","target":"kernel/.config","arguments":"{\"path\":\"kernel/.config\"}","output":"CONFIG_X=y","ok":true}`,
	}
	if err := os.WriteFile(path, []byte(strings.Join(records, "\n")+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := LoadRecent(path, 0)
	if err != nil {
		t.Fatalf("LoadRecent: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("loaded %d records, want 3", len(got))
	}
	if want := "make -C /work/linux oldconfig"; got[0].Command != want || got[0].ShellCommand() != want {
		t.Fatalf("legacy command = %q, want %q", got[0].Command, want)
	}
	if got[1].Command != "pwd" {
		t.Fatalf("target fallback = %q, want pwd", got[1].Command)
	}
	if got[2].Command != "" || got[2].ShellCommand() != "" {
		t.Fatalf("non-shell tool incorrectly gained a shell command: %#v", got[2])
	}
}

func TestShellCommandPrefersExplicitCommandOverLegacyArguments(t *testing.T) {
	message := Message{
		Role: "tool", Kind: "tool_call", Tool: "shell",
		Target: "make oldconfig", Command: "make -j1 oldconfig",
		Arguments: `{"command":"incorrect legacy value"}`,
	}
	if got := message.ShellCommand(); got != "make -j1 oldconfig" {
		t.Fatalf("ShellCommand() = %q, want explicit command", got)
	}
}
