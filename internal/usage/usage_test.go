package usage

import (
	"encoding/json"
	"testing"
)

func TestProviderUsageNormalizersPreserveKnownFields(t *testing.T) {
	tests := []struct {
		name string
		decode func(map[string]any) *TokenUsage
		raw string
		want TokenUsage
	}{
		{
			name: "OpenAI chat completion",
			decode: func(fields map[string]any) *TokenUsage { return FromOpenAIChatCompletion("groq", "qwen", fields) },
			raw: `{"prompt_tokens":120,"completion_tokens":35,"total_tokens":155,"prompt_tokens_details":{"cached_tokens":12,"cache_write_tokens":5},"completion_tokens_details":{"reasoning_tokens":8}}`,
			want: TokenUsage{Provider:"groq",Model:"qwen",Source:"chat.completion.usage",InputTokens:int64Ptr(120),CachedInputTokens:int64Ptr(12),CacheWriteInputTokens:int64Ptr(5),OutputTokens:int64Ptr(35),ReasoningTokens:int64Ptr(8),TotalTokens:int64Ptr(155)},
		},
		{
			name: "OpenAI Responses",
			decode: func(fields map[string]any) *TokenUsage { return FromOpenAIResponses("openai", "gpt", fields) },
			raw: `{"input_tokens":200,"output_tokens":50,"total_tokens":250,"input_tokens_details":{"cached_tokens":40,"cache_write_tokens":5},"output_tokens_details":{"reasoning_tokens":20}}`,
			want: TokenUsage{Provider:"openai",Model:"gpt",Source:"response.usage",InputTokens:int64Ptr(200),CachedInputTokens:int64Ptr(40),CacheWriteInputTokens:int64Ptr(5),OutputTokens:int64Ptr(50),ReasoningTokens:int64Ptr(20),TotalTokens:int64Ptr(250)},
		},
		{
			name: "Gemini",
			decode: func(fields map[string]any) *TokenUsage { return FromGeminiMetadata("gemini-api", "flash", fields) },
			raw: `{"promptTokenCount":300,"cachedContentTokenCount":60,"candidatesTokenCount":70,"thoughtsTokenCount":25,"toolUsePromptTokenCount":15,"totalTokenCount":410}`,
			want: TokenUsage{Provider:"gemini-api",Model:"flash",Source:"usageMetadata",InputTokens:int64Ptr(300),CachedInputTokens:int64Ptr(60),OutputTokens:int64Ptr(70),ReasoningTokens:int64Ptr(25),ToolUseTokens:int64Ptr(15),TotalTokens:int64Ptr(410)},
		},
		{
			name: "Gemini Interactions",
			decode: func(fields map[string]any) *TokenUsage { return FromGeminiInteraction("ai-studio", "gemini", fields) },
			raw: `{"total_input_tokens":400,"total_cached_tokens":100,"total_output_tokens":80,"total_thought_tokens":30,"total_tool_use_tokens":20,"total_tokens":530}`,
			want: TokenUsage{Provider:"ai-studio",Model:"gemini",Source:"interaction.usage",InputTokens:int64Ptr(400),CachedInputTokens:int64Ptr(100),OutputTokens:int64Ptr(80),ReasoningTokens:int64Ptr(30),ToolUseTokens:int64Ptr(20),TotalTokens:int64Ptr(530)},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var fields map[string]any
			if err := json.Unmarshal([]byte(test.raw), &fields); err != nil { t.Fatal(err) }
			got := test.decode(fields)
			if got == nil { t.Fatal("normalizer returned nil") }
			if !equalUsage(got, &test.want) { t.Fatalf("usage = %#v, want %#v", got, &test.want) }
		})
	}
}

func TestUsageKeepsMissingCountersUnknownAndReportedZeroKnown(t *testing.T) {
	zero := FromOpenAIChatCompletion("groq", "qwen", map[string]any{"prompt_tokens": float64(0)})
	if zero == nil || zero.InputTokens == nil || *zero.InputTokens != 0 {
		t.Fatalf("reported zero input tokens lost: %#v", zero)
	}
	if zero.OutputTokens != nil || zero.TotalTokens != nil {
		t.Fatalf("missing fields were converted to zero: %#v", zero)
	}
	if got := FormatCount(0, 0, 1); got != "not reported" {
		t.Fatalf("missing counter formatted as %q", got)
	}
	if got := FormatCount(0, 1, 1); got != "0" {
		t.Fatalf("reported zero formatted as %q", got)
	}
}

func TestTotalsTracksPartialCountersAndClones(t *testing.T) {
	record := FromOpenAIChatCompletion("groq", "qwen", map[string]any{
		"prompt_tokens": float64(11),
		"completion_tokens": float64(5),
		"total_tokens": float64(16),
	})
	var totals Totals
	totals.Add(record)
	totals.Add(FromGeminiMetadata("gemini-api", "flash", map[string]any{
		"promptTokenCount": float64(7),
		"totalTokenCount": float64(9),
	}))
	totals.Add(&TokenUsage{Provider: "custom", Model: "model", Source: "usage not returned"})
	if totals.Responses != 3 || totals.InputTokens != 18 || totals.InputSamples != 2 ||
		totals.OutputTokens != 5 || totals.OutputSamples != 1 || totals.TotalTokens != 25 {
		t.Fatalf("totals = %#v", totals)
	}
	clone := record.Clone()
	*clone.InputTokens = 90
	if *record.InputTokens != 11 { t.Fatal("Clone shares counter pointers with original") }
}

func int64Ptr(value int64) *int64 { return &value }

func equalUsage(a, b *TokenUsage) bool {
	if a.Provider != b.Provider || a.Model != b.Model || a.Source != b.Source { return false }
	return equalInt(a.InputTokens,b.InputTokens) && equalInt(a.CachedInputTokens,b.CachedInputTokens) &&
		equalInt(a.CacheWriteInputTokens,b.CacheWriteInputTokens) && equalInt(a.OutputTokens,b.OutputTokens) &&
		equalInt(a.ReasoningTokens,b.ReasoningTokens) && equalInt(a.ToolUseTokens,b.ToolUseTokens) &&
		equalInt(a.TotalTokens,b.TotalTokens)
}

func equalInt(a,b *int64) bool {
	if a==nil || b==nil { return a==nil && b==nil }
	return *a==*b
}
