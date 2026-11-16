package mcp

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/AmrUser-48/agy-open/internal/config"
)

const (
	CurrentProtocolVersion = "2026-07-28"
	LegacyProtocolVersion  = "2025-11-25"
	maxRPCLineBytes        = 16 << 20
)

type Tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	InputSchema map[string]any `json:"inputSchema"`
	Server      string         `json:"-"`
	RemoteName  string         `json:"-"`
}

type ServerStatus struct {
	Name      string
	Command   string
	Disabled  bool
	State     string
	Error     string
	ToolCount int
}

type Manager struct {
	root     string
	configs  []config.MCPServerConfig
	clients  map[string]*client
	statuses map[string]ServerStatus
	tools    map[string]Tool
	mu       sync.Mutex
}

func NewManager(root string, servers []config.MCPServerConfig) *Manager {
	return &Manager{
		root: root, configs: cloneServerConfigs(servers),
		clients: make(map[string]*client), statuses: make(map[string]ServerStatus),
		tools: make(map[string]Tool),
	}
}

func cloneServerConfigs(servers []config.MCPServerConfig) []config.MCPServerConfig {
	out := make([]config.MCPServerConfig, len(servers))
	for i, server := range servers {
		out[i] = server
		out[i].Args = append([]string(nil), server.Args...)
		if server.Env != nil {
			out[i].Env = make(map[string]string, len(server.Env))
			for key, value := range server.Env {
				out[i].Env[key] = value
			}
		}
	}
	return out
}

func (m *Manager) HasEnabled() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, server := range m.configs {
		if !server.Disabled {
			return true
		}
	}
	return false
}

func (m *Manager) SetServers(servers []config.MCPServerConfig) error {
	if err := config.ValidateMCPServers(servers); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, process := range m.clients {
		_ = process.Close()
	}
	m.configs = cloneServerConfigs(servers)
	m.clients = make(map[string]*client)
	m.statuses = make(map[string]ServerStatus)
	m.tools = make(map[string]Tool)
	return nil
}

func (m *Manager) Refresh(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := config.ValidateMCPServers(m.configs); err != nil {
		return err
	}

	m.tools = make(map[string]Tool)
	var failures []error
	for _, server := range m.configs {
		status := ServerStatus{Name: server.Name, Command: server.Command, Disabled: server.Disabled}
		if server.Disabled {
			status.State = "disabled"
			m.statuses[server.Name] = status
			if old := m.clients[server.Name]; old != nil {
				_ = old.Close()
				delete(m.clients, server.Name)
			}
			continue
		}

		process := m.clients[server.Name]
		if process == nil {
			var err error
			process, err = startClient(server, m.root)
			if err != nil {
				status.State, status.Error = "error", err.Error()
				m.statuses[server.Name] = status
				failures = append(failures, fmt.Errorf("%s: %w", server.Name, err))
				continue
			}
			m.clients[server.Name] = process
		}

		tools, err := process.ListTools(ctx)
		if err != nil {
			status.State, status.Error = "error", err.Error()
			m.statuses[server.Name] = status
			failures = append(failures, fmt.Errorf("%s: %w", server.Name, err))
			_ = process.Close()
			delete(m.clients, server.Name)
			continue
		}

		status.State, status.ToolCount = "connected", len(tools)
		m.statuses[server.Name] = status
		for _, remote := range tools {
			name := qualifiedToolName(server.Name, remote.Name)
			if _, exists := m.tools[name]; exists {
				sum := sha256.Sum256([]byte(server.Name + "\x00" + remote.Name))
				name = name[:minInt(len(name), 54)] + "_" + hex.EncodeToString(sum[:4])
			}
			remote.Name = name
			remote.Server = server.Name
			m.tools[name] = remote
		}
	}
	return errors.Join(failures...)
}

func (m *Manager) Tools() []Tool {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Tool, 0, len(m.tools))
	for _, tool := range m.tools {
		copyTool := tool
		copyTool.InputSchema = cloneMap(tool.InputSchema)
		out = append(out, copyTool)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func (m *Manager) Statuses() []ServerStatus {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]ServerStatus, 0, len(m.configs))
	for _, server := range m.configs {
		status, ok := m.statuses[server.Name]
		if !ok {
			status = ServerStatus{Name: server.Name, Command: server.Command, Disabled: server.Disabled, State: "not connected"}
			if server.Disabled {
				status.State = "disabled"
			}
		}
		out = append(out, status)
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out
}

