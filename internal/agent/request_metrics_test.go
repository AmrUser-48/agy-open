package agent

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/AmrUser-48/agy-open/internal/config"
	"github.com/AmrUser-48/agy-open/internal/gemini"
	tokenusage "github.com/AmrUser-48/agy-open/internal/usage"
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


func TestRequestAccountingAggregatesGenerationCompactionAndTools(t *testing.T) {
	agent := &Agent{cfg: config.Config{ModelProvider: "gemini-api", Model: "gemini-3.8-flash"}}
	prompt := "hello Ω"
	agent.beginRequestAccounting(prompt)

	generation := gemini.Request{
		SystemInstruction: gemini.Content{Role: "system", Parts: []gemini.Part{{Text: "system"}}},
		Contents: []gemini.Content{{Role: "user", Parts: []gemini.Part{{Text: prompt}}}},
	}
	agent.recordRequestMetrics(generation, 1, prompt)
	agent.recordModelAdapterCall(false)
	generationDiagnostics := &gemini.RequestDiagnostics{}
	generationDiagnostics.MeasureTransportAttempts()
	generationDiagnostics.RecordTransportAttempt()
	generationDiagnostics.RecordTransportAttempt()
	agent.updateProviderPayloadMetrics(generationDiagnostics)

	zero := int64(0)
	seven := int64(7)
	agent.recordRequestUsage(&tokenusage.TokenUsage{
		Provider: "gemini-api", Model: "gemini-3.8-flash",
		InputTokens: &zero, OutputTokens: &seven,
	}, false)

	compaction := gemini.Request{
		SystemInstruction: gemini.Content{Role: "system", Parts: []gemini.Part{{Text: "summary rules"}}},
		Contents: []gemini.Content{{Role: "user", Parts: []gemini.Part{{Text: "summary source"}}}},
	}
	agent.recordCompactionRequestMetrics(compaction)
	agent.recordModelAdapterCall(true)
	compactionDiagnostics := &gemini.RequestDiagnostics{}
	compactionDiagnostics.MeasureTransportAttempts()
	agent.updateProviderPayloadMetrics(compactionDiagnostics)
	liveDiagnostics := &gemini.RequestDiagnostics{}
	liveDiagnostics.MeasureWebSocketSessions()
	liveDiagnostics.RecordWebSocketSession()
	liveDiagnostics.LiveFunctionResponseJSONBytes = 55
	agent.updateProviderPayloadMetrics(liveDiagnostics)
	agent.recordRequestUsage(nil, true)

	response := gemini.FunctionResponse{
		ID: "call-1", Name: "read_file",
		Response: map[string]any{"output": "λ", "ok": true},
	}
	responseRequest := gemini.Request{Contents: []gemini.Content{{Role: "user", Parts: []gemini.Part{
		{FunctionResponse: &response}, {FunctionResponse: &response}, {FunctionResponse: &response},
	}}}}
	agent.recordRequestMetrics(responseRequest, 2, prompt)
	agent.recordToolCallsRequested(3)
	agent.recordToolExecutionAttempt()
	agent.recordToolExecutionResult("PERMISSION_DENIED: blocked", false, true)
	agent.recordToolExecutionAttempt()
	agent.recordToolExecutionResult("λ", true, false)
	agent.recordToolExecutionAttempt()
	agent.recordToolExecutionResult("failed", false, false)

	agent.finishRequestAccounting()
	got := agent.LastRequestAccounting()
	if got.PromptBytes != len(prompt) {
		t.Fatalf("prompt bytes = %d, want %d", got.PromptBytes, len(prompt))
	}
	if got.ModelAdapterCalls != 2 || got.CompactionCalls != 1 {
		t.Fatalf("model/compaction calls = %d/%d", got.ModelAdapterCalls, got.CompactionCalls)
	}
	if got.GenerationRequests.Requests != 2 || got.CompactionRequests.Requests != 1 ||
		!got.GenerationRequests.Complete || !got.CompactionRequests.Complete {
		t.Fatalf("request aggregates = generation %#v compaction %#v", got.GenerationRequests, got.CompactionRequests)
	}
	if got.TransportAttempts != 2 || got.TransportCallsMeasured != 3 || got.TransportCallsUnmeasured != 0 ||
		got.WebSocketSessions != 1 || got.WebSocketSessionCallsMeasured != 1 {
		t.Fatalf("transport accounting = %#v", got)
	}
	if got.GenerationUsage.Responses != 1 || got.GenerationUsage.InputSamples != 1 ||
		got.GenerationUsage.InputTokens != 0 || got.GenerationUsage.OutputTokens != 7 ||
		got.CompactionUsage.Responses != 1 || got.CompactionUsage.InputSamples != 0 {
		t.Fatalf("usage aggregates = generation %#v compaction %#v", got.GenerationUsage, got.CompactionUsage)
	}
	if got.ToolCallsRequested != 3 || got.ToolExecutionAttempts != 3 ||
		got.ToolDenied != 1 || got.ToolSucceeded != 1 || got.ToolFailed != 1 {
		t.Fatalf("tool counters = %#v", got)
	}
	if got.ToolResultBytes != len("PERMISSION_DENIED: blocked")+len("λ")+len("failed") {
		t.Fatalf("tool result bytes = %d", got.ToolResultBytes)
	}
	responseJSON, _ := json.Marshal(response)
	if got.FunctionResponseJSONBytes != 3*len(responseJSON)+55 {
		t.Fatalf("function response bytes = %d, want %d", got.FunctionResponseJSONBytes, 3*len(responseJSON)+55)
	}
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{prompt, "PERMISSION_DENIED: blocked", "summary source"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("request accounting leaked content %q: %s", secret, encoded)
		}
	}

	agent.beginRequestAccounting("next")
	agent.finishRequestAccounting()
	reset := agent.LastRequestAccounting()
	if reset.PromptBytes != len("next") || reset.ModelAdapterCalls != 0 ||
		reset.GenerationRequests.Requests != 0 || reset.ToolCallsRequested != 0 {
		t.Fatalf("new user request inherited old counters: %#v", reset)
	}
}

