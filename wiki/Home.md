# Agy project wiki

Agy is a lightweight, terminal-first AI coding agent written in Go. It provides an interactive TUI, local workspace tools, persistent conversations, and connections to hosted model providers.

Agy is an independent implementation of an Antigravity-compatible workflow. **It is not Google's Antigravity CLI and is not affiliated with Google.**

## Guides

- [Installation](Installation.md) — Linux installation, Bash completion, older x86_64 systems, and build policy.
- [Providers and authentication](Providers-and-Authentication.md) — provider differences, OAuth, API keys, environment variables, model selection, and quotas.
- [Getting started](Getting-Started.md) — roles, prompts, Web UI, web research, and basic agent usage.
- [TUI and workflows](TUI-and-Workflows.md) — keyboard controls, slash commands, scheduled tasks, teamwork, and workspace tools.
- [Configuration and permissions](Configuration-and-Permissions.md) — settings, headless mode, approval modes, and security boundaries.
- [Model Context Protocol](MCP.md) — MCP client and server setup.
- [Agent context architecture](Agent-Context-Architecture.md) — engineering design and implementation invariants.

## Features

- Static Linux x86_64 binary suitable for older machines
- Current Antigravity consumer Google OAuth
- Separate model catalogs for Antigravity, AI Studio text models, and Gemini Live
- Interactive terminal UI with slash-command typeahead
- Scrollable transcript viewport with PageUp/PageDown, Shift+Arrow, and mouse-wheel scrolling
- Streaming model responses without losing the prompt/footer
- Workspace file tools and shell tool with approval controls
- Persistent conversation history
- One-shot/headless mode
- User-local installation under `~/.local/bin`
- No root access required

## Relationship to Google's Antigravity CLI

Google's official project:

https://github.com/google-antigravity/antigravity-cli

Current Antigravity CLI documentation:

https://www.antigravity.google/docs/cli/reference/

https://www.antigravity.google/docs/cli/headless/

agy-open uses the current public Antigravity-facing authentication and transport behavior as a compatibility target, but it remains a separate implementation.

## License

See the repository's license information.
