# Model Context Protocol (MCP)

agy-open supports MCP over local stdio in both directions. No browser extension, web dashboard, Node runtime inside agy-open, or HTTP listener is required.

## MCP client

Configure a server with an executable and argument vector. The command is started directly (not through a shell), inherits the current environment, and communicates through newline-delimited JSON-RPC on stdin/stdout.

For GitHub, build the official local MCP server once:

```sh
git clone https://github.com/github/github-mcp-server
cd github-mcp-server
go build -o github-mcp-server ./cmd/github-mcp-server
```

Then configure its absolute path:

```sh
export GITHUB_PERSONAL_ACCESS_TOKEN=...
agy mcp add github /absolute/path/github-mcp-server stdio
```

The child process inherits exported environment variables. Optional explicit environment overrides can be stored in `~/.config/agy-open/settings.json`; that file is mode 0600.

TUI commands:

- `/mcp list` — show configured servers and connection status.
- `/mcp add <name> <command> [args...]` — add a stdio server.
- `/mcp enable <name>` / `/mcp disable <name>` — toggle a server.
- `/mcp remove <name>` — remove a server.
- `/mcp connect` — start enabled servers and discover tools.
- `/mcp tools [server]` — list discovered tools.

The command-line equivalents are `agy mcp list`, `agy mcp add`, `agy mcp enable`, `agy mcp disable`, and `agy mcp remove`. Enabled servers are connected on the next agent prompt; `/mcp connect` connects them immediately.

Discovered tool names are namespaced as `mcp__<server>__<tool>`. MCP calls are treated as mutating operations and pass through Agy's permission mode and role allowlist. They are not exposed in the read-only mode. The default `default` role permits them; restricted roles must explicitly include `mcp` in `allowedTools`.

## MCP server

Run agy-open as an MCP stdio server for another MCP client:

```sh
agy mcp serve
```

The server exposes Agy's native workspace tools. It defaults to read-only because stdio servers have no interactive approval prompt. Use `agy mcp serve --write` only when the connecting client is trusted; this grants workspace-write access. The server writes protocol messages only to stdout and leaves diagnostics on stderr.

The implementation supports the current stateless MCP protocol (2026-07-28) and the legacy initialize handshake (2025-11-25 / 2025-06-18 / 2025-03-26). The stdio transport is intentionally used for the first implementation; remote HTTP transports and MCP resources/prompts are not implemented.
