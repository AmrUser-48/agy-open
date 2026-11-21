package agent

import "testing"

func TestEvaluateRequestBudget(t *testing.T) {
	tests := []struct {
		name       string
		window     int64
		output     int64
		headroom   int64
		input      RequestTokenCount
		status     RequestBudgetStatus
		confidence RequestTokenConfidence
		available  int64
		remaining  int64
		reason     string
	}{
		{
			name: "exact count fits with no remaining capacity",
			window: 1000, output: 200, headroom: 50,
			input: RequestTokenCount{Tokens: 750, Confidence: RequestTokenConfidenceExact, Source: "provider"},
			status: RequestBudgetFits, confidence: RequestTokenConfidenceExact,
			available: 750, remaining: 0,
		},
		{
			name: "exact count exceeds capacity",
			window: 1000, output: 200, headroom: 50,
			input: RequestTokenCount{Tokens: 751, Confidence: RequestTokenConfidenceExact},
			status: RequestBudgetOver, confidence: RequestTokenConfidenceExact,
			available: 750, reason: "input tokens exceed the available request budget",
		},
		{
			name: "approximate count remains explicitly approximate",
			window: 1000, output: 200, headroom: 50,
			input: RequestTokenCount{Tokens: 650, Confidence: RequestTokenConfidenceApproximate, Source: "calibrated estimator"},
			status: RequestBudgetFits, confidence: RequestTokenConfidenceApproximate,
			available: 750, remaining: 100,
		},
		{
			name: "approximate count includes safety headroom",
			window: 1000, output: 200, headroom: 101,
			input: RequestTokenCount{Tokens: 700, Confidence: RequestTokenConfidenceApproximate},
			status: RequestBudgetOver, confidence: RequestTokenConfidenceApproximate,
			available: 699, reason: "input tokens exceed the available request budget",
		},
		{
			name: "unknown input count is not assumed to fit",
			window: 1000, output: 200, headroom: 50,
			input: RequestTokenCount{Confidence: RequestTokenConfidenceUnknown},
			status: RequestBudgetUnknown, confidence: RequestTokenConfidenceUnknown,
			available: 750, reason: "input token count is unknown",
		},
		{
			name: "zero context window means unknown",
			input: RequestTokenCount{Tokens: 1, Confidence: RequestTokenConfidenceExact},
			status: RequestBudgetUnknown, confidence: RequestTokenConfidenceExact,
			reason: "context window is unknown",
		},
		{
			name: "zero input fits zero remaining capacity",
			window: 1000, output: 500, headroom: 500,
			input: RequestTokenCount{Tokens: 0, Confidence: RequestTokenConfidenceExact},
			status: RequestBudgetFits, confidence: RequestTokenConfidenceExact,
			available: 0, remaining: 0,
		},
		{
			name: "negative input count is invalid",
			window: 1000,
			input: RequestTokenCount{Tokens: -1, Confidence: RequestTokenConfidenceExact},
			status: RequestBudgetInvalid, confidence: RequestTokenConfidenceExact,
			reason: "input token count cannot be negative",
		},
		{
			name: "negative context window is invalid",
			window: -1,
			input: RequestTokenCount{Confidence: RequestTokenConfidenceUnknown},
			status: RequestBudgetInvalid, confidence: RequestTokenConfidenceUnknown,
			reason: "context window cannot be negative",
		},
		{
			name: "negative output reserve is invalid",
			window: 1000, output: -1,
			input: RequestTokenCount{Confidence: RequestTokenConfidenceUnknown},
			status: RequestBudgetInvalid, confidence: RequestTokenConfidenceUnknown,
			reason: "output reserve cannot be negative",
		},
		{
			name: "negative safety headroom is invalid",
			window: 1000, headroom: -1,
			input: RequestTokenCount{Confidence: RequestTokenConfidenceUnknown},
			status: RequestBudgetInvalid, confidence: RequestTokenConfidenceUnknown,
			reason: "safety headroom cannot be negative",
		},
		{
			name: "output reserve cannot consume whole window",
			window: 1000, output: 1000,
			input: RequestTokenCount{Tokens: 0, Confidence: RequestTokenConfidenceExact},
			status: RequestBudgetInvalid, confidence: RequestTokenConfidenceExact,
			reason: "output reserve must be smaller than the context window",
		},
		{
			name: "safety headroom cannot exceed post-reserve capacity",
			window: 1000, output: 200, headroom: 801,
			input: RequestTokenCount{Tokens: 0, Confidence: RequestTokenConfidenceExact},
			status: RequestBudgetInvalid, confidence: RequestTokenConfidenceExact,
			reason: "safety headroom exceeds the capacity left after output reserve",
		},
		{
			name: "unrecognized confidence is invalid",
			window: 1000,
			input: RequestTokenCount{Tokens: 10, Confidence: "serialized-bytes"},
			status: RequestBudgetInvalid, confidence: "serialized-bytes",
			reason: "unsupported input token confidence",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := EvaluateRequestBudget(test.window, test.output, test.headroom, test.input)
			if got.Status != test.status {
				t.Fatalf("status = %q, want %q; reason: %s", got.Status, test.status, got.Reason)
			}
			if got.Confidence != test.confidence {
				t.Fatalf("confidence = %q, want %q", got.Confidence, test.confidence)
			}
			if got.AvailableInputTokens != test.available {
				t.Fatalf("available = %d, want %d", got.AvailableInputTokens, test.available)
			}
			if got.RemainingInputTokens != test.remaining {
				t.Fatalf("remaining = %d, want %d", got.RemainingInputTokens, test.remaining)
			}
			if got.Reason != test.reason {
				t.Fatalf("reason = %q, want %q", got.Reason, test.reason)
			}
		})
	}
}


