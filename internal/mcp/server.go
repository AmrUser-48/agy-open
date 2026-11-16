package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

type ToolHandler func(context.Context, string, map[string]any) (string, error)
type ToolProvider func() []Tool

type serverRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  map[string]any  `json:"params"`
}

type serverState struct {
	legacy bool
}

var legacyVersions = []string{"2025-11-25", "2025-06-18", "2025-03-26"}

// ServeStdio speaks MCP over newline-delimited JSON-RPC on stdin/stdout.
// stdout is reserved exclusively for protocol responses; diagnostics belong on stderr.
func ServeStdio(ctx context.Context, input io.Reader, output io.Writer, provideTools ToolProvider, callTool ToolHandler) error {
	if input == nil || output == nil || provideTools == nil || callTool == nil {
		return errors.New("MCP stdio server requires input, output, tool provider, and tool handler")
	}
	state := &serverState{}
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 64*1024), maxRPCLineBytes)
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return err
		}
		var request serverRequest
		if err := json.Unmarshal(scanner.Bytes(), &request); err != nil {
			if writeErr := writeRPC(output, map[string]any{
				"jsonrpc": "2.0", "id": nil,
				"error": map[string]any{"code": -32700, "message": "parse error"},
			}); writeErr != nil {
				return writeErr
			}
			continue
		}
		if request.JSONRPC != "2.0" || request.Method == "" {
			if len(request.ID) != 0 && string(request.ID) != "null" {
				if err := writeRPCError(output, request.ID, -32600, "invalid request"); err != nil {
					return err
				}
			}
			continue
		}
		if err := handleServerRequest(ctx, output, state, request, provideTools, callTool); err != nil {
			return err
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read MCP stdio: %w", err)
	}
	return nil
}

func handleServerRequest(ctx context.Context, output io.Writer, state *serverState, request serverRequest, provideTools ToolProvider, callTool ToolHandler) error {
	hasID := len(request.ID) != 0 && string(request.ID) != "null"
	switch request.Method {
	case "notifications/initialized", "notifications/cancelled":
		return nil
	case "server/discover":
		if !hasID {
			return nil
		}
		if !validModernMeta(request.Params) {
			return writeRPCError(output, request.ID, -32602, "server/discover requires MCP 2026-07-28 request metadata")
		}
		result := map[string]any{
			"resultType": "complete",
			"supportedVersions": append([]string{CurrentProtocolVersion}, legacyVersions...),
			"capabilities": map[string]any{"tools": map[string]any{"listChanged": false}},
			"_meta": map[string]any{"io.modelcontextprotocol/serverInfo": map[string]any{
				"name": "agy-open", "version": "0.8.7",
			}},
		}
		return writeRPCResult(output, request.ID, result)
	case "initialize":
		if !hasID {
			return nil
		}
		state.legacy = true
		requested, _ := request.Params["protocolVersion"].(string)
		selected := ""
		for _, supported := range legacyVersions {
			if supported == requested {
				selected = supported
				break
			}
		}
		if selected == "" {
			return writeRPCError(output, request.ID, -32602, "unsupported legacy MCP protocol version")
		}
		return writeRPCResult(output, request.ID, map[string]any{
			"protocolVersion": selected,
			"capabilities": map[string]any{"tools": map[string]any{"listChanged": false}},
			"serverInfo": map[string]any{"name": "agy-open", "version": "0.8.7"},
		})
	case "ping":
		if !hasID {
			return nil
		}
		return writeRPCResult(output, request.ID, map[string]any{})
	case "tools/list":
		if !hasID {
			return nil
		}
		if !state.legacy && !validModernMeta(request.Params) {
			return writeRPCError(output, request.ID, -32602, "tools/list requires MCP 2026-07-28 request metadata")
		}
		tools := provideTools()
		toolList := make([]map[string]any, 0, len(tools))
		for _, tool := range tools {
			if tool.Name == "" {
				continue
			}
			schema := tool.InputSchema
			if schema == nil {
				schema = map[string]any{"type": "object", "properties": map[string]any{}}
			}
			toolList = append(toolList, map[string]any{
				"name": tool.Name, "description": tool.Description, "inputSchema": schema,
			})
		}
		if state.legacy {
			return writeRPCResult(output, request.ID, map[string]any{"tools": toolList})
		}
		return writeRPCResult(output, request.ID, map[string]any{
			"resultType": "complete", "tools": toolList,
		})
	case "tools/call":
		if !hasID {
			return nil
		}
		if !state.legacy && !validModernMeta(request.Params) {
			return writeRPCError(output, request.ID, -32602, "tools/call requires MCP 2026-07-28 request metadata")
		}
		name, _ := request.Params["name"].(string)
		if name == "" {
			return writeRPCError(output, request.ID, -32602, "tools/call requires a tool name")
		}
		arguments, _ := request.Params["arguments"].(map[string]any)
		if arguments == nil {
			arguments = map[string]any{}
		}
		text, callErr := callTool(ctx, name, arguments)
		content := []map[string]any{{"type": "text", "text": text}}
		result := map[string]any{"content": content}
		if callErr != nil {
			result["isError"] = true
		} else {
			result["isError"] = false
		}
		if !state.legacy {
			result["resultType"] = "complete"
		}
		return writeRPCResult(output, request.ID, result)
	default:
		if !hasID {
			return nil
		}
		return writeRPCError(output, request.ID, -32601, "method not found: "+request.Method)
	}
}

func validModernMeta(params map[string]any) bool {
	meta, ok := params["_meta"].(map[string]any)
	if !ok {
		return false
	}
	version, _ := meta["io.modelcontextprotocol/protocolVersion"].(string)
	_, hasCapabilities := meta["io.modelcontextprotocol/clientCapabilities"].(map[string]any)
	return version == CurrentProtocolVersion && hasCapabilities
}

func writeRPCResult(output io.Writer, id json.RawMessage, result any) error {
	return writeRPC(output, map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
}

func writeRPCError(output io.Writer, id json.RawMessage, code int, message string) error {
	return writeRPC(output, map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{
		"code": code, "message": message,
	}})
}

func writeRPC(output io.Writer, value any) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	for len(encoded) > 0 {
		n, err := output.Write(encoded)
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		encoded = encoded[n:]
	}
	return nil
}

// Ensure errors emitted by this package remain useful when a remote tool fails.
func toolErrorText(err error) string {
	if err == nil {
		return ""
	}
	return strings.TrimSpace(err.Error())
}
