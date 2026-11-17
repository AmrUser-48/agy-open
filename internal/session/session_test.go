package session

import (
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
