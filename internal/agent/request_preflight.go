package agent

import (
	"context"
	"fmt"
	"time"

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


// modelCountTokensSupport reads and caches the selected model's advertised
// Developer API countTokens capability. It is separate from the configured
// context-window override: an override does not prove endpoint support.
func (a *Agent) modelCountTokensSupport(ctx context.Context) (bool, string) {
	modelName := a.model.ModelName()
	a.contextWindowMu.RLock()
	if a.contextCountTokensChecked && a.contextCountTokensModel == modelName {
		supported := a.contextCountTokensSupported
		a.contextWindowMu.RUnlock()
		if supported {
			return true, "model catalogue"
		}
		return false, "selected model catalogue does not advertise countTokens"
	}
	a.contextWindowMu.RUnlock()

	lookupCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	options, err := a.model.ListModelOptions(lookupCtx)
	if err != nil {
		a.contextWindowMu.Lock()
		a.contextCountTokensModel = modelName
		a.contextCountTokensSupported = false
		a.contextCountTokensChecked = true
		a.contextWindowMu.Unlock()
		return false, "Gemini model capability lookup failed: " + err.Error()
	}

	a.cacheModelContextWindow(options)
	found := false
	supported := false
	for _, option := range options {
		if sameModelID(option.ID, modelName) {
			found = true
			supported = option.CountTokensSupported
			break
		}
	}

	a.contextWindowMu.Lock()
	a.contextCountTokensModel = modelName
	a.contextCountTokensSupported = found && supported
	a.contextCountTokensChecked = true
	a.contextWindowMu.Unlock()

	if !found {
		return false, "selected model is missing from the Gemini model catalogue"
	}
	if !supported {
		return false, "selected model catalogue does not advertise countTokens"
	}
	return true, "model catalogue"
}


// requestInputTokenEstimator is an explicitly approximate token counter. It
// must not be used for exact admission decisions.
type requestInputTokenEstimator interface {
	EstimateInputTokens(context.Context, gemini.Request) (int64, error)
}

// preflightGeminiAPIRequest uses Gemini Developer API countTokens only when the
// selected model advertises support and a context window is known. Count
// failures remain unknown and must never block generation.
func (a *Agent) preflightGeminiAPIRequest(ctx context.Context, request gemini.Request) (RequestBudgetDecision, string) {
	unknown := func(reason string) (RequestBudgetDecision, string) {
		decision := EvaluateRequestBudget(0, 0, 0, RequestTokenCount{
			Confidence: RequestTokenConfidenceUnknown,
			Source:     "Gemini Developer API countTokens unavailable",
		})
		decision.Reason = reason
		return decision, reason
	}

	supported, capabilitySource := a.modelCountTokensSupport(ctx)
	if !supported {
		return unknown(capabilitySource)
	}
	window, windowSource := a.ContextWindowTokens(ctx)
	if window <= 0 {
		return unknown("selected Gemini API model has no verified context-window limit")
	}
	estimator, ok := a.model.(requestInputTokenEstimator)
	if !ok {
		return unknown("selected Gemini API client does not implement countTokens")
	}
	inputTokens, err := estimator.EstimateInputTokens(ctx, request)
	if err != nil {
		return unknown("Gemini countTokens failed: " + err.Error())
	}

	reserve, headroom, _ := contextInputBudget(
		window, a.cfg.ContextOutputReserveTokens, a.cfg.ContextSafetyHeadroomTokens,
	)
	decision := EvaluateRequestBudget(
		int64(window),
		int64(reserve),
		int64(headroom),
		RequestTokenCount{
			Tokens:     inputTokens,
			Confidence: RequestTokenConfidenceApproximate,
			Source:     "Gemini Developer API countTokens (approximate; context from " + windowSource + ")",
		},
	)
	return decision, ""
}
