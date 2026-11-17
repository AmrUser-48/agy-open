package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/AmrUser-48/agy-open/internal/config"
	"github.com/AmrUser-48/agy-open/internal/gemini"
	"github.com/AmrUser-48/agy-open/internal/mcp"
	rolecfg "github.com/AmrUser-48/agy-open/internal/roles"
	"github.com/AmrUser-48/agy-open/internal/session"
	"github.com/AmrUser-48/agy-open/internal/tools"
)

type mcpDiscoveryTestModel struct {
	responses []gemini.Content
	requests  []gemini.Request
}

func (m *mcpDiscoveryTestModel) ModelName() string { return "mcp-discovery-test" }

func (m *mcpDiscoveryTestModel) Generate(_ context.Context, request gemini.Request) (gemini.Content, error) {
	raw, err := json.Marshal(request)
	if err != nil {
		return gemini.Content{}, err
	}
	var snapshot gemini.Request
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		return gemini.Content{}, err
	}
	m.requests = append(m.requests, snapshot)
	if len(m.responses) == 0 {
		return gemini.Content{}, io.EOF
	}
	response := m.responses[0]
	m.responses = m.responses[1:]
	return response, nil
}

func (m *mcpDiscoveryTestModel) GenerateStream(ctx context.Context, request gemini.Request, _ func(string)) (gemini.Content, error) {
	return m.Generate(ctx, request)
}

func (m *mcpDiscoveryTestModel) ListModelOptions(context.Context) ([]gemini.ModelOption, error) {
	return nil, nil
}

func (m *mcpDiscoveryTestModel) ListModels(context.Context) ([]string, error) { return nil, nil }

func TestRunContextDiscoversAndActivatesOnlyMatchedMCPTool(t *testing.T) {
	root := t.TempDir()
	manager := mcp.NewManager(root, []config.MCPServerConfig{{
		Name: "helper", Command: os.Args[0],
		Args: []string{"-test.run=TestAgentMCPHelperProcess"},
		Env:  map[string]string{"AGY_AGENT_MCP_HELPER": "1"},
	}})
	defer manager.Close()

	history, err := session.New("none")
	if err != nil {
		t.Fatal(err)
	}
	defer history.Close()

	model := &mcpDiscoveryTestModel{responses: []gemini.Content{
		{Role: "model", Parts: []gemini.Part{{FunctionCall: &gemini.FunctionCall{
			ID: "search-1", Name: mcpSearchToolName, Args: map[string]any{"query": "echo"},
		}}}},
		{Role: "model", Parts: []gemini.Part{{FunctionCall: &gemini.FunctionCall{
			ID: "echo-1", Name: "mcp__helper__echo", Args: map[string]any{"value": "hello"},
		}}}},
		{Role: "model", Parts: []gemini.Part{{Text: "finished"}}},
	}}
	agent := &Agent{
		cfg: config.Config{
			ModelProvider: "groq", Model: "mcp-discovery-test",
			MaxTurns: 4, MaxToolCalls: 8, SessionCompression: "none",
		},
		model: model, tools: tools.New(root, tools.ModeAuto), history: history,
		effort: "default", toolDefs: declarations(), mcp: manager,
	}
	var events []Event
	agent.SetEventSink(func(event Event) { events = append(events, event) })

	if err := agent.RunContext(context.Background(), "find and call the echo tool", io.Discard); err != nil {
		t.Fatal(err)
	}
	if len(model.requests) != 3 {
		t.Fatalf("model received %d requests, want 3", len(model.requests))
	}
	hasDeclaration := func(request gemini.Request, name string) bool {
		for _, group := range request.Tools {
			declarations, _ := group["functionDeclarations"].([]any)
			for _, raw := range declarations {
				declaration, _ := raw.(map[string]any)
				if declaration["name"] == name {
					return true
				}
			}
			typed, _ := group["functionDeclarations"].([]gemini.FunctionDeclaration)
			for _, declaration := range typed {
				if declaration.Name == name {
					return true
				}
			}
		}
		return false
	}
	if !hasDeclaration(model.requests[0], mcpSearchToolName) {
		t.Fatal("initial request did not advertise MCP discovery")
	}
	if hasDeclaration(model.requests[0], "mcp__helper__echo") || hasDeclaration(model.requests[0], "mcp__helper__delete") {
		t.Fatal("initial request advertised remote MCP schemas before discovery")
	}
	if !hasDeclaration(model.requests[1], "mcp__helper__echo") {
		t.Fatal("search did not activate the matching MCP schema for the next request")
	}
	if hasDeclaration(model.requests[1], "mcp__helper__delete") {
		t.Fatal("search activated an unrelated MCP schema")
	}
	foundEcho := false
	for _, event := range events {
		if event.Kind == "tool_end" && event.Tool == "mcp__helper__echo" && event.OK && strings.Contains(event.Output, "echo: hello") {
			foundEcho = true
		}
	}
	if !foundEcho {
		t.Fatal("selected MCP tool did not execute through the existing manager route")
	}
}

