package session

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"

	tokenusage "github.com/AmrUser-48/agy-open/internal/usage"
)

func TestLoadProviderUsageAggregatesAllCompressionModes(t *testing.T) {
	for _, test := range []struct {
		name string
		compression string
		extension string
	}{
		{"plain JSONL", "none", ".jsonl"},
		{"gzip JSONL", "gzip", ".jsonl.gz"},
		{"zstd JSONL", "zstd", ".jsonl.zst"},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "session"+test.extension)
			store := Open(path, test.compression)
			first := &tokenusage.TokenUsage{
				Provider: "groq", Model: "qwen", Source: "chat.completion.usage",
				InputTokens: usageInt64(12), OutputTokens: usageInt64(4), TotalTokens: usageInt64(16),
			}
			second := &tokenusage.TokenUsage{
				Provider: "gemini-api", Model: "flash", Source: "usageMetadata",
				InputTokens: usageInt64(8), CachedInputTokens: usageInt64(3), TotalTokens: usageInt64(11),
			}
			latestWithoutUsage := &tokenusage.TokenUsage{
				Provider: "gemini-live", Model: "live", Source: "provider response (usage not reported)",
			}
			for _, entry := range []Message{
				{Role: "user", Content: "hello"},
				{Role: "meta", Kind: "provider_usage", ProviderUsage: first},
				{Role: "model", Content: "world"},
				{Role: "meta", Kind: "provider_usage", ProviderUsage: second},
				{Role: "meta", Kind: "provider_usage", ProviderUsage: latestWithoutUsage},
			} {
				if err := store.Add(entry); err != nil {
					t.Fatalf("Add(%s): %v", entry.Kind, err)
				}
			}
			if err := store.Close(); err != nil {
				t.Fatalf("Close: %v", err)
			}

			last, totals, err := LoadProviderUsage(path)
			if err != nil {
				t.Fatalf("LoadProviderUsage: %v", err)
			}
			if last == nil || last.Provider != "gemini-live" || last.HasCounters() {
				t.Fatalf("last provider usage = %#v", last)
			}
			if totals.Responses != 3 || totals.InputTokens != 20 || totals.InputSamples != 2 ||
				totals.OutputTokens != 4 || totals.OutputSamples != 1 ||
				totals.CachedInputTokens != 3 || totals.CachedInputSamples != 1 ||
				totals.TotalTokens != 27 || totals.TotalSamples != 2 {
				t.Fatalf("provider usage totals = %#v", totals)
			}

			// Internal accounting rows remain in the archive but are not
			// returned by LoadRecent, which serves transcript views.
			entries, err := LoadRecent(path, 0)
			if err != nil {
				t.Fatalf("LoadRecent: %v", err)
			}
			if len(entries) != 2 || entries[0].Role != "user" || entries[0].Content != "hello" ||
				entries[1].Role != "model" || entries[1].Content != "world" {
				t.Fatalf("visible transcript contains metadata or lost conversation rows: %#v", entries)
			}
		})
	}
}

func usageInt64(value int64) *int64 { return &value }


