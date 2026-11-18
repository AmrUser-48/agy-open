package agent

import (
	"encoding/json"
	"strings"

	"github.com/AmrUser-48/agy-open/internal/gemini"
	tokenusage "github.com/AmrUser-48/agy-open/internal/usage"
)

// RequestMetrics records serialized sizes of the provider-neutral request
// representation. It contains counts only; prompt text, tool arguments, and
// tool output are never copied into this structure.
type RequestMetrics struct {
	Turn                         int    `json:"turn"`
	RequestJSONBytes             int    `json:"requestJsonBytes"`
	SystemInstructionJSONBytes   int    `json:"systemInstructionJsonBytes"`
	ConversationJSONBytes        int    `json:"conversationJsonBytes"`
	BuiltinToolSchemaBytes       int    `json:"builtinToolSchemaBytes"`
	MCPToolSchemaBytes           int    `json:"mcpToolSchemaBytes"`
	FunctionCallJSONBytes        int    `json:"functionCallJsonBytes"`
	FunctionResponseJSONBytes    int    `json:"functionResponseJsonBytes"`
	InitialPromptBytes           int    `json:"initialPromptBytes"`
	BuiltinToolCount             int    `json:"builtinToolCount"`
	MCPToolCount                 int    `json:"mcpToolCount"`
	Complete                     bool   `json:"complete"`
	Representation               string `json:"representation"`
	ProviderPayloadBytes         int    `json:"providerPayloadBytes,omitempty"`
	ProviderPayloadKind          string `json:"providerPayloadKind,omitempty"`
	ProviderPayloadMeasured      bool   `json:"providerPayloadMeasured"`
}

const requestMetricsRepresentation = "gemini.Request JSON; provider wire payload may differ"

func jsonByteSize(value any) (int, bool) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return 0, false
	}
	return len(encoded), true
}

// measureModelRequest measures one immutable request snapshot. Component sizes
// intentionally overlap the total request size and must not be summed together.
func measureModelRequest(request gemini.Request, turn int, prompt string) RequestMetrics {
	metrics := RequestMetrics{
		Turn: turn,
		InitialPromptBytes: len(prompt),
		Complete: true,
		Representation: requestMetricsRepresentation,
	}
	var ok bool
	if metrics.RequestJSONBytes, ok = jsonByteSize(request); !ok {
		metrics.Complete = false
	}
	if metrics.SystemInstructionJSONBytes, ok = jsonByteSize(request.SystemInstruction); !ok {
		metrics.Complete = false
	}
	if metrics.ConversationJSONBytes, ok = jsonByteSize(request.Contents); !ok {
		metrics.Complete = false
	}

	for _, group := range request.Tools {
		declarations, ok := group["functionDeclarations"].([]gemini.FunctionDeclaration)
		if !ok {
			metrics.Complete = false
			continue
		}
		for _, declaration := range declarations {
			size, encoded := jsonByteSize(declaration)
			if !encoded {
				metrics.Complete = false
				continue
			}
			if strings.HasPrefix(declaration.Name, "mcp__") {
				metrics.MCPToolCount++
				metrics.MCPToolSchemaBytes += size
			} else {
				metrics.BuiltinToolCount++
				metrics.BuiltinToolSchemaBytes += size
			}
		}
	}

	for _, message := range request.Contents {
		for _, part := range message.Parts {
			if part.FunctionCall != nil {
				size, encoded := jsonByteSize(part.FunctionCall)
				if !encoded {
					metrics.Complete = false
				} else {
					metrics.FunctionCallJSONBytes += size
				}
			}
			if part.FunctionResponse != nil {
				size, encoded := jsonByteSize(part.FunctionResponse)
				if !encoded {
					metrics.Complete = false
				} else {
					metrics.FunctionResponseJSONBytes += size
				}
			}
		}
	}
	return metrics
}


// RequestSizeTotals aggregates provider-neutral serialized request sizes across
// all model requests made while handling one user prompt.
type RequestSizeTotals struct {
	Requests int `json:"requests"`
	Complete bool `json:"complete"`
	RequestJSONBytes int `json:"requestJsonBytes"`
	SystemInstructionJSONBytes int `json:"systemInstructionJsonBytes"`
	ConversationJSONBytes int `json:"conversationJsonBytes"`
	BuiltinToolSchemaBytes int `json:"builtinToolSchemaBytes"`
	MCPToolSchemaBytes int `json:"mcpToolSchemaBytes"`
	FunctionCallJSONBytes int `json:"functionCallJsonBytes"`
	FunctionResponseJSONBytes int `json:"functionResponseJsonBytes"`
	BuiltinToolCount int `json:"builtinToolCount"`
	MCPToolCount int `json:"mcpToolCount"`
}

func (totals *RequestSizeTotals) Add(metrics RequestMetrics) {
	if totals == nil {
		return
	}
	if totals.Requests == 0 {
		totals.Complete = true
	}
	totals.Requests++
	totals.Complete = totals.Complete && metrics.Complete
	totals.RequestJSONBytes += metrics.RequestJSONBytes
	totals.SystemInstructionJSONBytes += metrics.SystemInstructionJSONBytes
	totals.ConversationJSONBytes += metrics.ConversationJSONBytes
	totals.BuiltinToolSchemaBytes += metrics.BuiltinToolSchemaBytes
	totals.MCPToolSchemaBytes += metrics.MCPToolSchemaBytes
	totals.FunctionCallJSONBytes += metrics.FunctionCallJSONBytes
	totals.FunctionResponseJSONBytes += metrics.FunctionResponseJSONBytes
	totals.BuiltinToolCount += metrics.BuiltinToolCount
	totals.MCPToolCount += metrics.MCPToolCount
}