func (m *Manager) Call(ctx context.Context, qualifiedName string, arguments map[string]any) (string, error) {
	m.mu.Lock()
	tool, ok := m.tools[qualifiedName]
	process := m.clients[tool.Server]
	m.mu.Unlock()
	if !ok || process == nil {
		return "", fmt.Errorf("MCP tool %q is not connected; run /mcp connect", qualifiedName)
	}
	return process.CallTool(ctx, tool.RemoteName, arguments)
}

func (m *Manager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, process := range m.clients {
		_ = process.Close()
	}
	m.clients = make(map[string]*client)
	m.tools = make(map[string]Tool)
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

func (e *rpcError) Error() string {
	if e == nil {
		return ""
	}
	return fmt.Sprintf("MCP JSON-RPC error %d: %s", e.Code, e.Message)
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *rpcError       `json:"error,omitempty"`
}

type pendingResponse struct {
	result json.RawMessage
	err    *rpcError
}

type client struct {
	name     string
	cmd      *exec.Cmd
	stdin    io.WriteCloser
	done     chan struct{}
	mu       sync.Mutex
	writeMu  sync.Mutex
	pending  map[string]chan pendingResponse
	nextID   atomic.Uint64
	stopOnce sync.Once
	stopped  error
	protocol string
	tools    []Tool
	loaded   bool
}

func startClient(server config.MCPServerConfig, root string) (*client, error) {
	cmd := exec.Command(server.Command, server.Args...)
	if root != "" {
		cmd.Dir = root
	}
	cmd.Env = mergeEnvironment(os.Environ(), server.Env)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("open stdout: %w", err)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("open stdin: %w", err)
	}
	// MCP stdio reserves stdout for JSON-RPC. Server diagnostics belong on stderr.
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start %q: %w", server.Command, err)
	}
	c := &client{
		name: server.Name, cmd: cmd, stdin: stdin, done: make(chan struct{}),
		pending: make(map[string]chan pendingResponse),
	}
	go c.readLoop(stdout)
	go func() {
		err := cmd.Wait()
		if err == nil {
			err = errors.New("server process exited")
		}
		c.fail(fmt.Errorf("MCP server %q: %w", server.Name, err))
	}()
	return c, nil
}

func mergeEnvironment(base []string, overrides map[string]string) []string {
	values := make(map[string]string, len(base)+len(overrides))
	for _, entry := range base {
		key, value, ok := strings.Cut(entry, "=")
		if ok {
			values[key] = value
		}
	}
	for key, value := range overrides {
		values[key] = value
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, key := range keys {
		out = append(out, key+"="+values[key])
	}
	return out
}

func (c *client) fail(err error) {
	c.stopOnce.Do(func() {
		c.mu.Lock()
		c.stopped = err
		for id, ch := range c.pending {
			ch <- pendingResponse{err: &rpcError{Code: -32000, Message: err.Error()}}
			delete(c.pending, id)
		}
		close(c.done)
		c.mu.Unlock()
	})
}

func (c *client) readLoop(reader io.Reader) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), maxRPCLineBytes)
	for scanner.Scan() {
		var response rpcResponse
		if err := json.Unmarshal(scanner.Bytes(), &response); err != nil {
			c.fail(fmt.Errorf("invalid JSON-RPC response from %s: %w", c.name, err))
			return
		}
		id := rpcIDKey(response.ID)
		if id == "" {
			// Server notifications are valid on stdio; this client does not
			// currently subscribe to notifications, so they are ignored.
			continue
		}
		c.mu.Lock()
		ch := c.pending[id]
		delete(c.pending, id)
		c.mu.Unlock()
		if ch != nil {
			ch <- pendingResponse{result: response.Result, err: response.Error}
		}
	}
	err := scanner.Err()
	if err == nil {
		err = io.EOF
	}
	c.fail(fmt.Errorf("MCP server %q closed stdout: %w", c.name, err))
}

func rpcIDKey(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text
	}
	return strings.TrimSpace(string(raw))
}

