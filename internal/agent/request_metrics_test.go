package agent

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/AmrUser-48/agy-open/internal/gemini"
)

func TestMeasureModelRequestBreakdown(t *testing.T) {
	builtin := gemini.FunctionDeclaration{
		Name: "shell",
		Description: "Run an approved command.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{"command": map[string]any{"type": "string"}},
		},
	}
	mcpTool := gemini.FunctionDeclaration{
		Name: "mcp__git__status",
		Description: "Read repository status.",
		Parameters: map[string]any{"type": "object", "properties": map[string]any{}},
	}
	call := gemini.FunctionCall{ID: "call-1", Name: "mcp__git__status", Args: map[string]any{"path": "."}}
	response := gemini.FunctionResponse{
		ID: "call-1",
		Name: "mcp__git__status",
		Response: map[string]any{"output": "private tool output", "ok": true},
	}
	request := gemini.Request{
		SystemInstruction: gemini.Content{Role: "system", Parts: []gemini.Part{{Text: "system rules"}}},
		Contents: []gemini.Content{
			{Role: "user", Parts: []gemini.Part{{Text: "private initial prompt"}}},
			{Role: "model", Parts: []gemini.Part{{FunctionCall: &call}}},
			{Role: "user", Parts: []gemini.Part{{FunctionResponse: &response}}},
		},
		Tools: []map[string]any{
			{"functionDeclarations": []gemini.FunctionDeclaration{builtin}},
			{"functionDeclarations": []gemini.FunctionDeclaration{mcpTool}},
		},
	}
	got := measureModelRequest(request, 7, "private initial prompt")

	requestJSON, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	if got.RequestJSONBytes != len(requestJSON) {
		t.Fatalf("RequestJSONBytes = %d, want exact serialized request size %d", got.RequestJSONBytes, len(requestJSON))
	}
	if !got.Complete {
		t.Fatal("metrics unexpectedly incomplete")
	}
	if got.Turn != 7 || got.InitialPromptBytes != len("private initial prompt") {
		t.Fatalf("turn/prompt metrics = %#v", got)
	}
	if got.BuiltinToolCount != 1 || got.MCPToolCount != 1 {
		t.Fatalf("tool counts = built-in %d, MCP %d; want 1 each", got.BuiltinToolCount, got.MCPToolCount)
	}
	builtinJSON, _ := json.Marshal(builtin)
	mcpJSON, _ := json.Marshal(mcpTool)
	if got.BuiltinToolSchemaBytes != len(builtinJSON) || got.MCPToolSchemaBytes != len(mcpJSON) {
		t.Fatalf("schema bytes = built-in %d, MCP %d", got.BuiltinToolSchemaBytes, got.MCPToolSchemaBytes)
	}
	callJSON, _ := json.Marshal(call)
	responseJSON, _ := json.Marshal(response)
	if got.FunctionCallJSONBytes != len(callJSON) || got.FunctionResponseJSONBytes != len(responseJSON) {
		t.Fatalf("call/result bytes = %d/%d, want %d/%d", got.FunctionCallJSONBytes, got.FunctionResponseJSONBytes, len(callJSON), len(responseJSON))
	}
	if got.SystemInstructionJSONBytes == 0 || got.ConversationJSONBytes == 0 {
		t.Fatalf("missing system/history measurements: %#v", got)
	}

	metricsJSON, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"private initial prompt", "private tool output", "system rules"} {
		if strings.Contains(string(metricsJSON), secret) {
			t.Fatalf("diagnostics leaked content %q: %s", secret, metricsJSON)
		}
	}
}
