package tui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/AmrUser-48/agy-open/internal/agent"
	tokenusage "github.com/AmrUser-48/agy-open/internal/usage"
)

func formatUsageSummary(last *tokenusage.TokenUsage, totals tokenusage.Totals) []string {
	format := func(value *int64) string {
		if value == nil {
			return "not reported"
		}
		return strconv.FormatInt(*value, 10)
	}

	lines := []string{fmt.Sprintf("Provider-reported token usage · %d usage records", totals.Responses)}
	if last == nil {
		lines = append(lines, "No completed model responses in this session yet.")
	} else if !last.HasCounters() {
		lines = append(lines, fmt.Sprintf("Latest response: %s · %s · provider did not report token counters.",
			last.Provider, last.Model))
	} else {
		lines = append(lines,
			fmt.Sprintf("Latest response: %s · %s · %s", last.Provider, last.Model, last.Source),
			fmt.Sprintf("Input: %s · Cached input: %s · Cache write: %s",
				format(last.InputTokens), format(last.CachedInputTokens), format(last.CacheWriteInputTokens)),
			fmt.Sprintf("Output: %s · Reasoning: %s · Tool use: %s · Total: %s",
				format(last.OutputTokens), format(last.ReasoningTokens), format(last.ToolUseTokens), format(last.TotalTokens)),
		)
	}
	if totals.Responses > 0 {
		lines = append(lines,
			fmt.Sprintf("Session input: %s · Cached: %s · Cache write: %s",
				tokenusage.FormatCount(totals.InputTokens, totals.InputSamples, totals.Responses),
				tokenusage.FormatCount(totals.CachedInputTokens, totals.CachedInputSamples, totals.Responses),
				tokenusage.FormatCount(totals.CacheWriteInputTokens, totals.CacheWriteSamples, totals.Responses)),
			fmt.Sprintf("Session output: %s · Reasoning: %s · Tool use: %s · Total: %s",
				tokenusage.FormatCount(totals.OutputTokens, totals.OutputSamples, totals.Responses),
				tokenusage.FormatCount(totals.ReasoningTokens, totals.ReasoningSamples, totals.Responses),
				tokenusage.FormatCount(totals.ToolUseTokens, totals.ToolUseSamples, totals.Responses),
				tokenusage.FormatCount(totals.TotalTokens, totals.TotalSamples, totals.Responses)),
		)
	}
	lines = append(lines,
		"Session totals combine all providers and models used in this conversation.",
		"Provider-reported context-compaction usage is included when counters are returned.",
		"Only provider-reported counters are included; quota preflight estimates are excluded. Missing values remain unknown.",
	)
	return lines
}

func formatContextSummary(status agent.ContextStatus, metrics agent.RequestMetrics, contextChars int, sessionID, role, model string) []string {
	items := []string{
		fmt.Sprintf("Active conversation characters: %d (not a token count)", contextChars),
		"Conversation: " + sessionID,
		"Role: " + role,
		"Model: " + model,
	}
	if status.WindowTokens > 0 {
		items = append(items,
			fmt.Sprintf("Model context window: %d tokens (%s)", status.WindowTokens, status.WindowSource),
			fmt.Sprintf("Output reserve: %d tokens", status.OutputReserveTokens),
			fmt.Sprintf("Safety headroom: %d tokens", status.SafetyHeadroomTokens),
			fmt.Sprintf("Usable input budget after reserve and safety headroom: %d tokens", status.InputBudgetTokens),
		)
	} else {
		items = append(items, "Model context window: unknown · automatic compaction disabled")
	}
	if status.LastInputTokens == nil {
		items = append(items, "Latest provider-reported input: not reported")
		if status.WindowTokens > 0 {
			items = append(items, "Remaining input budget: unknown until the provider reports input tokens")
		}
	} else {
		provenance := strings.TrimSpace(strings.Join([]string{
			status.LastUsageProvider, status.LastUsageModel, status.LastUsageSource,
		}, " · "))
		label := fmt.Sprintf("Latest provider-reported input: %d tokens", *status.LastInputTokens)
		if provenance != "" {
			label += " (" + provenance + ")"
		}
		items = append(items, label)
		if status.WindowTokens > 0 {
			items = append(items, fmt.Sprintf(
				"Remaining input budget after reserve and safety headroom: %d tokens (based on the last measured request)",
				status.RemainingTokens))
		}
	}
	if status.SummaryActive {
		items = append(items, fmt.Sprintf(
			"Active context summary: yes · %d compactions; complete canonical history remains in the session",
			status.Compactions))
	} else {
		items = append(items, "Active context summary: none")
	}
	if status.CompactionCheckPending {
		items = append(items, "Context-limit check: pending before the next model request")
	}
	if status.LastCompactionInputTokens > 0 {
		items = append(items, fmt.Sprintf("Last compaction triggered at: %d provider-reported input tokens", status.LastCompactionInputTokens))
	}

	if metrics.RequestJSONBytes == 0 {
		items = append(items, "Last request size: not measured yet")
		return items
	}
	approxTokens := (metrics.RequestJSONBytes + 3) / 4
	items = append(items,
		fmt.Sprintf("Last model request: %d Agy request JSON bytes · approximately %d tokens at 4 bytes/token (rough heuristic, not provider metering)",
			metrics.RequestJSONBytes, approxTokens),
		fmt.Sprintf("Last request schemas: %d built-in tools · %d active MCP tools",
			metrics.BuiltinToolCount, metrics.MCPToolCount),
		fmt.Sprintf("System / history: %d / %d bytes", metrics.SystemInstructionJSONBytes, metrics.ConversationJSONBytes),
		fmt.Sprintf("Built-in / MCP schema bytes: %d / %d", metrics.BuiltinToolSchemaBytes, metrics.MCPToolSchemaBytes),
		fmt.Sprintf("Function calls / results: %d / %d bytes", metrics.FunctionCallJSONBytes, metrics.FunctionResponseJSONBytes),
		fmt.Sprintf("Initial prompt: %d bytes", metrics.InitialPromptBytes),
	)
	representation := metrics.Representation
	if representation == "" {
		representation = "Agy request JSON; provider wire payload may differ"
	}
	items = append(items, "Measurement: "+representation)
	if !metrics.Complete {
		items = append(items, "Request-size component measurement is incomplete")
	}
	if metrics.ProviderPayloadMeasured {
		items = append(items, fmt.Sprintf("Provider JSON body: %d bytes (%s)", metrics.ProviderPayloadBytes, metrics.ProviderPayloadKind))
	} else {
		items = append(items, "Provider JSON body: not measured by this adapter")
	}
	return items
}