func TestMCPDiscoveryRespectsExistingRoleAndPermissionGates(t *testing.T) {
	manager := mcp.NewManager(t.TempDir(), []config.MCPServerConfig{{Name: "helper", Command: "unused"}})
	defer manager.Close()

	cases := []struct {
		name string
		approvalMode string
		roleAllowed bool
		forcedReadOnly bool
		blockFileMutations bool
		want bool
	}{
		{"ask with MCP role", tools.ModeAsk, true, false, false, true},
		{"read-only permission", tools.ModeReadOnly, true, false, false, false},
		{"disabled permission", tools.ModeDisabled, true, false, false, false},
		{"role excludes MCP", tools.ModeAsk, false, false, false, false},
		{"forced read-only", tools.ModeAsk, true, true, false, false},
		{"isolated non-writer", tools.ModeAsk, true, false, true, false},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			allowed := []string{"list_files", "read_file", "search"}
			if test.roleAllowed {
				allowed = append(allowed, "mcp")
			}
			agent := &Agent{
				tools: tools.New(t.TempDir(), test.approvalMode),
				mcp: manager,
				activeRole: rolecfg.Definition{Name: "test", AllowedTools: allowed},
				forcedReadOnly: test.forcedReadOnly,
				blockFileMutations: test.blockFileMutations,
			}
			if got := agent.mcpDiscoveryEnabled(); got != test.want {
				t.Fatalf("mcpDiscoveryEnabled() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestMCPUnactivatedToolCannotExecute(t *testing.T) {
	agent := &Agent{
		tools: tools.New(t.TempDir(), tools.ModeAuto),
		mcp: mcp.NewManager(t.TempDir(), []config.MCPServerConfig{{Name: "helper", Command: "unused"}}),
		cfg: config.Config{ModelProvider: "groq"},
	}
	defer agent.mcp.Close()
	result := agent.callToolWithMCPActivation(context.Background(), "mcp__helper__echo", map[string]any{"value": "no"}, map[string]struct{}{})
	if result.OK || !strings.Contains(result.Output, "not activated") {
		t.Fatalf("unactivated MCP call result = %#v", result)
	}
}

func TestAgentMCPHelperProcess(t *testing.T) {
	if os.Getenv("AGY_AGENT_MCP_HELPER") != "1" {
		return
	}
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		var request struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments"`
			} `json:"params"`
		}
		if json.Unmarshal(scanner.Bytes(), &request) != nil {
			continue
		}
		var result any
		switch request.Method {
		case "server/discover":
			result = map[string]any{"supportedVersions": []string{mcp.CurrentProtocolVersion}}
		case "initialize":
			result = map[string]any{"protocolVersion": mcp.CurrentProtocolVersion, "capabilities": map[string]any{}}
		case "tools/list":
			result = map[string]any{"resultType": "complete", "tools": []any{
				map[string]any{
					"name": "echo", "description": "Echo a value to the caller",
					"inputSchema": map[string]any{"type": "object", "properties": map[string]any{
						"value": map[string]any{"type": "string"},
					}, "required": []string{"value"}},
				},
				map[string]any{
					"name": "delete", "description": "Delete a remote resource",
					"inputSchema": map[string]any{"type": "object", "properties": map[string]any{
						"resource": map[string]any{"type": "string"},
					}, "required": []string{"resource"}},
				},
			}}
		case "tools/call":
			output := "unknown tool"
			if request.Params.Name == "echo" {
				output = "echo: " + request.Params.Arguments["value"].(string)
			}
			result = map[string]any{"content": []any{map[string]any{"type": "text", "text": output}}}
		default:
			result = map[string]any{}
		}
		if len(request.ID) > 0 && string(request.ID) != "null" {
			_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result})
		}
	}
	os.Exit(0)
}
