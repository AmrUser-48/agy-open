package session

import (
	"path/filepath"
	"testing"
	"time"
)

func TestToolActivityRoundTripsInSessionHistory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tool-history.jsonl")
	store := Open(path, "none")
	started := time.Date(2026, 11, 8, 20, 0, 0, 0, time.UTC)
	entry := Message{
		Role:       "tool",
		Kind:       "tool_call",
		Tool:       "shell",
		Target:     "date +%Y",
		Output:     "2026",
		OK:         true,
		StartedAt:  started,
		FinishedAt: started.Add(12 * time.Millisecond),
		DurationMS: 12,
	}
	if err := store.Add(entry); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	got, err := LoadRecent(path, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("loaded %d entries, want 1", len(got))
	}
	if got[0].Kind != entry.Kind || got[0].Tool != entry.Tool || got[0].Target != entry.Target {
		t.Fatalf("tool identity lost: %#v", got[0])
	}
	if got[0].Output != entry.Output || !got[0].OK || got[0].DurationMS != entry.DurationMS {
		t.Fatalf("tool result lost: %#v", got[0])
	}
	if !got[0].StartedAt.Equal(started) || !got[0].FinishedAt.Equal(started.Add(12*time.Millisecond)) {
		t.Fatalf("tool timestamps lost: %#v", got[0])
	}
}

func TestLegacySessionMessagesRemainReadable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.jsonl")
	store := Open(path, "none")
	if err := store.Add(Message{Role: "user", Content: "hello"}); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	got, err := LoadRecent(path, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Role != "user" || got[0].Content != "hello" || got[0].Kind != "" {
		t.Fatalf("legacy message changed: %#v", got)
	}
}