func TestContextSafetyHeadroomAutomaticAndOverride(t *testing.T) {
	tests := []struct {
		name          string
		configured    int
		window        int
		outputReserve int
		want          int
	}{
		{name: "large context capped at 1024", window: 128000, outputReserve: 16000, want: 1024},
		{name: "small percentage uses 128 minimum", window: 8192, outputReserve: 1024, want: 128},
		{name: "small context stays within capacity", window: 512, outputReserve: 128, want: 96},
		{name: "no input capacity", window: 1024, outputReserve: 1024, want: 0},
		{name: "explicit override is preserved", configured: 512, window: 8192, outputReserve: 1024, want: 512},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := contextSafetyHeadroom(test.configured, test.window, test.outputReserve)
			if got != test.want {
				t.Fatalf("contextSafetyHeadroom(%d, %d, %d) = %d, want %d",
					test.configured, test.window, test.outputReserve, got, test.want)
			}
		})
	}
}


func TestContextInputBudgetSharesReserveAndHeadroomCalculation(t *testing.T) {
	tests := []struct {
		name       string
		window     int
		output     int
		headroom   int
		wantOutput int
		wantSafety int
		wantInput  int
	}{
		{name: "automatic safety headroom", window: 8192, output: 1024, wantOutput: 1024, wantSafety: 128, wantInput: 7040},
		{name: "explicit safety headroom", window: 8192, output: 1024, headroom: 256, wantOutput: 1024, wantSafety: 256, wantInput: 6912},
		{name: "unknown context stays unknown", window: 0, output: 1024, headroom: 256},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			output, safety, input := contextInputBudget(test.window, test.output, test.headroom)
			if output != test.wantOutput || safety != test.wantSafety || input != test.wantInput {
				t.Fatalf("contextInputBudget(%d, %d, %d) = %d/%d/%d, want %d/%d/%d",
					test.window, test.output, test.headroom,
					output, safety, input,
					test.wantOutput, test.wantSafety, test.wantInput)
			}
		})
	}
}


