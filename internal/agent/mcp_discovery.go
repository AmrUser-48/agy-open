package agent

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/AmrUser-48/agy-open/internal/gemini"
	"github.com/AmrUser-48/agy-open/internal/mcp"
	"github.com/AmrUser-48/agy-open/internal/tools"
)

const (
	mcpSearchToolName        = "mcp_search_tools"
	defaultMCPSearchLimit    = 8
	maxMCPDescriptionPreview = 240
)

func (a *Agent) declarationsAsTools() []map[string]any {
	return a.declarationsAsToolsWithMCP(nil)
}

func (a *Agent) declarationsAsToolsWithMCP(activated map[string]struct{}) []map[string]any {
	if a == nil || a.tools == nil {
		return nil
	}
	filtered := make([]map[string]any, 0, len(a.toolDefs)+4)
	for _, group := range a.toolDefs {
		declarations, ok := group["functionDeclarations"].([]gemini.FunctionDeclaration)
		if !ok {
			continue
		}
		allowed := make([]gemini.FunctionDeclaration, 0, len(declarations))
		for _, declaration := range declarations {
			if a.toolUnavailableReason(declaration.Name) == "" {
				allowed = append(allowed, declaration)
			}
		}
		if len(allowed) == 0 {
			continue
		}
		copyGroup := make(map[string]any, len(group))
		for key, value := range group {
			copyGroup[key] = value
		}
		copyGroup["functionDeclarations"] = allowed
		filtered = append(filtered, copyGroup)
	}

	// Output retrieval schemas are session-scoped and added only after a
	// large result has actually been captured. Gemini Live fixes its schema at
	// connection setup, so keep them available for that transport.
	if (a.history != nil && a.history.HasArtifacts()) || a.cfg.ModelProvider == "gemini-live" {
		for _, group := range artifactOutputDeclarations() {
			declarations, ok := group["functionDeclarations"].([]gemini.FunctionDeclaration)
			if !ok {
				continue
			}
			allowed := make([]gemini.FunctionDeclaration, 0, len(declarations))
			for _, declaration := range declarations {
				if a.toolUnavailableReason(declaration.Name) == "" {
					allowed = append(allowed, declaration)
				}
			}
			if len(allowed) == 0 {
				continue
			}
			copyGroup := map[string]any{"functionDeclarations": allowed}
			filtered = append(filtered, copyGroup)
		}
	}

	if !a.mcpDiscoveryEnabled() {
		return filtered
	}

	remoteTools := a.mcp.Tools()
	if a.cfg.ModelProvider == "gemini-live" {
		// Live tools are fixed when the WebSocket setup message is sent. Keep
		// the complete legacy schema set until that transport supports updates.
		declarations := make([]gemini.FunctionDeclaration, 0, len(remoteTools))
		for _, remote := range remoteTools {
			declarations = append(declarations, mcpFunctionDeclaration(remote))
		}
		if len(declarations) > 0 {
			filtered = append(filtered, map[string]any{"functionDeclarations": declarations})
		}
		return filtered
	}

	filtered = append(filtered, map[string]any{
		"functionDeclarations": []gemini.FunctionDeclaration{mcpSearchDeclaration()},
	})
	var activatedDeclarations []gemini.FunctionDeclaration
	for _, remote := range remoteTools {
		if _, ok := activated[remote.Name]; !ok {
			continue
		}
		activatedDeclarations = append(activatedDeclarations, mcpFunctionDeclaration(remote))
	}
	if len(activatedDeclarations) > 0 {
		filtered = append(filtered, map[string]any{"functionDeclarations": activatedDeclarations})
	}
	return filtered
}

func artifactOutputDeclarations() []map[string]any {
	return []map[string]any{
		{"functionDeclarations": []gemini.FunctionDeclaration{{
			Name: "read_tool_output",
			Description: "Read complete tool output retained in RAM for the current session by byte offset.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"artifact_id": map[string]any{"type": "string"},
					"offset": map[string]any{"type": "integer"},
					"max_bytes": map[string]any{"type": "integer"},
				},
				"required": []string{"artifact_id"},
			},
		}}},
		{"functionDeclarations": []gemini.FunctionDeclaration{{
			Name: "search_tool_output",
			Description: "Literal-search tool output retained in RAM for the current session; continue at next_offset.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"artifact_id": map[string]any{"type": "string"},
					"query": map[string]any{"type": "string"},
					"offset": map[string]any{"type": "integer"},
					"max_matches": map[string]any{"type": "integer"},
				},
				"required": []string{"artifact_id", "query"},
			},
		}}},
	}
}

