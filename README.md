# agy-open

> A lightweight terminal AI coding agent in Go for older Linux systems.

agy-open is an independent project inspired by the terminal-first workflow of Google's Antigravity CLI. It is designed to run on older x86_64 machines such as a Core 2 Duo T7250 while sending model inference to a hosted Gemini service.

**agy-open is not Google's Antigravity CLI and is not affiliated with Google.**

## Features

- Lightweight native Go binary
- Linux x86_64 / Debian 12 friendly
- Remote Gemini model inference
- Interactive terminal agent
- One-shot/headless prompts
- Workspace file tools
- Search and shell tools
- Approval mode for risky operations
- Persistent session history
- Google OAuth and Gemini API-key authentication
- No Go, Python, Node.js or Docker required on the target machine when using a release binary

## Installation

### Linux x86_64

Download the latest release binary from:

https://github.com/AmrUser-48/agy-open/releases

Then:

```bash
chmod +x agy
mkdir -p ~/.local/bin
mv agy ~/.local/bin/
agy --help
```

The release binary is intended to run directly on Debian 12 and similar Linux x86_64 systems.

### Bash completion

The repository includes a Bash completion script at `completions/agy.bash`.

For a system-wide installation on Debian/Ubuntu:

```bash
sudo install -D -m 0644 completions/agy.bash /usr/share/bash-completion/completions/agy
```

Then start a new Bash session, or load it immediately:

```bash
source /usr/share/bash-completion/completions/agy
```

It completes CLI subcommands, options, effort/output/input formats, filesystem paths, and model/agent values by querying `agy models` and `agy agents`.

For a user-local install:

```bash
mkdir -p ~/.bash_completion.d
cp completions/agy.bash ~/.bash_completion.d/agy
echo 'source ~/.bash_completion.d/agy' >> ~/.bashrc
source ~/.bashrc
```

### Build from source

```bash
git clone https://github.com/AmrUser-48/agy-open.git
cd agy-open
go test ./...
go vet ./...
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o agy ./cmd/agy
```

## Quick start

Start the agent:

```bash
agy
```

Ask a single question:

```bash
agy -p "Explain this repository."
```

Choose a model:

```bash
agy --model gemini-3.8-flash
```

Use a different workspace:

```bash
agy --workspace /path/to/project
```

## Authentication

agy-open supports Google OAuth and Gemini API keys.

### Google account

Sign in with your Google account:

```bash
agy login
```

The release binary opens the Google authorization page in your browser and stores the resulting refresh token locally. No `client_secret.json`, Google Cloud CLI, Python, or Node.js setup is required on the target machine.

The release build uses a Google-published installed-app OAuth client; the client credentials are injected during the GitHub release build and are not stored in this repository.

For SSH/headless sessions, use API-key authentication for now.

### Gemini API key

Set an API key in your environment:

```bash
export GEMINI_API_KEY="your-key"
```

Then run:

```bash
agy
```

## Using agy

### Interactive mode

```text
$ agy
agy-open — terminal-first AI coding agent

you › inspect this project and explain what is broken
```

### Headless mode

```bash
agy -p "Review this repository and list the most important issues."
```

### Approval mode

By default, potentially destructive actions such as file writes and shell commands require approval.

Use:

```text
/ask
```

to return to approval mode, or:

```text
/approve
```

to allow those actions during the current session.

For a trusted, isolated workspace:

```bash
agy --dangerously-skip-permissions
```

Use automatic execution only when you understand the workspace and command trust boundary.

## Commands

| Command | Description |
| --- | --- |
| `/help` | Show available commands |
| `/model <name>` | Change model |
| `/ask` | Require approval |
| `/approve` | Allow writes and shell for the session |
| `/clear` | Clear conversation context |
| `/quit` | Exit |

## Workspace tools

The current agent can use:

```text
list_files
read_file
search
write_file
shell
```

File operations are restricted to the selected workspace root.

## Configuration

Settings are stored under:

```text
~/.config/agy/
```

or the directory selected by `XDG_CONFIG_HOME`.

Session history and OAuth credentials are kept outside the repository.

## Debian 12 / T7250

agy-open is intentionally small enough for older x86_64 hardware.

The local machine handles terminal I/O, workspace operations and HTTPS requests. Model inference is performed remotely.

A release binary does not require:

- a GPU
- a local language model
- Python
- Node.js
- Docker

## SSH and headless use

agy-open can run over SSH, but browser authentication is easiest when the browser can reach the same machine that is running the OAuth callback.

API-key authentication is available for headless environments.

## Security

agy-open can read files, modify files and execute shell commands inside its workspace. Review agent actions before enabling automatic execution.

Keep credentials out of the repository. Local OAuth and token files should never be committed.

AI coding agents can introduce risks such as prompt injection, unintended command execution and data exposure. Use the agent with the same caution you would use for any tool that can modify a working tree.

## Project status

Current focus:

- lightweight Go CLI
- remote Gemini inference
- Linux x86_64 support
- workspace tools
- approvals
- authentication
- GitHub Actions builds and releases

Planned improvements:

- streaming responses
- richer terminal UI
- Git-aware tools
- MCP support
- better SSH authentication
- stronger sandboxing
- resumable sessions

## Relationship to Google's Antigravity CLI

Google's official project:

https://github.com/google-antigravity/antigravity-cli

Its README provides the general structure and user-facing style that influenced this project.

agy-open is a separate implementation intended for systems where the official CLI is not practical.

## License

See the repository license.