func (c *client) request(ctx context.Context, method string, params map[string]any) (json.RawMessage, error) {
	id := c.nextID.Add(1)
	key := strconv.FormatUint(id, 10)
	responseCh := make(chan pendingResponse, 1)
	c.mu.Lock()
	select {
	case <-c.done:
		err := c.stopped
		c.mu.Unlock()
		if err == nil {
			err = errors.New("MCP server is not running")
		}
		return nil, err
	default:
	}
	c.pending[key] = responseCh
	c.mu.Unlock()

	payload, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	if err != nil {
		c.removePending(key)
		return nil, err
	}
	c.writeMu.Lock()
	_, writeErr := c.stdin.Write(append(payload, '\n'))
	c.writeMu.Unlock()
	if writeErr != nil {
		c.removePending(key)
		return nil, fmt.Errorf("write MCP request %s: %w", method, writeErr)
	}

	select {
	case response := <-responseCh:
		if response.err != nil {
			return nil, response.err
		}
		if len(response.result) == 0 {
			return json.RawMessage(`{}`), nil
		}
		return response.result, nil
	case <-ctx.Done():
		c.removePending(key)
		return nil, ctx.Err()
	case <-c.done:
		select {
		case response := <-responseCh:
			if response.err != nil {
				return nil, response.err
			}
			return response.result, nil
		default:
			c.mu.Lock()
			err := c.stopped
			c.mu.Unlock()
			if err == nil {
				err = errors.New("MCP server stopped")
			}
			return nil, err
		}
	}
}

func (c *client) removePending(id string) {
	c.mu.Lock()
	delete(c.pending, id)
	c.mu.Unlock()
}

