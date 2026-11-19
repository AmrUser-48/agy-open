// Package usage normalizes provider-reported model token counters.
// Missing counters remain unknown; this package never estimates tokens from bytes.
package usage

import (
	"fmt"
	"math"
)

type TokenUsage struct {
	Provider string `json:"provider,omitempty"`
	Model    string `json:"model,omitempty"`
	Source   string `json:"source,omitempty"`

	InputTokens          *int64 `json:"inputTokens,omitempty"`
	CachedInputTokens    *int64 `json:"cachedInputTokens,omitempty"`
	CacheWriteInputTokens *int64 `json:"cacheWriteInputTokens,omitempty"`
	OutputTokens         *int64 `json:"outputTokens,omitempty"`
	ReasoningTokens      *int64 `json:"reasoningTokens,omitempty"`
	ToolUseTokens        *int64 `json:"toolUseTokens,omitempty"`
	TotalTokens          *int64 `json:"totalTokens,omitempty"`
}

func (u *TokenUsage) HasCounters() bool {
	return u != nil && (u.InputTokens != nil || u.CachedInputTokens != nil ||
		u.CacheWriteInputTokens != nil || u.OutputTokens != nil ||
		u.ReasoningTokens != nil || u.ToolUseTokens != nil || u.TotalTokens != nil)
}

func (u *TokenUsage) Clone() *TokenUsage {
	if u == nil {
		return nil
	}
	clone := *u
	clone.InputTokens = cloneInt64(u.InputTokens)
	clone.CachedInputTokens = cloneInt64(u.CachedInputTokens)
	clone.CacheWriteInputTokens = cloneInt64(u.CacheWriteInputTokens)
	clone.OutputTokens = cloneInt64(u.OutputTokens)
	clone.ReasoningTokens = cloneInt64(u.ReasoningTokens)
	clone.ToolUseTokens = cloneInt64(u.ToolUseTokens)
	clone.TotalTokens = cloneInt64(u.TotalTokens)
	return &clone
}

// FromOpenAIChatCompletion normalizes Chat Completions usage fields. It accepts
// OpenAI-compatible payloads, including provider-specific extensions.
func FromOpenAIChatCompletion(provider, model string, fields map[string]any) *TokenUsage {
	if len(fields) == 0 {
		return nil
	}
	u := &TokenUsage{
		Provider: provider,
		Model:    model,
		Source:   "chat.completion.usage",
		InputTokens: number(fields["prompt_tokens"]),
		OutputTokens: number(fields["completion_tokens"]),
		TotalTokens: number(fields["total_tokens"]),
	}
	if details, ok := fields["prompt_tokens_details"].(map[string]any); ok {
		u.CachedInputTokens = number(details["cached_tokens"])
		u.CacheWriteInputTokens = number(details["cache_write_tokens"])
	}
	if details, ok := fields["completion_tokens_details"].(map[string]any); ok {
		u.ReasoningTokens = number(details["reasoning_tokens"])
	}
	if !u.HasCounters() {
		return nil
	}
	return u
}

// FromOpenAIResponses normalizes Responses API usage fields.
func FromOpenAIResponses(provider, model string, fields map[string]any) *TokenUsage {
	if len(fields) == 0 {
		return nil
	}
	u := &TokenUsage{
		Provider:    provider,
		Model:       model,
		Source:      "response.usage",
		InputTokens: number(fields["input_tokens"]),
		OutputTokens: number(fields["output_tokens"]),
		TotalTokens: number(fields["total_tokens"]),
	}
	if details, ok := fields["input_tokens_details"].(map[string]any); ok {
		u.CachedInputTokens = number(details["cached_tokens"])
		u.CacheWriteInputTokens = number(details["cache_write_tokens"])
	}
	if details, ok := fields["output_tokens_details"].(map[string]any); ok {
		u.ReasoningTokens = number(details["reasoning_tokens"])
	}
	if !u.HasCounters() {
		return nil
	}
	return u
}

// FromGeminiMetadata normalizes Gemini GenerateContent and Antigravity
// usageMetadata fields. Provider-defined totals are retained as reported.
func FromGeminiMetadata(provider, model string, fields map[string]any) *TokenUsage {
	if len(fields) == 0 {
		return nil
	}
	u := &TokenUsage{
		Provider:          provider,
		Model:             model,
		Source:             "usageMetadata",
		InputTokens:        number(fields["promptTokenCount"]),
		CachedInputTokens:  number(fields["cachedContentTokenCount"]),
		OutputTokens:       number(fields["candidatesTokenCount"]),
		ReasoningTokens:    number(fields["thoughtsTokenCount"]),
		ToolUseTokens:      number(fields["toolUsePromptTokenCount"]),
		TotalTokens:        number(fields["totalTokenCount"]),
	}
	if !u.HasCounters() {
		return nil
	}
	return u
}