func TestPlanRequestPreflightConfidencePolicy(t *testing.T) {
	tests := []struct {
		name               string
		decision           RequestBudgetDecision
		compactionAttempted bool
		safeCut            bool
		wantAction         RequestPreflightAction
		wantWarning        bool
		wantReason         string
	}{
		{
			name: "exact fit dispatches",
			decision: RequestBudgetDecision{Status: RequestBudgetFits, Confidence: RequestTokenConfidenceExact},
			wantAction: RequestPreflightDispatch,
		},
		{
			name: "approximate fit dispatches without a routine warning",
			decision: RequestBudgetDecision{Status: RequestBudgetFits, Confidence: RequestTokenConfidenceApproximate},
			wantAction: RequestPreflightDispatch,
		},
		{
			name: "exact over budget compacts at a safe cut",
			decision: RequestBudgetDecision{Status: RequestBudgetOver, Confidence: RequestTokenConfidenceExact},
			safeCut: true, wantAction: RequestPreflightCompact,
		},
		{
			name: "exact over budget rejects without a safe cut",
			decision: RequestBudgetDecision{Status: RequestBudgetOver, Confidence: RequestTokenConfidenceExact, Reason: "over"},
			wantAction: RequestPreflightReject,
			wantReason: "exact input count exceeds budget and no safe compaction boundary is available",
		},
		{
			name: "exact over budget rejects after one compaction",
			decision: RequestBudgetDecision{Status: RequestBudgetOver, Confidence: RequestTokenConfidenceExact},
			compactionAttempted: true, safeCut: true, wantAction: RequestPreflightReject,
			wantReason: "exact input count remains over budget after the allowed compaction attempt",
		},
		{
			name: "approximate over budget compacts at a safe cut",
			decision: RequestBudgetDecision{Status: RequestBudgetOver, Confidence: RequestTokenConfidenceApproximate},
			safeCut: true, wantAction: RequestPreflightCompact,
		},
		{
			name: "approximate over budget warns when no safe cut exists",
			decision: RequestBudgetDecision{Status: RequestBudgetOver, Confidence: RequestTokenConfidenceApproximate},
			wantAction: RequestPreflightWarnAndDispatch, wantWarning: true,
		},
		{
			name: "approximate over budget warns after compaction",
			decision: RequestBudgetDecision{Status: RequestBudgetOver, Confidence: RequestTokenConfidenceApproximate},
			compactionAttempted: true, safeCut: true,
			wantAction: RequestPreflightWarnAndDispatch, wantWarning: true,
		},
		{
			name: "unknown count warns and dispatches",
			decision: RequestBudgetDecision{Status: RequestBudgetUnknown, Confidence: RequestTokenConfidenceUnknown, Reason: "count endpoint unavailable"},
			wantAction: RequestPreflightWarnAndDispatch, wantWarning: true,
		},
		{
			name: "invalid budget rejects",
			decision: RequestBudgetDecision{Status: RequestBudgetInvalid, Confidence: RequestTokenConfidenceExact, Reason: "invalid reserve"},
			wantAction: RequestPreflightReject, wantReason: "invalid reserve",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := PlanRequestPreflight(test.decision, test.compactionAttempted, test.safeCut)
			if got.Action != test.wantAction {
				t.Fatalf("action = %q, want %q; warning=%q reason=%q",
					got.Action, test.wantAction, got.Warning, got.Reason)
			}
			if (got.Warning != "") != test.wantWarning {
				t.Fatalf("warning = %q, wantWarning=%t", got.Warning, test.wantWarning)
			}
			if got.Reason != test.wantReason {
				t.Fatalf("reason = %q, want %q", got.Reason, test.wantReason)
			}
			if test.decision.Confidence == RequestTokenConfidenceApproximate &&
				got.Action == RequestPreflightReject {
				t.Fatal("approximate input-token count must never hard-reject a request")
			}
		})
	}
}


