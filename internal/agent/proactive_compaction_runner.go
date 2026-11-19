package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/AmrUser-48/agy-open/internal/gemini"
	tokenusage "github.com/AmrUser-48/agy-open/internal/usage"
)

// runProactiveCompactionSummary executes one summary request for a detached
// worker. It is intentionally limited to the two HTTP-backed API-key providers
// whose request budgeting and concurrent Generate behavior were verified.
// It returns data only and never mutates Agent messages, session history, usage
// totals, foreground request accounting, diagnostics, or event sinks.
func (a *Agent) runProactiveCompactionSummary(
	ctx context.Context,
	candidate contextCompactionCandidate,
) proactiveCompactionOutcome {
	if a.cfg.ModelProvider != "openai-api" && a.cfg.ModelProvider != "gemini-api" {
		return proactiveCompactionOutcome{
			Err: fmt.Errorf("proactive compaction is not enabled for provider %q", a.cfg.ModelProvider),
		}
	}
	if a.model == nil {
		return proactiveCompactionOutcome{Err: fmt.Errorf("proactive compaction has no model client")}
	}
	if err := ctx.Err(); err != nil {
		return proactiveCompactionOutcome{Err: err}
	}
	if candidate.sessionPath == "" || candidate.cut <= 0 ||
		candidate.cut >= len(candidate.sourceMessages) ||
		strings.TrimSpace(candidate.summaryInput) == "" {
		return proactiveCompactionOutcome{Err: fmt.Errorf("proactive compaction candidate is invalid")}
	}

	summaryRequest := gemini.Request{
		SystemInstruction: gemini.Content{
			Role:  "system",
			Parts: []gemini.Part{{Text: contextSummaryInstruction}},
		},
		Contents: []gemini.Content{{
			Role:  "user",
			Parts: []gemini.Part{{Text: candidate.summaryInput}},
		}},
		GenerationConfig: map[string]any{
			"temperature":    0,
			"maxOutputTokens": compactionSummaryOutputTokens,
		},
		Tools: nil,
	}
	if modelIgnoresTemperature(a.cfg.ModelProvider, a.cfg.Model) {
		delete(summaryRequest.GenerationConfig, "temperature")
	} else if a.cfg.Temperature != nil {
		summaryRequest.GenerationConfig["temperature"] = *a.cfg.Temperature
	}

	content, err := a.model.Generate(ctx, summaryRequest)
	if err != nil {
		return proactiveCompactionOutcome{Err: fmt.Errorf("proactive summary request failed: %w", err)}
	}

	var summaryParts []string
	for _, part := range content.Parts {
		if part.FunctionCall != nil {
			return proactiveCompactionOutcome{Err: fmt.Errorf("proactive summary returned a function call")}
		}
		if strings.TrimSpace(part.Text) != "" {
			summaryParts = append(summaryParts, strings.TrimSpace(part.Text))
		}
	}
	summary := strings.TrimSpace(strings.Join(summaryParts, "\n"))
	if summary == "" {
		return proactiveCompactionOutcome{Err: fmt.Errorf("proactive summary returned no text")}
	}
	summary = redactHistoryText(summary)
	if len(summary) > 24<<10 {
		summary = truncateContextText(summary, 24<<10)
	}
	return proactiveCompactionOutcome{
		Summary: summary,
		Usage:   content.Usage.Clone(),
	}
}


// maybeScheduleProactiveCompaction prepares a summary only after the caller
// has appended a structurally complete assistant turn. It uses provider-
// reported input tokens (not byte estimates) and a known context window.
// Missing usage/window, unknown capacity, invalid cuts, and below-threshold
// requests are intentionally no-ops.
func (a *Agent) maybeScheduleProactiveCompaction(
	ctx context.Context,
	providerUsage *tokenusage.TokenUsage,
) bool {
	if ctx.Err() != nil || a.model == nil || a.history == nil ||
		(a.cfg.ModelProvider != "openai-api" && a.cfg.ModelProvider != "gemini-api") ||
		providerUsage == nil || providerUsage.InputTokens == nil {
		return false
	}
	window, _ := a.ContextWindowTokens(ctx)
	if window <= 0 {
		return false
	}
	reserve, headroom, _ := contextInputBudget(
		window, a.cfg.ContextOutputReserveTokens, a.cfg.ContextSafetyHeadroomTokens,
	)
	decision := EvaluateRequestBudget(
		int64(window),
		int64(reserve),
		int64(headroom),
		RequestTokenCount{
			Tokens: *providerUsage.InputTokens,
			Confidence: RequestTokenConfidenceExact,
			Source: "provider-reported input usage",
		},
	)
	plan := PlanProactiveCompaction(decision)
	if plan.Action == ProactiveCompactionNone {
		return false
	}

	messages, revision, epoch := a.activeContextSnapshot()
	cut := contextCompactionCut(messages)
	if cut <= 0 {
		return false
	}
	candidate, err := newContextCompactionCandidate(
		a.history.Path(), messages, cut, revision, epoch, *providerUsage.InputTokens,
	)
	if err != nil {
		return false
	}
	return a.startProactiveCompactionWorker(
		candidate, proactiveCompactionWorkerTimeout, a.runProactiveCompactionSummary,
	)
}
