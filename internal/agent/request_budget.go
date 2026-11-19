package agent

// RequestTokenConfidence describes the provenance of a pre-dispatch input
// token count. Unknown is never treated as an unlimited or zero-cost request.
type RequestTokenConfidence string

const (
	RequestTokenConfidenceUnknown     RequestTokenConfidence = "unknown"
	RequestTokenConfidenceExact       RequestTokenConfidence = "exact"
	RequestTokenConfidenceApproximate RequestTokenConfidence = "approximate"
)

// RequestTokenCount is a provider-reported count or a calibrated estimate for
// the complete assembled request. Source is a short provenance label, not data.
type RequestTokenCount struct {
	Tokens     int64
	Confidence RequestTokenConfidence
	Source     string
}

// RequestBudgetStatus is the result of comparing input tokens with the
// context capacity left after output reserve and safety headroom.
type RequestBudgetStatus string

const (
	RequestBudgetUnknown   RequestBudgetStatus = "unknown"
	RequestBudgetFits      RequestBudgetStatus = "fits"
	RequestBudgetOver      RequestBudgetStatus = "over_budget"
	RequestBudgetInvalid   RequestBudgetStatus = "invalid"
)

// RequestBudgetDecision is a deterministic, side-effect-free budget result.
// AvailableInputTokens excludes both output reserve and safety headroom.
type RequestBudgetDecision struct {
	Status                RequestBudgetStatus
	Confidence            RequestTokenConfidence
	ContextWindowTokens   int64
	OutputReserveTokens   int64
	SafetyHeadroomTokens  int64
	InputTokens           int64
	AvailableInputTokens  int64
	RemainingInputTokens  int64
	InputSource           string
	Reason                string
}

// EvaluateRequestBudget evaluates one complete request count. It deliberately
// does not estimate tokens, fetch model metadata, mutate history, or compact
// messages. A caller must supply verified window and count provenance.
func EvaluateRequestBudget(
	contextWindowTokens int64,
	outputReserveTokens int64,
	safetyHeadroomTokens int64,
	input RequestTokenCount,
) RequestBudgetDecision {
	decision := RequestBudgetDecision{
		Status:               RequestBudgetInvalid,
		Confidence:           input.Confidence,
		ContextWindowTokens:  contextWindowTokens,
		OutputReserveTokens:  outputReserveTokens,
		SafetyHeadroomTokens: safetyHeadroomTokens,
		InputTokens:           input.Tokens,
		InputSource:           input.Source,
	}

	switch input.Confidence {
	case "", RequestTokenConfidenceUnknown:
		decision.Confidence = RequestTokenConfidenceUnknown
	case RequestTokenConfidenceExact, RequestTokenConfidenceApproximate:
		if input.Tokens < 0 {
			decision.Reason = "input token count cannot be negative"
			return decision
		}
	default:
		decision.Reason = "unsupported input token confidence"
		return decision
	}
	if contextWindowTokens < 0 {
		decision.Reason = "context window cannot be negative"
		return decision
	}
	if outputReserveTokens < 0 {
		decision.Reason = "output reserve cannot be negative"
		return decision
	}
	if safetyHeadroomTokens < 0 {
		decision.Reason = "safety headroom cannot be negative"
		return decision
	}
	if contextWindowTokens == 0 {
		decision.Status = RequestBudgetUnknown
		decision.Reason = "context window is unknown"
		return decision
	}
	if outputReserveTokens >= contextWindowTokens {
		decision.Reason = "output reserve must be smaller than the context window"
		return decision
	}

	inputCapacity := contextWindowTokens - outputReserveTokens
	if safetyHeadroomTokens > inputCapacity {
		decision.Reason = "safety headroom exceeds the capacity left after output reserve"
		return decision
	}
	decision.AvailableInputTokens = inputCapacity - safetyHeadroomTokens

	if decision.Confidence == RequestTokenConfidenceUnknown {
		decision.Status = RequestBudgetUnknown
		decision.Reason = "input token count is unknown"
		return decision
	}

	decision.Status = RequestBudgetFits
	if input.Tokens > decision.AvailableInputTokens {
		decision.Status = RequestBudgetOver
		decision.Reason = "input tokens exceed the available request budget"
		return decision
	}
	decision.RemainingInputTokens = decision.AvailableInputTokens - input.Tokens
	return decision
}