func (a *Agent) mcpDiscoveryEnabled() bool {
	return a != nil && a.mcp != nil && a.tools != nil && a.cfg.MCPEnabled &&
		a.mcp.HasEnabled() && a.toolUnavailableReason("mcp") == ""
}

func mcpSearchDeclaration() gemini.FunctionDeclaration {
	return gemini.FunctionDeclaration{
		Name:        mcpSearchToolName,
		Description: "Search connected MCP tools by name, server, or description. Results activate typed schemas for the next request; use offset to inspect more pages. Page size does not limit the registry.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{
					"type":        "string",
					"description": "Required text matched against MCP tool names, server names, and descriptions.",
				},
				"offset": map[string]any{
					"type":        "integer",
					"description": "Zero-based offset (default 0); continue with returned nextOffset.",
				},
				"limit": map[string]any{
					"type":        "integer",
					"description": "Page size 1-25 (default 8); not a total tool limit.",
				},
			},
			"required": []string{"query"},
		},
	}
}

func mcpFunctionDeclaration(remote mcp.Tool) gemini.FunctionDeclaration {
	schema := remote.InputSchema
	if schema == nil {
		schema = map[string]any{"type": "object", "properties": map[string]any{}}
	}
	description := remote.Description
	if description == "" {
		description = "Call remote MCP tool " + remote.RemoteName
	}
	description = "[MCP server: " + remote.Server + "; remote tool: " + remote.RemoteName + "] " + description
	return gemini.FunctionDeclaration{Name: remote.Name, Description: description, Parameters: schema}
}

func (a *Agent) searchMCPTools(args map[string]any, activated map[string]struct{}) tools.Result {
	if activated == nil || !a.mcpDiscoveryEnabled() {
		return tools.Result{Output: "PERMISSION_DENIED: MCP tool discovery is not available in this execution context", OK: false}
	}
	query, _ := args["query"].(string)
	offset, err := mcpSearchInteger(args, "offset", 0)
	if err != nil {
		return tools.Result{Output: err.Error(), OK: false}
	}
	limit, err := mcpSearchInteger(args, "limit", defaultMCPSearchLimit)
	if err != nil {
		return tools.Result{Output: err.Error(), OK: false}
	}
	matches, total, err := a.mcp.SearchTools(query, offset, limit)
	if err != nil {
		return tools.Result{Output: err.Error(), OK: false}
	}
	registryCount := len(a.mcp.Tools())
	results := make([]map[string]any, 0, len(matches))
	for _, remote := range matches {
		activated[remote.Name] = struct{}{}
		schemaBytes, marshalErr := json.Marshal(remote.InputSchema)
		if marshalErr != nil {
			return tools.Result{Output: fmt.Sprintf("cannot measure schema for %q: %v", remote.Name, marshalErr), OK: false}
		}
		results = append(results, map[string]any{
			"name":        remote.Name,
			"server":      remote.Server,
			"remoteName":  remote.RemoteName,
			"description": mcpDescriptionPreview(remote.Description),
			"schemaBytes": len(schemaBytes),
		})
	}
	hasMore := offset+len(matches) < total
	response := map[string]any{
		"query":             strings.TrimSpace(query),
		"offset":            offset,
		"limit":             limit,
		"matchedTools":      total,
		"registeredTools":   registryCount,
		"activatedThisPage": len(matches),
		"hasMore":           hasMore,
		"results":           results,
	}
	if hasMore {
		response["nextOffset"] = offset + len(matches)
	}
	encoded, err := json.Marshal(response)
	if err != nil {
		return tools.Result{Output: "could not encode MCP search results: " + err.Error(), OK: false}
	}
	return tools.Result{Output: string(encoded), OK: true}
}

func mcpSearchInteger(args map[string]any, key string, fallback int) (int, error) {
	value, exists := args[key]
	if !exists || value == nil {
		return fallback, nil
	}
	switch number := value.(type) {
	case int:
		if number >= 0 {
			return number, nil
		}
	case int64:
		if number >= 0 && int64(int(number)) == number {
			return int(number), nil
		}
	case float64:
		converted := int(number)
		if number >= 0 && float64(converted) == number {
			return converted, nil
		}
	case json.Number:
		converted, err := number.Int64()
		if err == nil && converted >= 0 && int64(int(converted)) == converted {
			return int(converted), nil
		}
	}
	return 0, fmt.Errorf("MCP search %s must be a non-negative integer", key)
}

func mcpDescriptionPreview(description string) string {
	if utf8.RuneCountInString(description) <= maxMCPDescriptionPreview {
		return description
	}
	runes := []rune(description)
	return string(runes[:maxMCPDescriptionPreview]) + "…"
}
