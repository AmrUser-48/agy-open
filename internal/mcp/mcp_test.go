package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/AmrUser-48/agy-open/internal/config"
)

func TestServerSupportsModernProtocol(t *testing.T) {
	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"server/discover","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":{},"io.modelcontextprotocol/clientInfo":{"name":"test","version":"1"}}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":{}}}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"echo","arguments":{"message":"hello"},"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":{}}}}`,
	}, "\n") + "\n"
	var output bytes.Buffer
	err := ServeStdio(context.Background(), strings.NewReader(input), &output,
		func() []Tool {
			return []Tool{{Name: "echo", Description: "Echo", InputSchema: map[string]any{"type": "object"}}}
		},
		func(_ context.Context, name string, args map[string]any) (string, error) {
			if name != "echo" || args["message"] != "hello" {
				t.Fatalf("unexpected call: %s %#v", name, args)
			}
			return "hello", nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("got %d protocol responses: %s", len(lines), output.String())
	}
	var discovery struct {
		Result struct {
			ResultType       string   `json:"resultType"`
			SupportedVersions []string `json:"supportedVersions"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(lines[0]), &discovery); err != nil {
		t.Fatal(err)
	}
	if discovery.Result.ResultType != "complete" || len(discovery.Result.SupportedVersions) == 0 || discovery.Result.SupportedVersions[0] != CurrentProtocolVersion {
		t.Fatalf("unexpected discovery result: %s", lines[0])
	}
	var listing struct {
		Result struct {
			ResultType string           `json:"resultType"`
			Tools      []map[string]any `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(lines[1]), &listing); err != nil {
		t.Fatal(err)
	}
	if listing.Result.ResultType != "complete" || len(listing.Result.Tools) != 1 {
		t.Fatalf("unexpected tools/list result: %s", lines[1])
	}
	var call struct {
		Result struct {
			IsError bool `json:"isError"`
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(lines[2]), &call); err != nil {
		t.Fatal(err)
	}
	if call.Result.IsError || len(call.Result.Content) != 1 || call.Result.Content[0].Text != "hello" {
		t.Fatalf("unexpected tools/call result: %s", lines[2])
	}
}

func TestServerSupportsLegacyInitialize(t *testing.T) {
	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"legacy","version":"1"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`,
	}, "\n") + "\n"
	var output bytes.Buffer
	err := ServeStdio(context.Background(), strings.NewReader(input), &output,
		func() []Tool { return []Tool{{Name: "read_file", InputSchema: map[string]any{"type": "object"}}} },
		func(context.Context, string, map[string]any) (string, error) { return "ok", nil },
	)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("legacy notification must not receive a response; got %d lines", len(lines))
	}
	var init struct {
		Result struct {
			ProtocolVersion string `json:"protocolVersion"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(lines[0]), &init); err != nil {
		t.Fatal(err)
	}
	if init.Result.ProtocolVersion != LegacyProtocolVersion {
		t.Fatalf("protocol version = %q", init.Result.ProtocolVersion)
	}
	var listing struct {
		Result struct {
			ResultType string           `json:"resultType"`
			Tools      []map[string]any `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(lines[1]), &listing); err != nil {
		t.Fatal(err)
	}
	if listing.Result.ResultType != "" || len(listing.Result.Tools) != 1 {
		t.Fatalf("unexpected legacy tools/list result: %s", lines[1])
	}
}

func TestStdioManagerDiscoversAndCallsTools(t *testing.T) {
	manager := NewManager(t.TempDir(), []config.MCPServerConfig{{
		Name: "helper", Command: os.Args[0],
		Args: []string{"-test.run=TestMCPHelperProcess"},
		Env: map[string]string{"AGY_MCP_HELPER": "1"},
	}})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := manager.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	tools := manager.Tools()
	if len(tools) != 1 || tools[0].Name != "mcp__helper__echo" {
		t.Fatalf("unexpected tools: %#v", tools)
	}
	got, err := manager.Call(ctx, tools[0].Name, map[string]any{"value": "round-trip"})
	if err != nil {
		t.Fatal(err)
	}
	if got != "echo: round-trip" {
		t.Fatalf("tool output = %q", got)
	}
	manager.Close()
}

func TestStdioManagerFallsBackToLegacyProtocol(t *testing.T) {
	manager := NewManager(t.TempDir(), []config.MCPServerConfig{{
		Name: "legacy", Command: os.Args[0],
		Args: []string{"-test.run=TestMCPHelperProcess"},
		Env: map[string]string{"AGY_MCP_HELPER": "1", "AGY_MCP_LEGACY": "1"},
	}})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := manager.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	tools := manager.Tools()
	if len(tools) != 1 || tools[0].Name != "mcp__legacy__echo" {
		t.Fatalf("legacy discovery returned unexpected tools: %#v", tools)
	}
	manager.Close()
}

func TestStdioManagerLoadsMoreThanOneHundredToolPages(t *testing.T) {
	manager := NewManager(t.TempDir(), []config.MCPServerConfig{{
		Name: "pager", Command: os.Args[0],
		Args: []string{"-test.run=TestMCPHelperProcess"},
		Env: map[string]string{"AGY_MCP_HELPER": "1", "AGY_MCP_PAGE_COUNT": "101"},
	}})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := manager.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	defer manager.Close()

	got := manager.Tools()
	if len(got) != 101 {
		t.Fatalf("manager loaded %d tools from 101 pages, want all 101", len(got))
	}
	if got[0].Name != "mcp__pager__tool_000" || got[100].Name != "mcp__pager__tool_100" {
		t.Fatalf("first/last tools = %q / %q", got[0].Name, got[100].Name)
	}
}

func TestMCPHelperProcess(t *testing.T) {
	if os.Getenv("AGY_MCP_HELPER") != "1" {
		return
	}
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		var request struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params struct {
				Cursor string `json:"cursor"`
			} `json:"params"`
		}
		if json.Unmarshal(scanner.Bytes(), &request) != nil {
			continue
		}
		legacy := os.Getenv("AGY_MCP_LEGACY") == "1"
		if legacy && request.Method == "server/discover" {
			_ = json.NewEncoder(os.Stdout).Encode(map[string]any{
				"jsonrpc": "2.0", "id": request.ID,
				"error": map[string]any{"code": -32601, "message": "method not found"},
			})
			continue
		}
		var result any
		switch request.Method {
		case "server/discover":
			result = map[string]any{"supportedVersions": []string{CurrentProtocolVersion}}
		case "initialize":
			var full struct {
				Params struct {
					ProtocolVersion string `json:"protocolVersion"`
				} `json:"params"`
			}
			_ = json.Unmarshal(scanner.Bytes(), &full)
			if full.Params.ProtocolVersion != "2025-06-18" {
				_ = json.NewEncoder(os.Stdout).Encode(map[string]any{
					"jsonrpc": "2.0", "id": request.ID,
					"error": map[string]any{"code": -32602, "message": "unsupported protocol version"},
				})
				continue
			}
			result = map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{}}
		case "tools/list":
			if pageCountText := os.Getenv("AGY_MCP_PAGE_COUNT"); pageCountText != "" {
				pageCount, _ := strconv.Atoi(pageCountText)
				page := 0
				if strings.HasPrefix(request.Params.Cursor, "page-") {
					page, _ = strconv.Atoi(strings.TrimPrefix(request.Params.Cursor, "page-"))
				}
				nextCursor := ""
				if page+1 < pageCount {
					nextCursor = fmt.Sprintf("page-%d", page+1)
				}
				result = map[string]any{
					"resultType": "complete",
					"tools": []any{map[string]any{
						"name": fmt.Sprintf("tool_%03d", page),
						"description": "paged test tool",
						"inputSchema": map[string]any{"type": "object"},
					}},
					"nextCursor": nextCursor,
				}
			} else {
				result = map[string]any{"resultType": "complete", "tools": []any{
					map[string]any{"name": "echo", "description": "test echo", "inputSchema": map[string]any{"type": "object"}},
				}}
			}
		case "tools/call":
			var full struct {
				Params struct {
					Arguments map[string]any `json:"arguments"`
				} `json:"params"`
			}
			_ = json.Unmarshal(scanner.Bytes(), &full)
			result = map[string]any{"resultType": "complete", "content": []any{
				map[string]any{"type": "text", "text": "echo: " + full.Params.Arguments["value"].(string)},
			}}
		default:
			result = map[string]any{}
		}
		if len(request.ID) > 0 && string(request.ID) != "null" {
			_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result})
		}
	}
	os.Exit(0)
}


func TestManagerNeedsRefreshAvoidsRepeatedFailedDiscovery(t *testing.T) {
	manager := NewManager(t.TempDir(), []config.MCPServerConfig{{Name: "helper", Command: "unused"}})
	if !manager.NeedsRefresh() {
		t.Fatal("newly configured server should require its first discovery")
	}

	manager.mu.Lock()
	manager.statuses["helper"] = ServerStatus{Name: "helper", State: "error", Error: "startup failed"}
	manager.mu.Unlock()
	if manager.NeedsRefresh() {
		t.Fatal("failed discovery should not be retried automatically on each chat")
	}

	process := &client{done: make(chan struct{})}
	manager.mu.Lock()
	manager.statuses["helper"] = ServerStatus{Name: "helper", State: "connected"}
	manager.clients["helper"] = process
	manager.mu.Unlock()
	if manager.NeedsRefresh() {
		t.Fatal("a healthy connected server should not be rediscovered on each chat")
	}

	close(process.done)
	if !manager.NeedsRefresh() {
		t.Fatal("an established server process that exited should request one recovery attempt")
	}

	manager.mu.Lock()
	manager.statuses["helper"] = ServerStatus{Name: "helper", State: "error", Error: "recovery failed"}
	manager.clients["helper"] = nil
	manager.mu.Unlock()
	if manager.NeedsRefresh() {
		t.Fatal("failed recovery should return to idle until an explicit connect")
	}
}

func TestManagerCloseClearsStatusesAndCachedTools(t *testing.T) {
	manager := NewManager(t.TempDir(), []config.MCPServerConfig{{Name: "helper", Command: "unused"}})
	manager.mu.Lock()
	manager.statuses["helper"] = ServerStatus{Name: "helper", State: "connected", ToolCount: 1}
	manager.tools["mcp__helper__echo"] = Tool{Name: "mcp__helper__echo", Server: "helper"}
	manager.mu.Unlock()
	manager.Close()
	if got := manager.Statuses(); len(got) != 1 || got[0].State != "not connected" {
		t.Fatalf("statuses after Close = %#v, want configured server shown as not connected", got)
	}
	if got := manager.Tools(); len(got) != 0 {
		t.Fatalf("cached remote tools survived Close: %#v", got)
	}
}