func (c *client) notification(method string, params map[string]any) error {
	payload, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
	if err != nil {
		return err
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	_, err = c.stdin.Write(append(payload, '\n'))
	return err
}

func requestMeta() map[string]any {
	return map[string]any{
		"io.modelcontextprotocol/protocolVersion": CurrentProtocolVersion,
		"io.modelcontextprotocol/clientCapabilities": map[string]any{},
		"io.modelcontextprotocol/clientInfo": map[string]any{"name": "agy-open", "version": "0.8.7"},
	}
}

func (c *client) discoverOrInitialize(ctx context.Context) error {
	result, discoverErr := c.request(ctx, "server/discover", map[string]any{"_meta": requestMeta()})
	if discoverErr == nil {
		var discovery struct {
			SupportedVersions []string `json:"supportedVersions"`
		}
		if err := json.Unmarshal(result, &discovery); err == nil {
			for _, version := range discovery.SupportedVersions {
				if version == CurrentProtocolVersion {
					c.protocol = CurrentProtocolVersion
					return nil
				}
			}
			for _, supported := range discovery.SupportedVersions {
				for _, legacy := range legacyFallbackVersions {
					if supported == legacy {
						if err := c.initializeLegacy(ctx, supported); err == nil {
							return nil
						}
					}
				}
			}
		}
	}

	// Servers predating server/discover negotiate with initialize. Probe newest
	// to oldest so implementations that only recognize an older revision work.
	var failures []error
	if discoverErr != nil {
		failures = append(failures, fmt.Errorf("server/discover: %w", discoverErr))
	}
	for _, version := range legacyFallbackVersions {
		if err := c.initializeLegacy(ctx, version); err == nil {
			return nil
		} else {
			failures = append(failures, fmt.Errorf("initialize %s: %w", version, err))
		}
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	return fmt.Errorf("MCP discovery failed: %w", errors.Join(failures...))
}

var legacyFallbackVersions = []string{
	"2025-11-25",
	"2025-06-18",
	"2025-03-26",
	"2024-11-05",
}

func (c *client) initializeLegacy(ctx context.Context, version string) error {
	result, err := c.request(ctx, "initialize", map[string]any{
		"protocolVersion": version,
		"capabilities": map[string]any{},
		"clientInfo": map[string]any{"name": "agy-open", "version": "0.8.7"},
	})
	if err != nil {
		return fmt.Errorf("MCP discovery and legacy initialization failed: %w", err)
	}
	var initialized struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	if err := json.Unmarshal(result, &initialized); err != nil {
		return fmt.Errorf("decode MCP initialize response: %w", err)
	}
	if initialized.ProtocolVersion == "" {
		return errors.New("MCP initialize response omitted protocolVersion")
	}
	c.protocol = initialized.ProtocolVersion
	if err := c.notification("notifications/initialized", map[string]any{}); err != nil {
		return fmt.Errorf("send MCP initialized notification: %w", err)
	}
	return nil
}

func (c *client) ListTools(ctx context.Context) ([]Tool, error) {
	select {
	case <-c.done:
		c.mu.Lock()
		err := c.stopped
		c.mu.Unlock()
		if err == nil {
			err = errors.New("MCP server process exited")
		}
		return nil, err
	default:
	}
	if !c.loaded {
		if err := c.discoverOrInitialize(ctx); err != nil {
			return nil, err
		}
		tools, err := c.listTools(ctx)
		if err != nil {
			return nil, err
		}
		c.tools = tools
		c.loaded = true
	}
	out := make([]Tool, len(c.tools))
	copy(out, c.tools)
	return out, nil
}

func (c *client) listTools(ctx context.Context) ([]Tool, error) {
	var tools []Tool
	cursor := ""
	for page := 0; page < 100; page++ {
		params := map[string]any{}
		if cursor != "" {
			params["cursor"] = cursor
		}
		if c.protocol == CurrentProtocolVersion {
			params["_meta"] = requestMeta()
		}
		raw, err := c.request(ctx, "tools/list", params)
		if err != nil {
			return nil, fmt.Errorf("MCP tools/list: %w", err)
		}
		var result struct {
			Tools []struct {
				Name        string         `json:"name"`
				Description string         `json:"description"`
				InputSchema map[string]any `json:"inputSchema"`
			} `json:"tools"`
			NextCursor string `json:"nextCursor"`
		}
		if err := json.Unmarshal(raw, &result); err != nil {
			return nil, fmt.Errorf("decode MCP tools/list: %w", err)
		}
		for _, item := range result.Tools {
			if item.Name == "" || len(item.Name) > 256 {
				continue
			}
			schema := item.InputSchema
			if schema == nil {
				schema = map[string]any{"type": "object", "properties": map[string]any{}}
			}
			encoded, _ := json.Marshal(schema)
			if len(encoded) > 64<<10 {
				continue
			}
			tools = append(tools, Tool{
				Name: item.Name, RemoteName: item.Name, Description: item.Description,
				InputSchema: schema,
			})
		}
		if result.NextCursor == "" {
			return tools, nil
		}
		cursor = result.NextCursor
	}
	return nil, errors.New("MCP tools/list exceeded 100 pages")
}

func (c *client) CallTool(ctx context.Context, name string, arguments map[string]any) (string, error) {
	if arguments == nil {
		arguments = map[string]any{}
	}
	params := map[string]any{"name": name, "arguments": arguments}
	if c.protocol == CurrentProtocolVersion {
		params["_meta"] = requestMeta()
	}
	raw, err := c.request(ctx, "tools/call", params)
	if err != nil {
		return "", fmt.Errorf("MCP tools/call %s: %w", name, err)
	}
	var result struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		StructuredContent any  `json:"structuredContent"`
		IsError            bool `json:"isError"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return "", fmt.Errorf("decode MCP tools/call result: %w", err)
	}
	var output []string
	for _, block := range result.Content {
		if block.Type == "text" && block.Text != "" {
			output = append(output, block.Text)
		}
	}
	if len(output) == 0 && result.StructuredContent != nil {
		encoded, _ := json.Marshal(result.StructuredContent)
		output = append(output, string(encoded))
	}
	text := strings.Join(output, "\n")
	if text == "" {
		text = "(MCP tool returned no text content)"
	}
	if result.IsError {
		return text, errors.New(text)
	}
	return text, nil
}

func (c *client) Close() error {
	if c == nil {
		return nil
	}
	_ = c.stdin.Close()
	if c.cmd != nil && c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
	}
	select {
	case <-c.done:
		return nil
	case <-time.After(2 * time.Second):
		return fmt.Errorf("timed out stopping MCP server %q", c.name)
	}
}

func qualifiedToolName(server, tool string) string {
	serverPart := sanitizeToolPart(server)
	toolPart := sanitizeToolPart(tool)
	name := "mcp__" + serverPart + "__" + toolPart
	if serverPart != server || toolPart != tool || len(name) > 64 {
		sum := sha256.Sum256([]byte(server + "\x00" + tool))
		suffix := "_" + hex.EncodeToString(sum[:4])
		if len(name)+len(suffix) > 64 {
			name = name[:64-len(suffix)]
		}
		name += suffix
	}
	if len(name) > 64 {
		name = name[:64]
	}
	return name
}

func sanitizeToolPart(value string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(value) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return "tool"
	}
	return b.String()
}

func cloneMap(input map[string]any) map[string]any {
	if input == nil {
		return nil
	}
	out := make(map[string]any, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
