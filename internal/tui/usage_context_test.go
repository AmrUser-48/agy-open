package tui

import (
	"strings"
	"testing"

	"github.com/AmrUser-48/agy-open/internal/agent"
	tokenusage "github.com/AmrUser-48/agy-open/internal/usage"
)

func TestFormatUsageSummarySeparatesProviderCountersFromLocalEstimates(t *testing.T) {
	last := &tokenusage.TokenUsage{
		Provider: "groq", Model: "qwen/qwen3.8-27b", Source: "chat.completion.usage",
		InputTokens: usageContextInt64Ptr(700), CachedInputTokens: usageContextInt64Ptr(120),
		OutputTokens: usageContextInt64Ptr(80), TotalTokens: usageContextInt64Ptr(780),
	}
	totals := tokenusage.Totals{
		Responses: 2, InputTokens: 1000, InputSamples: 1,
		OutputTokens: 150, OutputSamples: 2, TotalTokens: 1150, TotalSamples: 2,
	}
	got := strings.Join(formatUsageSummary(last, totals), "\n")
	for _, want := range []string{
		"Provider-reported token usage · 2 usage records",
		"Latest response: groq · qwen/qwen3.8-27b · chat.completion.usage",
		"Input: 700 · Cached input: 120",
		"Session input: 1000 (reported for 1/2 responses)",
		"Session totals combine all providers and models",
		"context-compaction usage is included",
		"quota preflight estimates are excluded",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("usage summary omitted %q:\n%s", want, got)
		}
	}
}

func TestFormatUsageSummaryKeepsMissingCountersUnknown(t *testing.T) {
	got := strings.Join(formatUsageSummary(nil, tokenusage.Totals{}), "\n")
	for _, want := range []string{
		"No completed model responses",
		"Only provider-reported counters",
		"Missing values remain unknown",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("empty usage summary omitted %q:\n%s", want, got)
		}
	}
}

func TestFormatContextSummaryShowsReservedBudgetAndActiveSchemas(t *testing.T) {
	status := agent.ContextStatus{
		WindowTokens: 8192, WindowSource: "configured override",
		OutputReserveTokens: 1024, InputBudgetTokens: 7168,
		LastInputTokens: usageContextInt64Ptr(6000),
		LastUsageProvider: "groq", LastUsageModel: "qwen/qwen3.8-27b",
		LastUsageSource: "chat.completion.usage", RemainingTokens: 1168,
		ActiveMessages: 12, Compactions: 2, SummaryActive: true,
		CompactionCheckPending: true, LastCompactionInputTokens: 7000,
	}
	metrics := agent.RequestMetrics{
		Turn: 15, RequestJSONBytes: 12000, Representation: "gemini.Request JSON",
		Complete: true, BuiltinToolCount: 14, MCPToolCount: 105,
		SystemInstructionJSONBytes: 1000, ConversationJSONBytes: 8000,
		BuiltinToolSchemaBytes: 1500, MCPToolSchemaBytes: 2500,
		FunctionCallJSONBytes: 400, FunctionResponseJSONBytes: 500,
		InitialPromptBytes: 120, ProviderPayloadMeasured: true,
		ProviderPayloadBytes: 9000, ProviderPayloadKind: "OpenAI-compatible JSON",
	}
	got := strings.Join(formatContextSummary(status, metrics, 24000, "session-1", "developer", "groq:qwen/qwen3.8-27b"), "\n")
	for _, want := range []string{
		"Active conversation characters: 24000 (not a token count)",
		"Model context window: 8192 tokens",
		"Output reserve: 1024 tokens",
		"Usable input budget after reserve: 7168 tokens",
		"Latest provider-reported input: 6000 tokens (groq · qwen/qwen3.8-27b · chat.completion.usage)",
		"Remaining input budget after reserve: 1168 tokens",
		"Active context summary: yes · 2 compactions",
		"Context-limit check: pending",
		"approximately 3000 tokens at 4 bytes/token",
		"14 built-in tools · 105 active MCP tools",
		"Provider JSON body: 9000 bytes",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("context summary omitted %q:\n%s", want, got)
		}
	}
}

func TestFormatContextSummaryDoesNotInventUnknownWindowOrRequestMetrics(t *testing.T) {
	got := strings.Join(formatContextSummary(agent.ContextStatus{
		WindowSource: "unknown",
	}, agent.RequestMetrics{}, 0, "session-2", "default", "model-1"), "\n")
	for _, want := range []string{
		"Model context window: unknown",
		"Latest provider-reported input: not reported",
		"Active context summary: none",
		"Last request size: not measured yet",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("unknown context summary omitted %q:\n%s", want, got)
		}
	}
}

func usageContextInt64Ptr(value int64) *int64 { return &value }
