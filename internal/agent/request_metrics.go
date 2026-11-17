package agent

import (
	"encoding/json"
	"strings"

	"github.com/AmrUser-48/agy-open/internal/gemini"
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
