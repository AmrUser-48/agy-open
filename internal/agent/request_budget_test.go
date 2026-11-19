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
