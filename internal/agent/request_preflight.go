package agent

import (
	"context"
	"fmt"

	"github.com/AmrUser-48/agy-open/internal/gemini"
)

// requestInputTokenCounter is an optional exact counter implemented only by
// provider adapters whose input-token endpoint accepts Agy's full request shape.
type requestInputTokenCounter interface {
	CountInputTokens(context.Context, gemini.Request) (int64, error)
}

// preflightRequestBudget checks the complete assembled request before provider
// generation. The first supported provider is OpenAI API-key auth. A missing
// context limit stays unknown; it is never converted into a fit decision.
func (a *Agent) preflightRequestBudget(ctx context.Context, request gemini.Request) (RequestBudgetDecision, error) {
	window, windowSource := a.ContextWindowTokens(ctx)
	reserve, headroom, _ := contextInputBudget(
		window, a.cfg.ContextOutputReserveTokens, a.cfg.ContextSafetyHeadroomTokens,
	)
	if window <= 0 {
		decision := EvaluateRequestBudget(0, int64(reserve), int64(headroom), RequestTokenCount{
			Confidence: RequestTokenConfidenceUnknown,
			Source:     "not counted: context window unknown",
		})
		decision.Reason = "selected model has no verified context-window limit (" + windowSource + ")"
		return decision, nil
	}

	counter, ok := a.model.(requestInputTokenCounter)
	if !ok {
		return RequestBudgetDecision{}, fmt.Errorf(
			"OpenAI API model client does not implement exact input-token preflight (context window %d tokens from %s)",
			window, windowSource,
		)
	}
	inputTokens, err := counter.CountInputTokens(ctx, request)
	if err != nil {
		return RequestBudgetDecision{}, fmt.Errorf(
			"OpenAI API input-token preflight failed (context window %d tokens from %s): %w",
			window, windowSource, err,
		)
	}

	decision := EvaluateRequestBudget(
		int64(window),
		int64(reserve),
		int64(headroom),
		RequestTokenCount{
			Tokens:     inputTokens,
			Confidence: RequestTokenConfidenceExact,
			Source:     "OpenAI Responses input_tokens endpoint",
		},
	)
	if decision.Status != RequestBudgetFits {
		return decision, fmt.Errorf(
			"OpenAI API request does not fit the verified context budget: %s (input %d; window %d; output reserve %d; safety headroom %d; available input %d)",
			decision.Reason,
			decision.InputTokens,
			decision.ContextWindowTokens,
			decision.OutputReserveTokens,
			decision.SafetyHeadroomTokens,
			decision.AvailableInputTokens,
		)
	}
	return decision, nil
}