// contextSafetyHeadroom applies an automatic margin when no explicit override
// is configured. The default is 1% of the context window, clamped to 128–1024
// tokens and to one quarter of capacity remaining after the output reserve.
// Explicit values are returned unchanged; config validation rejects invalid ones.
func contextSafetyHeadroom(configured, contextWindow, outputReserve int) int {
	if configured != 0 {
		return configured
	}
	if contextWindow <= 0 {
		return 0
	}
	available := contextWindow - outputReserve
	if available <= 0 {
		return 0
	}
	headroom := contextWindow / 100
	if headroom < 128 {
		headroom = 128
	}
	if headroom > 1024 {
		headroom = 1024
	}
	if limit := available / 4; headroom > limit {
		headroom = limit
	}
	return headroom
}


// contextInputBudget is the shared capacity calculation for reporting,
// preflight, and automatic compaction. Unknown context windows stay zero.
func contextInputBudget(window, configuredOutputReserve, configuredSafetyHeadroom int) (outputReserve, safetyHeadroom, availableInput int) {
	if window <= 0 {
		return 0, 0, 0
	}
	outputReserve = contextOutputReserve(configuredOutputReserve, window)
	safetyHeadroom = contextSafetyHeadroom(configuredSafetyHeadroom, window, outputReserve)
	availableInput = window - outputReserve - safetyHeadroom
	if availableInput < 0 {
		availableInput = 0
	}
	return outputReserve, safetyHeadroom, availableInput
}


// RequestPreflightAction determines whether a budget decision may dispatch,
// compact, or block. Approximate counts can request compaction but never block.
type RequestPreflightAction string

const (
	RequestPreflightDispatch       RequestPreflightAction = "dispatch"
	RequestPreflightWarnAndDispatch RequestPreflightAction = "warn_and_dispatch"
	RequestPreflightCompact        RequestPreflightAction = "compact"
	RequestPreflightReject         RequestPreflightAction = "reject"
)

// RequestPreflightPlan is a pure policy result for one assembled request.
type RequestPreflightPlan struct {
	Action  RequestPreflightAction
	Warning string
	Reason  string
}

// PlanRequestPreflight enforces a hard gate only for exact counts. Approximate
// estimates may trigger one safe compaction attempt; afterward the request is
// dispatched with a warning so an estimate cannot falsely reject valid input.
func PlanRequestPreflight(decision RequestBudgetDecision, compactionAttempted, safeCompactionCut bool) RequestPreflightPlan {
	switch decision.Status {
	case RequestBudgetInvalid:
		return RequestPreflightPlan{
			Action: RequestPreflightReject,
			Reason: decision.Reason,
		}

	case RequestBudgetFits:
		if decision.Confidence == RequestTokenConfidenceExact {
			return RequestPreflightPlan{Action: RequestPreflightDispatch}
		}
		return RequestPreflightPlan{
			Action:  RequestPreflightWarnAndDispatch,
			Warning: "input token count is approximate; request fit is not guaranteed",
		}

	case RequestBudgetOver:
		if decision.Confidence == RequestTokenConfidenceExact {
			if !compactionAttempted && safeCompactionCut {
				return RequestPreflightPlan{Action: RequestPreflightCompact}
			}
			reason := decision.Reason
			if compactionAttempted {
				reason = "exact input count remains over budget after the allowed compaction attempt"
			} else if !safeCompactionCut {
				reason = "exact input count exceeds budget and no safe compaction boundary is available"
			}
			return RequestPreflightPlan{
				Action: RequestPreflightReject,
				Reason: reason,
			}
		}
		if decision.Confidence == RequestTokenConfidenceApproximate &&
			!compactionAttempted && safeCompactionCut {
			return RequestPreflightPlan{Action: RequestPreflightCompact}
		}
		reason := "approximate input count is over budget; continuing without an exact fit guarantee"
		if compactionAttempted {
			reason = "approximate input count remains over budget after compaction; continuing without an exact fit guarantee"
		} else if !safeCompactionCut {
			reason = "approximate input count is over budget but no safe compaction boundary exists; continuing without an exact fit guarantee"
		}
		return RequestPreflightPlan{
			Action:  RequestPreflightWarnAndDispatch,
			Warning: reason,
		}

	case RequestBudgetUnknown:
		reason := decision.Reason
		if reason == "" {
			reason = "request budget is unknown"
		}
		return RequestPreflightPlan{
			Action:  RequestPreflightWarnAndDispatch,
			Warning: reason + "; continuing without a fit guarantee",
		}

	default:
		return RequestPreflightPlan{
			Action:  RequestPreflightWarnAndDispatch,
			Warning: "unrecognized request budget status; continuing without a fit guarantee",
		}
	}
}


