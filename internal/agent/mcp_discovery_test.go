package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
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
	m.requests = append(m.requests, request)
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
			ModelProvider: "groq", Model: "mcp-discovery-test", MCPEnabled: true,
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
		mcpEnabled bool
		want bool
	}{
		{"ask with MCP role", tools.ModeAsk, true, false, false, true, true},
		{"read-only permission", tools.ModeReadOnly, true, false, false, true, false},
		{"disabled permission", tools.ModeDisabled, true, false, false, true, false},
		{"role excludes MCP", tools.ModeAsk, false, false, false, true, false},
		{"forced read-only", tools.ModeAsk, true, true, false, true, false},
		{"isolated non-writer", tools.ModeAsk, true, false, true, true, false},
		{"global MCP switch off", tools.ModeAsk, true, false, false, false, false},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			allowed := []string{"list_files", "read_file", "search"}
			if test.roleAllowed {
				allowed = append(allowed, "mcp")
			}
			agent := &Agent{
				cfg: config.Config{MCPEnabled: test.mcpEnabled},
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
		cfg: config.Config{ModelProvider: "groq", MCPEnabled: true},
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
	if marker := os.Getenv("AGY_AGENT_MCP_START_MARKER"); marker != "" {
		_ = os.WriteFile(marker, []byte("started"), 0600)
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
			if marker := os.Getenv("AGY_AGENT_MCP_LIST_MARKER"); marker != "" {
				file, err := os.OpenFile(marker, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
				if err == nil {
					_, _ = file.WriteString("listed\n")
					_ = file.Close()
				}
			}
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


func TestRunContextDoesNotStartMCPWhenGlobalSwitchIsOff(t *testing.T) {
	root := t.TempDir()
	startMarker := filepath.Join(root, "started")
	manager := mcp.NewManager(root, []config.MCPServerConfig{{
		Name: "helper", Command: os.Args[0],
		Args: []string{"-test.run=TestAgentMCPHelperProcess"},
		Env: map[string]string{"AGY_AGENT_MCP_HELPER": "1", "AGY_AGENT_MCP_START_MARKER": startMarker},
	}})
	defer manager.Close()
	history, err := session.New("none")
	if err != nil { t.Fatal(err) }
	defer history.Close()
	model := &mcpDiscoveryTestModel{responses: []gemini.Content{
		{Role: "model", Parts: []gemini.Part{{Text: "ordinary chat response"}}},
	}}
	agent := &Agent{
		cfg: config.Config{ModelProvider: "groq", Model: "mcp-discovery-test", MCPEnabled: false, SessionCompression: "none"},
		model: model, tools: tools.New(root, tools.ModeAuto), history: history,
		effort: "default", toolDefs: declarations(), mcp: manager,
	}
	if err := agent.RunContext(context.Background(), "hello", io.Discard); err != nil { t.Fatal(err) }
	if _, err := os.Stat(startMarker); !os.IsNotExist(err) {
		t.Fatalf("MCP server started while globally disabled; stat err=%v", err)
	}
	for _, group := range model.requests[0].Tools {
		decls, _ := group["functionDeclarations"].([]gemini.FunctionDeclaration)
		for _, declaration := range decls {
			if declaration.Name == mcpSearchToolName || strings.HasPrefix(declaration.Name, "mcp__") {
				t.Fatalf("MCP tool advertised while globally disabled: %s", declaration.Name)
			}
		}
	}
}

func TestRunContextDoesNotRepeatMCPToolsListOnEveryChatTurn(t *testing.T) {
	root := t.TempDir()
	listMarker := filepath.Join(root, "tools-list-count")
	manager := mcp.NewManager(root, []config.MCPServerConfig{{
		Name: "helper", Command: os.Args[0],
		Args: []string{"-test.run=TestAgentMCPHelperProcess"},
		Env: map[string]string{"AGY_AGENT_MCP_HELPER": "1", "AGY_AGENT_MCP_LIST_MARKER": listMarker},
	}})
	defer manager.Close()
	history, err := session.New("none")
	if err != nil { t.Fatal(err) }
	defer history.Close()
	model := &mcpDiscoveryTestModel{responses: []gemini.Content{
		{Role: "model", Parts: []gemini.Part{{Text: "first response"}}},
		{Role: "model", Parts: []gemini.Part{{Text: "second response"}}},
	}}
	agent := &Agent{
		cfg: config.Config{ModelProvider: "groq", Model: "mcp-discovery-test", MCPEnabled: true, SessionCompression: "none"},
		model: model, tools: tools.New(root, tools.ModeAuto), history: history,
		effort: "default", toolDefs: declarations(), mcp: manager,
	}
	for _, prompt := range []string{"first", "second"} {
		if err := agent.RunContext(context.Background(), prompt, io.Discard); err != nil { t.Fatal(err) }
	}
	marker, err := os.ReadFile(listMarker)
	if err != nil { t.Fatal(err) }
	if got := strings.Count(string(marker), "listed"); got != 1 {
		t.Fatalf("MCP tools/list ran %d times for two chat turns, want once; log=%q", got, marker)
	}
}


// TestUpdateConfigGlobalMCPOffStopsConnectedServer verifies the user-facing
// settings update closes already-running subprocesses, clears their schemas,
// and blocks both discovery and execution after switching the master flag off.
func TestUpdateConfigGlobalMCPOffStopsConnectedServer(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg"))

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	root := t.TempDir()
	cfg.MCPEnabled = true
	cfg.MCPServers = []config.MCPServerConfig{{
		Name: "helper",
		Command: os.Args[0],
		Args: []string{"-test.run=TestAgentMCPHelperProcess"},
		Env: map[string]string{"AGY_AGENT_MCP_HELPER": "1"},
	}}

	roles, err := rolecfg.Load()
	if err != nil {
		t.Fatalf("load roles: %v", err)
	}
	activeRole, ok := rolecfg.Find(roles, cfg.AgentRole)
	if !ok {
		t.Fatalf("configured role %q not found", cfg.AgentRole)
	}
	manager := mcp.NewManager(root, cfg.MCPServers)
	if err := manager.Refresh(context.Background()); err != nil {
		t.Fatalf("initial MCP discovery: %v", err)
	}
	if got := len(manager.Tools()); got != 2 {
		t.Fatalf("initial MCP tools = %d, want 2", got)
	}

	history, err := session.New("none")
	if err != nil {
		t.Fatal(err)
	}
	agent := &Agent{
		cfg: cfg,
		model: &mcpDiscoveryTestModel{},
		tools: tools.New(root, cfg.ApprovalMode),
		history: history,
		effort: cfg.Effort,
		toolDefs: declarations(),
		mcp: manager,
		roleDefinitions: roles,
		activeRole: activeRole,
	}
	t.Cleanup(func() {
		_ = agent.Close()
	})

	next := agent.Config()
	next.MCPEnabled = false
	if err := agent.UpdateConfig(next); err != nil {
		t.Fatalf("UpdateConfig(MCP off): %v", err)
	}
	if agent.Config().MCPEnabled {
		t.Fatal("global MCP setting remained enabled")
	}
	if got := len(manager.Tools()); got != 0 {
		t.Fatalf("cached remote tools after global OFF = %d, want 0", got)
	}
	for _, status := range manager.Statuses() {
		if status.State == "connected" {
			t.Fatalf("server %q remained connected after global OFF", status.Name)
		}
	}
	if got := agent.MCPTools(); len(got) != 0 {
		t.Fatalf("agent exposed remote tools after global OFF: %#v", got)
	}

	activated := map[string]struct{}{"mcp__helper__echo": {}}
	discovery := agent.callToolWithMCPActivation(context.Background(), mcpSearchToolName,
		map[string]any{"query": "echo"}, activated)
	if discovery.OK || !strings.Contains(discovery.Output, "not available") {
		t.Fatalf("MCP discovery after global OFF = %#v", discovery)
	}
	call := agent.callToolWithMCPActivation(context.Background(), "mcp__helper__echo",
		map[string]any{"value": "must-not-run"}, activated)
	if call.OK || !strings.Contains(call.Output, "globally disabled") {
		t.Fatalf("remote MCP call after global OFF = %#v", call)
	}
}


func TestMCPDiscoveryDescriptionIsCompactAndRetainsPagingContract(t *testing.T) {
	declaration := mcpSearchDeclaration()
	for _, phrase := range []string{
		"connected MCP tools", "Results activate typed schemas",
		"offset", "Page size does not limit the registry",
	} {
		if !strings.Contains(declaration.Description, phrase) {
			t.Errorf("MCP discovery description lost %q", phrase)
		}
	}
	if len([]byte(declaration.Description)) > 180 {
		t.Fatalf("MCP discovery description is %d bytes, want <= 180", len(declaration.Description))
	}
	properties, ok := declaration.Parameters["properties"].(map[string]any)
	if !ok {
		t.Fatalf("MCP discovery properties = %T", declaration.Parameters["properties"])
	}
	for name, maxBytes := range map[string]int{"query": 90, "offset": 80, "limit": 80} {
		property, ok := properties[name].(map[string]any)
		if !ok {
			t.Fatalf("MCP discovery property %q = %T", name, properties[name])
		}
		description, _ := property["description"].(string)
		if len([]byte(description)) > maxBytes {
			t.Errorf("MCP property %q description is %d bytes, want <= %d", name, len(description), maxBytes)
		}
	}
}