// FromGeminiInteraction normalizes the Google Interactions API usage object.
func FromGeminiInteraction(provider, model string, fields map[string]any) *TokenUsage {
	if len(fields) == 0 {
		return nil
	}
	u := &TokenUsage{
		Provider:          provider,
		Model:             model,
		Source:             "interaction.usage",
		InputTokens:        number(fields["total_input_tokens"]),
		CachedInputTokens:  number(fields["total_cached_tokens"]),
		OutputTokens:       number(fields["total_output_tokens"]),
		ReasoningTokens:    number(fields["total_thought_tokens"]),
		ToolUseTokens:      number(fields["total_tool_use_tokens"]),
		TotalTokens:        number(fields["total_tokens"]),
	}
	if !u.HasCounters() {
		return nil
	}
	return u
}

// Totals sums only counters actually reported by the provider. The sample
// count for each field distinguishes a missing value from a reported zero.
type Totals struct {
	Responses int64 `json:"responses"`

	InputTokens          int64 `json:"inputTokens"`
	InputSamples         int64 `json:"inputSamples"`
	CachedInputTokens    int64 `json:"cachedInputTokens"`
	CachedInputSamples   int64 `json:"cachedInputSamples"`
	CacheWriteInputTokens int64 `json:"cacheWriteInputTokens"`
	CacheWriteSamples    int64 `json:"cacheWriteSamples"`
	OutputTokens         int64 `json:"outputTokens"`
	OutputSamples        int64 `json:"outputSamples"`
	ReasoningTokens      int64 `json:"reasoningTokens"`
	ReasoningSamples     int64 `json:"reasoningSamples"`
	ToolUseTokens        int64 `json:"toolUseTokens"`
	ToolUseSamples       int64 `json:"toolUseSamples"`
	TotalTokens          int64 `json:"totalTokens"`
	TotalSamples         int64 `json:"totalSamples"`
}

func (t *Totals) Add(record *TokenUsage) {
	if t == nil || record == nil {
		return
	}
	t.Responses++
	add(&t.InputTokens, &t.InputSamples, record.InputTokens)
	add(&t.CachedInputTokens, &t.CachedInputSamples, record.CachedInputTokens)
	add(&t.CacheWriteInputTokens, &t.CacheWriteSamples, record.CacheWriteInputTokens)
	add(&t.OutputTokens, &t.OutputSamples, record.OutputTokens)
	add(&t.ReasoningTokens, &t.ReasoningSamples, record.ReasoningTokens)
	add(&t.ToolUseTokens, &t.ToolUseSamples, record.ToolUseTokens)
	add(&t.TotalTokens, &t.TotalSamples, record.TotalTokens)
}

func add(total, samples *int64, value *int64) {
	if value == nil {
		return
	}
	*total += *value
	*samples = *samples + 1
}

func FormatCount(value, samples, responses int64) string {
	if samples == 0 {
		return "not reported"
	}
	if samples < responses {
		return fmt.Sprintf("%d (reported for %d/%d responses)", value, samples, responses)
	}
	return fmt.Sprintf("%d", value)
}

func number(value any) *int64 {
	var result int64
	switch n := value.(type) {
	case int:
		result = int64(n)
	case int8:
		result = int64(n)
	case int16:
		result = int64(n)
	case int32:
		result = int64(n)
	case int64:
		result = n
	case uint:
		if uint64(n) > uint64(^uint64(0)>>1) {
			return nil
		}
		result = int64(n)
	case uint64:
		if n > uint64(^uint64(0)>>1) {
			return nil
		}
		result = int64(n)
	case float64:
		if math.IsNaN(n) || math.IsInf(n, 0) || n < 0 ||
			n >= float64(int64(^uint64(0)>>1)) || math.Trunc(n) != n {
			return nil
		}
		result = int64(n)
	case float32:
		if math.IsNaN(float64(n)) || math.IsInf(float64(n), 0) || n < 0 ||
			float64(n) >= float64(int64(^uint64(0)>>1)) || math.Trunc(float64(n)) != float64(n) {
			return nil
		}
		result = int64(n)
	default:
		return nil
	}
	if result < 0 {
		return nil
	}
	return &result
}

func cloneInt64(value *int64) *int64 {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}
