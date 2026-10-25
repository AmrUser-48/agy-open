# agy-open

> A lightweight terminal AI coding agent in Go for older Linux systems.

agy-open is an independent Go implementation of a terminal-first Antigravity-compatible workflow. It keeps the local client small enough for older x86_64 Linux systems while sending model inference to Google's hosted Antigravity service.

**agy-open is not Google's Antigravity CLI and is not affiliated with Google.**

## Features

- Static Linux x86_64 binary suitable for older machines
- Current Antigravity consumer Google OAuth
- Antigravity-compatible model discovery and `v1internal` transport
- Interactive terminal UI with slash-command typeahead
- Scrollable transcript viewport with PageUp/PageDown, Shift+Arrow, and mouse-wheel scrolling
- Streaming model responses without losing the prompt/footer
- Workspace file tools and shell tool with approval controls
- Persistent conversation history
- One-shot/headless mode
- User-local installation under `~/.local/bin`
- No root access required

## Installation

### Linux x86_64 — recommended

Install the latest release directly into `~/.local/bin`:

```bash
curl -fsSL https://raw.githubusercontent.com/AmrUser-48/agy-open/main/install.sh | sh
```

The installer:

- downloads the latest `agy-open` release
- installs it as `~/.local/bin/agy`
- does not use `sudo`
- does not require Go, Python, Node.js, Docker, or a package manager

If `~/.local/bin` is not already on your PATH:

```bash
export PATH="$HOME/.local/bin:$PATH"
```

For a permanent Bash setup:

```bash
echo 'export PATH="$HOME/.local/bin:$PATH"' >> ~/.bashrc
source ~/.bashrc
```

Verify:

```bash
agy --version
```

### Build from source

```bash
git clone https://github.com/AmrUser-48/agy-open.git
cd agy-open
go test ./...
go vet ./...
mkdir -p ~/.local/bin
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GOAMD64=v1 \
  go build -trimpath -ldflags="-s -w" -o ~/.local/bin/agy ./cmd/agy
```

`GOAMD64=v1` is intentional for older x86_64 CPUs.

## Authentication

Sign in with your Google account:

```bash
agy login
```

agy-open uses the current Antigravity consumer OAuth flow and stores credentials at:

```text
~/.gemini/antigravity-cli/antigravity-oauth-token
```

No Google Cloud project provisioning, `client_secret.json`, gcloud setup, Python, or Node.js setup is required for the consumer flow.

For SSH sessions, `agy login` prints the authorization URL and accepts the authorization code from the Antigravity callback page.

To replace credentials created by an older agy-open build:

```bash
agy logout
agy login
```

## Quick start

Start the interactive TUI:

```bash
agy
```

Run one prompt and exit:

```bash
agy -p "Inspect this repository and explain the main problem."
```

Choose a model:

```bash
agy --model gemini-3.8-flash-medium
```

Choose reasoning effort:

```bash
agy --effort high
```

Use a different workspace:

```bash
agy --workspace /path/to/project
```

## TUI controls

The transcript is a real visual viewport rather than a list of whole messages. Long model responses can be scrolled without losing the fixed prompt area.

```text
PageUp / PageDown       scroll the transcript by a page
Shift+Up / Shift+Down   scroll the transcript
Mouse wheel             scroll the transcript
Ctrl+L                  return to the newest output
Ctrl+O                  toggle detailed tool trajectory
Ctrl+C                  cancel an active request; press again to exit
Ctrl+D                  forward-delete; exits on an empty prompt
Ctrl+G                  open the prompt in $EDITOR
Ctrl+R                  open the diff view
Shift+Tab               cycle permission mode
Tab                     autocomplete
```

When the viewport is scrolled, the footer shows the current output position. New streaming text no longer destroys the user's scroll position.

## Antigravity slash commands

The command menu follows Google's current Antigravity CLI command naming and aliases.