func TestPlanProactiveCompactionConfidenceAdjustedThresholds(t *testing.T) {
	tests := []struct {
		name       string
		status     RequestBudgetStatus
		confidence RequestTokenConfidence
		input      int64
		wantAction ProactiveCompactionAction
		wantEarly  int64
		wantSafety int64
	}{
		{name: "exact below early threshold", confidence: RequestTokenConfidenceExact, input: 7999, wantAction: ProactiveCompactionNone, wantEarly: 8000, wantSafety: 9500},
		{name: "exact early threshold", confidence: RequestTokenConfidenceExact, input: 8000, wantAction: ProactiveCompactionPrepare, wantEarly: 8000, wantSafety: 9500},
		{name: "exact between thresholds", confidence: RequestTokenConfidenceExact, input: 9499, wantAction: ProactiveCompactionPrepare, wantEarly: 8000, wantSafety: 9500},
		{name: "exact safety barrier", confidence: RequestTokenConfidenceExact, input: 9500, wantAction: ProactiveCompactionSafetyBarrier, wantEarly: 8000, wantSafety: 9500},
		{name: "approximate below early threshold", confidence: RequestTokenConfidenceApproximate, input: 7499, wantAction: ProactiveCompactionNone, wantEarly: 7500, wantSafety: 9000},
		{name: "approximate early threshold", confidence: RequestTokenConfidenceApproximate, input: 7500, wantAction: ProactiveCompactionPrepare, wantEarly: 7500, wantSafety: 9000},
		{name: "approximate between thresholds", confidence: RequestTokenConfidenceApproximate, input: 8999, wantAction: ProactiveCompactionPrepare, wantEarly: 7500, wantSafety: 9000},
		{name: "approximate safety barrier", confidence: RequestTokenConfidenceApproximate, input: 9000, wantAction: ProactiveCompactionSafetyBarrier, wantEarly: 7500, wantSafety: 9000},
		{name: "over-budget exact count reaches barrier", status: RequestBudgetOver, confidence: RequestTokenConfidenceExact, input: 11000, wantAction: ProactiveCompactionSafetyBarrier, wantEarly: 8000, wantSafety: 9500},
		{name: "over-budget estimate reaches advisory barrier", status: RequestBudgetOver, confidence: RequestTokenConfidenceApproximate, input: 11000, wantAction: ProactiveCompactionSafetyBarrier, wantEarly: 7500, wantSafety: 9000},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			status := test.status
			if status == "" {
				status = RequestBudgetFits
			}
			got := PlanProactiveCompaction(RequestBudgetDecision{
				Status: status, Confidence: test.confidence,
				AvailableInputTokens: 10000, InputTokens: test.input,
			})
			if got.Action != test.wantAction || got.EarlyThresholdTokens != test.wantEarly ||
				got.SafetyThresholdTokens != test.wantSafety {
				t.Fatalf("plan = %#v; want action %q thresholds %d/%d",
					got, test.wantAction, test.wantEarly, test.wantSafety)
			}
		})
	}
}

func TestPlanProactiveCompactionSkipsUnknownAndInvalidBudgets(t *testing.T) {
	tests := []struct {
		name     string
		decision RequestBudgetDecision
	}{
		{
			name: "unknown status",
			decision: RequestBudgetDecision{Status: RequestBudgetUnknown, Confidence: RequestTokenConfidenceExact, AvailableInputTokens: 10000, InputTokens: 9999},
		},
		{
			name: "invalid status",
			decision: RequestBudgetDecision{Status: RequestBudgetInvalid, Confidence: RequestTokenConfidenceExact, AvailableInputTokens: 10000, InputTokens: 9999},
		},
		{
			name: "unknown confidence",
			decision: RequestBudgetDecision{Status: RequestBudgetFits, Confidence: RequestTokenConfidenceUnknown, AvailableInputTokens: 10000, InputTokens: 9999},
		},
		{
			name: "missing available budget",
			decision: RequestBudgetDecision{Status: RequestBudgetFits, Confidence: RequestTokenConfidenceExact, InputTokens: 9999},
		},
		{
			name: "negative token count",
			decision: RequestBudgetDecision{Status: RequestBudgetFits, Confidence: RequestTokenConfidenceExact, AvailableInputTokens: 10000, InputTokens: -1},
		},
		{
			name: "unsupported confidence",
			decision: RequestBudgetDecision{Status: RequestBudgetFits, Confidence: "serialized-bytes", AvailableInputTokens: 10000, InputTokens: 9999},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := PlanProactiveCompaction(test.decision)
			if got.Action != ProactiveCompactionNone {
				t.Fatalf("invalid/unknown budget requested compaction: %#v", got)
			}
		})
	}
}

func TestCeilPercentOfBudgetHandlesSmallAndLargeCapacities(t *testing.T) {
	tests := []struct {
		budget  int64
		percent int64
		want    int64
	}{
		{budget: 8191, percent: 80, want: 6553},
		{budget: 101, percent: 95, want: 96},
		{budget: 1, percent: 80, want: 1},
		{budget: 0, percent: 95, want: 0},
		{budget: int64(^uint64(0) >> 1), percent: 95, want: 8762203435012037017},
	}
	for _, test := range tests {
		if got := ceilPercentOfBudget(test.budget, test.percent); got != test.want {
			t.Fatalf("ceilPercentOfBudget(%d, %d) = %d, want %d",
				test.budget, test.percent, got, test.want)
		}
	}
}
