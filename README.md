# agy-open

An independent Go implementation of a terminal-first AI coding agent inspired by the public workflow of Google's Antigravity CLI (`agy`). It is not affiliated with Google.

## Online-first design

The recommended environment is GitHub Codespaces. The local computer is only the client; Go, the repository, tests, and agent runtime execute remotely, while Gemini is called through its hosted API.

`text
D630 / browser or SSH
        |
        v
GitHub Codespaces
        |
        +--> agy (Go)
        |
        +--> Gemini API
        |
        +--> Git + GitHub Actions
`

## Quick start

In Codespaces:

`text
go test ./...
export GEMINI_API_KEY="..."
go run ./cmd/agy
`

Or install:

`text
go install github.com/AmrUser-48/agy-open/cmd/agy@main
agy -p "Inspect this repository and explain the highest-priority engineering tasks."
`

Interactive commands include `/help`, `/model <name>`, `/ask`, `/approve`, `/clear`, and `/quit`.

By default, writes and shell commands return an approval-required result. In an isolated remote workspace, `--dangerously-skip-permissions` enables automatic execution.

## Configuration

Configuration lives at `~/.config/agy/settings.json` or `$XDG_CONFIG_HOME/agy/settings.json`. The default model is `gemini-3.8-flash`.

## Roadmap

Next: richer TUI, subagents, MCP, skills/hooks, Git-aware operations, streaming, resumable sessions, and stronger sandboxing.