| Command | Purpose |
| --- | --- |
| `/add-dir <path>` | Add a directory to the workspace |
| `/agents` | Agent manager |
| `/artifact` | Artifact review |
| `/boost <task>` | Deep-reasoning task |
| `/btw <query>` | Side question |
| `/clear` / `/new` | Clear conversation |
| `/config` / `/settings` | Settings |
| `/context` | Context information |
| `/copy` | Copy the last response |
| `/credits` | Account credits |
| `/diff` | Working-tree diff |
| `/effort <level>` | Set low/medium/high reasoning effort |
| `/exit` / `/quit` | Exit |
| `/fork` | Fork the conversation |
| `/help` | Help |
| `/hooks` | Hook files |
| `/keybindings` | Keyboard shortcuts |
| `/logout` | Log out |
| `/mcp` | MCP manager |
| `/model [name]` | Select or change model |
| `/open <path>` | Open a file in the external editor |
| `/permissions` | Permission manager |
| `/planning` | High-effort planning mode |
| `/plugin` / `/plugins` | Plugin manager |
| `/remote-control` | Remote-control status |
| `/rename <name>` | Rename the conversation |
| `/resume` / `/switch` / `/conversation` | Resume a conversation |
| `/rewind` / `/undo` | Rewind one turn |
| `/skills` | Skills browser |
| `/statusline` | Status-line settings |
| `/tasks` | Task activity |
| `/teamwork-preview` / `/teamwork` | Team task entry point |
| `/title [on/off]` | Terminal title |
| `/usage` / `/quota` | Usage/quota information |
| `/voice` / `/record` | Voice entry point |

Some newer Antigravity surfaces such as browser research, scheduled tasks, and the full plugin/MCP managers require services that are not yet implemented locally in agy-open. Those command names remain available for completion, but agy-open reports when the local capability is unavailable rather than pretending it ran.

## Workspace tools

The agent currently exposes:

```text
list_files
read_file
search
write_file
edit_file
shell
```

File access is restricted to the selected workspace root unless explicitly configured otherwise.

## Configuration

User settings are stored in agy-open's own configuration directory:

```text
${XDG_CONFIG_HOME:-~/.config}/agy-open/settings.json
```

The repository includes [`settings.example.json`](settings.example.json), with the available agy-open settings, a configurable `systemPrompt`, and the Gemini safety settings. From the cloned repository, install the example only if you do not already have a config file:

```sh
mkdir -p "${XDG_CONFIG_HOME:-$HOME/.config}/agy-open"
test -e "${XDG_CONFIG_HOME:-$HOME/.config}/agy-open/settings.json" || cp settings.example.json "${XDG_CONFIG_HOME:-$HOME/.config}/agy-open/settings.json"
```

Edit `~/.config/agy-open/settings.json` (or the corresponding `XDG_CONFIG_HOME` path). Keep your existing settings when making changes. The `systemPrompt` value replaces the built-in system instructions entirely; setting it to an empty string sends an empty system instruction.

### Agent execution budgets

Agent runs no longer stop after the old 12-iteration default. The default budgets are:

- `maxTurns`: 100 model iterations per request. Set to `0` to disable this cap.
- `maxToolCalls`: 200 individual tool calls per request. Set to `0` to disable this cap.
- `maxRunSeconds`: 1800 seconds per request. Set to `0` to disable the agent-managed time cap.

These are independent safeguards. Disabling one does not disable the others. Headless runs also use `--print-timeout`, which defaults to 35 minutes and can be set per invocation. If a budget is reached, agy-open reports which limit stopped the run; changes already made in the workspace are retained. Existing settings that contain the shipped `maxTurns: 12` default and neither of the newer budget keys are automatically migrated to the new defaults, preserving the rest of the user's settings.

The `safetySettings` example uses `BLOCK_NONE` for harassment, hate speech, sexually explicit content, and dangerous content. These thresholds affect only configurable Gemini content filters; they do not disable built-in protections for core harms. The public Gemini API documents these settings, but the consumer Antigravity endpoint may ignore or reject them. agy-open sends them only for Gemini models.

For compatibility, agy-open reads older settings from `~/.config/agy/settings.json` and from the settings path used by versions prior to 0.6.5, but new settings are saved only under agy-open's own directory.

## Headless mode

```bash
agy -p "Review the project and list the three highest priority issues."
```

Supported output formats:

```bash
agy -p "..." --output-format text
agy -p "..." --output-format json
agy -p "..." --output-format stream-json
```

## Permissions

Default mode requires approval for risky operations.

Use:

```text
/permissions
```

or:

```text
/ask
```

For a trusted workspace where every tool call should proceed automatically:

```bash
agy --dangerously-skip-permissions
```

## Debian 12 / older x86_64

The local process handles terminal I/O, workspace operations, OAuth, and HTTPS transport. Model inference is remote.

A release binary does not require:

- a GPU
- a local language model
- Python
- Node.js
- Docker
- root privileges

## Relationship to Google's Antigravity CLI

Google's official project:

https://github.com/google-antigravity/antigravity-cli

Current Antigravity CLI documentation:

https://www.antigravity.google/docs/cli/reference/

https://www.antigravity.google/docs/cli/headless/

agy-open uses the current public Antigravity-facing authentication and transport behavior as a compatibility target, but it remains a separate implementation.

## License

See the repository license.