func TestLoadActiveContextUsesLatestSnapshotAndOnlySubsequentEvents(t *testing.T) {
	path := filepath.Join(t.TempDir(), "active.jsonl")
	store := Open(path, "none")
	entries := []Message{
		{Role: "user", Content: "original request"},
		{Role: "meta", Kind: "provider_usage", ProviderUsage: &tokenusage.TokenUsage{InputTokens: usageInt64(10)}},
		{Role: "tool", Kind: "tool_call", Tool: "shell", CallID: "old-call", Arguments: `{"command":"true"}`, Output: "old result", OK: true},
		{Role: "meta", Kind: "context_snapshot", ContextSnapshot: []json.RawMessage{json.RawMessage(`{"role":"user","parts":[{"text":"first summary"}]}`)}, ContextSnapshotCount: 1},
		{Role: "user", Content: "after first snapshot"},
		{Role: "meta", Kind: "provider_usage", ProviderUsage: &tokenusage.TokenUsage{InputTokens: usageInt64(20)}},
		{Role: "meta", Kind: "context_usage", ProviderUsage: &tokenusage.TokenUsage{InputTokens: usageInt64(5)}},
		{Role: "meta", Kind: "context_snapshot", ContextSnapshot: []json.RawMessage{json.RawMessage(`{"role":"user","parts":[{"text":"latest summary"}]}`)}, ContextSnapshotCount: 2},
		{Role: "user", Content: "new prompt"},
		{Role: "tool", Kind: "tool_call", Tool: "read_file", CallID: "new-call", Arguments: `{"path":"README.md"}`, Output: "current result", OK: true},
	}
	for _, entry := range entries {
		if err := store.Add(entry); err != nil {
			t.Fatalf("Add(%s): %v", entry.Kind, err)
		}
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	snapshot, tail, err := LoadActiveContext(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot == nil || snapshot.ContextSnapshotCount != 2 || len(snapshot.ContextSnapshot) != 1 {
		t.Fatalf("active snapshot = %#v", snapshot)
	}
	var active struct {
		Role string `json:"role"`
		Parts []struct { Text string `json:"text"` } `json:"parts"`
	}
	if err := json.Unmarshal(snapshot.ContextSnapshot[0], &active); err != nil {
		t.Fatal(err)
	}
	if active.Role != "user" || len(active.Parts) != 1 || active.Parts[0].Text != "latest summary" {
		t.Fatalf("latest snapshot content = %#v", active)
	}
	if len(tail) != 2 || tail[0].Content != "new prompt" || tail[1].CallID != "new-call" {
		t.Fatalf("post-snapshot tail = %#v", tail)
	}

	visible, err := LoadRecent(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(visible) != 5 || visible[0].Content != "original request" ||
		visible[1].CallID != "old-call" || visible[2].Content != "after first snapshot" ||
		visible[3].Content != "new prompt" || visible[4].CallID != "new-call" {
		t.Fatalf("canonical visible transcript changed or exposed metadata: %#v", visible)
	}
	_, totals, err := LoadProviderUsage(path)
	if err != nil {
		t.Fatal(err)
	}
	if totals.Responses != 3 || totals.InputTokens != 35 {
		t.Fatalf("usage totals did not include context summarization: %#v", totals)
	}
}

func TestLoadActiveContextWithoutSnapshotDoesNotDuplicateHistory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "plain.jsonl")
	store := Open(path, "none")
	for _, message := range []Message{
		{Role: "user", Content: "first"},
		{Role: "model", Content: "second"},
		{Role: "meta", Kind: "provider_usage", ProviderUsage: &tokenusage.TokenUsage{}},
		{Role: "user", Content: "third"},
	} {
		if err := store.Add(message); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	snapshot, tail, err := LoadActiveContext(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot != nil || len(tail) != 3 || tail[0].Content != "first" || tail[2].Content != "third" {
		t.Fatalf("snapshot/tail = %#v / %#v", snapshot, tail)
	}
}


func TestLoadActiveContextDoesNotTruncatePostSnapshotCallResultPairs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "snapshot.jsonl")
	store := Open(path, "none")
	snapshot := Message{
		Role: "meta", Kind: "context_snapshot", ContextSnapshot: []json.RawMessage{
			json.RawMessage(`{"role":"user","parts":[{"text":"summary"}]}`),
		}, ContextSnapshotCount: 1,
	}
	if err := store.Add(snapshot); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		if err := store.Add(Message{Role: "user", Content: fmt.Sprintf("tail-%d", i)}); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Add(Message{Role: "tool", Kind: "tool_call", CallID: "late-call", Tool: "shell", Arguments: `{"command":"pwd"}`, Output: "/workspace"}); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	loadedSnapshot, tail, err := LoadActiveContext(path, 2)
	if err != nil {
		t.Fatal(err)
	}
	if loadedSnapshot == nil || len(tail) != 11 || tail[len(tail)-1].CallID != "late-call" {
		t.Fatalf("post-snapshot active tail was truncated: snapshot=%#v tail=%#v", loadedSnapshot, tail)
	}
}