// RequestAccounting is an in-memory snapshot for one non-empty RunContext
// prompt. It stores measurements and counters only, never prompt or tool text.
type RequestAccounting struct {
	PromptBytes int `json:"promptBytes"`
	ModelAdapterCalls int `json:"modelAdapterCalls"`
	CompactionCalls int `json:"compactionCalls"`
	GenerationRequests RequestSizeTotals `json:"generationRequests"`
	CompactionRequests RequestSizeTotals `json:"compactionRequests"`
	TransportAttempts int `json:"transportAttempts"`
	TransportCallsMeasured int `json:"transportCallsMeasured"`
	TransportCallsUnmeasured int `json:"transportCallsUnmeasured"`
	ProviderPayloadBytes int `json:"providerPayloadBytes"`
	ProviderPayloadSamples int `json:"providerPayloadSamples"`
	WebSocketSessions int `json:"webSocketSessions"`
	WebSocketSessionCallsMeasured int `json:"webSocketSessionCallsMeasured"`
	ToolCallsRequested int `json:"toolCallsRequested"`
	ToolExecutionAttempts int `json:"toolExecutionAttempts"`
	ToolDenied int `json:"toolDenied"`
	ToolSucceeded int `json:"toolSucceeded"`
	ToolFailed int `json:"toolFailed"`
	ToolResultBytes int `json:"toolResultBytes"`
	FunctionResponseJSONBytes int `json:"functionResponseJsonBytes"`
	GenerationUsage tokenusage.Totals `json:"generationUsage"`
	CompactionUsage tokenusage.Totals `json:"compactionUsage"`
}

func (a *Agent) beginRequestAccounting(prompt string) {
	a.requestAccountingMu.Lock()
	a.activeRequestAccounting = &RequestAccounting{PromptBytes: len(prompt)}
	a.requestAccountingMu.Unlock()
}

func (a *Agent) finishRequestAccounting() {
	a.requestAccountingMu.Lock()
	if a.activeRequestAccounting != nil {
		a.lastRequestAccounting = *a.activeRequestAccounting
		a.activeRequestAccounting = nil
	}
	a.requestAccountingMu.Unlock()
}

// LastRequestAccounting returns counters for the most recently completed
// RunContext prompt. Its value contains no mutable pointers or transcript data.
func (a *Agent) LastRequestAccounting() RequestAccounting {
	a.requestAccountingMu.RLock()
	defer a.requestAccountingMu.RUnlock()
	return a.lastRequestAccounting
}

func (a *Agent) recordModelAdapterCall(compaction bool) {
	a.requestAccountingMu.Lock()
	if a.activeRequestAccounting != nil {
		a.activeRequestAccounting.ModelAdapterCalls++
		if compaction {
			a.activeRequestAccounting.CompactionCalls++
		}
	}
	a.requestAccountingMu.Unlock()
}

func (a *Agent) recordRequestUsage(record *tokenusage.TokenUsage, compaction bool) {
	if record == nil {
		record = &tokenusage.TokenUsage{
			Provider: a.cfg.ModelProvider,
			Model: a.cfg.Model,
			Source: "provider response (usage not reported)",
		}
	}
	a.requestAccountingMu.Lock()
	if a.activeRequestAccounting != nil {
		if compaction {
			a.activeRequestAccounting.CompactionUsage.Add(record)
		} else {
			a.activeRequestAccounting.GenerationUsage.Add(record)
		}
	}
	a.requestAccountingMu.Unlock()
}

func (a *Agent) recordToolCallsRequested(count int) {
	if count <= 0 {
		return
	}
	a.requestAccountingMu.Lock()
	if a.activeRequestAccounting != nil {
		a.activeRequestAccounting.ToolCallsRequested += count
	}
	a.requestAccountingMu.Unlock()
}

func (a *Agent) recordToolExecutionAttempt() {
	a.requestAccountingMu.Lock()
	if a.activeRequestAccounting != nil {
		a.activeRequestAccounting.ToolExecutionAttempts++
	}
	a.requestAccountingMu.Unlock()
}

func (a *Agent) recordToolExecutionResult(output string, ok, denied bool, response gemini.FunctionResponse) {
	responseBytes, measured := jsonByteSize(response)
	a.requestAccountingMu.Lock()
	if a.activeRequestAccounting != nil {
		accounting := a.activeRequestAccounting
		accounting.ToolResultBytes += len(output)
		if measured {
			accounting.FunctionResponseJSONBytes += responseBytes
		}
		if denied {
			accounting.ToolDenied++
		} else if ok {
			accounting.ToolSucceeded++
		} else {
			accounting.ToolFailed++
		}
	}
	a.requestAccountingMu.Unlock()
}

func (a *Agent) recordRequestMetricsKind(request gemini.Request, turn int, prompt string, compaction bool) {
	metrics := measureModelRequest(request, turn, prompt)
	a.requestMetricsMu.Lock()
	a.lastRequestMetrics = metrics
	a.requestMetricsMu.Unlock()

	a.requestAccountingMu.Lock()
	if a.activeRequestAccounting != nil {
		if compaction {
			a.activeRequestAccounting.CompactionRequests.Add(metrics)
		} else {
			a.activeRequestAccounting.GenerationRequests.Add(metrics)
		}
	}
	a.requestAccountingMu.Unlock()
}