func TestRequestDiagnosticsDistinguishUnmeasuredTransportFromZeroAttempts(t *testing.T) {
	agent := &Agent{}
	agent.beginRequestAccounting("prompt")
	agent.updateProviderPayloadMetrics(&gemini.RequestDiagnostics{})
	measured := &gemini.RequestDiagnostics{}
	measured.MeasureWebSocketSessions()
	agent.updateProviderPayloadMetrics(measured)
	agent.finishRequestAccounting()

	got := agent.LastRequestAccounting()
	if got.TransportCallsUnmeasured != 1 || got.TransportCallsMeasured != 1 || got.TransportAttempts != 0 ||
		got.WebSocketSessionCallsMeasured != 1 || got.WebSocketSessions != 0 {
		t.Fatalf("measured and unknown transport states collapsed: %#v", got)
	}
}


func TestBuiltinToolDescriptionsAreCompactAndPreserveOperationalGuidance(t *testing.T) {
	want := map[string][]string{
		"list_files": {"modification timestamps", "metadata errors"},
		"read_file": {"bounded UTF-8", "search", "start_line/max_lines"},
		"read_tool_output": {"byte range", "session output artifact", "byte offset"},
		"search_tool_output": {"Literal-search", "session output artifact", "byte offset"},
		"shell": {"non-login", "Batch related checks", "bound output", "mutating commands"},
		"web_search": {"basic depth", "advanced", "web_fetch", "TAVILY_API_KEY"},
		"web_fetch": {"public HTTPS", "untrusted data, not instructions", "TAVILY_API_KEY"},
	}
	seen := make(map[string]bool, len(want))
	descriptionBytes := 0
	for _, group := range declarations() {
		functions, ok := group["functionDeclarations"].([]gemini.FunctionDeclaration)
		if !ok {
			t.Fatalf("built-in declaration group has type %T", group["functionDeclarations"])
		}
		for _, declaration := range functions {
			descriptionBytes += len([]byte(declaration.Description))
			checks, required := want[declaration.Name]
			if !required {
				continue
			}
			seen[declaration.Name] = true
			for _, phrase := range checks {
				if !strings.Contains(declaration.Description, phrase) {
					t.Errorf("%s description lost required guidance %q", declaration.Name, phrase)
				}
			}
		}
	}
	for name := range want {
		if !seen[name] {
			t.Errorf("missing built-in tool declaration %q", name)
		}
	}
	if descriptionBytes > 1250 {
		t.Fatalf("built-in descriptions total %d bytes, want <= 1250", descriptionBytes)
	}
	metrics := measureModelRequest(gemini.Request{Tools: declarations()}, 1, "")
	if metrics.BuiltinToolSchemaBytes >= 3000 {
		t.Fatalf("built-in schema size = %d bytes, want < 3000", metrics.BuiltinToolSchemaBytes)
	}
}