// ProactiveCompactionAction is a non-blocking preparation recommendation.
type ProactiveCompactionAction string

const (
	ProactiveCompactionNone          ProactiveCompactionAction = "none"
	ProactiveCompactionPrepare       ProactiveCompactionAction = "prepare"
	ProactiveCompactionSafetyBarrier ProactiveCompactionAction = "safety_barrier"
)

// ProactiveCompactionPlan applies early-preparation and safety-barrier targets
// to a measured or estimated complete-request input count. It is advisory:
// it never rejects a request or changes active/canonical history.
type ProactiveCompactionPlan struct {
	Action                ProactiveCompactionAction
	Confidence            RequestTokenConfidence
	EarlyThresholdTokens  int64
	SafetyThresholdTokens int64
	Reason                string
}

// PlanProactiveCompaction uses 80%/95% targets for exact counts. Approximate
// estimates use earlier 75%/90% targets to reduce the risk of undercounting.
// AvailableInputTokens already excludes fixed request overhead, output reserve,
// safety headroom, and any provider/model-specific context limits.
func PlanProactiveCompaction(decision RequestBudgetDecision) ProactiveCompactionPlan {
	plan := ProactiveCompactionPlan{
		Action:     ProactiveCompactionNone,
		Confidence: decision.Confidence,
		Reason:     "no proactive compaction required",
	}
	if decision.Status != RequestBudgetFits && decision.Status != RequestBudgetOver {
		plan.Reason = "proactive compaction skipped because request budget is unknown or invalid"
		return plan
	}
	if decision.AvailableInputTokens <= 0 || decision.InputTokens < 0 {
		plan.Reason = "proactive compaction skipped because usable input capacity or token count is invalid"
		return plan
	}

	var earlyPercent, safetyPercent int64
	switch decision.Confidence {
	case RequestTokenConfidenceExact:
		earlyPercent, safetyPercent = 80, 95
	case RequestTokenConfidenceApproximate:
		earlyPercent, safetyPercent = 75, 90
	default:
		plan.Reason = "proactive compaction skipped because token-count confidence is unknown"
		return plan
	}

	plan.EarlyThresholdTokens = ceilPercentOfBudget(decision.AvailableInputTokens, earlyPercent)
	plan.SafetyThresholdTokens = ceilPercentOfBudget(decision.AvailableInputTokens, safetyPercent)
	if decision.InputTokens >= plan.SafetyThresholdTokens {
		plan.Action = ProactiveCompactionSafetyBarrier
		plan.Reason = "input usage reached the safety-barrier threshold"
		return plan
	}
	if decision.InputTokens >= plan.EarlyThresholdTokens {
		plan.Action = ProactiveCompactionPrepare
		plan.Reason = "input usage reached the early-compaction threshold"
		return plan
	}
	return plan
}

// ceilPercentOfBudget computes ceil(budget * percent / 100) without a
// potentially overflowing budget*percent intermediate.
func ceilPercentOfBudget(budget, percent int64) int64 {
	if budget <= 0 || percent <= 0 {
		return 0
	}
	whole := (budget / 100) * percent
	remainder := ((budget % 100) * percent) / 100
	if ((budget%100)*percent)%100 != 0 {
		remainder++
	}
	return whole + remainder
}
