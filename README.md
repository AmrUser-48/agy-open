# agy-open

An independent, open implementation of a terminal-first AI coding agent inspired by the public workflow of Google's Antigravity CLI (agy). It is not affiliated with or endorsed by Google.

## What it does

- `agy` launches an interactive terminal agent.
- `agy -p "..."` runs one prompt and exits for scripts/CI.
- Gemini API integration using `GEMINI_API_KEY`; model requests stay online and the recommended runtime is GitHub Codespaces, not the client machine.
- Workspace-aware tools: list files, read files, regex search, write files, and shell commands.
- Session history under `~/.agy/history/`.
- Basic slash commands and an explicit approval mode.
- GitHub Actions CI.

## Install

```bash
python -m venv .venv
source .venv/bin/activate
python -m pip install -e .
export GEMINI_API_KEY="your-key"
agy
```

For a safe first run, keep `approvalMode` as `ask`. Use `--dangerously-skip-permissions` only in an isolated environment.

## Example

```bash
agy -p "Inspect this repository and explain the three highest-priority engineering tasks."
```

## Configuration

Configuration is stored at `~/.config/agy/settings.json` and defaults to:

```json
{
  "modelProvider": "gemini",
  "model": "gemini-3.8-flash",
  "maxTurns": 12,
  "approvalMode": "ask",
  "theme": "default"
}
```

## Roadmap

The next layer is a richer TUI, parallel subagents, MCP server support, skills/hooks, structured tool calls, and stronger sandboxing. Those are planned extensions; this repository intentionally starts with a small, inspectable core.

## Online-first development

For older hardware, use GitHub Codespaces. See `docs/ONLINE_BUILD.md`. The local computer is only the browser/terminal client; the Codespace is the hosted development machine and Gemini is the hosted model service.
